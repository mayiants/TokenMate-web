package server

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/ai-proxy/ai-proxy-go/pkg/billing"
	"github.com/ai-proxy/ai-proxy-go/pkg/geoip"
	"github.com/ai-proxy/ai-proxy-go/pkg/metrics"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Shared guards for the media (image/video) endpoints.

const (
	// maxImagesPerRequest caps the client-controlled image count. It matches
	// the OpenAI images API maximum (n <= 10) and bounds the billing
	// multiplier so an inflated n cannot multiply the charged amount.
	maxImagesPerRequest = 10

	// maxVideoDurationSeconds caps the client-controlled video duration to
	// the longest commonly supported generation (Veo-class models: 120s),
	// bounding per-second media billing.
	maxVideoDurationSeconds = 120

	// maxMediaResponseBody bounds upstream media payloads read into memory.
	// Generated videos can reach 100MB+; 512MiB leaves headroom on the proxy
	// host while still failing runaway responses instead of OOMing it.
	maxMediaResponseBody = 512 << 20 // 512 MiB

	// mediaTaskBillingTTL bounds how long async task billing metadata is
	// retained. Video generation tasks can run for hours; 48h covers the
	// upstream task lifecycle without leaking Redis memory.
	mediaTaskBillingTTL = 48 * time.Hour
)

// validTaskIDRE restricts client-supplied async task IDs before they are
// concatenated into the upstream request URL. The poll endpoints fire the
// request with upstream provider credentials, so a task_id must not be able
// to inject path segments or query strings.
var validTaskIDRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func mediaTaskBillingKey(taskID string) string { return "media:task:billing:" + taskID }
func mediaTaskBilledKey(taskID string) string  { return "media:task:billed:" + taskID }

// mediaTaskBilling is the billing context captured at async task dispatch.
// It lets the poll endpoint attribute and bill the task when it reaches a
// terminal state, long after the dispatch request has finished.
type mediaTaskBilling struct {
	TraceID       string  `json:"traceId"`
	KeyHash       string  `json:"keyHash"`
	UpstreamKeyID string  `json:"upstreamKeyId"`
	Model         string  `json:"model"`
	Provider      string  `json:"provider"`
	Region        string  `json:"region"`
	Cost          float64 `json:"cost"`
	PricingRatio  float64 `json:"pricingRatio"`
	GroupID       string  `json:"groupId,omitempty"`
	ActualGroupID string  `json:"actualGroupId,omitempty"`
	IsFallback    bool    `json:"isFallback,omitempty"`
	RoutingTrack  string  `json:"routingTrack,omitempty"`
	OriginalTrack string  `json:"originalTrack,omitempty"`
	Endpoint      string  `json:"endpoint,omitempty"`
	ClientIP      string  `json:"clientIp,omitempty"`
	UserAgent     string  `json:"userAgent,omitempty"`

	// Media generation metadata for Portal media-mode billing.
	ImageSize    string `json:"imageSize,omitempty"`
	ImageQuality string `json:"imageQuality,omitempty"`
	ImageCount   int    `json:"imageCount,omitempty"`
	Duration     int    `json:"duration,omitempty"`

	// ChargeOnDispatch marks tasks of providers that bill at task creation
	// regardless of outcome (see BillingConfig.AsyncChargeOnDispatchProviders).
	// The dispatch handler bills these immediately; the poll endpoint only
	// emits a refund on terminal failure and never bills again on success.
	ChargeOnDispatch bool `json:"chargeOnDispatch,omitempty"`
}

// asyncChargeOnDispatch reports whether the provider bills async media tasks
// at dispatch time. For those providers billing must mirror upstream
// semantics (charge on dispatch, refund on terminal failure); all other
// providers keep deferred bill-on-terminal-success.
func (s *HandlerService) asyncChargeOnDispatch(provider string) bool {
	if s == nil || s.cfg == nil {
		return false
	}
	for _, p := range s.cfg.Billing.AsyncChargeOnDispatchProviders {
		if p == provider {
			return true
		}
	}
	return false
}

// mediaAsyncBillingEnabled reports whether async task billing can be
// deferred to the poll endpoint. Without Redis the dispatch-time billing
// path is kept (a failed task is then charged, but never unbillable).
func (s *HandlerService) mediaAsyncBillingEnabled() bool {
	return s.redisClient != nil
}

// storeMediaTaskBilling persists the billing context of an async media task
// at dispatch time. By default billing is deferred until the poll sees a
// terminal success state, so a task that fails upstream is never charged.
// For charge-on-dispatch providers (chargeOnDispatch=true) the dispatch
// handler bills immediately and the poll endpoint refunds on terminal
// failure instead.
func (s *HandlerService) storeMediaTaskBilling(c *gin.Context, taskID, upstreamKeyID, model, provider string, cost float64, chargeOnDispatch bool) {
	if !s.mediaAsyncBillingEnabled() {
		return
	}

	traceID := c.GetString("trace_id")
	if traceID == "" {
		traceID = uuid.New().String()
		c.Set("trace_id", traceID)
	}

	pricingRatio, _ := c.Get("pricing_ratio")
	ratio := 1.0
	if r, ok := pricingRatio.(float64); ok && r > 0 {
		ratio = r
	}
	routingTrack := c.GetString("routing_track")
	if routingTrack == "" {
		routingTrack = "standard"
	}

	snapshot := mediaTaskBilling{
		TraceID:          traceID,
		KeyHash:          c.GetString("key_hash"),
		UpstreamKeyID:    upstreamKeyID,
		Model:            model,
		Provider:         provider,
		Region:           c.GetString("region"),
		Cost:             cost,
		PricingRatio:     ratio,
		GroupID:          c.GetString("group_id"),
		ActualGroupID:    c.GetString("actual_group_id"),
		IsFallback:       c.GetBool("is_fallback"),
		RoutingTrack:     routingTrack,
		OriginalTrack:    c.GetString("original_track"),
		Endpoint:         c.Request.URL.Path,
		ClientIP:         c.ClientIP(),
		UserAgent:        c.Request.UserAgent(),
		ImageSize:        c.GetString("image_size"),
		ImageQuality:     c.GetString("image_quality"),
		ImageCount:       c.GetInt("image_count"),
		Duration:         c.GetInt("duration"),
		ChargeOnDispatch: chargeOnDispatch,
	}

	data, err := json.Marshal(snapshot)
	if err != nil {
		s.handlerDeps.Logger.Warn("Failed to marshal async task billing context",
			zap.String("task_id", taskID), zap.Error(err))
		return
	}
	if err := s.redisClient.Set(c.Request.Context(), mediaTaskBillingKey(taskID), data, mediaTaskBillingTTL).Err(); err != nil {
		s.handlerDeps.Logger.Warn("Failed to store async task billing context",
			zap.String("task_id", taskID), zap.Error(err))
	}
}

// billAsyncMediaTask settles an async media task exactly once at a terminal
// state. A Redis SETNX is the idempotency boundary: concurrent pollers and
// repeated polls cannot double-settle the task.
//
// Two billing semantics, chosen per provider at dispatch time:
//   - deferred (default): bill on terminal success; a terminal failure is
//     never billed, so no refund path is needed.
//   - charge-on-dispatch: the task was already billed when the upstream task
//     was created. Upstream providers in this class (e.g. TokenRouter) keep
//     the charge even when the task later fails, so we mirror that: terminal
//     success and terminal failure both need no action — the dispatch charge
//     stands. Submission-stage errors never reach this path (dispatch
//     billing only happens on a successfully created task), so the customer
//     is never charged for requests the upstream rejected outright.
//
// The billing context is fetched and the billing decision is made *before*
// the SETNX guard is acquired, so a transient Redis GET error (or an already
// no-op terminal state) never consumes the guard. Consuming the guard on a
// failed GET would permanently block re-billing on a later poll — a silent
// revenue leak. The guard is taken only immediately before the actual charge,
// which is the sole action that must not run twice.
func (s *HandlerService) billAsyncMediaTask(c *gin.Context, taskID, terminalStatus string, latencyMs int64) {
	if !s.mediaAsyncBillingEnabled() {
		return
	}
	logger := s.handlerDeps.Logger
	ctx := c.Request.Context()

	data, err := s.redisClient.Get(ctx, mediaTaskBillingKey(taskID)).Result()
	if err != nil {
		// Billing context lost (expired or Redis failure): without dispatch
		// metadata the usage cannot be attributed, so skip rather than bill
		// the wrong account. The guard is untouched, so a later poll that
		// recovers the context can still settle the task.
		logger.Warn("Async task billing context missing; task not settled",
			zap.String("task_id", taskID), zap.Error(err))
		return
	}
	var tb mediaTaskBilling
	if err := json.Unmarshal([]byte(data), &tb); err != nil {
		logger.Warn("Async task billing context corrupt; task not settled",
			zap.String("task_id", taskID), zap.Error(err))
		return
	}

	if tb.ChargeOnDispatch {
		// Charged at dispatch; upstream never refunds task-level failures,
		// so neither do we. Nothing to do at any terminal state — no guard
		// needed since there is no charge to deduplicate.
		return
	}

	if terminalStatus != "success" {
		return // deferred billing: only terminal success is billed
	}

	// Acquire the idempotency guard only now, right before the charge. A
	// failure or loss above never reaches here, so it cannot poison the guard.
	acquired, err := s.redisClient.SetNX(ctx, mediaTaskBilledKey(taskID), time.Now().Unix(), mediaTaskBillingTTL).Result()
	if err != nil {
		logger.Warn("Failed to guard async task billing", zap.String("task_id", taskID), zap.Error(err))
		return
	}
	if !acquired {
		return // already settled by an earlier poll
	}

	clientIP := c.ClientIP()
	uctx := UsageRecordContext{
		TraceID:       tb.TraceID,
		KeyHash:       tb.KeyHash,
		UpstreamKeyID: tb.UpstreamKeyID,
		Model:         tb.Model,
		Provider:      tb.Provider,
		Region:        tb.Region,
		PricingRatio:  tb.PricingRatio,
		GroupID:       tb.GroupID,
		ActualGroupID: tb.ActualGroupID,
		IsFallback:    tb.IsFallback,
		RoutingTrack:  tb.RoutingTrack,
		OriginalTrack: tb.OriginalTrack,
		StatusCode:    http.StatusOK,
		Cost:          tb.Cost,
		IsStream:      false,
		Endpoint:      tb.Endpoint,
		ClientIP:      clientIP,
		ClientGeo:     geoip.Lookup(clientIP),
		UserAgent:     tb.UserAgent,
		ImageSize:     tb.ImageSize,
		ImageQuality:  tb.ImageQuality,
		ImageCount:    tb.ImageCount,
		Duration:      tb.Duration,
	}
	s.recordMediaUsage(uctx, latencyMs, "")
}

// asyncTaskTerminalStatus extracts the terminal state of an async media task
// from an upstream poll body. It returns ("success" or "failed", true) at
// terminal states and ("", false) while the task is queued/running or when
// no status field is present (conservative: only an explicit terminal
// success is billed). Both the ARK-style top-level shape
// ({"status": "..."}) and a data-wrapped shape ({"data": {"status": "..."}}
// or {"data": [{"status": "..."}]}) are recognized.
func asyncTaskTerminalStatus(body []byte) (string, bool) {
	var top struct {
		Status string          `json:"status"`
		Data   json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &top); err != nil {
		return "", false
	}
	if status, terminal := classifyTaskStatus(top.Status); terminal {
		return status, true
	}
	if len(top.Data) == 0 {
		return "", false
	}
	// data may be an object or an array of objects.
	var obj struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(top.Data, &obj); err == nil && obj.Status != "" {
		return classifyTaskStatus(obj.Status)
	}
	var arr []struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(top.Data, &arr); err == nil {
		for _, e := range arr {
			if e.Status != "" {
				return classifyTaskStatus(e.Status)
			}
		}
	}
	return "", false
}

func classifyTaskStatus(status string) (string, bool) {
	// Providers are inconsistent about case and wording: Kling reports
	// "SUCCESS"/"FAILED", TokenRouter reports "SUCCESS"/"FAILURE". Normalize
	// before matching.
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "success", "succeeded", "completed", "complete", "done", "finished":
		return "success", true
	case "failed", "fail", "failure", "error", "cancelled", "canceled", "expired", "timeout":
		return "failed", true
	default:
		return "", false
	}
}

// preConsumeMedia reserves the estimated media cost before dispatch,
// mirroring the chat pre-consume gate (local balance cache first, Portal
// freeze otherwise). estimatedMicro is computed per unit (image count /
// declared video seconds), not per token. Returns the pre-consume state to
// settle after dispatch; rejected=true means the handler already wrote the
// 402 response and must abort.
func (s *HandlerService) preConsumeMedia(c *gin.Context, userID, keyType, model, provider string, estimatedMicro int64) (pcs *preConsumeState, rejected bool) {
	if estimatedMicro <= 0 || s.handlerDeps == nil || s.handlerDeps.Billing == nil {
		return nil, false
	}
	keyHash := c.GetString("key_hash")

	if s.localBalanceCacheEnabled(userID) && keyType != "enterprise" && keyType != "subscription" {
		_, ok, handled := s.authMiddleware.PreConsumeLocal(userID, estimatedMicro)
		if handled {
			if !ok {
				metrics.GetMetrics().IncrementPreconsumeRejected()
				c.JSON(http.StatusPaymentRequired, gin.H{"error": gin.H{
					"message": "Insufficient balance for this request",
					"type":    "billing_error",
					"code":    "INSUFFICIENT_BALANCE",
				}})
				return nil, true
			}
			// Reserve the full estimate (not the remaining balance returned by
			// PreConsumeLocal) so settleLocal can refund the exact amount.
			return &preConsumeState{local: estimatedMicro}, false
		}
		// No cached balance — fall through to the Portal freeze.
	}

	if s.cfg != nil && s.cfg.Billing.PreConsumeEnabled && keyType != "subscription" {
		traceID := c.GetString("request_id")
		if traceID == "" {
			traceID = uuid.New().String()
		}
		c.Set("trace_id", traceID)

		pcCtx, pcCancel := billing.NewPreConsumeContext(c.Request.Context())
		pcResult := s.handlerDeps.Billing.PreConsume(pcCtx, billing.PreConsumeRequest{
			UserID:                userID,
			KeyHash:               keyHash,
			Model:                 model,
			Provider:              provider,
			RequestID:             traceID,
			Margin:                s.cfg.Billing.PreConsumeMargin,
			EstimatedCostMicroUSD: estimatedMicro,
		})
		pcCancel()

		if pcResult.Rejected {
			metrics.GetMetrics().IncrementPreconsumeRejected()
			c.JSON(http.StatusPaymentRequired, gin.H{"error": gin.H{
				"message": "Insufficient balance for this request",
				"type":    "billing_error",
				"code":    "INSUFFICIENT_BALANCE",
			}})
			return nil, true
		}
		return &preConsumeState{portal: pcResult.Amount}, false
	}
	return nil, false
}

// settleMediaPreConsume releases the media pre-consume reservation once the
// dispatch outcome is known. Unlike chat, media cost is always billed through
// the usage record (Portal media-mode pricing), so the reservation must NOT
// be charged on success — that would double-bill. It is only converted to a
// charge for genuine upstream timeouts, where the provider may have started
// processing (mirroring chat settle semantics); every other outcome
// (success, 4xx/5xx, transport error) releases it.
func (s *HandlerService) settleMediaPreConsume(c *gin.Context, pcs *preConsumeState, model, provider string, statusCode int, errorType string, latencyMs int64) {
	if pcs == nil {
		return
	}
	userID := c.GetString(string(userIDKey))
	keyHash := c.GetString("key_hash")
	if pcs.local > 0 {
		// settleLocal charges only on "timeout"; all other outcomes refund.
		s.settleLocal(userID, pcs.local, nil, model, provider, false, statusCode, errorType)
		return
	}
	if pcs.portal <= 0 || s.handlerDeps.Billing == nil {
		return
	}
	if errorType == "timeout" {
		metrics.GetMetrics().IncrementSettleCharged(provider, model, false)
	}
	s.Settle(context.Background(), billing.SettleRequest{
		RequestID:   c.GetString("trace_id"),
		UserID:      userID,
		KeyHash:     keyHash,
		Model:       model,
		Provider:    provider,
		PreConsumed: pcs.portal,
		Success:     false, // DetermineSettleAction: charge only on timeout, else unfreeze
		StatusCode:  statusCode,
		ErrorType:   errorType,
		LatencyMs:   latencyMs,
	})
}

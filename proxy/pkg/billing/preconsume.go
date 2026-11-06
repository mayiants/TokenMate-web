package billing

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/ai-proxy/ai-proxy-go/dto"
	"github.com/ai-proxy/ai-proxy-go/pkg/telemetry"
)

// --- Pre-consume Scheme A: Freeze + Charge-on-Timeout ---
//
// Flow:
//   1. Before upstream call: freeze estimated input cost on user's balance
//      (balance not reduced, but frozen amount is reserved)
//   2a. Success (200): unfreeze → normal billing queue path charges actual usage
//   2b. Timeout/Error (no usage): convert freeze to charge (covers upstream cost)
//   2c. Upstream 4xx (rejected): unfreeze (upstream didn't process, no cost)
//
// This avoids double-charging: the normal billing queue only runs for successful
// requests, and pre-consume only charges for failed requests.

// PreConsumeRequest is sent to Portal to freeze estimated input cost.
type PreConsumeRequest struct {
	UserID      string  `json:"userId"`
	KeyHash     string  `json:"keyHash"`
	Model       string  `json:"model"`
	Provider    string  `json:"provider"`
	InputTokens int     `json:"inputTokens"`
	RequestID   string  `json:"requestId"`
	Margin      float64 `json:"margin,omitempty"`
	// EstimatedCostMicroUSD, when > 0, overrides the token-based estimate.
	// Media endpoints (image/video) bill per unit (image count / declared
	// duration seconds) rather than per token, so they compute the freeze
	// amount themselves via EstimateMediaCostMicroUSD.
	EstimatedCostMicroUSD int64 `json:"estimatedCostMicroUsd,omitempty"`
}

// PreConsumeResponse is returned by Portal after freeze.
type PreConsumeResponse struct {
	Success   bool  `json:"success"`
	Rejected  bool  `json:"rejected"`  // true if balance insufficient for freeze
	Amount    int64 `json:"amount"`    // amount frozen (micro-USD)
	Remaining int64 `json:"remaining"` // available balance after freeze
}

// SettleRequest is sent to Portal after the upstream request completes.
type SettleRequest struct {
	RequestID   string     `json:"requestId"`
	UserID      string     `json:"userId"`
	KeyHash     string     `json:"keyHash"`
	Model       string     `json:"model"`
	Provider    string     `json:"provider"`
	PreConsumed int64      `json:"preConsumed"` // frozen amount (micro-USD int64)
	ActualUsage *dto.Usage `json:"actualUsage"` // nil = timeout/error
	Success     bool       `json:"success"`     // upstream 200?
	StatusCode  int        `json:"statusCode"`
	ErrorType   string     `json:"errorType"` // "timeout", "4xx", "5xx", ""
	LatencyMs   int64      `json:"latencyMs"`
}

// SettleAction tells Portal what to do with the frozen amount.
type SettleAction string

const (
	SettleActionUnfreeze SettleAction = "unfreeze" // Release freeze, normal billing takes over
	SettleActionCharge   SettleAction = "charge"   // Convert freeze to actual charge
	SettleActionRefund   SettleAction = "refund"   // Same as unfreeze (alias for clarity)
)

// SettleResponse is returned by Portal after settlement.
type SettleResponse struct {
	Success bool  `json:"success"`
	Charged int64 `json:"charged"` // actual amount charged from freeze (0 if unfrozen)
}

// PreConsume sends a freeze request to Portal.
// If Portal is unavailable, the request is allowed (fail-open).
func (b *Billing) PreConsume(ctx context.Context, req PreConsumeRequest) PreConsumeResponse {
	if b.portalURL == "" || b.portalSecret == "" {
		return PreConsumeResponse{Success: true, Amount: 0}
	}

	estimatedCost := req.EstimatedCostMicroUSD
	if estimatedCost <= 0 {
		estimatedCost = b.estimateInputCost(req.Model, req.Provider, req.InputTokens, req.Margin)
	}
	if estimatedCost <= 0 {
		return PreConsumeResponse{Success: true, Amount: 0}
	}

	payload := struct {
		PreConsumeRequest
		EstimatedCost int64 `json:"estimatedCost"`
	}{
		PreConsumeRequest: req,
		EstimatedCost:     estimatedCost,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return PreConsumeResponse{Success: true, Amount: 0}
	}

	url := b.portalURL + "/api/webhook/pre-consume"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return PreConsumeResponse{Success: true, Amount: 0}
	}

	ts, sig := b.signRequest(data)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Timestamp", ts)
	httpReq.Header.Set("X-Signature", sig)
	telemetry.InjectTraceparent(ctx, httpReq)

	resp, err := b.portalClient.Do(httpReq)
	if err != nil {
		return PreConsumeResponse{Success: true, Amount: 0} // fail-open
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusPaymentRequired {
		return PreConsumeResponse{Success: true, Rejected: true, Amount: 0}
	}

	// Only decode the body for explicit 2xx responses. A 5xx (Portal degraded)
	// must fail-open so we don't block all traffic; decoding a 5xx error body
	// can succeed and yield a zero-value {Success:false} response, which would
	// otherwise fail-closed. Only HTTP 402 (handled above) blocks the request.
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Printf("[WARN] billing: pre-consume portal returned status %d, failing open", resp.StatusCode)
		return PreConsumeResponse{Success: true, Amount: 0}
	}

	var result PreConsumeResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return PreConsumeResponse{Success: true, Amount: 0}
	}
	return result
}

// Settle tells Portal what happened with the frozen amount.
//
// Outcomes:
//   - Success (200 + usage): unfreeze → normal billing queue handles actual cost
//   - Timeout (no usage): charge frozen amount (upstream consumed input)
//   - 4xx (rejected): unfreeze (upstream didn't process)
//   - 5xx: unfreeze by default (configurable)
func (b *Billing) Settle(ctx context.Context, req SettleRequest) SettleResponse {
	if b.portalURL == "" || b.portalSecret == "" {
		return SettleResponse{Success: true}
	}
	if req.PreConsumed == 0 {
		return SettleResponse{Success: true, Charged: 0}
	}

	// Determine action
	action := DetermineSettleAction(req.Success, req.StatusCode, req.ErrorType)

	settlePayload := struct {
		SettleRequest
		Action SettleAction `json:"action"`
	}{
		SettleRequest: req,
		Action:        action,
	}

	data, err := json.Marshal(settlePayload)
	if err != nil {
		return SettleResponse{Success: false}
	}

	url := b.portalURL + "/api/webhook/settle"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return SettleResponse{Success: false}
	}

	ts, sig := b.signRequest(data)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Timestamp", ts)
	httpReq.Header.Set("X-Signature", sig)
	telemetry.InjectTraceparent(ctx, httpReq)

	resp, err := b.portalClient.Do(httpReq)
	if err != nil {
		return SettleResponse{Success: false}
	}
	defer func() { _ = resp.Body.Close() }()

	var result SettleResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return SettleResponse{Success: false}
	}
	return result
}

// DetermineSettleAction decides what to do with a frozen amount based on request outcome.
//
// Rules:
//   - Success: unfreeze (normal billing queue will charge actual cost)
//   - Timeout: charge (upstream may have consumed input tokens before we got the error)
//   - Network error (statusCode==0, non-timeout): unfreeze (DNS failure, connection
//     refused, etc. — upstream never received the request, no tokens consumed)
//   - 4xx: unfreeze (upstream rejected before processing)
//   - 5xx: unfreeze (uncertain; favor user — reconciliation covers discrepancies)
func DetermineSettleAction(success bool, statusCode int, errorType string) SettleAction {
	if success {
		return SettleActionUnfreeze
	}
	switch {
	case errorType == "timeout":
		// Upstream likely received the request and consumed input tokens before
		// we hit the client-side deadline. Charge the frozen estimate.
		return SettleActionCharge
	case errorType == "usage_missing":
		// Upstream returned success but reported no token usage. Charging the
		// frozen estimate prevents the request from being completely free.
		return SettleActionCharge
	case statusCode == 0:
		// Non-timeout network error (DNS, connection refused, TLS handshake
		// failure, etc.): upstream never received the request, so no input
		// tokens were consumed. Release the frozen amount.
		return SettleActionUnfreeze
	case statusCode >= 400 && statusCode < 500:
		return SettleActionUnfreeze // upstream rejected, no processing
	case statusCode >= 500:
		return SettleActionUnfreeze // server error, uncertain — favor user
	default:
		return SettleActionUnfreeze
	}
}

// estimateInputCost calculates the estimated input cost in micro-USD.
func (b *Billing) estimateInputCost(model, provider string, inputTokens int, margin float64) int64 {
	if inputTokens <= 0 {
		return 0
	}
	if margin <= 0 {
		margin = 1.0
	}

	pricing := b.GetPricing(model, provider)
	if pricing == nil {
		pricing = DefaultPricing(model)
	}

	var inputPricePerM, fixedPrice float64
	if pricing != nil {
		inputPricePerM = pricing.ModelRatio
		fixedPrice = pricing.FixedPrice
	}
	if inputPricePerM <= 0 {
		inputPricePerM = 1.0
	}

	costUSD := (float64(inputTokens)/1_000_000.0*inputPricePerM + fixedPrice) * margin
	costMicroUSD := int64(costUSD * 1_000_000.0)

	if costMicroUSD <= 0 && inputTokens > 0 {
		costMicroUSD = 1
	}
	return costMicroUSD
}

// EstimateInputCost is the exported version for testing.
func (b *Billing) EstimateInputCost(model, provider string, inputTokens int, margin float64) int64 {
	return b.estimateInputCost(model, provider, inputTokens, margin)
}

// PreConsumeTimeout is the max time to wait for Portal pre-consume response.
const PreConsumeTimeout = 2 * time.Second

// NewPreConsumeContext creates a context with pre-consume timeout.
func NewPreConsumeContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, PreConsumeTimeout)
}

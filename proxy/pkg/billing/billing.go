package billing

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/mayiants/ai-proxy-go/dto"
	"github.com/mayiants/ai-proxy-go/pkg/telemetry"
	"github.com/mayiants/ai-proxy-go/types"
)

// CostBreakdown - Detailed cost breakdown
type CostBreakdown struct {
	InputCost      float64 `json:"input_cost"`
	OutputCost     float64 `json:"output_cost"`
	CacheReadCost  float64 `json:"cache_read_cost"`
	CacheWriteCost float64 `json:"cache_write_cost"`
	ReasoningCost  float64 `json:"reasoning_cost"`
	TotalCost      float64 `json:"total_cost"`
}

// CalculateCost - Calculate cost from usage and pricing
func CalculateCost(usage *dto.Usage, pricing *types.PriceData) *CostBreakdown {
	if usage == nil || pricing == nil {
		return &CostBreakdown{}
	}

	// Extract token counts
	inputTokens := usage.PromptTokens
	outputTokens := usage.CompletionTokens
	cachedTokens := usage.PromptTokensDetails.CachedTokens
	if cachedTokens == 0 {
		// Fallback: some providers (GLM, DeepSeek) report cache hits via
		// prompt_cache_hit_tokens instead of prompt_tokens_details.cached_tokens.
		cachedTokens = usage.PromptCacheHitTokens
	}
	cacheCreationTokens := usage.PromptTokensDetails.CachedCreationTokens
	reasoningTokens := usage.CompletionTokenDetails.ReasoningTokens

	// Also check Claude-style cache tokens
	if usage.ClaudeCacheCreation5mTokens > 0 || usage.ClaudeCacheCreation1hTokens > 0 {
		cacheCreationTokens = usage.ClaudeCacheCreation5mTokens + usage.ClaudeCacheCreation1hTokens
	}

	// Effective input tokens (excluding cached)
	effectiveInputTokens := inputTokens - cachedTokens
	if effectiveInputTokens < 0 {
		effectiveInputTokens = 0
	}

	// Calculate costs. Prefer absolute per-1M prices when synced from Portal.
	var inputCost, outputCost, cacheReadCost, cacheWriteCost, reasoningCost float64
	if pricing.InputPricePer1M > 0 {
		inputCost = float64(effectiveInputTokens) / 1e6 * pricing.InputPricePer1M
		outputCost = float64(outputTokens) / 1e6 * pricing.OutputPricePer1M
		cacheReadCost = float64(cachedTokens) / 1e6 * pricing.CacheHitPricePer1M
		cacheWriteCost = float64(cacheCreationTokens) / 1e6 * pricing.CacheWritePricePer1M
		reasoningCost = float64(reasoningTokens) / 1e6 * pricing.ReasoningPricePer1M
	} else {
		inputCost = float64(effectiveInputTokens) / 1e6 * pricing.ModelRatio
		outputCost = float64(outputTokens) / 1e6 * pricing.ModelRatio * pricing.CompletionRatio
		cacheReadCost = float64(cachedTokens) / 1e6 * pricing.ModelRatio * pricing.CacheRatio
		cacheWriteCost = float64(cacheCreationTokens) / 1e6 * pricing.ModelRatio * pricing.CacheCreationRatio
		reasoningCost = float64(reasoningTokens) / 1e6 * pricing.ModelRatio
	}

	totalCost := inputCost + outputCost + cacheReadCost + cacheWriteCost + reasoningCost

	return &CostBreakdown{
		InputCost:      roundCost(inputCost),
		OutputCost:     roundCost(outputCost),
		CacheReadCost:  roundCost(cacheReadCost),
		CacheWriteCost: roundCost(cacheWriteCost),
		ReasoningCost:  roundCost(reasoningCost),
		TotalCost:      roundCost(totalCost),
	}
}

// CalculateQuota - Calculate quota from usage (for billing system)
func CalculateQuota(usage *dto.Usage, pricing *types.PriceData) int {
	cost := CalculateCost(usage, pricing)

	// Convert cost to quota (1 quota = $0.002 / 500 tokens typically)
	// QuotaPerUnit is typically 500000 (500 tokens = 1 quota unit)
	quotaPerUnit := 500000.0

	quota := int(cost.TotalCost * quotaPerUnit)
	if quota < 0 {
		quota = 0
	}

	return quota
}

// CalculateCostWithCache - Calculate cost with cache discounts
// This matches new-api's billing formula
func CalculateCostWithCache(usage *dto.Usage, pricing *types.PriceData) *CostBreakdown {
	if usage == nil {
		return &CostBreakdown{}
	}

	// Get token counts
	inputTokens := usage.PromptTokens
	outputTokens := usage.CompletionTokens

	// Cache tokens
	cachedTokens := usage.PromptTokensDetails.CachedTokens
	if cachedTokens == 0 {
		// Fallback: see CalculateCost.
		cachedTokens = usage.PromptCacheHitTokens
	}
	cacheCreationTokens := usage.PromptTokensDetails.CachedCreationTokens

	// Claude-specific cache tokens
	cacheCreation5m := usage.ClaudeCacheCreation5mTokens
	cacheCreation1h := usage.ClaudeCacheCreation1hTokens

	// Total cache creation tokens
	if cacheCreation5m > 0 || cacheCreation1h > 0 {
		cacheCreationTokens = cacheCreation5m + cacheCreation1h
	}

	// Effective input = total input - cached
	effectiveInput := inputTokens - cachedTokens
	if effectiveInput < 0 {
		effectiveInput = 0
	}

	// Calculate costs. Prefer absolute per-1M prices when synced from Portal.
	var inputCost, outputCost, cacheReadCost, cacheWriteCost, reasoningCost float64
	if pricing.InputPricePer1M > 0 {
		inputCost = float64(effectiveInput) / 1e6 * pricing.InputPricePer1M
		outputCost = float64(outputTokens) / 1e6 * pricing.OutputPricePer1M
		cacheReadCost = float64(cachedTokens) / 1e6 * pricing.CacheHitPricePer1M
		cacheWriteCost = float64(cacheCreationTokens) / 1e6 * pricing.CacheWritePricePer1M
		if cacheCreation5m > 0 {
			cacheWriteCost = float64(cacheCreation5m) / 1e6 * pricing.CacheWritePricePer1M
			if cacheCreation1h > 0 {
				cacheWriteCost += float64(cacheCreation1h) / 1e6 * pricing.CacheWritePricePer1M
			}
		}
		reasoningCost = float64(usage.CompletionTokenDetails.ReasoningTokens) / 1e6 * pricing.ReasoningPricePer1M
	} else {
		inputCost = float64(effectiveInput) / 1e6 * pricing.ModelRatio
		outputCost = float64(outputTokens) / 1e6 * pricing.ModelRatio * pricing.CompletionRatio
		cacheReadCost = float64(cachedTokens) / 1e6 * pricing.ModelRatio * pricing.CacheRatio
		cacheWriteCost = float64(cacheCreationTokens) / 1e6 * pricing.ModelRatio * pricing.CacheCreationRatio
		if cacheCreation5m > 0 && pricing.CacheCreation5mRatio > 0 {
			cacheWriteCost = float64(cacheCreation5m) / 1e6 * pricing.ModelRatio * pricing.CacheCreation5mRatio
			if cacheCreation1h > 0 && pricing.CacheCreation1hRatio > 0 {
				cacheWriteCost += float64(cacheCreation1h) / 1e6 * pricing.ModelRatio * pricing.CacheCreation1hRatio
			}
		}
		reasoningCost = float64(usage.CompletionTokenDetails.ReasoningTokens) / 1e6 * pricing.ModelRatio
	}

	totalCost := inputCost + outputCost + cacheReadCost + cacheWriteCost + reasoningCost

	return &CostBreakdown{
		InputCost:      roundCost(inputCost),
		OutputCost:     roundCost(outputCost),
		CacheReadCost:  roundCost(cacheReadCost),
		CacheWriteCost: roundCost(cacheWriteCost),
		ReasoningCost:  roundCost(reasoningCost),
		TotalCost:      roundCost(totalCost),
	}
}

// GetCacheHitRate - Calculate cache hit rate
func GetCacheHitRate(usage *dto.Usage) float64 {
	if usage == nil || usage.PromptTokens == 0 {
		return 0
	}

	cachedTokens := usage.PromptTokensDetails.CachedTokens
	if cachedTokens == 0 {
		cachedTokens = usage.PromptCacheHitTokens
	}

	return float64(cachedTokens) / float64(usage.PromptTokens)
}

// pricingCacheEntry is a cached price view with a TTL.
type pricingCacheEntry struct {
	pricing *types.PriceData
	at      time.Time
}

// Billing - Billing calculator with pricing data
type Billing struct {
	pricingCache map[string]*pricingCacheEntry
	portalURL    string
	portalSecret string
	syncInterval time.Duration
	lastSync     time.Time
	syncMutex    sync.RWMutex
	portalClient *http.Client
}

// pricingCacheTTL is the maximum age of a cached price view before it is considered stale.
const pricingCacheTTL = 60 * time.Second

type PortalPricing struct {
	Provider             string  `json:"provider"`
	Region               string  `json:"region"`
	Model                string  `json:"model"`
	InputPricePer1M      float64 `json:"inputPricePer1M"`
	OutputPricePer1M     float64 `json:"outputPricePer1M"`
	CacheHitPricePer1M   float64 `json:"cacheHitPricePer1M"`
	CacheWritePricePer1M float64 `json:"cacheWritePricePer1M"`
	ReasoningPricePer1M  float64 `json:"reasoningPricePer1M"`
	FixedPrice           float64 `json:"fixedPrice"`
	BillingMode          string  `json:"billingMode"`
}

type PortalPricingSyncResponse struct {
	Pricing  []PortalPricing `json:"pricing"`
	SyncedAt string          `json:"syncedAt"`
}

func NewBilling() *Billing {
	return &Billing{
		pricingCache: make(map[string]*pricingCacheEntry),
		portalClient: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        50,
				MaxIdleConnsPerHost: 20,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
}

func NewBillingWithPortal(portalURL, portalSecret string, syncInterval time.Duration) *Billing {
	b := NewBilling()
	b.portalURL = portalURL
	b.portalSecret = portalSecret
	b.syncInterval = syncInterval
	if b.syncInterval == 0 {
		b.syncInterval = 60 * time.Second
	}
	return b
}

// signRequest computes HMAC-SHA256 signature for Portal webhook authentication.
// Format: HMAC-SHA256(timestamp + "." + body)
func (b *Billing) signRequest(data []byte) (timestamp, signature string) {
	ts := fmt.Sprintf("%d", time.Now().Unix())
	mac := hmac.New(sha256.New, []byte(b.portalSecret))
	mac.Write([]byte(ts + "."))
	mac.Write(data)
	return ts, hex.EncodeToString(mac.Sum(nil))
}

func (b *Billing) StartPricingSyncLoop(ctx context.Context) {
	if b.portalURL == "" || b.portalSecret == "" {
		return
	}

	go func() {
		ticker := time.NewTicker(b.syncInterval)
		defer ticker.Stop()

		initCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		_ = b.SyncPricingFromPortal(initCtx)
		cancel()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				syncCtx, syncCancel := context.WithTimeout(ctx, 30*time.Second)
				_ = b.SyncPricingFromPortal(syncCtx)
				syncCancel()
			}
		}
	}()
}

func (b *Billing) SyncPricingFromPortal(ctx context.Context) error {
	if b.portalURL == "" || b.portalSecret == "" {
		return nil
	}

	url := b.portalURL + "/api/admin/pricing/sync"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	req.Header.Set("x-proxy-secret", b.portalSecret)
	telemetry.InjectTraceparent(ctx, req)

	resp, err := b.portalClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		return fmt.Errorf("portal returned status %d", resp.StatusCode)
	}

	var result PortalPricingSyncResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}

	b.syncMutex.Lock()
	now := time.Now()
	// Per-sync aggregate: merge multiple regions within this batch using max(),
	// then overwrite the cache entry. Previously we reused the existing cache
	// entry and ran max() against it, which meant Portal price drops never took
	// effect (price only ever went up). Now each sync starts from a fresh
	// PriceData, so the cached value reflects the latest batch, not the
	// historical maximum.
	batchAgg := make(map[string]*types.PriceData)
	for _, p := range result.Pricing {
		key := p.Provider + ":" + p.Model
		pd, ok := batchAgg[key]
		if !ok {
			pd = &types.PriceData{}
			batchAgg[key] = pd
		}
		// Merge multiple regions by keeping the worst-case (max) absolute price.
		pd.InputPricePer1M = max(pd.InputPricePer1M, p.InputPricePer1M)
		pd.OutputPricePer1M = max(pd.OutputPricePer1M, p.OutputPricePer1M)
		pd.CacheHitPricePer1M = max(pd.CacheHitPricePer1M, p.CacheHitPricePer1M)
		pd.CacheWritePricePer1M = max(pd.CacheWritePricePer1M, p.CacheWritePricePer1M)
		pd.ReasoningPricePer1M = max(pd.ReasoningPricePer1M, p.ReasoningPricePer1M)
		pd.FixedPrice = max(pd.FixedPrice, p.FixedPrice)
		if p.BillingMode != "" {
			pd.BillingMode = p.BillingMode
		}
		pd.GroupRatio = 1.0

		// Derive ratio fields from absolute prices so existing callers keep working.
		pd.ModelRatio = pd.InputPricePer1M
		pd.CompletionRatio = safeRatio(pd.OutputPricePer1M, pd.InputPricePer1M)
		pd.CacheRatio = safeRatio(pd.CacheHitPricePer1M, pd.InputPricePer1M)
		pd.CacheCreationRatio = safeRatio(pd.CacheWritePricePer1M, pd.InputPricePer1M)
		if pd.CacheCreation5mRatio == 0 {
			pd.CacheCreation5mRatio = pd.CacheCreationRatio
		}
		if pd.CacheCreation1hRatio == 0 {
			pd.CacheCreation1hRatio = pd.CacheCreationRatio
		}
	}
	// Overwrite cache with the latest batch (drops stale high prices).
	for key, pd := range batchAgg {
		b.pricingCache[key] = &pricingCacheEntry{pricing: pd, at: now}
	}
	b.lastSync = now
	b.syncMutex.Unlock()

	return nil
}

func safeRatio(numerator, denominator float64) float64 {
	if denominator > 0 {
		return numerator / denominator
	}
	if numerator > 0 {
		return 1.0
	}
	return 0
}

func (b *Billing) CalculateCost(usage *dto.Usage, model, provider string) float64 {
	pricing := b.GetPricing(model, provider)
	breakdown := CalculateCostWithCache(usage, pricing)
	return breakdown.TotalCost
}

// CalculateImageCost - Calculate image generation cost
func (b *Billing) CalculateImageCost(model, size, quality string, n int) float64 {
	// Base cost per image (in dollars)
	baseCost := 0.04 // Default for DALL-E 3 standard 1024x1024

	modelLower := strings.ToLower(model)
	if stringsContains(modelLower, "dall-e-3") {
		switch size {
		case "1024x1024":
			if quality == "hd" {
				baseCost = 0.08
			} else {
				baseCost = 0.04
			}
		case "1792x1024", "1024x1792":
			if quality == "hd" {
				baseCost = 0.12
			} else {
				baseCost = 0.08
			}
		}
	} else if stringsContains(modelLower, "dall-e-2") {
		switch size {
		case "1024x1024":
			baseCost = 0.02
		case "512x512":
			baseCost = 0.018
		case "256x256":
			baseCost = 0.016
		}
	}

	if n <= 0 {
		n = 1
	}

	return baseCost * float64(n)
}

// EstimateMediaCostMicroUSD estimates the worst-case cost of a media
// generation request in micro-USD. The synced FixedPrice is the worst-case
// unit price Portal publishes for the model (max variant price, per second
// for video), so the freeze is unitPrice * units * margin. units is the
// billed unit count: image count for image generation, declared duration in
// seconds for video. Long videos must be estimated from the declared
// duration — a fixed one-unit estimate would under-freeze, letting the
// actual per-second cost exceed the frozen amount. Handlers cap units
// (max images / max duration) so a client cannot inflate the freeze without
// bound. Returns 0 when no synced media price exists; the gate then fails
// open and billing relies on the usage record.
func (b *Billing) EstimateMediaCostMicroUSD(model, provider string, units int, margin float64) int64 {
	if units < 1 {
		units = 1
	}
	if margin <= 0 {
		margin = 1.0
	}

	pricing := b.GetPricing(model, provider)
	var unitPrice float64
	if pricing != nil {
		unitPrice = pricing.FixedPrice
	}
	if unitPrice <= 0 {
		return 0
	}

	return int64(unitPrice * float64(units) * margin * 1_000_000)
}

// GetPricing returns the cached price for a model, falling back to DefaultPricing
// when no fresh (non-expired) cached entry exists. Stale entries are ignored so
// that the next successful Portal sync repopulates the cache.
func (b *Billing) GetPricing(model, provider string) *types.PriceData {
	key := provider + ":" + model

	b.syncMutex.RLock()
	entry, ok := b.pricingCache[key]
	if ok && time.Since(entry.at) <= pricingCacheTTL {
		b.syncMutex.RUnlock()
		return entry.pricing
	}
	b.syncMutex.RUnlock()

	pricing := DefaultPricing(model)
	// NOTE: do NOT cache fallback hardcoded pricing; otherwise sync updates
	// will never replace it. Only cache pricing from Portal sync (SetPricing).
	return pricing
}

// SetPricing - Set custom pricing for a model
func (b *Billing) SetPricing(model, provider string, pricing *types.PriceData) {
	key := provider + ":" + model
	b.syncMutex.Lock()
	b.pricingCache[key] = &pricingCacheEntry{pricing: pricing, at: time.Now()}
	b.syncMutex.Unlock()
}

func (b *Billing) GetLastSyncTime() time.Time {
	b.syncMutex.RLock()
	defer b.syncMutex.RUnlock()
	return b.lastSync
}

// roundCost - Round cost to 10 decimal places
func roundCost(cost float64) float64 {
	return math.Round(cost*1e10) / 1e10
}

// DefaultPricing - Get default pricing for a model
func DefaultPricing(model string) *types.PriceData {
	// Default pricing ratios
	pricing := &types.PriceData{
		CompletionRatio:      3.0,  // Output is 3x input
		CacheRatio:           0.1,  // Cache read is 10% of input
		CacheCreationRatio:   0.25, // Cache write is 25% of input
		CacheCreation5mRatio: 0.25,
		CacheCreation1hRatio: 0.30,
		GroupRatio:           1.0,
	}

	modelLower := strings.ToLower(model)

	// Model-specific pricing
	switch {
	case strings.Contains(modelLower, "claude") && strings.Contains(modelLower, "opus"):
		pricing.ModelRatio = 15.0
		pricing.CompletionRatio = 5.0
	case strings.Contains(modelLower, "claude") && strings.Contains(modelLower, "sonnet"):
		pricing.ModelRatio = 3.0
		pricing.CompletionRatio = 5.0
	case strings.Contains(modelLower, "claude") && strings.Contains(modelLower, "haiku"):
		pricing.ModelRatio = 0.25
		pricing.CompletionRatio = 5.0
	case strings.Contains(modelLower, "gpt-4o"):
		pricing.ModelRatio = 5.0
		pricing.CompletionRatio = 3.0
	case strings.Contains(modelLower, "gpt-4-turbo"):
		pricing.ModelRatio = 10.0
		pricing.CompletionRatio = 3.0
	case strings.Contains(modelLower, "gpt-4"):
		pricing.ModelRatio = 30.0
		pricing.CompletionRatio = 2.0
	case strings.Contains(modelLower, "gpt-3.5"):
		pricing.ModelRatio = 0.5
		pricing.CompletionRatio = 3.0
	case strings.Contains(modelLower, "deepseek"):
		pricing.ModelRatio = 0.14
		pricing.CompletionRatio = 2.0
	case strings.Contains(modelLower, "qwen"):
		pricing.ModelRatio = 0.2
		pricing.CompletionRatio = 2.0
	default:
		pricing.ModelRatio = 1.0
	}

	return pricing
}

// UsageRecord represents a usage record to send to Portal
type UsageRecord struct {
	TraceID              string `json:"traceId"`
	APIKeyHash           string `json:"apiKeyHash"`
	Model                string `json:"model"`
	Provider             string `json:"provider"`
	Region               string `json:"region"`
	InputTokens          int    `json:"inputTokens"`
	OutputTokens         int    `json:"outputTokens"`
	CachedTokens         int    `json:"cachedTokens"`
	CacheCreationTokens  int    `json:"cacheCreationTokens,omitempty"`
	EffectiveInputTokens int    `json:"effectiveInputTokens"`
	ReasoningTokens      int    `json:"reasoningTokens"`
	// Cost is reported in USD as a float64. It is NOT micro-USD: the
	// pre-consume / settle path (PreConsumeResponse.Amount, SettleRequest.
	// PreConsumed, SettleResponse.Charged) uses int64 micro-USD. Use
	// USDFloatToMicroUSD / MicroUSDToUSDFloat to cross the unit boundary —
	// a naive int64(cost) cast would be off by 10^6. Portal recomputes cost
	// from token counts; this field is deprecated on the portal side.
	Cost               float64 `json:"cost"`
	LatencyMs          int     `json:"latencyMs"`
	RequestCount       int     `json:"requestCount"`
	ServerID           string  `json:"serverId"`
	UpstreamKeyID      string  `json:"upstreamKeyId"`
	Timestamp          string  `json:"timestamp"`
	CacheHit           bool    `json:"cacheHit"`
	Error              string  `json:"error"`
	GroupID            string  `json:"groupId,omitempty"`
	ActualGroupID      string  `json:"actualGroupId,omitempty"`
	IsFallback         bool    `json:"isFallback,omitempty"`
	PricingRatio       float64 `json:"pricingRatio,omitempty"`
	RoutingTrack       string  `json:"routingTrack,omitempty"`
	OriginalTrack      string  `json:"originalTrack,omitempty"`
	Endpoint           string  `json:"endpoint,omitempty"`
	IsStreaming        bool    `json:"isStreaming,omitempty"`
	ClientIP           string  `json:"clientIp,omitempty"`
	ClientGeo          string  `json:"clientGeo,omitempty"`
	UserAgent          string  `json:"userAgent,omitempty"`
	AgentTools         string  `json:"agentTools,omitempty"`
	CalledTools        string  `json:"calledTools,omitempty"`
	TTFTMs             int     `json:"ttftMs,omitempty"`
	ReasoningIntensity string  `json:"reasoningIntensity,omitempty"`
	Status             string  `json:"status,omitempty"`

	// Media generation metadata used by Portal media-mode pricing rules.
	ImageSize    string `json:"imageSize,omitempty"`
	ImageQuality string `json:"imageQuality,omitempty"`
	ImageCount   int    `json:"imageCount,omitempty"`
	Duration     int    `json:"duration,omitempty"` // video/audio seconds
}

// USDFloatToMicroUSD converts a USD float64 amount to micro-USD (int64).
// UsageRecord.Cost and CalculateCost use USD float64; the pre-consume / settle
// path (PreConsumeResponse.Amount, SettleRequest.PreConsumed) uses int64
// micro-USD. Always cross the boundary through these helpers to avoid 10^6
// conversion mistakes.
func USDFloatToMicroUSD(usd float64) int64 {
	return int64(usd * 1_000_000)
}

// MicroUSDToUSDFloat converts a micro-USD int64 amount to USD float64.
func MicroUSDToUSDFloat(micro int64) float64 {
	return float64(micro) / 1_000_000.0
}

// SendUsageToPortal sends a usage record to Portal webhook
func (b *Billing) SendUsageToPortal(ctx context.Context, record *UsageRecord) error {
	if b.portalURL == "" || b.portalSecret == "" {
		return nil // Portal not configured, skip
	}

	url := b.portalURL + "/api/webhook/usage"
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("failed to marshal usage record: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	ts, sig := b.signRequest(data)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Timestamp", ts)
	req.Header.Set("X-Signature", sig)
	telemetry.InjectTraceparent(ctx, req)

	resp, err := b.portalClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("portal returned status %d", resp.StatusCode)
	}

	return nil
}

// SendUsageBatchToPortal sends multiple usage records to Portal webhook
func (b *Billing) SendUsageBatchToPortal(ctx context.Context, records []*UsageRecord) error {
	if b.portalURL == "" || b.portalSecret == "" {
		return nil // Portal not configured, skip
	}

	if len(records) == 0 {
		return nil
	}

	url := b.portalURL + "/api/webhook/usage-batch"
	data, err := json.Marshal(map[string]interface{}{
		"records":  records,
		"serverId": records[0].ServerID,
	})
	if err != nil {
		return fmt.Errorf("failed to marshal usage batch: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	ts, sig := b.signRequest(data)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Timestamp", ts)
	req.Header.Set("X-Signature", sig)
	telemetry.InjectTraceparent(ctx, req)

	resp, err := b.portalClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("portal returned status %d", resp.StatusCode)
	}

	return nil
}

// SendUsageStatsBatchToPortal sends usage stats records to Portal's usage-stats-batch endpoint.
// This is the new primary billing path (separate from audit logger).
func (b *Billing) SendUsageStatsBatchToPortal(ctx context.Context, records []*UsageRecord) error {
	if b.portalURL == "" || b.portalSecret == "" {
		return nil
	}
	if len(records) == 0 {
		return nil
	}

	url := b.portalURL + "/api/webhook/usage-stats-batch"
	data, err := json.Marshal(map[string]interface{}{
		"records":  records,
		"serverId": records[0].ServerID,
	})
	if err != nil {
		return fmt.Errorf("failed to marshal usage stats batch: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	ts, sig := b.signRequest(data)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Timestamp", ts)
	req.Header.Set("X-Signature", sig)
	telemetry.InjectTraceparent(ctx, req)

	resp, err := b.portalClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("portal returned status %d", resp.StatusCode)
	}

	return nil
}

// stringsContains - Check if string contains substring (uses standard library)
func stringsContains(s, substr string) bool {
	return strings.Contains(s, substr)
}

// UpstreamUsageRecord represents aggregated upstream key usage for a single day
type UpstreamUsageRecord struct {
	UpstreamKeyID       string `json:"upstreamKeyId"`
	Provider            string `json:"provider"`
	Model               string `json:"model"`
	Date                string `json:"date"` // YYYY-MM-DD
	RequestCount        int    `json:"requestCount"`
	InputTokens         int64  `json:"inputTokens"`
	OutputTokens        int64  `json:"outputTokens"`
	CacheReadTokens     int64  `json:"cacheReadTokens"`
	CacheCreationTokens int64  `json:"cacheCreationTokens"`
	ErrorCount          int    `json:"errorCount"`
	LatencyMs           int    `json:"latencyMs"`
}

// ProviderHealthRecord captures the outcome of a single upstream request.
type ProviderHealthRecord struct {
	Provider     string `json:"provider"`
	Model        string `json:"model"`
	GroupID      string `json:"groupId"`
	TraceID      string `json:"traceId"`
	StatusCode   int    `json:"statusCode"`
	LatencyMs    int    `json:"latencyMs"`
	ErrorType    string `json:"errorType"`
	ErrorMessage string `json:"errorMessage"`
	ServerID     string `json:"serverId"`
	Timestamp    string `json:"timestamp"`
}

// ProviderErrorType is the normalized taxonomy for provider health records.
type ProviderErrorType string

const (
	ProviderErrorNone            ProviderErrorType = ""                 // success
	ProviderErrorRateLimit       ProviderErrorType = "rate_limit"       // 429 / quota
	ProviderErrorTimeout         ProviderErrorType = "timeout"          // request timeout
	ProviderErrorNetwork         ProviderErrorType = "network_error"    // connection/dns/tls failure
	ProviderError4xx             ProviderErrorType = "error_4xx"        // generic 4xx
	ProviderError5xx             ProviderErrorType = "error_5xx"        // generic 5xx
	ProviderErrorAuth            ProviderErrorType = "auth_error"       // 401/403 / invalid key
	ProviderErrorInvalidRequest  ProviderErrorType = "invalid_request"  // 400 / bad request
	ProviderErrorResponseTimeout ProviderErrorType = "response_timeout" // connected but read timeout
	ProviderErrorContentMismatch ProviderErrorType = "content_mismatch" // HTTP 200 but unexpected content
	ProviderErrorUpstream        ProviderErrorType = "upstream_error"   // fallback
)

// ClassifyProviderError maps an upstream outcome to a normalized ProviderErrorType.
func ClassifyProviderError(statusCode int, transportErr error, body string) ProviderErrorType {
	if transportErr != nil {
		if errors.Is(transportErr, context.DeadlineExceeded) || os.IsTimeout(transportErr) {
			return ProviderErrorTimeout
		}
		return ProviderErrorNetwork
	}

	switch {
	case statusCode >= 200 && statusCode < 300:
		return ProviderErrorNone
	case statusCode == 429:
		return ProviderErrorRateLimit
	case statusCode == 401 || statusCode == 403:
		return ProviderErrorAuth
	case statusCode == 400:
		return ProviderErrorInvalidRequest
	case statusCode >= 400 && statusCode < 500:
		return ProviderError4xx
	case statusCode >= 500:
		return ProviderError5xx
	default:
		return ProviderErrorUpstream
	}
}

// ValidProviderErrorType reports whether v is a known error type value.
func ValidProviderErrorType(v string) bool {
	switch ProviderErrorType(v) {
	case ProviderErrorNone, ProviderErrorRateLimit, ProviderErrorTimeout,
		ProviderErrorNetwork, ProviderError4xx, ProviderError5xx,
		ProviderErrorAuth, ProviderErrorInvalidRequest, ProviderErrorResponseTimeout,
		ProviderErrorContentMismatch, ProviderErrorUpstream:
		return true
	}
	return false
}

// SendUpstreamUsageBatchToPortal sends upstream usage records to Portal webhook
func (b *Billing) SendUpstreamUsageBatchToPortal(ctx context.Context, records []*UpstreamUsageRecord) error {
	if b.portalURL == "" || b.portalSecret == "" {
		return nil // Portal not configured, skip
	}
	if len(records) == 0 {
		return nil
	}

	url := b.portalURL + "/api/webhook/upstream-usage-batch"
	data, err := json.Marshal(map[string]interface{}{
		"records": records,
	})
	if err != nil {
		return fmt.Errorf("failed to marshal upstream usage batch: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	ts, sig := b.signRequest(data)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Timestamp", ts)
	req.Header.Set("X-Signature", sig)
	telemetry.InjectTraceparent(ctx, req)

	resp, err := b.portalClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("portal returned status %d", resp.StatusCode)
	}

	return nil
}

// SendProviderHealthBatchToPortal sends provider health records to Portal webhook
func (b *Billing) SendProviderHealthBatchToPortal(ctx context.Context, records []*ProviderHealthRecord) error {
	if b.portalURL == "" || b.portalSecret == "" {
		return nil // Portal not configured, skip
	}
	if len(records) == 0 {
		return nil
	}

	url := b.portalURL + "/api/webhook/provider-health-batch"
	data, err := json.Marshal(map[string]interface{}{
		"records": records,
	})
	if err != nil {
		return fmt.Errorf("failed to marshal provider health batch: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	ts, sig := b.signRequest(data)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Timestamp", ts)
	req.Header.Set("X-Signature", sig)
	telemetry.InjectTraceparent(ctx, req)

	resp, err := b.portalClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("portal returned status %d", resp.StatusCode)
	}

	return nil
}

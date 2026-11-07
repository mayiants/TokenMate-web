package services

import (
	"context"
	"sync"
	"time"

	"github.com/mayiants/ai-portal-go/internal/db"
	"github.com/mayiants/ai-portal-go/pkg/metrics"
	"go.uber.org/zap"
)

// UsageRecord represents the subset of a proxy usage record needed for billing
// calculation. It mirrors the shape in internal/routes/webhook.go so that a
// routes.UsageRecord can be cast directly to services.UsageRecord.
type UsageRecord struct {
	TraceID              string  `json:"traceId"`
	APIKeyHash           string  `json:"apiKeyHash"`
	Model                string  `json:"model"`
	Provider             string  `json:"provider"`
	Region               string  `json:"region"`
	InputTokens          int     `json:"inputTokens"`
	OutputTokens         int     `json:"outputTokens"`
	CachedTokens         int     `json:"cachedTokens"`
	CacheCreationTokens  int     `json:"cacheCreationTokens"`
	EffectiveInputTokens int     `json:"effectiveInputTokens"`
	ReasoningTokens      int     `json:"reasoningTokens"`
	Cost                 float64 `json:"cost,omitempty"`
	LatencyMs            int     `json:"latencyMs"`
	RequestCount         int     `json:"requestCount"`
	ServerID             string  `json:"serverId"`
	UpstreamKeyID        string  `json:"upstreamKeyId"`
	RetryCount           int     `json:"retryCount"`
	Timestamp            string  `json:"timestamp"`
	CacheHit             bool    `json:"cacheHit"`
	Error                string  `json:"error"`
	ErrorCode            string  `json:"errorCode,omitempty"`
	GroupID              string  `json:"groupId,omitempty"`
	ActualGroupID        string  `json:"actualGroupId,omitempty"`
	IsFallback           bool    `json:"isFallback,omitempty"`
	PricingRatio         float64 `json:"pricingRatio,omitempty"`
	RoutingTrack         string  `json:"routingTrack,omitempty"`
	OriginalTrack        string  `json:"originalTrack,omitempty"`
	Endpoint             string  `json:"endpoint,omitempty"`
	IsStreaming          bool    `json:"isStreaming,omitempty"`
	ClientIP             string  `json:"clientIp,omitempty"`
	ClientGeo            string  `json:"clientGeo,omitempty"`
	UserAgent            string  `json:"userAgent,omitempty"`
	AgentTools           string  `json:"agentTools,omitempty"`
	CalledTools          string  `json:"calledTools,omitempty"`
	TTFTMs               int     `json:"ttftMs,omitempty"`
	ReasoningIntensity   string  `json:"reasoningIntensity,omitempty"`
	Status               string  `json:"status,omitempty"`
	PricingError         bool    `json:"pricingError,omitempty"`
	StatusCode           int     `json:"statusCode,omitempty"` // client-facing HTTP status; kept in sync with routes.UsageRecord

	// Media generation metadata used by media-mode pricing rules.
	ImageSize    string `json:"imageSize,omitempty"`
	ImageQuality string `json:"imageQuality,omitempty"`
	ImageCount   int    `json:"imageCount,omitempty"`
	Duration     int    `json:"duration,omitempty"` // video/audio seconds
}

// BillingService calculates usage cost from Portal pricing rules and caches
// pricing/group-ratio lookups to avoid repeated database queries.
type BillingService struct {
	db             *db.DB
	pricingSvc     *PricingService
	logger         *zap.Logger
	pricingRatioMu sync.RWMutex
	pricingRatio   map[string]float64 // groupID → ratio, 5min TTL
	pricingRatioAt time.Time
}

// NewBillingService creates a new billing calculator.
func NewBillingService(database *db.DB, logger *zap.Logger) *BillingService {
	return &BillingService{
		db:           database,
		pricingSvc:   NewPricingService(database, logger),
		logger:       logger,
		pricingRatio: make(map[string]float64),
	}
}

// CalculateCost computes cost in micro-USD quota from model_pricing_rules.
// Always uses Portal's own pricing data — Proxy's record.Cost is a USD estimate
// for monitoring only. PricingRatio is applied separately by the caller.
// On pricing failure, returns 0 (fail-open) and sets record.PricingError so
// the caller can flag the usage record for later reconciliation.
func (s *BillingService) CalculateCost(ctx context.Context, record *UsageRecord) int64 {
	costs, err := s.pricingSvc.CalculateCost(ctx, *record)
	if err != nil {
		metrics.GetMetrics().PricingFailures.Add(1)
		record.PricingError = true
		s.logger.Warn("failed to calculate pricing cost, returning zero",
			zap.String("provider", record.Provider),
			zap.String("region", record.Region),
			zap.String("model", record.Model),
			zap.Error(err))
		return 0
	}
	return USDtoQuota(costs.Total())
}

// CalculateCostBatch computes costs for a batch of records, grouping identical
// (provider, region, model, minute) rules so each rule is resolved once.
func (s *BillingService) CalculateCostBatch(ctx context.Context, records []UsageRecord) []int64 {
	costs, err := s.pricingSvc.CalculateCostBatch(ctx, records)
	if err != nil {
		metrics.GetMetrics().PricingFailures.Add(1)
		s.logger.Warn("failed to calculate batch pricing costs, returning zeros",
			zap.Int("count", len(records)),
			zap.Error(err))
		return make([]int64, len(records))
	}
	results := make([]int64, len(costs))
	for i, c := range costs {
		if c != nil {
			results[i] = USDtoQuota(c.Total())
		}
	}
	return results
}

// GetPricingRatio returns pricing_ratio for a group, using in-memory cache (5min TTL).
func (s *BillingService) GetPricingRatio(ctx context.Context, groupID string) float64 {
	if groupID == "" {
		return 1.0
	}

	result := s.GetPricingRatioBatch(ctx, []string{groupID})
	return result[groupID]
}

// GetPricingRatioBatch returns pricing_ratio for multiple groups in a single
// database round-trip. Missing or empty group IDs default to 1.0.
func (s *BillingService) GetPricingRatioBatch(ctx context.Context, groupIDs []string) map[string]float64 {
	result := make(map[string]float64, len(groupIDs))
	var missing []string

	s.pricingRatioMu.RLock()
	cacheValid := time.Now().Before(s.pricingRatioAt.Add(5 * time.Minute))
	for _, groupID := range groupIDs {
		if groupID == "" {
			result[groupID] = 1.0
			continue
		}
		if cacheValid {
			if ratio, ok := s.pricingRatio[groupID]; ok {
				result[groupID] = ratio
				continue
			}
		}
		missing = append(missing, groupID)
	}
	s.pricingRatioMu.RUnlock()

	if len(missing) > 0 {
		rows, err := s.db.Query(ctx,
			// Pricing ratio is multiplicative along the group chain: an
			// enterprise follow group with its own ratio (e.g. 1.01) on top
			// of a platform group (e.g. ent-us-1 at 1.015) bills at the
			// product (1.02515). The path array detects cycles: recursion stops
			// when a parent was already visited, so a bad parent_group_id loop
			// cannot inflate the ratio (e.g. 1.5^10). Depth is also capped.
			`WITH RECURSIVE chain AS (
			    SELECT g.id, g.pricing_ratio, g.parent_group_id,
			           COALESCE(g.pricing_ratio, 1.0) AS effective, 1 AS depth,
			           ARRAY[g.id] AS path, false AS cycle
			    FROM user_groups g
			    WHERE g.id = ANY($1::uuid[])
			    UNION ALL
			    SELECT c.id, pg.pricing_ratio, pg.parent_group_id,
			           c.effective * COALESCE(pg.pricing_ratio, 1.0), c.depth + 1,
			           c.path || pg.id, pg.id = ANY(c.path)
			    FROM chain c
			    JOIN user_groups pg ON pg.id = c.parent_group_id
			    WHERE c.depth < 10 AND NOT c.cycle
			)
			SELECT id, effective FROM chain
			WHERE parent_group_id IS NULL OR cycle OR depth = 10`,
			missing,
		)
		if err != nil {
			s.logger.Warn("failed to query pricing ratios, defaulting to 1.0",
				zap.Int("count", len(missing)),
				zap.Error(err))
		} else {
			defer rows.Close()
			for rows.Next() {
				var id string
				var ratio float64
				if err := rows.Scan(&id, &ratio); err == nil {
					result[id] = ratio
				}
			}
		}

		// Ensure every requested ID has an entry before caching, so groups
		// with ratio=1.0 (including those not found in the DB) are cached
		// and don't hit the DB on every request.
		for _, groupID := range groupIDs {
			if _, ok := result[groupID]; !ok {
				result[groupID] = 1.0
			}
		}

		s.pricingRatioMu.Lock()
		s.pricingRatioAt = time.Now()
		for id, ratio := range result {
			s.pricingRatio[id] = ratio
		}
		if len(s.pricingRatio) > 1000 {
			s.pricingRatio = map[string]float64{}
			for id, ratio := range result {
				s.pricingRatio[id] = ratio
			}
		}
		s.pricingRatioMu.Unlock()
	}

	return result
}

package services

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mayiants/ai-portal-go/internal/db"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

// PriceVariant describes a unit price for a media generation spec bucket
// (e.g. image resolution, video resolution, duration block).
type PriceVariant struct {
	Label string  `json:"label"`
	Price float64 `json:"price"`
	Unit  string  `json:"unit"` // image, second, block, request
}

// ComponentPrices holds unit prices for all billing components.
// Prices are in USD per 1M tokens, except FixedRequest which is USD per request.
// Variants are used when BillingMode is "media".
type ComponentPrices struct {
	Input        float64        `json:"input"`
	Output       float64        `json:"output"`
	CacheHit     float64        `json:"cache_hit"`
	CacheWrite   float64        `json:"cache_write"`
	Reasoning    float64        `json:"reasoning"`
	FixedRequest float64        `json:"fixed_request"`
	Variants     []PriceVariant `json:"variants,omitempty"`
}

// UnmarshalJSON provides defaults for missing component fields.
// It also falls back to the legacy "cache_miss" key for backward compatibility.
func (cp *ComponentPrices) UnmarshalJSON(data []byte) error {
	type alias ComponentPrices
	aux := &alias{
		Input:        0,
		Output:       0,
		CacheHit:     0,
		CacheWrite:   0,
		Reasoning:    0,
		FixedRequest: 0,
	}
	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}
	*cp = ComponentPrices(*aux)

	// Backward compatibility: older rules stored cache write price under "cache_miss".
	if cp.CacheWrite == 0 {
		var legacy struct {
			CacheMiss float64 `json:"cache_miss"`
		}
		if err := json.Unmarshal(data, &legacy); err == nil && legacy.CacheMiss > 0 {
			cp.CacheWrite = legacy.CacheMiss
		}
	}
	return nil
}

// TokenTier describes one price bracket for tiered token billing.
type TokenTier struct {
	MaxTokens *int64          `json:"max_tokens"`
	Prices    ComponentPrices `json:"prices"`
}

// TimeRule describes one time window for time-based pricing.
type TimeRule struct {
	Days   []int           `json:"days"`
	Start  string          `json:"start"`
	End    string          `json:"end"`
	Prices ComponentPrices `json:"prices"`
}

// PricingRule is the database representation of a model pricing rule.
type PricingRule struct {
	ID            string
	Provider      string
	Region        string
	ProductID     string
	APIModelName  string
	BillingMode   string
	Prices        ComponentPrices
	Tiers         []TokenTier
	TimeRules     []TimeRule
	Timezone      string
	Currency      string
	EffectiveFrom time.Time
	EffectiveTo   *time.Time
	Priority      int
	Status        string
	RuleName      string
	Note          string
	BlockSeconds  int // Used when a media variant unit is "block".
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// ComponentCosts holds the USD cost for each billing component.
type ComponentCosts struct {
	Input      float64
	Output     float64
	CacheHit   float64
	CacheWrite float64
	Reasoning  float64
	Fixed      float64
}

// Total returns the sum of all component costs.
func (cc *ComponentCosts) Total() float64 {
	return cc.Input + cc.Output + cc.CacheHit + cc.CacheWrite + cc.Reasoning + cc.Fixed
}

// PricingService resolves pricing rules and calculates usage costs.
type PricingService struct {
	db     *db.DB
	logger *zap.Logger

	cacheMu sync.RWMutex
	cache   map[string]*pricingCacheEntry
}

type pricingCacheEntry struct {
	rule *PricingRule
	at   time.Time
}

// NewPricingService creates a new pricing resolver.
func NewPricingService(database *db.DB, logger *zap.Logger) *PricingService {
	return &PricingService{
		db:     database,
		logger: logger,
		cache:  make(map[string]*pricingCacheEntry),
	}
}

const pricingCacheTTL = 1 * time.Minute

// CalculateCost computes the USD cost for a usage record.
func (s *PricingService) CalculateCost(ctx context.Context, record UsageRecord) (*ComponentCosts, error) {
	ts, err := parseUsageTimestamp(record.Timestamp)
	if err != nil {
		return nil, fmt.Errorf("invalid timestamp: %w", err)
	}

	rule, err := s.MatchRule(ctx, record.Provider, record.Region, record.Model, ts)
	if err != nil {
		return nil, err
	}
	if rule == nil {
		return nil, fmt.Errorf("no pricing rule found for %s/%s/%s", record.Provider, record.Region, record.Model)
	}

	return s.calculateCostForRule(record, rule, ts), nil
}

// CalculateCostBatch computes USD costs for a slice of usage records. It groups
// records by (provider, region, model, minute) and resolves each distinct rule
// once, then applies the rule to every record in the group. This avoids
// repeated MatchRule calls for high-volume webhook batches.
func (s *PricingService) CalculateCostBatch(ctx context.Context, records []UsageRecord) ([]*ComponentCosts, error) {
	if len(records) == 0 {
		return nil, nil
	}

	type groupKey struct {
		provider string
		region   string
		model    string
		minute   int64
	}
	type groupValue struct {
		indices []int
		rule    *PricingRule
		ts      time.Time
	}

	groups := make(map[groupKey]*groupValue)
	indicesByKey := make(map[groupKey][]int, len(records))

	for i, record := range records {
		ts, err := parseUsageTimestamp(record.Timestamp)
		if err != nil {
			return nil, fmt.Errorf("invalid timestamp at index %d: %w", i, err)
		}
		key := groupKey{
			provider: record.Provider,
			region:   record.Region,
			model:    record.Model,
			minute:   ts.Unix() / 60,
		}
		indicesByKey[key] = append(indicesByKey[key], i)
		if _, ok := groups[key]; !ok {
			// Store a placeholder; we will resolve the rule after grouping.
			groups[key] = &groupValue{indices: indicesByKey[key]}
		}
		groups[key].indices = indicesByKey[key]
		groups[key].ts = ts
	}

	// Resolve one rule per group.
	for key, gv := range groups {
		rule, err := s.MatchRule(ctx, key.provider, key.region, key.model, gv.ts)
		if err != nil {
			return nil, err
		}
		if rule == nil {
			return nil, fmt.Errorf("no pricing rule found for %s/%s/%s", key.provider, key.region, key.model)
		}
		gv.rule = rule
	}

	// Calculate per-record costs using the shared rule.
	results := make([]*ComponentCosts, len(records))
	for _, gv := range groups {
		for _, idx := range gv.indices {
			results[idx] = s.calculateCostForRule(records[idx], gv.rule, gv.ts)
		}
	}

	return results, nil
}

func (s *PricingService) calculateCostForRule(record UsageRecord, rule *PricingRule, ts time.Time) *ComponentCosts {
	switch rule.BillingMode {
	case "tiered":
		return s.calculateTieredCost(record, rule.Tiers, rule.Prices)
	case "tiered_context":
		return s.calculateContextTierCost(record, rule.Tiers, rule.Prices)
	case "time_based":
		prices, err := s.resolveTimeBasedPrices(rule, ts)
		if err != nil {
			return &ComponentCosts{}
		}
		return s.calculateFlatCost(record, prices)
	case "media":
		return s.calculateMediaCost(record, rule)
	default:
		// flat and unknown modes fall back to flat pricing.
		return s.calculateFlatCost(record, rule.Prices)
	}
}

// EstimateComponentCostForAggregate computes an approximate component cost
// breakdown for aggregated token/request totals. It intentionally uses the
// rule's base flat prices rather than tiered/time-based windows, because
// aggregating across many requests or days and then applying tiers would
// produce misleadingly low costs. The result is suitable for reconciliation
// dashboards that need to understand *composition* of billed cost, not an
// exact invoice.
func (s *PricingService) EstimateComponentCostForAggregate(ctx context.Context, provider, region, model string, ts time.Time, input, output, cacheHit, cacheWrite, reasoning, effectiveInput, requests int64) (*ComponentCosts, error) {
	rule, err := s.MatchRule(ctx, provider, region, model, ts)
	if err != nil {
		return nil, err
	}
	if rule == nil {
		return &ComponentCosts{}, nil
	}
	effective := effectiveInput
	if effective == 0 {
		effective = input - cacheHit
		if effective < 0 {
			effective = 0
		}
	}
	return &ComponentCosts{
		Input:      float64(effective) / 1e6 * rule.Prices.Input,
		Output:     float64(output) / 1e6 * rule.Prices.Output,
		CacheHit:   float64(cacheHit) / 1e6 * rule.Prices.CacheHit,
		CacheWrite: float64(cacheWrite) / 1e6 * rule.Prices.CacheWrite,
		Reasoning:  float64(reasoning) / 1e6 * rule.Prices.Reasoning,
		Fixed:      rule.Prices.FixedRequest * float64(requests),
	}, nil
}

func (s *PricingService) calculateFlatCost(record UsageRecord, prices ComponentPrices) *ComponentCosts {
	effectiveInput := record.EffectiveInputTokens
	if effectiveInput == 0 {
		effectiveInput = record.InputTokens - record.CachedTokens
	}
	// The fixed fee is per request, so multiply by the request count. A
	// record always represents at least one request; guard against a
	// zero/unset count so we never drop the fixed fee entirely.
	reqCount := record.RequestCount
	if reqCount < 1 {
		reqCount = 1
	}
	return &ComponentCosts{
		Input:      float64(effectiveInput) / 1e6 * prices.Input,
		Output:     float64(record.OutputTokens) / 1e6 * prices.Output,
		CacheHit:   float64(record.CachedTokens) / 1e6 * prices.CacheHit,
		CacheWrite: float64(record.CacheCreationTokens) / 1e6 * prices.CacheWrite,
		Reasoning:  float64(record.ReasoningTokens) / 1e6 * prices.Reasoning,
		Fixed:      prices.FixedRequest * float64(reqCount),
	}
}

func (s *PricingService) calculateMediaCost(record UsageRecord, rule *PricingRule) *ComponentCosts {
	variants := rule.Prices.Variants
	if len(variants) == 0 {
		// No variants configured; fall back to the flat fixed request price.
		return s.calculateFlatCost(record, rule.Prices)
	}

	variant := matchVariant(record, variants)
	if variant == nil {
		// No match; use the cheapest variant to avoid undercharging.
		variant = cheapestVariant(variants)
	}

	count := record.ImageCount
	if count < 1 {
		count = record.RequestCount
	}
	if count < 1 {
		count = 1
	}

	var fixed float64
	switch variant.Unit {
	case "second":
		duration := record.Duration
		if duration < 1 {
			duration = 1
		}
		fixed = variant.Price * float64(duration) * float64(count)
	case "block":
		blockSeconds := rule.BlockSeconds
		if blockSeconds < 1 {
			blockSeconds = 1
		}
		duration := record.Duration
		if duration < 1 {
			duration = 1
		}
		blocks := (duration + blockSeconds - 1) / blockSeconds
		fixed = variant.Price * float64(blocks) * float64(count)
	default:
		// "image" and "request" are billed per unit.
		fixed = variant.Price * float64(count)
	}

	// Always charge the small per-request fixed fee on top of the media unit
	// price so downstream never undercuts a token-model-only rule.
	fixed += rule.Prices.FixedRequest * float64(count)

	return &ComponentCosts{Fixed: fixed}
}

func matchVariant(record UsageRecord, variants []PriceVariant) *PriceVariant {
	// Match using the image size/quality or duration reported by the proxy.
	// Endpoint hints are not used because the variant labels already encode
	// resolution/spec/duration.
	pixels := imagePixels(record.ImageSize)
	duration := record.Duration

	// First pass: exact or threshold-based matching.
	for i := range variants {
		labelLower := strings.ToLower(variants[i].Label)

		// Direct resolution substring, e.g. "1024x1024" or "768P" in the label.
		// If the label also encodes a duration bucket (e.g. "768P 6s"), require the
		// requested duration to match so that "768P 10s" is not picked for a 6s request.
		if record.ImageSize != "" && strings.Contains(labelLower, strings.ToLower(record.ImageSize)) {
			if duration > 0 {
				if seconds := extractSeconds(labelLower); seconds > 0 && seconds != duration {
					continue
				}
			}
			return &variants[i]
		}

		// Pixel-count thresholds, e.g. "Output Pixels <= 236W".
		if op, threshold := parsePixelThreshold(labelLower); threshold > 0 && pixels > 0 {
			switch op {
			case "<=", "<", "≤":
				if pixels <= threshold {
					return &variants[i]
				}
			case ">=", ">", "≥":
				if pixels > threshold {
					return &variants[i]
				}
			}
		}

		// Duration thresholds/buckets, e.g. "5s", "10s", "duration <= 5s".
		// When an image size is provided, skip labels that also encode a resolution
		// (e.g. "1080P 6s") — those should have matched the size check above.
		if duration > 0 && (record.ImageSize == "" || !labelHasResolutionHint(labelLower)) {
			if seconds := extractSeconds(labelLower); seconds > 0 {
				if seconds == duration {
					return &variants[i]
				}
				if op, threshold := parseDurationThreshold(labelLower); threshold > 0 {
					switch op {
					case "<=", "<", "≤":
						if duration <= threshold {
							return &variants[i]
						}
					case ">=", ">", "≥":
						if duration > threshold {
							return &variants[i]
						}
					}
				}
			}
		}
	}

	// Second pass: quality/voice keyword matching.
	if record.ImageQuality != "" {
		qualityLower := strings.ToLower(record.ImageQuality)
		for i := range variants {
			labelLower := strings.ToLower(variants[i].Label)
			if strings.Contains(labelLower, qualityLower) {
				return &variants[i]
			}
		}
	}

	// Third pass: loose substring fallback for legacy labels.
	candidates := []string{record.ImageSize, record.ImageQuality}
	for _, cand := range candidates {
		if cand == "" {
			continue
		}
		candLower := strings.ToLower(cand)
		for i := range variants {
			labelLower := strings.ToLower(variants[i].Label)
			if strings.Contains(labelLower, candLower) || strings.Contains(candLower, labelLower) {
				return &variants[i]
			}
		}
	}
	return nil
}

// extractSeconds parses a bare "5s" or "10 sec" suffix from a variant label.
func extractSeconds(label string) int {
	re := regexp.MustCompile(`(\d+(?:\.\d+)?)\s*s\b`)
	m := re.FindStringSubmatch(label)
	if len(m) < 2 {
		return 0
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0
	}
	return int(v)
}

// parseDurationThreshold extracts a threshold like "<= 5s" from a label.
func parseDurationThreshold(label string) (string, int) {
	ops := []string{"<=", ">=", "<", ">", "≤", "≥"}
	for _, op := range ops {
		idx := strings.Index(label, op)
		if idx < 0 {
			continue
		}
		after := strings.TrimSpace(label[idx+len(op):])
		re := regexp.MustCompile(`(\d+(?:\.\d+)?)\s*s\b`)
		m := re.FindStringSubmatch(after)
		if len(m) < 2 {
			continue
		}
		v, err := strconv.ParseFloat(m[1], 64)
		if err != nil {
			continue
		}
		return op, int(v)
	}
	return "", 0
}

// imagePixels parses "1024x1024" into total pixel count.
func imagePixels(size string) int {
	parts := strings.Split(strings.ToLower(size), "x")
	if len(parts) != 2 {
		return 0
	}
	w, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	h, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil || w <= 0 || h <= 0 {
		return 0
	}
	return w * h
}

// labelHasResolutionHint reports whether a variant label encodes a resolution
// spec such as "768P", "1080P", or "1024x1024". These labels should be matched
// by size, not by duration alone, when the caller provided an image size.
var resolutionHintRe = regexp.MustCompile(`\d+\s*p|\d+\s*x\s*\d+`)

func labelHasResolutionHint(label string) bool {
	return resolutionHintRe.MatchString(strings.ToLower(label))
}

// parsePixelThreshold extracts a threshold like "<= 236W" from a label.
// "W" is treated as 10,000 (Chinese 万), "K" as 1,000, "M" as 1,000,000.
func parsePixelThreshold(label string) (string, int) {
	ops := []string{"<=", ">=", "<", ">", "≤", "≥"}
	for _, op := range ops {
		idx := strings.Index(label, op)
		if idx < 0 {
			continue
		}
		after := strings.TrimSpace(label[idx+len(op):])
		fields := strings.Fields(after)
		if len(fields) == 0 {
			continue
		}
		numStr := fields[0]
		multiplier := 1
		numStr = strings.TrimSpace(numStr)
		if strings.HasSuffix(numStr, "w") {
			multiplier = 10000
			numStr = strings.TrimSuffix(numStr, "w")
		} else if strings.HasSuffix(numStr, "k") {
			multiplier = 1000
			numStr = strings.TrimSuffix(numStr, "k")
		} else if strings.HasSuffix(numStr, "m") {
			multiplier = 1000000
			numStr = strings.TrimSuffix(numStr, "m")
		}
		numStr = strings.TrimSuffix(numStr, ",")
		v, err := strconv.ParseFloat(numStr, 64)
		if err != nil {
			continue
		}
		return op, int(v * float64(multiplier))
	}
	return "", 0
}

func cheapestVariant(variants []PriceVariant) *PriceVariant {
	best := &variants[0]
	for i := 1; i < len(variants); i++ {
		if variants[i].Price < best.Price {
			best = &variants[i]
		}
	}
	return best
}

// calculateContextTierCost prices a request at a single tier selected by its
// total context length (input + output), matching new-api style tiered_expr
// semantics (e.g. laozhang): once total length crosses a threshold, every
// component — including tokens below the threshold — bills at the higher
// tier's prices. Falls back to the rule's base flat prices when tiers are
// empty or no tier covers the total.
func (s *PricingService) calculateContextTierCost(record UsageRecord, tiers []TokenTier, base ComponentPrices) *ComponentCosts {
	tier := selectContextTier(int64(record.InputTokens+record.OutputTokens), tiers)
	if tier == nil {
		return s.calculateFlatCost(record, base)
	}
	return s.calculateFlatCost(record, tier.Prices)
}

// selectContextTier picks the first tier whose max_tokens covers totalLen
// (sorted ascending; nil max_tokens is the catch-all). Returns nil when tiers
// is empty or no tier covers totalLen.
func selectContextTier(totalLen int64, tiers []TokenTier) *TokenTier {
	if len(tiers) == 0 {
		return nil
	}
	sorted := make([]TokenTier, len(tiers))
	copy(sorted, tiers)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].MaxTokens == nil {
			return false
		}
		if sorted[j].MaxTokens == nil {
			return true
		}
		return *sorted[i].MaxTokens < *sorted[j].MaxTokens
	})
	for i := range sorted {
		if sorted[i].MaxTokens == nil || totalLen <= *sorted[i].MaxTokens {
			return &sorted[i]
		}
	}
	return nil
}

func (s *PricingService) calculateTieredCost(record UsageRecord, tiers []TokenTier, prices ComponentPrices) *ComponentCosts {
	effectiveInput := record.EffectiveInputTokens
	if effectiveInput == 0 {
		effectiveInput = record.InputTokens - record.CachedTokens
	}
	// The fixed fee is per request, so multiply by the request count. A
	// record always represents at least one request; guard against a
	// zero/unset count so we never drop the fixed fee entirely.
	reqCount := record.RequestCount
	if reqCount < 1 {
		reqCount = 1
	}
	return &ComponentCosts{
		Input:      calculateTieredComponentCost(effectiveInput, tiers, "input"),
		Output:     calculateTieredComponentCost(record.OutputTokens, tiers, "output"),
		CacheHit:   calculateTieredComponentCost(record.CachedTokens, tiers, "cache_hit"),
		CacheWrite: calculateTieredComponentCost(record.CacheCreationTokens, tiers, "cache_write"),
		Reasoning:  calculateTieredComponentCost(record.ReasoningTokens, tiers, "reasoning"),
		Fixed:      prices.FixedRequest * float64(reqCount),
	}
}

func calculateTieredComponentCost(tokens int, tiers []TokenTier, component string) float64 {
	if tokens <= 0 || len(tiers) == 0 {
		return 0
	}

	sorted := make([]TokenTier, len(tiers))
	copy(sorted, tiers)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].MaxTokens == nil {
			return false
		}
		if sorted[j].MaxTokens == nil {
			return true
		}
		return *sorted[i].MaxTokens < *sorted[j].MaxTokens
	})

	cost := 0.0
	consumed := 0
	for _, tier := range sorted {
		if consumed >= tokens {
			break
		}
		limit := tokens
		if tier.MaxTokens != nil {
			limit = int(*tier.MaxTokens)
			if limit > tokens {
				limit = tokens
			}
		}
		chunk := limit - consumed
		if chunk > 0 {
			cost += float64(chunk) / 1e6 * componentPrice(tier.Prices, component)
			consumed += chunk
		}
	}
	return cost
}

func componentPrice(prices ComponentPrices, component string) float64 {
	switch component {
	case "input":
		return prices.Input
	case "output":
		return prices.Output
	case "cache_hit":
		return prices.CacheHit
	case "cache_write":
		return prices.CacheWrite
	case "cache_miss":
		return prices.CacheWrite
	case "reasoning":
		return prices.Reasoning
	default:
		return 0
	}
}

func (s *PricingService) resolveTimeBasedPrices(rule *PricingRule, t time.Time) (ComponentPrices, error) {
	loc, err := time.LoadLocation(rule.Timezone)
	if err != nil {
		loc = time.UTC
	}
	local := t.In(loc)
	weekday := int(local.Weekday())
	timeOfDay := local.Format("15:04")

	for _, tr := range rule.TimeRules {
		if !sliceContainsInt(tr.Days, weekday) {
			continue
		}
		if tr.Start <= tr.End {
			if timeOfDay >= tr.Start && timeOfDay < tr.End {
				return tr.Prices, nil
			}
		} else {
			// Crosses midnight, e.g. 23:00 -> 09:00.
			if timeOfDay >= tr.Start || timeOfDay < tr.End {
				return tr.Prices, nil
			}
		}
	}

	// No time window matched: fall back to rule.Prices (base price).
	return rule.Prices, nil
}

func sliceContainsInt(vals []int, v int) bool {
	for _, x := range vals {
		if x == v {
			return true
		}
	}
	return false
}

// MatchRule finds the best matching pricing rule for a provider/region/model/time.
// If no rule matches and the model starts with the AWS "global." inference-profile
// prefix, the prefix is stripped and matching is retried once.
func (s *PricingService) MatchRule(ctx context.Context, provider, region, model string, t time.Time) (*PricingRule, error) {
	key := fmt.Sprintf("%s|%s|%s|%d", provider, region, model, t.Unix()/60)

	s.cacheMu.RLock()
	if e, ok := s.cache[key]; ok && time.Since(e.at) < pricingCacheTTL {
		s.cacheMu.RUnlock()
		return e.rule, nil
	}
	s.cacheMu.RUnlock()

	rule, err := s.matchRuleFromDB(ctx, provider, region, model, t)
	if err != nil {
		return nil, err
	}
	if rule == nil && strings.HasPrefix(model, "global.") {
		stripped := strings.TrimPrefix(model, "global.")
		rule, err = s.matchRuleFromDB(ctx, provider, region, stripped, t)
		if err != nil {
			return nil, err
		}
	}

	s.cacheMu.Lock()
	// Evict expired entries for the same (provider, region, model) prefix to
	// bound memory growth: without this, each active combination accumulates
	// one stale entry per minute until the rules change and invalidateCache
	// clears everything.
	prefix := fmt.Sprintf("%s|%s|%s|", provider, region, model)
	for k, e := range s.cache {
		if strings.HasPrefix(k, prefix) && time.Since(e.at) >= pricingCacheTTL {
			delete(s.cache, k)
		}
	}
	s.cache[key] = &pricingCacheEntry{rule: rule, at: time.Now()}
	s.cacheMu.Unlock()

	return rule, nil
}

// normalizeModelName strips dots and dashes so version separators don't
// decide matches: clients request "claude-opus-4.6" (dot, upstream catalog
// style) while a provider's service list may register "claude-opus-4-6"
// (dash, Bedrock inference-profile style). Exact matches always win over
// normalized ones via matchScore.
func normalizeModelName(s string) string {
	r := strings.NewReplacer(".", "", "-", "", "_", "", "/", "")
	return strings.ToLower(r.Replace(s))
}

func (s *PricingService) matchRuleFromDB(ctx context.Context, provider, region, model string, t time.Time) (*PricingRule, error) {
	normModel := normalizeModelName(model)
	rows, err := s.db.Query(ctx,
		`SELECT id::text, provider, region, product_id, api_model_name, billing_mode,
		        prices, tiers, time_rules, timezone, currency,
		        effective_from, effective_to, priority, status, rule_name, note,
		        block_seconds, created_at, updated_at
		 FROM model_pricing_rules
		 WHERE status = 'active'
		   AND effective_from <= $1
		   AND (effective_to IS NULL OR effective_to >= $1)
		   AND provider IN ($2, '*')
		   AND region IN ($3, '*')
		   AND (product_id = $4 OR api_model_name = $4
		        OR replace(replace(replace(replace(COALESCE(product_id,''),'.',''),'-',''),'_',''),'/','') = $5
		        OR replace(replace(replace(replace(COALESCE(api_model_name,''),'.',''),'-',''),'_',''),'/','') = $5
		        OR (product_id IS NULL AND api_model_name IS NULL))
		 ORDER BY priority DESC, effective_from DESC`,
		t, provider, region, model, normModel,
	)
	if err != nil {
		return nil, fmt.Errorf("query pricing rules: %w", err)
	}
	defer rows.Close()

	var candidates []*PricingRule
	for rows.Next() {
		r, err := scanPricingRule(rows)
		if err != nil {
			s.logger.Warn("failed to scan pricing rule", zap.Error(err))
			continue
		}
		candidates = append(candidates, r)
	}

	if len(candidates) == 0 {
		return nil, nil
	}

	// Score: exact provider +2, exact region +2, exact model +2, then priority/effective_from.
	best := candidates[0]
	bestScore := matchScore(best, provider, region, model)
	for _, r := range candidates[1:] {
		score := matchScore(r, provider, region, model)
		if score > bestScore {
			bestScore = score
			best = r
		}
	}
	return best, nil
}

// matchScore ranks candidates: exact provider/region/model matches beat
// normalized (dot/dash-stripped) model matches, which in turn beat the
// nameless global fallback rule.
func matchScore(r *PricingRule, provider, region, model string) int {
	score := r.Priority * 10
	if r.Provider == provider {
		score += 100
	}
	if r.Region == region {
		score += 100
	}
	if r.ProductID == model || r.APIModelName == model {
		score += 100
	} else if normalizeModelName(r.ProductID) == normalizeModelName(model) ||
		normalizeModelName(r.APIModelName) == normalizeModelName(model) {
		score += 50
	}
	return score
}

// ListRules returns pricing rules with optional filters.
func (s *PricingService) ListRules(ctx context.Context, provider, region, model, billingMode, status string) ([]*PricingRule, error) {
	query := `SELECT id::text, provider, region, product_id, api_model_name, billing_mode,
	                 prices, tiers, time_rules, timezone, currency,
	                 effective_from, effective_to, priority, status, rule_name, note,
	                 block_seconds, created_at, updated_at
	          FROM model_pricing_rules WHERE 1=1`
	args := []interface{}{}
	idx := 1

	if provider != "" && provider != "all" {
		query += fmt.Sprintf(" AND provider = $%d", idx)
		args = append(args, provider)
		idx++
	}
	if region != "" && region != "all" {
		query += fmt.Sprintf(" AND region = $%d", idx)
		args = append(args, region)
		idx++
	}
	if model != "" && model != "all" {
		query += fmt.Sprintf(" AND (product_id ILIKE $%d OR api_model_name ILIKE $%d OR region ILIKE $%d)", idx, idx, idx)
		args = append(args, "%"+model+"%")
		idx++
	}
	if billingMode != "" && billingMode != "all" {
		query += fmt.Sprintf(" AND billing_mode = $%d", idx)
		args = append(args, billingMode)
		idx++
	}
	if status != "" {
		query += fmt.Sprintf(" AND status = $%d", idx)
		args = append(args, status)
	}
	query += " ORDER BY provider, region, api_model_name, product_id, priority DESC"

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list pricing rules: %w", err)
	}
	defer rows.Close()

	var rules []*PricingRule
	for rows.Next() {
		r, err := scanPricingRule(rows)
		if err != nil {
			s.logger.Warn("failed to scan pricing rule", zap.Error(err))
			continue
		}
		rules = append(rules, r)
	}
	return rules, nil
}

// GetRule fetches a single pricing rule by ID.
func (s *PricingService) GetRule(ctx context.Context, id string) (*PricingRule, error) {
	row := s.db.QueryRow(ctx,
		`SELECT id::text, provider, region, product_id, api_model_name, billing_mode,
		        prices, tiers, time_rules, timezone, currency,
		        effective_from, effective_to, priority, status, rule_name, note,
		        block_seconds, created_at, updated_at
		 FROM model_pricing_rules WHERE id = $1::uuid`, id,
	)
	return scanPricingRule(row)
}

// CreateRule inserts a new pricing rule.
func (s *PricingService) CreateRule(ctx context.Context, r *PricingRule) (string, error) {
	if err := normalizePricingRule(r); err != nil {
		return "", err
	}
	if err := s.ValidateRuleAgainstUpstream(ctx, r); err != nil {
		return "", err
	}
	pricesJSON, tiersJSON, timeRulesJSON, err := marshalPricingRuleJSON(r)
	if err != nil {
		return "", err
	}

	var id string
	err = s.db.QueryRow(ctx,
		`INSERT INTO model_pricing_rules
		 (provider, region, product_id, api_model_name, billing_mode,
		  prices, tiers, time_rules, timezone, currency,
		  effective_from, effective_to, priority, status, rule_name, note, block_seconds)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
		 RETURNING id::text`,
		r.Provider, r.Region, strPtr(r.ProductID), strPtr(r.APIModelName), r.BillingMode,
		pricesJSON, tiersJSON, timeRulesJSON, r.Timezone, r.Currency,
		r.EffectiveFrom, r.EffectiveTo, r.Priority, r.Status, r.RuleName, r.Note, r.BlockSeconds,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("create pricing rule: %w", err)
	}
	s.invalidateCache()
	return id, nil
}

// UpdateRule updates an existing pricing rule.
func (s *PricingService) UpdateRule(ctx context.Context, r *PricingRule) error {
	if r.ID == "" {
		return fmt.Errorf("rule id required")
	}
	if err := normalizePricingRule(r); err != nil {
		return err
	}
	if err := s.ValidateRuleAgainstUpstream(ctx, r); err != nil {
		return err
	}
	pricesJSON, tiersJSON, timeRulesJSON, err := marshalPricingRuleJSON(r)
	if err != nil {
		return err
	}

	if err := s.db.Exec(ctx,
		`UPDATE model_pricing_rules SET
		  provider=$1, region=$2, product_id=$3, api_model_name=$4, billing_mode=$5,
		  prices=$6, tiers=$7, time_rules=$8, timezone=$9, currency=$10,
		  effective_from=$11, effective_to=$12, priority=$13, status=$14,
		  rule_name=$15, note=$16, block_seconds=$17, updated_at=NOW()
		 WHERE id=$18::uuid`,
		r.Provider, r.Region, strPtr(r.ProductID), strPtr(r.APIModelName), r.BillingMode,
		pricesJSON, tiersJSON, timeRulesJSON, r.Timezone, r.Currency,
		r.EffectiveFrom, r.EffectiveTo, r.Priority, r.Status,
		r.RuleName, r.Note, r.BlockSeconds, r.ID,
	); err != nil {
		return fmt.Errorf("update pricing rule: %w", err)
	}
	s.invalidateCache()
	return nil
}

// DeleteRule hard-deletes a pricing rule.
func (s *PricingService) DeleteRule(ctx context.Context, id string) error {
	if err := s.db.Exec(ctx, `DELETE FROM model_pricing_rules WHERE id = $1::uuid`, id); err != nil {
		return fmt.Errorf("delete pricing rule: %w", err)
	}
	s.invalidateCache()
	return nil
}

// ExportRulesToCSV exports pricing rules to a CSV document.
// Columns: id, provider, region, product_id, api_model_name, billing_mode, status,
// priority, currency, timezone, effective_from, effective_to, rule_name, note,
// price_input, price_output, price_cache_hit, price_cache_write, price_reasoning,
// price_fixed_request, tiers_json, time_rules_json.
func (s *PricingService) ExportRulesToCSV(ctx context.Context, provider, region, model, billingMode, status string) ([]byte, error) {
	if status == "" {
		status = "active"
	}
	rules, err := s.ListRules(ctx, provider, region, model, billingMode, status)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	header := []string{
		"id", "provider", "region", "product_id", "api_model_name", "billing_mode",
		"status", "priority", "currency", "timezone", "effective_from", "effective_to",
		"rule_name", "note", "price_input", "price_output", "price_cache_hit",
		"price_cache_write", "price_reasoning", "price_fixed_request", "tiers_json", "time_rules_json",
	}
	if err := w.Write(header); err != nil {
		return nil, err
	}

	for _, r := range rules {
		tiersJSON := ""
		if len(r.Tiers) > 0 {
			b, _ := json.Marshal(r.Tiers)
			tiersJSON = string(b)
		}
		timeRulesJSON := ""
		if len(r.TimeRules) > 0 {
			b, _ := json.Marshal(r.TimeRules)
			timeRulesJSON = string(b)
		}
		effectiveTo := ""
		if r.EffectiveTo != nil {
			effectiveTo = r.EffectiveTo.Format(time.RFC3339)
		}
		row := []string{
			r.ID,
			r.Provider,
			r.Region,
			r.ProductID,
			r.APIModelName,
			r.BillingMode,
			r.Status,
			strconv.Itoa(r.Priority),
			r.Currency,
			r.Timezone,
			r.EffectiveFrom.Format(time.RFC3339),
			effectiveTo,
			r.RuleName,
			r.Note,
			fmt.Sprintf("%g", r.Prices.Input),
			fmt.Sprintf("%g", r.Prices.Output),
			fmt.Sprintf("%g", r.Prices.CacheHit),
			fmt.Sprintf("%g", r.Prices.CacheWrite),
			fmt.Sprintf("%g", r.Prices.Reasoning),
			fmt.Sprintf("%g", r.Prices.FixedRequest),
			tiersJSON,
			timeRulesJSON,
		}
		if err := w.Write(row); err != nil {
			return nil, err
		}
	}
	w.Flush()
	return buf.Bytes(), w.Error()
}

// ImportRulesFromCSV imports or updates pricing rules from a CSV document.
// It returns the number of created, updated and failed rows, plus a slice of
// per-row failure details (row number + reason).
func (s *PricingService) ImportRulesFromCSV(ctx context.Context, data []byte) (created, updated, failed int, failures []string, err error) {
	r := csv.NewReader(bytes.NewReader(data))
	r.FieldsPerRecord = -1
	records, err := r.ReadAll()
	if err != nil {
		return 0, 0, 0, nil, fmt.Errorf("parse csv: %w", err)
	}
	if len(records) == 0 {
		return 0, 0, 0, nil, nil
	}

	header := records[0]
	colIdx := map[string]int{}
	for i, h := range header {
		colIdx[strings.TrimSpace(h)] = i
	}
	get := func(row []string, name string) string {
		idx, ok := colIdx[name]
		if !ok || idx >= len(row) {
			return ""
		}
		return strings.TrimSpace(row[idx])
	}

	for rowIdx, row := range records[1:] {
		rowNum := rowIdx + 2 // 1-indexed, header is row 1
		if len(row) == 0 {
			continue
		}
		allEmpty := true
		for _, c := range row {
			if strings.TrimSpace(c) != "" {
				allEmpty = false
				break
			}
		}
		if allEmpty {
			continue
		}

		rule := &PricingRule{}
		rule.Provider = get(row, "provider")
		rule.Region = get(row, "region")
		rule.ProductID = get(row, "product_id")
		rule.APIModelName = get(row, "api_model_name")
		rule.BillingMode = get(row, "billing_mode")
		rule.Status = get(row, "status")
		rule.Priority, _ = strconv.Atoi(get(row, "priority"))
		rule.Currency = get(row, "currency")
		rule.Timezone = get(row, "timezone")
		rule.RuleName = get(row, "rule_name")
		rule.Note = get(row, "note")

		effectiveFrom := get(row, "effective_from")
		if effectiveFrom == "" {
			rule.EffectiveFrom = time.Now()
		} else {
			rule.EffectiveFrom, _ = parsePricingTime(effectiveFrom)
			if rule.EffectiveFrom.IsZero() {
				rule.EffectiveFrom = time.Now()
			}
		}
		effectiveTo := get(row, "effective_to")
		if effectiveTo != "" {
			if t, _ := parsePricingTime(effectiveTo); !t.IsZero() {
				rule.EffectiveTo = &t
			}
		}

		rule.Prices = ComponentPrices{
			Input:        parseFloatOrZero(get(row, "price_input")),
			Output:       parseFloatOrZero(get(row, "price_output")),
			CacheHit:     parseFloatOrZero(get(row, "price_cache_hit")),
			CacheWrite:   parseFloatOrZero(get(row, "price_cache_write")),
			Reasoning:    parseFloatOrZero(get(row, "price_reasoning")),
			FixedRequest: parseFloatOrZero(get(row, "price_fixed_request")),
		}
		// Backward compatibility for older CSV exports that used "price_cache_miss".
		if rule.Prices.CacheWrite == 0 {
			rule.Prices.CacheWrite = parseFloatOrZero(get(row, "price_cache_miss"))
		}
		if tiersJSON := get(row, "tiers_json"); tiersJSON != "" {
			if jsonErr := json.Unmarshal([]byte(tiersJSON), &rule.Tiers); jsonErr != nil {
				failed++
				failures = append(failures, fmt.Sprintf("row %d: tiers_json parse error: %v", rowNum, jsonErr))
				continue
			}
		}
		if timeRulesJSON := get(row, "time_rules_json"); timeRulesJSON != "" {
			if jsonErr := json.Unmarshal([]byte(timeRulesJSON), &rule.TimeRules); jsonErr != nil {
				failed++
				failures = append(failures, fmt.Sprintf("row %d: time_rules_json parse error: %v", rowNum, jsonErr))
				continue
			}
		}

		if err := normalizePricingRule(rule); err != nil {
			s.logger.Warn("import pricing rule failed", zap.Error(err))
			failed++
			continue
		}

		id := get(row, "id")
		if id != "" {
			if existing, err := s.GetRule(ctx, id); err == nil && existing != nil {
				rule.ID = id
				if err := s.UpdateRule(ctx, rule); err != nil {
					s.logger.Warn("import pricing rule update failed", zap.Error(err))
					failed++
					continue
				}
				updated++
				continue
			}
		}
		if _, err := s.CreateRule(ctx, rule); err != nil {
			s.logger.Warn("import pricing rule create failed", zap.Error(err))
			failed++
			continue
		}
		created++
	}

	return created, updated, failed, failures, nil
}

func parsePricingTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{time.RFC3339, time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported time format: %s", s)
}

func parseFloatOrZero(s string) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0
	}
	return v
}

// ProxyPricingView is the simplified pricing data sent to Proxy for pre-consume.
type ProxyPricingView struct {
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

// SyncViewForProxy returns a worst-case simplified price view for Proxy caching.
func (s *PricingService) SyncViewForProxy(ctx context.Context) ([]ProxyPricingView, error) {
	rules, err := s.ListRules(ctx, "", "", "", "", "active")
	if err != nil {
		return nil, err
	}

	// Group by (provider, region, model) and compute worst-case component prices.
	type key struct{ provider, region, model string }
	groups := map[key]*ProxyPricingView{}

	now := time.Now()
	for _, r := range rules {
		if r.EffectiveTo != nil && r.EffectiveTo.Before(now) {
			continue
		}
		if r.EffectiveFrom.After(now) {
			continue
		}

		model := r.APIModelName
		if model == "" {
			model = r.ProductID
		}
		if model == "" {
			continue
		}
		k := key{provider: r.Provider, region: r.Region, model: model}
		v, ok := groups[k]
		if !ok {
			v = &ProxyPricingView{
				Provider: r.Provider,
				Region:   r.Region,
				Model:    model,
			}
			groups[k] = v
		}

		// Update view with worst-case prices from flat / tiered / time-based rules.
		worst := s.worstCasePrices(r)
		v.InputPricePer1M = max(v.InputPricePer1M, worst.Input)
		v.OutputPricePer1M = max(v.OutputPricePer1M, worst.Output)
		v.CacheHitPricePer1M = max(v.CacheHitPricePer1M, worst.CacheHit)
		v.CacheWritePricePer1M = max(v.CacheWritePricePer1M, worst.CacheWrite)
		v.ReasoningPricePer1M = max(v.ReasoningPricePer1M, worst.Reasoning)
		v.FixedPrice = max(v.FixedPrice, worst.FixedRequest)
		v.BillingMode = r.BillingMode
	}

	views := make([]ProxyPricingView, 0, len(groups))
	for _, v := range groups {
		views = append(views, *v)
	}
	return views, nil
}

func (s *PricingService) worstCasePrices(r *PricingRule) ComponentPrices {
	worst := r.Prices
	for _, tier := range r.Tiers {
		worst = maxComponentPrices(worst, tier.Prices)
	}
	for _, tr := range r.TimeRules {
		worst = maxComponentPrices(worst, tr.Prices)
	}
	// For media rules the sync view uses FixedRequest as the worst-case unit
	// price (most expensive variant) so pre-consume can freeze a safe amount.
	if r.BillingMode == "media" && len(r.Prices.Variants) > 0 {
		maxVariant := r.Prices.Variants[0].Price
		for _, v := range r.Prices.Variants[1:] {
			if v.Price > maxVariant {
				maxVariant = v.Price
			}
		}
		if maxVariant > worst.FixedRequest {
			worst.FixedRequest = maxVariant
		}
	}
	return worst
}

func maxComponentPrices(a, b ComponentPrices) ComponentPrices {
	return ComponentPrices{
		Input:        max(a.Input, b.Input),
		Output:       max(a.Output, b.Output),
		CacheHit:     max(a.CacheHit, b.CacheHit),
		CacheWrite:   max(a.CacheWrite, b.CacheWrite),
		Reasoning:    max(a.Reasoning, b.Reasoning),
		FixedRequest: max(a.FixedRequest, b.FixedRequest),
	}
}

func max(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// invalidateCache clears the in-memory rule cache.
func (s *PricingService) invalidateCache() {
	s.cacheMu.Lock()
	s.cache = make(map[string]*pricingCacheEntry)
	s.cacheMu.Unlock()
}

// Helpers for DB scanning and marshaling.

type scanner interface {
	Scan(dest ...interface{}) error
}

func scanPricingRule(s scanner) (*PricingRule, error) {
	var r PricingRule
	var pricesJSON, tiersJSON, timeRulesJSON []byte
	var productID, apiModelName, ruleName, note sql.NullString
	var effectiveTo sql.NullTime
	var blockSeconds sql.NullInt32

	err := s.Scan(
		&r.ID, &r.Provider, &r.Region, &productID, &apiModelName, &r.BillingMode,
		&pricesJSON, &tiersJSON, &timeRulesJSON, &r.Timezone, &r.Currency,
		&r.EffectiveFrom, &effectiveTo, &r.Priority, &r.Status, &ruleName, &note,
		&blockSeconds, &r.CreatedAt, &r.UpdatedAt,
	)
	if blockSeconds.Valid {
		r.BlockSeconds = int(blockSeconds.Int32)
	}
	if err != nil {
		return nil, err
	}

	r.ProductID = productID.String
	r.APIModelName = apiModelName.String
	r.RuleName = ruleName.String
	r.Note = note.String
	if effectiveTo.Valid {
		r.EffectiveTo = &effectiveTo.Time
	}

	if len(pricesJSON) > 0 {
		if err := json.Unmarshal(pricesJSON, &r.Prices); err != nil {
			return nil, fmt.Errorf("unmarshal prices: %w", err)
		}
	}
	if len(tiersJSON) > 0 {
		if err := json.Unmarshal(tiersJSON, &r.Tiers); err != nil {
			return nil, fmt.Errorf("unmarshal tiers: %w", err)
		}
	}
	if len(timeRulesJSON) > 0 {
		if err := json.Unmarshal(timeRulesJSON, &r.TimeRules); err != nil {
			return nil, fmt.Errorf("unmarshal time_rules: %w", err)
		}
	}

	return &r, nil
}

func marshalPricingRuleJSON(r *PricingRule) (prices, tiers, timeRules []byte, err error) {
	prices, err = json.Marshal(r.Prices)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("marshal prices: %w", err)
	}
	tiers, err = json.Marshal(r.Tiers)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("marshal tiers: %w", err)
	}
	timeRules, err = json.Marshal(r.TimeRules)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("marshal time_rules: %w", err)
	}
	return prices, tiers, timeRules, nil
}

// ValidateRuleAgainstUpstream checks that a pricing rule is not cheaper than
// the recorded upstream cost for the same model. It allows a tiny epsilon to
// avoid rejecting rules due to floating-point rounding.
func (s *PricingService) ValidateRuleAgainstUpstream(ctx context.Context, r *PricingRule) error {
	if r.Provider == "*" || r.Provider == "" {
		return nil
	}
	model := r.APIModelName
	if model == "" {
		model = r.ProductID
	}
	if model == "" {
		return nil
	}

	var upInputMiss, upInputHit, upOutput, upCacheWrite, upReasoning, upFixed sql.NullFloat64
	err := s.db.QueryRow(ctx,
		`SELECT input_price_cache_miss, input_price_cache_hit, output_price,
		        cache_write_price, reasoning_price, fixed_price
		 FROM service_models
		 WHERE provider = $1 AND deleted_at IS NULL
		   AND (product_id = $2 OR api_model_name = $2 OR product_id = $3 OR api_model_name = $3)
		 LIMIT 1`,
		r.Provider, r.ProductID, r.APIModelName,
	).Scan(&upInputMiss, &upInputHit, &upOutput, &upCacheWrite, &upReasoning, &upFixed)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("lookup upstream price: %w", err)
	}

	const eps = 1e-9
	checks := []struct {
		name       string
		downstream float64
		upstream   sql.NullFloat64
	}{
		{"input", r.Prices.Input, upInputMiss},
		{"cache_hit", r.Prices.CacheHit, upInputHit},
		{"output", r.Prices.Output, upOutput},
		{"cache_write", r.Prices.CacheWrite, upCacheWrite},
		{"reasoning", r.Prices.Reasoning, upReasoning},
		{"fixed_request", r.Prices.FixedRequest, upFixed},
	}
	for _, c := range checks {
		if !c.upstream.Valid {
			continue
		}
		if c.downstream < c.upstream.Float64-eps {
			return fmt.Errorf("price inversion on %s for %s/%s: downstream %.6f < upstream %.6f",
				c.name, r.Provider, model, c.downstream, c.upstream.Float64)
		}
	}
	return nil
}

func normalizePricingRule(r *PricingRule) error {
	r.Provider = strings.TrimSpace(r.Provider)
	r.Region = strings.TrimSpace(r.Region)
	if r.Provider == "" {
		r.Provider = "*"
	}
	if r.Region == "" {
		r.Region = "*"
	}
	r.BillingMode = strings.TrimSpace(r.BillingMode)
	if r.BillingMode == "" {
		r.BillingMode = "flat"
	}
	if r.Status == "" {
		r.Status = "active"
	}
	if r.Timezone == "" {
		r.Timezone = "UTC"
	}
	if r.Currency == "" {
		r.Currency = "USD"
	}
	if r.RuleName == "" {
		r.RuleName = fmt.Sprintf("%s/%s/%s", r.Provider, r.Region, coalesce(r.APIModelName, r.ProductID, "*"))
	}
	return validatePricingRule(r)
}

const (
	minPricingPriority = -1000
	maxPricingPriority = 1000
)

func validatePricingRule(r *PricingRule) error {
	if r.Priority < minPricingPriority || r.Priority > maxPricingPriority {
		return fmt.Errorf("priority must be between %d and %d", minPricingPriority, maxPricingPriority)
	}
	if r.BillingMode == "time_based" {
		if len(r.TimeRules) == 0 {
			return fmt.Errorf("time-based rules require at least one time window")
		}
		for i, tr := range r.TimeRules {
			if len(tr.Days) == 0 {
				return fmt.Errorf("time window %d: days are required (0=Sunday, 6=Saturday)", i+1)
			}
			for _, d := range tr.Days {
				if d < 0 || d > 6 {
					return fmt.Errorf("time window %d: invalid day %d, must be 0-6 (0=Sunday)", i+1, d)
				}
			}
			if tr.Start == "" || tr.End == "" {
				return fmt.Errorf("time window %d: start and end time are required", i+1)
			}
		}
	}
	if r.BillingMode == "tiered" {
		if len(r.Tiers) == 0 {
			return fmt.Errorf("tiered rules require at least one tier")
		}
	}
	return nil
}

func coalesce(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func strPtr(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func parseUsageTimestamp(ts string) (time.Time, error) {
	if ts == "" {
		return time.Now(), nil
	}
	// Try common formats.
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05"} {
		t, err := time.Parse(layout, ts)
		if err == nil {
			return t, nil
		}
	}
	// Try unix timestamp (seconds or milliseconds).
	if i, err := strconv.ParseInt(ts, 10, 64); err == nil {
		if i > 1e12 {
			return time.UnixMilli(i), nil
		}
		return time.Unix(i, 0), nil
	}
	return time.Time{}, fmt.Errorf("unsupported timestamp format: %s", ts)
}

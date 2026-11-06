// Package analytics provides background analysis of retention data
// for operational intelligence: model rankings, performance tracking,
// reliability metrics, and usage patterns.
package analytics

import (
	"math"
	"sort"
	"sync"
	"time"
)

// Report is the top-level analytics output regenerated periodically.
type Report struct {
	GeneratedAt  time.Time        `json:"generated_at"`
	PeriodStart  time.Time        `json:"period_start"`
	PeriodEnd    time.Time        `json:"period_end"`
	TotalRecords int              `json:"total_records"`
	Summary      Summary          `json:"summary"`
	Models       []ModelStats     `json:"models"`
	Performance  PerformanceStats `json:"performance"`
	Reliability  ReliabilityStats `json:"reliability"`
	Users        []UserStats      `json:"users"`
	Trends       TrendData        `json:"trends"`
}

// Summary provides top-level KPIs.
type Summary struct {
	TotalRequests     int     `json:"total_requests"`
	UniqueModels      int     `json:"unique_models"`
	UniqueUsers       int     `json:"unique_users"`
	AvgLatencyMs      float64 `json:"avg_latency_ms"`
	P95LatencyMs      float64 `json:"p95_latency_ms"`
	SuccessRate       float64 `json:"success_rate"`
	TotalInputTokens  int64   `json:"total_input_tokens"`
	TotalOutputTokens int64   `json:"total_output_tokens"`
	TotalCachedTokens int64   `json:"total_cached_tokens"`
	CacheHitRatio     float64 `json:"cache_hit_ratio"`
	FallbackRate      float64 `json:"fallback_rate"`
}

// ModelStats holds per-model analytics with a composite score.
type ModelStats struct {
	Model            string         `json:"model"`
	VendorModel      string         `json:"vendor_model,omitempty"`
	Provider         string         `json:"provider"`
	RequestCount     int            `json:"request_count"`
	SuccessRate      float64        `json:"success_rate"`
	AvgLatencyMs     float64        `json:"avg_latency_ms"`
	P95LatencyMs     float64        `json:"p95_latency_ms"`
	AvgFirstTokenMs  float64        `json:"avg_first_token_ms,omitempty"`
	TokensPerSecond  float64        `json:"tokens_per_second,omitempty"`
	AvgInputTokens   float64        `json:"avg_input_tokens"`
	AvgOutputTokens  float64        `json:"avg_output_tokens"`
	CacheHitRatio    float64        `json:"cache_hit_ratio"`
	FallbackRate     float64        `json:"fallback_rate"`
	StreamRatio      float64        `json:"stream_ratio"`
	PopularityScore  float64        `json:"popularity_score"`
	SpeedScore       float64        `json:"speed_score"`
	ReliabilityScore float64        `json:"reliability_score"`
	OverallScore     float64        `json:"overall_score"`
	FirstSeen        string         `json:"first_seen"`
	LastSeen         string         `json:"last_seen"`
	RequestTypes     map[string]int `json:"request_types"`
}

// PerformanceStats holds latency distribution data.
type PerformanceStats struct {
	OverallP50Ms float64            `json:"overall_p50_ms"`
	OverallP95Ms float64            `json:"overall_p95_ms"`
	OverallP99Ms float64            `json:"overall_p99_ms"`
	ByModel      map[string]Latency `json:"by_model"`
	ByHour       []HourlyPerf       `json:"by_hour"`
}

// Latency holds percentile latency data.
type Latency struct {
	P50Ms float64 `json:"p50_ms"`
	P95Ms float64 `json:"p95_ms"`
	P99Ms float64 `json:"p99_ms"`
	Count int     `json:"count"`
}

// HourlyPerf holds per-hour aggregated performance.
type HourlyPerf struct {
	Hour         string  `json:"hour"` // "2026-08-14T18"
	RequestCount int     `json:"request_count"`
	AvgLatencyMs float64 `json:"avg_latency_ms"`
	SuccessRate  float64 `json:"success_rate"`
}

// ReliabilityStats holds error and fallback analysis.
type ReliabilityStats struct {
	OverallSuccessRate float64            `json:"overall_success_rate"`
	FallbackRate       float64            `json:"fallback_rate"`
	ErrorsByCode       map[int]int        `json:"errors_by_code"`
	ErrorsByModel      map[string]float64 `json:"errors_by_model"` // model → error rate
	TopFallbackModels  []string           `json:"top_fallback_models"`
}

// UserStats holds per-user usage summary (anonymized by user_id hash prefix).
type UserStats struct {
	UserID       string         `json:"user_id"` // first 8 chars for privacy
	RequestCount int            `json:"request_count"`
	UniqueModels int            `json:"unique_models"`
	TopModel     string         `json:"top_model"`
	TotalTokens  int64          `json:"total_tokens"`
	AvgLatencyMs float64        `json:"avg_latency_ms"`
	RequestTypes map[string]int `json:"request_types"`
}

// TrendData holds time-series data for trend visualization.
type TrendData struct {
	DailyRequests []DailyPoint         `json:"daily_requests"`
	DailyTokens   []DailyPoint         `json:"daily_tokens"`
	ModelAdoption []ModelAdoptionPoint `json:"model_adoption"`
}

// DailyPoint is a single day's data point.
type DailyPoint struct {
	Date  string  `json:"date"` // "2026-08-14"
	Value float64 `json:"value"`
}

// ModelAdoptionPoint tracks when a model was first used.
type ModelAdoptionPoint struct {
	Model    string `json:"model"`
	FirstUse string `json:"first_use"`
	Provider string `json:"provider"`
}

// --- Helper: percentile calculation ---

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := p / 100.0 * float64(len(sorted)-1)
	lower := int(math.Floor(idx))
	upper := int(math.Ceil(idx))
	if lower == upper || upper >= len(sorted) {
		return sorted[lower]
	}
	frac := idx - float64(lower)
	return sorted[lower]*(1-frac) + sorted[upper]*frac
}

func sortedFloat64(vals []float64) []float64 {
	sort.Float64s(vals)
	return vals
}

// --- Thread-safe report holder ---

// ReportHolder provides thread-safe access to the latest report.
type ReportHolder struct {
	mu     sync.RWMutex
	report *Report
}

// Get returns the current report (may be nil if not yet generated).
func (h *ReportHolder) Get() *Report {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.report
}

// Set stores a new report.
func (h *ReportHolder) Set(r *Report) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.report = r
}

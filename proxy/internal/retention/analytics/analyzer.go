package analytics

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mayiants/ai-proxy-go/internal/retention"
	"go.uber.org/zap"
)

// Analyzer walks retention files and computes analytics.
type Analyzer struct {
	directory string
	maxAge    time.Duration // only analyze records within this window
	logger    *zap.Logger
}

// NewAnalyzer creates an analyzer that reads from the retention directory.
func NewAnalyzer(directory string, maxAge time.Duration, logger *zap.Logger) *Analyzer {
	if maxAge <= 0 {
		maxAge = 30 * 24 * time.Hour
	}
	return &Analyzer{
		directory: directory,
		maxAge:    maxAge,
		logger:    logger,
	}
}

// Analyze walks all retention files and produces a report.
// It processes files sequentially to bound memory usage.
func (a *Analyzer) Analyze() (*Report, error) {
	cutoff := time.Now().UTC().Add(-a.maxAge)

	// Accumulators
	var (
		totalRecords int
		records      []recordMeta // lightweight metadata only
		earliest     time.Time
		latest       time.Time
	)

	err := filepath.Walk(a.directory, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if info.IsDir() || !strings.HasSuffix(path, ".json.gz") {
			return nil
		}
		if info.ModTime().Before(cutoff) {
			return nil // skip old files
		}

		meta, err := a.readMetadata(path)
		if err != nil {
			a.logger.Debug("Skip unreadable retention file", zap.String("path", path), zap.Error(err))
			return nil
		}

		totalRecords++
		records = append(records, meta)

		if earliest.IsZero() || meta.receivedAt.Before(earliest) {
			earliest = meta.receivedAt
		}
		if meta.receivedAt.After(latest) {
			latest = meta.receivedAt
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk retention directory: %w", err)
	}

	if totalRecords == 0 {
		return &Report{
			GeneratedAt:  time.Now().UTC(),
			TotalRecords: 0,
		}, nil
	}

	report := a.computeReport(records, earliest, latest)
	report.TotalRecords = totalRecords
	return report, nil
}

// recordMeta is the in-memory representation during analysis (no body).
type recordMeta struct {
	traceID           string
	receivedAt        time.Time
	userID            string
	provider          string
	model             string
	vendorModel       string
	statusCode        int
	latencyMs         int64
	firstTokenLatency int64
	inputTokens       int
	outputTokens      int
	cachedTokens      int
	isStream          bool
	isFallback        bool
	requestType       string
}

// readMetadata reads only the metadata from a gzip record file.
func (a *Analyzer) readMetadata(path string) (recordMeta, error) {
	f, err := os.Open(path)
	if err != nil {
		return recordMeta{}, err
	}
	defer func() { _ = f.Close() }()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return recordMeta{}, err
	}
	defer func() { _ = gz.Close() }()

	var rec retention.Record
	if err := json.NewDecoder(gz).Decode(&rec); err != nil {
		return recordMeta{}, err
	}

	receivedAt, _ := time.Parse(time.RFC3339Nano, rec.ReceivedAt)

	return recordMeta{
		traceID:           rec.TraceID,
		receivedAt:        receivedAt,
		userID:            rec.Metadata.UserID,
		provider:          rec.Metadata.Provider,
		model:             rec.Metadata.Model,
		vendorModel:       rec.Metadata.VendorModelName,
		statusCode:        rec.Metadata.StatusCode,
		latencyMs:         rec.Metadata.LatencyMs,
		firstTokenLatency: rec.Metadata.FirstTokenLatency,
		inputTokens:       rec.Metadata.InputTokens,
		outputTokens:      rec.Metadata.OutputTokens,
		cachedTokens:      rec.Metadata.CachedTokens,
		isStream:          rec.Metadata.IsStream,
		isFallback:        rec.Metadata.IsFallback,
		requestType:       rec.Metadata.RequestType,
	}, nil
}

// computeReport builds the full report from collected metadata.
func (a *Analyzer) computeReport(records []recordMeta, earliest, latest time.Time) *Report {
	report := &Report{
		GeneratedAt: time.Now().UTC(),
		PeriodStart: earliest,
		PeriodEnd:   latest,
	}

	// --- Summary ---
	report.Summary = a.computeSummary(records)

	// --- Model stats ---
	report.Models = a.computeModelStats(records)

	// --- Performance ---
	report.Performance = a.computePerformance(records)

	// --- Reliability ---
	report.Reliability = a.computeReliability(records)

	// --- Users ---
	report.Users = a.computeUserStats(records)

	// --- Trends ---
	report.Trends = a.computeTrends(records)

	return report
}

func (a *Analyzer) computeSummary(records []recordMeta) Summary {
	s := Summary{
		TotalRequests: len(records),
	}

	models := map[string]bool{}
	users := map[string]bool{}
	var latencies []float64
	var successCount, fallbackCount int
	var totalInput, totalOutput, totalCached int64

	for _, r := range records {
		models[r.model] = true
		if r.userID != "" {
			users[r.userID] = true
		}
		latencies = append(latencies, float64(r.latencyMs))
		if r.statusCode >= 200 && r.statusCode < 400 {
			successCount++
		}
		if r.isFallback {
			fallbackCount++
		}
		totalInput += int64(r.inputTokens)
		totalOutput += int64(r.outputTokens)
		totalCached += int64(r.cachedTokens)
	}

	s.UniqueModels = len(models)
	s.UniqueUsers = len(users)
	s.TotalInputTokens = totalInput
	s.TotalOutputTokens = totalOutput
	s.TotalCachedTokens = totalCached

	if len(latencies) > 0 {
		var sum float64
		for _, l := range latencies {
			sum += l
		}
		s.AvgLatencyMs = sum / float64(len(latencies))
		s.P95LatencyMs = percentile(sortedFloat64(latencies), 95)
	}
	if len(records) > 0 {
		s.SuccessRate = float64(successCount) / float64(len(records))
		s.FallbackRate = float64(fallbackCount) / float64(len(records))
	}
	if totalInput > 0 {
		s.CacheHitRatio = float64(totalCached) / float64(totalInput)
	}

	return s
}

func (a *Analyzer) computeModelStats(records []recordMeta) []ModelStats {
	type modelAcc struct {
		provider      string
		vendorModel   string
		count         int
		successCount  int
		fallbackCount int
		streamCount   int
		latencies     []float64
		firstTokens   []float64
		inputTokens   int64
		outputTokens  int64
		cachedTokens  int64
		firstSeen     time.Time
		lastSeen      time.Time
		requestTypes  map[string]int
	}

	acc := map[string]*modelAcc{}
	totalRequests := len(records)

	for _, r := range records {
		m, ok := acc[r.model]
		if !ok {
			m = &modelAcc{
				provider:     r.provider,
				vendorModel:  r.vendorModel,
				firstSeen:    r.receivedAt,
				requestTypes: map[string]int{},
			}
			acc[r.model] = m
		}
		m.count++
		if r.statusCode >= 200 && r.statusCode < 400 {
			m.successCount++
		}
		if r.isFallback {
			m.fallbackCount++
		}
		if r.isStream {
			m.streamCount++
		}
		m.latencies = append(m.latencies, float64(r.latencyMs))
		if r.firstTokenLatency > 0 {
			m.firstTokens = append(m.firstTokens, float64(r.firstTokenLatency))
		}
		m.inputTokens += int64(r.inputTokens)
		m.outputTokens += int64(r.outputTokens)
		m.cachedTokens += int64(r.cachedTokens)
		if r.receivedAt.Before(m.firstSeen) {
			m.firstSeen = r.receivedAt
		}
		if r.receivedAt.After(m.lastSeen) {
			m.lastSeen = r.receivedAt
		}
		if r.requestType != "" {
			m.requestTypes[r.requestType]++
		}
	}

	var result []ModelStats
	for model, m := range acc {
		ms := ModelStats{
			Model:        model,
			VendorModel:  m.vendorModel,
			Provider:     m.provider,
			RequestCount: m.count,
			RequestTypes: m.requestTypes,
			FirstSeen:    m.firstSeen.Format("2006-01-02"),
			LastSeen:     m.lastSeen.Format("2006-01-02"),
		}

		if m.count > 0 {
			ms.SuccessRate = float64(m.successCount) / float64(m.count)
			ms.FallbackRate = float64(m.fallbackCount) / float64(m.count)
			ms.StreamRatio = float64(m.streamCount) / float64(m.count)
			ms.AvgInputTokens = float64(m.inputTokens) / float64(m.count)
			ms.AvgOutputTokens = float64(m.outputTokens) / float64(m.count)
		}
		if m.inputTokens > 0 {
			ms.CacheHitRatio = float64(m.cachedTokens) / float64(m.inputTokens)
		}

		if len(m.latencies) > 0 {
			var sum float64
			for _, l := range m.latencies {
				sum += l
			}
			ms.AvgLatencyMs = sum / float64(len(m.latencies))
			ms.P95LatencyMs = percentile(sortedFloat64(m.latencies), 95)
		}
		if len(m.firstTokens) > 0 {
			var sum float64
			for _, ft := range m.firstTokens {
				sum += ft
			}
			ms.AvgFirstTokenMs = sum / float64(len(m.firstTokens))
		}

		// Tokens per second: output_tokens / (latency - first_token_latency)
		if m.outputTokens > 0 && ms.AvgLatencyMs > ms.AvgFirstTokenMs && ms.AvgFirstTokenMs > 0 {
			genTimeMs := ms.AvgLatencyMs - ms.AvgFirstTokenMs
			if genTimeMs > 0 {
				ms.TokensPerSecond = ms.AvgOutputTokens / (genTimeMs / 1000.0)
			}
		}

		// Popularity: share of total requests
		if totalRequests > 0 {
			ms.PopularityScore = float64(m.count) / float64(totalRequests) * 100
		}

		// Speed score: normalized tokens/sec (higher = better, scale 0-100)
		ms.SpeedScore = math.Min(ms.TokensPerSecond/200.0*100, 100)

		// Reliability score: success rate × 100
		ms.ReliabilityScore = ms.SuccessRate * 100

		// Overall: weighted composite
		ms.OverallScore = ms.SpeedScore*0.3 + ms.ReliabilityScore*0.4 + ms.PopularityScore*0.3

		result = append(result, ms)
	}

	// Sort by overall score descending
	sort.Slice(result, func(i, j int) bool {
		return result[i].OverallScore > result[j].OverallScore
	})

	return result
}

func (a *Analyzer) computePerformance(records []recordMeta) PerformanceStats {
	ps := PerformanceStats{
		ByModel: map[string]Latency{},
	}

	var allLatencies []float64
	modelLatencies := map[string][]float64{}
	hourlyAcc := map[string]*struct {
		count   int
		sumLat  float64
		success int
	}{}

	for _, r := range records {
		allLatencies = append(allLatencies, float64(r.latencyMs))
		modelLatencies[r.model] = append(modelLatencies[r.model], float64(r.latencyMs))

		hour := r.receivedAt.Format("2006-01-02T15")
		h, ok := hourlyAcc[hour]
		if !ok {
			h = &struct {
				count   int
				sumLat  float64
				success int
			}{}
			hourlyAcc[hour] = h
		}
		h.count++
		h.sumLat += float64(r.latencyMs)
		if r.statusCode >= 200 && r.statusCode < 400 {
			h.success++
		}
	}

	if len(allLatencies) > 0 {
		sorted := sortedFloat64(allLatencies)
		ps.OverallP50Ms = percentile(sorted, 50)
		ps.OverallP95Ms = percentile(sorted, 95)
		ps.OverallP99Ms = percentile(sorted, 99)
	}

	for model, lats := range modelLatencies {
		sorted := sortedFloat64(lats)
		ps.ByModel[model] = Latency{
			P50Ms: percentile(sorted, 50),
			P95Ms: percentile(sorted, 95),
			P99Ms: percentile(sorted, 99),
			Count: len(lats),
		}
	}

	// Sort hours
	var hours []string
	for h := range hourlyAcc {
		hours = append(hours, h)
	}
	sort.Strings(hours)
	for _, h := range hours {
		acc := hourlyAcc[h]
		hp := HourlyPerf{
			Hour:         h,
			RequestCount: acc.count,
		}
		if acc.count > 0 {
			hp.AvgLatencyMs = acc.sumLat / float64(acc.count)
			hp.SuccessRate = float64(acc.success) / float64(acc.count)
		}
		ps.ByHour = append(ps.ByHour, hp)
	}

	return ps
}

func (a *Analyzer) computeReliability(records []recordMeta) ReliabilityStats {
	rs := ReliabilityStats{
		ErrorsByCode:  map[int]int{},
		ErrorsByModel: map[string]float64{},
	}

	var successCount, fallbackCount int
	modelErrors := map[string][2]int{} // [total, errors]

	for _, r := range records {
		if r.statusCode >= 200 && r.statusCode < 400 {
			successCount++
		} else {
			rs.ErrorsByCode[r.statusCode]++
		}
		if r.isFallback {
			fallbackCount++
		}

		me := modelErrors[r.model]
		me[0]++
		if r.statusCode < 200 || r.statusCode >= 400 {
			me[1]++
		}
		modelErrors[r.model] = me
	}

	if len(records) > 0 {
		rs.OverallSuccessRate = float64(successCount) / float64(len(records))
		rs.FallbackRate = float64(fallbackCount) / float64(len(records))
	}

	for model, counts := range modelErrors {
		if counts[0] > 0 {
			rs.ErrorsByModel[model] = float64(counts[1]) / float64(counts[0])
		}
	}

	// Top fallback models
	type fbEntry struct {
		model string
		count int
	}
	var fbModels []fbEntry
	modelFallback := map[string]int{}
	for _, r := range records {
		if r.isFallback {
			modelFallback[r.model]++
		}
	}
	for m, c := range modelFallback {
		fbModels = append(fbModels, fbEntry{m, c})
	}
	sort.Slice(fbModels, func(i, j int) bool { return fbModels[i].count > fbModels[j].count })
	for i, fb := range fbModels {
		if i >= 5 {
			break
		}
		rs.TopFallbackModels = append(rs.TopFallbackModels, fb.model)
	}

	return rs
}

func (a *Analyzer) computeUserStats(records []recordMeta) []UserStats {
	type userAcc struct {
		count      int
		models     map[string]int
		tokens     int64
		sumLatency float64
		reqTypes   map[string]int
	}
	users := map[string]*userAcc{}

	for _, r := range records {
		if r.userID == "" {
			continue
		}
		u, ok := users[r.userID]
		if !ok {
			u = &userAcc{
				models:   map[string]int{},
				reqTypes: map[string]int{},
			}
			users[r.userID] = u
		}
		u.count++
		u.models[r.model]++
		u.tokens += int64(r.inputTokens) + int64(r.outputTokens)
		u.sumLatency += float64(r.latencyMs)
		if r.requestType != "" {
			u.reqTypes[r.requestType]++
		}
	}

	var result []UserStats
	for uid, u := range users {
		// Anonymize: show only first 8 chars of user_id
		displayID := uid
		if len(uid) > 8 {
			displayID = uid[:8]
		}

		// Find top model
		var topModel string
		var topCount int
		for m, c := range u.models {
			if c > topCount {
				topModel = m
				topCount = c
			}
		}

		us := UserStats{
			UserID:       displayID,
			RequestCount: u.count,
			UniqueModels: len(u.models),
			TopModel:     topModel,
			TotalTokens:  u.tokens,
			RequestTypes: u.reqTypes,
		}
		if u.count > 0 {
			us.AvgLatencyMs = u.sumLatency / float64(u.count)
		}
		result = append(result, us)
	}

	// Sort by request count descending
	sort.Slice(result, func(i, j int) bool {
		return result[i].RequestCount > result[j].RequestCount
	})

	// Cap at top 20 users
	if len(result) > 20 {
		result = result[:20]
	}

	return result
}

func (a *Analyzer) computeTrends(records []recordMeta) TrendData {
	td := TrendData{}

	dailyReqs := map[string]int{}
	dailyTokens := map[string]int64{}
	modelFirstSeen := map[string]time.Time{}

	for _, r := range records {
		day := r.receivedAt.Format("2006-01-02")
		dailyReqs[day]++
		dailyTokens[day] += int64(r.inputTokens) + int64(r.outputTokens)

		if first, ok := modelFirstSeen[r.model]; !ok || r.receivedAt.Before(first) {
			modelFirstSeen[r.model] = r.receivedAt
		}
	}

	// Sort days
	var days []string
	for d := range dailyReqs {
		days = append(days, d)
	}
	sort.Strings(days)

	for _, d := range days {
		td.DailyRequests = append(td.DailyRequests, DailyPoint{Date: d, Value: float64(dailyReqs[d])})
		td.DailyTokens = append(td.DailyTokens, DailyPoint{Date: d, Value: float64(dailyTokens[d])})
	}

	// Model adoption timeline
	type adoption struct {
		model    string
		firstUse time.Time
	}
	var adoptions []adoption
	for m, t := range modelFirstSeen {
		adoptions = append(adoptions, adoption{m, t})
	}
	sort.Slice(adoptions, func(i, j int) bool { return adoptions[i].firstUse.Before(adoptions[j].firstUse) })
	for _, ad := range adoptions {
		// Find provider from records
		provider := ""
		for _, r := range records {
			if r.model == ad.model {
				provider = r.provider
				break
			}
		}
		td.ModelAdoption = append(td.ModelAdoption, ModelAdoptionPoint{
			Model:    ad.model,
			FirstUse: ad.firstUse.Format("2006-01-02"),
			Provider: provider,
		})
	}

	return td
}

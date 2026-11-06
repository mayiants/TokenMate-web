package routing

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// BudgetController tracks daily cost expenditure and triggers model downgrade
// when spending approaches the configured budget limit. This prevents runaway
// costs from high-traffic periods or expensive model overuse.
//
// When the budget threshold is exceeded:
//   - High-cost models (opus, pro, o3) are downgraded to mid-tier (sonnet, mini)
//   - Low-cost models continue unchanged
//   - Budget resets daily at midnight UTC
type BudgetController struct {
	// Configuration
	dailyBudgetMicroUSD int64   // Daily budget in micro-USD (0 = disabled)
	warningThreshold    float64 // Fraction at which to start warning (e.g. 0.7)
	downgradeThreshold  float64 // Fraction at which to downgrade models (e.g. 0.85)

	// Dynamic configuration (set via Portal admin sync)
	downgradeRules         map[string]string    // model → cheaper alternative
	defaultDowngrade       string               // fallback downgrade target
	expensiveThresholdPerM float64              // input price $/M above which model is expensive
	pricingLookup          func(string) float64 // lookup model input price

	// State
	todaySpent atomic.Int64 // Today's accumulated cost in micro-USD
	lastReset  time.Time
	mu         sync.Mutex
}

// BudgetConfig holds the configuration for the budget controller.
type BudgetConfig struct {
	DailyBudgetUSD     float64 // Daily budget in USD (e.g. 50.0 = $50/day)
	WarningThreshold   float64 // Warning at this fraction (default 0.7)
	DowngradeThreshold float64 // Downgrade at this fraction (default 0.85)
}

// BudgetStatus represents the current budget state.
type BudgetStatus struct {
	DailyBudgetMicroUSD int64   `json:"daily_budget_micro_usd"`
	TodaySpentMicroUSD  int64   `json:"today_spent_micro_usd"`
	UtilizationPct      float64 `json:"utilization_pct"`
	IsWarning           bool    `json:"is_warning"`
	IsDowngraded        bool    `json:"is_downgraded"`
	ResetsAt            string  `json:"resets_at"`
}

// NewBudgetController creates a new budget controller.
// Pass DailyBudgetUSD=0 to disable budget control.
func NewBudgetController(cfg BudgetConfig) *BudgetController {
	if cfg.WarningThreshold <= 0 {
		cfg.WarningThreshold = 0.7
	}
	if cfg.DowngradeThreshold <= 0 {
		cfg.DowngradeThreshold = 0.85
	}

	bc := &BudgetController{
		dailyBudgetMicroUSD:    int64(cfg.DailyBudgetUSD * 1_000_000),
		warningThreshold:       cfg.WarningThreshold,
		downgradeThreshold:     cfg.DowngradeThreshold,
		downgradeRules:         make(map[string]string),
		expensiveThresholdPerM: 10.0, // default: models > $10/M input are expensive
		lastReset:              todayMidnight(),
	}
	return bc
}

// RecordCost adds a cost to today's spending.
func (bc *BudgetController) RecordCost(microUSD int64) {
	if bc == nil || bc.dailyBudgetMicroUSD <= 0 {
		return
	}
	bc.checkReset()
	bc.todaySpent.Add(microUSD)
}

// ShouldDowngrade returns true if the daily budget threshold has been exceeded
// and the given model should be downgraded to a cheaper alternative.
func (bc *BudgetController) ShouldDowngrade(model string) bool {
	if bc == nil || bc.dailyBudgetMicroUSD <= 0 {
		return false
	}
	bc.checkReset()

	spent := bc.todaySpent.Load()
	threshold := int64(float64(bc.dailyBudgetMicroUSD) * bc.downgradeThreshold)

	if spent < threshold {
		return false
	}

	// Check if model is expensive based on pricing data or configured rules
	return bc.isExpensive(model)
}

// GetDowngradeModel returns a cheaper alternative for an expensive model.
// Uses configured downgrade rules. Returns original if no downgrade needed.
func (bc *BudgetController) GetDowngradeModel(model string) string {
	if bc == nil || !bc.ShouldDowngrade(model) {
		return model
	}

	// Check configured downgrade map first
	bc.mu.Lock()
	alt, ok := bc.downgradeRules[model]
	bc.mu.Unlock()
	if ok && alt != "" {
		return alt
	}

	// Fallback to default model if configured
	if bc.defaultDowngrade != "" {
		return bc.defaultDowngrade
	}

	return model
}

// SetDowngradeRules configures the model downgrade map.
// This should be called with data from Portal admin configuration.
func (bc *BudgetController) SetDowngradeRules(rules map[string]string) {
	if bc == nil {
		return
	}
	bc.mu.Lock()
	bc.downgradeRules = rules
	bc.mu.Unlock()
}

// SetExpensiveThreshold sets the input price threshold ($/M) above which a model
// is considered expensive. Models with input_price > threshold get downgraded.
func (bc *BudgetController) SetExpensiveThreshold(pricePerMillion float64) {
	if bc == nil {
		return
	}
	bc.expensiveThresholdPerM = pricePerMillion
}

// SetDefaultDowngrade sets the default model to downgrade to when no specific rule exists.
func (bc *BudgetController) SetDefaultDowngrade(model string) {
	if bc == nil {
		return
	}
	bc.defaultDowngrade = model
}

// SetPricingLookup sets the function used to look up model input price.
// This connects the budget controller to the billing pricing cache.
func (bc *BudgetController) SetPricingLookup(fn func(model string) float64) {
	if bc == nil {
		return
	}
	bc.pricingLookup = fn
}

// isExpensive checks if a model is expensive based on pricing data.
func (bc *BudgetController) isExpensive(model string) bool {
	if bc.pricingLookup != nil && bc.expensiveThresholdPerM > 0 {
		price := bc.pricingLookup(model)
		return price >= bc.expensiveThresholdPerM
	}
	// Fallback: pattern-based detection (for when pricing is not available)
	return isExpensiveByPattern(model)
}

// Status returns the current budget status.
func (bc *BudgetController) Status() BudgetStatus {
	if bc == nil || bc.dailyBudgetMicroUSD <= 0 {
		return BudgetStatus{}
	}
	bc.checkReset()

	spent := bc.todaySpent.Load()
	utilization := float64(spent) / float64(bc.dailyBudgetMicroUSD)

	return BudgetStatus{
		DailyBudgetMicroUSD: bc.dailyBudgetMicroUSD,
		TodaySpentMicroUSD:  spent,
		UtilizationPct:      utilization * 100,
		IsWarning:           utilization >= bc.warningThreshold,
		IsDowngraded:        utilization >= bc.downgradeThreshold,
		ResetsAt:            tomorrowMidnight().Format(time.RFC3339),
	}
}

// IsEnabled returns whether budget control is active.
func (bc *BudgetController) IsEnabled() bool {
	return bc != nil && bc.dailyBudgetMicroUSD > 0
}

// checkReset resets the counter if a new day has started.
func (bc *BudgetController) checkReset() {
	today := todayMidnight()
	if today.After(bc.lastReset) {
		bc.mu.Lock()
		if today.After(bc.lastReset) {
			bc.todaySpent.Store(0)
			bc.lastReset = today
		}
		bc.mu.Unlock()
	}
}

// --- Pattern-based fallback (used when pricing data is not available) ---

// isExpensiveByPattern uses model name patterns to guess if a model is expensive.
// This is only used as a fallback when pricing lookup is not configured.
func isExpensiveByPattern(model string) bool {
	lower := toLower(model)
	patterns := []string{"opus", "o3", "o4", "premier", "5.5"}
	for _, p := range patterns {
		if contains(lower, p) {
			if p == "o3" || p == "o4" {
				if contains(lower, "mini") {
					continue
				}
			}
			return true
		}
	}
	if contains(lower, "pro") && !contains(lower, "mini") && !contains(lower, "micro") {
		return true
	}
	return false
}

// --- Helpers ---

func toLower(s string) string {
	return strings.ToLower(s)
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

func todayMidnight() time.Time {
	now := time.Now().UTC()
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
}

func tomorrowMidnight() time.Time {
	return todayMidnight().Add(24 * time.Hour)
}

package routing

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBudgetController_Disabled(t *testing.T) {
	bc := NewBudgetController(BudgetConfig{DailyBudgetUSD: 0})
	assert.False(t, bc.IsEnabled())
	assert.False(t, bc.ShouldDowngrade("claude-opus-4-6"))
	assert.Equal(t, "claude-opus-4-6", bc.GetDowngradeModel("claude-opus-4-6"))
}

func TestBudgetController_UnderBudget(t *testing.T) {
	bc := NewBudgetController(BudgetConfig{DailyBudgetUSD: 50.0})
	assert.True(t, bc.IsEnabled())

	// Spend $10 (under 85% threshold of $50 = $42.50)
	bc.RecordCost(10_000_000) // $10

	assert.False(t, bc.ShouldDowngrade("claude-opus-4-6"))
	assert.Equal(t, "claude-opus-4-6", bc.GetDowngradeModel("claude-opus-4-6"))
}

func TestBudgetController_OverThreshold_DowngradesExpensive(t *testing.T) {
	bc := NewBudgetController(BudgetConfig{DailyBudgetUSD: 50.0, DowngradeThreshold: 0.85})

	// Configure downgrade rules (normally from Portal admin)
	bc.SetDowngradeRules(map[string]string{
		"claude-opus-4-6": "claude-sonnet-4-6",
		"gpt-5.5":         "gpt-5.4-mini",
		"o3":              "gpt-5.4-mini",
	})
	bc.SetDefaultDowngrade("gpt-5.4-mini")

	// Spend $43 (over 85% of $50 = $42.50)
	bc.RecordCost(43_000_000)

	// Expensive models should be downgraded (pattern-based fallback)
	assert.True(t, bc.ShouldDowngrade("claude-opus-4-6"))
	assert.Equal(t, "claude-sonnet-4-6", bc.GetDowngradeModel("claude-opus-4-6"))

	assert.True(t, bc.ShouldDowngrade("gpt-5.5"))
	assert.Equal(t, "gpt-5.4-mini", bc.GetDowngradeModel("gpt-5.5"))

	assert.True(t, bc.ShouldDowngrade("o3"))
	assert.Equal(t, "gpt-5.4-mini", bc.GetDowngradeModel("o3"))
}

func TestBudgetController_CheapModelsNotDowngraded(t *testing.T) {
	bc := NewBudgetController(BudgetConfig{DailyBudgetUSD: 50.0, DowngradeThreshold: 0.85})
	bc.RecordCost(43_000_000) // Over threshold

	// Cheap models should NOT be downgraded
	assert.False(t, bc.ShouldDowngrade("gpt-5.4-mini"))
	assert.False(t, bc.ShouldDowngrade("claude-sonnet-4-6"))
	assert.False(t, bc.ShouldDowngrade("gemini-2.5-flash"))
	assert.False(t, bc.ShouldDowngrade("us.amazon.nova-lite-v1:0"))
}

func TestBudgetController_Status(t *testing.T) {
	bc := NewBudgetController(BudgetConfig{DailyBudgetUSD: 100.0, WarningThreshold: 0.7, DowngradeThreshold: 0.85})

	bc.RecordCost(75_000_000) // $75 of $100

	status := bc.Status()
	assert.Equal(t, int64(100_000_000), status.DailyBudgetMicroUSD)
	assert.Equal(t, int64(75_000_000), status.TodaySpentMicroUSD)
	assert.InDelta(t, 75.0, status.UtilizationPct, 0.1)
	assert.True(t, status.IsWarning)     // 75% > 70%
	assert.False(t, status.IsDowngraded) // 75% < 85%
}

func TestBudgetController_NilSafe(t *testing.T) {
	var bc *BudgetController
	assert.False(t, bc.IsEnabled())
	assert.False(t, bc.ShouldDowngrade("opus"))
	assert.Equal(t, "opus", bc.GetDowngradeModel("opus"))
	bc.RecordCost(1000) // Should not panic
}

func TestIsExpensiveByPattern(t *testing.T) {
	expensive := []string{"claude-opus-4-6", "gpt-5.5", "o3", "o3-pro", "gpt-5.4-pro", "us.amazon.nova-premier-v1:0"}
	cheap := []string{"gpt-5.4-mini", "claude-sonnet-4-6", "gemini-2.5-flash", "us.amazon.nova-lite-v1:0", "gpt-4o-mini"}

	for _, m := range expensive {
		assert.True(t, isExpensiveByPattern(m), "expected expensive: %s", m)
	}
	for _, m := range cheap {
		assert.False(t, isExpensiveByPattern(m), "expected cheap: %s", m)
	}
}

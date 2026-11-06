package billing

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ai-proxy/ai-proxy-go/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEstimateMediaCostMicroUSD(t *testing.T) {
	b := NewBilling()
	// Synced worst-case unit price: $0.50 per unit (e.g. per second).
	b.SetPricing("veo-3", "vertex", &types.PriceData{FixedPrice: 0.50})

	// Long videos must be estimated from the declared duration: 120s at
	// $0.50/s = $60 = 60,000,000 micro-USD.
	assert.Equal(t, int64(60_000_000), b.EstimateMediaCostMicroUSD("veo-3", "vertex", 120, 1.0))
	// Margin multiplies the freeze.
	assert.Equal(t, int64(72_000_000), b.EstimateMediaCostMicroUSD("veo-3", "vertex", 120, 1.2))
	// Missing duration bills as one unit, matching Portal media billing.
	assert.Equal(t, int64(500_000), b.EstimateMediaCostMicroUSD("veo-3", "vertex", 0, 1.0))
	// No synced media price: cannot estimate, gate fails open.
	assert.Equal(t, int64(0), b.EstimateMediaCostMicroUSD("unknown-model", "vertex", 120, 1.0))
}

// TestPreConsume_EstimatedCostOverride verifies that media endpoints can pass
// a per-unit estimate (image count / duration) instead of the token-based one.
func TestPreConsume_EstimatedCostOverride(t *testing.T) {
	var received map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&received))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"amount":60000000,"remaining":90000000}`))
	}))
	defer server.Close()

	b := NewBillingWithPortal(server.URL, "secret", time.Minute)
	resp := b.PreConsume(context.Background(), PreConsumeRequest{
		UserID:                "user-1",
		KeyHash:               "hash-1",
		Model:                 "veo-3",
		Provider:              "vertex",
		RequestID:             "pc-1",
		EstimatedCostMicroUSD: 60_000_000,
	})

	require.True(t, resp.Success)
	assert.Equal(t, int64(60_000_000), resp.Amount)
	require.NotNil(t, received)
	// The override reaches Portal as estimatedCost, not the token estimate (0 tokens).
	assert.Equal(t, float64(60_000_000), received["estimatedCost"])
}

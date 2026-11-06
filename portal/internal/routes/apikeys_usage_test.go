package routes

import (
	"testing"
)

// TestAPIKeyUsageMethodsExist is a compile-time check.
func TestAPIKeyUsageMethodsExist(t *testing.T) {
	_ = (*APIKeyRoutes).getKeyUsage
}

// TestAPIKeyUsageAggregationTests verifies basic aggregation logic patterns.
func TestAPIKeyUsageAggregationTests(t *testing.T) {
	// Simulate the aggregation logic in getKeyUsage
	tests := []struct {
		name          string
		inputTokens   []int
		outputTokens  []int
		costs         []float64
		latencies     []int // -1 means nil (no latency)
		wantTotalIn   int64
		wantTotalOut  int64
		wantTotalCost float64
		wantAvgLat    int
		wantCount     int
	}{
		{
			name:          "single record",
			inputTokens:   []int{100},
			outputTokens:  []int{50},
			costs:         []float64{0.005},
			latencies:     []int{200},
			wantTotalIn:   100,
			wantTotalOut:  50,
			wantTotalCost: 0.005,
			wantAvgLat:    200,
			wantCount:     1,
		},
		{
			name:          "multiple records",
			inputTokens:   []int{100, 200, 300},
			outputTokens:  []int{50, 100, 150},
			costs:         []float64{0.005, 0.010, 0.015},
			latencies:     []int{200, 300, 400},
			wantTotalIn:   600,
			wantTotalOut:  300,
			wantTotalCost: 0.03,
			wantAvgLat:    300,
			wantCount:     3,
		},
		{
			name:          "empty records",
			inputTokens:   []int{},
			outputTokens:  []int{},
			costs:         []float64{},
			latencies:     []int{},
			wantTotalIn:   0,
			wantTotalOut:  0,
			wantTotalCost: 0,
			wantAvgLat:    0,
			wantCount:     0,
		},
		{
			name:          "record with nil latency",
			inputTokens:   []int{100},
			outputTokens:  []int{50},
			costs:         []float64{0.005},
			latencies:     []int{-1}, // represents nil
			wantTotalIn:   100,
			wantTotalOut:  50,
			wantTotalCost: 0.005,
			wantAvgLat:    0,
			wantCount:     1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var totalInput, totalOutput int64
			var totalCost float64
			var totalLatency int64
			var count int

			for i := range tt.inputTokens {
				totalInput += int64(tt.inputTokens[i])
				totalOutput += int64(tt.outputTokens[i])
				totalCost += tt.costs[i]
				count++
				if tt.latencies[i] >= 0 {
					totalLatency += int64(tt.latencies[i])
				}
			}

			avgLatency := 0
			if count > 0 {
				avgLatency = int(totalLatency / int64(count))
			}

			if totalInput != tt.wantTotalIn {
				t.Errorf("totalInput = %d, want %d", totalInput, tt.wantTotalIn)
			}
			if totalOutput != tt.wantTotalOut {
				t.Errorf("totalOutput = %d, want %d", totalOutput, tt.wantTotalOut)
			}
			if totalCost != tt.wantTotalCost {
				t.Errorf("totalCost = %f, want %f", totalCost, tt.wantTotalCost)
			}
			if avgLatency != tt.wantAvgLat {
				t.Errorf("avgLatency = %d, want %d", avgLatency, tt.wantAvgLat)
			}
			if count != tt.wantCount {
				t.Errorf("count = %d, want %d", count, tt.wantCount)
			}
		})
	}
}

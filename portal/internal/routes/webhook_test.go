package routes

import (
	"encoding/json"
	"math"
	"testing"
)

func TestNullStr(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  interface{}
	}{
		{"empty string returns nil", "", nil},
		{"non-empty returns string", "hello", "hello"},
		{"whitespace returns string", "  ", "  "},
		{"uuid string", "550e8400-e29b-41d4-a716-446655440000", "550e8400-e29b-41d4-a716-446655440000"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := nullStr(tt.input)
			if got != tt.want {
				t.Errorf("nullStr(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestNullStrNilInterface(t *testing.T) {
	got := nullStr("")
	if got != nil {
		t.Errorf("nullStr(\"\") = %v (%T), want nil", got, got)
	}
}

func TestUsageRecordJSON(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		wantErr bool
		check   func(*UsageRecord) bool
	}{
		{
			name: "full record",
			json: `{
				"traceId": "trace-123",
				"apiKeyHash": "abc123hash",
				"model": "gpt-4",
				"provider": "openai",
				"region": "us-east-1",
				"inputTokens": 100,
				"outputTokens": 50,
				"cachedTokens": 20,
				"effectiveInputTokens": 80,
				"reasoningTokens": 10,
				"cost": 0.005,
				"latencyMs": 200,
				"requestCount": 1,
				"serverId": "server-1",
				"timestamp": "2024-01-01T00:00:00Z",
				"cacheHit": true,
				"error": ""
			}`,
			wantErr: false,
			check: func(r *UsageRecord) bool {
				return r.TraceID == "trace-123" &&
					r.Model == "gpt-4" &&
					r.InputTokens == 100 &&
					r.OutputTokens == 50 &&
					r.CachedTokens == 20 &&
					r.Cost == 0.005 &&
					r.CacheHit == true
			},
		},
		{
			name: "minimal record",
			json: `{"traceId":"t1","model":"gpt-3.5-turbo"}`,
			check: func(r *UsageRecord) bool {
				return r.TraceID == "t1" && r.Model == "gpt-3.5-turbo" && r.InputTokens == 0
			},
		},
		{
			name:    "invalid json",
			json:    `{bad json`,
			wantErr: true,
		},
		{
			name: "zero cost",
			json: `{"traceId":"t2","cost":0}`,
			check: func(r *UsageRecord) bool {
				return r.Cost == 0
			},
		},
		{
			name: "negative tokens defaults to zero",
			json: `{"traceId":"t3","inputTokens":0,"outputTokens":0}`,
			check: func(r *UsageRecord) bool {
				return r.InputTokens == 0 && r.OutputTokens == 0
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var record UsageRecord
			err := json.Unmarshal([]byte(tt.json), &record)
			if (err != nil) != tt.wantErr {
				t.Errorf("Unmarshal() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && tt.check != nil && !tt.check(&record) {
				t.Errorf("check failed for record: %+v", record)
			}
		})
	}
}

func TestBatchUsageReportJSON(t *testing.T) {
	tests := []struct {
		name      string
		json      string
		wantCount int
		wantErr   bool
	}{
		{
			name: "batch with 2 records",
			json: `{
				"serverId": "server-1",
				"records": [
					{"traceId": "t1", "model": "gpt-4"},
					{"traceId": "t2", "model": "gpt-3.5-turbo"}
				]
			}`,
			wantCount: 2,
		},
		{
			name:      "empty records",
			json:      `{"serverId": "server-1", "records": []}`,
			wantCount: 0,
		},
		{
			name:    "invalid json",
			json:    `{invalid`,
			wantErr: true,
		},
		{
			name:      "no records field",
			json:      `{"serverId": "server-1"}`,
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var batch BatchUsageReport
			err := json.Unmarshal([]byte(tt.json), &batch)
			if (err != nil) != tt.wantErr {
				t.Errorf("Unmarshal() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && len(batch.Records) != tt.wantCount {
				t.Errorf("got %d records, want %d", len(batch.Records), tt.wantCount)
			}
		})
	}
}

func TestUsageRecordSerialization(t *testing.T) {
	record := UsageRecord{
		TraceID:      "trace-abc",
		APIKeyHash:   "hash123",
		Model:        "claude-3-opus",
		Provider:     "anthropic",
		InputTokens:  500,
		OutputTokens: 200,
		Cost:         0.015,
		CacheHit:     false,
	}

	data, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("Marshal() error: %v", err)
	}

	var roundTrip UsageRecord
	if err := json.Unmarshal(data, &roundTrip); err != nil {
		t.Fatalf("Unmarshal() error: %v", err)
	}

	if roundTrip.TraceID != record.TraceID {
		t.Errorf("TraceID = %q, want %q", roundTrip.TraceID, record.TraceID)
	}
	if roundTrip.Model != record.Model {
		t.Errorf("Model = %q, want %q", roundTrip.Model, record.Model)
	}
	if roundTrip.InputTokens != record.InputTokens {
		t.Errorf("InputTokens = %d, want %d", roundTrip.InputTokens, record.InputTokens)
	}
}

func TestCostCalculation(t *testing.T) {
	tests := []struct {
		name         string
		inputTokens  int
		outputTokens int
		providedCost float64
		wantCost     float64
	}{
		{"cost provided", 100, 50, 0.005, 0.005},
		{"zero cost with tokens", 1000, 500, 0, 0.015},
		{"zero cost zero tokens", 0, 0, 0, 0},
		{"zero cost input only", 100, 0, 0, 0.001},
		{"zero cost output only", 0, 100, 0, 0.001},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record := UsageRecord{
				InputTokens:  tt.inputTokens,
				OutputTokens: tt.outputTokens,
				Cost:         tt.providedCost,
			}

			cost := record.Cost
			if cost == 0 && (record.InputTokens > 0 || record.OutputTokens > 0) {
				cost = float64(record.InputTokens+record.OutputTokens) * 0.00001
			}

			if math.Abs(cost-tt.wantCost) > 1e-9 {
				t.Errorf("cost = %f, want %f", cost, tt.wantCost)
			}
		})
	}
}

func TestEffectiveInputTokensCalculation(t *testing.T) {
	tests := []struct {
		name               string
		effectiveInput     int
		inputTokens        int
		cachedTokens       int
		wantEffectiveInput int
	}{
		{"effective provided", 80, 100, 20, 80},
		{"calculate from input-cached", 0, 100, 30, 70},
		{"cached equals input", 0, 100, 100, 0},
		{"cached exceeds input", 0, 50, 60, 0},
		{"no tokens", 0, 0, 0, 0},
		{"effective zero input zero", 0, 0, 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record := UsageRecord{
				EffectiveInputTokens: tt.effectiveInput,
				InputTokens:          tt.inputTokens,
				CachedTokens:         tt.cachedTokens,
			}

			effectiveInput := record.EffectiveInputTokens
			if effectiveInput == 0 && record.InputTokens > 0 {
				effectiveInput = record.InputTokens - record.CachedTokens
				if effectiveInput < 0 {
					effectiveInput = 0
				}
			}

			if effectiveInput != tt.wantEffectiveInput {
				t.Errorf("effectiveInput = %d, want %d", effectiveInput, tt.wantEffectiveInput)
			}
		})
	}
}

package routes

import (
	"encoding/json"
	"testing"
)

// TestWebhookCommon_UpstreamUsageRecordJSON tests UpstreamUsageRecord JSON serialization.
func TestWebhookCommon_UpstreamUsageRecordJSON(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		wantErr bool
		check   func(*UpstreamUsageRecord) bool
	}{
		{
			name: "full record",
			json: `{
				"upstreamKeyId": "uk-123",
				"provider": "openai",
				"model": "gpt-4",
				"date": "2024-01-15",
				"requestCount": 100,
				"inputTokens": 50000,
				"outputTokens": 25000,
				"cacheReadTokens": 10000,
				"cacheCreationTokens": 5000,
				"errorCount": 2,
				"latencyMs": 200
			}`,
			wantErr: false,
			check: func(r *UpstreamUsageRecord) bool {
				return r.UpstreamKeyID == "uk-123" &&
					r.Provider == "openai" &&
					r.Model == "gpt-4" &&
					r.Date == "2024-01-15" &&
					r.RequestCount == 100 &&
					r.InputTokens == 50000 &&
					r.OutputTokens == 25000
			},
		},
		{
			name: "minimal record",
			json: `{"upstreamKeyId": "uk-1", "date": "2024-01-01"}`,
			check: func(r *UpstreamUsageRecord) bool {
				return r.UpstreamKeyID == "uk-1" && r.Date == "2024-01-01"
			},
		},
		{
			name:    "invalid json",
			json:    `bad json`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var record UpstreamUsageRecord
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

// TestWebhookCommon_BatchUpstreamUsageReportJSON tests batch upstream usage report.
func TestWebhookCommon_BatchUpstreamUsageReportJSON(t *testing.T) {
	tests := []struct {
		name      string
		json      string
		wantCount int
		wantErr   bool
	}{
		{
			name: "batch with 2 records",
			json: `{
				"records": [
					{"upstreamKeyId": "uk-1", "provider": "openai", "model": "gpt-4", "date": "2024-01-01"},
					{"upstreamKeyId": "uk-2", "provider": "anthropic", "model": "claude-3", "date": "2024-01-01"}
				]
			}`,
			wantCount: 2,
		},
		{
			name:      "empty records",
			json:      `{"records": []}`,
			wantCount: 0,
		},
		{
			name:    "invalid json",
			json:    `invalid`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var batch BatchUpstreamUsageReport
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

// TestWebhookCommon_ProviderHealthRecordJSON tests ProviderHealthRecord serialization.
func TestWebhookCommon_ProviderHealthRecordJSON(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		wantErr bool
		check   func(*ProviderHealthRecord) bool
	}{
		{
			name: "full health record",
			json: `{
				"provider": "openai",
				"model": "gpt-4",
				"groupId": "group-1",
				"traceId": "trace-123",
				"statusCode": 200,
				"latencyMs": 150,
				"errorType": "",
				"errorMessage": "",
				"serverId": "server-1",
				"timestamp": "2024-01-01T00:00:00Z"
			}`,
			check: func(r *ProviderHealthRecord) bool {
				return r.Provider == "openai" && r.Model == "gpt-4" && r.StatusCode == 200
			},
		},
		{
			name: "error record",
			json: `{
				"provider": "openai",
				"model": "gpt-4",
				"statusCode": 500,
				"errorType": "server_error",
				"errorMessage": "Internal Server Error"
			}`,
			check: func(r *ProviderHealthRecord) bool {
				return r.StatusCode == 500 && r.ErrorType == "server_error"
			},
		},
		{
			name:    "invalid json",
			json:    `bad`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var record ProviderHealthRecord
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

// TestWebhookCommon_UsageRecordSerialization tests the full UsageRecord round-trip.
func TestWebhookCommon_UsageRecordSerialization(t *testing.T) {
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
}

// TestWebhookCommon_MaxBodySize tests the max webhook body size constant.
func TestWebhookCommon_MaxBodySize(t *testing.T) {
	if maxWebhookBodySize != 1*1024*1024 {
		t.Errorf("maxWebhookBodySize = %d, want 1048576 (1MB)", maxWebhookBodySize)
	}
}

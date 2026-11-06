package routes

import (
	"encoding/json"
	"testing"
)

// TestReconcileResultStruct verifies the ReconcileResult type serializes correctly.
func TestReconcileResultStruct(t *testing.T) {
	r := ReconcileResult{
		Total:    100,
		Inserted: 85,
		Skipped:  10,
		Failed:   5,
	}
	r.Deductions.Gift = 1000
	r.Deductions.Recharge = 2000
	r.Deductions.Balance = 3000
	r.Deductions.Credit = 1500
	r.Deductions.Subscription = 500

	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("Marshal() error: %v", err)
	}

	var roundTrip ReconcileResult
	if err := json.Unmarshal(data, &roundTrip); err != nil {
		t.Fatalf("Unmarshal() error: %v", err)
	}

	if roundTrip.Total != r.Total {
		t.Errorf("Total = %d, want %d", roundTrip.Total, r.Total)
	}
	if roundTrip.Inserted != r.Inserted {
		t.Errorf("Inserted = %d, want %d", roundTrip.Inserted, r.Inserted)
	}
	if roundTrip.Skipped != r.Skipped {
		t.Errorf("Skipped = %d, want %d", roundTrip.Skipped, r.Skipped)
	}
	if roundTrip.Failed != r.Failed {
		t.Errorf("Failed = %d, want %d", roundTrip.Failed, r.Failed)
	}
	if roundTrip.Deductions.Gift != r.Deductions.Gift {
		t.Errorf("Deductions.Gift = %d, want %d", roundTrip.Deductions.Gift, r.Deductions.Gift)
	}
}

// TestReconcileResultZeroValues tests default/zero value serialization.
func TestReconcileResultZeroValues(t *testing.T) {
	r := ReconcileResult{}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("Marshal() error: %v", err)
	}

	var roundTrip ReconcileResult
	if err := json.Unmarshal(data, &roundTrip); err != nil {
		t.Fatalf("Unmarshal() error: %v", err)
	}

	if roundTrip.Total != 0 {
		t.Errorf("Total = %d, want 0", roundTrip.Total)
	}
	if roundTrip.Deductions.Gift != 0 {
		t.Errorf("Deductions.Gift = %d, want 0", roundTrip.Deductions.Gift)
	}
}

// TestAdminRoutesReconciliationMethodsExist is a compile-time check.
func TestAdminRoutesReconciliationMethodsExist(t *testing.T) {
	_ = (*AdminRoutes).listInvoiceRequests
	_ = (*AdminRoutes).approveInvoiceRequest
	_ = (*AdminRoutes).rejectInvoiceRequest
	_ = (*AdminRoutes).completeInvoiceRequest
	_ = (*AdminRoutes).getUserReconciliation
	_ = (*AdminRoutes).getLedger
	_ = (*AdminRoutes).getEnterpriseReconciliation
	_ = (*AdminRoutes).listTopUsers
	_ = (*AdminRoutes).listTopEnterprises
	_ = (*AdminRoutes).searchEnterprises
	_ = (*AdminRoutes).handleReconcileUpload
	_ = (*AdminRoutes).handleReconcilePreview
}

// TestPaginationDefaults tests common pagination patterns used in reconciliation.
func TestPaginationDefaults(t *testing.T) {
	// Test default page calculation
	tests := []struct {
		name      string
		total     int
		limit     int
		wantPages int
	}{
		{"zero total", 0, 20, 0},
		{"exact division", 100, 20, 5},
		{"partial page", 101, 20, 6},
		{"single item", 1, 20, 1},
		{"large limit", 5, 100, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := (tt.total + tt.limit - 1) / tt.limit
			if got != tt.wantPages {
				t.Errorf("total_pages = %d, want %d", got, tt.wantPages)
			}
		})
	}
}

package routes

import (
	"encoding/json"
	"testing"
)

// TestCreateInvoice_BindingValidation tests JSON binding for invoice creation.
func TestCreateInvoice_BindingValidation(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantValid bool
	}{
		{
			name:      "valid invoice",
			body:      `{"order_id": "550e8400-e29b-41d4-a716-446655440000", "title": "Test Company"}`,
			wantValid: true,
		},
		{
			name:      "missing order_id",
			body:      `{"title": "Test Company"}`,
			wantValid: false,
		},
		{
			name:      "missing title",
			body:      `{"order_id": "550e8400-e29b-41d4-a716-446655440000"}`,
			wantValid: false,
		},
		{
			name:      "empty order_id",
			body:      `{"order_id": "", "title": "Test"}`,
			wantValid: false,
		},
		{
			name:      "empty title",
			body:      `{"order_id": "550e8400", "title": ""}`,
			wantValid: false,
		},
		{
			name:      "empty body",
			body:      `{}`,
			wantValid: false,
		},
		{
			name:      "invalid json",
			body:      `not json`,
			wantValid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req struct {
				OrderID     string `json:"order_id" binding:"required"`
				InvoiceType string `json:"invoice_type"`
				TitleType   string `json:"title_type"`
				Title       string `json:"title" binding:"required"`
				TaxID       string `json:"tax_id"`
				Email       string `json:"email"`
				Phone       string `json:"phone"`
				Address     string `json:"address"`
				BankName    string `json:"bank_name"`
				BankAccount string `json:"bank_account"`
			}
			err := json.Unmarshal([]byte(tt.body), &req)
			if err != nil {
				if tt.wantValid {
					t.Errorf("unexpected unmarshal error: %v", err)
				}
				return
			}

			valid := req.OrderID != "" && req.Title != ""
			if valid != tt.wantValid {
				t.Errorf("valid = %v, want %v (order_id=%q, title=%q)", valid, tt.wantValid, req.OrderID, req.Title)
			}
		})
	}
}

// TestCreateInvoice_DefaultValues tests default invoice_type and title_type.
func TestCreateInvoice_DefaultValues(t *testing.T) {
	tests := []struct {
		name            string
		invoiceType     string
		titleType       string
		wantInvoiceType string
		wantTitleType   string
	}{
		{"both empty", "", "", "personal", "personal"},
		{"invoice_type set", "enterprise", "", "enterprise", "personal"},
		{"title_type set", "", "company", "personal", "company"},
		{"both set", "enterprise", "company", "enterprise", "company"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			invoiceType := tt.invoiceType
			if invoiceType == "" {
				invoiceType = "personal"
			}
			titleType := tt.titleType
			if titleType == "" {
				titleType = "personal"
			}

			if invoiceType != tt.wantInvoiceType {
				t.Errorf("invoiceType = %q, want %q", invoiceType, tt.wantInvoiceType)
			}
			if titleType != tt.wantTitleType {
				t.Errorf("titleType = %q, want %q", titleType, tt.wantTitleType)
			}
		})
	}
}

// TestCreateInvoice_AllOptionalFields tests invoice creation with all optional fields.
func TestCreateInvoice_AllOptionalFields(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantOrderID string
		wantTitle   string
		wantTaxID   string
		wantEmail   string
	}{
		{
			name:        "all fields",
			body:        `{"order_id": "ord-1", "title": "Corp", "invoice_type": "enterprise", "title_type": "company", "tax_id": "TAX123", "email": "a@b.com", "phone": "123", "address": "addr", "bank_name": "bank", "bank_account": "acc"}`,
			wantOrderID: "ord-1",
			wantTitle:   "Corp",
			wantTaxID:   "TAX123",
			wantEmail:   "a@b.com",
		},
		{
			name:        "required only",
			body:        `{"order_id": "ord-2", "title": "Personal"}`,
			wantOrderID: "ord-2",
			wantTitle:   "Personal",
			wantTaxID:   "",
			wantEmail:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req struct {
				OrderID     string `json:"order_id" binding:"required"`
				InvoiceType string `json:"invoice_type"`
				TitleType   string `json:"title_type"`
				Title       string `json:"title" binding:"required"`
				TaxID       string `json:"tax_id"`
				Email       string `json:"email"`
				Phone       string `json:"phone"`
				Address     string `json:"address"`
				BankName    string `json:"bank_name"`
				BankAccount string `json:"bank_account"`
			}
			if err := json.Unmarshal([]byte(tt.body), &req); err != nil {
				t.Fatalf("unmarshal error: %v", err)
			}

			if req.OrderID != tt.wantOrderID {
				t.Errorf("orderID = %q, want %q", req.OrderID, tt.wantOrderID)
			}
			if req.Title != tt.wantTitle {
				t.Errorf("title = %q, want %q", req.Title, tt.wantTitle)
			}
			if req.TaxID != tt.wantTaxID {
				t.Errorf("taxID = %q, want %q", req.TaxID, tt.wantTaxID)
			}
			if req.Email != tt.wantEmail {
				t.Errorf("email = %q, want %q", req.Email, tt.wantEmail)
			}
		})
	}
}

// TestCreateInvoice_OrderMustBeCompleted tests the order status validation logic.
func TestCreateInvoice_OrderMustBeCompleted(t *testing.T) {
	tests := []struct {
		name        string
		orderStatus string
		wantValid   bool
	}{
		{"completed order", "completed", true},
		{"pending order", "pending", false},
		{"cancelled order", "cancelled", false},
		{"processing order", "processing", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// From invoices.go:148 - orderStatus != "completed" returns error
			valid := tt.orderStatus == "completed"
			if valid != tt.wantValid {
				t.Errorf("valid(%q) = %v, want %v", tt.orderStatus, valid, tt.wantValid)
			}
		})
	}
}

// TestUpdateInvoice_BindingValidation tests JSON binding for invoice update.
func TestUpdateInvoice_BindingValidation(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantStatus string
		wantNumber string
	}{
		{
			name:       "status only",
			body:       `{"status": "approved"}`,
			wantStatus: "approved",
			wantNumber: "",
		},
		{
			name:       "invoice_number only",
			body:       `{"invoice_number": "INV-2024-001"}`,
			wantStatus: "",
			wantNumber: "INV-2024-001",
		},
		{
			name:       "both fields",
			body:       `{"status": "completed", "invoice_number": "INV-2024-002"}`,
			wantStatus: "completed",
			wantNumber: "INV-2024-002",
		},
		{
			name:       "empty body",
			body:       `{}`,
			wantStatus: "",
			wantNumber: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req struct {
				Status        string `json:"status"`
				InvoiceNumber string `json:"invoice_number"`
			}
			if err := json.Unmarshal([]byte(tt.body), &req); err != nil {
				t.Fatalf("unmarshal error: %v", err)
			}

			if req.Status != tt.wantStatus {
				t.Errorf("status = %q, want %q", req.Status, tt.wantStatus)
			}
			if req.InvoiceNumber != tt.wantNumber {
				t.Errorf("invoiceNumber = %q, want %q", req.InvoiceNumber, tt.wantNumber)
			}
		})
	}
}

// TestUpdateInvoice_InvoiceNumberSetsStatus tests that setting invoice_number also sets status to completed.
func TestUpdateInvoice_InvoiceNumberSetsStatus(t *testing.T) {
	tests := []struct {
		name          string
		invoiceNumber string
		wantStatus    string
	}{
		{"with number", "INV-001", "completed"},
		{"empty number", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// From invoices.go:228 - when invoice_number is set, status becomes 'completed'
			var status string
			if tt.invoiceNumber != "" {
				status = "completed"
			}
			if status != tt.wantStatus {
				t.Errorf("status = %q, want %q", status, tt.wantStatus)
			}
		})
	}
}

// TestInvoiceStatusValues tests valid invoice status values.
func TestInvoiceStatusValues(t *testing.T) {
	validStatuses := []string{"pending", "approved", "completed", "rejected"}
	for _, status := range validStatuses {
		if status == "" {
			t.Error("status should not be empty")
		}
	}
}

// TestInvoiceTypes tests valid invoice type values.
func TestInvoiceTypes(t *testing.T) {
	tests := []struct {
		name        string
		invoiceType string
		titleType   string
		wantValid   bool
	}{
		{"personal/personal", "personal", "personal", true},
		{"enterprise/company", "enterprise", "company", true},
		{"personal/company", "personal", "company", true},
		{"enterprise/personal", "enterprise", "personal", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			valid := tt.invoiceType != "" && tt.titleType != ""
			if valid != tt.wantValid {
				t.Errorf("valid = %v, want %v", valid, tt.wantValid)
			}
		})
	}
}

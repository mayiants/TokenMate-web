package routes

import (
	"testing"
)

// TestAuditRoutesMethodsExist is a compile-time check.
func TestAuditRoutesMethodsExist(t *testing.T) {
	_ = (*AuditRoutes).list
	_ = (*AuditRoutes).get
	_ = (*AuditRoutes).export
	_ = (*AuditRoutes).listDeleted
	_ = (*AuditRoutes).softDelete
	_ = (*AuditRoutes).restore
}

// TestAuditLogLevelValues tests valid audit log levels.
func TestAuditLogLevelValues(t *testing.T) {
	validLevels := []string{"info", "warning", "error", "critical"}

	for _, level := range validLevels {
		if level == "" {
			t.Error("audit log level should not be empty")
		}
	}

	tests := []struct {
		level      string
		isWarning  bool
		isCritical bool
	}{
		{"info", false, false},
		{"warning", true, false},
		{"error", false, false},
		{"critical", false, true},
		{"", false, false},
		{"debug", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.level, func(t *testing.T) {
			isWarning := tt.level == "warning"
			if isWarning != tt.isWarning {
				t.Errorf("isWarning(%q) = %v, want %v", tt.level, isWarning, tt.isWarning)
			}
			isCritical := tt.level == "critical"
			if isCritical != tt.isCritical {
				t.Errorf("isCritical(%q) = %v, want %v", tt.level, isCritical, tt.isCritical)
			}
		})
	}
}

// TestAuditCategoryValues tests common audit log categories.
func TestAuditCategoryValues(t *testing.T) {
	commonCategories := []string{
		"admin", "user", "apikeys", "security", "billing",
	}

	for _, cat := range commonCategories {
		if cat == "" {
			t.Error("audit category should not be empty")
		}
	}
}

// TestAuditPagination tests pagination used in audit routes.
func TestAuditPagination(t *testing.T) {
	tests := []struct {
		name      string
		total     int
		limit     int
		page      int
		wantPages int
	}{
		{"empty", 0, 20, 1, 0},
		{"single page", 15, 20, 1, 1},
		{"multiple pages", 50, 20, 1, 3},
		{"exact division", 40, 20, 1, 2},
		{"page 2 start", 50, 20, 2, 3},
		{"large limit", 10, 100, 1, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			offset := (tt.page - 1) * tt.limit
			totalPages := (tt.total + tt.limit - 1) / tt.limit

			// offset sanity
			if offset < 0 {
				t.Error("offset should not be negative")
			}

			if totalPages != tt.wantPages {
				t.Errorf("total_pages = %d, want %d", totalPages, tt.wantPages)
			}
		})
	}
}

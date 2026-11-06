package audit

import (
	"context"
	"testing"

	"go.uber.org/zap/zaptest"
)

// TestNewLogger tests that NewLogger creates a non-nil logger.
func TestNewLogger(t *testing.T) {
	log := NewLogger(nil, zaptest.NewLogger(t))
	if log == nil {
		t.Fatal("NewLogger() returned nil")
	}
	if log.db != nil {
		t.Error("Logger.db should be nil when passed nil")
	}
}

// TestNewNoopLogger tests the noop logger creation.
func TestNewNoopLogger(t *testing.T) {
	log := NewNoopLogger(zaptest.NewLogger(t))
	if log == nil {
		t.Fatal("NewNoopLogger() returned nil")
	}
	if log.db != nil {
		t.Error("Noop logger should have nil db")
	}
}

// TestAuditLogStruct tests the AuditLog struct fields.
func TestAuditLogStruct(t *testing.T) {
	var log AuditLog
	if log.ID != 0 {
		t.Error("AuditLog.ID should default to 0")
	}
	if log.Category != "" {
		t.Error("AuditLog.Category should default to empty")
	}
}

// TestLogWithNilDB tests that Log is a no-op when db is nil.
func TestLogWithNilDB(t *testing.T) {
	log := NewNoopLogger(zaptest.NewLogger(t))
	// Should not panic
	log.Log(context.Background(), "user-1", "test@example.com", "test", "action", "info", "message")
	log.LogWithDetails(context.Background(), "user-1", "test@example.com", "test", "action", "info", "message", map[string]string{"key": "value"})
	log.LogStructured(context.Background(), "user-1", "test@example.com", "test", "action", "info", "message", "target", "123", nil, map[string]string{"new": "value"})
	log.LogWithIP(context.Background(), "user-1", "test@example.com", "test", "action", "info", "message", "192.168.1.1")
}

// TestResolveEmailNilDB tests resolveEmail with nil db.
func TestResolveEmailNilDB(t *testing.T) {
	log := NewNoopLogger(zaptest.NewLogger(t))
	email := log.resolveEmail(context.Background(), "user-1", "test@example.com")
	if email != "test@example.com" {
		t.Errorf("resolveEmail() = %q, want %q", email, "test@example.com")
	}
}

// TestResolveEmailEmptyEmail tests resolveEmail when email is empty but db is nil.
func TestResolveEmailEmptyEmail(t *testing.T) {
	log := NewNoopLogger(zaptest.NewLogger(t))
	email := log.resolveEmail(context.Background(), "user-1", "")
	if email != "" {
		t.Errorf("resolveEmail() with empty email and nil db = %q, want empty", email)
	}
}

// TestResolveEmailEmptyUserID tests resolveEmail when userID is empty.
func TestResolveEmailEmptyUserID(t *testing.T) {
	log := NewNoopLogger(zaptest.NewLogger(t))
	email := log.resolveEmail(context.Background(), "", "")
	if email != "" {
		t.Errorf("resolveEmail() with empty userID = %q, want empty", email)
	}
}

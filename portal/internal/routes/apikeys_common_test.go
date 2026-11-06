package routes

import (
	"encoding/json"
	"testing"
)

// TestContainsString tests the containsString helper function.
func TestContainsString(t *testing.T) {
	tests := []struct {
		name  string
		slice []string
		item  string
		want  bool
	}{
		{"found at start", []string{"a", "b", "c"}, "a", true},
		{"found in middle", []string{"a", "b", "c"}, "b", true},
		{"found at end", []string{"a", "b", "c"}, "c", true},
		{"not found", []string{"a", "b", "c"}, "d", false},
		{"empty slice", []string{}, "a", false},
		{"nil slice", nil, "a", false},
		{"empty item", []string{"a", "b", ""}, "", true},
		{"item not empty, not found", []string{"a", "b"}, "", false},
		{"single element match", []string{"hello"}, "hello", true},
		{"single element no match", []string{"hello"}, "world", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := containsString(tt.slice, tt.item)
			if got != tt.want {
				t.Errorf("containsString(%v, %q) = %v, want %v", tt.slice, tt.item, got, tt.want)
			}
		})
	}
}

// TestValidateKeyResponseSerialization tests the key validation response struct.
func TestValidateKeyResponseSerialization(t *testing.T) {
	groupID := "group-123"
	keyType := "general"
	billingMode := "token"
	currentModel := "gpt-4"
	routingMode := "single"

	resp := validateKeyResponse{
		Valid:            true,
		UserID:           "user-1",
		APIKeyID:         "key-1",
		Role:             "user",
		AccessMode:       "single_model",
		AllowedModels:    []string{"gpt-4", "gpt-3.5-turbo"},
		RateLimitRPM:     60,
		RateLimitTPM:     100000,
		Balance:          100.0,
		QuotaExceeded:    false,
		QuotaWindow:      "",
		GroupID:          &groupID,
		KeyType:          &keyType,
		BillingMode:      &billingMode,
		CurrentModel:     &currentModel,
		PricingRatio:     1.0,
		ModelRoutingMode: &routingMode,
		RoutingTrack:     "standard",
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal() error: %v", err)
	}

	var roundTrip validateKeyResponse
	if err := json.Unmarshal(data, &roundTrip); err != nil {
		t.Fatalf("Unmarshal() error: %v", err)
	}

	if roundTrip.Valid != resp.Valid {
		t.Error("Valid mismatch")
	}
	if roundTrip.UserID != resp.UserID {
		t.Errorf("UserID = %q, want %q", roundTrip.UserID, resp.UserID)
	}
	if roundTrip.Role != resp.Role {
		t.Errorf("Role = %q, want %q", roundTrip.Role, resp.Role)
	}
	if roundTrip.RateLimitRPM != resp.RateLimitRPM {
		t.Errorf("RateLimitRPM = %d, want %d", roundTrip.RateLimitRPM, resp.RateLimitRPM)
	}
	if roundTrip.Balance != resp.Balance {
		t.Errorf("Balance = %f, want %f", roundTrip.Balance, resp.Balance)
	}
}

// TestValidateKeyResponseInvalidKey tests serialization of an invalid key response.
func TestValidateKeyResponseInvalidKey(t *testing.T) {
	resp := validateKeyResponse{
		Valid:         false,
		QuotaExceeded: true,
		QuotaWindow:   "daily",
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal() error: %v", err)
	}

	var roundTrip validateKeyResponse
	if err := json.Unmarshal(data, &roundTrip); err != nil {
		t.Fatalf("Unmarshal() error: %v", err)
	}

	if roundTrip.Valid {
		t.Error("expected Valid=false")
	}
	if !roundTrip.QuotaExceeded {
		t.Error("expected QuotaExceeded=true")
	}
}

// TestAPIKeyRoutesMethodsExist is a compile-time check.
func TestAPIKeyRoutesMethodsExist(t *testing.T) {
	_ = (*APIKeyRoutes).listKeys
	_ = (*APIKeyRoutes).createKey
	_ = (*APIKeyRoutes).getKey
	_ = (*APIKeyRoutes).getKeyUsage
	_ = (*APIKeyRoutes).updateKey
	_ = (*APIKeyRoutes).deleteKey
	_ = (*APIKeyRoutes).validateKey
}

package routes

import (
	"testing"
	"time"
)

// TestValidateKeyCacheTTL tests the cache TTL constant.
func TestValidateKeyCacheTTL(t *testing.T) {
	if validateKeyCacheTTL <= 0 {
		t.Error("validateKeyCacheTTL should be positive")
	}
	if validateKeyCacheTTL != 5*time.Minute {
		t.Errorf("validateKeyCacheTTL = %v, want 5m", validateKeyCacheTTL)
	}
}

// TestMaxPersonalKeysPerUser tests the personal keys limit constant.
func TestMaxPersonalKeysPerUser(t *testing.T) {
	if maxPersonalKeysPerUser <= 0 {
		t.Error("maxPersonalKeysPerUser should be positive")
	}
	// Should be a reasonable value
	if maxPersonalKeysPerUser > 1000 {
		t.Errorf("maxPersonalKeysPerUser = %d, seems unreasonably high", maxPersonalKeysPerUser)
	}
}

// TestDefaultAPIKeyRateLimitRPM tests the default RPM rate limit constant.
func TestDefaultAPIKeyRateLimitRPM(t *testing.T) {
	if defaultAPIKeyRateLimitRPM <= 0 {
		t.Error("defaultAPIKeyRateLimitRPM should be positive")
	}
}

// TestDefaultAPIKeyRateLimitTPM tests the default TPM rate limit constant.
func TestDefaultAPIKeyRateLimitTPM(t *testing.T) {
	if defaultAPIKeyRateLimitTPM <= 0 {
		t.Error("defaultAPIKeyRateLimitTPM should be positive")
	}
}

// TestAPIKeyUsageWindowDays tests the usage window constant.
func TestAPIKeyUsageWindowDays(t *testing.T) {
	if apiKeyUsageWindowDays <= 0 {
		t.Error("apiKeyUsageWindowDays should be positive")
	}
}

// TestAPIKeyPrefixDisplayLength tests the display length constant.
func TestAPIKeyPrefixDisplayLength(t *testing.T) {
	if apiKeyPrefixDisplayLength <= 0 {
		t.Error("apiKeyPrefixDisplayLength should be positive")
	}
}

// TestMaxGroupPriorityGroups tests the group priority limit.
func TestMaxGroupPriorityGroups(t *testing.T) {
	if maxGroupPriorityGroups <= 0 {
		t.Error("maxGroupPriorityGroups should be positive")
	}
	// Should allow at least 1 group
	if maxGroupPriorityGroups < 1 {
		t.Error("maxGroupPriorityGroups should allow at least 1 group")
	}
}

// TestEnterpriseCreditHardCapPercent tests the enterprise credit cap.
func TestEnterpriseCreditHardCapPercent(t *testing.T) {
	if enterpriseCreditHardCapPercent <= 0 {
		t.Error("enterpriseCreditHardCapPercent should be positive")
	}
	// Should be >= 100 to allow overage
	if enterpriseCreditHardCapPercent < 100 {
		t.Errorf("enterpriseCreditHardCapPercent = %d, should be >= 100", enterpriseCreditHardCapPercent)
	}
}

// TestVoucherCodeLengths tests voucher/invitation code length constants.
func TestVoucherCodeLengths(t *testing.T) {
	if invitationCodeRandomLength <= 0 {
		t.Error("invitationCodeRandomLength should be positive")
	}
	if affiliateCodeRandomLength <= 0 {
		t.Error("affiliateCodeRandomLength should be positive")
	}
	if voucherCodeRandomLength <= 0 {
		t.Error("voucherCodeRandomLength should be positive")
	}

	// Code lengths should be reasonable (at least 4)
	if invitationCodeRandomLength < 4 {
		t.Errorf("invitationCodeRandomLength = %d, should be >= 4", invitationCodeRandomLength)
	}
	if voucherCodeRandomLength < 4 {
		t.Errorf("voucherCodeRandomLength = %d, should be >= 4", voucherCodeRandomLength)
	}
}

// TestInvitationDefaults tests invitation default values.
func TestInvitationDefaults(t *testing.T) {
	// Reward amounts should be positive
	if defaultInvitationInviterReward <= 0 {
		t.Error("defaultInvitationInviterReward should be positive")
	}
	if defaultInvitationInviteeReward <= 0 {
		t.Error("defaultInvitationInviteeReward should be positive")
	}

	// Max uses should be at least 1
	if defaultInvitationMaxUses < 1 {
		t.Errorf("defaultInvitationMaxUses = %d, should be >= 1", defaultInvitationMaxUses)
	}

	// Expire days should be positive
	if defaultInvitationExpireDays <= 0 {
		t.Error("defaultInvitationExpireDays should be positive")
	}

	// Max codes per user should be positive
	if defaultInvitationMaxCodesPerUser <= 0 {
		t.Error("defaultInvitationMaxCodesPerUser should be positive")
	}
}

// TestInvitationBounds tests min/max invitation bounds.
func TestInvitationBounds(t *testing.T) {
	if maxInvitationMaxUses < minInvitationMaxUses {
		t.Errorf("maxInvitationMaxUses (%d) < minInvitationMaxUses (%d)",
			maxInvitationMaxUses, minInvitationMaxUses)
	}
	if minInvitationExpireDays <= 0 {
		t.Error("minInvitationExpireDays should be positive")
	}
}

// TestVoucherConstraints tests voucher creation constraints.
func TestVoucherConstraints(t *testing.T) {
	if maxVoucherCreateCount <= 0 {
		t.Error("maxVoucherCreateCount should be positive")
	}
	// Should be at least 1
	if maxVoucherCreateCount < 1 {
		t.Error("maxVoucherCreateCount should allow at least 1 voucher")
	}
}

// TestWelcomeVoucherMaxUses tests the welcome voucher limit.
func TestWelcomeVoucherMaxUses(t *testing.T) {
	if welcomeVoucherMaxUses <= 0 {
		t.Error("welcomeVoucherMaxUses should be positive")
	}
	// Should be effectively unlimited (large number)
	if welcomeVoucherMaxUses < 1000 {
		t.Errorf("welcomeVoucherMaxUses = %d, should be large (effectively unlimited)", welcomeVoucherMaxUses)
	}
}

// TestVoucherPaginationDefaults tests pagination default constants.
func TestVoucherPaginationDefaults(t *testing.T) {
	if voucherPaginationDefaultLimit <= 0 {
		t.Error("voucherPaginationDefaultLimit should be positive")
	}
	if voucherPaginationMaxLimit <= 0 {
		t.Error("voucherPaginationMaxLimit should be positive")
	}
	if voucherPaginationMaxLimit < voucherPaginationDefaultLimit {
		t.Errorf("voucherPaginationMaxLimit (%d) < voucherPaginationDefaultLimit (%d)",
			voucherPaginationMaxLimit, voucherPaginationDefaultLimit)
	}
}

// TestDefaultUSDCNYExchangeRate tests the exchange rate constant.
func TestDefaultUSDCNYExchangeRate(t *testing.T) {
	if defaultUSDCNYExchangeRate <= 0 {
		t.Error("defaultUSDCNYExchangeRate should be positive")
	}
	// Should be a reasonable CNY/USD rate
	if defaultUSDCNYExchangeRate < 1.0 || defaultUSDCNYExchangeRate > 20.0 {
		t.Errorf("defaultUSDCNYExchangeRate = %.2f, should be between 1.0 and 20.0", defaultUSDCNYExchangeRate)
	}
}

// TestAllConstantsNonEmpty tests that key constants are non-empty/non-zero.
func TestAllConstantsNonEmpty(t *testing.T) {
	constants := []struct {
		name  string
		value interface{}
	}{
		{"validateKeyCacheTTL", validateKeyCacheTTL},
		{"maxPersonalKeysPerUser", maxPersonalKeysPerUser},
		{"defaultAPIKeyRateLimitRPM", defaultAPIKeyRateLimitRPM},
		{"defaultAPIKeyRateLimitTPM", defaultAPIKeyRateLimitTPM},
		{"apiKeyUsageWindowDays", apiKeyUsageWindowDays},
		{"apiKeyPrefixDisplayLength", apiKeyPrefixDisplayLength},
		{"maxGroupPriorityGroups", maxGroupPriorityGroups},
		{"enterpriseCreditHardCapPercent", enterpriseCreditHardCapPercent},
		{"invitationCodeRandomLength", invitationCodeRandomLength},
		{"voucherCodeRandomLength", voucherCodeRandomLength},
		{"welcomeVoucherMaxUses", welcomeVoucherMaxUses},
		{"defaultUSDCNYExchangeRate", defaultUSDCNYExchangeRate},
	}

	for _, c := range constants {
		t.Run(c.name, func(t *testing.T) {
			switch v := c.value.(type) {
			case int:
				if v <= 0 && c.name != "enterpriseCreditHardCapPercent" {
					t.Errorf("%s = %d, should be positive", c.name, v)
				}
				// All non-zero check
				if c.name != "maxImportKeys" && v == 0 {
					t.Errorf("%s = %d, should be non-zero", c.name, v)
				}
			case float64:
				if v <= 0 {
					t.Errorf("%s = %v, should be positive", c.name, v)
				}
			case time.Duration:
				if v <= 0 {
					t.Errorf("%s = %v, should be positive", c.name, v)
				}
			}
		})
	}
}

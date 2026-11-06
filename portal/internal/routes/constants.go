package routes

import "time"

// Constants for API key management.
const (
	// validateKeyCacheTTL is the TTL for Redis cache of key validation results.
	validateKeyCacheTTL = 5 * time.Minute

	// validateKeyNegativeCacheTTL is the TTL for caching unknown/disabled key
	// results. Kept short so a newly created key or a re-enabled key becomes
	// valid quickly, while still protecting the DB from unauthenticated
	// repeated-lookup amplification.
	validateKeyNegativeCacheTTL = 30 * time.Second

	// validateKeyRateLimitPerSec is the maximum number of validate requests
	// allowed per client IP per second. The validate endpoint is
	// unauthenticated (called by the proxy with the raw key), so this guards
	// against unauthenticated DB-lookup amplification.
	validateKeyRateLimitPerSec = 30

	// maxPersonalKeysPerUser limits how many personal API keys a user can own.
	maxPersonalKeysPerUser = 20

	// defaultAPIKeyRateLimitRPM is the default per-minute request rate limit.
	// This is used when creating a key if neither the group nor the system setting provides a value.
	// Admin can override globally via admin/settings → default_user_rpm.
	defaultAPIKeyRateLimitRPM = 120

	// defaultAPIKeyRateLimitTPM is the default per-minute token rate limit.
	// Admin can override globally via admin/settings → default_user_tpm.
	defaultAPIKeyRateLimitTPM = 500000

	// defaultAPIKeyDailyRequests is the default daily request limit per key.
	// Admin can override globally via admin/settings → default_user_daily_requests.
	defaultAPIKeyDailyRequests = 10000

	// apiKeyUsageWindowDays is how many days of usage records are returned for a key.
	apiKeyUsageWindowDays = 30

	// apiKeyPrefixDisplayLength is the number of characters displayed in the key prefix.
	apiKeyPrefixDisplayLength = 12

	// maxGroupPriorityGroups limits how many groups can be set in group_priority.
	maxGroupPriorityGroups = 3

	// enterpriseCreditHardCapPercent defines the enterprise credit overage limit as a percentage.
	// A value of 110 means service stops when credit_used reaches 110% of credit_total.
	enterpriseCreditHardCapPercent = 110
)

// Constants for voucher / invitation code management.
const (
	// Code lengths.
	invitationCodeRandomLength = 8
	affiliateCodeRandomLength  = 6
	voucherCodeRandomLength    = 12

	// Invitation default settings.
	defaultInvitationMaxCodesPerUser = 10
	defaultInvitationInviterReward   = 5_000_000
	defaultInvitationInviteeReward   = 2_000_000
	defaultInvitationMaxUses         = 10
	defaultInvitationExpireDays      = 365

	// Invitation bounds.
	maxInvitationMaxUses    = 100
	minInvitationMaxUses    = 1
	minInvitationExpireDays = 1

	// Voucher creation bounds.
	maxVoucherCreateCount = 100

	// Welcome voucher uses an effectively unlimited max_uses value.
	welcomeVoucherMaxUses = 999999

	// Voucher pagination defaults.
	voucherPaginationDefaultLimit = 20
	voucherPaginationMaxLimit     = 50

	// defaultUSDCNYExchangeRate is the fallback CNY per USD rate when not configured.
	defaultUSDCNYExchangeRate = 7.20
)

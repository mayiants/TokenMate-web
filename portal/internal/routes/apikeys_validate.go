package routes

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mayiants/ai-portal-go/internal/db"
	"github.com/mayiants/ai-portal-go/pkg/crypto"
	"github.com/mayiants/ai-portal-go/pkg/metrics"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

func (r *APIKeyRoutes) validateKey(c *gin.Context) {
	metrics.GetMetrics().APIKeyValidated.Add(1)

	// IP-based rate limiting. The validate endpoint is unauthenticated (the
	// proxy calls it with the raw key), so without this an attacker can
	// amplify DB lookups by hammering it with arbitrary key strings.
	if !r.checkValidateRateLimit(c) {
		c.JSON(http.StatusTooManyRequests, gin.H{"valid": false, "error": "Rate limit exceeded"})
		return
	}

	apiKey := extractBearerToken(c)
	if apiKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"valid": false, "error": "Missing API key"})
		return
	}

	keyHash := crypto.HashKeySHA256(apiKey)

	// Try to get cached result from Redis.
	// Both positive (valid:true) and negative (valid:false) results are cached
	// so that unknown/disabled keys do not hammer the database on every call.
	if r.db.Redis != nil {
		cacheKey := "validate:" + keyHash
		cached, err := r.db.RedisGet(c.Request.Context(), cacheKey).Result()
		if err == nil && cached != "" {
			var resp validateKeyResponse
			if json.Unmarshal([]byte(cached), &resp) == nil {
				// Return cached response
				if !resp.Valid {
					c.JSON(http.StatusOK, gin.H{"valid": false})
					return
				}
				// Entries cached before the retention fields existed carry no
				// retention_mode; treat them as a miss so enterprise keys never
				// get platform defaults by accident.
				if resp.RetentionMode != "" {
					if resp.EnterpriseID == "" {
						// Platform defaults are not tied to any key, so they
						// are read live instead of being frozen in the cache.
						resp.RetentionMode, resp.RetentionDays = platformRetentionDefaults(c.Request.Context(), r.db)
					}
					c.JSON(http.StatusOK, resp)
					return
				}
			}
		}
	}

	// Single query with LEFT JOIN for subscription data
	// For subscription keys (sk-wgit-tp), subscription data will be populated
	// For other keys, subscription columns will be NULL
	var keyID, userID string
	var status, accessMode, role string
	var allowedModels interface{}
	var rateLimitRPM, rateLimitTPM int
	// NOTE: balance is in micro-USD (1 USD = 1,000,000 micro-USD).
	var balance float64
	var groupID, keyType, billingMode, currentModel *string
	var pricingRatio float64
	var groupPriority interface{}
	var modelRoutingMode *string
	var selectedModels interface{}
	var userRoutingTrack, groupRoutingTrack string

	// Subscription fields (may be NULL for non-subscription keys)
	var subQuotaRemaining, subDailyLimit, subDailyUsed sql.NullInt64
	var subPlanType sql.NullString
	var planModels interface{}
	var subTpmLimit, subTphLimit, subRpmLimit, subRphLimit sql.NullInt64
	var subConcurrentLimit, subRp5hLimit, subRpdLimit, subRpwLimit, subTpdLimit, subTpwLimit, subTPMonthLimit sql.NullInt64
	var subTpmUsed, subTphUsed, subTpwUsed, subRpmUsed, subRphUsed, subRpwUsed, subRp5hUsed, subTPMonthUsed sql.NullInt64
	var subTpmResetAt, subTphResetAt, subTpwResetAt, subRpmResetAt, subRphResetAt, subRpwResetAt, subRp5hResetAt, subTPMonthResetAt sql.NullTime

	// Group rate limit fields
	var groupRPM, groupRP5H, groupRPW, groupRPMonth sql.NullInt64
	var groupRPH sql.NullInt64
	var groupTPM, groupTPH, groupTPD, groupTPW, groupTPMonth sql.NullInt64
	var groupConcurrentLimit sql.NullInt64

	var keyExpiresAt sql.NullTime

	// Data-retention policy of the key's enterprise (empty when none).
	var retEnterpriseID, retMode string
	var retDays int

	// Enterprise fields merged into the main query to avoid extra round-trips.
	var entPricingRatio sql.NullFloat64
	var entCreditTotal, entCreditUsed sql.NullInt64
	var memberQuotaLimit, userQuotaUsed sql.NullInt64
	var memberCreditLimit, userCreditUsed sql.NullInt64
	var entStatus sql.NullString

	err := r.db.QueryRow(c.Request.Context(),
		`SELECT ak.id::text, COALESCE(ak.assigned_user_id, ak.user_id)::text, ak.status, ak.access_mode, u.role,
		        -- Use group's models when key's allowed_models is empty (real-time inheritance).
		        -- When non-empty, intersect with group models so removed models are revoked immediately.
		        CASE WHEN ak.allowed_models IS NULL OR ak.allowed_models = '[]'::jsonb OR ak.allowed_models = 'null'::jsonb
		             THEN g.models
		             WHEN g.models IS NULL OR g.models = '[]'::jsonb OR g.models = 'null'::jsonb
		             THEN ak.allowed_models
		             ELSE (SELECT COALESCE(jsonb_agg(elem), '[]'::jsonb)
		                   FROM jsonb_array_elements(ak.allowed_models) AS ae(elem)
		                   WHERE ae.elem IN (SELECT jsonb_array_elements(g.models)))
		        END as allowed_models,
		        ak.rate_limit_rpm, ak.rate_limit_tpm,
		        -- Combined spendable balance: billing deducts gift_balance first, then balance,
		        -- so the gate and the proxy must see the sum, not just real funds.
		        COALESCE(u.balance, 0) + COALESCE(u.gift_balance, 0) AS balance,
		        ak.group_id::text, ak.key_type, ak.billing_mode, ak.current_model,
		        -- Group ratio is multiplicative along the group chain (enterprise
		        -- follow group ratio × parent platform group ratio).
		        COALESCE(gr.effective, 1.0), ak.group_priority,
		        ak.model_routing_mode, ak.selected_models,
		        COALESCE(u.routing_track, 'standard'), COALESCE(g.routing_track, 'standard'),
		        -- Enterprise fields (NULL for non-enterprise keys)
		        COALESCE(e.pricing_ratio, 1.0) as enterprise_pricing_ratio,
		        COALESCE(e.credit_total, 0) as enterprise_credit_total,
		        COALESCE(e.credit_used, 0) as enterprise_credit_used,
		        COALESCE(em.quota_limit, 0) as member_quota_limit,
		        COALESCE(u.quota_used, 0) as user_quota_used,
		        COALESCE(em.credit_limit, 0) as member_credit_limit,
		        COALESCE(u.credit_used, 0) as user_credit_used,
		        e.status as enterprise_status,
		        -- Subscription fields (NULL if no subscription)
		        s.quota_remaining, s.daily_limit, s.daily_quota_used,
		        s.plan_type,
		        s.plan_models,
		        s.tpm_limit, s.tph_limit, s.rpm_limit, s.rph_limit,
		        s.concurrent_limit, s.rp5h_limit, s.rpd_limit, s.rpw_limit, s.tpd_limit, s.tpw_limit, s.tpmonth_limit,
		        s.tpm_used, s.tph_used, s.tpw_used, s.rpm_used, s.rph_used, s.rpw_used, s.rp5h_used, s.tpmonth_used,
		        s.tpm_reset_at, s.tph_reset_at, s.tpw_reset_at, s.rpm_reset_at, s.rph_reset_at, s.rpw_reset_at, s.rp5h_reset_at, s.tpmonth_reset_at,
		        -- Group rate limit fields
		        g.rpm, g.rph, g.rp5h, g.rpw, g.rpmonth,
		        g.tpm, g.tph, g.tpd, g.tpw, g.tpmonth,
		        g.concurrent_limit,
		        ak.expires_at,
		        -- Retention policy (per key's enterprise; '' / 0 when the key has none,
		        -- in which case the platform defaults are applied below).
		        COALESCE(ak.enterprise_id::text, ''),
		        COALESCE(re.retention_mode, ''),
		        COALESCE(re.retention_days, 0)
		 FROM api_keys ak
		 JOIN users u ON u.id = COALESCE(ak.assigned_user_id, ak.user_id)
		        AND u.deleted_at IS NULL
		        AND (u.status IS NULL OR u.status != 'suspended')
		 LEFT JOIN user_groups g ON ak.group_id = g.id
		 LEFT JOIN LATERAL (
		     -- Multiplicative ratio along the group parent chain, same
		     -- semantics as GetPricingRatioBatch (depth capped + path-based
		     -- cycle detection so a parent_group_id loop can't inflate the ratio).
		     WITH RECURSIVE chain AS (
		         SELECT gg.id, gg.pricing_ratio, gg.parent_group_id,
		                COALESCE(gg.pricing_ratio, 1.0) AS effective, 1 AS depth,
		                ARRAY[gg.id] AS path, false AS cycle
		         FROM user_groups gg WHERE gg.id = g.id
		         UNION ALL
		         SELECT pg.id, pg.pricing_ratio, pg.parent_group_id,
		                c.effective * COALESCE(pg.pricing_ratio, 1.0), c.depth + 1,
		                c.path || pg.id, pg.id = ANY(c.path)
		         FROM chain c
		         JOIN user_groups pg ON pg.id = c.parent_group_id
		         WHERE c.depth < 10 AND NOT c.cycle
		     )
		     SELECT effective FROM chain
		     WHERE parent_group_id IS NULL OR cycle OR depth = 10
		 ) gr ON true
		 LEFT JOIN enterprises e ON u.enterprise_id = e.id
		 LEFT JOIN enterprises re ON re.id = ak.enterprise_id AND re.deleted_at IS NULL
		 LEFT JOIN enterprise_members em ON em.enterprise_id = e.id AND em.user_id = u.id AND em.removed_at IS NULL
		 LEFT JOIN LATERAL (
		        -- Package keys bind a subscription at creation (ak.subscription_id);
		        -- prefer it. Unbound keys (legacy) fall back to the newest active
		        -- subscription, matching the deduction path's fallback.
		        -- plan_models carries the plan's model allowlist
		        -- (subscription_plans.model_permissions) for gating.
		        SELECT s.*, sp.model_permissions AS plan_models
		        FROM user_subscriptions s
		        LEFT JOIN subscription_plans sp ON sp.id = s.plan_id AND sp.deleted_at IS NULL
		        WHERE s.user_id = u.id AND s.status = 'active' AND s.expires_at > NOW()
		          AND (ak.subscription_id IS NULL OR s.id = ak.subscription_id)
		        ORDER BY s.created_at DESC LIMIT 1
		 ) s ON true
		 WHERE ak.key_hash = $1 AND ak.deleted_at IS NULL`,
		keyHash,
	).Scan(&keyID, &userID, &status, &accessMode, &role, &allowedModels, &rateLimitRPM, &rateLimitTPM, &balance,
		&groupID, &keyType, &billingMode, &currentModel, &pricingRatio, &groupPriority,
		&modelRoutingMode, &selectedModels,
		&userRoutingTrack, &groupRoutingTrack,
		&entPricingRatio, &entCreditTotal, &entCreditUsed, &memberQuotaLimit, &userQuotaUsed, &memberCreditLimit, &userCreditUsed, &entStatus,
		&subQuotaRemaining, &subDailyLimit, &subDailyUsed, &subPlanType,
		&planModels,
		&subTpmLimit, &subTphLimit, &subRpmLimit, &subRphLimit,
		&subConcurrentLimit, &subRp5hLimit, &subRpdLimit, &subRpwLimit, &subTpdLimit, &subTpwLimit, &subTPMonthLimit,
		&subTpmUsed, &subTphUsed, &subTpwUsed, &subRpmUsed, &subRphUsed, &subRpwUsed, &subRp5hUsed, &subTPMonthUsed,
		&subTpmResetAt, &subTphResetAt, &subTpwResetAt, &subRpmResetAt, &subRphResetAt, &subRpwResetAt, &subRp5hResetAt, &subTPMonthResetAt,
		&groupRPM, &groupRPH, &groupRP5H, &groupRPW, &groupRPMonth,
		&groupTPM, &groupTPH, &groupTPD, &groupTPW, &groupTPMonth,
		&groupConcurrentLimit,
		&keyExpiresAt,
		&retEnterpriseID, &retMode, &retDays)
	if err != nil {
		// No row means the key is unknown, or its owner is deleted/suspended
		// (the JOIN above filters those users out). Reject with the same
		// semantics as a disabled key instead of a 500.
		if errors.Is(err, pgx.ErrNoRows) {
			// Cache the negative result so repeated lookups for the same
			// unknown key skip the database (DoS amplification protection).
			cacheNegativeResult(c, r, keyHash)
			c.JSON(http.StatusOK, gin.H{"valid": false, "error": "Key is disabled"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"valid": false, "error": "Database error"})
		return
	}

	if status != "active" {
		c.JSON(http.StatusOK, gin.H{"valid": false, "error": "Key is disabled"})
		return
	}

	// Suspended enterprise: all keys of its members stop working until the
	// enterprise is reactivated. NULL status (or no enterprise row) means active.
	if entStatus.Valid && entStatus.String != "" && entStatus.String != "active" {
		c.JSON(http.StatusOK, gin.H{"valid": false, "error": "Key is disabled"})
		return
	}

	if keyExpiresAt.Valid && !keyExpiresAt.Time.After(time.Now()) {
		c.JSON(http.StatusOK, gin.H{"valid": false, "error": "Key expired"})
		return
	}

	// Apply system-level default RPM/TPM when key has no custom override (DB default = 0 or column default)
	if rateLimitRPM <= 0 {
		rateLimitRPM = DefaultUserRPM(r.db)
	}
	if rateLimitTPM <= 0 {
		rateLimitTPM = DefaultUserTPM(r.db)
	}

	keyTypeStr := ""
	if keyType != nil {
		keyTypeStr = *keyType
	}

	// Enterprise pricing_ratio is a multiplier on top of the (chain-computed)
	// group ratio: final = cost × groupChain × enterpriseRatio. The webhook
	// billing path applies the same factor when the proxy sends no ratio.
	if keyTypeStr == "enterprise" && entPricingRatio.Valid && entPricingRatio.Float64 > 0 {
		pricingRatio = pricingRatio * entPricingRatio.Float64
	}

	// Enterprise credit check (110% hard cap, replaces personal balance gating)
	switch keyTypeStr {
	case "enterprise":
		creditTotal := entCreditTotal.Int64
		creditUsed := entCreditUsed.Int64
		if creditTotal > 0 {
			// Hard cap: stop service when credit_used reaches the configured percentage of credit_total.
			capLimit := creditTotal * enterpriseCreditHardCapPercent / 100
			if creditUsed >= capLimit {
				c.JSON(http.StatusPaymentRequired, gin.H{
					"valid":      false,
					"error":      "Enterprise credit exhausted (110% limit reached)",
					"error_code": "ENTERPRISE_CREDIT_EXHAUSTED",
				})
				return
			}
		}

		// Per-member quota cap: check enterprise_members.quota_limit against user's quota_used
		if memberQuotaLimit.Int64 > 0 && userQuotaUsed.Int64 >= memberQuotaLimit.Int64 {
			c.JSON(http.StatusPaymentRequired, gin.H{
				"valid":      false,
				"error":      "Member quota limit exceeded",
				"error_code": "MEMBER_QUOTA_EXCEEDED",
			})
			return
		}

		// Per-member credit cap: check enterprise_members.credit_limit against user's credit_used
		if memberCreditLimit.Int64 > 0 && userCreditUsed.Int64 >= memberCreditLimit.Int64 {
			c.JSON(http.StatusPaymentRequired, gin.H{
				"valid":      false,
				"error":      "Member credit limit exceeded",
				"error_code": "MEMBER_CREDIT_EXHAUSTED",
			})
			return
		}
	case "subscription":
		// Subscription keys settle ONLY against subscription quota — they never
		// fall back to personal gift/balance. Reject once the quota bucket is
		// exhausted, regardless of any wallet balance the user may hold. (The
		// per-window quota_exceeded checks below also cover daily/rate limits.)
		hasSubQuota := subQuotaRemaining.Valid && subQuotaRemaining.Int64 > 0
		if !hasSubQuota {
			c.JSON(http.StatusPaymentRequired, gin.H{
				"valid":      false,
				"error":      "Subscription quota exhausted",
				"error_code": "SUBSCRIPTION_QUOTA_EXHAUSTED",
			})
			return
		}
	default:
		// Check spendable balance (real + gift, combined in the query above).
		// An active subscription with remaining quota also passes the gate;
		// its quota windows are checked below and surfaced via quota_exceeded.
		hasSubQuota := subQuotaRemaining.Valid && subQuotaRemaining.Int64 > 0
		if balance <= 0 && !hasSubQuota {
			c.JSON(http.StatusPaymentRequired, gin.H{
				"valid":      false,
				"error":      "Insufficient balance",
				"error_code": "BALANCE_EXHAUSTED",
			})
			return
		}
	}

	// Check quota and rate limits from subscription (only if subscription exists)
	quotaExceeded := false
	quotaWindow := ""

	// Extract subscription values with defaults
	subRpmLimitVal := int(subRpmLimit.Int64)
	subTpmLimitVal := int(subTpmLimit.Int64)
	subRphLimitVal := int(subRphLimit.Int64)
	subTphLimitVal := int(subTphLimit.Int64)
	subConcurrentLimitVal := int(subConcurrentLimit.Int64)
	subRp5hLimitVal := int(subRp5hLimit.Int64)
	subRpdLimitVal := int(subRpdLimit.Int64)
	subRpwLimitVal := int(subRpwLimit.Int64)
	subTpdLimitVal := int(subTpdLimit.Int64)
	subTpwLimitVal := int(subTpwLimit.Int64)
	subTPMonthLimitVal := int(subTPMonthLimit.Int64)

	if subPlanType.Valid {
		now := time.Now()

		// Check TPM limit
		if subTpmLimit.Valid && subTpmLimit.Int64 > 0 {
			if !subTpmResetAt.Valid || now.After(subTpmResetAt.Time) {
				// Reset window - don't block, let Proxy handle it
			} else if subTpmUsed.Valid && subTpmUsed.Int64 >= subTpmLimit.Int64 {
				quotaExceeded = true
				quotaWindow = "tpm"
			}
		}

		// Check TPH limit
		if subTphLimit.Valid && subTphLimit.Int64 > 0 && !quotaExceeded {
			if !subTphResetAt.Valid || now.After(subTphResetAt.Time) {
				// Reset window
			} else if subTphUsed.Valid && subTphUsed.Int64 >= subTphLimit.Int64 {
				quotaExceeded = true
				quotaWindow = "tph"
			}
		}

		// Check TPW limit
		if subTpwLimit.Valid && subTpwLimit.Int64 > 0 && !quotaExceeded {
			if !subTpwResetAt.Valid || now.After(subTpwResetAt.Time) {
				// Reset window
			} else if subTpwUsed.Valid && subTpwUsed.Int64 >= subTpwLimit.Int64 {
				quotaExceeded = true
				quotaWindow = "tpw"
			}
		}

		// Check TPMonth limit
		if subTPMonthLimit.Valid && subTPMonthLimit.Int64 > 0 && !quotaExceeded {
			if !subTPMonthResetAt.Valid || now.After(subTPMonthResetAt.Time) {
				// Reset window
			} else if subTPMonthUsed.Valid && subTPMonthUsed.Int64 >= subTPMonthLimit.Int64 {
				quotaExceeded = true
				quotaWindow = "tpmonth"
			}
		}

		// Check RPM limit
		if subRpmLimit.Valid && subRpmLimit.Int64 > 0 && !quotaExceeded {
			if !subRpmResetAt.Valid || now.After(subRpmResetAt.Time) {
				// Reset window
			} else if subRpmUsed.Valid && subRpmUsed.Int64 >= subRpmLimit.Int64 {
				quotaExceeded = true
				quotaWindow = "rpm"
			}
		}

		// Check RPH limit
		if subRphLimit.Valid && subRphLimit.Int64 > 0 && !quotaExceeded {
			if !subRphResetAt.Valid || now.After(subRphResetAt.Time) {
				// Reset window
			} else if subRphUsed.Valid && subRphUsed.Int64 >= subRphLimit.Int64 {
				quotaExceeded = true
				quotaWindow = "rph"
			}
		}

		// Check RPW limit
		if subRpwLimit.Valid && subRpwLimit.Int64 > 0 && !quotaExceeded {
			if !subRpwResetAt.Valid || now.After(subRpwResetAt.Time) {
				// Reset window
			} else if subRpwUsed.Valid && subRpwUsed.Int64 >= subRpwLimit.Int64 {
				quotaExceeded = true
				quotaWindow = "rpw"
			}
		}

		// Check RP5H limit
		if subRp5hLimit.Valid && subRp5hLimit.Int64 > 0 && !quotaExceeded {
			if !subRp5hResetAt.Valid || now.After(subRp5hResetAt.Time) {
				// Reset window
			} else if subRp5hUsed.Valid && subRp5hUsed.Int64 >= subRp5hLimit.Int64 {
				quotaExceeded = true
				quotaWindow = "rp5h"
			}
		}

		// Override rate limits from subscription (use subscription limits if higher)
		if subTpmLimitVal > rateLimitTPM {
			rateLimitTPM = subTpmLimitVal
		}
		if subRpmLimitVal > rateLimitRPM {
			rateLimitRPM = subRpmLimitVal
		}

		// Check daily quota
		if subDailyLimit.Valid && subDailyLimit.Int64 > 0 && subDailyUsed.Valid && subDailyUsed.Int64 >= subDailyLimit.Int64 && !quotaExceeded {
			quotaExceeded = true
			quotaWindow = "daily"
		}

		// Check monthly quota
		if subQuotaRemaining.Valid && subQuotaRemaining.Int64 <= 0 && !quotaExceeded {
			quotaExceeded = true
			quotaWindow = "monthly"
		}
	}

	// Filter allowed_models against active service_models (removes EOL/disabled models)
	// This ensures enterprise groups don't serve stale model names after platform updates
	if allowedModels != nil {
		activeModels, err := loadActiveServiceModels(c.Request.Context(), r.db, r.logger)
		if err != nil {
			r.logger.Warn("Failed to load active service models", zap.Error(err))
		}
		if len(activeModels) > 0 {
			// Filter allowed_models
			switch am := allowedModels.(type) {
			case []interface{}:
				var filtered []interface{}
				for _, m := range am {
					if s, ok := m.(string); ok && activeModels[s] {
						filtered = append(filtered, m)
					}
				}
				allowedModels = filtered
			case []string:
				var filtered []string
				for _, m := range am {
					if activeModels[m] {
						filtered = append(filtered, m)
					}
				}
				allowedModels = filtered
			}
			// Filter selected_models (for round_robin/priority routing)
			switch sm := selectedModels.(type) {
			case []interface{}:
				var filtered []interface{}
				for _, m := range sm {
					if s, ok := m.(string); ok && activeModels[s] {
						filtered = append(filtered, m)
					}
				}
				selectedModels = filtered
			case []string:
				var filtered []string
				for _, m := range sm {
					if activeModels[m] {
						filtered = append(filtered, m)
					}
				}
				selectedModels = filtered
			}
		}
	}

	// Plan-level model gating: a subscription plan can restrict which models
	// its keys may use (industry-standard tiering — e.g. entry plans exclude
	// premium models). Applies only to subscription-billed keys; the user's
	// pay-as-you-go keys are unaffected. Empty/unset plan list = no restriction.
	if keyType != nil && *keyType == "subscription" {
		if planList := jsonbModelList(planModels); len(planList) > 0 {
			allowedModels = intersectModelAllowlist(allowedModels, planList)
		}
	}

	// Build response
	resp := validateKeyResponse{
		Valid:              true,
		UserID:             userID,
		APIKeyID:           keyID,
		Role:               role,
		AccessMode:         accessMode,
		AllowedModels:      allowedModels,
		RateLimitRPM:       rateLimitRPM,
		RateLimitTPM:       rateLimitTPM,
		Balance:            balance,
		QuotaExceeded:      quotaExceeded,
		QuotaWindow:        quotaWindow,
		GroupID:            groupID,
		KeyType:            keyType,
		BillingMode:        billingMode,
		CurrentModel:       currentModel,
		PricingRatio:       pricingRatio,
		GroupPriority:      groupPriority,
		ModelRoutingMode:   modelRoutingMode,
		SelectedModels:     selectedModels,
		SubRpmLimit:        subRpmLimitVal,
		SubTpmLimit:        0, // TPM not enforced at minute level — only RPM limits per minute
		SubRphLimit:        subRphLimitVal,
		SubTphLimit:        subTphLimitVal,
		SubConcurrentLimit: subConcurrentLimitVal,
		SubRp5hLimit:       subRp5hLimitVal,
		SubRpdLimit:        subRpdLimitVal,
		SubRpwLimit:        subRpwLimitVal,
		SubTpdLimit:        subTpdLimitVal,
		SubTpwLimit:        subTpwLimitVal,
		SubTPMonthLimit:    subTPMonthLimitVal,
		// Group rate limits
		GroupRPM:             int(groupRPM.Int64),
		GroupRPH:             int(groupRPH.Int64),
		GroupRP5H:            int(groupRP5H.Int64),
		GroupRPW:             int(groupRPW.Int64),
		GroupRPMonth:         int(groupRPMonth.Int64),
		GroupTPM:             int(groupTPM.Int64),
		GroupTPH:             int(groupTPH.Int64),
		GroupTPD:             int(groupTPD.Int64),
		GroupTPW:             int(groupTPW.Int64),
		GroupTPMonth:         int(groupTPMonth.Int64),
		GroupConcurrentLimit: int(groupConcurrentLimit.Int64),
		// User-level routing_track overrides group-level
		RoutingTrack: func() string {
			if userRoutingTrack != "" {
				return userRoutingTrack
			}
			if groupRoutingTrack != "" {
				return groupRoutingTrack
			}
			return "standard"
		}(),
	}
	resp.EnterpriseID, resp.RetentionMode, resp.RetentionDays = retEnterpriseID, retMode, retDays
	if resp.EnterpriseID == "" || !validRetentionMode(resp.RetentionMode) || !validRetentionDays(resp.RetentionDays) {
		// No enterprise (or a policy row that violates the CHECKs): platform defaults.
		resp.EnterpriseID = ""
		resp.RetentionMode, resp.RetentionDays = platformRetentionDefaults(c.Request.Context(), r.db)
	}

	// Cache the result in Redis
	if r.db.Redis != nil {
		cacheKey := "validate:" + keyHash
		if data, err := json.Marshal(resp); err == nil {
			r.db.RedisSet(c.Request.Context(), cacheKey, string(data), validateKeyCacheTTL)
		}
	}

	c.JSON(http.StatusOK, resp)
}

// invalidateValidateCache removes the cached validation result for a key hash.
// It should be called after any operation that may change the validation
// response (status, group, subscription, rate limits, allowed models, etc.).
func invalidateValidateCache(ctx context.Context, database *db.DB, logger *zap.Logger, keyHash string) {
	if database == nil || keyHash == "" {
		return
	}
	if database.Redis != nil {
		cacheKey := "validate:" + keyHash
		if err := database.Redis.Del(ctx, cacheKey).Err(); err != nil {
			logger.Warn("Failed to invalidate validate cache",
				zap.String("key_hash", keyHash),
				zap.Error(err),
			)
		}
	}
	// Propagate to Proxies so their in-process validation cache (~5 min TTL)
	// does not keep a disabled/deleted key alive. Dropping only the Portal's own
	// validate: cache is not enough — the Proxy caches claims independently.
	database.PublishKeyRevoked(ctx, keyHash)
}

// cacheNegativeResult caches a "key not valid" result with a short TTL so
// repeated lookups for the same unknown/disabled key skip the database.
func cacheNegativeResult(c *gin.Context, r *APIKeyRoutes, keyHash string) {
	if r.db.Redis == nil || keyHash == "" {
		return
	}
	cacheKey := "validate:" + keyHash
	neg := validateKeyResponse{Valid: false}
	if data, err := json.Marshal(neg); err == nil {
		r.db.RedisSet(c.Request.Context(), cacheKey, string(data), validateKeyNegativeCacheTTL)
	}
}

// validateRateLimitBuckets is the in-memory fallback for per-IP rate limiting
// when Redis is unavailable. Used only by checkValidateRateLimit.
var validateRateLimitBuckets = struct {
	sync.RWMutex
	m map[string]*validateRateBucket
}{m: make(map[string]*validateRateBucket)}

type validateRateBucket struct {
	tokens    float64
	lastCheck time.Time
}

// checkValidateRateLimit applies a per-IP token-bucket rate limit to the
// validate endpoint. Uses Redis for multi-instance safety when available,
// falling back to an in-memory token bucket (fail-open on Redis errors so a
// Redis outage does not take validation down).
func (r *APIKeyRoutes) checkValidateRateLimit(c *gin.Context) bool {
	ip := c.ClientIP()
	capacity := float64(validateKeyRateLimitPerSec)
	rate := float64(validateKeyRateLimitPerSec)

	if r.db.Redis != nil {
		ctx := c.Request.Context()
		key := fmt.Sprintf("validate:ratelimit:%s:%d", ip, time.Now().Unix())
		count, err := r.db.Redis.Incr(ctx, key).Result()
		if err == nil {
			if count == 1 {
				r.db.Redis.Expire(ctx, key, 2*time.Second)
			}
			return count <= int64(capacity)
		}
		// Redis error — fall through to in-memory fallback (fail-open).
	}

	validateRateLimitBuckets.Lock()
	defer validateRateLimitBuckets.Unlock()
	bucket, ok := validateRateLimitBuckets.m[ip]
	if !ok {
		bucket = &validateRateBucket{tokens: capacity, lastCheck: time.Now()}
		validateRateLimitBuckets.m[ip] = bucket
	}
	elapsed := time.Since(bucket.lastCheck).Seconds()
	bucket.tokens = min64(bucket.tokens+elapsed*rate, capacity)
	bucket.lastCheck = time.Now()
	if bucket.tokens < 1 {
		return false
	}
	bucket.tokens--
	return true
}

func min64(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func (r *APIKeyRoutes) invalidateValidateCache(ctx context.Context, keyHash string) {
	invalidateValidateCache(ctx, r.db, r.logger, keyHash)
}

// invalidateUserValidateCaches invalidates the cached validation results for
// all non-deleted keys owned by a user. Call it after anything that changes
// the user's validation outcome (status toggle, deletion, etc.).
func invalidateUserValidateCaches(ctx context.Context, database *db.DB, logger *zap.Logger, userID string) {
	if database == nil || userID == "" {
		return
	}
	rows, err := database.Query(ctx,
		`SELECT key_hash FROM api_keys
		 WHERE deleted_at IS NULL
		   AND (user_id = $1::uuid OR assigned_user_id = $1::uuid)`, userID)
	if err != nil {
		logger.Warn("Failed to list user key hashes for cache invalidation",
			zap.String("user_id", userID), zap.Error(err))
		return
	}
	defer rows.Close()
	for rows.Next() {
		var h string
		if scanErr := rows.Scan(&h); scanErr == nil {
			invalidateValidateCache(ctx, database, logger, h)
		}
	}
	// Also send a single user_banned event so Proxies drop balance + all cached
	// keys for this user in one shot (covers keys that may not have been listed,
	// e.g. race with concurrent key creation).
	database.PublishUserBanned(ctx, userID)
}

func extractBearerToken(c *gin.Context) string {
	auth := c.GetHeader("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return c.GetHeader("X-API-Key")
}

// jsonbModelList normalizes a jsonb array column (scanned as interface{}) into
// a string slice. Returns nil for NULL/empty/non-array values.
func jsonbModelList(v interface{}) []string {
	switch t := v.(type) {
	case nil:
		return nil
	case []string:
		return t
	case []interface{}:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// intersectModelAllowlist intersects the current allowed models with a plan's
// allowlist. A nil current list means "unrestricted so far", in which case the
// plan list becomes the effective allowlist.
func intersectModelAllowlist(current interface{}, plan []string) interface{} {
	if current == nil {
		return plan
	}
	planSet := make(map[string]bool, len(plan))
	for _, m := range plan {
		planSet[m] = true
	}
	cur := jsonbModelList(current)
	out := make([]string, 0, len(cur))
	for _, m := range cur {
		if planSet[m] {
			out = append(out, m)
		}
	}
	return out
}

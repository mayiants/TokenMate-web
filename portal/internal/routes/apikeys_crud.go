package routes

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/ai-proxy/ai-portal-go/internal/query"
	"github.com/ai-proxy/ai-portal-go/pkg/crypto"
	"github.com/ai-proxy/ai-portal-go/pkg/metrics"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

func (r *APIKeyRoutes) listKeys(c *gin.Context) {
	userID := c.GetString("user_id")
	limitStr := c.DefaultQuery("limit", "0")

	opts := query.APIKeysOptions{IncludeStats: true}
	if limitStr != "0" {
		if n, err := strconv.Atoi(limitStr); err == nil && n > 0 {
			opts.Limit = n
		}
	}

	data, err := query.GetAPIKeysData(c.Request.Context(), r.db, userID, opts)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to list keys"}})
		return
	}

	keys := []gin.H{}
	for _, k := range data.Items {
		displayRPM := k.RateLimitRPM
		if displayRPM <= 0 {
			displayRPM = DefaultUserRPM(r.db)
		}
		displayTPM := k.RateLimitTPM
		if displayTPM <= 0 {
			displayTPM = DefaultUserTPM(r.db)
		}
		keys = append(keys, gin.H{
			"id": k.ID, "key_prefix": k.KeyPrefix,
			"name": k.Name, "status": k.Status,
			"access_mode":        k.AccessMode,
			"allowed_models":     k.AllowedModels,
			"rate_limit_rpm":     displayRPM,
			"rate_limit_tpm":     displayTPM,
			"total_requests":     k.TotalRequests,
			"total_tokens":       k.TotalTokens,
			"month_requests":     k.MonthRequests,
			"month_tokens":       k.MonthTokens,
			"today_requests":     k.TodayRequests,
			"today_tokens":       k.TodayTokens,
			"group_id":           k.GroupID,
			"group_name":         k.GroupName,
			"key_type":           k.KeyType,
			"billing_mode":       k.BillingMode,
			"current_model":      k.CurrentModel,
			"model_routing_mode": k.ModelRoutingMode,
			"selected_models":    k.SelectedModels,
			"group_priority":     k.GroupPriority,
			"created_at":         k.CreatedAt,
			"expires_at":         k.ExpiresAt,
		})
	}

	c.JSON(http.StatusOK, gin.H{"keys": keys})
}

func (r *APIKeyRoutes) createKey(c *gin.Context) {
	userID := c.GetString("user_id")
	userEmail := c.GetString("email")

	// Parse JSON body manually to support both camelCase and snake_case
	var body map[string]interface{}
	if err := c.ShouldBindJSON(&body); err != nil {
		body = map[string]interface{}{"name": "Default Key"}
	}

	getString := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := body[k]; ok {
				if s, ok := v.(string); ok {
					return s
				}
			}
		}
		return ""
	}
	getInt := func(keys ...string) int {
		for _, k := range keys {
			if v, ok := body[k]; ok {
				switch n := v.(type) {
				case float64:
					return int(n)
				case int:
					return n
				}
			}
		}
		return 0
	}
	getStringSlice := func(keys ...string) []string {
		for _, k := range keys {
			if v, ok := body[k]; ok {
				if arr, ok := v.([]interface{}); ok {
					result := make([]string, 0, len(arr))
					for _, item := range arr {
						if s, ok := item.(string); ok {
							result = append(result, s)
						}
					}
					return result
				}
			}
		}
		return nil
	}

	req := struct {
		Name             string   `json:"name"`
		AccessMode       string   `json:"access_mode"`
		Models           []string `json:"allowed_models"`
		RateLimitRPM     int      `json:"rate_limit_rpm"`
		RateLimitTPM     int      `json:"rate_limit_tpm"`
		KeyType          string   `json:"key_type"`
		CurrentModel     string   `json:"current_model"`
		ModelRoutingMode string   `json:"model_routing_mode"`
		SelectedModels   []string `json:"selected_models"`
		GroupPriority    []string `json:"group_priority"`
		ExpiresIn        int      `json:"expires_in"`
		SubscriptionID   string   `json:"subscription_id"`
	}{
		Name:             getString("name"),
		AccessMode:       getString("access_mode", "accessMode"),
		Models:           getStringSlice("allowed_models", "allowedModels"),
		RateLimitRPM:     getInt("rate_limit_rpm", "rateLimitRpm"),
		RateLimitTPM:     getInt("rate_limit_tpm", "rateLimitTpm"),
		KeyType:          getString("key_type", "keyType"),
		CurrentModel:     getString("current_model", "currentModel"),
		ModelRoutingMode: getString("model_routing_mode", "modelRoutingMode"),
		SelectedModels:   getStringSlice("selected_models", "selectedModels"),
		GroupPriority:    getStringSlice("group_priority", "groupPriority"),
		ExpiresIn:        getInt("expires_in", "expiresIn"),
		SubscriptionID:   getString("subscription_id", "subscriptionId"),
	}
	if !validKeyExpiresInDays[req.ExpiresIn] {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "expires_in must be one of 0, 30, 90, 365"}})
		return
	}

	// Default key_type
	if req.KeyType == "" {
		req.KeyType = "general"
	}

	// key_type whitelist. Regular users may create 'general' or 'subscription'
	// keys. Enterprise members may also self-create 'enterprise' keys, but only
	// bound to their own enterprise and to an enterprise group the admin has made
	// member-visible (visibility='public'); admin_only groups stay admin-managed.
	// This is the controlled self-service model: the admin governs the boundary
	// (which groups + models + rate limits are visible), members create keys
	// freely within it. Billing goes to the enterprise credit pool and is capped
	// by the member's credit/quota limit at validation time.
	if req.KeyType != "general" && req.KeyType != "subscription" && req.KeyType != "enterprise" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"message": "Invalid key_type: must be 'general', 'subscription', or 'enterprise'.",
			},
		})
		return
	}
	if req.KeyType == "enterprise" {
		var callerEnterpriseID string
		_ = r.db.QueryRow(c.Request.Context(),
			`SELECT COALESCE(enterprise_id::text, '') FROM users WHERE id = $1::uuid AND deleted_at IS NULL`,
			userID,
		).Scan(&callerEnterpriseID)
		if callerEnterpriseID == "" {
			c.JSON(http.StatusForbidden, gin.H{
				"error": gin.H{
					"message": "Only members of an enterprise can create enterprise keys.",
				},
			})
			return
		}
		// An enterprise key must be bound to a specific enterprise group; the
		// group_priority path below validates ownership + member visibility via
		// userCanAccessGroup. Reject an enterprise key with no group.
		if len(req.GroupPriority) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": gin.H{
					"message": "An enterprise key requires selecting an enterprise group.",
				},
			})
			return
		}
	}

	// Limit personal keys per user (exclude enterprise keys assigned by admin)
	if req.KeyType != "enterprise" {
		var personalKeyCount int
		_ = r.db.QueryRow(c.Request.Context(),
			`SELECT COUNT(*) FROM api_keys WHERE user_id = $1::uuid AND deleted_at IS NULL AND NOT (key_type = 'enterprise' AND enterprise_id IS NOT NULL)`,
			userID,
		).Scan(&personalKeyCount)
		if personalKeyCount >= maxPersonalKeysPerUser {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": gin.H{
					"message": T("error.max_personal_keys_reached", c),
				},
			})
			return
		}
	}

	// Package (subscription) keys bind to a specific active subscription chosen
	// by the user; the plan's model list becomes the key's model pool. No
	// subscription group is involved anymore.
	var boundSubID *string
	var planModelPool []string
	if req.KeyType == "subscription" {
		if req.SubscriptionID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": T("error.select_subscription_for_package_key", c)}})
			return
		}
		var subID string
		var planModelsRaw []byte
		err := r.db.QueryRow(c.Request.Context(),
			`SELECT s.id::text, COALESCE(sp.model_permissions::text, 'null')
			 FROM user_subscriptions s
			 LEFT JOIN subscription_plans sp ON sp.id = s.plan_id AND sp.deleted_at IS NULL
			 WHERE s.id = $1::uuid AND s.user_id = $2::uuid AND s.status = 'active' AND s.expires_at > NOW()`,
			req.SubscriptionID, userID,
		).Scan(&subID, &planModelsRaw)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": T("error.subscription_not_active", c)}})
			return
		}
		boundSubID = &subID
		if len(planModelsRaw) > 0 && string(planModelsRaw) != "null" {
			_ = json.Unmarshal(planModelsRaw, &planModelPool)
		}
	}

	// Default model_routing_mode
	if req.ModelRoutingMode == "" {
		req.ModelRoutingMode = "single"
	}

	// Validate group_priority (max groups)
	if len(req.GroupPriority) > maxGroupPriorityGroups {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Maximum 3 groups allowed in priority"}})
		return
	}

	// Ensure selected_models is set for multi-model modes
	if (req.ModelRoutingMode == "round_robin" || req.ModelRoutingMode == "priority") && len(req.SelectedModels) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "selected_models required for round_robin/priority mode"}})
		return
	}

	// Generate API key with prefix based on key_type
	var rawKey string
	switch req.KeyType {
	case "subscription":
		rawKey = "sk-wgit-tp-" + uuid.New().String()
	case "enterprise":
		rawKey = "sk-wgit-en-" + uuid.New().String()
	default: // economy, general
		rawKey = "sk-wgit-" + uuid.New().String()
	}

	keyHash := crypto.HashKeySHA256(rawKey)
	keyPrefix := rawKey
	if len(rawKey) > apiKeyPrefixDisplayLength {
		keyPrefix = rawKey[:apiKeyPrefixDisplayLength] + "..."
	}

	keyEncrypted, err := crypto.Encrypt(rawKey, r.cfg.Auth.EncryptionKey)
	if err != nil {
		r.logger.Error("Failed to encrypt API key", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to create key"}})
		return
	}

	// Determine group_id from group_priority (first = primary)
	var groupID *string
	var groupType string
	var billingMode string
	var groupRateLimitRPM, groupRateLimitTPM int
	var groupModels interface{}

	if req.KeyType == "subscription" {
		// No group: the plan's model pool stands in for the group model list so
		// the subset validation below works unchanged.
		groupModels = planModelPool
	} else if len(req.GroupPriority) > 0 {
		// Validate that the user can access all groups in group_priority
		for _, gid := range req.GroupPriority {
			if !r.userCanAccessGroup(c, gid, userID) {
				c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "You do not have access to one or more specified groups"}})
				return
			}
		}
		groupID = &req.GroupPriority[0]
		// Get primary group config
		_ = r.db.QueryRow(c.Request.Context(),
			`SELECT COALESCE(group_type, 'general'), COALESCE(billing_mode, 'token'),
			        COALESCE(rate_limit_rpm, $2), COALESCE(rate_limit_tpm, $3), models
			 FROM user_groups WHERE id = $1::uuid`, *groupID, DefaultUserRPM(r.db), DefaultUserTPM(r.db),
		).Scan(&groupType, &billingMode, &groupRateLimitRPM, &groupRateLimitTPM, &groupModels)
	} else {
		// Fallback to user's default group
		_ = r.db.QueryRow(c.Request.Context(),
			`SELECT u.group_id::text, COALESCE(g.group_type, 'general'), COALESCE(g.billing_mode, 'token'),
			        COALESCE(g.rate_limit_rpm, $2), COALESCE(g.rate_limit_tpm, $3), g.models
			 FROM users u
			 LEFT JOIN user_groups g ON u.group_id = g.id
			 WHERE u.id = $1::uuid`, userID, DefaultUserRPM(r.db), DefaultUserTPM(r.db),
		).Scan(&groupID, &groupType, &billingMode, &groupRateLimitRPM, &groupRateLimitTPM, &groupModels)

		// Legacy users created before default-group assignment may have NULL
		// group_id. Use the platform default general group so they can still
		// create keys without admin intervention.
		if groupID == nil || *groupID == "" {
			var defaultGroupID string
			if err := r.db.QueryRow(c.Request.Context(),
				`SELECT id::text FROM user_groups WHERE group_type = 'general' AND status = 'active' ORDER BY created_at LIMIT 1`,
			).Scan(&defaultGroupID); err == nil && defaultGroupID != "" {
				groupID = &defaultGroupID
				_ = r.db.QueryRow(c.Request.Context(),
					`SELECT COALESCE(group_type, 'general'), COALESCE(billing_mode, 'token'),
					        COALESCE(rate_limit_rpm, $2), COALESCE(rate_limit_tpm, $3), models
					 FROM user_groups WHERE id = $1::uuid`, defaultGroupID, DefaultUserRPM(r.db), DefaultUserTPM(r.db),
				).Scan(&groupType, &billingMode, &groupRateLimitRPM, &groupRateLimitTPM, &groupModels)
			}
		}
	}

	// Validate key_type / group_type compatibility
	switch req.KeyType {
	case "general":
		if groupType != "general" && groupType != "economy" {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": fmt.Sprintf("general keys can only use general or economy groups, got %s", groupType)}})
			return
		}
	case "enterprise":
		if groupType != "enterprise" {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": fmt.Sprintf("enterprise keys can only use enterprise groups, got %s", groupType)}})
			return
		}
	}

	// access_mode is derived from key_type and group_type:
	// - subscription/enterprise keys keep their key_type as access_mode
	// - general keys inherit the group_type (general/economy/subscription/enterprise)
	if req.AccessMode == "" {
		switch req.KeyType {
		case "subscription":
			req.AccessMode = "subscription"
		case "enterprise":
			req.AccessMode = "enterprise"
		default:
			req.AccessMode = groupType
			if req.AccessMode == "" {
				req.AccessMode = "general"
			}
		}
	}
	if billingMode == "" {
		billingMode = "token"
	}
	if req.RateLimitRPM == 0 && req.RateLimitTPM == 0 {
		r.logger.Debug("createKey: no explicit rate limits, validate will apply system defaults")
	}

	var allowedModelsJSON interface{}
	if len(req.Models) > 0 {
		allowedModelsJSON = req.Models
	} else if groupModels != nil {
		allowedModelsJSON = groupModels
	}

	// Validate selected_models ⊆ group.models (when group has model restrictions)
	if groupModels != nil && len(req.SelectedModels) > 0 {
		var groupModelList []string
		switch v := groupModels.(type) {
		case []byte:
			_ = json.Unmarshal(v, &groupModelList)
		case string:
			_ = json.Unmarshal([]byte(v), &groupModelList)
		case []interface{}:
			// pgx may decode a jsonb array directly to []interface{}.
			for _, item := range v {
				if s, ok := item.(string); ok {
					groupModelList = append(groupModelList, s)
				}
			}
		case []string:
			groupModelList = v
		}
		if len(groupModelList) > 0 {
			for _, sm := range req.SelectedModels {
				if !containsString(groupModelList, sm) {
					c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
						"message": fmt.Sprintf("model '%s' is not available in your group. Available: %v", sm, groupModelList),
					}})
					return
				}
			}
		}
	}

	// For enterprise keys: set enterprise_id and assigned_user_id
	var enterpriseID *string
	if req.KeyType == "enterprise" {
		var entID string
		err := r.db.QueryRow(c.Request.Context(),
			`SELECT enterprise_id::text FROM users WHERE id = $1::uuid AND enterprise_id IS NOT NULL AND deleted_at IS NULL`,
			userID,
		).Scan(&entID)
		if err == nil && entID != "" {
			enterpriseID = &entID
		}
	}

	// Package keys carry the subscription chosen at creation time (validated
	// above); billing and validation both resolve through this binding.
	subscriptionID := boundSubID

	var keyID string
	expiresAt := keyExpiresAt(req.ExpiresIn)
	if enterpriseID != nil {
		err = r.db.QueryRow(c.Request.Context(),
			`INSERT INTO api_keys (user_id, key_hash, key_prefix, key_encrypted, name, status, access_mode, allowed_models,
			                       rate_limit_rpm, rate_limit_tpm, group_id, key_type, billing_mode, current_model,
			                       model_routing_mode, selected_models, group_priority, subscription_id, enterprise_id, assigned_user_id, expires_at)
			 VALUES ($1::uuid, $2, $3, $4, $5, 'active', $6, $7, $8, $9, $10::uuid, $11, $12, $13, $14, $15, $16, $17::uuid, $18::uuid, $1::uuid, $19)
			 RETURNING id::text`,
			userID, keyHash, keyPrefix, keyEncrypted, req.Name, req.AccessMode, allowedModelsJSON,
			req.RateLimitRPM, req.RateLimitTPM, groupID, req.KeyType, billingMode, req.CurrentModel,
			req.ModelRoutingMode, req.SelectedModels, req.GroupPriority, subscriptionID, *enterpriseID, expiresAt,
		).Scan(&keyID)
	} else {
		err = r.db.QueryRow(c.Request.Context(),
			`INSERT INTO api_keys (user_id, key_hash, key_prefix, key_encrypted, name, status, access_mode, allowed_models,
			                       rate_limit_rpm, rate_limit_tpm, group_id, key_type, billing_mode, current_model,
			                       model_routing_mode, selected_models, group_priority, subscription_id, expires_at)
			 VALUES ($1::uuid, $2, $3, $4, $5, 'active', $6, $7, $8, $9, $10::uuid, $11, $12, $13, $14, $15, $16, $17::uuid, $18)
			 RETURNING id::text`,
			userID, keyHash, keyPrefix, keyEncrypted, req.Name, req.AccessMode, allowedModelsJSON,
			req.RateLimitRPM, req.RateLimitTPM, groupID, req.KeyType, billingMode, req.CurrentModel,
			req.ModelRoutingMode, req.SelectedModels, req.GroupPriority, subscriptionID, expiresAt,
		).Scan(&keyID)
	}
	if err != nil {
		r.logger.Error("Failed to create API key", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to create key"}})
		return
	}
	// Best-effort active key gauge: increment when a new active key is created.
	// This is not decremented on deletion/disable; a periodic refresh would be needed for accuracy.
	metrics.GetMetrics().ActiveKeys.Add(1)

	r.audit.Log(c.Request.Context(), userID, userEmail, "apikeys", "key_created", "info",
		fmt.Sprintf("API key created: %s (type=%s)", req.Name, req.KeyType))

	// Return response with both camelCase and snake_case for compatibility
	c.JSON(http.StatusOK, gin.H{
		"id":                 keyID,
		"key":                rawKey,
		"prefix":             keyPrefix,
		"key_prefix":         keyPrefix,
		"name":               req.Name,
		"status":             "active",
		"accessMode":         req.AccessMode,
		"access_mode":        req.AccessMode,
		"rateLimitRpm":       req.RateLimitRPM,
		"rate_limit_rpm":     req.RateLimitRPM,
		"keyType":            req.KeyType,
		"key_type":           req.KeyType,
		"billingMode":        billingMode,
		"billing_mode":       billingMode,
		"currentModel":       req.CurrentModel,
		"current_model":      req.CurrentModel,
		"modelRoutingMode":   req.ModelRoutingMode,
		"model_routing_mode": req.ModelRoutingMode,
		"selectedModels":     req.SelectedModels,
		"selected_models":    req.SelectedModels,
		"groupPriority":      req.GroupPriority,
		"group_priority":     req.GroupPriority,
		"groupId":            groupID,
		"group_id":           groupID,
		"created_at":         time.Now(),
		"createdAt":          time.Now(),
	})
}

func (r *APIKeyRoutes) getKey(c *gin.Context) {
	userID := c.GetString("user_id")
	keyID := c.Param("id")

	var id, prefix, name, status, accessMode string
	var rateLimit int
	var rateLimitTPM int
	var totalRequests int
	var totalTokens int64
	var createdAt interface{}
	var groupID, keyType, billingMode, currentModel *string
	var modelRoutingMode *string
	var selectedModels, groupPriority, allowedModels interface{}
	var keyEncrypted *string

	err := r.db.QueryRow(c.Request.Context(),
		`SELECT id::text, key_prefix, name, status, access_mode, rate_limit_rpm, rate_limit_tpm, total_requests, total_tokens, created_at,
		        group_id::text, key_type, billing_mode, current_model,
		        model_routing_mode, selected_models, group_priority, key_encrypted, allowed_models
		 FROM api_keys WHERE id = $1::uuid AND user_id = $2::uuid AND deleted_at IS NULL`,
		keyID, userID,
	).Scan(&id, &prefix, &name, &status, &accessMode, &rateLimit, &rateLimitTPM, &totalRequests, &totalTokens, &createdAt,
		&groupID, &keyType, &billingMode, &currentModel,
		&modelRoutingMode, &selectedModels, &groupPriority, &keyEncrypted, &allowedModels)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Key not found"}})
		return
	}

	// Fetch recent usage records (last 30 days)
	rows, err := r.db.Query(c.Request.Context(),
		`SELECT model, provider, input_tokens, output_tokens, total_cost, latency_ms, created_at
		 FROM usage_records
		 WHERE user_api_key_id = $1::uuid AND created_at > NOW() - INTERVAL '30 days'
		 ORDER BY created_at DESC LIMIT 50`,
		keyID)
	records := []gin.H{}
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var model, provider string
			var inputTokens, outputTokens int
			// NOTE: total_cost is in micro-USD (1 USD = 1,000,000 micro-USD).
			var totalCost float64
			var latencyMs *int
			var recCreatedAt interface{}
			_ = rows.Scan(&model, &provider, &inputTokens, &outputTokens, &totalCost, &latencyMs, &recCreatedAt)
			records = append(records, gin.H{
				"model": model, "provider": provider,
				"input_tokens": inputTokens, "output_tokens": outputTokens,
				"total_cost": totalCost, "latency_ms": latencyMs,
				"created_at": recCreatedAt,
			})
		}
	}

	// Aggregated stats
	var statsTotalRequests int
	var statsTotalInput, statsTotalOutput int64
	// NOTE: statsTotalCost is in micro-USD (1 USD = 1,000,000 micro-USD).
	var statsTotalCost float64
	var statsAvgLatency *int
	var statsRPM int
	var statsTPM int64
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT COUNT(*),
		        COALESCE(SUM(input_tokens), 0),
		        COALESCE(SUM(output_tokens), 0),
		        COALESCE(SUM(total_cost), 0),
		        AVG(latency_ms)::int,
		        COUNT(*) FILTER (WHERE created_at > NOW() - INTERVAL '1 minute'),
		        COALESCE(SUM(input_tokens + output_tokens) FILTER (WHERE created_at > NOW() - INTERVAL '1 minute'), 0)
		 FROM usage_records
		 WHERE user_api_key_id = $1::uuid AND created_at > NOW() - INTERVAL '30 days'`,
		keyID).Scan(&statsTotalRequests, &statsTotalInput, &statsTotalOutput, &statsTotalCost, &statsAvgLatency, &statsRPM, &statsTPM)

	// Decrypt key_encrypted only when an explicit reveal is requested.
	// By default only the key_prefix is returned so that a compromised
	// session/XSS cannot bulk-export plaintext keys.
	var fullKey string
	reveal := c.Query("reveal") == "true"
	if reveal && keyEncrypted != nil && *keyEncrypted != "" {
		decrypted, err := crypto.Decrypt(*keyEncrypted, r.cfg.Auth.EncryptionKey)
		if err == nil {
			fullKey = decrypted
		}
		// Audit the full-key reveal so sensitive key accesses are traceable.
		r.audit.Log(c.Request.Context(), userID, c.GetString("email"), "apikeys", "key_revealed", "warning",
			fmt.Sprintf("Full API key revealed: %s", keyID))
	}

	// Resolve display RPM/TPM: 0 means "use system default" (validate applies dynamically)
	displayRPM := rateLimit
	if displayRPM <= 0 {
		displayRPM = DefaultUserRPM(r.db)
	}
	displayTPM := 0
	if rateLimitTPM > 0 {
		displayTPM = rateLimitTPM
	} else {
		displayTPM = DefaultUserTPM(r.db)
	}

	c.JSON(http.StatusOK, gin.H{
		"key": gin.H{
			"id": id, "key_prefix": prefix, "name": name, "status": status,
			"access_mode": accessMode, "rate_limit_rpm": displayRPM, "rate_limit_tpm": displayTPM,
			"total_requests": totalRequests, "total_tokens": totalTokens,
			"group_id": groupID, "key_type": keyType,
			"billing_mode": billingMode, "current_model": currentModel,
			"model_routing_mode": modelRoutingMode, "selected_models": selectedModels,
			"group_priority": groupPriority, "allowed_models": allowedModels,
			"created_at": createdAt,
			"key":        fullKey,
		},
		"records": records,
		"stats": gin.H{
			"total_requests":      statsTotalRequests,
			"total_input_tokens":  statsTotalInput,
			"total_output_tokens": statsTotalOutput,
			"total_cost":          statsTotalCost,
			"avg_latency":         statsAvgLatency,
			"rpm":                 statsRPM,
			"tpm":                 statsTPM,
		},
	})
}

func (r *APIKeyRoutes) updateKey(c *gin.Context) {
	userID := c.GetString("user_id")
	keyID := c.Param("id")

	// Fetch the key hash before updating so we can invalidate the validation cache.
	// This doubles as an ownership check: every UPDATE below is scoped by
	// user_id, so a key owned by someone else (e.g. an administrator-assigned
	// enterprise key) would match 0 rows. Reject it up front instead of
	// silently reporting success.
	var keyHash string
	if err := r.db.QueryRow(c.Request.Context(),
		`SELECT key_hash FROM api_keys WHERE id = $1::uuid AND user_id = $2::uuid AND deleted_at IS NULL`,
		keyID, userID,
	).Scan(&keyHash); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "key not found or not editable"}})
			return
		}
		r.logger.Error("database operation failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
		return
	}

	var req struct {
		Name             string   `json:"name"`
		Status           string   `json:"status"`
		CurrentModel     string   `json:"current_model,omitempty"`
		ModelRoutingMode string   `json:"model_routing_mode,omitempty"`
		SelectedModels   []string `json:"selected_models,omitempty"`
		GroupPriority    []string `json:"group_priority,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}

	// Validate status against a whitelist of allowed values.
	// NOTE: api_keys.status has a DB CHECK constraint allowing only
	// 'active', 'disabled', 'revoked', 'inactive' (see schema.go).
	// The whitelist below is for user-facing self-service actions only.
	if req.Status != "" {
		switch req.Status {
		case "active", "inactive":
		default:
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid status value"}})
			return
		}
		// Prevent reactivating a key that was disabled by an admin.
		// Admins disable keys by setting status = 'disabled'; users must
		// not bypass that by setting status back to 'active'.
		if req.Status == "active" {
			var currentStatus string
			_ = r.db.QueryRow(c.Request.Context(),
				`SELECT status FROM api_keys WHERE id = $1::uuid AND user_id = $2::uuid AND deleted_at IS NULL`,
				keyID, userID,
			).Scan(&currentStatus)
			if currentStatus == "disabled" {
				c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "Cannot reactivate a key disabled by an administrator"}})
				return
			}
		}
	}

	// Validate selected_models ⊆ group.models (same check as createKey).
	// Determine the effective group: new group_priority[0] if provided,
	// otherwise the key's existing group_id.
	var accessMode string
	if req.SelectedModels != nil {
		var effectiveGroupID *string
		if len(req.GroupPriority) > 0 {
			effectiveGroupID = &req.GroupPriority[0]
		} else {
			_ = r.db.QueryRow(c.Request.Context(),
				`SELECT group_id::text FROM api_keys WHERE id = $1::uuid AND user_id = $2::uuid AND deleted_at IS NULL`,
				keyID, userID,
			).Scan(&effectiveGroupID)
		}
		if effectiveGroupID != nil && *effectiveGroupID != "" {
			var groupModels interface{}
			_ = r.db.QueryRow(c.Request.Context(),
				`SELECT models FROM user_groups WHERE id = $1::uuid`, *effectiveGroupID,
			).Scan(&groupModels)
			if groupModels != nil {
				var groupModelList []string
				switch v := groupModels.(type) {
				case []byte:
					_ = json.Unmarshal(v, &groupModelList)
				case string:
					_ = json.Unmarshal([]byte(v), &groupModelList)
				}
				if len(groupModelList) > 0 {
					for _, sm := range req.SelectedModels {
						if !containsString(groupModelList, sm) {
							c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
								"message": fmt.Sprintf("model '%s' is not available in your group. Available: %v", sm, groupModelList),
							}})
							return
						}
					}
				}
			}
		}
	}
	if len(req.GroupPriority) > 0 {
		if len(req.GroupPriority) > 3 {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Maximum 3 groups allowed"}})
			return
		}
		// Validate that the user can access all groups in group_priority
		for _, gid := range req.GroupPriority {
			if !r.userCanAccessGroup(c, gid, userID) {
				c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "You do not have access to one or more specified groups"}})
				return
			}
		}
		// Resolve access_mode from the new primary group's type
		var groupType string
		_ = r.db.QueryRow(c.Request.Context(),
			`SELECT COALESCE(group_type, 'general') FROM user_groups WHERE id = $1::uuid`, req.GroupPriority[0]).Scan(&groupType)
		switch groupType {
		case "economy":
			accessMode = "economy"
		case "subscription", "enterprise":
			accessMode = groupType
		default:
			accessMode = "general"
		}
	}

	// Apply all field updates in a single transaction so a mid-failure does
	// not leave the key in an inconsistent state (previously up to 5 separate
	// UPDATE statements ran independently).
	err := r.db.WithTx(c.Request.Context(), func(tx pgx.Tx) error {
		if req.Name != "" {
			if _, err := tx.Exec(c.Request.Context(), `UPDATE api_keys SET name = $1, updated_at = NOW() WHERE id = $2::uuid AND user_id = $3::uuid`, req.Name, keyID, userID); err != nil {
				return err
			}
		}
		if req.Status != "" {
			if _, err := tx.Exec(c.Request.Context(), `UPDATE api_keys SET status = $1, updated_at = NOW() WHERE id = $2::uuid AND user_id = $3::uuid`, req.Status, keyID, userID); err != nil {
				return err
			}
		}
		if req.CurrentModel != "" {
			if _, err := tx.Exec(c.Request.Context(), `UPDATE api_keys SET current_model = $1, updated_at = NOW() WHERE id = $2::uuid AND user_id = $3::uuid`, req.CurrentModel, keyID, userID); err != nil {
				return err
			}
		}
		if req.ModelRoutingMode != "" {
			if _, err := tx.Exec(c.Request.Context(), `UPDATE api_keys SET model_routing_mode = $1, updated_at = NOW() WHERE id = $2::uuid AND user_id = $3::uuid`, req.ModelRoutingMode, keyID, userID); err != nil {
				return err
			}
		}
		if req.SelectedModels != nil {
			// Sync allowed_models with the UI's selected_models so that validateKey
			// sees the updated model list instead of the stale creation-time snapshot.
			if _, err := tx.Exec(c.Request.Context(), `UPDATE api_keys SET selected_models = $1, allowed_models = $1, updated_at = NOW() WHERE id = $2::uuid AND user_id = $3::uuid`, req.SelectedModels, keyID, userID); err != nil {
				return err
			}
		}
		if len(req.GroupPriority) > 0 {
			// Update group_id to first priority group
			if _, err := tx.Exec(c.Request.Context(), `UPDATE api_keys SET group_priority = $1, group_id = $2::uuid, access_mode = $3, updated_at = NOW() WHERE id = $4::uuid AND user_id = $5::uuid`, req.GroupPriority, req.GroupPriority[0], accessMode, keyID, userID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		r.logger.Error("database operation failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
		return
	}

	r.audit.Log(c.Request.Context(), userID, c.GetString("email"), "apikeys", "key_updated", "info",
		fmt.Sprintf("API key updated: %s", keyID))

	if keyHash != "" {
		r.invalidateValidateCache(c.Request.Context(), keyHash)
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (r *APIKeyRoutes) deleteKey(c *gin.Context) {
	userID := c.GetString("user_id")
	keyID := c.Param("id")

	// Fetch the key hash before soft-deleting so we can invalidate the validation cache.
	// Admin-assigned enterprise keys (assigned_user_id differs from the owner) must not
	// be deletable by the user — only self-created enterprise keys and personal keys are.
	var keyHash string
	var adminAssigned bool
	var keyName, keyType string
	var keyEnterpriseID *string
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT key_hash,
		 (key_type = 'enterprise' AND enterprise_id IS NOT NULL AND assigned_user_id IS DISTINCT FROM user_id),
		 COALESCE(name, ''), COALESCE(key_type, 'general'), enterprise_id::text
		 FROM api_keys WHERE id = $1::uuid AND user_id = $2::uuid AND deleted_at IS NULL`,
		keyID, userID,
	).Scan(&keyHash, &adminAssigned, &keyName, &keyType, &keyEnterpriseID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Key not found"}})
		return
	}

	if adminAssigned {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "Administrator-assigned enterprise keys cannot be deleted"}})
		return
	}

	if err := r.db.Exec(c.Request.Context(), `UPDATE api_keys SET deleted_at = NOW() WHERE id = $1::uuid AND user_id = $2::uuid`, keyID, userID); err != nil {
		r.logger.Error("database operation failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
		return
	}

	r.audit.Log(c.Request.Context(), userID, c.GetString("email"), "apikeys", "key_deleted", "warning",
		fmt.Sprintf("API key deleted: %s", keyID))

	// Enterprise keys are created via the enterprise flow (which writes an
	// enterprise_audit_log 'key_created' event). A self-delete here must mirror
	// that so the enterprise/audit page shows the deletion too — otherwise a
	// key vanishes with only a create log and no delete log.
	if keyType == "enterprise" && keyEnterpriseID != nil && *keyEnterpriseID != "" {
		reason := fmt.Sprintf("Enterprise key '%s' deleted by member", keyName)
		writeEnterpriseAudit(c.Request.Context(), r.db, r.logger, enterpriseAuditEntry{
			EnterpriseID: *keyEnterpriseID,
			Category:     "key",
			ChangeType:   "key_deleted",
			FieldName:    "status",
			UserID:       &userID,
			ChangedBy:    userID,
			TargetType:   "key",
			TargetID:     keyID,
			Reason:       reason,
		})
	}

	if keyHash != "" {
		r.invalidateValidateCache(c.Request.Context(), keyHash)
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// userCanAccessGroup checks whether the user is allowed to use the given group.
// Platform groups (non-enterprise) are accessible to all users. Enterprise
// groups require membership in the owning enterprise (public visibility for
// regular users; enterprise admins see all their enterprise's groups).
// userCanAccessGroup checks whether the user is allowed to use the given group.
// Invariant: platform groups (enterprise_id IS NULL) with group_type
// 'enterprise' are templates — members may NOT use them directly unless the
// template is marked inherit_to_enterprises, in which case every enterprise
// member can use it without a local copy.
func (r *APIKeyRoutes) userCanAccessGroup(c *gin.Context, groupID, userID string) bool {
	var allowed bool
	_ = r.db.QueryRow(c.Request.Context(), `
		SELECT EXISTS(
		  SELECT 1 FROM user_groups g, users u
		  WHERE g.id = $1::uuid AND u.id = $2::uuid
		    AND g.deleted_at IS NULL AND g.status = 'active'
		    AND (
		      (g.enterprise_id IS NULL AND g.group_type != 'enterprise')
		      OR u.role IN ('super_admin', 'system_admin')
		      OR (g.enterprise_id IS NULL AND g.group_type = 'enterprise' AND g.inherit_to_enterprises
		          AND (u.enterprise_id IS NOT NULL OR EXISTS(SELECT 1 FROM enterprise_members em WHERE em.user_id = u.id AND em.removed_at IS NULL)))
		      OR (g.enterprise_id = u.enterprise_id
		          AND (g.visibility = 'public' OR u.role = 'enterprise_admin'))
		    )
		)`,
		groupID, userID,
	).Scan(&allowed)
	return allowed
}

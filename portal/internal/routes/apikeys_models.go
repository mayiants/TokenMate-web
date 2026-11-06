package routes

import (
	"encoding/json"
	"net/http"
	"sort"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// keyModelEntry describes a single model available to an API key, with
// capabilities surfaced for the key-card drawer.
type keyModelEntry struct {
	Model           string      `json:"model"`
	Provider        string      `json:"provider"`
	ProductID       string      `json:"product_id"`
	APIModelName    *string     `json:"api_model_name"`
	Status          string      `json:"status"`
	InputPrice      *float64    `json:"input_price"`
	OutputPrice     *float64    `json:"output_price"`
	ContextWindow   *int        `json:"context_window"`
	MaxOutputTokens *int        `json:"max_output_tokens"`
	Capabilities    interface{} `json:"capabilities"`
	IsCurrentModel  bool        `json:"is_current_model"`
	IsSelectedModel bool        `json:"is_selected_model"`
}

// keyModelsResponse is the response for GET /api/keys/:id/models.
type keyModelsResponse struct {
	ModelRoutingMode string          `json:"model_routing_mode"`
	CurrentModel     *string         `json:"current_model"`
	SelectedModels   []string        `json:"selected_models"`
	RateLimitRPM     int             `json:"rate_limit_rpm"`
	RateLimitTPM     int             `json:"rate_limit_tpm"`
	Models           []keyModelEntry `json:"models"`
}

// getKeyModels returns the active models available to the authenticated user's
// API key, along with each model's capabilities and the key's rate limits.
func (r *APIKeyRoutes) getKeyModels(c *gin.Context) {
	userID := c.GetString("user_id")
	keyID := c.Param("id")

	var (
		keyUserID         *string
		keyAssignedUserID *string
		keyEnterpriseID   *string
		groupID           *string
		allowedModelsRaw  interface{}
		selectedModelsRaw interface{}
		currentModel      *string
		modelRoutingMode  *string
		rateLimitRPM      int
		rateLimitTPM      int
	)

	err := r.db.QueryRow(c.Request.Context(),
		`SELECT ak.user_id::text, ak.assigned_user_id::text, ak.enterprise_id::text,
		        ak.group_id::text, ak.allowed_models, ak.selected_models, ak.current_model,
		        ak.model_routing_mode, ak.rate_limit_rpm, ak.rate_limit_tpm
		 FROM api_keys ak
		 WHERE ak.id = $1::uuid AND ak.deleted_at IS NULL`,
		keyID,
	).Scan(&keyUserID, &keyAssignedUserID, &keyEnterpriseID, &groupID,
		&allowedModelsRaw, &selectedModelsRaw, &currentModel,
		&modelRoutingMode, &rateLimitRPM, &rateLimitTPM)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Key not found"}})
		return
	}

	if !r.canViewKeyModels(c, userID, keyUserID, keyAssignedUserID, keyEnterpriseID) {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "Access denied"}})
		return
	}

	mode := "single"
	if modelRoutingMode != nil && *modelRoutingMode != "" {
		mode = *modelRoutingMode
	}

	// Resolve selected_models to a string slice.
	selectedModels := modelsToStringSlice(selectedModelsRaw)
	selectedSet := make(map[string]bool, len(selectedModels))
	for _, m := range selectedModels {
		selectedSet[m] = true
	}

	// Determine the effective allowed model names:
	// 1. Start from the key's allowed_models (or group's models if empty).
	// 2. For enterprise keys with neither, fall back to the union of all
	//    enterprise group models so we do not expose the whole platform catalog.
	// 3. Intersect with active service_models.
	allowedNames := r.resolveAllowedModelNames(c, groupID, keyEnterpriseID, allowedModelsRaw)
	if len(allowedNames) == 0 {
		displayRPM := rateLimitRPM
		if displayRPM <= 0 {
			displayRPM = DefaultUserRPM(r.db)
		}
		displayTPM := rateLimitTPM
		if displayTPM <= 0 {
			displayTPM = DefaultUserTPM(r.db)
		}
		c.JSON(http.StatusOK, keyModelsResponse{
			ModelRoutingMode: mode,
			CurrentModel:     currentModel,
			SelectedModels:   selectedModels,
			RateLimitRPM:     displayRPM,
			RateLimitTPM:     displayTPM,
			Models:           []keyModelEntry{},
		})
		return
	}

	// Fetch capability metadata for all active models whose name is in the
	// allowed set. We match against both api_model_name and product_id because
	// groups may store either value.
	rows, err := r.db.Query(c.Request.Context(),
		`SELECT COALESCE(api_model_name, product_id) as model_name,
		        provider, product_id, api_model_name, status,
		        input_price, output_price, context_window, max_output_tokens, capabilities
		 FROM service_models
		 WHERE status IN ('active', 'eol_pending', 'eol')
		   AND (COALESCE(api_model_name, product_id) = ANY($1::text[])
		        OR product_id = ANY($1::text[]))`,
		allowedNames)
	if err != nil {
		r.logger.Error("Failed to load key models", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to load models"}})
		return
	}
	defer rows.Close()

	modelMap := make(map[string]keyModelEntry)
	for rows.Next() {
		var e keyModelEntry
		var capsRaw interface{}
		if err := rows.Scan(&e.Model, &e.Provider, &e.ProductID, &e.APIModelName,
			&e.Status, &e.InputPrice, &e.OutputPrice, &e.ContextWindow,
			&e.MaxOutputTokens, &capsRaw); err != nil {
			continue
		}
		e.Capabilities = normalizeCapabilities(capsRaw)
		e.IsSelectedModel = selectedSet[e.Model]
		if currentModel != nil && e.Model == *currentModel {
			e.IsCurrentModel = true
		}
		modelMap[e.Model] = e
	}

	// Resolve display RPM/TPM: 0 means "use system default"
	displayRPM := rateLimitRPM
	if displayRPM <= 0 {
		displayRPM = DefaultUserRPM(r.db)
	}
	displayTPM := rateLimitTPM
	if displayTPM <= 0 {
		displayTPM = DefaultUserTPM(r.db)
	}

	// Preserve the order returned by resolveAllowedModelNames and ensure every
	// allowed name has an entry (some may lack service_model metadata).
	models := make([]keyModelEntry, 0, len(allowedNames))
	for _, name := range allowedNames {
		if e, ok := modelMap[name]; ok {
			models = append(models, e)
		} else {
			models = append(models, keyModelEntry{
				Model:           name,
				IsSelectedModel: selectedSet[name],
				IsCurrentModel:  currentModel != nil && name == *currentModel,
			})
		}
	}

	c.JSON(http.StatusOK, keyModelsResponse{
		ModelRoutingMode: mode,
		CurrentModel:     currentModel,
		SelectedModels:   selectedModels,
		RateLimitRPM:     displayRPM,
		RateLimitTPM:     displayTPM,
		Models:           models,
	})
}

// resolveAllowedModelNames computes the ordered list of model names available
// to a key. It mirrors the semantics used by /api/keys/validate:
//   - If the key has no explicit allowed_models, inherit from the primary group.
//   - If both key and group define models, intersect them.
//   - For enterprise keys with neither, fall back to the union of all enterprise
//     group models so the platform catalog is not exposed.
//   - Filter to active service_models.
func (r *APIKeyRoutes) resolveAllowedModelNames(c *gin.Context, groupID, enterpriseID *string, allowedModelsRaw interface{}) []string {
	var keyModels []string
	if allowedModelsRaw != nil {
		switch v := allowedModelsRaw.(type) {
		case []byte:
			_ = json.Unmarshal(v, &keyModels)
		case string:
			_ = json.Unmarshal([]byte(v), &keyModels)
		case []string:
			keyModels = append([]string(nil), v...)
		case []interface{}:
			for _, item := range v {
				if s, ok := item.(string); ok {
					keyModels = append(keyModels, s)
				}
			}
		}
	}

	var groupModels []string
	if groupID != nil && *groupID != "" {
		var groupModelsRaw interface{}
		_ = r.db.QueryRow(c.Request.Context(),
			`SELECT models FROM user_groups WHERE id = $1::uuid AND deleted_at IS NULL`,
			*groupID,
		).Scan(&groupModelsRaw)
		if groupModelsRaw != nil {
			switch v := groupModelsRaw.(type) {
			case []byte:
				_ = json.Unmarshal(v, &groupModels)
			case string:
				_ = json.Unmarshal([]byte(v), &groupModels)
			case []string:
				groupModels = append([]string(nil), v...)
			case []interface{}:
				for _, item := range v {
					if s, ok := item.(string); ok {
						groupModels = append(groupModels, s)
					}
				}
			}
		}
	}

	var names []string
	if len(keyModels) == 0 && len(groupModels) == 0 {
		// No explicit restriction on the key or its group. For enterprise keys,
		// restrict to the union of all models in the enterprise's groups so we
		// never expose the whole platform catalog. Non-enterprise keys fall back
		// to all active service models, matching /api/keys/validate semantics.
		if enterpriseID != nil && *enterpriseID != "" {
			names = r.loadEnterpriseGroupModels(c, *enterpriseID)
		}
		if len(names) == 0 {
			activeModels, err := loadActiveServiceModels(c.Request.Context(), r.db, r.logger)
			if err != nil {
				r.logger.Warn("Failed to load active service models", zap.Error(err))
			}
			for m := range activeModels {
				names = append(names, m)
			}
		}
	} else if len(keyModels) == 0 {
		names = append([]string(nil), groupModels...)
	} else if len(groupModels) == 0 {
		names = append([]string(nil), keyModels...)
	} else {
		groupSet := make(map[string]bool, len(groupModels))
		for _, m := range groupModels {
			groupSet[m] = true
		}
		for _, m := range keyModels {
			if groupSet[m] {
				names = append(names, m)
			}
		}
	}

	// Filter against active service_models.
	activeModels, err := loadActiveServiceModels(c.Request.Context(), r.db, r.logger)
	if err != nil {
		r.logger.Warn("Failed to load active service models", zap.Error(err))
	}
	if len(activeModels) > 0 {
		filtered := names[:0]
		for _, m := range names {
			if activeModels[m] {
				filtered = append(filtered, m)
			}
		}
		names = filtered
	}

	// Deduplicate while preserving order.
	seen := make(map[string]bool, len(names))
	unique := make([]string, 0, len(names))
	for _, m := range names {
		if !seen[m] {
			seen[m] = true
			unique = append(unique, m)
		}
	}
	sort.Strings(unique)
	return unique
}

// loadEnterpriseGroupModels returns the union of model names across all active
// groups owned by an enterprise. This is used as the fallback allowed-model set
// when an enterprise key has no explicit group or allowed_models, preventing
// the whole platform catalog from being exposed.
func (r *APIKeyRoutes) loadEnterpriseGroupModels(c *gin.Context, enterpriseID string) []string {
	rows, err := r.db.Query(c.Request.Context(),
		`SELECT models FROM user_groups
		 WHERE enterprise_id = $1::uuid AND deleted_at IS NULL`,
		enterpriseID)
	if err != nil {
		r.logger.Warn("Failed to load enterprise group models", zap.String("enterprise_id", enterpriseID), zap.Error(err))
		return nil
	}
	defer rows.Close()

	seen := make(map[string]bool)
	var names []string
	for rows.Next() {
		var raw interface{}
		if err := rows.Scan(&raw); err != nil {
			continue
		}
		for _, m := range modelsToStringSlice(raw) {
			if m != "" && !seen[m] {
				seen[m] = true
				names = append(names, m)
			}
		}
	}
	return names
}

// canViewKeyModels checks whether the requesting user may view the key's
// available models. Owners, assignees, and admins of the owning enterprise are
// allowed.
func (r *APIKeyRoutes) canViewKeyModels(c *gin.Context, userID string, keyUserID, keyAssignedUserID, keyEnterpriseID *string) bool {
	if keyUserID != nil && *keyUserID == userID {
		return true
	}
	if keyAssignedUserID != nil && *keyAssignedUserID == userID {
		return true
	}

	role := c.GetString("role")
	if role == "super_admin" || role == "system_admin" {
		return true
	}

	if role == "enterprise_admin" && keyEnterpriseID != nil && *keyEnterpriseID != "" {
		var isAdminEnterprise bool
		_ = r.db.QueryRow(c.Request.Context(),
			`SELECT EXISTS(
			  SELECT 1 FROM users u
			  WHERE u.id = $1::uuid
			    AND u.enterprise_id = $2::uuid
			    AND u.role = 'enterprise_admin'
			    AND u.deleted_at IS NULL
			)`,
			userID, *keyEnterpriseID,
		).Scan(&isAdminEnterprise)
		return isAdminEnterprise
	}

	return false
}

// normalizeCapabilities converts a raw JSONB capabilities value into a map that
// the frontend can render consistently.
func normalizeCapabilities(raw interface{}) interface{} {
	if raw == nil {
		return gin.H{}
	}
	var caps map[string]interface{}
	switch v := raw.(type) {
	case []byte:
		_ = json.Unmarshal(v, &caps)
	case string:
		_ = json.Unmarshal([]byte(v), &caps)
	case map[string]interface{}:
		caps = v
	}
	if caps == nil {
		return gin.H{}
	}
	return caps
}

// modelsToStringSlice normalizes a JSONB array value into []string.
func modelsToStringSlice(v interface{}) []string {
	if v == nil {
		return nil
	}
	if arr, ok := v.([]string); ok {
		return arr
	}
	if arr, ok := v.([]interface{}); ok {
		result := make([]string, 0, len(arr))
		for _, item := range arr {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
		return result
	}
	if b, ok := v.([]byte); ok {
		var result []string
		_ = json.Unmarshal(b, &result)
		return result
	}
	if s, ok := v.(string); ok {
		var result []string
		_ = json.Unmarshal([]byte(s), &result)
		return result
	}
	return nil
}

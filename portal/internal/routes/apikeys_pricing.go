package routes

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/mayiants/ai-portal-go/pkg/crypto"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// keyPricing returns the model price list visible to the API key presented in
// the Authorization header — an OpenAI-style, key-authenticated pricing query
// (GET /api/keys/pricing) so customers can fetch their own price book
// programmatically, mirroring laozhang's console /api/pricing.
//
// Auth is by API key (Bearer sk-...), not a JWT session, matching /validate.
// The response is scoped to the key's group: the effective model set is the
// key's allowed_models when set, otherwise the group's models; prices come from
// model_pricing_rules (the same source as billing) and are multiplied by the
// group's pricing_ratio so the numbers match what the customer is actually
// charged.
func (r *APIKeyRoutes) keyPricing(c *gin.Context) {
	rawKey := extractBearerToken(c)
	if rawKey == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": gin.H{"message": "Missing API key", "type": "authentication_error"}})
		return
	}
	keyHash := crypto.HashKeySHA256(rawKey)

	// Resolve the key's group + effective model list, rejecting keys of
	// deleted/suspended users and disabled keys (same gate as /validate).
	var groupID string
	var pricingRatio float64
	var allowedRaw, groupModelsRaw []byte
	err := r.db.QueryRow(c.Request.Context(), `
		SELECT COALESCE(ak.group_id::text, ''),
		       COALESCE(g.pricing_ratio, 1.0),
		       COALESCE(ak.allowed_models, '[]'::jsonb),
		       COALESCE(g.models, '[]'::jsonb)
		 FROM api_keys ak
		 JOIN users u ON u.id = COALESCE(ak.assigned_user_id, ak.user_id)
		        AND u.deleted_at IS NULL AND (u.status IS NULL OR u.status != 'suspended')
		 LEFT JOIN user_groups g ON ak.group_id = g.id
		 WHERE ak.key_hash = $1 AND ak.deleted_at IS NULL AND ak.status = 'active'`,
		keyHash,
	).Scan(&groupID, &pricingRatio, &allowedRaw, &groupModelsRaw)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": gin.H{"message": "Invalid API key", "type": "authentication_error"}})
		return
	}
	if pricingRatio <= 0 {
		pricingRatio = 1.0
	}

	// Effective models: key's allowed_models if non-empty, else group's models.
	var allowed, groupModels []string
	_ = json.Unmarshal(allowedRaw, &allowed)
	_ = json.Unmarshal(groupModelsRaw, &groupModels)
	models := allowed
	if len(models) == 0 {
		models = groupModels
	}

	// Query prices from model_pricing_rules (billing source of truth),
	// deduped by api_model_name (cheapest input wins across providers).
	query := `SELECT * FROM (
		SELECT DISTINCT ON (sm.api_model_name)
			sm.api_model_name, sm.vendor,
			COALESCE(NULLIF(sm.aimodels_data->>'type',''), sm.capabilities->>'type', 'text') AS model_type,
			mpr.billing_mode,
			mpr.prices->>'input' AS input_price,
			mpr.prices->>'output' AS output_price,
			mpr.prices->>'cache_hit' AS cache_hit_price,
			mpr.prices->>'reasoning' AS reasoning_price,
			mpr.prices->>'fixed_request' AS fixed_request_price,
			mpr.prices->'variants' AS price_variants
		 FROM service_models sm
		 LEFT JOIN model_pricing_rules mpr ON mpr.status = 'active'
		   AND (mpr.effective_to IS NULL OR mpr.effective_to > NOW())
		   AND sm.provider = mpr.provider
		   AND (sm.api_model_name = mpr.api_model_name OR sm.product_id = mpr.product_id)
		 WHERE sm.status IN ('active', 'eol_pending') AND sm.deleted_at IS NULL`
	args := []interface{}{}
	if len(models) > 0 {
		query += ` AND (sm.api_model_name = ANY($1) OR sm.product_id = ANY($1))`
		args = append(args, models)
	}
	query += ` ORDER BY sm.api_model_name, COALESCE((mpr.prices->>'input')::numeric, 999999) ASC
	) AS dedup ORDER BY api_model_name`

	rows, err := r.db.Query(c.Request.Context(), query, args...)
	if err != nil {
		r.logger.Error("keyPricing query failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to query pricing"}})
		return
	}
	defer rows.Close()

	out := []gin.H{}
	for rows.Next() {
		var apiModelName, modelType string
		var vendor *string
		var billingMode, inputPrice, outputPrice, cacheHitPrice, reasoningPrice, fixedRequestPrice *string
		var priceVariants []byte
		if err := rows.Scan(&apiModelName, &vendor, &modelType, &billingMode,
			&inputPrice, &outputPrice, &cacheHitPrice, &reasoningPrice, &fixedRequestPrice, &priceVariants); err != nil {
			continue
		}
		// Apply the group's pricing_ratio so the numbers match actual billing.
		scale := func(p *string) *float64 {
			if p == nil || *p == "" {
				return nil
			}
			var f float64
			if _, err := fmt.Sscanf(*p, "%f", &f); err != nil {
				return nil
			}
			s := f * pricingRatio
			return &s
		}
		entry := gin.H{
			"model":        apiModelName,
			"vendor":       vendor,
			"model_type":   modelType,
			"currency":     "USD",
			"unit":         "per_1m_tokens",
			"input_price":  scale(inputPrice),
			"output_price": scale(outputPrice),
		}
		if billingMode != nil && *billingMode != "" {
			entry["billing_mode"] = *billingMode
			if *billingMode == "media" {
				entry["unit"] = "per_request"
			}
		}
		if v := scale(cacheHitPrice); v != nil {
			entry["cache_hit_price"] = v
		}
		if v := scale(reasoningPrice); v != nil {
			entry["reasoning_price"] = v
		}
		if v := scale(fixedRequestPrice); v != nil {
			entry["fixed_request_price"] = v
		}
		if len(priceVariants) > 0 && string(priceVariants) != "null" {
			var variants []gin.H
			if json.Unmarshal(priceVariants, &variants) == nil && len(variants) > 0 {
				// scale variant prices too
				for _, vv := range variants {
					if p, ok := vv["price"].(float64); ok {
						vv["price"] = p * pricingRatio
					}
				}
				entry["price_variants"] = variants
			}
		}
		out = append(out, entry)
	}

	c.JSON(http.StatusOK, gin.H{
		"pricing_ratio": pricingRatio,
		"data":          out,
	})
}

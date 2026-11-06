package routes

// Model capability master data.
//
// model_capability_overrides holds one row per api model name (the name
// clients send). It layers over service_models.capabilities with explicit
// provenance:
//
//  1. source='manual' — admin-edited via /api/admin/models/capabilities;
//     never auto-overwritten.
//  2. source='probe'  — reserved for capability probes (phase 2); may replace
//     'upstream' rows but never 'manual' ones.
//  3. source='upstream' — derived from upstream provider data (model tags,
//     name inference during key validation); refreshed automatically.
//
// Names without a row fall back to service_models.capabilities and finally
// to the ['text'] default, exactly as before this table existed.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/ai-proxy/ai-portal-go/internal/db"
	"github.com/gin-gonic/gin"
)

// canonicalCapabilityOrder fixes the display/storage order of type tags.
var canonicalCapabilityOrder = []string{"text", "image", "video", "audio", "embedding", "other"}

var knownCapabilityTypes = func() map[string]bool {
	m := make(map[string]bool, len(canonicalCapabilityOrder))
	for _, t := range canonicalCapabilityOrder {
		m[t] = true
	}
	return m
}()

// normalizeCapabilityTypes lowercases, dedupes and canonically orders type
// tags, dropping unknown values.
func normalizeCapabilityTypes(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range in {
		t = strings.ToLower(strings.TrimSpace(t))
		if !knownCapabilityTypes[t] || seen[t] {
			continue
		}
		seen[t] = true
	}
	for _, t := range canonicalCapabilityOrder {
		if seen[t] {
			out = append(out, t)
		}
	}
	return out
}

// loadCapabilityOverrides returns model_name -> supported_types for the given
// names in a single query. Missing names are absent from the map.
func loadCapabilityOverrides(ctx context.Context, database *db.DB, names []string) map[string][]string {
	out := map[string][]string{}
	if database == nil || len(names) == 0 {
		return out
	}
	rows, err := database.Query(ctx,
		`SELECT model_name, supported_types FROM model_capability_overrides
		 WHERE model_name = ANY($1::text[]) AND jsonb_array_length(supported_types) > 0`,
		names)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var raw []byte
		if err := rows.Scan(&name, &raw); err != nil {
			continue
		}
		var types []string
		if json.Unmarshal(raw, &types) == nil && len(types) > 0 {
			out[name] = types
		}
	}
	return out
}

// inferCapabilityTypes derives an initial supported_types guess from the
// model name alone. It exists to seed 'upstream'-source rows for models
// discovered during upstream key validation, where tag metadata may be
// unavailable; admins correct mistakes via the manual override API.
func inferCapabilityTypes(name string) []string {
	n := strings.ToLower(name)
	has := func(subs ...string) bool {
		for _, s := range subs {
			if strings.Contains(n, s) {
				return true
			}
		}
		return false
	}
	types := []string{"text"}
	switch {
	case has("embed"):
		types = []string{"embedding"}
	case has("image", "seedream", "flux", "dall-e", "dalle", "imagen"):
		types = []string{"text", "image"}
	case has("sora", "kling", "veo", "seedance", "video", "t2v", "i2v", "hailuo"):
		types = []string{"text", "video"}
	case has("audio", "whisper", "transcribe", "-tts", "voice"):
		types = []string{"text", "audio"}
	}
	return types
}

// upsertUpstreamCapabilityDefaults records 'upstream'-source capability rows
// for newly discovered model names. Existing rows are never touched — a
// later probe or an admin may have corrected them, and name inference is too
// coarse to overwrite a curated value.
func upsertUpstreamCapabilityDefaults(ctx context.Context, database *db.DB, names []string) {
	if database == nil || len(names) == 0 {
		return
	}
	for _, name := range names {
		types := normalizeCapabilityTypes(inferCapabilityTypes(name))
		if len(types) == 0 {
			continue
		}
		typesJSON, _ := json.Marshal(types)
		_ = database.Exec(ctx,
			`INSERT INTO model_capability_overrides (model_name, supported_types, source)
			 VALUES ($1, $2, 'upstream')
			 ON CONFLICT (model_name) DO NOTHING`,
			name, typesJSON)
	}
}

// ---- Admin API ----

// getModelCapabilities returns the effective capability override for a model
// name: the override row when present, otherwise null (callers fall back to
// service_models.capabilities).
// GET /api/admin/models/capabilities?model_name=xxx
func (r *AdminRoutes) getModelCapabilities(c *gin.Context) {
	name := strings.TrimSpace(c.Query("model_name"))
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "model_name is required"}})
		return
	}
	var raw []byte
	var source string
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT supported_types, source FROM model_capability_overrides WHERE model_name = $1`, name).Scan(&raw, &source)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"model_name": name, "supported_types": nil, "source": nil})
		return
	}
	var types []string
	_ = json.Unmarshal(raw, &types)
	c.JSON(http.StatusOK, gin.H{"model_name": name, "supported_types": types, "source": source})
}

// setModelCapabilities writes the manual capability override for a model
// name. An empty supported_types deletes the row so resolution falls back to
// the lower-priority sources. Manual rows are never touched by the upstream
// sync hook.
// PUT /api/admin/models/capabilities  {model_name, supported_types}
func (r *AdminRoutes) setModelCapabilities(c *gin.Context) {
	var req struct {
		ModelName      string   `json:"model_name" binding:"required"`
		SupportedTypes []string `json:"supported_types"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}
	types := normalizeCapabilityTypes(req.SupportedTypes)
	if len(types) == 0 {
		if err := r.db.Exec(c.Request.Context(),
			`DELETE FROM model_capability_overrides WHERE model_name = $1`, req.ModelName); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to clear override"}})
			return
		}
		c.JSON(http.StatusOK, gin.H{"model_name": req.ModelName, "supported_types": nil, "source": nil})
		return
	}
	typesJSON, _ := json.Marshal(types)
	if err := r.db.Exec(c.Request.Context(),
		`INSERT INTO model_capability_overrides (model_name, supported_types, source, updated_at)
		 VALUES ($1, $2, 'manual', NOW())
		 ON CONFLICT (model_name) DO UPDATE SET
		   supported_types = EXCLUDED.supported_types,
		   source = 'manual',
		   updated_at = NOW()`, req.ModelName, typesJSON); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to save override"}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"model_name": req.ModelName, "supported_types": types, "source": "manual"})
}

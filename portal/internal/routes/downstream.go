package routes

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/ai-proxy/ai-portal-go/internal/db"
	"github.com/ai-proxy/ai-portal-go/internal/middleware"
	"github.com/ai-proxy/ai-portal-go/pkg/audit"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type DownstreamRoutes struct {
	db    *db.DB
	auth  *middleware.Auth
	audit *audit.Logger
}

func NewDownstreamRoutes(db *db.DB, auth *middleware.Auth, auditLog *audit.Logger) *DownstreamRoutes {
	return &DownstreamRoutes{db: db, auth: auth, audit: auditLog}
}

var validKeyStatuses = map[string]bool{
	"active":   true,
	"disabled": true,
	"revoked":  true,
}

func (r *DownstreamRoutes) Register(rg *gin.RouterGroup) {
	authGroup := rg.Use(r.auth.RequireAuth(), r.auth.RequireAdmin())
	authGroup.GET("", r.listDownstreamKeys)
	authGroup.GET("/:id", r.getDownstreamKey)
	authGroup.GET("/:id/usage", r.getKeyUsage)
	authGroup.PATCH("/:id/status", r.updateKeyStatus)
	authGroup.DELETE("/:id", r.deleteDownstreamKey)
}

func (r *DownstreamRoutes) listDownstreamKeys(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "50"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 50
	}
	offset := (page - 1) * pageSize

	var total int
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM api_keys k
		 JOIN users u ON k.user_id = u.id
		 WHERE k.deleted_at IS NULL`,
	).Scan(&total)

	rows, err := r.db.Query(c.Request.Context(),
		`SELECT k.id, k.key_prefix, k.name, k.status, k.access_mode, k.key_type, k.allowed_models,
		        k.rate_limit_rpm, k.total_requests, k.total_tokens,
		        k.created_at, k.updated_at, k.deleted_at,
		        u.name as user_name, u.email as user_email
		 FROM api_keys k
		 JOIN users u ON k.user_id = u.id
		 WHERE k.deleted_at IS NULL
		 ORDER BY k.created_at DESC
		 LIMIT $1 OFFSET $2`, pageSize, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to list downstream keys"}})
		return
	}
	defer rows.Close()

	keys := []gin.H{}
	for rows.Next() {
		var id, keyPrefix, name, status, accessMode, keyType string
		var allowedModels interface{}
		var rateLimitRPM, totalRequests int
		var totalTokens int64
		var createdAt, updatedAt interface{}
		var deletedAt interface{}
		var userName, userEmail string
		_ = rows.Scan(&id, &keyPrefix, &name, &status, &accessMode, &keyType, &allowedModels,
			&rateLimitRPM, &totalRequests, &totalTokens,
			&createdAt, &updatedAt, &deletedAt, &userName, &userEmail)
		keys = append(keys, gin.H{
			"id": id, "key_prefix": keyPrefix, "name": name, "status": status,
			"access_mode": accessMode, "key_type": keyType, "allowed_models": allowedModels,
			"rate_limit_rpm": rateLimitRPM, "total_requests": totalRequests,
			"total_tokens": totalTokens, "created_at": createdAt,
			"updated_at": updatedAt, "deleted_at": deletedAt,
			"user_name": userName, "user_email": userEmail,
		})
	}
	c.JSON(http.StatusOK, gin.H{"keys": keys, "total": total, "page": page, "page_size": pageSize})
}

func (r *DownstreamRoutes) getDownstreamKey(c *gin.Context) {
	keyID := c.Param("id")

	var id, keyPrefix, name, status, accessMode string
	var allowedModels, modelBinding interface{}
	var rateLimitRPM, totalRequests int
	var totalTokens int64
	var balance int64
	var createdAt, updatedAt interface{}
	var deletedAt interface{}
	var userName, userEmail string
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT k.id, k.key_prefix, k.name, k.status, k.access_mode, k.allowed_models,
		        k.rate_limit_rpm, k.total_requests, k.total_tokens, k.balance,
		        k.model_binding, k.created_at, k.updated_at, k.deleted_at,
		        u.name, u.email
		 FROM api_keys k
		 JOIN users u ON k.user_id = u.id
		 WHERE k.id = $1::uuid`, keyID,
	).Scan(&id, &keyPrefix, &name, &status, &accessMode, &allowedModels,
		&rateLimitRPM, &totalRequests, &totalTokens, &balance,
		&modelBinding, &createdAt, &updatedAt, &deletedAt, &userName, &userEmail)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Key not found"}})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"key": gin.H{
			"id": id, "key_prefix": keyPrefix, "name": name, "status": status,
			"access_mode": accessMode, "allowed_models": allowedModels,
			"rate_limit_rpm": rateLimitRPM, "total_requests": totalRequests,
			"total_tokens": totalTokens, "balance": balance,
			"model_binding": modelBinding, "created_at": createdAt,
			"updated_at": updatedAt, "deleted_at": deletedAt,
			"user_name": userName, "user_email": userEmail,
		},
	})
}

func (r *DownstreamRoutes) getKeyUsage(c *gin.Context) {
	keyID := c.Param("id")
	days, _ := strconv.Atoi(c.DefaultQuery("days", "30"))

	// Recent records
	rows, err := r.db.Query(c.Request.Context(),
		`SELECT model, provider, input_tokens, output_tokens, total_cost, latency_ms, created_at
		 FROM usage_records
		 WHERE user_api_key_id = $1::uuid AND created_at > NOW() - INTERVAL '1 day' * $2
		 ORDER BY created_at DESC LIMIT 50`, keyID, days)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"records": []gin.H{}, "stats": gin.H{}})
		return
	}
	defer rows.Close()

	records := []gin.H{}
	for rows.Next() {
		var model, provider string
		var inputTokens, outputTokens int
		var totalCost float64
		var latencyMs *int
		var createdAt interface{}
		_ = rows.Scan(&model, &provider, &inputTokens, &outputTokens, &totalCost, &latencyMs, &createdAt)
		records = append(records, gin.H{
			"model": model, "provider": provider,
			"input_tokens": inputTokens, "output_tokens": outputTokens,
			"total_cost": totalCost, "latency_ms": latencyMs,
			"created_at": createdAt,
		})
	}

	// Stats
	var totalRequests int
	var totalTokens int64
	var totalCost float64
	var avgLatency *int
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT COUNT(*), COALESCE(SUM(input_tokens + output_tokens), 0), COALESCE(SUM(total_cost), 0),
		        AVG(latency_ms)::int
		 FROM usage_records
		 WHERE user_api_key_id = $1::uuid AND created_at > NOW() - INTERVAL '1 day' * $2`,
		keyID, days).Scan(&totalRequests, &totalTokens, &totalCost, &avgLatency)

	c.JSON(http.StatusOK, gin.H{
		"records": records,
		"stats": gin.H{
			"total_requests": totalRequests,
			"total_tokens":   totalTokens,
			"total_cost":     totalCost,
			"avg_latency":    avgLatency,
		},
	})
}

func (r *DownstreamRoutes) updateKeyStatus(c *gin.Context) {
	keyID := c.Param("id")
	var req struct {
		Status string `json:"status"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Status == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Status required"}})
		return
	}

	if !validKeyStatuses[req.Status] {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid status, must be one of: active, disabled, revoked"}})
		return
	}

	var keyHash, currentStatus string
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT key_hash, COALESCE(status, '') FROM api_keys WHERE id = $1::uuid AND deleted_at IS NULL`,
		keyID,
	).Scan(&keyHash, &currentStatus)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Key not found"}})
		return
	}

	if currentStatus == req.Status {
		c.JSON(http.StatusOK, gin.H{"success": true})
		return
	}

	_, err = r.db.Pool.Exec(c.Request.Context(),
		`UPDATE api_keys SET status = $1, updated_at = NOW() WHERE id = $2::uuid AND deleted_at IS NULL`,
		req.Status, keyID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to update key status"}})
		return
	}

	invalidateValidateCache(c.Request.Context(), r.db, zap.L(), keyHash)

	r.audit.Log(c.Request.Context(), c.GetString("user_id"), c.GetString("email"),
		"apikeys", "key_status_updated", "warning",
		"Admin updated key "+keyID+" status from "+currentStatus+" to "+req.Status)

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// deleteDownstreamKey soft-deletes any downstream (user/enterprise) API key by
// id, regardless of owner. This is the only path that can remove a key whose
// owning user was already (soft-)deleted — the per-user deleteKey handler
// requires user_id to match the caller, so such keys would otherwise linger
// in the api_keys table forever (visible in admin listings, non-functional
// because validateKey joins on users.deleted_at IS NULL).
//
// Cross-user deletion is destructive, so it requires the sensitive-ops
// capability (super_admin, or system_admin in mvp mode) on top of the
// group-level RequireAdmin — the same bar as deleting a privileged user.
func (r *DownstreamRoutes) deleteDownstreamKey(c *gin.Context) {
	if !r.auth.CanPerformSensitiveOps(c.GetString("role")) {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "Insufficient permissions to delete keys"}})
		return
	}

	keyID := c.Param("id")

	// Fetch the key hash before soft-deleting so the proxy validation cache
	// can be invalidated. Enterprise keys assigned to a member also have
	// assigned_user_id cleared so a later member restore cannot re-link a
	// deleted key (mirrors the enterprise revoke path).
	var keyHash, keyType string
	var keyEnterpriseID *string
	var keyName string
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT key_hash, COALESCE(key_type, 'general'), enterprise_id::text, COALESCE(name,'')
		 FROM api_keys WHERE id = $1::uuid AND deleted_at IS NULL`,
		keyID,
	).Scan(&keyHash, &keyType, &keyEnterpriseID, &keyName)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Key not found"}})
		return
	}

	if _, err := r.db.Pool.Exec(c.Request.Context(),
		`UPDATE api_keys SET deleted_at = NOW(), status = 'revoked', assigned_user_id = NULL, updated_at = NOW()
		 WHERE id = $1::uuid AND deleted_at IS NULL`,
		keyID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to delete key"}})
		return
	}

	invalidateValidateCache(c.Request.Context(), r.db, zap.L(), keyHash)

	r.audit.Log(c.Request.Context(), c.GetString("user_id"), c.GetString("email"),
		"apikeys", "key_deleted", "warning",
		"Admin deleted "+keyType+" key "+keyID)

	// Mirror enterprise-key deletion into enterprise_audit_log so the
	// enterprise/audit page shows the deletion alongside the creation event.
	if keyType == "enterprise" && keyEnterpriseID != nil && *keyEnterpriseID != "" {
		actor := c.GetString("user_id")
		writeEnterpriseAudit(c.Request.Context(), r.db, zap.L(), enterpriseAuditEntry{
			EnterpriseID: *keyEnterpriseID,
			Category:     "key",
			ChangeType:   "key_deleted",
			FieldName:    "status",
			ChangedBy:    actor,
			TargetType:   "key",
			TargetID:     keyID,
			Reason:       fmt.Sprintf("Enterprise key '%s' deleted by administrator", keyName),
		})
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

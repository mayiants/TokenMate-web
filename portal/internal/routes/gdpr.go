package routes

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/mayiants/ai-portal-go/internal/db"
	"github.com/mayiants/ai-portal-go/internal/middleware"
	"github.com/mayiants/ai-portal-go/internal/storage"
	"github.com/mayiants/ai-portal-go/pkg/mail"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type GDPRRoutes struct {
	db      *db.DB
	auth    *middleware.Auth
	mail    *mail.Sender
	storage storage.Provider
	logger  *zap.Logger
}

func NewGDPRRoutes(db *db.DB, auth *middleware.Auth, mailSender *mail.Sender, storageProvider storage.Provider, logger *zap.Logger) *GDPRRoutes {
	return &GDPRRoutes{db: db, auth: auth, mail: mailSender, storage: storageProvider, logger: logger}
}

func (r *GDPRRoutes) Register(rg *gin.RouterGroup) {
	rg.GET("/export", r.auth.RequireAuth(), r.exportData)
	rg.POST("/delete", r.auth.RequireAuth(), r.deleteData)
	rg.GET("/status", r.auth.RequireAuth(), r.getStatus)
	rg.POST("/export-requests", r.auth.RequireAuth(), r.requestFullExport)
	rg.GET("/export-requests", r.auth.RequireAuth(), r.listMyExportRequests)
	rg.GET("/export-requests/:id/download", r.auth.RequireAuth(), r.downloadExportRequest)

	admin := rg.Group("/admin")
	admin.Use(r.auth.RequireAuth(), r.auth.RequireAdmin())
	admin.GET("/export-requests", r.adminListExportRequests)
	admin.POST("/export-requests/:id/process", r.adminProcessExportRequest)
	admin.GET("/pending-deletions", r.adminListPendingDeletions)
	admin.GET("/users/:id/export", r.adminExportUser)
	admin.POST("/pending-deletions/:id/anonymize", r.adminAnonymizeNow)
}

// exportData returns the default (windowed) self-service export: last 90 days,
// max 1000 rows per section, plus always-complete profile/address/invoice data.
func (r *GDPRRoutes) exportData(c *gin.Context) {
	userID := c.GetString("user_id")
	userEmail := c.GetString("email")

	exportData, err := r.db.ExportUserData(c.Request.Context(), userID, false)
	if err != nil {
		r.logger.Error("GDPR export failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Export failed"}})
		return
	}

	if err := r.db.Exec(c.Request.Context(), `UPDATE users SET gdpr_data_exported_at = NOW() WHERE id = $1::uuid`, userID); err != nil {
		r.logger.Error("database operation failed", zap.Error(err))
	}

	if err := r.db.Exec(c.Request.Context(), `INSERT INTO audit_logs (user_id, user_email, category, action, level, message, ip_address, user_agent)
			 VALUES ($1::uuid, $2, 'gdpr', 'data_exported', 'info', 'User exported personal data (windowed)', $3, $4)`, userID, userEmail, c.ClientIP(), c.Request.UserAgent()); err != nil {
		r.logger.Error("database operation failed", zap.Error(err))
	}

	r.logger.Info("GDPR data exported", zap.String("user_id", userID), zap.String("ip", c.ClientIP()))
	c.JSON(http.StatusOK, exportData)
}

// deleteData handles a deletion request: the account is deactivated
// immediately (keys revoked, login blocked) but PII is retained for the
// configured retention window (gdpr_retention_days, default 90) so admins can
// export it for upstream compliance audits. The nightly retention job performs
// the actual anonymization after the window.
func (r *GDPRRoutes) deleteData(c *gin.Context) {
	userID := c.GetString("user_id")
	userEmail := c.GetString("email")

	var req struct {
		Confirm string `json:"confirm"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}
	if req.Confirm != "DELETE_MY_DATA" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"message": "Please confirm by sending {\"confirm\": \"DELETE_MY_DATA\"}"},
		})
		return
	}

	var alreadyRequested *time.Time
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT gdpr_erasure_requested_at FROM users WHERE id = $1::uuid`, userID).Scan(&alreadyRequested)
	if alreadyRequested != nil {
		c.JSON(http.StatusConflict, gin.H{"error": gin.H{"message": "Deletion already requested"}})
		return
	}

	// Drop cached validate results before soft-deleting the keys, otherwise
	// they keep working until the cache TTL expires.
	invalidateUserValidateCaches(c.Request.Context(), r.db, r.logger, userID)

	// Revoke all API keys immediately
	if err := r.db.Exec(c.Request.Context(), `UPDATE api_keys SET deleted_at = NOW() WHERE user_id = $1::uuid AND deleted_at IS NULL`, userID); err != nil {
		r.logger.Error("database operation failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
		return
	}

	// Deactivate account and schedule erasure; PII retained for the window
	if err := r.db.Exec(c.Request.Context(), `UPDATE users SET
				gdpr_erasure_requested_at = NOW(),
				status = 'pending_deletion',
				token_version = token_version + 1,
				updated_at = NOW()
			 WHERE id = $1::uuid`, userID); err != nil {
		r.logger.Error("database operation failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
		return
	}

	if err := r.db.Exec(c.Request.Context(), `INSERT INTO audit_logs (user_id, user_email, category, action, level, message, details, ip_address, user_agent)
			 VALUES ($1::uuid, $2, 'gdpr', 'deletion_requested', 'warning', 'User requested data deletion (deferred erasure)', '{"confirmed": true}'::jsonb, $3, $4)`, userID, userEmail, c.ClientIP(), c.Request.UserAgent()); err != nil {
		r.logger.Error("database operation failed", zap.Error(err))
	}

	r.logger.Warn("GDPR data deletion requested (deferred)",
		zap.String("user_id", userID),
		zap.String("ip", c.ClientIP()),
	)

	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"message": "Your account has been deactivated. Your data will be permanently erased after the retention period.",
	})
}

func (r *GDPRRoutes) getStatus(c *gin.Context) {
	userID := c.GetString("user_id")

	var dataExportedAt, erasureRequestedAt *time.Time
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT gdpr_data_exported_at, gdpr_erasure_requested_at FROM users WHERE id = $1::uuid`,
		userID,
	).Scan(&dataExportedAt, &erasureRequestedAt)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{})
		return
	}

	resp := gin.H{}
	if dataExportedAt != nil {
		resp["gdpr_data_exported_at"] = dataExportedAt.Format(time.RFC3339)
	}
	if erasureRequestedAt != nil {
		resp["gdpr_erasure_requested_at"] = erasureRequestedAt.Format(time.RFC3339)
	}

	c.JSON(http.StatusOK, resp)
}

// requestFullExport creates a full-export request. Admin processes it manually
// and the result is emailed as a download notice. Email defaults to the
// account's registered address.
func (r *GDPRRoutes) requestFullExport(c *gin.Context) {
	userID := c.GetString("user_id")

	var req struct {
		Email string `json:"email"`
	}
	_ = c.ShouldBindJSON(&req)

	email := req.Email
	if email == "" {
		email = c.GetString("email")
	}

	// One pending request at a time per user
	var pending int
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM gdpr_export_requests WHERE user_id = $1::uuid AND status = 'pending'`, userID).Scan(&pending)
	if pending > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": gin.H{"message": "You already have a pending export request"}})
		return
	}

	var id string
	err := r.db.QueryRow(c.Request.Context(),
		`INSERT INTO gdpr_export_requests (user_id, email, scope, status)
		 VALUES ($1::uuid, $2, 'full', 'pending') RETURNING id::text`,
		userID, email).Scan(&id)
	if err != nil {
		r.logger.Error("Failed to create export request", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Request failed"}})
		return
	}

	if err := r.db.Exec(c.Request.Context(), `INSERT INTO audit_logs (user_id, user_email, category, action, level, message, ip_address, user_agent)
			 VALUES ($1::uuid, $2, 'gdpr', 'full_export_requested', 'info', 'User requested full data export', $3, $4)`, userID, c.GetString("email"), c.ClientIP(), c.Request.UserAgent()); err != nil {
		r.logger.Error("database operation failed", zap.Error(err))
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "request_id": id, "email": email})
}

func (r *GDPRRoutes) listMyExportRequests(c *gin.Context) {
	userID := c.GetString("user_id")

	rows, err := r.db.Query(c.Request.Context(),
		`SELECT id::text, email, scope, status, requested_at, processed_at
		 FROM gdpr_export_requests WHERE user_id = $1::uuid ORDER BY requested_at DESC LIMIT 20`, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed"}})
		return
	}
	defer rows.Close()

	items := []gin.H{}
	for rows.Next() {
		var id, email, scope, status string
		var requestedAt time.Time
		var processedAt *time.Time
		if err := rows.Scan(&id, &email, &scope, &status, &requestedAt, &processedAt); err != nil {
			continue
		}
		item := gin.H{
			"id": id, "email": email, "scope": scope, "status": status,
			"requested_at": requestedAt, "processed_at": processedAt,
		}
		if status == "ready" || status == "sent" {
			item["download_url"] = "/api/gdpr/export-requests/" + id + "/download"
		}
		items = append(items, item)
	}
	c.JSON(http.StatusOK, gin.H{"requests": items})
}

func (r *GDPRRoutes) downloadExportRequest(c *gin.Context) {
	userID := c.GetString("user_id")
	reqID := c.Param("id")

	var ownerID, storageKey string
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT user_id::text, COALESCE(file_path, '') FROM gdpr_export_requests
		 WHERE id = $1::uuid AND status IN ('ready', 'sent')`, reqID).Scan(&ownerID, &storageKey)
	if err != nil || ownerID != userID || storageKey == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Export not found"}})
		return
	}

	reader, err := r.storage.Download(c.Request.Context(), storageKey)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Export file expired"}})
		return
	}
	defer func() { _ = reader.Close() }()

	_ = r.db.Exec(c.Request.Context(),
		`UPDATE gdpr_export_requests SET status = 'sent' WHERE id = $1::uuid AND status = 'ready'`, reqID)

	c.Header("Content-Type", "application/json")
	c.Header("Content-Disposition", "attachment; filename=my-data-full-"+reqID[:8]+".json")
	if _, err := io.Copy(c.Writer, reader); err != nil {
		r.logger.Error("Failed to stream export file", zap.Error(err), zap.String("request_id", reqID))
	}
}

// ── Admin endpoints ──

func (r *GDPRRoutes) adminListExportRequests(c *gin.Context) {
	status := c.DefaultQuery("status", "")

	query := `SELECT r.id::text, r.user_id::text, COALESCE(u.email, ''), r.email, r.scope, r.status,
	                 r.requested_at, r.processed_at
	          FROM gdpr_export_requests r LEFT JOIN users u ON u.id = r.user_id`
	args := []interface{}{}
	if status != "" {
		query += ` WHERE r.status = $1`
		args = append(args, status)
	}
	query += ` ORDER BY r.requested_at DESC LIMIT 200`

	rows, err := r.db.Query(c.Request.Context(), query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed"}})
		return
	}
	defer rows.Close()

	items := []gin.H{}
	for rows.Next() {
		var id, userID, userEmail, email, scope, st string
		var requestedAt time.Time
		var processedAt *time.Time
		if err := rows.Scan(&id, &userID, &userEmail, &email, &scope, &st, &requestedAt, &processedAt); err != nil {
			continue
		}
		items = append(items, gin.H{
			"id": id, "user_id": userID, "user_email": userEmail, "email": email,
			"scope": scope, "status": st, "requested_at": requestedAt, "processed_at": processedAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{"requests": items})
}

// adminProcessExportRequest generates the full export file, marks the request
// ready, and emails the user a download notice.
func (r *GDPRRoutes) adminProcessExportRequest(c *gin.Context) {
	reqID := c.Param("id")
	adminID := c.GetString("user_id")

	var userID, email string
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT user_id::text, email FROM gdpr_export_requests WHERE id = $1::uuid AND status = 'pending'`,
		reqID).Scan(&userID, &email)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Pending request not found"}})
		return
	}

	exportData, err := r.db.ExportUserData(c.Request.Context(), userID, true)
	if err != nil {
		r.logger.Error("Full export generation failed", zap.Error(err), zap.String("request_id", reqID))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Export generation failed"}})
		return
	}

	storageKey := "gdpr-exports/" + reqID + ".json"
	raw, err := json.MarshalIndent(exportData, "", "  ")
	if err != nil {
		r.logger.Error("Failed to marshal export", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Export generation failed"}})
		return
	}
	if err := r.storage.Upload(c.Request.Context(), storageKey, bytes.NewReader(raw), storage.UploadOptions{
		ContentType: "application/json",
		Size:        int64(len(raw)),
	}); err != nil {
		r.logger.Error("Failed to store export file", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Export generation failed"}})
		return
	}

	if _, err := r.db.Pool.Exec(c.Request.Context(),
		`UPDATE gdpr_export_requests SET status = 'ready', file_path = $2, processed_at = NOW(), processed_by = $3::uuid
		 WHERE id = $1::uuid`, reqID, storageKey, adminID); err != nil {
		r.logger.Error("Failed to update export request", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed"}})
		return
	}

	if r.mail != nil && r.mail.IsConfigured() {
		if err := r.mail.SendGDPRExportReady(email, c.GetString("locale")); err != nil {
			r.logger.Error("Failed to send export notification email", zap.Error(err), zap.String("request_id", reqID))
		}
	}

	if err := r.db.Exec(c.Request.Context(), `INSERT INTO audit_logs (user_id, user_email, category, action, level, message, ip_address, user_agent)
			 VALUES ($1::uuid, $2, 'gdpr', 'full_export_processed', 'info', $3, $4, $5)`,
		adminID, c.GetString("email"), "Processed full export request "+reqID+" for user "+userID, c.ClientIP(), c.Request.UserAgent()); err != nil {
		r.logger.Error("database operation failed", zap.Error(err))
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// adminListPendingDeletions lists accounts awaiting deferred erasure with the
// remaining retention days.
func (r *GDPRRoutes) adminListPendingDeletions(c *gin.Context) {
	days := 90
	var val string
	if err := r.db.QueryRow(c.Request.Context(),
		`SELECT value FROM system_settings WHERE key = 'gdpr_retention_days'`).Scan(&val); err == nil {
		if parsed, err := strconv.Atoi(val); err == nil && parsed > 0 {
			days = parsed
		}
	}

	rows, err := r.db.Query(c.Request.Context(),
		`SELECT id::text, email, name, gdpr_erasure_requested_at,
		        GREATEST(0, $1 - EXTRACT(DAY FROM NOW() - gdpr_erasure_requested_at)::int) AS days_remaining
		 FROM users
		 WHERE gdpr_erasure_requested_at IS NOT NULL AND email NOT LIKE 'deleted_%@deleted.local'
		 ORDER BY gdpr_erasure_requested_at DESC LIMIT 200`, days)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed"}})
		return
	}
	defer rows.Close()

	items := []gin.H{}
	for rows.Next() {
		var id, email, name string
		var requestedAt time.Time
		var daysRemaining int
		if err := rows.Scan(&id, &email, &name, &requestedAt, &daysRemaining); err != nil {
			continue
		}
		items = append(items, gin.H{
			"user_id": id, "email": email, "name": name,
			"requested_at": requestedAt, "days_remaining": daysRemaining,
		})
	}
	c.JSON(http.StatusOK, gin.H{"retention_days": days, "pending_deletions": items})
}

// adminExportUser exports any user's data (full scope) for compliance purposes,
// including accounts pending deletion.
func (r *GDPRRoutes) adminExportUser(c *gin.Context) {
	targetID := c.Param("id")

	exportData, err := r.db.ExportUserData(c.Request.Context(), targetID, true)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "User not found"}})
		return
	}

	if err := r.db.Exec(c.Request.Context(), `INSERT INTO audit_logs (user_id, user_email, category, action, level, message, ip_address, user_agent)
			 VALUES ($1::uuid, $2, 'gdpr', 'admin_user_export', 'warning', $3, $4, $5)`,
		c.GetString("user_id"), c.GetString("email"), "Admin exported data of user "+targetID, c.ClientIP(), c.Request.UserAgent()); err != nil {
		r.logger.Error("database operation failed", zap.Error(err))
	}

	c.Header("Content-Disposition", "attachment; filename=user-export-"+targetID[:8]+".json")
	c.JSON(http.StatusOK, exportData)
}

// adminAnonymizeNow performs immediate erasure of a pending-deletion account,
// skipping the remaining retention window.
func (r *GDPRRoutes) adminAnonymizeNow(c *gin.Context) {
	targetID := c.Param("id")

	if err := r.db.AnonymizeUser(c.Request.Context(), targetID); err != nil {
		r.logger.Error("Immediate anonymization failed", zap.Error(err), zap.String("user_id", targetID))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Anonymization failed"}})
		return
	}

	if err := r.db.Exec(c.Request.Context(), `INSERT INTO audit_logs (user_id, user_email, category, action, level, message, ip_address, user_agent)
			 VALUES ($1::uuid, $2, 'gdpr', 'admin_immediate_erasure', 'warning', $3, $4, $5)`,
		c.GetString("user_id"), c.GetString("email"), "Admin immediately erased user "+targetID, c.ClientIP(), c.Request.UserAgent()); err != nil {
		r.logger.Error("database operation failed", zap.Error(err))
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

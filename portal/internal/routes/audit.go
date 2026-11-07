package routes

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mayiants/ai-portal-go/internal/db"
	"github.com/mayiants/ai-portal-go/internal/middleware"
	"github.com/mayiants/ai-portal-go/pkg/audit"
	"github.com/mayiants/ai-portal-go/pkg/auditchain"
	"github.com/gin-gonic/gin"
)

// maxSoftDeleteReasonLen bounds the free-text reason stored on audit rows and
// echoed into the audit trail.
const maxSoftDeleteReasonLen = 500

type AuditRoutes struct {
	db    *db.DB
	auth  *middleware.Auth
	audit *audit.Logger
	// anchorKey is ENCRYPTION_KEY; the verify endpoint derives the anchor HMAC
	// key from it. Empty disables anchor HMAC checks (reported as a warning).
	anchorKey string
}

// SetAnchorKey provides the secret used to verify audit anchor signatures.
func (r *AuditRoutes) SetAnchorKey(encryptionKey string) { r.anchorKey = encryptionKey }

func NewAuditRoutes(db *db.DB, auth *middleware.Auth, auditLog *audit.Logger) *AuditRoutes {
	return &AuditRoutes{db: db, auth: auth, audit: auditLog}
}

func (r *AuditRoutes) Register(rg *gin.RouterGroup) {
	rg.GET("", r.auth.RequireAuth(), r.auth.RequireAdmin(), r.list)
	rg.GET("/deleted", r.auth.RequireAuth(), r.auth.RequireSuperAdmin(), r.listDeleted)
	rg.GET("/:id", r.auth.RequireAuth(), r.auth.RequireAdmin(), r.get)
	rg.POST("/:id/soft-delete", r.auth.RequireAuth(), r.auth.RequireSuperAdmin(), r.softDelete)
	rg.POST("/:id/restore", r.auth.RequireAuth(), r.auth.RequireSuperAdmin(), r.restore)
	rg.GET("/export", r.auth.RequireAuth(), r.auth.RequireAdmin(), r.export)
}

// RegisterVerify mounts the tamper-evidence check (GET .../verify). It lives
// on its own group (/api/admin/audit) and is restricted to super_admin: the
// same role that can soft-delete audit rows must not be the only party able to
// attest that nothing else was altered.
func (r *AuditRoutes) RegisterVerify(rg *gin.RouterGroup) {
	rg.GET("/verify", r.auth.RequireAuth(), r.auth.RequireSuperAdmin(), r.verify)
}

func (r *AuditRoutes) list(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}
	offset := (page - 1) * limit

	// Build dynamic WHERE clause
	where := "WHERE 1=1"
	args := []interface{}{}
	argIdx := 1

	// Soft delete filtering: system_admin sees only non-deleted, super_admin sees all
	role := c.GetString("role")
	if role != "super_admin" {
		where += " AND deleted_at IS NULL"
	}

	// Audit logs older than 12 months are retained for compliance but are not
	// available through the self-service UI/API. Super admins can still query
	// them directly for manual lookup requests.
	hotWindowCutoff := time.Now().AddDate(0, -12, 0)
	if role != "super_admin" {
		where += " AND created_at >= $" + strconv.Itoa(argIdx)
		args = append(args, hotWindowCutoff)
		argIdx++
	}

	if v := c.Query("action"); v != "" {
		where += " AND action = $" + strconv.Itoa(argIdx)
		args = append(args, v)
		argIdx++
	}
	if v := c.Query("category"); v != "" {
		where += " AND category = $" + strconv.Itoa(argIdx)
		args = append(args, v)
		argIdx++
	}
	if v := c.Query("target_type"); v != "" {
		where += " AND target_type = $" + strconv.Itoa(argIdx)
		args = append(args, v)
		argIdx++
	}
	if v := c.Query("target_id"); v != "" {
		where += " AND target_id = $" + strconv.Itoa(argIdx)
		args = append(args, v)
		argIdx++
	}
	if v := c.Query("user_id"); v != "" {
		where += " AND user_id = $" + strconv.Itoa(argIdx) + "::uuid"
		args = append(args, v)
		argIdx++
	}
	if v := c.Query("level"); v != "" {
		where += " AND level = $" + strconv.Itoa(argIdx)
		args = append(args, v)
		argIdx++
	}
	if v := c.Query("start_date"); v != "" {
		if startTime, err := time.Parse("2006-01-02", v); err == nil && role != "super_admin" && startTime.Before(hotWindowCutoff) {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Audit logs older than 12 months are not available through self-service. Please contact the system administrator for manual lookup."}})
			return
		}
		where += " AND created_at >= $" + strconv.Itoa(argIdx)
		args = append(args, v)
		argIdx++
	}
	if v := c.Query("end_date"); v != "" {
		where += " AND created_at <= $" + strconv.Itoa(argIdx)
		args = append(args, v)
		argIdx++
	}

	// Count
	var total int
	countArgs := make([]interface{}, len(args))
	copy(countArgs, args)
	_ = r.db.QueryRow(c.Request.Context(),
		"SELECT COUNT(*) FROM audit_logs "+where, countArgs...).Scan(&total)

	// Query
	queryArgs := append(args, limit, offset)
	rows, err := r.db.Query(c.Request.Context(),
		`SELECT id, COALESCE(user_id::text, ''), COALESCE(user_email, ''), COALESCE(category, ''), COALESCE(action, ''), COALESCE(level, ''), COALESCE(message, ''),
		        target_type, target_id, old_value, new_value, host(ip_address), created_at
		 FROM audit_logs `+where+` ORDER BY created_at DESC LIMIT $`+strconv.Itoa(argIdx)+` OFFSET $`+strconv.Itoa(argIdx+1),
		queryArgs...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": T("error.query_failed", c)}})
		return
	}
	defer rows.Close()

	logs := []gin.H{}
	for rows.Next() {
		var id int64
		var userID, userEmail, category, action, level, message string
		var targetType, targetID, oldValue, newValue, ipAddress *string
		var createdAt time.Time
		// A scan error must not yield a zero-valued row (that is how an INET
		// column scanned into *string once surfaced as blank entries).
		if err := rows.Scan(&id, &userID, &userEmail, &category, &action, &level, &message,
			&targetType, &targetID, &oldValue, &newValue, &ipAddress, &createdAt); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": T("error.query_failed", c)}})
			return
		}
		logs = append(logs, gin.H{
			"id": id, "user_id": userID, "user_email": userEmail,
			"category": category, "action": action, "level": level, "message": message,
			"target_type": targetType, "target_id": targetID,
			"old_value": oldValue, "new_value": newValue,
			"ip_address": ipAddress, "created_at": createdAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"logs": logs, "total": total,
		"page": page, "limit": limit,
		"total_pages": (total + limit - 1) / limit,
	})
}

func (r *AuditRoutes) get(c *gin.Context) {
	id := c.Param("id")
	var logID int64
	var userID, userEmail, category, action, level, message string
	var targetType, targetID, oldValue, newValue, ipAddress, userAgent *string
	var details *string
	var createdAt time.Time
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT id, COALESCE(user_id::text, ''), COALESCE(user_email, ''), COALESCE(category, ''), COALESCE(action, ''), COALESCE(level, ''), COALESCE(message, ''),
		        target_type, target_id, old_value, new_value, host(ip_address), user_agent, details, created_at
		 FROM audit_logs WHERE id = $1::bigint`, id).Scan(
		&logID, &userID, &userEmail, &category, &action, &level, &message,
		&targetType, &targetID, &oldValue, &newValue, &ipAddress, &userAgent, &details, &createdAt)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Not found"}})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"id": logID, "user_id": userID, "user_email": userEmail,
		"category": category, "action": action, "level": level, "message": message,
		"target_type": targetType, "target_id": targetID,
		"old_value": oldValue, "new_value": newValue,
		"ip_address": ipAddress, "user_agent": userAgent,
		"details": details, "created_at": createdAt,
	})
}

func (r *AuditRoutes) export(c *gin.Context) {
	// Build same WHERE clause as list
	where := "WHERE 1=1"
	args := []interface{}{}
	argIdx := 1

	// Soft delete filtering: system_admin sees only non-deleted, super_admin sees all
	role := c.GetString("role")
	if role != "super_admin" {
		where += " AND deleted_at IS NULL"
	}

	// Audit logs older than 12 months are retained for compliance but are not
	// available through the self-service UI/API. Super admins can still query
	// them directly for manual lookup requests.
	hotWindowCutoff := time.Now().AddDate(0, -12, 0)
	if role != "super_admin" {
		where += " AND created_at >= $" + strconv.Itoa(argIdx)
		args = append(args, hotWindowCutoff)
		argIdx++
	}

	if v := c.Query("action"); v != "" {
		where += " AND action = $" + strconv.Itoa(argIdx)
		args = append(args, v)
		argIdx++
	}
	if v := c.Query("category"); v != "" {
		where += " AND category = $" + strconv.Itoa(argIdx)
		args = append(args, v)
		argIdx++
	}
	if v := c.Query("start_date"); v != "" {
		if startTime, err := time.Parse("2006-01-02", v); err == nil && role != "super_admin" && startTime.Before(hotWindowCutoff) {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Audit logs older than 12 months are not available through self-service. Please contact the system administrator for manual lookup."}})
			return
		}
		where += " AND created_at >= $" + strconv.Itoa(argIdx)
		args = append(args, v)
		argIdx++
	}
	if v := c.Query("end_date"); v != "" {
		where += " AND created_at <= $" + strconv.Itoa(argIdx)
		args = append(args, v)
	}

	rows, err := r.db.Query(c.Request.Context(),
		`SELECT id, COALESCE(user_email, ''), COALESCE(category, ''), COALESCE(action, ''), COALESCE(level, ''), COALESCE(message, ''),
		        target_type, target_id, host(ip_address), created_at
		 FROM audit_logs `+where+` ORDER BY created_at DESC LIMIT 10000`,
		args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": T("error.export_failed", c)}})
		return
	}
	defer rows.Close()

	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=audit-logs-"+time.Now().UTC().Format("200601021504")+".csv")
	_, _ = c.Writer.Write([]byte{0xEF, 0xBB, 0xBF})
	_, _ = c.Writer.WriteString(T("csv.header.id", c) + "," + T("csv.header.user", c) + "," + T("csv.header.category", c) + "," + T("csv.header.action", c) + "," + T("csv.header.level", c) + "," + T("csv.header.message", c) + "," + T("csv.header.target_type", c) + "," + T("csv.header.target_id", c) + "," + T("csv.header.ip", c) + "," + T("csv.header.created_at", c) + "\n")
	for rows.Next() {
		var id int64
		var userEmail, category, action, level, message string
		var targetType, targetID, ipAddress *string
		var createdAt time.Time
		_ = rows.Scan(&id, &userEmail, &category, &action, &level, &message,
			&targetType, &targetID, &ipAddress, &createdAt)
		tt := ""
		if targetType != nil {
			tt = *targetType
		}
		ti := ""
		if targetID != nil {
			ti = *targetID
		}
		ip := ""
		if ipAddress != nil {
			ip = *ipAddress
		}
		_, _ = fmt.Fprintf(c.Writer, "%d,%s,%s,%s,%s,%s,%s,%s,%s,%s\n",
			id, csvSanitize(userEmail), csvSanitize(category), csvSanitize(action), csvSanitize(level), csvSanitize(message),
			csvSanitize(tt), csvSanitize(ti), csvSanitize(ip), createdAt.Format("2006-01-02 15:04:05"))
	}

}

func (r *AuditRoutes) listDeleted(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}
	offset := (page - 1) * limit

	where := "WHERE deleted_at IS NOT NULL"
	args := []interface{}{}
	argIdx := 1

	if v := c.Query("action"); v != "" {
		where += " AND action = $" + strconv.Itoa(argIdx)
		args = append(args, v)
		argIdx++
	}
	if v := c.Query("category"); v != "" {
		where += " AND category = $" + strconv.Itoa(argIdx)
		args = append(args, v)
		argIdx++
	}

	var total int
	_ = r.db.QueryRow(c.Request.Context(),
		"SELECT COUNT(*) FROM audit_logs "+where, args...).Scan(&total)

	queryArgs := append(args, limit, offset)
	rows, err := r.db.Query(c.Request.Context(),
		`SELECT id, COALESCE(user_id::text, ''), COALESCE(user_email, ''), COALESCE(category, ''), COALESCE(action, ''), COALESCE(level, ''), COALESCE(message, ''),
		        target_type, target_id, deleted_at, deleted_by, created_at
		 FROM audit_logs `+where+` ORDER BY deleted_at DESC LIMIT $`+strconv.Itoa(argIdx)+` OFFSET $`+strconv.Itoa(argIdx+1),
		queryArgs...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": T("error.query_failed", c)}})
		return
	}
	defer rows.Close()

	logs := []gin.H{}
	for rows.Next() {
		var id int64
		var userID, userEmail, category, action, level, message string
		var targetType, targetID *string
		var deletedAt time.Time
		var deletedBy *string
		var createdAt time.Time
		_ = rows.Scan(&id, &userID, &userEmail, &category, &action, &level, &message,
			&targetType, &targetID, &deletedAt, &deletedBy, &createdAt)
		logs = append(logs, gin.H{
			"id": id, "user_id": userID, "user_email": userEmail,
			"category": category, "action": action, "level": level, "message": message,
			"target_type": targetType, "target_id": targetID,
			"deleted_at": deletedAt, "deleted_by": deletedBy, "created_at": createdAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"logs": logs, "total": total,
		"page": page, "limit": limit,
		"total_pages": (total + limit - 1) / limit,
	})
}

// softDeleteRequest is the JSON body of POST /:id/soft-delete.
type softDeleteRequest struct {
	Reason string `json:"reason"`
}

func (r *AuditRoutes) softDelete(c *gin.Context) {
	id := c.Param("id")
	userID := c.GetString("user_id")

	// A soft delete hides evidence from the other admins, so it must be
	// explained. The reason is stored on the row (outside the hash) and echoed
	// into the audit trail below.
	var req softDeleteRequest
	_ = c.ShouldBindJSON(&req)
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": T("error.audit_delete_reason_required", c)}})
		return
	}
	if len([]rune(reason)) > maxSoftDeleteReasonLen {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": T("error.audit_delete_reason_too_long", c)}})
		return
	}

	var logEntry struct {
		UserID   string
		Category string
		Action   string
		Message  string
	}
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT COALESCE(user_id::text, ''), COALESCE(category, ''), COALESCE(action, ''), COALESCE(message, '')
		 FROM audit_logs WHERE id = $1::bigint AND deleted_at IS NULL`,
		id).Scan(&logEntry.UserID, &logEntry.Category, &logEntry.Action, &logEntry.Message)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": T("error.not_found", c)}})
		return
	}

	r.audit.LogWithDetails(c.Request.Context(), userID, c.GetString("email"),
		"security", "audit_soft_deleted", "warning",
		fmt.Sprintf("Audit log %s soft-deleted by super admin: %s (original: user=%s category=%s action=%s)",
			id, reason, logEntry.UserID, logEntry.Category, logEntry.Action),
		gin.H{"audit_log_id": id, "reason": reason, "original_category": logEntry.Category, "original_action": logEntry.Action})

	err = r.db.Exec(c.Request.Context(),
		`UPDATE audit_logs SET deleted_at = NOW(), deleted_by = $1::uuid, delete_reason = $3 WHERE id = $2::bigint AND deleted_at IS NULL`,
		userID, id, reason)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": T("error.operation_failed", c)}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (r *AuditRoutes) restore(c *gin.Context) {
	id := c.Param("id")
	userID := c.GetString("user_id")

	r.audit.Log(c.Request.Context(), userID, c.GetString("email"),
		"security", "audit_restored", "info",
		fmt.Sprintf("Audit log %s restored by super admin", id))

	err := r.db.Exec(c.Request.Context(),
		`UPDATE audit_logs SET deleted_at = NULL, deleted_by = NULL, delete_reason = NULL WHERE id = $1::bigint AND deleted_at IS NOT NULL`,
		id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": T("error.operation_failed", c)}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// parseVerifyTime accepts YYYY-MM-DD or RFC3339 and returns a UTC wall-clock
// time suitable for comparing against the TIMESTAMP (no zone) created_at.
func parseVerifyTime(v string) (*time.Time, error) {
	if v == "" {
		return nil, nil
	}
	for _, layout := range []string{"2006-01-02", time.RFC3339, "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, v); err == nil {
			u := t.UTC()
			return &u, nil
		}
	}
	return nil, fmt.Errorf("invalid time %q (use YYYY-MM-DD or RFC3339)", v)
}

// VerifyAuditTable runs the integrity check shared by the HTTP endpoint and
// the `portal verify-audit` CLI.
func VerifyAuditTable(ctx context.Context, database *db.DB, anchorKey, table, from, to string, includeUsage bool) (*db.VerifyResult, int, error) {
	if _, ok := auditchain.SpecFor(table); !ok {
		return nil, http.StatusBadRequest, fmt.Errorf("table must be audit_logs or enterprise_audit_log")
	}
	fromT, err := parseVerifyTime(from)
	if err != nil {
		return nil, http.StatusBadRequest, err
	}
	toT, err := parseVerifyTime(to)
	if err != nil {
		return nil, http.StatusBadRequest, err
	}
	res, err := database.VerifyAuditChain(ctx, db.VerifyOptions{
		Table: table, From: fromT, To: toT, AnchorKey: anchorKey, IncludeUsage: includeUsage,
	})
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	return res, http.StatusOK, nil
}

// verify recomputes the hash chain of an audit table and checks it against the
// signed anchors. HTTP 200 always carries the verdict in `ok`; a failed
// verification is a successful answer to the question that was asked.
func (r *AuditRoutes) verify(c *gin.Context) {
	table := c.DefaultQuery("table", auditchain.TableAuditLogs)
	res, status, err := VerifyAuditTable(c.Request.Context(), r.db, r.anchorKey, table, c.Query("from"), c.Query("to"), c.Query("usage") == "1")
	if err != nil {
		msg := err.Error()
		if status == http.StatusInternalServerError {
			msg = T("error.query_failed", c)
		}
		c.JSON(status, gin.H{"error": gin.H{"message": msg}})
		return
	}
	level, action := "info", "audit_verify_ok"
	if !res.OK {
		level, action = "error", "audit_verify_failed"
	}
	r.audit.LogWithDetails(c.Request.Context(), c.GetString("user_id"), c.GetString("email"),
		"security", action, level,
		fmt.Sprintf("Audit integrity check on %s: ok=%v checked=%d", table, res.OK, res.Checked),
		gin.H{"table": table, "ok": res.OK, "checked": res.Checked, "first_broken_id": res.FirstBrokenID, "reason": res.Reason})
	c.JSON(http.StatusOK, res)
}

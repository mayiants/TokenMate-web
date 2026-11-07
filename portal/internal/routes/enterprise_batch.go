package routes

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mayiants/ai-portal-go/pkg/crypto"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

const maxImportRows = 500
const maxBatchQuotaUsers = 200

// importEnterpriseMembers handles CSV batch import of members.
// The import runs inside a transaction so partial failures are rolled back.
func (r *EnterpriseRoutes) importEnterpriseMembers(c *gin.Context) {
	enterpriseID := c.GetString("enterprise_id")
	userID := c.GetString("user_id")
	ctx := c.Request.Context()

	file, _, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "CSV file is required"}})
		return
	}
	defer func() { _ = file.Close() }()

	reader := csv.NewReader(file)
	header, err := reader.Read()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Failed to read CSV header"}})
		return
	}

	// Map columns by lowercase header name.
	colMap := map[string]int{}
	colPresent := map[string]bool{}
	for i, h := range header {
		key := strings.ToLower(strings.TrimSpace(h))
		colMap[key] = i
		colPresent[key] = true
	}
	if !colPresent["email"] {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "CSV must have 'email' column"}})
		return
	}
	emailIdx := colMap["email"]
	nameIdx := colMap["name"]
	hasName := colPresent["name"]
	roleIdx := colMap["role"]
	hasRole := colPresent["role"]

	// Parse all rows up front so we can validate before opening a transaction.
	type importRow struct {
		email string
		name  string
		role  string
	}
	var rows []importRow
	for i := 0; i < maxImportRows; i++ {
		row, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": fmt.Sprintf("Failed to read CSV row %d", i+2)}})
			return
		}
		email := strings.TrimSpace(strings.ToLower(row[emailIdx]))
		if email == "" {
			continue
		}
		name := ""
		if hasName && nameIdx < len(row) {
			name = strings.TrimSpace(row[nameIdx])
		}
		role := "member"
		if hasRole && roleIdx < len(row) {
			r := strings.TrimSpace(strings.ToLower(row[roleIdx]))
			if r == "admin" {
				role = "admin"
			}
		}
		rows = append(rows, importRow{email: email, name: name, role: role})
	}

	if len(rows) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "No valid rows found in CSV"}})
		return
	}

	// Execute inside a transaction to keep users / enterprise_members / invitations consistent.
	var result struct {
		imported int
		skipped  int
		failed   int
		errors   []string
	}

	// New-user invitations (email confirmation required) get an email after commit.
	type pendingInvite struct {
		email string
		token string
	}
	var pendingInvites []pendingInvite

	err = r.db.WithTx(ctx, func(tx pgx.Tx) error {
		// Determine current member count and max members inside the transaction.
		var currentCount, maxMembers int
		if err := tx.QueryRow(ctx,
			`SELECT COUNT(*) FROM users WHERE enterprise_id = $1::uuid AND deleted_at IS NULL`,
			enterpriseID).Scan(&currentCount); err != nil {
			return fmt.Errorf("count members: %w", err)
		}
		if err := tx.QueryRow(ctx,
			`SELECT COALESCE(max_members, 50) FROM enterprises WHERE id = $1::uuid`,
			enterpriseID).Scan(&maxMembers); err != nil {
			return fmt.Errorf("get max members: %w", err)
		}

		for i, row := range rows {
			if currentCount >= maxMembers {
				result.errors = append(result.errors, fmt.Sprintf("Row %d (%s): member limit reached (%d)", i+2, row.email, maxMembers))
				result.skipped++
				continue
			}

			// Check if user already exists.
			var existingUserID string
			err := tx.QueryRow(ctx,
				`SELECT id::text FROM users WHERE email = $1 AND deleted_at IS NULL`,
				row.email).Scan(&existingUserID)

			if err != nil && err != pgx.ErrNoRows {
				result.failed++
				result.errors = append(result.errors, fmt.Sprintf("Row %d (%s): database error", i+2, row.email))
				continue
			}

			if err == pgx.ErrNoRows {
				// New user: create a pending invitation (email confirmation required).
				token := generateRandomCode("ABCDEFGHJKLMNPQRSTUVWXYZ23456789", 32)
				tokenHash := crypto.HashKeySHA256(token)
				var inviteID string
				err := tx.QueryRow(ctx, `
					INSERT INTO enterprise_invitations (enterprise_id, email, name, role, token, invited_by, status, expires_at, created_at, updated_at)
					VALUES ($1::uuid, $2, $3, $4, $5, $6::uuid, 'pending', NOW() + INTERVAL '14 days', NOW(), NOW())
					ON CONFLICT (enterprise_id, email) WHERE status IN ('pending', 'email_confirmed') DO NOTHING
					RETURNING id::text`,
					enterpriseID, row.email, row.name, row.role, tokenHash, userID).Scan(&inviteID)
				if err != nil {
					// ON CONFLICT DO NOTHING returns no row when conflict occurs; err is ErrNoRows, not a failure.
					if err == pgx.ErrNoRows {
						result.skipped++
						result.errors = append(result.errors, fmt.Sprintf("Row %d (%s): invitation already exists", i+2, row.email))
					} else {
						result.failed++
						result.errors = append(result.errors, fmt.Sprintf("Row %d (%s): invite failed", i+2, row.email))
					}
					continue
				}
				result.imported++
				currentCount++
				pendingInvites = append(pendingInvites, pendingInvite{email: row.email, token: token})
				continue
			}

			// User exists: check if already in an enterprise.
			var userEntID *string
			if err := tx.QueryRow(ctx,
				`SELECT enterprise_id::text FROM users WHERE id = $1::uuid`,
				existingUserID).Scan(&userEntID); err != nil {
				result.failed++
				result.errors = append(result.errors, fmt.Sprintf("Row %d (%s): failed to verify user", i+2, row.email))
				continue
			}
			if userEntID != nil && *userEntID != "" {
				result.skipped++
				result.errors = append(result.errors, fmt.Sprintf("Row %d (%s): already in an enterprise", i+2, row.email))
				continue
			}

			// Existing user without enterprise: pre-confirmed invitation awaiting EA
			// approval (mirrors inviteMember; no email is sent to existing users).
			token := generateRandomCode("ABCDEFGHJKLMNPQRSTUVWXYZ23456789", 32)
			tokenHash := crypto.HashKeySHA256(token)
			var inviteID string
			err = tx.QueryRow(ctx, `
				INSERT INTO enterprise_invitations (enterprise_id, email, name, role, token, status, email_confirmed, email_confirmed_at, expires_at, invited_by)
				VALUES ($1::uuid, $2, $3, $4, $5, 'email_confirmed', TRUE, NOW(), NOW() + INTERVAL '14 days', $6::uuid)
				ON CONFLICT (enterprise_id, email) WHERE status IN ('pending', 'email_confirmed') DO NOTHING
				RETURNING id::text`,
				enterpriseID, row.email, row.name, row.role, tokenHash, userID).Scan(&inviteID)
			if err != nil {
				if err == pgx.ErrNoRows {
					result.skipped++
					result.errors = append(result.errors, fmt.Sprintf("Row %d (%s): invitation already exists", i+2, row.email))
				} else {
					result.failed++
					result.errors = append(result.errors, fmt.Sprintf("Row %d (%s): invite failed", i+2, row.email))
				}
				continue
			}
			result.imported++
			currentCount++
		}
		return nil
	})

	if err != nil {
		r.logger.Error("Batch import transaction failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Import failed due to data validation errors"}})
		return
	}

	// Send invitation emails to brand-new users after the transaction committed.
	if len(pendingInvites) > 0 && r.mail != nil && r.mail.IsConfigured() {
		var enterpriseName string
		_ = r.db.QueryRow(ctx, `SELECT name FROM enterprises WHERE id = $1::uuid`, enterpriseID).Scan(&enterpriseName)
		locale := GetLocale(c)
		for _, inv := range pendingInvites {
			acceptURL := fmt.Sprintf("%s/api/enterprise/accept?token=%s", r.cfg.App.BaseURL, inv.token)
			go func(email, url string) {
				if err := r.mail.SendEnterpriseInvitation(email, enterpriseName, url, locale); err != nil {
					r.logger.Warn("Failed to send import invitation email",
						zap.String("email", email),
						zap.Error(err))
				}
			}(inv.email, acceptURL)
		}
	}

	if r.audit != nil {
		r.audit.Log(ctx, userID, c.GetString("email"), "enterprise", "members_imported", "info",
			fmt.Sprintf("Batch import: %d imported, %d skipped, %d failed", result.imported, result.skipped, result.failed))
	}

	c.JSON(http.StatusOK, gin.H{
		"success":  true,
		"imported": result.imported,
		"skipped":  result.skipped,
		"failed":   result.failed,
		"errors":   result.errors,
	})
}

// exportEnterpriseMembers exports all members as CSV using proper encoding.
func (r *EnterpriseRoutes) exportEnterpriseMembers(c *gin.Context) {
	enterpriseID := c.GetString("enterprise_id")
	ctx := c.Request.Context()

	rows, err := r.db.Query(ctx,
		`SELECT u.email, COALESCE(u.name,''), u.role, COALESCE(u.status,'active'),
		        COALESCE(em.quota_limit, 0), COALESCE(em.credit_limit, 0), u.created_at::text
		 FROM users u
		 JOIN enterprise_members em ON u.id = em.user_id AND em.enterprise_id = u.enterprise_id AND em.removed_at IS NULL
		 WHERE u.enterprise_id = $1::uuid AND u.deleted_at IS NULL
		 ORDER BY u.created_at`, enterpriseID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Export failed"}})
		return
	}
	defer rows.Close()

	var buf bytes.Buffer
	buf.WriteString("\xEF\xBB\xBF") // UTF-8 BOM
	writer := csv.NewWriter(&buf)
	_ = writer.Write([]string{"email", "name", "role", "status", "quota_limit", "credit_limit", "joined_at"})

	for rows.Next() {
		var email, name, role, status, joinedAt string
		var quotaLimit, creditLimit int64
		if err := rows.Scan(&email, &name, &role, &status, &quotaLimit, &creditLimit, &joinedAt); err != nil {
			continue
		}
		joined := ""
		if len(joinedAt) >= 10 {
			joined = joinedAt[:10]
		}
		_ = writer.Write([]string{email, name, role, status, fmt.Sprintf("%d", quotaLimit), fmt.Sprintf("%d", creditLimit), joined})
	}
	writer.Flush()

	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=members-"+time.Now().UTC().Format("200601021504")+".csv")
	c.Data(http.StatusOK, "text/csv; charset=utf-8", buf.Bytes())
}

// batchSetMemberQuota sets quota for multiple members at once using a single UPDATE.
func (r *EnterpriseRoutes) batchSetMemberQuota(c *gin.Context) {
	enterpriseID := c.GetString("enterprise_id")
	ctx := c.Request.Context()

	var req struct {
		UserIDs    []string `json:"user_ids"`
		QuotaLimit int64    `json:"quota_limit"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.UserIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "user_ids and quota_limit are required"}})
		return
	}
	if len(req.UserIDs) > maxBatchQuotaUsers {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Maximum 200 members per batch"}})
		return
	}
	if req.QuotaLimit < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "quota_limit cannot be negative"}})
		return
	}

	// Capture old values before update for audit.
	type memberLimit struct {
		userID string
		oldVal int64
	}
	var members []memberLimit
	oldRows, err := r.db.Pool.Query(ctx, `
		SELECT user_id::text, COALESCE(quota_limit, 0)
		FROM enterprise_members
		WHERE user_id = ANY($1::uuid[]) AND enterprise_id = $2::uuid AND removed_at IS NULL`,
		req.UserIDs, enterpriseID)
	if err == nil {
		for oldRows.Next() {
			var m memberLimit
			if err := oldRows.Scan(&m.userID, &m.oldVal); err == nil {
				members = append(members, m)
			}
		}
		oldRows.Close()
	}

	res, err := r.db.Pool.Exec(ctx, `
		UPDATE enterprise_members SET quota_limit = $1
		WHERE user_id = ANY($2::uuid[]) AND enterprise_id = $3::uuid AND removed_at IS NULL`,
		req.QuotaLimit, req.UserIDs, enterpriseID)
	if err != nil {
		r.logger.Error("Failed to batch set quota", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to set quota"}})
		return
	}
	updated := res.RowsAffected()

	operatorID := c.GetString("user_id")
	for _, m := range members {
		if m.oldVal == req.QuotaLimit {
			continue
		}
		if _, logErr := r.db.Pool.Exec(ctx, `
			INSERT INTO enterprise_audit_log (enterprise_id, category, change_type, field_name, action, amount, delta, old_value, new_value, reason, changed_by, user_id)
			VALUES ($1, 'billing', 'member_quota_limit_set', 'quota_limit', 'set', 0, $2, $3, $4, 'Batch quota limit updated', $5::uuid, $6::uuid)`,
			enterpriseID, req.QuotaLimit-m.oldVal, m.oldVal, req.QuotaLimit, operatorID, m.userID); logErr != nil {
			r.logger.Warn("Failed to log batch quota limit change", zap.String("user_id", m.userID), zap.Error(logErr))
		}
	}

	if r.audit != nil {
		r.audit.Log(ctx, operatorID, c.GetString("email"), "enterprise", "batch_quota_set", "info",
			fmt.Sprintf("Batch set quota_limit=%d for %d members", req.QuotaLimit, updated))
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "updated": updated})
}

// batchSetMemberCreditLimit sets credit limit for multiple members at once using a single UPDATE.
func (r *EnterpriseRoutes) batchSetMemberCreditLimit(c *gin.Context) {
	enterpriseID := c.GetString("enterprise_id")
	ctx := c.Request.Context()

	var req struct {
		UserIDs     []string `json:"user_ids"`
		CreditLimit int64    `json:"credit_limit"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.UserIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "user_ids and credit_limit are required"}})
		return
	}
	if len(req.UserIDs) > maxBatchQuotaUsers {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Maximum 200 members per batch"}})
		return
	}
	if req.CreditLimit < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "credit_limit cannot be negative"}})
		return
	}

	// Capture old values before update for audit.
	type memberCreditLimit struct {
		userID string
		oldVal int64
	}
	var creditMembers []memberCreditLimit
	oldRows, err := r.db.Pool.Query(ctx, `
		SELECT user_id::text, COALESCE(credit_limit, 0)
		FROM enterprise_members
		WHERE user_id = ANY($1::uuid[]) AND enterprise_id = $2::uuid AND removed_at IS NULL`,
		req.UserIDs, enterpriseID)
	if err == nil {
		for oldRows.Next() {
			var m memberCreditLimit
			if err := oldRows.Scan(&m.userID, &m.oldVal); err == nil {
				creditMembers = append(creditMembers, m)
			}
		}
		oldRows.Close()
	}

	res, err := r.db.Pool.Exec(ctx, `
		UPDATE enterprise_members SET credit_limit = $1
		WHERE user_id = ANY($2::uuid[]) AND enterprise_id = $3::uuid AND removed_at IS NULL`,
		req.CreditLimit, req.UserIDs, enterpriseID)
	if err != nil {
		r.logger.Error("Failed to batch set credit limit", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to set credit limit"}})
		return
	}
	updated := res.RowsAffected()

	operatorID := c.GetString("user_id")
	for _, m := range creditMembers {
		if m.oldVal == req.CreditLimit {
			continue
		}
		if _, logErr := r.db.Pool.Exec(ctx, `
			INSERT INTO enterprise_audit_log (enterprise_id, category, change_type, field_name, action, amount, delta, old_value, new_value, reason, changed_by, user_id)
			VALUES ($1, 'billing', 'member_credit_limit_set', 'credit_limit', 'set', 0, $2, $3, $4, 'Batch credit limit updated', $5::uuid, $6::uuid)`,
			enterpriseID, req.CreditLimit-m.oldVal, m.oldVal, req.CreditLimit, operatorID, m.userID); logErr != nil {
			r.logger.Warn("Failed to log batch credit limit change", zap.String("user_id", m.userID), zap.Error(logErr))
		}
	}

	if r.audit != nil {
		r.audit.Log(ctx, operatorID, c.GetString("email"), "enterprise", "batch_credit_limit_set", "info",
			fmt.Sprintf("Batch set credit_limit=%d for %d members", req.CreditLimit, updated))
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "updated": updated})
}

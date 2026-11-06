package routes

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ai-proxy/ai-portal-go/internal/config"
	"github.com/ai-proxy/ai-portal-go/internal/db"
	"github.com/ai-proxy/ai-portal-go/internal/middleware"
	"github.com/ai-proxy/ai-portal-go/pkg/audit"
	"github.com/ai-proxy/ai-portal-go/pkg/mail"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

// ProfileRoutes handles user profile management
type ProfileRoutes struct {
	db     *db.DB
	auth   *middleware.Auth
	logger *zap.Logger
	audit  *audit.Logger
	mail   *mail.Sender
}

func NewProfileRoutes(db *db.DB, auth *middleware.Auth, logger *zap.Logger, auditLog *audit.Logger, mailSender *mail.Sender) *ProfileRoutes {
	return &ProfileRoutes{db: db, auth: auth, logger: logger, audit: auditLog, mail: mailSender}
}

// Register registers all profile routes
func (r *ProfileRoutes) Register(rg *gin.RouterGroup) {
	rg.GET("", r.auth.RequireAuth(), r.getProfile)
	rg.PATCH("", r.auth.RequireAuth(), r.updateProfile)
	rg.PATCH("/password", r.auth.RequireAuth(), r.changePassword)
	rg.DELETE("/account", r.auth.RequireAuth(), r.deleteAccount)
	rg.POST("/send-change-code", r.auth.RequireAuth(), r.sendChangeCode)
	rg.PATCH("/change-email", r.auth.RequireAuth(), r.changeEmail)
	rg.PATCH("/change-phone", r.auth.RequireAuth(), r.changePhone)
	rg.POST("/send-backup-email-code", r.auth.RequireAuth(), r.sendBackupEmailCode)
	rg.PATCH("/change-backup-email", r.auth.RequireAuth(), r.changeBackupEmail)
	rg.GET("/currency", r.auth.RequireAuth(), r.getCurrency)
	rg.PUT("/currency", r.auth.RequireAuth(), r.updateCurrency)
}

func (r *ProfileRoutes) getProfile(c *gin.Context) {
	userID := c.GetString("user_id")

	var id, name, email, role, timezone string
	var balance int64
	var phone, company *string
	var createdAt time.Time

	err := r.db.QueryRow(c.Request.Context(),
		`SELECT id::text, name, email, phone, company, role, balance, COALESCE(timezone, 'UTC'), created_at
		 FROM users WHERE id = $1::uuid AND deleted_at IS NULL`,
		userID,
	).Scan(&id, &name, &email, &phone, &company, &role, &balance, &timezone, &createdAt)
	if err != nil {
		r.logger.Warn("Profile query failed", zap.Error(err), zap.String("user_id", userID))
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "User not found"}})
		return
	}

	resp := gin.H{
		"id":         id,
		"name":       name,
		"email":      email,
		"role":       role,
		"balance":    balance,
		"timezone":   timezone,
		"created_at": createdAt,
	}
	if phone != nil {
		resp["phone"] = *phone
	}
	if company != nil {
		resp["company"] = *company
	}

	c.JSON(http.StatusOK, gin.H{"user": resp})
}

func (r *ProfileRoutes) updateProfile(c *gin.Context) {
	userID := c.GetString("user_id")

	var req struct {
		Name            string      `json:"name"`
		Company         string      `json:"company"`
		Timezone        string      `json:"timezone"`
		ShippingAddress interface{} `json:"shipping_address"`
		InvoiceInfo     interface{} `json:"invoice_info"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}

	// Build dynamic update query
	updates := []string{}
	args := []interface{}{}
	argIdx := 1

	if req.Name != "" {
		updates = append(updates, "name = $"+strconv.Itoa(argIdx))
		args = append(args, req.Name)
		argIdx++
	}
	if req.Company != "" {
		updates = append(updates, "company = $"+strconv.Itoa(argIdx))
		args = append(args, req.Company)
		argIdx++
	}
	if req.Timezone != "" {
		updates = append(updates, "timezone = $"+strconv.Itoa(argIdx))
		args = append(args, req.Timezone)
		argIdx++
	}
	if req.ShippingAddress != nil {
		updates = append(updates, "shipping_address = $"+strconv.Itoa(argIdx))
		args = append(args, req.ShippingAddress)
		argIdx++
	}
	if req.InvoiceInfo != nil {
		updates = append(updates, "invoice_info = $"+strconv.Itoa(argIdx))
		args = append(args, req.InvoiceInfo)
		argIdx++
	}

	if len(updates) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "No fields to update"}})
		return
	}

	updates = append(updates, "updated_at = NOW()")
	args = append(args, userID)

	query := "UPDATE users SET " + strings.Join(updates, ", ") + " WHERE id = $" + strconv.Itoa(argIdx) + "::uuid"
	if err := r.db.Exec(c.Request.Context(), query, args...); err != nil {
		r.logger.Error("Failed to update profile", zap.String("user_id", userID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to update profile"}})
		return
	}

	if r.audit != nil {
		r.audit.Log(c.Request.Context(), c.GetString("user_id"), c.GetString("email"), "profile", "profile_updated", "info", "Profile information updated")
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (r *ProfileRoutes) changePassword(c *gin.Context) {
	var req struct {
		OldPassword string `json:"old_password" binding:"required"`
		NewPassword string `json:"new_password" binding:"required,min=8"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Old and new password required"}})
		return
	}

	userID := c.GetString("user_id")
	var passwordHash string
	err := r.db.QueryRow(c.Request.Context(), `SELECT password_hash FROM users WHERE id = $1::uuid`, userID).Scan(&passwordHash)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "User not found"}})
		return
	}

	if err := comparePassword(passwordHash, req.OldPassword); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Current password is incorrect"}})
		return
	}

	newHash, _ := hashPassword(req.NewPassword)
	if err := r.db.Exec(c.Request.Context(), `UPDATE users SET password_hash = $1, token_version = token_version + 1, updated_at = NOW() WHERE id = $2::uuid`, newHash, userID); err != nil {
		r.logger.Error("Failed to change password", zap.String("user_id", userID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to change password"}})
		return
	}

	if r.audit != nil {
		r.audit.Log(c.Request.Context(), userID, c.GetString("email"), "security", "password_changed", "warning", "Password changed successfully")
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Password changed successfully"})
}

func (r *ProfileRoutes) deleteAccount(c *gin.Context) {
	userID := c.GetString("user_id")
	ctx := c.Request.Context()

	// Deferred erasure: deactivate the account now, anonymize after the
	// retention window (see GDPRRoutes.deleteData and db.runGDPRErasure).
	var alreadyRequested *time.Time
	_ = r.db.QueryRow(ctx,
		`SELECT gdpr_erasure_requested_at FROM users WHERE id = $1::uuid`, userID).Scan(&alreadyRequested)
	if alreadyRequested != nil {
		c.JSON(http.StatusConflict, gin.H{"error": gin.H{"message": "Deletion already requested"}})
		return
	}

	// Run both writes in one transaction: if the api_keys soft-delete fails
	// the user record stays untouched, avoiding a window where the account is
	// marked pending_deletion but keys are still live.
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		r.logger.Error("Failed to begin deleteAccount transaction", zap.String("user_id", userID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to delete account"}})
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		`UPDATE users SET
			gdpr_erasure_requested_at = NOW(),
			status = 'pending_deletion',
			token_version = token_version + 1,
			updated_at = NOW()
		 WHERE id = $1::uuid`,
		userID,
	); err != nil {
		r.logger.Error("Failed to schedule account deletion", zap.String("user_id", userID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to delete account"}})
		return
	}

	if _, err := tx.Exec(ctx,
		`UPDATE api_keys SET deleted_at = NOW() WHERE user_id = $1::uuid`,
		userID,
	); err != nil {
		r.logger.Error("Failed to soft-delete user API keys", zap.String("user_id", userID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to delete account"}})
		return
	}

	if err := tx.Commit(ctx); err != nil {
		r.logger.Error("Failed to commit deleteAccount transaction", zap.String("user_id", userID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to delete account"}})
		return
	}

	// Drop cached validate results after the transaction commits so the gate
	// takes effect immediately without waiting for the cache TTL.
	invalidateUserValidateCaches(ctx, r.db, r.logger, userID)

	if r.audit != nil {
		r.audit.Log(ctx, userID, c.GetString("email"), "security", "account_deletion_requested", "warning", "Account deletion requested by user (deferred erasure)")
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Account deactivated. Your data will be permanently erased after the retention period."})
}

func hashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(hash), err
}

func comparePassword(hash, password string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
}

func (r *ProfileRoutes) sendChangeCode(c *gin.Context) {
	var req struct {
		Type  string `json:"type" binding:"required"`
		Value string `json:"value" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}

	if req.Type != "email" && req.Type != "phone" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid type, must be 'email' or 'phone'"}})
		return
	}

	// SMS gateway is not integrated; phone change codes cannot be delivered.
	if req.Type == "phone" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Phone verification is not available yet"}})
		return
	}

	userID := c.GetString("user_id")
	code := generateVerificationCode()

	if err := r.db.Exec(c.Request.Context(),
		`INSERT INTO verification_codes (user_id, code, type, value, expires_at)
		 VALUES ($1::uuid, $2, $3, $4, NOW() + INTERVAL '5 minutes')
		 ON CONFLICT (user_id, type) DO UPDATE SET code = $2, value = $4, expires_at = NOW() + INTERVAL '5 minutes'`,
		userID, code, req.Type, req.Value,
	); err != nil {
		r.logger.Error("Failed to store verification code", zap.String("user_id", userID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to send verification code"}})
		return
	}

	r.sendVerificationEmail(c, req.Value, code)

	r.logger.Info("Change verification code sent",
		zap.String("user_id", userID),
		zap.String("type", req.Type),
	)

	c.JSON(http.StatusOK, gin.H{"success": true, "cooldown": 60})
}

// sendVerificationEmail delivers a code via SMTP when configured.
// The code is never logged: it authorizes an email change and anyone with
// log access could hijack the account with it.
func (r *ProfileRoutes) sendVerificationEmail(c *gin.Context, to, code string) {
	if r.mail != nil && r.mail.IsConfigured() {
		if err := r.mail.SendVerificationCode(to, code, GetLocale(c)); err != nil {
			r.logger.Warn("Failed to send verification email", zap.String("to", to), zap.Error(err))
		}
	} else {
		r.logger.Debug("SMTP not configured, verification email not sent", zap.String("to", to))
	}
}

func (r *ProfileRoutes) changeEmail(c *gin.Context) {
	userID := c.GetString("user_id")

	var req struct {
		NewEmail         string `json:"new_email" binding:"required,email"`
		VerificationCode string `json:"verification_code" binding:"required"`
		Password         string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "All fields are required"}})
		return
	}

	var passwordHash string
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT password_hash FROM users WHERE id = $1::uuid`,
		userID,
	).Scan(&passwordHash)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "User not found"}})
		return
	}

	if err := comparePassword(passwordHash, req.Password); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Password is incorrect"}})
		return
	}

	var storedCode string
	var storedValue string
	err = r.db.QueryRow(c.Request.Context(),
		`SELECT code, value FROM verification_codes WHERE user_id = $1::uuid AND type = 'email' AND expires_at > NOW()`,
		userID,
	).Scan(&storedCode, &storedValue)
	if err != nil || storedCode != req.VerificationCode || storedValue != req.NewEmail {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Verification code is incorrect or expired"}})
		return
	}

	req.NewEmail = strings.ToLower(strings.TrimSpace(req.NewEmail))
	var emailTaken int
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM users WHERE email = $1 AND id <> $2::uuid AND deleted_at IS NULL`,
		req.NewEmail, userID,
	).Scan(&emailTaken)
	if emailTaken > 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Email is already in use"}})
		return
	}

	if err := r.db.Exec(c.Request.Context(), `UPDATE users SET email = $1, updated_at = NOW() WHERE id = $2::uuid`, req.NewEmail, userID); err != nil {
		r.logger.Error("database operation failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
		return
	}

	if err := r.db.Exec(c.Request.Context(), `DELETE FROM verification_codes WHERE user_id = $1::uuid AND type = 'email'`, userID); err != nil {
		r.logger.Error("database operation failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
		return
	}

	if err := r.db.Exec(c.Request.Context(), `INSERT INTO audit_logs (user_id, user_email, category, action, level, message, ip_address, user_agent)
			 VALUES ($1::uuid, $2, 'profile', 'email_changed', 'warning', 'Email changed to: ' || $3, $4, $5)`, userID, req.NewEmail, req.NewEmail, c.ClientIP(), c.Request.UserAgent()); err != nil {
		r.logger.Error("database operation failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
		return
	}

	r.logger.Info("Email changed",
		zap.String("user_id", userID),
		zap.String("new_email", req.NewEmail),
	)

	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (r *ProfileRoutes) changePhone(c *gin.Context) {
	userID := c.GetString("user_id")

	var req struct {
		NewPhone         string `json:"new_phone" binding:"required"`
		VerificationCode string `json:"verification_code" binding:"required"`
		Password         string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "All fields are required"}})
		return
	}

	var passwordHash string
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT password_hash FROM users WHERE id = $1::uuid`,
		userID,
	).Scan(&passwordHash)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "User not found"}})
		return
	}

	if err := comparePassword(passwordHash, req.Password); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Password is incorrect"}})
		return
	}

	var storedCode string
	var storedValue string
	err = r.db.QueryRow(c.Request.Context(),
		`SELECT code, value FROM verification_codes WHERE user_id = $1::uuid AND type = 'phone' AND expires_at > NOW()`,
		userID,
	).Scan(&storedCode, &storedValue)
	if err != nil || storedCode != req.VerificationCode || storedValue != req.NewPhone {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Verification code is incorrect or expired"}})
		return
	}

	if err := r.db.Exec(c.Request.Context(), `UPDATE users SET phone = $1, updated_at = NOW() WHERE id = $2::uuid`, req.NewPhone, userID); err != nil {
		r.logger.Error("database operation failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
		return
	}

	if err := r.db.Exec(c.Request.Context(), `DELETE FROM verification_codes WHERE user_id = $1::uuid AND type = 'phone'`, userID); err != nil {
		r.logger.Error("database operation failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (r *ProfileRoutes) sendBackupEmailCode(c *gin.Context) {
	userID := c.GetString("user_id")

	var req struct {
		Email string `json:"email" binding:"required,email"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid email format"}})
		return
	}

	code := generateVerificationCode()

	if err := r.db.Exec(c.Request.Context(), `INSERT INTO verification_codes (user_id, code, type, value, expires_at)
			 VALUES ($1::uuid, $2, 'backup_email', $3, NOW() + INTERVAL '5 minutes')
			 ON CONFLICT (user_id, type) DO UPDATE SET code = $2, value = $3, expires_at = NOW() + INTERVAL '5 minutes'`, userID, code, req.Email); err != nil {
		r.logger.Error("database operation failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
		return
	}

	r.sendVerificationEmail(c, req.Email, code)

	r.logger.Info("Backup email verification code sent",
		zap.String("user_id", userID),
		zap.String("email", req.Email),
	)

	c.JSON(http.StatusOK, gin.H{"success": true, "cooldown": 60})
}

func (r *ProfileRoutes) changeBackupEmail(c *gin.Context) {
	userID := c.GetString("user_id")

	var req struct {
		NewEmail         string `json:"new_email" binding:"required,email"`
		VerificationCode string `json:"verification_code" binding:"required"`
		Password         string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "All fields are required"}})
		return
	}
	req.NewEmail = strings.ToLower(strings.TrimSpace(req.NewEmail))

	var passwordHash string
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT password_hash FROM users WHERE id = $1::uuid`,
		userID,
	).Scan(&passwordHash)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "User not found"}})
		return
	}

	if err := comparePassword(passwordHash, req.Password); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Password is incorrect"}})
		return
	}

	var storedCode string
	var storedValue string
	err = r.db.QueryRow(c.Request.Context(),
		`SELECT code, value FROM verification_codes WHERE user_id = $1::uuid AND type = 'backup_email' AND expires_at > NOW()`,
		userID,
	).Scan(&storedCode, &storedValue)
	if err != nil || storedCode != req.VerificationCode || strings.ToLower(strings.TrimSpace(storedValue)) != req.NewEmail {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Verification code is incorrect or expired"}})
		return
	}

	if err := r.db.Exec(c.Request.Context(), `UPDATE users SET second_email = $1, updated_at = NOW() WHERE id = $2::uuid`, req.NewEmail, userID); err != nil {
		r.logger.Error("database operation failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
		return
	}

	if err := r.db.Exec(c.Request.Context(), `DELETE FROM verification_codes WHERE user_id = $1::uuid AND type = 'backup_email'`, userID); err != nil {
		r.logger.Error("database operation failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
		return
	}

	r.logger.Info("Backup email changed",
		zap.String("user_id", userID),
		zap.String("new_backup_email", req.NewEmail),
	)

	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (r *ProfileRoutes) getCurrency(c *gin.Context) {
	userID := c.GetString("user_id")
	var currency string
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT COALESCE(preferred_currency, 'USD') FROM users WHERE id = $1::uuid`,
		userID,
	).Scan(&currency)
	if err != nil {
		r.logger.Warn("Failed to get user currency", zap.Error(err), zap.String("user_id", userID))
		c.JSON(http.StatusOK, gin.H{"currency": config.DefaultCurrency})
		return
	}
	c.JSON(http.StatusOK, gin.H{"currency": currency})
}

func (r *ProfileRoutes) updateCurrency(c *gin.Context) {
	userID := c.GetString("user_id")
	var req struct {
		Currency string `json:"currency" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Currency is required"}})
		return
	}
	if err := UpdateUserPreferences(r.db, c.Request.Context(), userID, "", "", req.Currency, ""); err != nil {
		r.logger.Warn("Failed to update user currency", zap.Error(err), zap.String("user_id", userID))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to update currency"}})
		return
	}

	// Sync cookie so getCurrency() picks up the new value immediately
	secure := c.Request.TLS != nil || c.GetHeader("X-Forwarded-Proto") == "https"
	c.SetCookie("currency", req.Currency, 86400*365, "/", "", secure, false)
	c.JSON(http.StatusOK, gin.H{"success": true, "currency": req.Currency})
}

// UpdateUserPreferences updates locale, theme, currency and timezone for the
// given user. Empty values are ignored so callers can update only the fields
// they care about. This is the single implementation used by both the JSON API
// and the web preference switches; web handlers must not write their own SQL.
func UpdateUserPreferences(db *db.DB, ctx context.Context, userID, locale, theme, currency, timezone string) error {
	updates := []string{}
	args := []interface{}{}
	argIdx := 1

	if locale != "" {
		updates = append(updates, "locale = $"+strconv.Itoa(argIdx))
		args = append(args, locale)
		argIdx++
	}
	if theme != "" {
		updates = append(updates, "theme = $"+strconv.Itoa(argIdx))
		args = append(args, theme)
		argIdx++
	}
	if currency != "" {
		if !config.IsSupportedCurrency(currency) {
			return fmt.Errorf("unsupported currency: %s", currency)
		}
		updates = append(updates, "preferred_currency = $"+strconv.Itoa(argIdx))
		args = append(args, currency)
		argIdx++
	}
	if timezone != "" {
		updates = append(updates, "timezone = $"+strconv.Itoa(argIdx))
		args = append(args, timezone)
		argIdx++
	}

	if len(updates) == 0 {
		return nil
	}

	updates = append(updates, "updated_at = NOW()")
	args = append(args, userID)

	query := "UPDATE users SET " + strings.Join(updates, ", ") + " WHERE id = $" + strconv.Itoa(argIdx) + "::uuid"
	return db.Exec(ctx, query, args...)
}

// generateVerificationCode returns a 6-digit numeric code, consistent with
// the registration/password-reset codes (see generateCode in auth.go).
func generateVerificationCode() string {
	return generateCode()
}

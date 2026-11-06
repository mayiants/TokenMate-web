package routes

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

// --- User-facing APIs ---

// submitEnterpriseApplication handles user submission of enterprise registration
func (r *EnterpriseApplicationRoutes) submitEnterpriseApplication(c *gin.Context) {
	userID := c.GetString("user_id")

	// Anti-abuse: account maturity check
	var accountAgeDays int
	var emailVerified bool
	var totalUsage int
	_ = r.db.QueryRow(c.Request.Context(), `
		SELECT EXTRACT(DAY FROM NOW() - created_at)::int,
		       COALESCE(email_verified, false),
		       COALESCE((SELECT COUNT(*) FROM usage_records WHERE user_id = u.id), 0)
		FROM users u WHERE u.id = $1::uuid`, userID).Scan(&accountAgeDays, &emailVerified, &totalUsage)

	if accountAgeDays < 7 {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "Account must be at least 7 days old to apply for enterprise registration"}})
		return
	}
	if !emailVerified {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "Please verify your email address before applying"}})
		return
	}
	if totalUsage < 1 {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "Account must have at least 1 API usage record before applying"}})
		return
	}

	// Check if user already has a pending/approved application
	var existing int
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM enterprise_applications WHERE applicant_user_id = $1::uuid AND status IN ('pending_review','needs_more_info','approved')`,
		userID).Scan(&existing)
	if existing > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": gin.H{"message": "You already have an active application"}})
		return
	}

	// Anti-abuse: rejection cooldown (7 days after last rejection)
	var reviewedAt time.Time
	_ = r.db.QueryRow(c.Request.Context(), `
		SELECT reviewed_at FROM enterprise_applications
		WHERE applicant_user_id = $1::uuid AND status = 'rejected'
		ORDER BY reviewed_at DESC LIMIT 1`, userID).Scan(&reviewedAt)
	if !reviewedAt.IsZero() {
		if time.Since(reviewedAt) < 7*24*time.Hour {
			c.JSON(http.StatusTooManyRequests, gin.H{"error": gin.H{"message": "Please wait 7 days after rejection before reapplying"}})
			return
		}
	}

	// Anti-abuse: IP rate limiting
	clientIP := c.ClientIP()
	rateKey := fmt.Sprintf("enterprise_apply:%s", clientIP)
	if r.db.Redis != nil {
		count, _ := r.db.Redis.Incr(c.Request.Context(), rateKey).Result()
		if count == 1 {
			r.db.Redis.Expire(c.Request.Context(), rateKey, 24*time.Hour)
		}
		if count > 3 {
			c.JSON(http.StatusTooManyRequests, gin.H{"error": gin.H{"message": "Too many applications from this address. Please try again tomorrow."}})
			return
		}
	}

	// Check if user is already in an enterprise
	var enterpriseID *string
	_ = r.db.QueryRow(c.Request.Context(), `SELECT enterprise_id::text FROM users WHERE id = $1::uuid`, userID).Scan(&enterpriseID)
	if enterpriseID != nil && *enterpriseID != "" {
		c.JSON(http.StatusConflict, gin.H{"error": gin.H{"message": "You are already a member of an enterprise"}})
		return
	}

	var req struct {
		EnterpriseName     string `json:"enterprise_name"`
		Country            string `json:"country"`
		ContactName        string `json:"contact_name"`
		ContactEmail       string `json:"contact_email"`
		ContactPhone       string `json:"contact_phone"`
		RegistrationNumber string `json:"registration_number"`
		RegisteredAddress  string `json:"registered_address"`
		BankName           string `json:"bank_name"`
		BankAccount        string `json:"bank_account"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}
	req.EnterpriseName = strings.TrimSpace(req.EnterpriseName)
	req.Country = strings.TrimSpace(req.Country)
	req.ContactName = strings.TrimSpace(req.ContactName)
	req.ContactEmail = strings.TrimSpace(req.ContactEmail)
	if req.EnterpriseName == "" || len(req.EnterpriseName) > 255 {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Enterprise name is required (max 255 chars)"}})
		return
	}
	if req.Country == "" || len(req.Country) > 100 {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Country is required (max 100 chars)"}})
		return
	}
	if req.ContactName == "" || len(req.ContactName) > 255 {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Contact name is required"}})
		return
	}

	var appID string
	err := r.db.QueryRow(c.Request.Context(), `
		INSERT INTO enterprise_applications (applicant_user_id, enterprise_name, country, contact_name, contact_email, contact_phone, registration_number, registered_address, bank_name, bank_account)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING id::text`,
		userID, req.EnterpriseName, req.Country, req.ContactName, req.ContactEmail, req.ContactPhone, req.RegistrationNumber, req.RegisteredAddress, req.BankName, req.BankAccount,
	).Scan(&appID)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate") || strings.Contains(err.Error(), "unique") {
			c.JSON(http.StatusConflict, gin.H{"error": gin.H{"message": "You already have an active application"}})
			return
		}
		r.logger.Error("Failed to create enterprise application", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to submit application"}})
		return
	}

	r.audit.Log(c.Request.Context(), userID, c.GetString("email"), "user", "enterprise_application_submitted", "info",
		fmt.Sprintf("Enterprise application submitted: %s (%s)", req.EnterpriseName, appID))

	c.JSON(http.StatusOK, gin.H{"success": true, "application_id": appID})
}

// getMyEnterpriseApplication returns the current user's application status
func (r *EnterpriseApplicationRoutes) getMyEnterpriseApplication(c *gin.Context) {
	userID := c.GetString("user_id")

	var app struct {
		ID                 string  `json:"id"`
		EnterpriseName     string  `json:"enterprise_name"`
		Country            string  `json:"country"`
		ContactName        string  `json:"contact_name"`
		ContactEmail       string  `json:"contact_email"`
		ContactPhone       string  `json:"contact_phone"`
		RegistrationNumber string  `json:"registration_number"`
		RegisteredAddress  string  `json:"registered_address"`
		BankName           string  `json:"bank_name"`
		BankAccount        string  `json:"bank_account"`
		Status             string  `json:"status"`
		RejectionReason    string  `json:"rejection_reason"`
		CreatedAt          string  `json:"created_at"`
		ReviewedAt         *string `json:"reviewed_at"`
	}
	err := r.db.QueryRow(c.Request.Context(), `
		SELECT id::text, enterprise_name, country, contact_name, contact_email, contact_phone, registration_number,
			COALESCE(registered_address,''), COALESCE(bank_name,''), COALESCE(bank_account,''),
			status, COALESCE(rejection_reason,''), created_at::text, reviewed_at::text
		FROM enterprise_applications
		WHERE applicant_user_id = $1::uuid
		ORDER BY created_at DESC LIMIT 1`, userID,
	).Scan(&app.ID, &app.EnterpriseName, &app.Country, &app.ContactName, &app.ContactEmail, &app.ContactPhone,
		&app.RegistrationNumber, &app.RegisteredAddress, &app.BankName, &app.BankAccount,
		&app.Status, &app.RejectionReason, &app.CreatedAt, &app.ReviewedAt)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"application": nil})
		return
	}
	c.JSON(http.StatusOK, gin.H{"application": app})
}

// updateEnterpriseApplication allows user to supplement materials when status is needs_more_info
func (r *EnterpriseApplicationRoutes) updateEnterpriseApplication(c *gin.Context) {
	userID := c.GetString("user_id")
	appID := c.Param("id")

	var currentStatus string
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT status FROM enterprise_applications WHERE id = $1::uuid AND applicant_user_id = $2::uuid`,
		appID, userID).Scan(&currentStatus)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Application not found"}})
		return
	}
	if currentStatus != "needs_more_info" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Application cannot be updated in current status"}})
		return
	}

	var req struct {
		EnterpriseName     string `json:"enterprise_name"`
		Country            string `json:"country"`
		ContactName        string `json:"contact_name"`
		ContactEmail       string `json:"contact_email"`
		ContactPhone       string `json:"contact_phone"`
		RegistrationNumber string `json:"registration_number"`
		RegisteredAddress  string `json:"registered_address"`
		BankName           string `json:"bank_name"`
		BankAccount        string `json:"bank_account"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}

	err = r.db.Exec(c.Request.Context(), `
		UPDATE enterprise_applications SET
			enterprise_name = COALESCE(NULLIF($1,''), enterprise_name),
			country = COALESCE(NULLIF($2,''), country),
			contact_name = COALESCE(NULLIF($3,''), contact_name),
			contact_email = COALESCE(NULLIF($4,''), contact_email),
			contact_phone = COALESCE(NULLIF($5,''), contact_phone),
			registration_number = COALESCE(NULLIF($6,''), registration_number),
			registered_address = COALESCE(NULLIF($7,''), registered_address),
			bank_name = COALESCE(NULLIF($8,''), bank_name),
			bank_account = COALESCE(NULLIF($9,''), bank_account),
			status = 'pending_review', updated_at = NOW()
		WHERE id = $10::uuid`,
		req.EnterpriseName, req.Country, req.ContactName, req.ContactEmail, req.ContactPhone,
		req.RegistrationNumber, req.RegisteredAddress, req.BankName, req.BankAccount, appID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to update"}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// submitForReview allows user to re-submit an application after providing more info.
func (r *EnterpriseApplicationRoutes) submitEnterpriseApplicationForReview(c *gin.Context) {
	userID := c.GetString("user_id")
	appID := c.Param("id")
	ctx := c.Request.Context()

	var currentStatus string
	err := r.db.QueryRow(ctx,
		`SELECT status FROM enterprise_applications WHERE id = $1::uuid AND applicant_user_id = $2::uuid`,
		appID, userID).Scan(&currentStatus)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Application not found"}})
		return
	}
	if currentStatus != "needs_more_info" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Application cannot be re-submitted in current status"}})
		return
	}

	if err := r.db.Exec(ctx,
		`UPDATE enterprise_applications SET status = 'pending_review', updated_at = NOW() WHERE id = $1::uuid`,
		appID); err != nil {
		r.logger.Error("Failed to re-submit application for review", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to submit for review"}})
		return
	}

	r.audit.Log(ctx, userID, c.GetString("email"), "user", "enterprise_application_resubmitted", "info",
		fmt.Sprintf("Application %s re-submitted for review", appID))

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// --- Admin-facing APIs ---

// listEnterpriseApplications returns all applications for admin review
func (r *AdminRoutes) listEnterpriseApplications(c *gin.Context) {
	status := c.DefaultQuery("status", "")

	query := `SELECT a.id::text, a.enterprise_name, a.country, a.contact_name, a.contact_email,
		a.registration_number, a.status, a.created_at::text,
		COALESCE(u.name, u.email) as applicant_name, u.email as applicant_email,
		u.created_at::text as account_created_at,
		(SELECT COUNT(*) FROM enterprise_applications ea WHERE ea.applicant_user_id = u.id AND ea.status = 'rejected') as rejection_count
		FROM enterprise_applications a
		JOIN users u ON a.applicant_user_id = u.id
		WHERE 1=1`
	args := []interface{}{}

	if status != "" {
		args = append(args, status)
		query += fmt.Sprintf(` AND a.status = $%d`, len(args))
	}
	query += ` ORDER BY a.created_at DESC LIMIT 100`

	rows, err := r.db.Query(c.Request.Context(), query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to list applications"}})
		return
	}
	defer rows.Close()

	type appRow struct {
		ID                 string `json:"id"`
		EnterpriseName     string `json:"enterprise_name"`
		Country            string `json:"country"`
		ContactName        string `json:"contact_name"`
		ContactEmail       string `json:"contact_email"`
		RegistrationNumber string `json:"registration_number"`
		Status             string `json:"status"`
		CreatedAt          string `json:"created_at"`
		ApplicantName      string `json:"applicant_name"`
		ApplicantEmail     string `json:"applicant_email"`
		AccountCreatedAt   string `json:"account_created_at"`
		RejectionCount     int    `json:"rejection_count"`
	}
	apps := []appRow{}
	for rows.Next() {
		var a appRow
		if err := rows.Scan(&a.ID, &a.EnterpriseName, &a.Country, &a.ContactName, &a.ContactEmail,
			&a.RegistrationNumber, &a.Status, &a.CreatedAt, &a.ApplicantName, &a.ApplicantEmail,
			&a.AccountCreatedAt, &a.RejectionCount); err == nil {
			apps = append(apps, a)
		}
	}
	c.JSON(http.StatusOK, gin.H{"applications": apps})
}

// getEnterpriseApplicationDetail returns full detail of an application
func (r *AdminRoutes) getEnterpriseApplicationDetail(c *gin.Context) {
	appID := c.Param("id")

	var app struct {
		ID                 string  `json:"id"`
		EnterpriseName     string  `json:"enterprise_name"`
		Country            string  `json:"country"`
		ContactName        string  `json:"contact_name"`
		ContactEmail       string  `json:"contact_email"`
		ContactPhone       string  `json:"contact_phone"`
		RegistrationNumber string  `json:"registration_number"`
		RegisteredAddress  string  `json:"registered_address"`
		BankName           string  `json:"bank_name"`
		BankAccount        string  `json:"bank_account"`
		Documents          string  `json:"documents"`
		Status             string  `json:"status"`
		RejectionReason    string  `json:"rejection_reason"`
		CreatedAt          string  `json:"created_at"`
		ReviewedAt         *string `json:"reviewed_at"`
		ApplicantName      string  `json:"applicant_name"`
		ApplicantEmail     string  `json:"applicant_email"`
		ApplicantID        string  `json:"applicant_id"`
	}
	err := r.db.QueryRow(c.Request.Context(), `
		SELECT a.id::text, a.enterprise_name, a.country, a.contact_name, a.contact_email, a.contact_phone,
			a.registration_number, COALESCE(a.registered_address,''), COALESCE(a.bank_name,''), COALESCE(a.bank_account,''),
			COALESCE(a.documents::text,'[]'), a.status, COALESCE(a.rejection_reason,''),
			a.created_at::text, a.reviewed_at::text,
			COALESCE(u.name, u.email), u.email, u.id::text
		FROM enterprise_applications a
		JOIN users u ON a.applicant_user_id = u.id
		WHERE a.id = $1::uuid`, appID,
	).Scan(&app.ID, &app.EnterpriseName, &app.Country, &app.ContactName, &app.ContactEmail, &app.ContactPhone,
		&app.RegistrationNumber, &app.RegisteredAddress, &app.BankName, &app.BankAccount,
		&app.Documents, &app.Status, &app.RejectionReason,
		&app.CreatedAt, &app.ReviewedAt, &app.ApplicantName, &app.ApplicantEmail, &app.ApplicantID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Application not found"}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"application": app})
}

// approveEnterpriseApplication approves application, creates enterprise, assigns admin.
// The entire operation runs inside a transaction to keep application, enterprise and user consistent.
func (r *AdminRoutes) approveEnterpriseApplication(c *gin.Context) {
	appID := c.Param("id")
	adminUserID := c.GetString("user_id")

	ctx := c.Request.Context()

	// Get application details
	var applicantID, enterpriseName, country, contactEmail, contactName, contactPhone string
	var status string
	var regNumber, regAddress, bankName, bankAccount string
	err := r.db.QueryRow(ctx, `
		SELECT applicant_user_id::text, enterprise_name, country, COALESCE(contact_email,''), COALESCE(contact_name,''), COALESCE(contact_phone,''), status,
			COALESCE(registration_number,''), COALESCE(registered_address,''), COALESCE(bank_name,''), COALESCE(bank_account,'')
		FROM enterprise_applications WHERE id = $1::uuid`, appID,
	).Scan(&applicantID, &enterpriseName, &country, &contactEmail, &contactName, &contactPhone, &status,
		&regNumber, &regAddress, &bankName, &bankAccount)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Application not found"}})
		return
	}
	if status != "pending_review" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Application is not pending review"}})
		return
	}

	var enterpriseID string
	err = r.db.WithTx(ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			INSERT INTO enterprises (name, legal_name, contact_email, contact_name, contact_phone, country, tax_number, registered_address, bank_name, bank_account, status, application_id, verified_at, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'active', $11::uuid, NOW(), NOW(), NOW()) RETURNING id::text`,
			enterpriseName, enterpriseName, contactEmail, contactName, contactPhone, country, regNumber, regAddress, bankName, bankAccount, appID,
		).Scan(&enterpriseID); err != nil {
			return fmt.Errorf("create enterprise: %w", err)
		}

		if _, err := tx.Exec(ctx,
			`INSERT INTO enterprise_members (enterprise_id, user_id, role, joined_at) VALUES ($1::uuid, $2::uuid, 'admin', NOW())
			 ON CONFLICT (enterprise_id, user_id) WHERE removed_at IS NULL DO UPDATE SET role = 'admin', removed_at = NULL, removed_by = NULL`,
			enterpriseID, applicantID); err != nil {
			return fmt.Errorf("insert enterprise_members: %w", err)
		}

		if _, err := tx.Exec(ctx,
			`UPDATE users SET enterprise_id = $1::uuid, role = 'enterprise_admin', updated_at = NOW() WHERE id = $2::uuid`,
			enterpriseID, applicantID); err != nil {
			return fmt.Errorf("assign admin: %w", err)
		}

		if _, err := tx.Exec(ctx,
			`UPDATE enterprise_applications SET status = 'approved', reviewed_by = $1::uuid, reviewed_at = NOW(), updated_at = NOW() WHERE id = $2::uuid`,
			adminUserID, appID); err != nil {
			return fmt.Errorf("update application: %w", err)
		}
		return nil
	})

	if err != nil {
		r.logger.Error("Failed to approve application", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to approve application"}})
		return
	}

	r.audit.Log(ctx, adminUserID, c.GetString("email"), "admin", "enterprise_application_approved", "info",
		fmt.Sprintf("Approved enterprise application %s, created enterprise %s for user %s", appID, enterpriseName, applicantID))

	// Send email notification to applicant
	if r.mail != nil {
		var applicantEmail string
		_ = r.db.QueryRow(ctx, `SELECT email FROM users WHERE id = $1::uuid`, applicantID).Scan(&applicantEmail)
		if applicantEmail != "" {
			_ = r.mail.SendEnterpriseApplicationStatusChange(applicantEmail, enterpriseName, "approved", "", "en")
		}
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "enterprise_id": enterpriseID})
}

// rejectEnterpriseApplication rejects an application
func (r *AdminRoutes) rejectEnterpriseApplication(c *gin.Context) {
	appID := c.Param("id")
	adminUserID := c.GetString("user_id")

	var req struct {
		Reason string `json:"reason"`
	}
	_ = c.ShouldBindJSON(&req)

	var status string
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT status FROM enterprise_applications WHERE id = $1::uuid`, appID).Scan(&status)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Application not found"}})
		return
	}
	if status != "pending_review" && status != "needs_more_info" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Application cannot be rejected in current status"}})
		return
	}

	err = r.db.Exec(c.Request.Context(), `
		UPDATE enterprise_applications SET status = 'rejected', rejection_reason = $1, reviewed_by = $2::uuid, reviewed_at = NOW(), updated_at = NOW()
		WHERE id = $3::uuid`, req.Reason, adminUserID, appID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to reject"}})
		return
	}

	r.audit.Log(c.Request.Context(), adminUserID, c.GetString("email"), "admin", "enterprise_application_rejected", "warning",
		fmt.Sprintf("Rejected enterprise application %s: %s", appID, req.Reason))

	// Send email notification
	if r.mail != nil {
		var applicantEmail, enterpriseName string
		_ = r.db.QueryRow(c.Request.Context(),
			`SELECT u.email, a.enterprise_name FROM enterprise_applications a JOIN users u ON a.applicant_user_id = u.id WHERE a.id = $1::uuid`,
			appID).Scan(&applicantEmail, &enterpriseName)
		if applicantEmail != "" {
			_ = r.mail.SendEnterpriseApplicationStatusChange(applicantEmail, enterpriseName, "rejected", req.Reason, "en")
		}
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// requestMoreInfoEnterpriseApplication asks applicant to provide more info
func (r *AdminRoutes) requestMoreInfoEnterpriseApplication(c *gin.Context) {
	appID := c.Param("id")
	adminUserID := c.GetString("user_id")

	var req struct {
		Reason string `json:"reason"`
	}
	_ = c.ShouldBindJSON(&req)

	var status string
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT status FROM enterprise_applications WHERE id = $1::uuid`, appID).Scan(&status)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Application not found"}})
		return
	}
	if status != "pending_review" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Can only request info for pending applications"}})
		return
	}

	err = r.db.Exec(c.Request.Context(), `
		UPDATE enterprise_applications SET status = 'needs_more_info', rejection_reason = $1, reviewed_by = $2::uuid, updated_at = NOW()
		WHERE id = $3::uuid`, req.Reason, adminUserID, appID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to update"}})
		return
	}

	r.audit.Log(c.Request.Context(), adminUserID, c.GetString("email"), "admin", "enterprise_application_request_info", "info",
		fmt.Sprintf("Requested more info for application %s: %s", appID, req.Reason))

	// Send email notification
	if r.mail != nil {
		var applicantEmail, enterpriseName string
		_ = r.db.QueryRow(c.Request.Context(),
			`SELECT u.email, a.enterprise_name FROM enterprise_applications a JOIN users u ON a.applicant_user_id = u.id WHERE a.id = $1::uuid`,
			appID).Scan(&applicantEmail, &enterpriseName)
		if applicantEmail != "" {
			_ = r.mail.SendEnterpriseApplicationStatusChange(applicantEmail, enterpriseName, "needs_more_info", req.Reason, "en")
		}
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

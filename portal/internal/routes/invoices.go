package routes

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/mayiants/ai-portal-go/internal/config"
	"github.com/mayiants/ai-portal-go/internal/db"
	"github.com/mayiants/ai-portal-go/internal/middleware"
	"github.com/mayiants/ai-portal-go/internal/services"
	"github.com/mayiants/ai-portal-go/internal/storage"
	"github.com/mayiants/ai-portal-go/pkg/audit"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type InvoiceRoutes struct {
	db      *db.DB
	auth    *middleware.Auth
	logger  *zap.Logger
	audit   *audit.Logger
	notif   *services.NotificationService
	storage storage.Provider
	cfg     *config.Config
}

func NewInvoiceRoutes(db *db.DB, auth *middleware.Auth, logger *zap.Logger, auditLog *audit.Logger, notif *services.NotificationService, storageProvider storage.Provider, cfg *config.Config) *InvoiceRoutes {
	return &InvoiceRoutes{db: db, auth: auth, logger: logger, audit: auditLog, notif: notif, storage: storageProvider, cfg: cfg}
}

var emailRegex = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)
var phoneRegex = regexp.MustCompile(`^[\d\+\-\s\(\)]{6,20}$`)

func isValidEmail(email string) bool {
	return emailRegex.MatchString(email)
}

func isValidPhone(phone string) bool {
	return phoneRegex.MatchString(phone)
}

func (r *InvoiceRoutes) Register(rg *gin.RouterGroup) {
	// Profile CRUD
	rg.GET("/profiles", r.auth.RequireAuth(), r.listProfiles)
	rg.POST("/profiles", r.auth.RequireAuth(), r.createProfile)
	rg.PUT("/profiles/:id", r.auth.RequireAuth(), r.updateProfile)
	rg.DELETE("/profiles/:id", r.auth.RequireAuth(), r.deleteProfile)

	// Request CRUD
	rg.GET("/requests", r.auth.RequireAuth(), r.listRequests)
	rg.GET("/eligibility", r.auth.RequireAuth(), r.getEligibility)
	rg.POST("/requests", r.auth.RequireAuth(), r.createRequest)
	rg.GET("/requests/:id/download", r.auth.RequireAuth(), r.downloadInvoice)
}

// --- Invoice Profiles ---

func (r *InvoiceRoutes) listProfiles(c *gin.Context) {
	userID := c.GetString("user_id")

	rows, err := r.db.Query(c.Request.Context(),
		`SELECT id::text, COALESCE(name, ''), COALESCE(type, 'normal'),
		        COALESCE(tax_number, ''), COALESCE(address, ''),
		        COALESCE(phone, ''), COALESCE(bank_name, ''),
		        COALESCE(bank_account, ''), COALESCE(is_default, false),
		        COALESCE(email, ''), COALESCE(country, 'CN'),
		        COALESCE(company_name, ''), COALESCE(registered_address, ''),
		        created_at
		 FROM invoice_profiles
		 WHERE user_id = $1::uuid AND deleted_at IS NULL
		 ORDER BY is_default DESC, created_at DESC`,
		userID)
	if err != nil {
		r.logger.Error("Failed to list invoice profiles", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to list profiles"}})
		return
	}
	defer rows.Close()

	profiles := []gin.H{}
	for rows.Next() {
		var id, name, pType, taxNumber, address, phone, bankName, bankAccount string
		var isDefault bool
		var email, country, companyName, registeredAddress string
		var createdAt interface{}
		if err := rows.Scan(&id, &name, &pType, &taxNumber, &address,
			&phone, &bankName, &bankAccount, &isDefault,
			&email, &country, &companyName, &registeredAddress, &createdAt); err != nil {
			continue
		}
		profiles = append(profiles, gin.H{
			"id": id, "name": name, "type": pType,
			"tax_number": taxNumber, "address": address,
			"phone": phone, "bank_name": bankName,
			"bank_account": bankAccount, "is_default": isDefault,
			"email": email, "country": country,
			"company_name": companyName, "registered_address": registeredAddress,
			"created_at": createdAt,
		})
	}

	c.JSON(http.StatusOK, gin.H{"profiles": profiles})
}

func (r *InvoiceRoutes) createProfile(c *gin.Context) {
	userID := c.GetString("user_id")

	var req struct {
		Name              string `json:"name" binding:"required"`
		Type              string `json:"type"`
		TaxNumber         string `json:"tax_number"`
		Address           string `json:"address"`
		Phone             string `json:"phone"`
		BankName          string `json:"bank_name"`
		BankAccount       string `json:"bank_account"`
		IsDefault         bool   `json:"is_default"`
		Email             string `json:"email"`
		Country           string `json:"country"`
		CompanyName       string `json:"company_name"`
		RegisteredAddress string `json:"registered_address"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": T("error.invoice_title_required", c)}})
		return
	}

	if req.Type == "" {
		req.Type = "personal"
	}
	if req.Country == "" {
		req.Country = "CN"
	}

	// Validate by type
	switch req.Type {
	case "normal", "special":
		if req.TaxNumber == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": T("error.tax_id_required", c)}})
			return
		}
		if len(req.TaxNumber) < 15 || len(req.TaxNumber) > 20 {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": T("error.invalid_tax_id", c)}})
			return
		}
		if req.Email != "" && !isValidEmail(req.Email) {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": T("error.invalid_email_format", c)}})
			return
		}
		if req.Phone != "" && !isValidPhone(req.Phone) {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": T("error.invalid_phone", c)}})
			return
		}
		if req.Type == "special" {
			if req.BankName == "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": T("error.bank_required", c)}})
				return
			}
			if req.BankAccount == "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": T("error.bank_account_required", c)}})
				return
			}
		}
	case "proforma":
		if req.CompanyName == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": T("error.company_name_required", c)}})
			return
		}
		if req.Email == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": T("error.email_required", c)}})
			return
		}
		if !isValidEmail(req.Email) {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": T("error.invalid_email_format", c)}})
			return
		}
	}

	if req.IsDefault {
		if err := r.db.Exec(c.Request.Context(), `UPDATE invoice_profiles SET is_default = false WHERE user_id = $1::uuid`, userID); err != nil {
			r.logger.Error("database operation failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
			return
		}
	}

	var id string
	err := r.db.QueryRow(c.Request.Context(),
		`INSERT INTO invoice_profiles (user_id, name, type, tax_number, address, phone, bank_name, bank_account, is_default, email, country, company_name, registered_address)
		 VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		 RETURNING id::text`,
		userID, req.Name, req.Type, req.TaxNumber, req.Address,
		req.Phone, req.BankName, req.BankAccount, req.IsDefault,
		req.Email, req.Country, req.CompanyName, req.RegisteredAddress,
	).Scan(&id)
	if err != nil {
		r.logger.Error("Failed to create invoice profile", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": T("error.create_failed", c)}})
		return
	}

	if r.audit != nil {
		r.audit.Log(c.Request.Context(), userID, c.GetString("email"), "billing", "invoice_profile_created", "info",
			"Invoice profile created: "+req.Name)
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "id": id})
}

func (r *InvoiceRoutes) updateProfile(c *gin.Context) {
	userID := c.GetString("user_id")
	profileID := c.Param("id")

	var req struct {
		Name              *string `json:"name"`
		Type              *string `json:"type"`
		TaxNumber         *string `json:"tax_number"`
		Address           *string `json:"address"`
		Phone             *string `json:"phone"`
		BankName          *string `json:"bank_name"`
		BankAccount       *string `json:"bank_account"`
		IsDefault         *bool   `json:"is_default"`
		Email             *string `json:"email"`
		Country           *string `json:"country"`
		CompanyName       *string `json:"company_name"`
		RegisteredAddress *string `json:"registered_address"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": T("error.invalid_request", c)}})
		return
	}

	// Build UPDATE with column whitelist to prevent SQL injection
	var setClauses []string
	var args []interface{}
	argIdx := 1

	// Column whitelist — only these columns are allowed to be updated
	columnMap := map[string]string{
		"name":               "name",
		"type":               "type",
		"tax_number":         "tax_number",
		"address":            "address",
		"phone":              "phone",
		"bank_name":          "bank_name",
		"bank_account":       "bank_account",
		"is_default":         "is_default",
		"email":              "email",
		"country":            "country",
		"company_name":       "company_name",
		"registered_address": "registered_address",
	}

	addField := func(col string, val interface{}) {
		// Validate column against whitelist
		dbCol, ok := columnMap[col]
		if !ok {
			return // Skip invalid columns
		}
		setClauses = append(setClauses, fmt.Sprintf("%s = $%d", dbCol, argIdx))
		args = append(args, val)
		argIdx++
	}

	if req.IsDefault != nil {
		addField("is_default", *req.IsDefault)
		if *req.IsDefault {
			// Reset all other profiles' is_default first
			if err := r.db.Exec(c.Request.Context(),
				`UPDATE invoice_profiles SET is_default = false WHERE user_id = $1::uuid`,
				userID); err != nil {
				r.logger.Error("Failed to reset default invoice profile", zap.Error(err))
			}
		}
	}
	if req.Name != nil {
		addField("name", *req.Name)
	}
	if req.Type != nil {
		addField("type", *req.Type)
	}
	if req.TaxNumber != nil {
		addField("tax_number", *req.TaxNumber)
	}
	if req.Address != nil {
		addField("address", *req.Address)
	}
	if req.Phone != nil {
		addField("phone", *req.Phone)
	}
	if req.BankName != nil {
		addField("bank_name", *req.BankName)
	}
	if req.BankAccount != nil {
		addField("bank_account", *req.BankAccount)
	}
	if req.Email != nil {
		addField("email", *req.Email)
	}
	if req.Country != nil {
		addField("country", *req.Country)
	}
	if req.CompanyName != nil {
		addField("company_name", *req.CompanyName)
	}
	if req.RegisteredAddress != nil {
		addField("registered_address", *req.RegisteredAddress)
	}

	if len(setClauses) > 0 {
		args = append(args, profileID, userID)
		err := r.db.Exec(c.Request.Context(),
			fmt.Sprintf(`UPDATE invoice_profiles SET %s, updated_at = NOW() WHERE id = $%d::uuid AND user_id = $%d::uuid AND deleted_at IS NULL`,
				strings.Join(setClauses, ", "), argIdx, argIdx+1),
			args...)
		if err != nil {
			r.logger.Error("Failed to update invoice profile", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": T("error.update_failed", c)}})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (r *InvoiceRoutes) deleteProfile(c *gin.Context) {
	userID := c.GetString("user_id")
	profileID := c.Param("id")

	// Check if profile is used by any invoice requests
	var count int
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM invoice_requests WHERE profile_id = $1::uuid`,
		profileID).Scan(&count)
	if count > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": gin.H{"message": T("error.invoice_title_in_use", c)}})
		return
	}

	err := r.db.Exec(c.Request.Context(),
		`UPDATE invoice_profiles SET deleted_at = NOW() WHERE id = $1::uuid AND user_id = $2::uuid AND deleted_at IS NULL`,
		profileID, userID)
	if err != nil {
		r.logger.Error("Failed to delete invoice profile", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": T("error.delete_failed", c)}})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// --- Invoice Requests ---

// listRequests returns the caller's invoice requests. Personal pages
// (billing-details, invoices) call it without a scope and only see requests
// with NO enterprise (enterprise and personal invoicing stay strictly
// separated); the enterprise billing page passes scope=enterprise and sees
// this enterprise's requests regardless of which admin submitted them.
// downloadInvoice streams the issued PDF for an invoice request the caller
// may access: the requester themselves, an enterprise admin of the request's
// enterprise, or a system/super admin. Authorization is by invoice_requests
// ownership; the file bytes come from the storage provider via the invoices
// row written at issuance.
func (r *InvoiceRoutes) downloadInvoice(c *gin.Context) {
	userID := c.GetString("user_id")
	requestID := c.Param("id")

	var storageKey, reqUserID, reqEnterpriseID, status string
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT COALESCE(i.storage_key, ''), ir.user_id::text, COALESCE(ir.enterprise_id::text, ''), ir.status
		 FROM invoice_requests ir
		 LEFT JOIN invoices i ON i.request_id = ir.id
		 WHERE ir.id = $1::uuid`,
		requestID).Scan(&storageKey, &reqUserID, &reqEnterpriseID, &status)
	if err != nil || status != "completed" {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Invoice not found or not issued"}})
		return
	}

	// Authorize: requester, admin of the owning enterprise, or system admin.
	if reqUserID != userID {
		var role, callerEnterpriseID string
		_ = r.db.QueryRow(c.Request.Context(),
			`SELECT COALESCE(role, ''), COALESCE(enterprise_id::text, '') FROM users WHERE id = $1::uuid`,
			userID).Scan(&role, &callerEnterpriseID)
		allowed := role == "super_admin" || role == "system_admin" ||
			(role == "enterprise_admin" && reqEnterpriseID != "" && callerEnterpriseID == reqEnterpriseID)
		if !allowed {
			c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "Not authorized"}})
			return
		}
	}

	if storageKey == "" || r.storage == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "No PDF available for this invoice"}})
		return
	}
	rc, err := r.storage.Download(c.Request.Context(), storageKey)
	if err != nil {
		r.logger.Warn("invoice pdf download failed", zap.String("request_id", requestID), zap.Error(err))
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Invoice file not found"}})
		return
	}
	defer func() { _ = rc.Close() }()

	filename := fmt.Sprintf("invoice-%s.pdf", requestID[:8])
	c.Header("Content-Type", "application/pdf")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
	_, _ = io.Copy(c.Writer, rc)
}

func (r *InvoiceRoutes) listRequests(c *gin.Context) {
	userID := c.GetString("user_id")

	var enterpriseID string
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT COALESCE(enterprise_id::text, '') FROM users WHERE id = $1::uuid AND deleted_at IS NULL`,
		userID).Scan(&enterpriseID)

	query := `SELECT ir.id::text, COALESCE(ir.title, ''), ir.amount,
	        COALESCE(ir.status, 'pending'),
	        COALESCE(ir.delivery_method, 'email'),
	        COALESCE(ir.delivery_email, ''),
	        COALESCE(ip.name, '') as profile_name,
	        COALESCE(ir.invoice_number, ''),
	        COALESCE(ir.rejection_reason, ''),
	        COALESCE(ir.enterprise_id::text, ''),
	        ir.created_at,
	        COALESCE(ir.invoice_type, 'general'),
	        COALESCE((SELECT storage_key FROM invoices i WHERE i.request_id = ir.id), '') AS storage_key
	 FROM invoice_requests ir
	 LEFT JOIN invoice_profiles ip ON ir.profile_id = ip.id
	 WHERE ir.user_id = $1::uuid AND ir.enterprise_id IS NULL
	 ORDER BY ir.created_at DESC LIMIT 50`
	arg := interface{}(userID)
	if c.Query("scope") == "enterprise" && enterpriseID != "" {
		query = `SELECT ir.id::text, COALESCE(ir.title, ''), ir.amount,
	        COALESCE(ir.status, 'pending'),
	        COALESCE(ir.delivery_method, 'email'),
	        COALESCE(ir.delivery_email, ''),
	        COALESCE(ip.name, '') as profile_name,
	        COALESCE(ir.invoice_number, ''),
	        COALESCE(ir.rejection_reason, ''),
	        COALESCE(ir.enterprise_id::text, ''),
	        ir.created_at,
	        COALESCE(ir.invoice_type, 'general'),
	        COALESCE((SELECT storage_key FROM invoices i WHERE i.request_id = ir.id), '') AS storage_key
	 FROM invoice_requests ir
	 LEFT JOIN invoice_profiles ip ON ir.profile_id = ip.id
	 WHERE ir.enterprise_id = $1::uuid
	 ORDER BY ir.created_at DESC LIMIT 50`
		arg = enterpriseID
	}

	rows, err := r.db.Query(c.Request.Context(), query, arg)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"requests": []gin.H{}})
		return
	}
	defer rows.Close()

	requests := []gin.H{}
	for rows.Next() {
		var id, title, status, deliveryMethod, deliveryEmail, profileName string
		var invoiceNumber, rejectionReason, enterpriseIDOut, storageKey, invoiceType string
		var amount int64
		var createdAt interface{}
		if err := rows.Scan(&id, &title, &amount, &status, &deliveryMethod,
			&deliveryEmail, &profileName, &invoiceNumber, &rejectionReason, &enterpriseIDOut, &createdAt, &invoiceType, &storageKey); err != nil {
			continue
		}
		requests = append(requests, gin.H{
			"id": id, "title": title, "amount": amount, "amount_cents": amount,
			"status": status, "delivery_method": deliveryMethod,
			"delivery_email": deliveryEmail, "profile_name": profileName,
			"invoice_number": invoiceNumber, "rejection_reason": rejectionReason,
			"enterprise_id": enterpriseIDOut,
			"created_at":    createdAt,
			"invoice_type":  invoiceType,
			"has_pdf":       storageKey != "",
		})
	}

	c.JSON(http.StatusOK, gin.H{"requests": requests})
}

func (r *InvoiceRoutes) createRequest(c *gin.Context) {
	userID := c.GetString("user_id")

	// All amounts are integer USD cents (amount_cents). No currency conversion is
	// performed server-side: stored amounts, eligible amounts and requested
	// amounts are all compared in cents.
	var req struct {
		AmountCents    int64  `json:"amount_cents"`
		ProfileID      string `json:"profile_id"` // required for personal users only
		Title          string `json:"title"`
		DeliveryMethod string `json:"delivery_method"`
		DeliveryEmail  string `json:"delivery_email"`
		AddressID      string `json:"address_id"`
		Pool           string `json:"pool"`         // enterprise only: postpaid | prepaid
		Period         string `json:"period"`       // enterprise postpaid only: statement month YYYY-MM
		InvoiceType    string `json:"invoice_type"` // enterprise: normal | special | proforma
		Scope          string `json:"scope"`        // personal pages force the personal path even for EAs
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": T("error.select_title_and_amount", c)}})
		return
	}

	if req.AmountCents <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": T("error.select_title_and_valid_amount", c)}})
		return
	}

	// --- Invoice eligibility validation ---
	const minInvoiceCents int64 = 1000 // $10 USD
	if req.AmountCents < minInvoiceCents {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Minimum invoice amount is $10 USD"}})
		return
	}

	// The enterprise admin invoices against the enterprise credit pool, using the
	// verified enterprise registration info — never a user-editable profile.
	// Regular enterprise members fall through to the personal path below: their
	// personal real-fund consumption (own balance) is invoiced separately and
	// never consumes the enterprise invoice quota.
	var enterpriseID, userRole string
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT COALESCE(enterprise_id::text, ''), COALESCE(role, '') FROM users WHERE id = $1::uuid AND deleted_at IS NULL`,
		userID,
	).Scan(&enterpriseID, &userRole)

	if enterpriseID != "" && userRole == "enterprise_admin" && req.Scope != "personal" {
		r.createEnterpriseRequest(c, userID, enterpriseID, &req)
		return
	}

	if req.ProfileID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": T("error.select_title_and_amount", c)}})
		return
	}

	// The invoice type comes from the selected header (invoice profile),
	// which the user picks among personal / normal / special / proforma
	// when creating it — not from a separate request-time toggle.
	var profileType, profileTax string
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT COALESCE(type, 'personal'), COALESCE(tax_number, '') FROM invoice_profiles WHERE id = $1::uuid AND user_id = $2::uuid`,
		req.ProfileID, userID).Scan(&profileType, &profileTax)
	if profileType == "" {
		profileType = "personal"
	}
	invoiceType := profileType
	if invoiceType == "special" && profileTax == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Special (VAT) invoices require a tax number on the invoice header"}})
		return
	}

	if req.DeliveryMethod == "" {
		req.DeliveryMethod = "email"
	}

	if req.DeliveryMethod == "email" {
		if req.DeliveryEmail == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": T("error.enter_receive_email", c)}})
			return
		}
		if !isValidEmail(req.DeliveryEmail) {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": T("error.invalid_email_format", c)}})
			return
		}
	}

	if req.DeliveryMethod == "paper" && req.AddressID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": T("error.select_shipping_address", c)}})
		return
	}

	eligibleCents, _, _ := r.personalInvoiceEligibility(c.Request.Context(), userID)
	if req.AmountCents > eligibleCents {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": fmt.Sprintf("Invoice amount exceeds eligible consumption. Eligible: $%.2f, Requested: $%.2f",
			float64(eligibleCents)/100.0, float64(req.AmountCents)/100.0)}})
		return
	}

	var addressID interface{}
	if req.AddressID != "" {
		addressID = req.AddressID
	}

	var id string
	err := r.db.QueryRow(c.Request.Context(),
		`INSERT INTO invoice_requests (user_id, profile_id, amount, title, delivery_method, delivery_email, address_id, invoice_type)
		 VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8)
		 RETURNING id::text`,
		userID, req.ProfileID, req.AmountCents, req.Title,
		req.DeliveryMethod, req.DeliveryEmail, addressID, invoiceType,
	).Scan(&id)
	if err != nil {
		r.logger.Error("Failed to create invoice request", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": T("error.submit_failed", c)}})
		return
	}

	if r.audit != nil {
		r.audit.Log(c.Request.Context(), userID, c.GetString("email"), "billing", "invoice_requested", "info",
			fmt.Sprintf("Invoice request created: $%.2f", float64(req.AmountCents)/100.0))
	}

	if r.notif != nil {
		params := map[string]string{
			"email":  c.GetString("email"),
			"amount": fmt.Sprintf("$%.2f", float64(req.AmountCents)/100.0),
			"title":  req.Title,
		}
		r.notif.NotifySystemAdmins(c.Request.Context(), "invoice_request", "high",
			"notif.invoice_request.title", "notif.invoice_request.body", params,
			"/admin/reconciliation", "invoice_request", id)
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "id": id})
}

// enterpriseEligibility is the two-pool invoice eligibility of an enterprise.
// Postpaid pool: monthly statements not yet invoiced (reset snapshots).
// Prepaid pool: consumed portion of cumulative prepaid top-ups not yet
// invoiced (consumed = top-ups − current wallet balance; refunds net out).
// All money fields are USD cents.
type enterpriseEligibility struct {
	EligibleCents          int64 `json:"eligible_cents"`
	PostpaidEligibleCents  int64 `json:"postpaid_eligible_cents"`
	PrepaidEligibleCents   int64 `json:"prepaid_eligible_cents"`
	PostpaidStatementCents int64 `json:"postpaid_statement_cents"`
	PrepaidTopupCents      int64 `json:"prepaid_topup_cents"`
	PrepaidConsumedCents   int64 `json:"prepaid_consumed_cents"`
	AlreadyRequestedCents  int64 `json:"already_cents"`
}

// enterpriseInvoiceEligibility computes the enterprise's invoice eligibility
// per the two-pool model (docs/enterprise-billing-prepost-paid-design-202611010445.md).
// The old topup-only formula returned 0 for credit-granted (postpaid)
// enterprises because their credit_total was never a credit_topup.
func (r *InvoiceRoutes) enterpriseInvoiceEligibility(ctx context.Context, enterpriseID string) enterpriseEligibility {
	var el enterpriseEligibility

	// Postpaid pool: statement consumption minus ALL non-rejected postpaid
	// requests, globally (not per-period). Requests without a period still
	// consume eligibility — a period-scoped JOIN would let them slip through,
	// which allowed duplicate full-amount requests.
	_ = r.db.QueryRow(ctx,
		`SELECT GREATEST(
		     COALESCE((SELECT SUM(credit_used_micro) FROM enterprise_statements
		               WHERE enterprise_id = $1::uuid), 0) / 10000
		     - COALESCE((SELECT SUM(amount) FROM invoice_requests
		                 WHERE enterprise_id = $1::uuid AND pool = 'postpaid'
		                   AND status IN ('pending', 'processing', 'completed')), 0),
		     0)`,
		enterpriseID).Scan(&el.PostpaidStatementCents)
	el.PostpaidEligibleCents = el.PostpaidStatementCents
	if el.PostpaidEligibleCents < 0 {
		el.PostpaidEligibleCents = 0
	}

	// Prepaid pool: consumed share of cumulative top-ups, minus invoiced.
	var topupMicro, prepaidBalance int64
	_ = r.db.QueryRow(ctx,
		`SELECT COALESCE(SUM(delta), 0) FROM enterprise_audit_log
		 WHERE enterprise_id = $1::uuid AND change_type = 'prepaid_topup' AND field_name = 'prepaid_balance'`,
		enterpriseID).Scan(&topupMicro)
	_ = r.db.QueryRow(ctx,
		`SELECT COALESCE(prepaid_balance, 0) FROM enterprises WHERE id = $1::uuid`,
		enterpriseID).Scan(&prepaidBalance)
	el.PrepaidTopupCents = topupMicro / 10000
	el.PrepaidConsumedCents = (topupMicro - prepaidBalance) / 10000
	if el.PrepaidConsumedCents < 0 {
		el.PrepaidConsumedCents = 0
	}
	var prepaidInvoiced int64
	_ = r.db.QueryRow(ctx,
		`SELECT COALESCE(SUM(amount), 0) FROM invoice_requests
		 WHERE enterprise_id = $1::uuid AND pool = 'prepaid' AND status IN ('pending', 'processing', 'completed')`,
		enterpriseID).Scan(&prepaidInvoiced)
	el.PrepaidEligibleCents = el.PrepaidConsumedCents - prepaidInvoiced
	if el.PrepaidEligibleCents < 0 {
		el.PrepaidEligibleCents = 0
	}

	el.EligibleCents = el.PostpaidEligibleCents + el.PrepaidEligibleCents
	_ = r.db.QueryRow(ctx,
		`SELECT COALESCE(SUM(amount), 0) FROM invoice_requests
		 WHERE enterprise_id = $1::uuid AND status IN ('pending', 'processing', 'completed')`,
		enterpriseID).Scan(&el.AlreadyRequestedCents)
	return el
}

// statementPeriodEligible returns the still-invocable amount (USD cents) of
// one postpaid statement period: its consumption minus invoice requests in
// any non-rejected state for that period.
func (r *InvoiceRoutes) statementPeriodEligible(ctx context.Context, enterpriseID, period string) int64 {
	var consumedMicro, invoiced int64
	_ = r.db.QueryRow(ctx,
		`SELECT COALESCE(credit_used_micro, 0) FROM enterprise_statements
		 WHERE enterprise_id = $1::uuid AND period = $2`,
		enterpriseID, period).Scan(&consumedMicro)
	_ = r.db.QueryRow(ctx,
		`SELECT COALESCE(SUM(amount), 0) FROM invoice_requests
		 WHERE enterprise_id = $1::uuid AND pool = 'postpaid' AND period = $2
		   AND status IN ('pending', 'processing', 'completed')`,
		enterpriseID, period).Scan(&invoiced)
	el := consumedMicro/10000 - invoiced
	if el < 0 {
		return 0
	}
	return el
}

// personalInvoiceEligibility returns the personal invoice eligibility for a user
// in USD cents: cumulative real-fund consumption (micro-USD) minus amounts
// already requested personally (enterprise requests excluded).
func (r *InvoiceRoutes) personalInvoiceEligibility(ctx context.Context, userID string) (eligibleCents, totalConsumptionCents, alreadyRequestedCents int64) {
	var totalConsumptionQuota int64
	_ = r.db.QueryRow(ctx,
		`SELECT COALESCE(SUM(ABS(amount)), 0)
		 FROM balance_transactions
		 WHERE user_id = $1::uuid
		   AND transaction_type = 'usage'
		   AND balance_type = 'balance'`,
		userID,
	).Scan(&totalConsumptionQuota)
	totalConsumptionCents = totalConsumptionQuota / 10000 // micro-USD → USD cents

	_ = r.db.QueryRow(ctx,
		`SELECT COALESCE(SUM(amount), 0)
		 FROM invoice_requests
		 WHERE user_id = $1::uuid
		   AND status IN ('pending', 'processing', 'completed')
		   AND enterprise_id IS NULL`,
		userID,
	).Scan(&alreadyRequestedCents)

	eligibleCents = totalConsumptionCents - alreadyRequestedCents
	return eligibleCents, totalConsumptionCents, alreadyRequestedCents
}

// getEligibility returns the current user's invoice eligibility in USD cents.
// Scope: billing-details is the PERSONAL billing page and always passes
// scope=personal (an enterprise admin's personal consumption is invoiced
// separately from the enterprise pools); enterprise/billing renders its
// enterprise figures server-side and does not call this endpoint. Without an
// explicit scope the legacy role-based behaviour is kept for compatibility.
func (r *InvoiceRoutes) getEligibility(c *gin.Context) {
	userID := c.GetString("user_id")
	scope := c.Query("scope")

	if scope != "personal" {
		var enterpriseID, userRole string
		_ = r.db.QueryRow(c.Request.Context(),
			`SELECT COALESCE(enterprise_id::text, ''), COALESCE(role, '') FROM users WHERE id = $1::uuid AND deleted_at IS NULL`,
			userID,
		).Scan(&enterpriseID, &userRole)

		if enterpriseID != "" && userRole == "enterprise_admin" {
			el := r.enterpriseInvoiceEligibility(c.Request.Context(), enterpriseID)
			c.JSON(http.StatusOK, gin.H{
				"type":                     "enterprise",
				"eligible_cents":           el.EligibleCents,
				"total_cents":              el.PostpaidStatementCents + el.PrepaidConsumedCents,
				"already_cents":            el.AlreadyRequestedCents,
				"postpaid_eligible_cents":  el.PostpaidEligibleCents,
				"prepaid_eligible_cents":   el.PrepaidEligibleCents,
				"postpaid_statement_cents": el.PostpaidStatementCents,
				"prepaid_topup_cents":      el.PrepaidTopupCents,
				"prepaid_consumed_cents":   el.PrepaidConsumedCents,
			})
			return
		}
	}

	eligible, total, already := r.personalInvoiceEligibility(c.Request.Context(), userID)
	c.JSON(http.StatusOK, gin.H{
		"type":           "personal",
		"eligible_cents": eligible,
		"total_cents":    total,
		"already_cents":  already,
	})
}

// createEnterpriseRequest handles invoice requests from enterprise members.
// The invoice title comes from the verified enterprise registration
// (enterprises / enterprise_applications), not from a user-editable profile.
func (r *InvoiceRoutes) createEnterpriseRequest(c *gin.Context, userID, enterpriseID string, req *struct {
	AmountCents    int64  `json:"amount_cents"`
	ProfileID      string `json:"profile_id"`
	Title          string `json:"title"`
	DeliveryMethod string `json:"delivery_method"`
	DeliveryEmail  string `json:"delivery_email"`
	AddressID      string `json:"address_id"`
	Pool           string `json:"pool"`
	Period         string `json:"period"`
	InvoiceType    string `json:"invoice_type"`
	Scope          string `json:"scope"`
}) {
	if req.DeliveryEmail != "" && !isValidEmail(req.DeliveryEmail) {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": T("error.invalid_email_format", c)}})
		return
	}

	// Two-pool validation (design doc §开票模型): postpaid requests are
	// checked against the un-invoiced monthly statements (optionally one
	// specific period), prepaid requests against consumed prepaid top-ups.
	pool := req.Pool
	if pool == "" {
		pool = "postpaid"
	}
	if pool != "postpaid" && pool != "prepaid" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "pool must be 'postpaid' or 'prepaid'"}})
		return
	}
	el := r.enterpriseInvoiceEligibility(c.Request.Context(), enterpriseID)
	var poolEligible int64
	if pool == "prepaid" {
		poolEligible = el.PrepaidEligibleCents
	} else {
		// Always validate against the GLOBAL postpaid net: per-period checks
		// would let period-less requests bypass the running deduction.
		poolEligible = el.PostpaidEligibleCents
	}
	if req.AmountCents > poolEligible {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": fmt.Sprintf("Invoice amount exceeds eligible %s credit. Eligible: $%.2f, Requested: $%.2f",
			pool, float64(poolEligible)/100.0, float64(req.AmountCents)/100.0)}})
		return
	}

	// Verified enterprise registration info as invoice title source
	var entName, regNumber string
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT e.name, COALESCE(ea.registration_number, '')
		 FROM enterprises e
		 LEFT JOIN enterprise_applications ea ON ea.id = e.application_id
		 WHERE e.id = $1::uuid AND e.deleted_at IS NULL`,
		enterpriseID,
	).Scan(&entName, &regNumber)

	title := req.Title
	if title == "" {
		title = entName
	}

	var taxNumber interface{}
	if regNumber != "" {
		taxNumber = regNumber
	}

	var period interface{}
	if pool == "postpaid" && req.Period != "" {
		period = req.Period
	}
	invoiceType := req.InvoiceType
	if invoiceType == "" {
		invoiceType = "normal"
	}
	switch invoiceType {
	case "normal", "special", "proforma":
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "invoice_type must be 'normal', 'special' or 'proforma'"}})
		return
	}
	if invoiceType == "special" && taxNumber == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Special (VAT) invoices require a tax number on the header"}})
		return
	}

	var id string
	err := r.db.QueryRow(c.Request.Context(),
		`INSERT INTO invoice_requests (user_id, enterprise_id, amount, title, tax_number, delivery_method, delivery_email, pool, period, invoice_type)
		 VALUES ($1::uuid, $2::uuid, $3, $4, $5, 'email', $6, $7, $8, $9)
		 RETURNING id::text`,
		userID, enterpriseID, req.AmountCents, title, taxNumber, req.DeliveryEmail, pool, period, invoiceType,
	).Scan(&id)
	if err != nil {
		r.logger.Error("Failed to create enterprise invoice request", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": T("error.submit_failed", c)}})
		return
	}

	if r.audit != nil {
		r.audit.Log(c.Request.Context(), userID, c.GetString("email"), "billing", "enterprise_invoice_requested", "info",
			fmt.Sprintf("Enterprise invoice request created: $%.2f for %s", float64(req.AmountCents)/100.0, entName))
	}

	if r.notif != nil {
		params := map[string]string{
			"email":      c.GetString("email"),
			"amount":     fmt.Sprintf("$%.2f", float64(req.AmountCents)/100.0),
			"enterprise": entName,
			"title":      title,
		}
		r.notif.NotifyEnterpriseAdmins(c.Request.Context(), enterpriseID, "enterprise_invoice_request", "high",
			"notif.enterprise_invoice_request.title", "notif.enterprise_invoice_request.body", params,
			"/enterprise/billing", "invoice_request", id)
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "id": id})
}

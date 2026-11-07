package routes

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ai-proxy/ai-portal-go/internal/db"
	"github.com/ai-proxy/ai-portal-go/internal/query"
	"github.com/ai-proxy/ai-portal-go/internal/services"
	"github.com/ai-proxy/ai-portal-go/internal/storage"
	"github.com/ai-proxy/ai-portal-go/pkg/invoicepdf"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

// listInvoiceRequests returns paginated invoice requests for admin review
func (r *AdminRoutes) listInvoiceRequests(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	offset := (page - 1) * limit

	status := c.Query("status")
	userID := c.Query("user_id")
	search := c.Query("search")
	dateFrom := c.Query("from") // YYYY-MM-DD, inclusive
	dateTo := c.Query("to")     // YYYY-MM-DD, inclusive

	where := "ir.deleted_at IS NULL"
	args := []interface{}{}
	argIdx := 1

	if status != "" {
		where += fmt.Sprintf(" AND ir.status = $%d", argIdx)
		args = append(args, status)
		argIdx++
	}
	if userID != "" {
		where += fmt.Sprintf(" AND ir.user_id = $%d::uuid", argIdx)
		args = append(args, userID)
		argIdx++
	}
	if search != "" {
		where += fmt.Sprintf(` AND (u.email ILIKE $%[1]d OR u.name ILIKE $%[1]d OR e.name ILIKE $%[1]d
			OR ir.invoice_number ILIKE $%[1]d OR ir.title ILIKE $%[1]d)`, argIdx)
		args = append(args, "%"+search+"%")
		argIdx++
	}
	if dateFrom != "" {
		where += fmt.Sprintf(" AND ir.created_at >= $%d::date", argIdx)
		args = append(args, dateFrom)
		argIdx++
	}
	if dateTo != "" {
		where += fmt.Sprintf(" AND ir.created_at < $%d::date + INTERVAL '1 day'", argIdx)
		args = append(args, dateTo)
		argIdx++
	}

	var total int
	countQuery := fmt.Sprintf(`SELECT COUNT(*) FROM invoice_requests ir
		JOIN users u ON u.id = ir.user_id
		LEFT JOIN enterprises e ON e.id = ir.enterprise_id
		WHERE %s`, where)
	_ = r.db.QueryRow(c.Request.Context(), countQuery, args...).Scan(&total)

	queryArgs := append(args, limit, offset)
	rows, err := r.db.Query(c.Request.Context(), fmt.Sprintf(`
		SELECT ir.id::text, ir.user_id::text, u.email, u.name,
		       COALESCE(ir.title, ''), ir.amount, ir.status, ir.delivery_method,
		       COALESCE(ir.delivery_email, ''), ir.created_at,
		       COALESCE(ip.name, '') as profile_name, COALESCE(ip.tax_number, ''),
		       COALESCE(ir.enterprise_id::text, ''), COALESCE(e.name, ''),
		       COALESCE(ir.invoice_number, ''), COALESCE(ir.rejection_reason, ''),
		       EXISTS (SELECT 1 FROM invoices i WHERE i.request_id = ir.id AND i.storage_key IS NOT NULL AND i.storage_key <> '')
		FROM invoice_requests ir
		JOIN users u ON u.id = ir.user_id
		LEFT JOIN invoice_profiles ip ON ip.id = ir.profile_id
		LEFT JOIN enterprises e ON e.id = ir.enterprise_id
		WHERE %s
		ORDER BY ir.created_at DESC
		LIMIT $%d OFFSET $%d`, where, argIdx, argIdx+1), queryArgs...)
	if err != nil {
		r.logger.Error("Failed to list invoice requests", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to list invoice requests"}})
		return
	}
	defer rows.Close()

	requests := []gin.H{}
	for rows.Next() {
		var id, userID, email, name, title, status, deliveryMethod, deliveryEmail, profileName, taxNumber string
		var enterpriseID, enterpriseName, invoiceNumber, rejectionReason string
		var amount int
		var createdAt time.Time
		var hasPDF bool
		if err := rows.Scan(&id, &userID, &email, &name, &title, &amount, &status, &deliveryMethod, &deliveryEmail, &createdAt, &profileName, &taxNumber, &enterpriseID, &enterpriseName, &invoiceNumber, &rejectionReason, &hasPDF); err == nil {
			requests = append(requests, gin.H{
				"id":               id,
				"user_id":          userID,
				"user_email":       email,
				"user_name":        name,
				"title":            title,
				"amount_cents":     amount,
				"amount_usd":       fmt.Sprintf("%.2f", float64(amount)/100.0),
				"status":           status,
				"delivery_method":  deliveryMethod,
				"delivery_email":   deliveryEmail,
				"profile_name":     profileName,
				"tax_number":       taxNumber,
				"enterprise_id":    enterpriseID,
				"enterprise_name":  enterpriseName,
				"invoice_number":   invoiceNumber,
				"rejection_reason": rejectionReason,
				"created_at":       createdAt,
				"has_pdf":          hasPDF,
			})
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"requests":    requests,
		"total":       total,
		"page":        page,
		"limit":       limit,
		"total_pages": (total + limit - 1) / limit,
	})
}

// approveInvoiceRequest approves an invoice request → status 'processing'
func (r *AdminRoutes) approveInvoiceRequest(c *gin.Context) {
	requestID := c.Param("id")
	result, err := r.db.Pool.Exec(c.Request.Context(),
		`UPDATE invoice_requests SET status = 'processing', updated_at = NOW()
		 WHERE id = $1::uuid AND status = 'pending'`,
		requestID)
	if err != nil {
		r.logger.Error("Failed to approve invoice request", zap.String("id", requestID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to approve request"}})
		return
	}
	if result.RowsAffected() == 0 {
		c.JSON(http.StatusConflict, gin.H{"error": gin.H{"message": "Invoice request is not in 'pending' status"}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// rejectInvoiceRequest rejects an invoice request → status 'rejected'
func (r *AdminRoutes) rejectInvoiceRequest(c *gin.Context) {
	requestID := c.Param("id")
	var req struct {
		Reason string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		r.logger.Warn("Failed to bind reject invoice request", zap.Error(err))
	}

	result, err := r.db.Pool.Exec(c.Request.Context(),
		`UPDATE invoice_requests SET status = 'rejected', rejection_reason = $2, updated_at = NOW()
		 WHERE id = $1::uuid AND status IN ('pending', 'processing')`,
		requestID, req.Reason)
	if err != nil {
		r.logger.Error("Failed to reject invoice request", zap.String("id", requestID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to reject request"}})
		return
	}
	if result.RowsAffected() == 0 {
		c.JSON(http.StatusConflict, gin.H{"error": gin.H{"message": "Invoice request is not in a rejectable status"}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// completeInvoiceRequest marks invoice as issued → status 'completed'
func (r *AdminRoutes) completeInvoiceRequest(c *gin.Context) {
	requestID := c.Param("id")

	// Accept both JSON (legacy callers) and multipart bodies — the latter
	// carries an optional admin-uploaded invoice file (e.g. a China VAT
	// fapiao produced by finance), which replaces the auto-generated PDF.
	var invoiceNumber string
	var uploadedFileData []byte
	var uploadedFileName string
	ct := c.Request.Header.Get("Content-Type")
	if len(ct) >= 10 && ct[:10] == "multipart/" {
		invoiceNumber = c.PostForm("invoice_number")
		if fh, err := c.FormFile("invoice_file"); err == nil && fh != nil {
			if fh.Size > 20*1024*1024 {
				c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invoice file too large (max 20MB)"}})
				return
			}
			f, err := fh.Open()
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Failed to read uploaded file"}})
				return
			}
			defer func() { _ = f.Close() }()
			data, err := io.ReadAll(io.LimitReader(f, 20*1024*1024))
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Failed to read uploaded file"}})
				return
			}
			uploadedFileData = data
			uploadedFileName = fh.Filename
		}
	} else {
		var req struct {
			InvoiceNumber string `json:"invoice_number"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			r.logger.Warn("Failed to bind complete invoice request", zap.Error(err))
		}
		invoiceNumber = req.InvoiceNumber
	}

	// Completing marks the request issued AND registers it in the invoices
	// table — the billing history tab reads `invoices`, so without this row
	// an issued invoice never surfaces for the user/enterprise.
	tx, err := r.db.Pool.Begin(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to complete request"}})
		return
	}
	defer func() { _ = tx.Rollback(c.Request.Context()) }()

	result, err := tx.Exec(c.Request.Context(),
		`UPDATE invoice_requests SET status = 'completed', invoice_number = $2, issued_at = NOW(), updated_at = NOW()
		 WHERE id = $1::uuid AND status = 'processing'`,
		requestID, invoiceNumber)
	if err != nil {
		r.logger.Error("Failed to complete invoice request", zap.String("id", requestID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to complete request"}})
		return
	}
	if result.RowsAffected() == 0 {
		c.JSON(http.StatusConflict, gin.H{"error": gin.H{"message": "Invoice request is not in 'processing' status"}})
		return
	}
	if _, err := tx.Exec(c.Request.Context(),
		`INSERT INTO invoices (user_id, enterprise_id, invoice_number, amount, status, request_id)
		 SELECT user_id, enterprise_id, invoice_number, amount, 'issued', id
		 FROM invoice_requests WHERE id = $1::uuid`,
		requestID); err != nil {
		r.logger.Error("Failed to register issued invoice", zap.String("id", requestID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to register invoice"}})
		return
	}
	if err := tx.Commit(c.Request.Context()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to complete request"}})
		return
	}

	// Generate/store the downloadable invoice best-effort: issuance already
	// succeeded. An admin-uploaded file wins over the auto-generated PDF.
	if uploadedFileData != nil {
		r.storeUploadedInvoice(c.Request.Context(), requestID, uploadedFileData, uploadedFileName)
	} else {
		r.generateInvoicePDF(c.Request.Context(), requestID)
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// storeUploadedInvoice persists the admin-uploaded invoice file (finance-
// issued VAT fapiao etc.) and points the invoices row at it.
func (r *AdminRoutes) storeUploadedInvoice(ctx context.Context, requestID string, data []byte, filename string) {
	if r.storage == nil {
		r.logger.Warn("invoice upload: storage not configured", zap.String("id", requestID))
		return
	}
	ext := "pdf"
	for _, e := range []string{".pdf", ".png", ".jpg", ".jpeg"} {
		if len(filename) > len(e) && filename[len(filename)-len(e):] == e {
			ext = e[1:]
			break
		}
	}
	storageKey := fmt.Sprintf("invoices/uploaded/%s.%s", requestID, ext)
	if err := r.storage.Upload(ctx, storageKey, bytes.NewReader(data), storage.UploadOptions{
		ContentType: http.DetectContentType(data),
		Size:        int64(len(data)),
	}); err != nil {
		r.logger.Warn("invoice upload: upload failed", zap.String("id", requestID), zap.Error(err))
		return
	}
	if _, err := r.db.Pool.Exec(ctx,
		`UPDATE invoices SET storage_key = $2, invoice_source = 'uploaded' WHERE request_id = $1::uuid`,
		requestID, storageKey); err != nil {
		r.logger.Warn("invoice upload: record storage key failed", zap.String("id", requestID), zap.Error(err))
	}
}

// generateInvoicePDF renders the invoice PDF and stores it via the storage
// provider (same local/S3 abstraction as enterprise application documents),
// then records storage_key on the invoices row.
func (r *AdminRoutes) generateInvoicePDF(ctx context.Context, requestID string) {
	if r.storage == nil || r.cfg == nil {
		return
	}
	var title, taxNumber, billTo, invoiceNumber string
	var amountCents int64
	var issuedAt time.Time
	err := r.db.QueryRow(ctx,
		`SELECT COALESCE(ir.title, ''), COALESCE(ir.tax_number, ''),
		        COALESCE(e.name, u.email), ir.amount, ir.issued_at, COALESCE(ir.invoice_number, '')
		 FROM invoice_requests ir
		 JOIN users u ON u.id = ir.user_id
		 LEFT JOIN enterprises e ON e.id = ir.enterprise_id
		 WHERE ir.id = $1::uuid`,
		requestID).Scan(&title, &taxNumber, &billTo, &amountCents, &issuedAt, &invoiceNumber)
	if err != nil {
		r.logger.Warn("invoice pdf: load request failed", zap.String("id", requestID), zap.Error(err))
		return
	}
	if title == "" {
		title = "AI API services"
	}
	if invoiceNumber == "" {
		invoiceNumber = "INV-" + requestID[:8]
	}

	// GST treatment by customer jurisdiction (scheme C): Singapore customers
	// pay 9% GST on top (price-exclusive, mirroring how AWS invoices
	// Woodgit); overseas customers are zero-rated. Country priority: the
	// enterprise's own country (enterprise/settings, maintained by the EA),
	// then the registration application, then the billing address, then
	// unknown (treated as overseas/0%).
	country := invoiceCustomerCountry(ctx, r.db, requestID)
	gstPercent := 0.0
	if strings.EqualFold(country, "SG") || strings.EqualFold(country, "Singapore") {
		gstPercent = 9.0
	}
	amountUSD := float64(amountCents) / 100.0
	gstCents := int64(amountUSD * gstPercent / 100.0 * 100.0)

	pdfBytes, err := invoicepdf.Render(invoicepdf.Invoice{
		InvoiceNumber:   invoiceNumber,
		IssuedAt:        issuedAt,
		BillTo:          billTo,
		TaxNumber:       taxNumber,
		Description:     title,
		AmountUSD:       amountUSD,
		GSTPercent:      gstPercent,
		CustomerCountry: country,
		PlatformName:    "Woodgit",
		PlatformURL:     r.cfg.App.BaseURL,
	})
	if err != nil {
		r.logger.Warn("invoice pdf: render failed", zap.String("id", requestID), zap.Error(err))
		return
	}

	storageKey := fmt.Sprintf("invoices/%s.pdf", requestID)
	if err := r.storage.Upload(ctx, storageKey, bytes.NewReader(pdfBytes), storage.UploadOptions{
		ContentType: "application/pdf",
		Size:        int64(len(pdfBytes)),
	}); err != nil {
		r.logger.Warn("invoice pdf: upload failed", zap.String("id", requestID), zap.Error(err))
		return
	}
	if _, err := r.db.Pool.Exec(ctx,
		`UPDATE invoices SET storage_key = $2, gst_cents = $3 WHERE request_id = $1::uuid`,
		requestID, storageKey, gstCents); err != nil {
		r.logger.Warn("invoice pdf: record storage key failed", zap.String("id", requestID), zap.Error(err))
	}
}

// invoiceCustomerCountry resolves the customer's country for GST treatment:
// the enterprise's own record (enterprise/settings, EA-maintained) wins,
// falling back to the registration application, then the billing address.
// Unknown means overseas (0% GST).
func invoiceCustomerCountry(ctx context.Context, database *db.DB, requestID string) string {
	var country string
	_ = database.QueryRow(ctx,
		`SELECT COALESCE(
		    (SELECT e.country FROM invoice_requests ir
		      JOIN enterprises e ON e.id = ir.enterprise_id
		      WHERE ir.id = $1::uuid AND e.country IS NOT NULL AND e.country <> ''),
		    (SELECT ea.country FROM invoice_requests ir
		      JOIN enterprises e ON e.id = ir.enterprise_id
		      LEFT JOIN enterprise_applications ea ON ea.id = e.application_id
		      WHERE ir.id = $1::uuid AND ea.country IS NOT NULL AND ea.country <> ''),
		    (SELECT a.country FROM invoice_requests ir
		      LEFT JOIN addresses a ON a.id = ir.address_id
		      WHERE ir.id = $1::uuid AND a.country IS NOT NULL AND a.country <> ''),
		    '')`,
		requestID).Scan(&country)
	return country
}

// getUserReconciliation returns financial snapshot for a user
func (r *AdminRoutes) getUserReconciliation(c *gin.Context) {
	userID := c.Param("id")
	ctx := c.Request.Context()

	// Balance snapshot
	var balance, giftBalance int64
	var email, name string
	_ = r.db.QueryRow(ctx,
		`SELECT COALESCE(balance, 0), COALESCE(gift_balance, 0), email, COALESCE(name, '')
		 FROM users WHERE id = $1::uuid`, userID).Scan(&balance, &giftBalance, &email, &name)

	// Total consumption (real funds)
	var totalConsumption int64
	_ = r.db.QueryRow(ctx,
		`SELECT COALESCE(SUM(ABS(amount)), 0)
		 FROM balance_transactions
		 WHERE user_id = $1::uuid AND transaction_type = 'usage' AND balance_type = 'balance'`,
		userID).Scan(&totalConsumption)

	// Total gift consumption
	var giftConsumption int64
	_ = r.db.QueryRow(ctx,
		`SELECT COALESCE(SUM(ABS(amount)), 0)
		 FROM balance_transactions
		 WHERE user_id = $1::uuid AND transaction_type = 'usage' AND balance_type = 'gift'`,
		userID).Scan(&giftConsumption)

	// Total recharge
	var totalRecharge int64
	_ = r.db.QueryRow(ctx,
		`SELECT COALESCE(SUM(amount), 0)
		 FROM balance_transactions
		 WHERE user_id = $1::uuid AND transaction_type = 'recharge'`,
		userID).Scan(&totalRecharge)

	// Invoice requested (invoice_requests.amount is stored in USD cents)
	var invoiceRequestedCents int64
	_ = r.db.QueryRow(ctx,
		`SELECT COALESCE(SUM(amount), 0)
		 FROM invoice_requests
		 WHERE user_id = $1::uuid AND status IN ('pending', 'processing', 'completed')`,
		userID).Scan(&invoiceRequestedCents)
	invoiceRequested := invoiceRequestedCents * 10000 // cents → micro-USD quota

	// Subscription stats
	var subscriptionRecharge int64
	_ = r.db.QueryRow(ctx,
		`SELECT COALESCE(SUM(amount), 0) FROM orders
		 WHERE deleted_at IS NULL AND user_id = $1::uuid AND status = 'completed'
		   AND product_type IN ('subscription', 'subscription_renewal')`,
		userID).Scan(&subscriptionRecharge)

	var subQuotaTotal, subQuotaRemaining int64
	_ = r.db.QueryRow(ctx,
		`SELECT COALESCE(SUM(quota_total), 0), COALESCE(SUM(quota_remaining), 0)
		 FROM user_subscriptions WHERE deleted_at IS NULL AND user_id = $1::uuid AND status = 'active'`,
		userID).Scan(&subQuotaTotal, &subQuotaRemaining)
	var subscriptionBalance int64
	if subQuotaTotal > 0 {
		subscriptionBalance = int64(float64(subscriptionRecharge) * float64(subQuotaRemaining) / float64(subQuotaTotal))
	} else {
		subscriptionBalance = subscriptionRecharge
	}

	// Recent ledger (last 20). Optional from/to date filters let the
	// reconciliation drill-down scope the ledger to the selected range.
	ledgerSQL := `
		SELECT created_at, transaction_type, amount, balance_type, reference_id, description
		FROM balance_transactions
		WHERE user_id = $1::uuid`
	ledgerArgs := []interface{}{userID}
	if from := c.Query("from"); from != "" {
		ledgerArgs = append(ledgerArgs, from+" 00:00:00")
		ledgerSQL += fmt.Sprintf(" AND created_at >= $%d::timestamp", len(ledgerArgs))
	}
	if to := c.Query("to"); to != "" {
		ledgerArgs = append(ledgerArgs, to+" 23:59:59")
		ledgerSQL += fmt.Sprintf(" AND created_at <= $%d::timestamp", len(ledgerArgs))
	}
	ledgerSQL += " ORDER BY created_at DESC LIMIT 20"
	rows, err := r.db.Query(ctx, ledgerSQL, ledgerArgs...)
	if err != nil {
		r.logger.Error("Failed to query ledger", zap.Error(err))
		return
	}
	defer rows.Close()

	ledger := []gin.H{}
	for rows.Next() {
		var t time.Time
		var txType, balanceType, refID, desc string
		var amount int64
		if err := rows.Scan(&t, &txType, &amount, &balanceType, &refID, &desc); err == nil {
			ledger = append(ledger, gin.H{
				"created_at":   t,
				"type":         txType,
				"amount":       QuotaToUSDFloat(amount),
				"amount_quota": amount,
				"balance_type": balanceType,
				"reference_id": refID,
				"description":  desc,
			})
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"user": gin.H{
			"id":            userID,
			"email":         email,
			"name":          name,
			"balance":       QuotaToUSDFloat(balance),
			"balance_quota": balance,
			"gift_balance":  QuotaToUSDFloat(giftBalance),
			"total_balance": QuotaToUSDFloat(balance + giftBalance),
		},
		"stats": gin.H{
			"total_consumption":     QuotaToUSDFloat(totalConsumption),
			"gift_consumption":      QuotaToUSDFloat(giftConsumption),
			"total_recharge":        QuotaToUSDFloat(totalRecharge),
			"invoice_requested":     QuotaToUSDFloat(invoiceRequested),
			"invoice_eligible":      QuotaToUSDFloat(totalConsumption - invoiceRequested),
			"subscription_recharge": QuotaToUSDFloat(subscriptionRecharge),
			"subscription_balance":  QuotaToUSDFloat(subscriptionBalance),
		},
		"ledger": ledger,
	})
}

// getLedger returns unified ledger for a user with filtering
func (r *AdminRoutes) getLedger(c *gin.Context) {
	userID := c.Query("user_id")
	if userID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "user_id is required"}})
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 50
	}
	offset := (page - 1) * limit

	var total int
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM balance_transactions WHERE user_id = $1::uuid`, userID).Scan(&total)

	rows, err := r.db.Query(c.Request.Context(),
		`SELECT created_at, transaction_type, amount, balance_type, balance_before, balance_after, reference_id, description
		 FROM balance_transactions
		 WHERE user_id = $1::uuid
		 ORDER BY created_at DESC LIMIT $2 OFFSET $3`,
		userID, limit, offset)
	if err != nil {
		r.logger.Error("Failed to query ledger", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to query ledger"}})
		return
	}
	defer rows.Close()

	records := []gin.H{}
	for rows.Next() {
		var t time.Time
		var txType, balanceType, refID, desc string
		var amount, before, after int64
		if err := rows.Scan(&t, &txType, &amount, &balanceType, &before, &after, &refID, &desc); err == nil {
			records = append(records, gin.H{
				"created_at":     t,
				"type":           txType,
				"amount":         QuotaToUSDFloat(amount),
				"amount_quota":   amount,
				"balance_type":   balanceType,
				"balance_before": QuotaToUSDFloat(before),
				"balance_after":  QuotaToUSDFloat(after),
				"reference_id":   refID,
				"description":    desc,
			})
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"records":     records,
		"total":       total,
		"page":        page,
		"limit":       limit,
		"total_pages": (total + limit - 1) / limit,
	})
}

// ── User/Enterprise usage detail for reconciliation (P0) ──────────────────────

func parseRange(c *gin.Context) string {
	r := c.DefaultQuery("range", "7d")
	if _, ok := query.AllowedRanges[r]; !ok {
		r = "7d"
	}
	return r
}

// getUserUsage returns trend + model/key distribution for a single user.
func (r *AdminRoutes) getUserUsage(c *gin.Context) {
	userID := c.Param("id")
	rangeKey := parseRange(c)
	ctx := c.Request.Context()

	trend, err := query.GetUserUsageTrend(ctx, r.db, userID, rangeKey)
	if err != nil {
		r.logger.Error("Failed to get user usage trend", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to query usage trend"}})
		return
	}

	models, _ := query.GetUsageDistributionRange(ctx, r.db, userID, rangeKey, "model")
	keys, _ := query.GetUsageDistributionRange(ctx, r.db, userID, rangeKey, "api_key")

	c.JSON(http.StatusOK, gin.H{
		"range":  rangeKey,
		"trend":  trend,
		"models": models,
		"keys":   keys,
	})
}

// getUserLogs returns paginated usage_records for a single user.
func (r *AdminRoutes) getUserLogs(c *gin.Context) {
	userID := c.Param("id")
	ctx := c.Request.Context()

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}

	filters := query.UsageRecordFilter{
		IsAdmin:   true,
		UserID:    userID,
		StartDate: c.Query("from"),
		EndDate:   c.Query("to"),
		Page:      page,
		PageSize:  limit,
	}

	records, total, err := query.GetUsageRecords(ctx, r.db, "", filters)
	if err != nil {
		r.logger.Error("Failed to get user logs", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to query usage logs"}})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"records":     records,
		"total":       total,
		"page":        page,
		"limit":       limit,
		"total_pages": (total + limit - 1) / limit,
	})
}

// exportUserLogs exports a user's usage records as CSV.
func (r *AdminRoutes) exportUserLogs(c *gin.Context) {
	userID := c.Param("id")
	ctx := c.Request.Context()

	filters := query.UsageRecordFilter{
		IsAdmin:   true,
		UserID:    userID,
		StartDate: c.Query("from"),
		EndDate:   c.Query("to"),
		Page:      1,
		PageSize:  10000,
	}

	records, _, err := query.GetUsageRecords(ctx, r.db, "", filters)
	if err != nil {
		r.logger.Error("Failed to export user logs", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Export failed"}})
		return
	}

	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=user-usage-"+userID+"-"+time.Now().UTC().Format("200601021504")+".csv")
	_, _ = c.Writer.Write([]byte{0xEF, 0xBB, 0xBF})
	_, _ = c.Writer.WriteString("Time,Model,Endpoint,Status,Streaming,Requests,Input Tokens,Output Tokens,Cache Tokens,Latency(ms),Cost(USD),Key Prefix,Client IP,Client App,Agent Tools,Called Tools\n")
	for _, rec := range records {
		_, _ = fmt.Fprintf(c.Writer, "%s,%s,%s,%s,%v,%d,%d,%d,%d,%d,%.6f,%s,%s,%s,%s,%s\n",
			rec.CreatedAt.Format("2006-01-02 15:04:05"),
			rec.Model,
			rec.Endpoint,
			rec.Status,
			rec.IsStreaming,
			rec.Requests,
			rec.InputTokens,
			rec.OutputTokens,
			rec.CachedTokens,
			rec.LatencyMs,
			QuotaToUSDFloat(rec.Cost),
			rec.KeyPrefix,
			rec.ClientIP,
			rec.UserAgent,
			rec.AgentTools,
			rec.CalledTools,
		)
	}
}

// getEnterpriseUsage returns trend + model/key distribution for an enterprise.
func (r *AdminRoutes) getEnterpriseUsage(c *gin.Context) {
	enterpriseID := c.Param("id")
	rangeKey := parseRange(c)
	ctx := c.Request.Context()

	trend, err := query.GetEnterpriseUsageTrend(ctx, r.db, enterpriseID, rangeKey)
	if err != nil {
		r.logger.Error("Failed to get enterprise usage trend", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to query usage trend"}})
		return
	}

	models, _ := query.GetEnterpriseUsageDistributionRange(ctx, r.db, enterpriseID, rangeKey, "model")
	keys, _ := query.GetEnterpriseUsageDistributionRange(ctx, r.db, enterpriseID, rangeKey, "api_key")

	c.JSON(http.StatusOK, gin.H{
		"range":  rangeKey,
		"trend":  trend,
		"models": models,
		"keys":   keys,
	})
}

// getEnterpriseLogs returns paginated usage_records for an enterprise.
func (r *AdminRoutes) getEnterpriseLogs(c *gin.Context) {
	enterpriseID := c.Param("id")
	ctx := c.Request.Context()

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}

	filters := query.UsageRecordFilter{
		IsAdmin:      true,
		EnterpriseID: enterpriseID,
		StartDate:    c.Query("from"),
		EndDate:      c.Query("to"),
		Page:         page,
		PageSize:     limit,
	}

	records, total, err := query.GetUsageRecords(ctx, r.db, "", filters)
	if err != nil {
		r.logger.Error("Failed to get enterprise logs", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to query usage logs"}})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"records":     records,
		"total":       total,
		"page":        page,
		"limit":       limit,
		"total_pages": (total + limit - 1) / limit,
	})
}

// exportEnterpriseLogs exports an enterprise's usage records as CSV.
func (r *AdminRoutes) exportEnterpriseLogs(c *gin.Context) {
	enterpriseID := c.Param("id")
	ctx := c.Request.Context()

	filters := query.UsageRecordFilter{
		IsAdmin:      true,
		EnterpriseID: enterpriseID,
		StartDate:    c.Query("from"),
		EndDate:      c.Query("to"),
		Page:         1,
		PageSize:     10000,
	}

	records, _, err := query.GetUsageRecords(ctx, r.db, "", filters)
	if err != nil {
		r.logger.Error("Failed to export enterprise logs", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Export failed"}})
		return
	}

	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=enterprise-usage-"+enterpriseID+"-"+time.Now().UTC().Format("200601021504")+".csv")
	_, _ = c.Writer.Write([]byte{0xEF, 0xBB, 0xBF})
	_, _ = c.Writer.WriteString("Time,Model,Endpoint,Status,Streaming,Requests,Input Tokens,Output Tokens,Cache Tokens,Latency(ms),Cost(USD),Key Prefix,Client IP,Client App,Agent Tools,Called Tools\n")
	for _, rec := range records {
		_, _ = fmt.Fprintf(c.Writer, "%s,%s,%s,%s,%v,%d,%d,%d,%d,%d,%.6f,%s,%s,%s,%s,%s\n",
			rec.CreatedAt.Format("2006-01-02 15:04:05"),
			rec.Model,
			rec.Endpoint,
			rec.Status,
			rec.IsStreaming,
			rec.Requests,
			rec.InputTokens,
			rec.OutputTokens,
			rec.CachedTokens,
			rec.LatencyMs,
			QuotaToUSDFloat(rec.Cost),
			rec.KeyPrefix,
			rec.ClientIP,
			rec.UserAgent,
			rec.AgentTools,
			rec.CalledTools,
		)
	}
}

// getEnterpriseReconciliation returns financial snapshot for an enterprise
func (r *AdminRoutes) getEnterpriseReconciliation(c *gin.Context) {
	enterpriseID := c.Param("id")
	ctx := c.Request.Context()

	var name string
	var quotaTotal, quotaUsed, creditTotal, creditUsed int64
	var billingCycle string
	err := r.db.QueryRow(ctx,
		`SELECT name, COALESCE(quota_total, 0), COALESCE(quota_used, 0),
		        COALESCE(credit_total, 0), COALESCE(credit_used, 0), COALESCE(billing_cycle, 'monthly')
		 FROM enterprises WHERE id = $1::uuid`, enterpriseID).Scan(&name, &quotaTotal, &quotaUsed, &creditTotal, &creditUsed, &billingCycle)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Enterprise not found"}})
		return
	}

	// Member count
	var memberCount int
	_ = r.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM users WHERE enterprise_id = $1::uuid AND deleted_at IS NULL`,
		enterpriseID).Scan(&memberCount)

	// Member usage summary (only usage from enterprise-owned keys)
	rows, err := r.db.Query(ctx, `
		SELECT u.id::text, COALESCE(u.name, u.email) as user_name,
		       COALESCE(SUM(ur.total_cost), 0) as total_cost,
		       COALESCE(SUM(ur.request_count), 0) as total_requests
		FROM users u
		-- Include soft-deleted keys (no k.deleted_at filter): deleting a key must
		-- not erase its historical usage/cost from the enterprise's member summary.
		LEFT JOIN api_keys k ON k.user_id = u.id AND k.enterprise_id = $1::uuid
		LEFT JOIN usage_records ur ON ur.user_api_key_id = k.id
		WHERE u.enterprise_id = $1::uuid AND u.deleted_at IS NULL
		GROUP BY u.id, u.name, u.email
		ORDER BY total_cost DESC LIMIT 50`, enterpriseID)
	if err != nil {
		r.logger.Error("Failed to query member usage", zap.Error(err))
		return
	}
	defer rows.Close()

	members := []gin.H{}
	for rows.Next() {
		var uid, userName string
		var totalCost, totalRequests int64
		if err := rows.Scan(&uid, &userName, &totalCost, &totalRequests); err == nil {
			members = append(members, gin.H{
				"user_id":        uid,
				"user_name":      userName,
				"total_cost":     QuotaToUSDFloat(totalCost),
				"total_requests": totalRequests,
			})
		}
	}

	// Credit log. Optional from/to date filters scope it to the selected range.
	creditSQL := `
		SELECT created_at, change_type, field_name, action, reason, delta
		FROM enterprise_audit_log
		WHERE enterprise_id = $1::uuid`
	creditArgs := []interface{}{enterpriseID}
	if from := c.Query("from"); from != "" {
		creditArgs = append(creditArgs, from+" 00:00:00")
		creditSQL += fmt.Sprintf(" AND created_at >= $%d::timestamp", len(creditArgs))
	}
	if to := c.Query("to"); to != "" {
		creditArgs = append(creditArgs, to+" 23:59:59")
		creditSQL += fmt.Sprintf(" AND created_at <= $%d::timestamp", len(creditArgs))
	}
	creditSQL += " ORDER BY created_at DESC LIMIT 20"
	logRows, err := r.db.Query(ctx, creditSQL, creditArgs...)
	if err != nil {
		r.logger.Error("Failed to query credit log", zap.Error(err))
		return
	}
	defer logRows.Close()

	creditLogs := []gin.H{}
	for logRows.Next() {
		var t time.Time
		var changeType, fieldName, action, reason string
		var delta int64
		if err := logRows.Scan(&t, &changeType, &fieldName, &action, &reason, &delta); err == nil {
			creditLogs = append(creditLogs, gin.H{
				"created_at":  t,
				"change_type": changeType,
				"field_name":  fieldName,
				"action":      action,
				"reason":      reason,
				"delta":       QuotaToUSDFloat(delta),
			})
		}
	}

	// Billing block: monthly statements, two-pool eligibility and invoice
	// history, so system admins see actual consumption and outstanding
	// (un-invoiced) amounts without leaving the reconciliation page.
	var statements []gin.H
	stRows, sErr := r.db.Query(ctx,
		`SELECT period, credit_used_micro, quota_used, prepaid_topup_micro, COALESCE(paid_micro, 0), payment_note
		 FROM enterprise_statements WHERE enterprise_id = $1::uuid
		 ORDER BY period DESC LIMIT 24`, enterpriseID)
	if sErr == nil {
		defer stRows.Close()
		for stRows.Next() {
			var period string
			var creditMicro, quotaUsed, prepaidTopup, paidMicro int64
			var paymentNote *string
			if stRows.Scan(&period, &creditMicro, &quotaUsed, &prepaidTopup, &paidMicro, &paymentNote) == nil {
				var invoiced int64
				_ = r.db.QueryRow(ctx,
					`SELECT COALESCE(SUM(amount), 0) FROM invoice_requests
					 WHERE enterprise_id = $1::uuid AND pool = 'postpaid' AND period = $2
					   AND status IN ('pending', 'processing', 'completed')`,
					enterpriseID, period).Scan(&invoiced)
				// Payment status is derived: a statement with no consumption is
				// not a receivable; otherwise unpaid/partial/paid by paid vs consumed.
				paymentStatus := "unpaid"
				switch {
				case creditMicro <= 0:
					paymentStatus = "none"
				case paidMicro >= creditMicro:
					paymentStatus = "paid"
				case paidMicro > 0:
					paymentStatus = "partial"
				}
				note := ""
				if paymentNote != nil {
					note = *paymentNote
				}
				statements = append(statements, gin.H{
					"period":            period,
					"credit_used_usd":   float64(creditMicro) / 1_000_000.0,
					"quota_used":        quotaUsed,
					"prepaid_topup_usd": float64(prepaidTopup) / 1_000_000.0,
					"invoiced_cents":    invoiced,
					"open_cents":        creditMicro/10000 - invoiced,
					"paid_usd":          float64(paidMicro) / 1_000_000.0,
					"outstanding_usd":   float64(creditMicro-paidMicro) / 1_000_000.0,
					"payment_status":    paymentStatus,
					"payment_note":      note,
				})
			}
		}
	}

	var postpaidNetCents, prepaidConsumedCents, prepaidBalanceMicro, prepaidTopupMicro int64
	_ = r.db.QueryRow(ctx,
		`SELECT GREATEST(
		     COALESCE((SELECT SUM(credit_used_micro) FROM enterprise_statements WHERE enterprise_id = $1::uuid), 0) / 10000
		     - COALESCE((SELECT SUM(amount) FROM invoice_requests
		                 WHERE enterprise_id = $1::uuid AND pool = 'postpaid'
		                   AND status IN ('pending', 'processing', 'completed')), 0), 0)`,
		enterpriseID).Scan(&postpaidNetCents)
	_ = r.db.QueryRow(ctx,
		`SELECT COALESCE((SELECT SUM(delta) FROM enterprise_audit_log
		                  WHERE enterprise_id = $1::uuid AND change_type = 'prepaid_topup' AND field_name = 'prepaid_balance'), 0)`,
		enterpriseID).Scan(&prepaidTopupMicro)
	_ = r.db.QueryRow(ctx, `SELECT COALESCE(prepaid_balance, 0) FROM enterprises WHERE id = $1::uuid`, enterpriseID).Scan(&prepaidBalanceMicro)
	prepaidConsumedCents = (prepaidTopupMicro - prepaidBalanceMicro) / 10000
	if prepaidConsumedCents < 0 {
		prepaidConsumedCents = 0
	}
	var prepaidInvoicedCents int64
	_ = r.db.QueryRow(ctx,
		`SELECT COALESCE(SUM(amount), 0) FROM invoice_requests
		 WHERE enterprise_id = $1::uuid AND pool = 'prepaid' AND status IN ('pending', 'processing', 'completed')`,
		enterpriseID).Scan(&prepaidInvoicedCents)
	prepaidEligibleCents := prepaidConsumedCents - prepaidInvoicedCents
	if prepaidEligibleCents < 0 {
		prepaidEligibleCents = 0
	}

	invoiceRows, iErr := r.db.Query(ctx,
		`SELECT ir.invoice_number, ir.amount, ir.status, COALESCE(ir.pool, 'postpaid'), ir.issued_at, ir.title
		 FROM invoice_requests ir
		 WHERE ir.enterprise_id = $1::uuid AND ir.status = 'completed'
		 ORDER BY ir.issued_at DESC NULLS LAST LIMIT 20`, enterpriseID)
	invoices := []gin.H{}
	if iErr == nil {
		defer invoiceRows.Close()
		for invoiceRows.Next() {
			var num, status, pool, title string
			var amount int64
			var issuedAt interface{}
			if invoiceRows.Scan(&num, &amount, &status, &pool, &issuedAt, &title) == nil {
				invoices = append(invoices, gin.H{
					"invoice_number": num, "amount_cents": amount, "status": status,
					"pool": pool, "issued_at": issuedAt, "title": title,
				})
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"enterprise": gin.H{
			"id":            enterpriseID,
			"name":          name,
			"billing_cycle": billingCycle,
			"quota_total":   QuotaToUSDFloat(quotaTotal),
			"quota_used":    QuotaToUSDFloat(quotaUsed),
			"credit_total":  QuotaToUSDFloat(creditTotal),
			"credit_used":   QuotaToUSDFloat(creditUsed),
			"member_count":  memberCount,
		},
		"members":     members,
		"credit_logs": creditLogs,
		"billing": gin.H{
			"statements":              statements,
			"postpaid_eligible_cents": postpaidNetCents,
			"prepaid_consumed_cents":  prepaidConsumedCents,
			"prepaid_eligible_cents":  prepaidEligibleCents,
			"prepaid_balance_usd":     float64(prepaidBalanceMicro) / 1_000_000.0,
			"invoices":                invoices,
		},
	})
}

// reconciliationTopRangeFilter returns a SQL predicate for usage_records.created_at based on range.
// Supported values: 7d, 30d, anything else defaults to the current calendar month.
func reconciliationTopRangeFilter(rangeKey string) string {
	switch rangeKey {
	case "1d":
		return "ur.created_at >= NOW() - INTERVAL '1 day'"
	case "7d":
		return "ur.created_at >= NOW() - INTERVAL '7 days'"
	case "15d":
		return "ur.created_at >= NOW() - INTERVAL '15 days'"
	case "30d":
		return "ur.created_at >= NOW() - INTERVAL '30 days'"
	default:
		return "ur.created_at >= DATE_TRUNC('month', NOW())"
	}
}

// listTopUsers returns top 10 users by consumption for the selected range.
// Query param ?range=7d|30d|month (defaults to month).
func (r *AdminRoutes) listTopUsers(c *gin.Context) {
	ctx := c.Request.Context()
	dateFilter := reconciliationTopRangeFilter(c.Query("range"))
	// Oversight view: soft-deleted users stay listed (tagged in the UI) so
	// deleting an account can never hide its historical spend.
	rows, err := r.db.Query(ctx, fmt.Sprintf(`
		SELECT u.id::text, COALESCE(u.name, u.email) as display_name, u.email,
		       COALESCE(SUM(ur.total_cost), 0) as total_cost,
		       COALESCE(SUM(ur.request_count), 0) as total_requests,
		       COALESCE(u.balance, 0) as balance,
		       COALESCE(u.gift_balance, 0) as gift_balance,
		       u.deleted_at IS NOT NULL AS deleted
		FROM users u
		LEFT JOIN usage_records ur ON ur.user_id = u.id AND %s
		GROUP BY u.id, u.name, u.email, u.balance, u.gift_balance, u.deleted_at
		ORDER BY total_cost DESC
		LIMIT 20`, dateFilter))
	if err != nil {
		r.logger.Error("Failed to query top users", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to query top users"}})
		return
	}
	defer rows.Close()

	users := []gin.H{}
	for rows.Next() {
		var id, displayName, email string
		var totalCost, totalRequests, balance, giftBalance int64
		var deleted bool
		if err := rows.Scan(&id, &displayName, &email, &totalCost, &totalRequests, &balance, &giftBalance, &deleted); err == nil {
			users = append(users, gin.H{
				"id":             id,
				"display_name":   displayName,
				"email":          email,
				"deleted":        deleted,
				"total_cost":     QuotaToUSDFloat(totalCost),
				"total_requests": totalRequests,
				"balance":        QuotaToUSDFloat(balance),
				"gift_balance":   QuotaToUSDFloat(giftBalance),
				"total_balance":  QuotaToUSDFloat(balance + giftBalance),
			})
		}
	}
	c.JSON(http.StatusOK, gin.H{"users": users})
}

// searchEnterprises searches enterprises by name (admin only)
func (r *AdminRoutes) searchEnterprises(c *gin.Context) {
	ctx := c.Request.Context()
	q := strings.TrimSpace(c.Query("search"))

	// include_deleted=1 (reconciliation pickers) also matches soft-deleted
	// enterprises so their historical spend stays auditable.
	deletedClause := "e.deleted_at IS NULL"
	if c.Query("include_deleted") == "1" {
		deletedClause = "TRUE"
	}

	// Common SELECT shape (adds registration fields so the admin edit modal can
	// prefill without a second round-trip).
	selectSQL := `
		SELECT e.id::text, e.name, COALESCE(e.contact_email, ''), e.status,
		       COALESCE(e.quota_total, 0), COALESCE(e.quota_used, 0),
		       COALESCE(e.credit_total, 0), COALESCE(e.credit_used, 0),
		       (SELECT COUNT(*) FROM users WHERE enterprise_id = e.id AND deleted_at IS NULL) as member_count,
		       e.created_at, e.deleted_at IS NOT NULL AS deleted,
		       COALESCE(e.country,''), COALESCE(e.tax_number,''), COALESCE(e.legal_name,''),
		       COALESCE(e.contact_name,''), COALESCE(e.contact_phone,''), COALESCE(e.registered_address,''),
		       COALESCE(e.bank_name,''), COALESCE(e.bank_account,'')
		FROM enterprises e`

	var rows pgx.Rows
	var err error
	if q == "" {
		// Mirror the reconciliation top-lists: no query -> most recent 20.
		rows, err = r.db.Query(ctx, fmt.Sprintf(`%s WHERE %s ORDER BY e.created_at DESC LIMIT 20`, selectSQL, deletedClause))
	} else {
		// Multi-field fuzzy match: name / contact_email / legal_name / tax_number.
		like := "%" + q + "%"
		rows, err = r.db.Query(ctx, fmt.Sprintf(`%s
		WHERE %s AND (
		    e.name ILIKE $1 OR e.contact_email ILIKE $1 OR
		    COALESCE(e.legal_name,'') ILIKE $1 OR COALESCE(e.tax_number,'') ILIKE $1
		)
		ORDER BY e.name LIMIT 50`, selectSQL, deletedClause), like)
	}
	if err != nil {
		r.logger.Error("Failed to search enterprises", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to search enterprises"}})
		return
	}
	defer rows.Close()

	enterprises := []gin.H{}
	for rows.Next() {
		var id, name, contactEmail, status string
		var quotaTotal, quotaUsed, creditTotal, creditUsed int64
		var memberCount int
		var createdAt interface{}
		var deleted bool
		var country, taxNumber, legalName, contactName, contactPhone, registeredAddress, bankName, bankAccount string
		if err := rows.Scan(&id, &name, &contactEmail, &status, &quotaTotal, &quotaUsed, &creditTotal, &creditUsed, &memberCount, &createdAt, &deleted,
			&country, &taxNumber, &legalName, &contactName, &contactPhone, &registeredAddress, &bankName, &bankAccount); err == nil {
			enterprises = append(enterprises, gin.H{
				"id": id, "name": name, "contact_email": contactEmail, "status": status, "deleted": deleted,
				// USD floats, consistent with /reconciliation/top-enterprises —
				// the two endpoints feed the same page dropdown.
				"quota_total": QuotaToUSDFloat(quotaTotal), "quota_used": QuotaToUSDFloat(quotaUsed),
				"credit_total": QuotaToUSDFloat(creditTotal), "credit_used": QuotaToUSDFloat(creditUsed),
				"member_count": memberCount, "created_at": createdAt,
				"country": country, "tax_number": taxNumber, "legal_name": legalName,
				"contact_name": contactName, "contact_phone": contactPhone, "registered_address": registeredAddress,
				"bank_name": bankName, "bank_account": bankAccount,
			})
		}
	}
	c.JSON(http.StatusOK, gin.H{"enterprises": enterprises})
}

// listTopEnterprises returns top 20 enterprises by consumption for the selected range.
// Query param ?range=7d|30d|month (defaults to month).
func (r *AdminRoutes) listTopEnterprises(c *gin.Context) {
	ctx := c.Request.Context()
	dateFilter := reconciliationTopRangeFilter(c.Query("range"))
	rows, err := r.db.Query(ctx, fmt.Sprintf(`
		SELECT e.id::text, e.name,
		       COALESCE(e.credit_total, 0) as credit_total,
		       COALESCE(e.credit_used, 0) as credit_used,
		       COALESCE(SUM(ur.total_cost), 0) as total_cost,
		       (SELECT COUNT(*) FROM users WHERE enterprise_id = e.id AND deleted_at IS NULL) as member_count,
		       e.deleted_at IS NOT NULL AS deleted, COALESCE(e.status, '') AS status
		FROM enterprises e
		-- Include soft-deleted keys (no k.deleted_at filter): a deleted key must
		-- not erase its historical usage/cost from the enterprise usage summary.
		LEFT JOIN api_keys k ON k.enterprise_id = e.id
		LEFT JOIN usage_records ur ON ur.user_api_key_id = k.id AND %s
		GROUP BY e.id, e.name, e.credit_total, e.credit_used, e.deleted_at, e.status
		ORDER BY total_cost DESC
		LIMIT 20`, dateFilter))
	if err != nil {
		r.logger.Error("Failed to query top enterprises", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to query top enterprises"}})
		return
	}
	defer rows.Close()

	enterprises := []gin.H{}
	for rows.Next() {
		var id, name, entStatus string
		var creditTotal, creditUsed, totalCost int64
		var memberCount int
		var deleted bool
		if err := rows.Scan(&id, &name, &creditTotal, &creditUsed, &totalCost, &memberCount, &deleted, &entStatus); err == nil {
			enterprises = append(enterprises, gin.H{
				"id":           id,
				"name":         name,
				"deleted":      deleted,
				"status":       entStatus,
				"credit_total": QuotaToUSDFloat(creditTotal),
				"credit_used":  QuotaToUSDFloat(creditUsed),
				"total_cost":   QuotaToUSDFloat(totalCost),
				"member_count": memberCount,
			})
		}
	}
	c.JSON(http.StatusOK, gin.H{"enterprises": enterprises})
}

// ReconcileResult holds the outcome of a manual reconciliation run.
type ReconcileResult struct {
	Total      int `json:"total"`
	Inserted   int `json:"inserted"`
	Skipped    int `json:"skipped"` // trace_id already exists
	Failed     int `json:"failed"`
	Deductions struct {
		Gift         int64 `json:"gift"`
		Recharge     int64 `json:"recharge"`
		Balance      int64 `json:"balance"`
		Credit       int64 `json:"credit"`
		Subscription int64 `json:"subscription"`
	} `json:"deductions"`
}

// handleDeadLetterStatus returns counts for all dead letter queues and unbilled records.
func (r *AdminRoutes) handleDeadLetterStatus(c *gin.Context) {
	ctx := c.Request.Context()
	queues := []string{"webhook:billing:dead", "webhook:usage:deadletter", "webhook:usage-stats:deadletter", "billing:freeze:dead"}
	status := gin.H{}

	for _, q := range queues {
		if r.db.Redis != nil {
			length, _ := r.db.Redis.LLen(ctx, q).Result()
			status[q] = length
		} else {
			status[q] = -1
		}
	}

	// Unbilled records: total_cost > 0 but all deduction columns are 0, older than 10 minutes.
	// These are records that entered the stats pipeline but were never charged by the billing
	// queue (e.g., dead letter billing records, or records stuck in the billing queue).
	var unbilledCount int64
	_ = r.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM usage_records
		 WHERE total_cost > 0
		   AND COALESCE(gift_deduction, 0) = 0
		   AND COALESCE(credit_deduction, 0) = 0
		   AND COALESCE(subscription_deduction, 0) = 0
		   AND COALESCE(balance_deduction, 0) = 0
		   AND created_at < NOW() - INTERVAL '10 minutes'`,
	).Scan(&unbilledCount)
	status["unbilled_records"] = unbilledCount

	c.JSON(http.StatusOK, gin.H{"dead_letter_queues": status})
}

// handleListUnbilledRecords returns unbilled record details for admin review.
func (r *AdminRoutes) handleListUnbilledRecords(c *gin.Context) {
	ctx := c.Request.Context()
	limit := 100
	if l := c.Query("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}

	rows, err := r.db.Query(ctx,
		`SELECT trace_id, user_api_key_id, model, provider, total_cost, status, error_code, created_at
		 FROM usage_records
		 WHERE total_cost > 0
		   AND COALESCE(gift_deduction, 0) = 0
		   AND COALESCE(credit_deduction, 0) = 0
		   AND COALESCE(subscription_deduction, 0) = 0
		   AND COALESCE(balance_deduction, 0) = 0
		   AND created_at < NOW() - INTERVAL '10 minutes'
		 ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to query unbilled records"}})
		return
	}
	defer rows.Close()

	records := []gin.H{}
	for rows.Next() {
		var traceID, apiKeyID, model, provider, status, errorCode string
		var totalCost int64
		var createdAt interface{}
		_ = rows.Scan(&traceID, &apiKeyID, &model, &provider, &totalCost, &status, &errorCode, &createdAt)
		records = append(records, gin.H{
			"trace_id":   traceID,
			"api_key_id": apiKeyID,
			"model":      model,
			"provider":   provider,
			"total_cost": totalCost,
			"status":     status,
			"error_code": errorCode,
			"created_at": createdAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{"records": records, "count": len(records)})
}

// handleReconcileUpload accepts an NDJSON file from Proxy fallback logs and
// replays missing usage records through processRecord. When mode=unbilled, it
// reads unbilled records from the DB (dead letter recovery) instead of a file upload.
func (r *AdminRoutes) handleReconcileUpload(c *gin.Context) {
	mode := c.DefaultQuery("mode", "file")

	if mode == "unbilled" {
		r.reconcileUnbilled(c)
		return
	}

	file, _, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "No file uploaded"}})
		return
	}
	defer func() { _ = file.Close() }()

	result := ReconcileResult{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 1024*1024) // 1MB max line

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var record UsageRecord
		if err := json.Unmarshal(line, &record); err != nil {
			r.logger.Warn("Failed to unmarshal reconcile line", zap.Error(err))
			result.Failed++
			continue
		}
		result.Total++

		// Check if trace_id already exists
		var exists int
		err := r.db.QueryRow(c.Request.Context(),
			"SELECT 1 FROM usage_records WHERE trace_id = $1 LIMIT 1",
			record.TraceID,
		).Scan(&exists)
		if err == nil {
			// Already processed — skip
			result.Skipped++
			continue
		}

		// Re-process through the full sync path (same as processRecord)
		// We need a WebhookRoutes instance to call processRecord.
		// For reconciliation we construct a minimal one.
		wr := &WebhookRoutes{
			db:         r.db,
			logger:     r.logger,
			cfg:        r.cfg,
			billingSvc: services.NewBillingService(r.db, r.logger),
		}
		if err := wr.processRecord(c.Request.Context(), &record); err != nil {
			r.logger.Warn("Reconcile processRecord failed",
				zap.Error(err), zap.String("trace_id", record.TraceID))
			result.Failed++
			continue
		}
		result.Inserted++
	}

	if err := scanner.Err(); err != nil {
		r.logger.Error("Reconcile scanner error", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to read file"}})
		return
	}

	// Log audit
	if r.audit != nil {
		r.audit.Log(c.Request.Context(), c.GetString("user_id"), c.GetString("email"),
			"admin", "reconcile", "info",
			fmt.Sprintf("Reconciled %d records (%d inserted, %d skipped, %d failed)",
				result.Total, result.Inserted, result.Skipped, result.Failed))
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"result":  result,
	})
}

// handleReconcilePreview returns a preview of what would happen without actually
// processing the records.
func (r *AdminRoutes) handleReconcilePreview(c *gin.Context) {
	file, _, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "No file uploaded"}})
		return
	}
	defer func() { _ = file.Close() }()

	type previewItem struct {
		TraceID  string `json:"trace_id"`
		Model    string `json:"model"`
		Provider string `json:"provider"`
		Exists   bool   `json:"exists"`
		UserID   string `json:"user_id,omitempty"`
		Cost     int64  `json:"cost,omitempty"`
	}

	var items []previewItem
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	count := 0

	for scanner.Scan() && count < 100 {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var record UsageRecord
		if err := json.Unmarshal(line, &record); err != nil {
			continue
		}
		count++

		var exists int
		_ = r.db.QueryRow(c.Request.Context(),
			"SELECT 1 FROM usage_records WHERE trace_id = $1 LIMIT 1",
			record.TraceID,
		).Scan(&exists)

		item := previewItem{
			TraceID:  record.TraceID,
			Model:    record.Model,
			Provider: record.Provider,
			Exists:   exists == 1,
		}
		if !item.Exists {
			// Quick lookup for preview
			var userID string
			_ = r.db.QueryRow(c.Request.Context(),
				"SELECT user_id::text FROM api_keys WHERE key_hash = $1 AND deleted_at IS NULL LIMIT 1",
				record.APIKeyHash,
			).Scan(&userID)
			item.UserID = userID
			item.Cost = int64(record.Cost)
		}
		items = append(items, item)
	}

	c.JSON(http.StatusOK, gin.H{
		"success":      true,
		"preview":      items,
		"previewLimit": 100,
		"hasMore":      scanner.Scan(),
	})
}

// reconcileUnbilled reprocesses unbilled records through processRecord to retry billing.
// This covers the gap where dead letter billing records already exist in usage_records
// but were never charged (all deduction columns are 0).
func (r *AdminRoutes) reconcileUnbilled(c *gin.Context) {
	ctx := c.Request.Context()
	limit := 200
	if l := c.Query("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 1000 {
			limit = n
		}
	}

	rows, err := r.db.Query(ctx,
		`SELECT trace_id, api_key_hash, model, provider,
		        input_tokens, output_tokens, cached_tokens, cache_creation_tokens,
		        effective_input_tokens, reasoning_tokens, latency_ms,
		        status, error_code, is_streaming, pricing_error,
		        group_id, pricing_ratio,
		        created_at
		 FROM usage_records
		 WHERE total_cost > 0
		   AND COALESCE(gift_deduction, 0) = 0
		   AND COALESCE(credit_deduction, 0) = 0
		   AND COALESCE(subscription_deduction, 0) = 0
		   AND COALESCE(balance_deduction, 0) = 0
		   AND created_at < NOW() - INTERVAL '10 minutes'
		 ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to query unbilled records"}})
		return
	}
	defer rows.Close()

	var records []UsageRecord
	for rows.Next() {
		var rec UsageRecord
		var pricingRatio *float64
		if err := rows.Scan(&rec.TraceID, &rec.APIKeyHash, &rec.Model, &rec.Provider,
			&rec.InputTokens, &rec.OutputTokens, &rec.CachedTokens, &rec.CacheCreationTokens,
			&rec.EffectiveInputTokens, &rec.ReasoningTokens, &rec.LatencyMs,
			&rec.Status, &rec.ErrorCode, &rec.IsStreaming, &rec.PricingError,
			&rec.GroupID, &pricingRatio,
			&rec.Timestamp); err != nil {
			continue
		}
		rec.PricingRatio = 1.0
		if pricingRatio != nil {
			rec.PricingRatio = *pricingRatio
		}
		records = append(records, rec)
	}

	result := ReconcileResult{Total: len(records)}
	wr := &WebhookRoutes{
		db:         r.db,
		logger:     r.logger,
		cfg:        r.cfg,
		billingSvc: services.NewBillingService(r.db, r.logger),
	}

	for _, record := range records {
		if err := wr.processRecord(ctx, &record); err != nil {
			r.logger.Warn("Unbilled record reprocessing failed",
				zap.Error(err), zap.String("trace_id", record.TraceID))
			result.Failed++
			continue
		}
		result.Inserted++
	}

	if r.audit != nil {
		r.audit.Log(ctx, c.GetString("user_id"), c.GetString("email"),
			"admin", "unbilled_reconciled", "info",
			fmt.Sprintf("Unbilled reconciliation: %d processed, %d failed", result.Inserted, result.Failed))
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "result": result})
}

// recordExternalInvoice books an invoice that was issued OUTSIDE the portal
// (e.g. a wire-transfer fapiao produced by finance for an enterprise's
// prepaid top-up). It inserts a completed request in the chosen pool so the
// amount consumes eligibility exactly like a portal-issued invoice — this
// prevents double-invoicing when the customer later requests invoices
// through the UI. A matching invoices row keeps the billing history
// consistent, and invoice_source stays 'auto' while the title carries the
// EXTERNAL marker.
func (r *AdminRoutes) recordExternalInvoice(c *gin.Context) {
	var req struct {
		EnterpriseID  string `json:"enterprise_id" binding:"required"`
		Pool          string `json:"pool"` // postpaid | prepaid (default postpaid)
		AmountCents   int64  `json:"amount_cents" binding:"required"`
		InvoiceNumber string `json:"invoice_number"`
		Note          string `json:"note"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}
	pool := req.Pool
	if pool == "" {
		pool = "postpaid"
	}
	if pool != "postpaid" && pool != "prepaid" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "pool must be 'postpaid' or 'prepaid'"}})
		return
	}
	if req.AmountCents <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "amount_cents must be positive"}})
		return
	}

	var entName, entContact string
	if err := r.db.QueryRow(c.Request.Context(),
		`SELECT name, COALESCE(contact_email, '') FROM enterprises WHERE id = $1::uuid AND deleted_at IS NULL`,
		req.EnterpriseID).Scan(&entName, &entContact); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Enterprise not found"}})
		return
	}

	adminID := c.GetString("user_id")
	title := "External invoice (recorded by admin)"
	if req.Note != "" {
		title = "External invoice: " + req.Note
	}
	invoiceNumber := req.InvoiceNumber
	if invoiceNumber == "" {
		invoiceNumber = "EXT-" + time.Now().UTC().Format("20060102150405")
	}

	var requestID string
	err := r.db.WithTx(c.Request.Context(), func(tx pgx.Tx) error {
		if err := tx.QueryRow(c.Request.Context(),
			`INSERT INTO invoice_requests (user_id, enterprise_id, amount, title, status, invoice_number, issued_at, delivery_method, pool)
			 VALUES ($1::uuid, $2::uuid, $3, $4, 'completed', $5, NOW(), 'email', $6)
			 RETURNING id::text`,
			adminID, req.EnterpriseID, req.AmountCents, title, invoiceNumber, pool).Scan(&requestID); err != nil {
			return err
		}
		if _, err := tx.Exec(c.Request.Context(),
			`INSERT INTO invoices (user_id, enterprise_id, invoice_number, amount, status, request_id)
			 VALUES ($1::uuid, $2::uuid, $3, $4, 'issued', $5::uuid)`,
			adminID, req.EnterpriseID, invoiceNumber, req.AmountCents, requestID); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		r.logger.Error("Failed to record external invoice", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to record external invoice"}})
		return
	}

	r.audit.Log(c.Request.Context(), adminID, c.GetString("user_email"), "billing", "external_invoice_recorded", "warning",
		fmt.Sprintf("Recorded external %s invoice %s for %s: $%.2f (%s)", pool, invoiceNumber, entName, float64(req.AmountCents)/100.0, req.Note))
	c.JSON(http.StatusOK, gin.H{"success": true, "id": requestID, "invoice_number": invoiceNumber})
}

// recordStatementPayment records a bank-transfer payment against a monthly
// post-paid statement, for accounts-receivable tracking. It does NOT move any
// balance (post-paid consumption already deducted credit during the month and
// the month-end reset closed the statement); it only records how much of the
// statement the customer has since paid, and writes a tamper-proof ledger row.
// Prepaid statements carry no receivable, so a payment against a period with no
// post-paid consumption is rejected.
//
// POST /api/admin/reconciliation/enterprise/:id/statements/:period/payment
//
//	{ "amount_cents": 99000, "note": "wire 2026-11-05", "replace": false }
//
// amount_cents is added to the already-recorded paid amount (or replaces it
// when replace=true, e.g. to correct a mistake). The recorded total may not
// exceed the statement consumption.
func (r *AdminRoutes) recordStatementPayment(c *gin.Context) {
	enterpriseID := c.Param("id")
	period := c.Param("period")
	var req struct {
		AmountCents int64  `json:"amount_cents"`
		Note        string `json:"note"`
		Replace     bool   `json:"replace"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}
	if req.AmountCents < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "amount_cents cannot be negative"}})
		return
	}
	amountMicro := req.AmountCents * 10000

	adminID := c.GetString("user_id")
	var newPaidMicro int64
	err := r.db.WithTx(c.Request.Context(), func(tx pgx.Tx) error {
		var consumedMicro, paidMicro int64
		if err := tx.QueryRow(c.Request.Context(),
			`SELECT credit_used_micro, COALESCE(paid_micro, 0) FROM enterprise_statements
			 WHERE enterprise_id = $1::uuid AND period = $2 FOR UPDATE`,
			enterpriseID, period).Scan(&consumedMicro, &paidMicro); err != nil {
			return err
		}
		if consumedMicro <= 0 {
			return errStatementNoReceivable
		}
		newPaidMicro = paidMicro + amountMicro
		if req.Replace {
			newPaidMicro = amountMicro
		}
		if newPaidMicro > consumedMicro {
			return errPaymentExceedsStatement
		}
		if _, err := tx.Exec(c.Request.Context(),
			`UPDATE enterprise_statements SET paid_micro = $3, paid_at = NOW(), payment_note = $4
			 WHERE enterprise_id = $1::uuid AND period = $2`,
			enterpriseID, period, newPaidMicro, req.Note); err != nil {
			return err
		}
		_, err := tx.Exec(c.Request.Context(),
			`INSERT INTO enterprise_audit_log (enterprise_id, category, change_type, field_name, action, amount, delta, old_value, new_value, reason, changed_by)
			 VALUES ($1, 'billing', 'statement_payment', 'paid_micro', 'payment', $2, $3, $4, $5, $6, $7::uuid)`,
			enterpriseID, amountMicro, newPaidMicro-paidMicro, paidMicro, newPaidMicro,
			fmt.Sprintf("Statement %s payment: %s", period, req.Note), adminID)
		return err
	})
	if err != nil {
		if errors.Is(err, errStatementNoReceivable) || errors.Is(err, errPaymentExceedsStatement) {
			c.JSON(http.StatusConflict, gin.H{"error": gin.H{"message": err.Error()}})
			return
		}
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Statement not found"}})
			return
		}
		r.logger.Error("Failed to record statement payment", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to record payment"}})
		return
	}
	if r.audit != nil {
		r.audit.Log(c.Request.Context(), adminID, c.GetString("user_email"), "billing", "statement_payment_recorded", "info",
			fmt.Sprintf("Statement %s payment for %s: +$%.2f (total $%.2f)", period, enterpriseID, float64(req.AmountCents)/100.0, float64(newPaidMicro)/1_000_000.0))
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "paid_usd": float64(newPaidMicro) / 1_000_000.0})
}

var errStatementNoReceivable = errors.New("this period has no post-paid consumption, so there is no receivable to pay")

var errPaymentExceedsStatement = errors.New("recorded payment would exceed the statement amount")

package routes

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

// computeNextResetAt returns the next reset timestamp for a billing cycle.
func computeNextResetAt(billingCycle string) *time.Time {
	now := time.Now().UTC()
	var t time.Time
	switch billingCycle {
	case "yearly":
		t = time.Date(now.Year()+1, time.January, 1, 0, 0, 0, 0, time.UTC)
	default:
		t = time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC)
	}
	return &t
}

// getEnterpriseBillingDetails returns enterprise billing data as JSON
func (r *EnterpriseRoutes) getEnterpriseBillingDetails(c *gin.Context) {
	userID := c.GetString("user_id")

	var enterpriseID string
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT enterprise_id::text FROM users WHERE id = $1::uuid AND deleted_at IS NULL`,
		userID,
	).Scan(&enterpriseID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Enterprise not found"}})
		return
	}

	var quotaTotal, quotaUsed, quotaRemaining int64
	var creditTotal, creditUsed, creditRemaining int64
	var billingCycle, displayCurrency string
	var storedExchangeRate float64
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT COALESCE(quota_total, 0), COALESCE(quota_used, 0), COALESCE(quota_total - quota_used, 0),
		        COALESCE(credit_total, 0), COALESCE(credit_used, 0), COALESCE(credit_total - credit_used, 0),
		        COALESCE(billing_cycle, 'monthly'),
		        COALESCE(display_currency, 'USD'), COALESCE(exchange_rate, 1.0)
		 FROM enterprises WHERE id = $1::uuid`,
		enterpriseID,
	).Scan(&quotaTotal, &quotaUsed, &quotaRemaining, &creditTotal, &creditUsed, &creditRemaining, &billingCycle, &displayCurrency, &storedExchangeRate)

	type memberUsage struct {
		UserID   string  `json:"user_id"`
		UserName string  `json:"user_name"`
		Tokens   int64   `json:"tokens"`
		Requests int64   `json:"requests"`
		CostUSD  float64 `json:"cost_usd"` // USD (converted from micro-USD)
	}
	var usageByMember []memberUsage
	rows, err := r.db.Query(c.Request.Context(),
		`SELECT u.id::text, COALESCE(u.name, u.email), SUM(ur.input_tokens + ur.output_tokens), COUNT(*), SUM(ur.total_cost)
		 FROM usage_records ur
		 JOIN api_keys k ON ur.user_api_key_id = k.id
		 JOIN users u ON ur.user_id = u.id
		 WHERE k.enterprise_id = $1::uuid
		 AND ur.created_at >= DATE_TRUNC('month', CURRENT_DATE)
		 GROUP BY u.id, u.name, u.email
		 ORDER BY SUM(ur.total_cost) DESC`,
		enterpriseID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var u memberUsage
			var costMicroUSD int64
			if err := rows.Scan(&u.UserID, &u.UserName, &u.Tokens, &u.Requests, &costMicroUSD); err == nil {
				u.CostUSD = float64(costMicroUSD) / 1_000_000.0
				usageByMember = append(usageByMember, u)
			}
		}
	}

	type inv struct {
		ID              string  `json:"id"`
		AmountUSD       float64 `json:"amount_usd"`
		AmountLocal     float64 `json:"amount_local"`
		DisplayCurrency string  `json:"display_currency"`
		Status          string  `json:"status"`
		CreatedAt       string  `json:"created_at"`
	}
	var invoices []inv
	invRows, err := r.db.Query(c.Request.Context(),
		`SELECT id::text, amount_cents, status, created_at::text
		 FROM invoices WHERE enterprise_id = $1::uuid
		 ORDER BY created_at DESC LIMIT 20`,
		enterpriseID)
	if err == nil {
		defer invRows.Close()
		for invRows.Next() {
			var i inv
			var amountCents int64
			if err := invRows.Scan(&i.ID, &amountCents, &i.Status, &i.CreatedAt); err == nil {
				i.AmountUSD = float64(amountCents) / 100.0
				invoices = append(invoices, i)
			}
		}
	}

	creditTotalUSD := float64(creditTotal) / 1_000_000.0
	creditUsedUSD := float64(creditUsed) / 1_000_000.0
	creditRemainingUSD := float64(creditRemaining) / 1_000_000.0

	exchangeRate := storedExchangeRate
	creditTotalLocal := 0.0
	creditUsedLocal := 0.0
	creditRemainingLocal := 0.0
	if displayCurrency != "" && displayCurrency != "USD" {
		if r.exchange != nil {
			exchangeRate = r.exchange.GetRateOrDefault(c.Request.Context(), "USD", displayCurrency, storedExchangeRate)
		}
		if exchangeRate > 0 {
			creditTotalLocal = creditTotalUSD * exchangeRate
			creditUsedLocal = creditUsedUSD * exchangeRate
			creditRemainingLocal = creditRemainingUSD * exchangeRate
		}
	}

	for i := range invoices {
		if displayCurrency != "" && displayCurrency != "USD" && exchangeRate > 0 {
			invoices[i].AmountLocal = invoices[i].AmountUSD * exchangeRate
		}
		invoices[i].DisplayCurrency = displayCurrency
	}

	// Compute next reset for monthly/yearly cycles when credit_reset_at is unset.
	var nextResetAt *time.Time
	if billingCycle != "" {
		nextResetAt = computeNextResetAt(billingCycle)
	}

	c.JSON(http.StatusOK, gin.H{
		"billing_cycle":    billingCycle,
		"quota_total":      quotaTotal,
		"quota_used":       quotaUsed,
		"quota_remaining":  quotaRemaining,
		"next_reset_at":    nextResetAt,
		"display_currency": displayCurrency,
		"exchange_rate":    exchangeRate,
		"credit": gin.H{
			"total":           creditTotal,
			"used":            creditUsed,
			"remaining":       creditRemaining,
			"total_usd":       creditTotalUSD,
			"used_usd":        creditUsedUSD,
			"remaining_usd":   creditRemainingUSD,
			"total_local":     creditTotalLocal,
			"used_local":      creditUsedLocal,
			"remaining_local": creditRemainingLocal,
		},
		"usage_by_member": usageByMember,
		"invoices":        invoices,
	})
}

// topupEnterprisePool allows system admin to add credit/quota to an enterprise pool
func (r *EnterpriseRoutes) topupEnterprisePool(c *gin.Context) {
	enterpriseID := c.Param("id")
	currentUserID := c.GetString("user_id")

	var req struct {
		QuotaTotal     *int64 `json:"quota_total"`
		CreditTotal    *int64 `json:"credit_total"`
		PrepaidBalance *int64 `json:"prepaid_balance"`
		Reason         string `json:"reason"`
		// IssueInvoice (prepaid top-ups only): record a completed prepaid-pool
		// invoice for this top-up amount in the same transaction — for
		// customers who need the invoice at wire-transfer time. Without it the
		// amount stays in the事后申请 pool (design doc §prepaid 开票模式 A).
		IssueInvoice bool   `json:"issue_invoice"`
		InvoiceTitle string `json:"invoice_title"`
		// RecordOnly (prepaid only): book a deposit that already reached the
		// wallet (e.g. funded by a direct DB edit before the ledger existed)
		// WITHOUT changing prepaid_balance. EffectiveAt is when the money
		// actually arrived; it dates the audit row so monthly statements and
		// reconciliation attribute it to the right period.
		RecordOnly  bool       `json:"record_only"`
		EffectiveAt *time.Time `json:"effective_at"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}
	if req.RecordOnly {
		if msg := validateRecordOnly(req.PrepaidBalance, req.CreditTotal, req.QuotaTotal, req.IssueInvoice, req.Reason, req.EffectiveAt); msg != "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": msg}})
			return
		}
	}

	// Validate: reject negative values
	if req.QuotaTotal != nil && *req.QuotaTotal < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "quota_total cannot be negative"}})
		return
	}
	if req.CreditTotal != nil && *req.CreditTotal < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "credit_total cannot be negative"}})
		return
	}
	if req.PrepaidBalance != nil && *req.PrepaidBalance < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "prepaid_balance cannot be negative"}})
		return
	}

	// Verify enterprise exists and is not soft-deleted
	var exists bool
	existErr := r.db.QueryRow(c.Request.Context(),
		`SELECT true FROM enterprises WHERE id = $1::uuid AND deleted_at IS NULL`,
		enterpriseID,
	).Scan(&exists)
	if existErr != nil || !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Enterprise not found or has been deleted"}})
		return
	}

	if !req.RecordOnly && req.QuotaTotal != nil && *req.QuotaTotal > 0 {
		if err := r.db.WithTx(c.Request.Context(), func(tx pgx.Tx) error {
			var oldQuota int64
			if err := tx.QueryRow(c.Request.Context(),
				`SELECT COALESCE(quota_total, 0) FROM enterprises WHERE id = $1::uuid FOR UPDATE`,
				enterpriseID).Scan(&oldQuota); err != nil {
				return err
			}
			newQuota := oldQuota + *req.QuotaTotal
			if _, err := tx.Exec(c.Request.Context(),
				`UPDATE enterprises SET quota_total = $1, updated_at = NOW() WHERE id = $2::uuid`,
				newQuota, enterpriseID); err != nil {
				return err
			}
			if _, err := tx.Exec(c.Request.Context(),
				`INSERT INTO enterprise_audit_log (enterprise_id, category, change_type, field_name, action, amount, delta, old_value, new_value, reason, changed_by)
				 VALUES ($1, 'billing', 'quota_topup', 'quota_total', 'topup', $2, $2, $3, $4, $5, $6::uuid)`,
				enterpriseID, *req.QuotaTotal, oldQuota, newQuota, req.Reason, currentUserID); err != nil {
				return err
			}
			return nil
		}); err != nil {
			r.logger.Error("Failed to top up enterprise quota", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to top up enterprise quota"}})
			return
		}
	}
	if req.CreditTotal != nil && *req.CreditTotal > 0 {
		if err := r.db.WithTx(c.Request.Context(), func(tx pgx.Tx) error {
			var oldCredit int64
			if err := tx.QueryRow(c.Request.Context(),
				`SELECT COALESCE(credit_total, 0) FROM enterprises WHERE id = $1::uuid FOR UPDATE`,
				enterpriseID).Scan(&oldCredit); err != nil {
				return err
			}
			if req.RecordOnly {
				return r.recordDeposit(c.Request.Context(), tx, enterpriseID, currentUserID, "credit_topup", "credit_total", oldCredit, *req.CreditTotal, *req.EffectiveAt, req.Reason)
			}
			newCredit := oldCredit + *req.CreditTotal
			if _, err := tx.Exec(c.Request.Context(),
				`UPDATE enterprises SET credit_total = $1, updated_at = NOW() WHERE id = $2::uuid`,
				newCredit, enterpriseID); err != nil {
				return err
			}
			if _, err := tx.Exec(c.Request.Context(),
				`INSERT INTO enterprise_audit_log (enterprise_id, category, change_type, field_name, action, amount, delta, old_value, new_value, reason, changed_by)
				 VALUES ($1, 'billing', 'credit_topup', 'credit_total', 'topup', $2, $2, $3, $4, $5, $6::uuid)`,
				enterpriseID, *req.CreditTotal, oldCredit, newCredit, req.Reason, currentUserID); err != nil {
				return err
			}
			return nil
		}); err != nil {
			if errors.Is(err, errRecordBelowBalance) || errors.Is(err, errRecordDuplicate) {
				c.JSON(http.StatusConflict, gin.H{"error": gin.H{"message": err.Error()}})
				return
			}
			r.logger.Error("Failed to top up enterprise credit", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to top up enterprise credit"}})
			return
		}
	}

	if req.PrepaidBalance != nil && *req.PrepaidBalance > 0 {
		if req.IssueInvoice && req.InvoiceTitle == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "invoice_title is required when issue_invoice is set"}})
			return
		}
		if err := r.db.WithTx(c.Request.Context(), func(tx pgx.Tx) error {
			var oldPrepaid int64
			if err := tx.QueryRow(c.Request.Context(),
				`SELECT COALESCE(prepaid_balance, 0) FROM enterprises WHERE id = $1::uuid FOR UPDATE`,
				enterpriseID).Scan(&oldPrepaid); err != nil {
				return err
			}
			if req.RecordOnly {
				return r.recordDeposit(c.Request.Context(), tx, enterpriseID, currentUserID, "prepaid_topup", "prepaid_balance", oldPrepaid, *req.PrepaidBalance, *req.EffectiveAt, req.Reason)
			}
			newPrepaid := oldPrepaid + *req.PrepaidBalance
			if _, err := tx.Exec(c.Request.Context(),
				`UPDATE enterprises SET prepaid_balance = $1, updated_at = NOW() WHERE id = $2::uuid`,
				newPrepaid, enterpriseID); err != nil {
				return err
			}
			if _, err := tx.Exec(c.Request.Context(),
				`INSERT INTO enterprise_audit_log (enterprise_id, category, change_type, field_name, action, amount, delta, old_value, new_value, reason, changed_by)
				 VALUES ($1, 'billing', 'prepaid_topup', 'prepaid_balance', 'topup', $2, $2, $3, $4, $5, $6::uuid)`,
				enterpriseID, *req.PrepaidBalance, oldPrepaid, newPrepaid, req.Reason, currentUserID); err != nil {
				return err
			}
			// Mode A: invoice issued together with the top-up. The amount
			// leaves the事后申请 eligible pool immediately (pool='prepaid',
			// status='completed').
			if req.IssueInvoice {
				invoiceNo := fmt.Sprintf("INV-PP-%s-%d", enterpriseID[:8], time.Now().Unix())
				amountCents := *req.PrepaidBalance / 10000 // micro-USD → cents
				if _, err := tx.Exec(c.Request.Context(),
					`INSERT INTO invoice_requests (user_id, enterprise_id, amount, title, status, invoice_number, issued_at, pool)
					 VALUES ($1::uuid, $2::uuid, $3, $4, 'completed', $5, NOW(), 'prepaid')`,
					currentUserID, enterpriseID, amountCents, req.InvoiceTitle, invoiceNo); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			if errors.Is(err, errRecordBelowBalance) || errors.Is(err, errRecordDuplicate) {
				c.JSON(http.StatusConflict, gin.H{"error": gin.H{"message": err.Error()}})
				return
			}
			r.logger.Error("Failed to top up enterprise prepaid balance", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to top up enterprise prepaid balance"}})
			return
		}
	}

	if r.audit != nil {
		r.audit.Log(c.Request.Context(), currentUserID, c.GetString("email"), "enterprise", "enterprise_topup", "info",
			fmt.Sprintf("Enterprise %s topped up: quota=%v, credit=%v, prepaid=%v, reason=%s", enterpriseID, req.QuotaTotal, req.CreditTotal, req.PrepaidBalance, req.Reason))
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ResetMonthlyBilling resets quota_used and credit_used for enterprises with billing_cycle = 'monthly'
// at the start of each calendar month (1st of month). Should be called by cron on the 1st.
//
// Idempotency guard: a successful reset stores NEXT month's 1st in
// credit_reset_at, so "already reset this month" is detected by comparing
// against the FIRST day of NEXT month. The previous predicate
// (credit_reset_at < current month's 1st) broke on reset day itself: after
// the Oct 1 reset stored Nov 1, the Nov 1 run computed Nov 1 < Nov 1 = false
// and skipped — while Oct 1 had run because Sep 1 < Oct 1 was true. The
// result was alternating reset/skip months (observed: resets on Aug 1 and
// Oct 1, silent skips on Sep 1 and Nov 1). With the next-month comparison:
// Nov 1 stored value (Nov 1) < Dec 1 → reset; any later Nov 1 run sees
// Nov 1 < Dec 1 still true for OTHER enterprises pending, but a re-run for
// an already-reset enterprise finds credit_reset_at = Dec 1 and Dec 1 < Dec
// 1 = false → skip. Idempotent.
//
// S-U8 fix: quota_limit / credit_limit on enterprise_members are MONTHLY caps
// gated against users.quota_used / users.credit_used by /api/apikeys/validate.
// Previously ResetMonthlyBilling only zeroed the enterprise pool and the
// members' quota_total/quota_remaining — the members' running quota_used /
// credit_used counters were never reset, so any member who hit last month's
// cap stayed permanently blocked from the second calendar month onward. The
// reset now atomically snapshots each member's pre-reset quota_used /
// credit_used into enterprise_audit_log (category 'billing',
// change_type 'monthly_member_reset_snapshot') and then zeros those counters
// in the same transaction, preserving audit history without locking the gate
// to lifetime accumulation.
func (r *EnterpriseRoutes) ResetMonthlyBilling(ctx context.Context) {
	now := time.Now()
	// Only run on the 1st of each month (cron may drift, guard here)
	if now.Day() != 1 {
		r.logger.Info("Skipping monthly billing reset — not the 1st of the month",
			zap.Int("day", now.Day()))
		return
	}

	nextReset := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, now.Location())

	rows, err := r.db.Query(ctx, `
		SELECT id::text, name
		FROM enterprises
		WHERE status = 'active'
		  AND billing_cycle = 'monthly'
		  AND deleted_at IS NULL
		  AND (credit_reset_at IS NULL OR credit_reset_at < DATE_TRUNC('month', NOW() + INTERVAL '1 month'))`)
	if err != nil {
		r.logger.Error("Failed to query enterprises for billing reset", zap.Error(err))
		return
	}
	defer rows.Close()

	var resetCount int
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			continue
		}

		// Snapshot pre-reset member counters BEFORE zeroing them, so the
		// monthly audit history is preserved without the counters themselves
		// accumulating across months and permanently blocking members.
		memberRows, mErr := r.db.Query(ctx, `
			SELECT id::text, COALESCE(quota_used, 0), COALESCE(credit_used, 0)
			  FROM users
			 WHERE enterprise_id = $1::uuid AND deleted_at IS NULL
			   AND (COALESCE(quota_used, 0) > 0 OR COALESCE(credit_used, 0) > 0)`,
			id,
		)
		if mErr != nil {
			r.logger.Error("Failed to query member counters for monthly snapshot",
				zap.String("enterprise_id", id), zap.Error(mErr))
			// Continue anyway — the reset is the priority; losing the audit
			// snapshot for a single enterprise does not block the monthly gate.
		} else {
			for memberRows.Next() {
				var memberID string
				var prevQuotaUsed, prevCreditUsed int64
				if err := memberRows.Scan(&memberID, &prevQuotaUsed, &prevCreditUsed); err != nil {
					continue
				}
				if err := r.db.Exec(ctx,
					`INSERT INTO enterprise_audit_log
					     (enterprise_id, user_id, category, change_type, field_name, action, old_value, new_value, reason)
					 VALUES ($1, $2::uuid, 'billing', 'monthly_member_reset_snapshot',
					         'quota_used,credit_used', 'reset', $3::bigint, 0,
					         'Pre-reset snapshot: quota_used='||$3::bigint||' credit_used='||$4::bigint)`,
					id, memberID, prevQuotaUsed, prevCreditUsed); err != nil {
					r.logger.Error("Failed to snapshot member counter",
						zap.String("enterprise_id", id), zap.String("user_id", memberID), zap.Error(err))
				}
			}
			memberRows.Close()
		}

		statementInserted, period, creditUsedMicro, err := r.resetOneEnterpriseBilling(ctx, id, nextReset)
		if err != nil {
			r.logger.Error("Failed to reset enterprise billing", zap.String("id", id), zap.Error(err))
			continue
		}

		resetCount++
		r.logger.Info("Enterprise billing reset", zap.String("id", id), zap.String("name", name))

		// Notify admins about the freshly closed month's statement exactly
		// once (same-day re-runs insert no statement row and notify nothing).
		if statementInserted && r.notif != nil {
			r.notif.NotifyEnterpriseAdmins(ctx, id, "enterprise_monthly_statement", "normal",
				"notif.enterprise_monthly_statement.title", "notif.enterprise_monthly_statement.body",
				map[string]string{
					"enterprise": name,
					"period":     period,
					"amount":     fmt.Sprintf("%.2f", float64(creditUsedMicro)/1_000_000.0),
				},
				"/enterprise/billing", "enterprise", id)
		}
	}

	if resetCount > 0 {
		r.logger.Info("Monthly billing reset completed", zap.Int("enterprises_reset", resetCount))
	}
}

// resetOneEnterpriseBilling snapshots the monthly statement (P1), then
// atomically zeroes the enterprise pool counters (quota_used, credit_used,
// credit_reset_at) and every active member's (quota_total, quota_remaining,
// quota_used, credit_used). The member pre-reset counters were already
// snapshotted into enterprise_audit_log by the caller, so audit history is
// preserved while the running monthly gate counters are cleared (S-U8).
// prepaid_balance is a prepayment wallet, not a monthly counter: it carries
// across resets and is never touched here. The returned bool reports whether
// a NEW statement row was inserted (false on same-day re-runs), so the
// caller notifies the admins exactly once. Also returns the closed statement
// period and its postpaid consumption so the caller can build the notice
// without a second query.
func (r *EnterpriseRoutes) resetOneEnterpriseBilling(ctx context.Context, id string, nextReset time.Time) (inserted bool, period string, creditUsedMicro int64, err error) {
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return false, "", 0, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // safe: Commit first makes Rollback a no-op.

	// Statement period = the month being closed (reset runs on the 1st).
	period = time.Now().AddDate(0, 0, -1).Format("2006-01")

	// Lock the row and read pre-reset counters BEFORE zeroing them.
	var quotaUsed, prepaidEnd int64
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(credit_used, 0), COALESCE(quota_used, 0), COALESCE(prepaid_balance, 0)
		 FROM enterprises WHERE id = $1::uuid FOR UPDATE`,
		id).Scan(&creditUsedMicro, &quotaUsed, &prepaidEnd); err != nil {
		return false, "", 0, fmt.Errorf("read pre-reset counters: %w", err)
	}
	var prepaidTopup int64
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(SUM(delta), 0) FROM enterprise_audit_log
		 WHERE enterprise_id = $1::uuid AND change_type = 'prepaid_topup' AND field_name = 'prepaid_balance'
		   AND created_at >= DATE_TRUNC('month', NOW()) - INTERVAL '1 month'
		   AND created_at < DATE_TRUNC('month', NOW())`,
		id).Scan(&prepaidTopup); err != nil {
		return false, "", 0, fmt.Errorf("read prepaid top-ups: %w", err)
	}

	stmtTag, err := tx.Exec(ctx,
		`INSERT INTO enterprise_statements
		     (enterprise_id, period, credit_used_micro, quota_used, prepaid_topup_micro, prepaid_balance_end_micro)
		 VALUES ($1::uuid, $2, $3, $4, $5, $6)
		 ON CONFLICT (enterprise_id, period) DO NOTHING`,
		id, period, creditUsedMicro, quotaUsed, prepaidTopup, prepaidEnd)
	if err != nil {
		return false, "", 0, fmt.Errorf("insert monthly statement: %w", err)
	}
	statementInserted := stmtTag.RowsAffected() > 0

	if _, err := tx.Exec(ctx,
		`UPDATE enterprises SET quota_used = 0, credit_used = 0, credit_reset_at = $2, updated_at = NOW()
		 WHERE id = $1::uuid`,
		id, nextReset,
	); err != nil {
		return false, "", 0, fmt.Errorf("reset enterprise pool: %w", err)
	}
	// Reclaim per-member quota balances (so they cannot be re-spent against
	// the freshly-reset enterprise pool) AND zero the running quota_used /
	// credit_used counters gated monthly by /api/apikeys/validate (S-U8).
	if _, err := tx.Exec(ctx,
		`UPDATE users SET quota_total = 0, quota_remaining = 0,
		                  quota_used = 0, credit_used = 0, updated_at = NOW()
		 WHERE enterprise_id = $1::uuid AND deleted_at IS NULL`,
		id,
	); err != nil {
		return false, "", 0, fmt.Errorf("reset member counters: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO enterprise_audit_log (enterprise_id, category, change_type, field_name, action, amount, reason)
		 VALUES ($1, 'billing', 'monthly_reset', 'quota_used,credit_used', 'reset', 0, 'Calendar month reset')`,
		id); err != nil {
		return false, "", 0, fmt.Errorf("log reset: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, "", 0, fmt.Errorf("commit: %w", err)
	}
	return statementInserted, period, creditUsedMicro, nil
}

var errRecordDuplicate = errors.New("an identical deposit (same amount and effective_at) is already recorded")

var errRecordBelowBalance = errors.New("recorded deposits would be lower than the current balance; refusing to book an inconsistent ledger")

// recordDeposit books a historical prepaid or credit deposit in
// enterprise_audit_log (what the ledger reconciliation and prepaid-invoice
// eligibility are computed from) WITHOUT touching the balance column. The
// change_type/field_name select which ledger (prepaid_topup/prepaid_balance or
// credit_topup/credit_total). old_value/new_value carry the running total of
// recorded deposits of that type. created_at is the effective date so the row
// lands in the right statement period. It refuses when the recorded total would
// fall below the current balance, and rejects an exact replay (same amount and
// effective_at) so a deposit cannot be counted twice.
func (r *EnterpriseRoutes) recordDeposit(ctx context.Context, tx pgx.Tx, enterpriseID, userID, changeType, fieldName string, balance, amount int64, effectiveAt time.Time, reason string) error {
	var dup bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM enterprise_audit_log
		  WHERE enterprise_id = $1::uuid AND change_type = $2 AND field_name = $3
		    AND delta = $4 AND created_at = $5)`,
		enterpriseID, changeType, fieldName, amount, effectiveAt.UTC()).Scan(&dup); err != nil {
		return err
	}
	if dup {
		return errRecordDuplicate
	}
	var prior int64
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(SUM(delta), 0) FROM enterprise_audit_log
		  WHERE enterprise_id = $1::uuid AND change_type = $2 AND field_name = $3`,
		enterpriseID, changeType, fieldName).Scan(&prior); err != nil {
		return err
	}
	if prior+amount < balance {
		return errRecordBelowBalance
	}
	_, err := tx.Exec(ctx,
		`INSERT INTO enterprise_audit_log (enterprise_id, category, change_type, field_name, action, amount, delta, old_value, new_value, reason, changed_by, created_at)
		 VALUES ($1, 'billing', $2, $3, 'topup', $4, $4, $5, $6, $7, $8::uuid, $9)`,
		enterpriseID, changeType, fieldName, amount, prior, prior+amount, "[record-only, balance unchanged] "+reason, userID, effectiveAt.UTC())
	return err
}

// validateRecordOnly returns a client-facing message for an invalid record_only
// request, or "" when it is acceptable. record_only books a historical deposit
// into the ledger WITHOUT changing a balance; it applies to exactly one of
// prepaid_balance or credit_total, never quota, and never issues an invoice.
func validateRecordOnly(prepaid, credit, quota *int64, issueInvoice bool, reason string, effectiveAt *time.Time) string {
	pos := func(p *int64) bool { return p != nil && *p > 0 }
	switch {
	case quota != nil && *quota != 0:
		return "record_only cannot be combined with quota_total"
	case pos(prepaid) == pos(credit):
		return "record_only requires exactly one of a positive prepaid_balance or credit_total"
	case issueInvoice:
		return "record_only cannot issue an invoice"
	case strings.TrimSpace(reason) == "":
		return "record_only requires a reason"
	case effectiveAt == nil || !effectiveAt.Before(time.Now()):
		return "record_only requires effective_at in the past"
	}
	return ""
}

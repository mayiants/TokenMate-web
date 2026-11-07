package routes

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mayiants/ai-portal-go/internal/services"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

// processBillingQueue pops records from the billing queue, groups by user_id, and processes
// balance deduction in batched transactions.
func (r *WebhookRoutes) processBillingQueue() {
	ctx := context.Background()
	var records []*UsageRecord

	for i := 0; i < 100; i++ {
		data, err := r.db.Redis.RPop(ctx, "webhook:billing:queue").Result()
		if err != nil {
			break
		}

		var record UsageRecord
		if err := json.Unmarshal([]byte(data), &record); err != nil {
			r.logger.Warn("Failed to unmarshal billing record", zap.Error(err))
			continue
		}
		records = append(records, &record)
	}

	if len(records) == 0 {
		return
	}

	// Trace-level idempotency: proxy retries can push the same record into the
	// queue twice, and usage_records dedup (ON CONFLICT) does not protect the
	// deduction itself. Skip records whose trace_id was already billed.
	records, markers := r.markBillingRecords(ctx, records)
	if len(records) == 0 {
		return
	}

	// Group records by user_id for batch deduction
	userRecords := make(map[string][]*UsageRecord)
	for _, rec := range records {
		if rec.APIKeyHash == "" {
			r.logger.Warn("Billing record has no API key hash, moving to dead-letter",
				zap.String("trace_id", rec.TraceID))
			data, _ := json.Marshal(rec)
			_ = r.db.Redis.RPush(ctx, "webhook:billing:dead", data).Err()
			continue
		}
		// We need user_id — look it up first. For assigned enterprise keys the
		// actual billing user is assigned_user_id, not the key owner (user_id).
		var userID string
		_ = r.db.QueryRow(ctx,
			`SELECT COALESCE(assigned_user_id, user_id)::text FROM api_keys WHERE key_hash = $1 AND deleted_at IS NULL LIMIT 1`,
			rec.APIKeyHash,
		).Scan(&userID)
		if userID != "" {
			userRecords[userID] = append(userRecords[userID], rec)
		} else {
			// Key not found — usage is already in DB but will never be charged.
			// Push to dead-letter with a Warn log so it can be investigated.
			r.logger.Warn("Billing record: API key not found, moving to dead-letter",
				zap.String("api_key_hash", rec.APIKeyHash),
				zap.String("trace_id", rec.TraceID))
			data, _ := json.Marshal(rec)
			_ = r.db.Redis.RPush(ctx, "webhook:billing:dead", data).Err()
		}
	}

	// Process each user's records in a single transaction
	for userID, recs := range userRecords {
		if err := r.processBillingBatch(ctx, userID, recs); err != nil {
			r.recordWebhookFailed()
			r.logger.Error("Failed to process billing batch",
				zap.Error(err), zap.String("user_id", userID), zap.Int("count", len(recs)))
			// Re-queue failed records with retry limit. Release the idempotency
			// markers of re-queued records so the retry is not skipped as a
			// duplicate; dead-lettered records keep theirs.
			for _, rec := range recs {
				if rec.RetryCount >= 3 {
					data, _ := json.Marshal(rec)
					_ = r.db.Redis.RPush(ctx, "webhook:billing:dead", data).Err()
					r.logger.Warn("Billing record exceeded max retries, moved to dead queue",
						zap.String("api_key_hash", rec.APIKeyHash), zap.Int("retry_count", rec.RetryCount))
					continue
				}
				r.releaseBillingMarker(ctx, markers, rec)
				rec.RetryCount++
				data, _ := json.Marshal(rec)
				_ = r.db.Redis.RPush(ctx, "webhook:billing:queue", data).Err()
			}
		}
	}
}

// billingDedupTTL bounds how long a billed trace_id is remembered. Proxy
// retries/replays arrive within minutes; 24h is ample.
const billingDedupTTL = 24 * time.Hour

// markBillingRecords applies trace-level idempotency markers for records about
// to be billed and returns the records that still need billing, plus the
// markers keyed by record so they can be released if billing fails and the
// record is re-queued. Fails open on Redis errors: without markers we bill as
// before rather than dropping charges.
func (r *WebhookRoutes) markBillingRecords(ctx context.Context, records []*UsageRecord) (kept []*UsageRecord, markers map[*UsageRecord]string) {
	if r.db.Redis == nil {
		return records, nil
	}
	markers = make(map[*UsageRecord]string, len(records))
	kept = make([]*UsageRecord, 0, len(records))
	for _, rec := range records {
		if rec.TraceID == "" {
			kept = append(kept, rec)
			continue
		}
		marker := "webhook:billing:seen:" + rec.TraceID
		ok, err := r.db.Redis.SetNX(ctx, marker, 1, billingDedupTTL).Result()
		if err != nil {
			kept = append(kept, rec) // fail-open without dedup
			continue
		}
		if !ok {
			r.logger.Warn("Skipping duplicate billing record", zap.String("trace_id", rec.TraceID))
			continue
		}
		markers[rec] = marker
		kept = append(kept, rec)
	}
	return kept, markers
}

// releaseBillingMarker deletes the idempotency marker of a record that failed
// billing and is being re-queued, so the retry is not skipped as a duplicate.
func (r *WebhookRoutes) releaseBillingMarker(ctx context.Context, markers map[*UsageRecord]string, rec *UsageRecord) {
	if r.db.Redis == nil || markers == nil {
		return
	}
	if m, ok := markers[rec]; ok {
		_ = r.db.Redis.Del(ctx, m).Err()
	}
}

// billRecordsDirectly bills records synchronously, bypassing the billing
// queue. It is the fallback for when queueing to Redis fails after the usage
// records were already persisted — without it those records would never be
// charged. Records that still fail are moved to the dead-letter queue.
func (r *WebhookRoutes) billRecordsDirectly(ctx context.Context, records []*UsageRecord) {
	if len(records) == 0 {
		return
	}
	records, markers := r.markBillingRecords(ctx, records)
	userRecords := make(map[string][]*UsageRecord)
	for _, rec := range records {
		if rec.APIKeyHash == "" {
			continue
		}
		// For assigned enterprise keys the billing user is assigned_user_id.
		var userID string
		_ = r.db.QueryRow(ctx,
			`SELECT COALESCE(assigned_user_id, user_id)::text FROM api_keys WHERE key_hash = $1 AND deleted_at IS NULL LIMIT 1`,
			rec.APIKeyHash,
		).Scan(&userID)
		if userID == "" {
			continue
		}
		userRecords[userID] = append(userRecords[userID], rec)
	}
	for userID, recs := range userRecords {
		if err := r.processBillingBatch(ctx, userID, recs); err != nil {
			r.recordWebhookFailed()
			r.logger.Error("Direct billing fallback failed",
				zap.Error(err), zap.String("user_id", userID), zap.Int("count", len(recs)))
			if r.db.Redis != nil {
				for _, rec := range recs {
					r.releaseBillingMarker(ctx, markers, rec)
					data, _ := json.Marshal(rec)
					_ = r.db.Redis.RPush(ctx, "webhook:billing:dead", data).Err()
				}
			}
		}
	}
}

// processBillingBatch handles balance deduction for multiple records of the same user
// in a single transaction. This is the heavy path that can be batched.
func (r *WebhookRoutes) processBillingBatch(ctx context.Context, userID string, records []*UsageRecord) error {
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// 1. Calculate model-pricing costs in one batch lookup per (provider, region, model, minute).
	serviceRecords := make([]services.UsageRecord, len(records))
	for i, record := range records {
		serviceRecords[i] = services.UsageRecord(*record)
	}
	baseCosts := r.billingSvc.CalculateCostBatch(ctx, serviceRecords)

	// Batch resolve group IDs and key types for all records that need a
	// pricing ratio or key-type-aware deduction. Key type MUST be resolved
	// per key hash: a user may own general and enterprise keys at the same
	// time, and picking an arbitrary key of the user misroutes the whole
	// batch (previously enterprise usage was charged to personal balance).
	keyHashes := make(map[string]struct{})
	for _, record := range records {
		if record.APIKeyHash == "" {
			continue
		}
		keyHashes[record.APIKeyHash] = struct{}{}
	}
	type keyInfo struct {
		groupID      string
		keyType      string
		enterpriseID string
	}
	keyInfoByHash := make(map[string]keyInfo)
	if len(keyHashes) > 0 {
		hashes := make([]string, 0, len(keyHashes))
		for h := range keyHashes {
			hashes = append(hashes, h)
		}
		rows, err := tx.Query(ctx,
			`SELECT k.key_hash, COALESCE(k.group_id::text, COALESCE(u.group_id::text, '')),
			        COALESCE(k.key_type, 'general'),
			        COALESCE(k.enterprise_id::text, COALESCE(u.enterprise_id::text, ''))
			 FROM api_keys k JOIN users u ON u.id = COALESCE(k.assigned_user_id, k.user_id)
			 WHERE k.key_hash = ANY($1) AND k.deleted_at IS NULL`,
			hashes)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var hash string
				var info keyInfo
				if err := rows.Scan(&hash, &info.groupID, &info.keyType, &info.enterpriseID); err == nil {
					keyInfoByHash[hash] = info
				}
			}
		}
	}

	groupIDs := make(map[string]struct{})
	for _, info := range keyInfoByHash {
		if info.groupID != "" {
			groupIDs[info.groupID] = struct{}{}
		}
	}
	groupIDList := make([]string, 0, len(groupIDs))
	for id := range groupIDs {
		groupIDList = append(groupIDList, id)
	}
	ratioMap := r.billingSvc.GetPricingRatioBatch(ctx, groupIDList)

	// Enterprise multipliers for the fallback path: the proxy-sent ratio
	// already folds in the enterprise factor, so this only applies when a
	// record arrives without one.
	entRatioMap := make(map[string]float64)
	entIDs := make([]string, 0, len(keyInfoByHash))
	for _, info := range keyInfoByHash {
		if info.keyType == "enterprise" && info.enterpriseID != "" {
			entIDs = append(entIDs, info.enterpriseID)
		}
	}
	if len(entIDs) > 0 {
		rows, err := tx.Query(ctx,
			`SELECT id::text, COALESCE(pricing_ratio, 1.0) FROM enterprises WHERE id = ANY($1::uuid[])`,
			entIDs)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var id string
				var ratio float64
				if rows.Scan(&id, &ratio) == nil {
					entRatioMap[id] = ratio
				}
			}
		}
	}

	// Prefer total_cost already stored in usage_records by the stats pipeline
	// over recalculating. This ensures billing always matches what the user
	// sees in usage history, even if pricing changes between stats and billing.
	dbCosts := make(map[string]int64, len(records))
	traceIDs := make([]string, 0, len(records))
	for _, record := range records {
		traceIDs = append(traceIDs, record.TraceID)
	}
	if len(traceIDs) > 0 {
		costRows, err := tx.Query(ctx,
			`SELECT trace_id, COALESCE(total_cost, 0) FROM usage_records WHERE trace_id = ANY($1)`,
			traceIDs)
		if err == nil {
			defer costRows.Close()
			for costRows.Next() {
				var tid string
				var tc int64
				if costRows.Scan(&tid, &tc) == nil {
					dbCosts[tid] = tc
				}
			}
		}
	}

	// Apply pricing ratios and compute total cost.
	totalCost := int64(0)
	recordCosts := make([]int64, len(records))
	for i, record := range records {
		// Use DB-stored total_cost as the primary source; fall back to
		// recalculated cost only if the DB value is 0 (new/unknown record).
		if dbCost, ok := dbCosts[record.TraceID]; ok && dbCost > 0 {
			recordCosts[i] = dbCost
			totalCost += dbCost
			continue
		}

		cost := baseCosts[i]
		pricingRatio := 1.0
		info := keyInfoByHash[record.APIKeyHash]
		if record.PricingRatio > 0 {
			pricingRatio = record.PricingRatio
		} else {
			if gid := info.groupID; gid != "" {
				if ratio, ok := ratioMap[gid]; ok && ratio > 0 {
					pricingRatio = ratio
				}
			}
			if info.keyType == "enterprise" {
				if entRatio, ok := entRatioMap[info.enterpriseID]; ok && entRatio > 0 {
					pricingRatio = pricingRatio * entRatio
				}
			}
		}
		cost = int64(float64(cost) * pricingRatio)
		recordCosts[i] = cost
		totalCost += cost
	}

	// Refund records (status=refund): an async media task was charged on
	// dispatch (upstream provider bills at task creation) and then failed.
	// The recomputed cost mirrors the original dispatch charge; partition
	// these records out of the charge path and settle them as credits below.
	refundIdxSet := make(map[int]struct{})
	var refundTotal int64    // personal-key refunds → balance credit
	var entRefundTotal int64 // enterprise-key refunds → release credit_used
	for i, record := range records {
		if record.Status != "refund" || recordCosts[i] <= 0 {
			continue
		}
		refundIdxSet[i] = struct{}{}
		if keyInfoByHash[record.APIKeyHash].keyType == "enterprise" {
			entRefundTotal += recordCosts[i]
		} else {
			refundTotal += recordCosts[i]
		}
		recordCosts[i] = -recordCosts[i]
	}

	// 2. Partition records by key type (resolved per key hash above).
	// Enterprise-key records bill the enterprise ONLY — never personal
	// gift/balance; subscription-key records use subscription quota first;
	// general/economy records use gift/balance. Mixing key types in one
	// batch is common, so each partition is deducted independently.
	var enterpriseIdx []int
	var enterpriseCost, subscriptionCost, otherCost int64
	var entEnterpriseID string
	for i, record := range records {
		if _, ok := refundIdxSet[i]; ok {
			continue // refunds are settled as credits, never as charges
		}
		info := keyInfoByHash[record.APIKeyHash]
		switch info.keyType {
		case "enterprise":
			enterpriseIdx = append(enterpriseIdx, i)
			enterpriseCost += recordCosts[i]
			if entEnterpriseID == "" {
				entEnterpriseID = info.enterpriseID
			}
		case "subscription":
			subscriptionCost += recordCosts[i]
		default:
			otherCost += recordCosts[i]
		}
	}

	// Build a set of enterprise-record indices for O(1) partition lookups.
	enterpriseIdxSet := make(map[int]struct{}, len(enterpriseIdx))
	for _, i := range enterpriseIdx {
		enterpriseIdxSet[i] = struct{}{}
	}

	// 3. Lock user row and get all balances
	var giftBalance, totalBalance int64
	// Captured for post-commit quota_warning notifications (see below).
	var quotaWarnSubID string
	var quotaWarnTotal, quotaWarnRemaining int64
	var enterpriseID string
	err = tx.QueryRow(ctx,
		`SELECT COALESCE(gift_balance, 0), COALESCE(balance, 0),
		        COALESCE(enterprise_id::text, '')
		 FROM users WHERE id = $1::uuid FOR UPDATE`,
		userID,
	).Scan(&giftBalance, &totalBalance, &enterpriseID)
	if err != nil {
		return fmt.Errorf("lock user: %w", err)
	}

	// 5. Deduct based on key type
	var giftDeduction, balanceDeduction int64
	var creditDeduction int64
	var subscriptionDeduction int64
	var subscriptionDeductionCost int64

	// 5a. Enterprise-key records: enterprise credit ONLY, with 110% cap.
	// No personal gift/balance is touched for enterprise usage.
	if enterpriseCost > 0 {
		if entEnterpriseID == "" {
			entEnterpriseID = enterpriseID
		}
		if entEnterpriseID != "" {
			var prepaidBalance, creditTotal, creditUsed int64
			// NOTE: no FOR UPDATE here — the 110% buffer absorbs the small
			// cap-check race, and row-locking enterprises deadlocks with the
			// FK key-share taken by enterprise_alerts inserts from
			// checkEnterpriseLimits (which uses a separate pool connection).
			err = tx.QueryRow(ctx,
				`SELECT COALESCE(prepaid_balance, 0), COALESCE(credit_total, 0), COALESCE(credit_used, 0)
				 FROM enterprises WHERE id = $1::uuid`,
				entEnterpriseID,
			).Scan(&prepaidBalance, &creditTotal, &creditUsed)
			if err == nil && (prepaidBalance > 0 || creditTotal > 0) {
				// 1) Prepaid wallet first: d1 = min(cost, prepaidBalance).
				// Prepaid-first is a product decision; refunds unwind in the
				// reverse order (allowance → prepaid, LIFO).
				remaining := enterpriseCost
				if prepaidBalance > 0 {
					prepaidDeduction := remaining
					if prepaidBalance < prepaidDeduction {
						prepaidDeduction = prepaidBalance
					}
					if _, err := tx.Exec(ctx, `UPDATE enterprises SET prepaid_balance = prepaid_balance - $1, updated_at = NOW()
											 WHERE id = $2::uuid`, prepaidDeduction, entEnterpriseID); err != nil {
						return fmt.Errorf("database operation failed: %w", err)
					}
					remaining -= prepaidDeduction
				}
				// 2) Monthly allowance with 110% overflow buffer
				// (10% headroom for in-flight requests).
				if remaining > 0 {
					buffer := creditTotal / 10
					creditCap := creditTotal + buffer
					creditRemaining := creditCap - creditUsed
					if creditRemaining > 0 {
						if creditRemaining >= remaining {
							creditDeduction = remaining
						} else {
							creditDeduction = creditRemaining
						}
						if _, err := tx.Exec(ctx, `UPDATE enterprises SET credit_used = credit_used + $1, updated_at = NOW()
												 WHERE id = $2::uuid`, creditDeduction, entEnterpriseID); err != nil {
							return fmt.Errorf("database operation failed: %w", err)
						}
					}
				}
			}
		}
		if unpaid := enterpriseCost - creditDeduction; unpaid > 0 {
			r.logger.Warn("Enterprise credit insufficient for enterprise-key usage",
				zap.String("user_id", userID),
				zap.String("enterprise_id", entEnterpriseID),
				zap.Int64("unpaid_quota", unpaid))
		}

		// Track per-member credit usage for per-member credit cap enforcement.
		// processBillingBatch is aggregated by user_id, so a single UPDATE covers
		// all enterprise-key records in this batch.
		if enterpriseCost > 0 {
			if _, err := tx.Exec(ctx, `UPDATE users SET credit_used = COALESCE(credit_used, 0) + $1, updated_at = NOW()
									 WHERE id = $2::uuid`, enterpriseCost, userID); err != nil {
				return fmt.Errorf("database operation failed: %w", err)
			}
		}
	}

	// 5b. Subscription-key records: subscription quota first.
	if subscriptionCost > 0 {
		var subID string
		var subQuotaRemaining int64
		var subDailyLimit int64
		var subDailyUsed int64
		var subDailyReset time.Time
		var subPlanType string
		err = tx.QueryRow(ctx,
			`SELECT id::text, quota_remaining, COALESCE(daily_limit, 0), daily_quota_used,
			        COALESCE(daily_quota_reset, '0001-01-01'::timestamp without time zone),
			        COALESCE(plan_type, 'token')
			 FROM user_subscriptions
			 WHERE user_id = $1::uuid AND status = 'active' AND expires_at > NOW()
			 ORDER BY created_at DESC LIMIT 1`,
			userID,
		).Scan(&subID, &subQuotaRemaining, &subDailyLimit, &subDailyUsed, &subDailyReset, &subPlanType)
		if err == nil {
			// Reset daily quota if needed (NULL/zero reset time means never initialized).
			// Scope updates to the selected subscription row: a user may hold
			// multiple active subscriptions, and updating by user_id+status
			// would deduct quota from ALL of them (double consumption).
			if subDailyReset.IsZero() || time.Now().After(subDailyReset) {
				subDailyUsed = 0
				subDailyReset = time.Now().Truncate(24 * time.Hour).Add(24 * time.Hour)
				if _, err := tx.Exec(ctx, `UPDATE user_subscriptions SET daily_quota_used = 0, daily_quota_reset = $1
									 WHERE id = $2::uuid`, subDailyReset, subID); err != nil {
					return fmt.Errorf("database operation failed: %w", err)
				}
			}
			// Units consumed across the subscription records in this batch, per
			// plan_type: token plan -> input+output tokens; code plan -> one per
			// request.
			totalUnits := int64(0)
			for i, rec := range records {
				if _, ok := enterpriseIdxSet[i]; ok {
					continue // enterprise records are billed separately
				}
				if keyInfoByHash[rec.APIKeyHash].keyType == "subscription" {
					if subPlanType == "code" {
						reqs := int64(rec.RequestCount)
						if reqs <= 0 {
							reqs = 1
						}
						totalUnits += reqs
					} else {
						totalUnits += int64(rec.InputTokens + rec.OutputTokens)
					}
				}
			}
			if totalUnits > 0 && subQuotaRemaining > 0 {
				// Honor daily limit if the plan defines one (0/unlimited otherwise).
				usableQuota := subQuotaRemaining
				if subDailyLimit > 0 {
					dailyRemaining := subDailyLimit - subDailyUsed
					if dailyRemaining < usableQuota {
						usableQuota = dailyRemaining
					}
				}
				if usableQuota > 0 {
					if totalUnits <= usableQuota {
						subscriptionDeduction = totalUnits
						subscriptionDeductionCost = subscriptionCost
					} else {
						subscriptionDeduction = usableQuota
						// Pro-rate the subscription cost covered by the quota.
						if totalUnits > 0 {
							subscriptionDeductionCost = int64(float64(subscriptionCost) * float64(usableQuota) / float64(totalUnits))
						}
					}
					if _, err := tx.Exec(ctx, `UPDATE user_subscriptions
										 SET quota_used = quota_used + $1,
										     quota_remaining = quota_remaining - $1,
										     daily_quota_used = daily_quota_used + $1,
										     updated_at = NOW()
										 WHERE id = $2::uuid`, subscriptionDeduction, subID); err != nil {
						return fmt.Errorf("database operation failed: %w", err)
					}
					if r.notif != nil && subscriptionDeduction > 0 {
						var qt, qr int64
						if err := tx.QueryRow(ctx,
							`SELECT quota_total, quota_remaining FROM user_subscriptions WHERE id = $1::uuid`, subID,
						).Scan(&qt, &qr); err == nil && qt > 0 {
							quotaWarnSubID, quotaWarnTotal, quotaWarnRemaining = subID, qt, qr
						}
					}
				}
			}
		}
	}

	// 5c. General/economy records: Gift -> Balance.
	// Subscription-key records settle ONLY against subscription quota; their
	// uncovered cost is NOT charged to the wallet (the proxy gate rejects
	// requests once quota is exhausted). So only otherCost reaches the wallet.
	remaining := otherCost
	if remaining > 0 && giftBalance > 0 {
		if giftBalance >= remaining {
			giftDeduction = remaining
			remaining = 0
		} else {
			giftDeduction = giftBalance
			remaining -= giftBalance
		}
	}
	if remaining > 0 {
		if totalBalance >= remaining {
			balanceDeduction = remaining
			remaining = 0
		} else {
			balanceDeduction = totalBalance
			remaining -= totalBalance
		}
	}

	// 5. Update user balances
	if giftDeduction > 0 || balanceDeduction > 0 {
		_, err = tx.Exec(ctx,
			`UPDATE users SET
			 gift_balance = GREATEST(gift_balance - $1, 0),
			 balance = GREATEST(balance - $2, 0),
			 updated_at = NOW()
			 WHERE id = $3::uuid`,
			giftDeduction, balanceDeduction, userID,
		)
		if err != nil {
			r.logger.Warn("Failed to deduct balance", zap.Error(err), zap.String("user_id", userID))
		}
	}

	// 6. Log balance transactions (one per type, aggregated)
	batchRefID := fmt.Sprintf("batch-%s-%d", time.Now().Format("20060102150405"), len(records))
	r.logBalanceDeductions(ctx, tx, userID, batchRefID,
		giftBalance, totalBalance,
		giftDeduction, balanceDeduction)
	if creditDeduction > 0 {
		if _, err := tx.Exec(ctx, `INSERT INTO balance_transactions
					 (user_id, transaction_type, amount, balance_type, balance_before, balance_after, reference_id, description)
					 VALUES ($1::uuid, 'usage', $2, 'credit', $3, $4, $5, 'API usage deduction (batch)')`, userID, -creditDeduction, 0, 0, batchRefID); err != nil {
			return fmt.Errorf("database operation failed: %w", err)
		}
	}

	// 6b. Settle refund records: credit the balance back for charge-on-dispatch
	// tasks that failed upstream. Enterprise-key refunds release enterprise
	// credit_used instead (post-paid model, no personal balance involved).
	if refundTotal > 0 {
		if _, err := tx.Exec(ctx,
			`UPDATE users SET balance = balance + $1, updated_at = NOW() WHERE id = $2::uuid`,
			refundTotal, userID); err != nil {
			return fmt.Errorf("database operation failed: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO balance_transactions
					 (user_id, transaction_type, amount, balance_type, balance_before, balance_after, reference_id, description)
					 VALUES ($1::uuid, 'refund', $2, 'balance', $3, $4, $5, 'Async media task failed — refund of dispatch charge (batch)')`,
			userID, refundTotal, totalBalance, totalBalance+refundTotal, batchRefID); err != nil {
			return fmt.Errorf("database operation failed: %w", err)
		}
		r.logger.Info("Refunded dispatch charges for failed async media tasks",
			zap.String("user_id", userID),
			zap.Int64("refund_quota", refundTotal))
	}
	if entRefundTotal > 0 {
		if entEnterpriseID == "" {
			entEnterpriseID = enterpriseID
		}
		if entEnterpriseID != "" {
			// LIFO refund: deduction order is prepaid → allowance, so refunds
			// repay the monthly allowance first, then the prepaid wallet. Mirrors
			// the per-record path (webhook_usage.go). The previous
			// `GREATEST(credit_used - $1, 0)` form silently discarded any portion
			// that should have returned to prepaid_balance, permanently losing
			// real prepaid money on failed charge-on-dispatch media tasks.
			var creditUsed int64
			if err := tx.QueryRow(ctx,
				`SELECT COALESCE(credit_used, 0) FROM enterprises WHERE id = $1::uuid`,
				entEnterpriseID,
			).Scan(&creditUsed); err != nil {
				return fmt.Errorf("select enterprise credit for refund: %w", err)
			}
			toAllowance := entRefundTotal
			if creditUsed < toAllowance {
				toAllowance = creditUsed
			}
			toPrepaid := entRefundTotal - toAllowance
			if _, err := tx.Exec(ctx,
				`UPDATE enterprises SET credit_used = credit_used - $1, prepaid_balance = prepaid_balance + $2, updated_at = NOW() WHERE id = $3::uuid`,
				toAllowance, toPrepaid, entEnterpriseID); err != nil {
				return fmt.Errorf("release enterprise credit for refund (batch): %w", err)
			}
		}
	}

	if remaining > 0 {
		r.logger.Warn("User balance insufficient",
			zap.String("user_id", userID),
			zap.Int64("remaining_cents", remaining))
	}

	// Notify Proxy if balance is low after deduction (batch path)
	remainingBalance := giftBalance - giftDeduction + totalBalance - balanceDeduction + refundTotal
	if remainingBalance <= 1_000_000 {
		r.db.PublishBalanceUpdate(ctx, userID, remainingBalance, 1_000_000)
	}

	// 7. Check enterprise limits if applicable — enterprise-key records only.
	// Personal-key usage under an enterprise member must NOT affect enterprise
	// quota_used or credit thresholds (personal and enterprise billing are separate).
	if len(enterpriseIdx) > 0 && entEnterpriseID != "" {
		entTokens := int64(0)
		for _, i := range enterpriseIdx {
			entTokens += int64(records[i].InputTokens + records[i].OutputTokens)
		}
		// Track per-member token usage for quota cap enforcement (mirrors processRecord)
		if entTokens > 0 {
			if _, err := tx.Exec(ctx, `UPDATE users SET quota_used = COALESCE(quota_used, 0) + $1, updated_at = NOW()
							 WHERE id = $2::uuid`, entTokens, userID); err != nil {
				return fmt.Errorf("database operation failed: %w", err)
			}
		}
		r.checkEnterpriseLimits(ctx, tx, entEnterpriseID, entTokens, enterpriseCost)
	}

	// 8. Update usage_records with deduction amounts.
	// Enterprise records only carry credit_deduction; subscription-key records
	// carry subscription_deduction (up to quota) plus gift/balance for the
	// unpaid portion; general/economy records carry gift/balance only.
	isEnterprise := make([]bool, len(records))
	for _, i := range enterpriseIdx {
		isEnterprise[i] = true
	}
	// Wallet (gift/balance) only ever covers general/economy records now, so
	// the attribution pool is otherCost. Subscription records carry only
	// subscription_deduction; their cost never reaches the wallet.
	chargeablePersonalCost := otherCost
	for i, record := range records {
		var giftShare, creditShare, subShare, balanceShare int64
		if _, ok := refundIdxSet[i]; ok {
			// Refund audit: negative balance_deduction mirrors the credit
			// settled in step 6b (recordCosts[i] is already negated).
			balanceShare = recordCosts[i]
		} else {
			info := keyInfoByHash[record.APIKeyHash]
			if isEnterprise[i] {
				if enterpriseCost > 0 {
					creditShare = int64(float64(creditDeduction) * float64(recordCosts[i]) / float64(enterpriseCost))
				}
			} else if info.keyType == "subscription" {
				// Subscription records: only the quota-covered portion is
				// attributed (subscription_deduction cost share). No wallet share.
				if subscriptionCost > 0 {
					subShare = int64(float64(subscriptionDeductionCost) * float64(recordCosts[i]) / float64(subscriptionCost))
				}
			} else {
				if chargeablePersonalCost > 0 {
					giftShare = int64(float64(giftDeduction) * float64(recordCosts[i]) / float64(chargeablePersonalCost))
					balanceShare = int64(float64(balanceDeduction) * float64(recordCosts[i]) / float64(chargeablePersonalCost))
				}
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE usage_records SET
					 gift_deduction = $1,
					 recharge_deduction = 0,
					 credit_deduction = $2,
					 subscription_deduction = $3,
					 balance_deduction = $4
					 WHERE trace_id = $5`, giftShare, creditShare, subShare, balanceShare, record.TraceID); err != nil {
			return fmt.Errorf("database operation failed: %w", err)
		}
	}

	// Invalidate the cached validation result for this user so the validate
	// endpoint sees the new balance immediately. Without this, a user in
	// arrears can keep calling the API within the TTL (overspend), and a
	// recharged user stays 402 until the cache expires.
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	if giftDeduction > 0 || balanceDeduction > 0 || enterpriseCost > 0 || refundTotal > 0 || entRefundTotal > 0 {
		invalidateUserValidateCaches(ctx, r.db, r.logger, userID)
	}
	// Post-commit touchpoint: warn when the user's usable balance (gift +
	// paid) drops below the low-balance threshold. Threshold default $1;
	// admin/settings configurability lands with the P2 delivery worker.
	if r.notif != nil && (giftDeduction > 0 || balanceDeduction > 0) {
		usable := (giftBalance - giftDeduction) + (totalBalance - balanceDeduction)
		threshold := int64(1_000_000)
		if r.notif != nil {
			threshold = r.notif.BalanceLowThresholdMicro(ctx)
		}
		if usable < threshold {
			params := map[string]string{"balance": fmt.Sprintf("$%.2f", float64(usable)/1e6)}
			r.notif.NotifyUserLocalized(ctx, userID, "balance_low", "high",
				"notif.balance_low.title", "notif.balance_low.body", params,
				"/billing", "balance", userID)
		}
	}
	if r.notif != nil && quotaWarnTotal > 0 {
		usedPct := float64(quotaWarnTotal-quotaWarnRemaining) / float64(quotaWarnTotal) * 100
		params := map[string]string{
			"used":      fmt.Sprintf("%.0f", usedPct),
			"remaining": fmt.Sprintf("%d", quotaWarnRemaining),
		}
		if usedPct >= 90 {
			r.notif.NotifyUserLocalized(ctx, userID, "quota_warning", "high",
				"notif.quota_warning.title", "notif.quota_warning.body", params,
				"/plan-and-group", "subscription", quotaWarnSubID)
		} else if usedPct >= 80 {
			r.notif.NotifyUserLocalized(ctx, userID, "quota_warning", "normal",
				"notif.quota_warning.title", "notif.quota_warning.body", params,
				"/plan-and-group", "subscription", quotaWarnSubID)
		}
	}
	return nil
}

// logBalanceDeductions writes balance_transactions for each non-zero deduction.
func (r *WebhookRoutes) logBalanceDeductions(ctx context.Context, tx pgx.Tx, userID, traceID string,
	giftBalance, totalBalance int64,
	giftDeduction, balanceDeduction int64) {

	if giftDeduction > 0 {
		if _, err := tx.Exec(ctx, `INSERT INTO balance_transactions
					 (user_id, transaction_type, amount, balance_type, balance_before, balance_after, reference_id, description)
					 VALUES ($1::uuid, 'usage', $2, 'gift', $3, $4, $5, 'API usage deduction')`, userID, -giftDeduction, giftBalance, giftBalance-giftDeduction, traceID); err != nil {
			r.logger.Error("database operation failed", zap.Error(err))
		}
	}
	if balanceDeduction > 0 {
		if _, err := tx.Exec(ctx, `INSERT INTO balance_transactions
					 (user_id, transaction_type, amount, balance_type, balance_before, balance_after, reference_id, description)
					 VALUES ($1::uuid, 'usage', $2, 'balance', $3, $4, $5, 'API usage deduction')`, userID, -balanceDeduction, totalBalance, totalBalance-balanceDeduction, traceID); err != nil {
			r.logger.Error("database operation failed", zap.Error(err))
		}
	}
}

// checkEnterpriseLimits checks enterprise quota and credit limits, sends alerts if thresholds exceeded
func (r *WebhookRoutes) checkEnterpriseLimits(ctx context.Context, tx pgx.Tx, enterpriseID string, tokens int64, cost int64) {
	var quotaTotal, quotaUsed int64
	var creditTotal, creditUsed int64

	err := tx.QueryRow(ctx,
		`SELECT COALESCE(quota_total, 0), COALESCE(quota_used, 0), 
		        COALESCE(credit_total, 0), COALESCE(credit_used, 0)
		 FROM enterprises WHERE id = $1::uuid`,
		enterpriseID,
	).Scan(&quotaTotal, &quotaUsed, &creditTotal, &creditUsed)

	if err != nil {
		return
	}

	// Update quota used
	if quotaTotal > 0 {
		if _, err := tx.Exec(ctx, `UPDATE enterprises SET quota_used = quota_used + $1, updated_at = NOW() WHERE id = $2::uuid`, tokens, enterpriseID); err != nil {
			r.logger.Error("database operation failed", zap.Error(err))
		}

		// Check quota thresholds
		newQuotaUsed := quotaUsed + tokens
		quotaPercent := float64(newQuotaUsed) / float64(quotaTotal) * 100

		if quotaPercent >= 100 {
			r.sendEnterpriseAlert(ctx, enterpriseID, "quota_exceeded", 100, float64(newQuotaUsed), float64(quotaTotal))
		} else if quotaPercent >= 90 {
			r.sendEnterpriseAlert(ctx, enterpriseID, "quota_warning", 90, float64(newQuotaUsed), float64(quotaTotal))
		} else if quotaPercent >= 80 {
			r.sendEnterpriseAlert(ctx, enterpriseID, "quota_warning", 80, float64(newQuotaUsed), float64(quotaTotal))
		}
	}

	// Check credit thresholds
	if creditTotal > 0 {
		newCreditUsed := creditUsed + cost
		creditPercent := float64(newCreditUsed) / float64(creditTotal) * 100

		if creditPercent >= 100 {
			r.sendEnterpriseAlert(ctx, enterpriseID, "credit_exceeded", 100, float64(newCreditUsed), float64(creditTotal))
		} else if creditPercent >= 90 {
			r.sendEnterpriseAlert(ctx, enterpriseID, "credit_warning", 90, float64(newCreditUsed), float64(creditTotal))
		} else if creditPercent >= 80 {
			r.sendEnterpriseAlert(ctx, enterpriseID, "credit_warning", 80, float64(newCreditUsed), float64(creditTotal))
		}
	}
}

// sendEnterpriseAlert records an alert for enterprise quota/credit thresholds
func (r *WebhookRoutes) sendEnterpriseAlert(ctx context.Context, enterpriseID, alertType string, threshold int, currentValue, limitValue float64) {
	// Respect per-enterprise alert settings: missing rows default to enabled.
	var enabled bool
	if err := r.db.QueryRow(ctx,
		`SELECT COALESCE(enabled, true) FROM enterprise_alert_settings
		 WHERE enterprise_id = $1::uuid AND alert_type = $2 AND threshold_percent = $3`,
		enterpriseID, alertType, threshold,
	).Scan(&enabled); err == nil && !enabled {
		return
	}

	// Check if alert already sent for this threshold today
	var existingAlertID string
	err := r.db.QueryRow(ctx,
		`SELECT id FROM enterprise_alerts 
		 WHERE enterprise_id = $1::uuid AND alert_type = $2 AND threshold_percent = $3
		 AND notified_at > NOW() - INTERVAL '24 hours'
		 ORDER BY notified_at DESC LIMIT 1`,
		enterpriseID, alertType, threshold,
	).Scan(&existingAlertID)

	if err == nil && existingAlertID != "" {
		return // Alert already sent today
	}

	// Record the alert
	if err := r.db.Exec(ctx, `INSERT INTO enterprise_alerts (enterprise_id, alert_type, threshold_percent, current_value, limit_value)
			 VALUES ($1::uuid, $2, $3, $4, $5)`, enterpriseID, alertType, threshold, currentValue, limitValue); err != nil {
		r.logger.Error("database operation failed", zap.Error(err))
	}

	// Mirror to the notification inbox so admins see it without opening the
	// alerts page (title/body rendered per admin locale by the service).
	// In-app only: the alerts pipeline below already sends its own email —
	// enqueueing a second one here would double-notify on high thresholds.
	if r.notif != nil {
		ntype := "enterprise_" + alertType // quota_warning | quota_exceeded | credit_warning | credit_exceeded
		priority := "normal"
		if threshold >= 90 || strings.HasSuffix(alertType, "_exceeded") {
			priority = "high"
		}
		var entName string
		_ = r.db.QueryRow(ctx, `SELECT name FROM enterprises WHERE id = $1::uuid`, enterpriseID).Scan(&entName)
		params := map[string]string{
			"enterprise": entName,
			"threshold":  strconv.Itoa(threshold),
			"current":    fmt.Sprintf("%.2f", currentValue),
			"limit":      fmt.Sprintf("%.2f", limitValue),
		}
		r.notif.NotifyEnterpriseAdminsInApp(ctx, enterpriseID, ntype, priority,
			"notif."+ntype+".title", "notif."+ntype+".body", params,
			"/enterprise/alerts", "enterprise", enterpriseID)
	}

	// Send email notification to enterprise admin
	r.sendEnterpriseAlertEmail(ctx, enterpriseID, alertType, threshold, currentValue, limitValue)

	r.logger.Warn("Enterprise alert triggered",
		zap.String("enterprise_id", enterpriseID),
		zap.String("alert_type", alertType),
		zap.Int("threshold", threshold),
		zap.Float64("current", currentValue),
		zap.Float64("limit", limitValue),
	)
}

// sendEnterpriseAlertEmail sends email notification to all enterprise admins
func (r *WebhookRoutes) sendEnterpriseAlertEmail(ctx context.Context, enterpriseID, alertType string, threshold int, currentValue, limitValue float64) {
	if r.mailSender == nil || !r.mailSender.IsConfigured() {
		r.logger.Debug("Mail sender not configured, skipping enterprise alert email")
		return
	}

	// Get enterprise name
	var enterpriseName string
	err := r.db.QueryRow(ctx,
		`SELECT name FROM enterprises WHERE id = $1::uuid`, enterpriseID,
	).Scan(&enterpriseName)
	if err != nil {
		r.logger.Warn("Failed to get enterprise name", zap.Error(err), zap.String("enterprise_id", enterpriseID))
		return
	}

	// Get all admin emails
	rows, err := r.db.Query(ctx,
		`SELECT u.email FROM users u
		 WHERE u.enterprise_id = $1::uuid AND u.role = 'enterprise_admin' AND u.deleted_at IS NULL`,
		enterpriseID)
	if err != nil {
		r.logger.Warn("Failed to get enterprise admin emails", zap.Error(err), zap.String("enterprise_id", enterpriseID))
		return
	}
	defer rows.Close()

	var adminEmails []string
	for rows.Next() {
		var email string
		if err := rows.Scan(&email); err == nil && email != "" {
			adminEmails = append(adminEmails, email)
		}
	}

	if len(adminEmails) == 0 {
		r.logger.Debug("No enterprise admin emails found", zap.String("enterprise_id", enterpriseID))
		return
	}

	// Send alert email to all admins
	for _, adminEmail := range adminEmails {
		if err := r.mailSender.SendEnterpriseAlert(adminEmail, enterpriseName, alertType, float64(threshold), currentValue, limitValue, "zh"); err != nil {
			r.logger.Warn("Failed to send enterprise alert email",
				zap.Error(err),
				zap.String("email", adminEmail),
			)
		} else {
			r.logger.Info("Enterprise alert email sent",
				zap.String("email", adminEmail),
				zap.String("enterprise_id", enterpriseID),
				zap.String("alert_type", alertType),
			)
		}
	}
}

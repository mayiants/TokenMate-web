package routes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ai-proxy/ai-portal-go/internal/services"
	"github.com/ai-proxy/ai-portal-go/pkg/metrics"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

// usageRecordStatus returns the status for a usage record, trusting the proxy-sent
// value when present and falling back to "error" if an error message exists.
func usageRecordStatus(r UsageRecord) string {
	if r.Status != "" {
		return r.Status
	}
	// A 4xx/5xx status code marks a failed request even if the Proxy sent no
	// error text (e.g. upstream returned an empty error body).
	if r.Error != "" || r.StatusCode >= 400 {
		return "error"
	}
	return "success"
}

// deriveErrorCode normalizes a proxy-sent status/error into a stable error code
// for enterprise error analysis. It trusts an explicit ErrorCode when present.
func deriveErrorCode(status, errText, errCode string) string {
	if errCode != "" {
		return errCode
	}
	if status != "" && status != "success" && status != "error" && status != "failed" {
		return status
	}
	errLower := strings.ToLower(errText)
	if strings.Contains(errLower, "rate") || strings.Contains(errLower, "429") || strings.Contains(errLower, "throttl") {
		return "429"
	}
	if strings.Contains(errLower, "unauthorized") || strings.Contains(errLower, "401") {
		return "401"
	}
	if strings.Contains(errLower, "forbidden") || strings.Contains(errLower, "403") {
		return "403"
	}
	if strings.Contains(errLower, "not found") || strings.Contains(errLower, "404") {
		return "404"
	}
	if strings.Contains(errLower, "timeout") || strings.Contains(errLower, "504") {
		return "504"
	}
	if strings.Contains(errLower, "500") || strings.Contains(errLower, "internal") {
		return "500"
	}
	if strings.Contains(errLower, "member_credit_exhausted") {
		return "MEMBER_CREDIT_EXHAUSTED"
	}
	if strings.Contains(errLower, "model_capability_exceeded") {
		return "MODEL_CAPABILITY_EXCEEDED"
	}
	if errText != "" {
		return "error"
	}
	return ""
}

// recordCreatedAt returns the timestamp parsed from the record's Timestamp field
// (RFC3339), falling back to time.Now().UTC() when missing or unparseable. Using
// the proxy-sent timestamp instead of NOW() ensures that the UNIQUE(trace_id,
// created_at) constraint can actually de-duplicate replays of the same traceId.
func recordCreatedAt(r UsageRecord) time.Time {
	if r.Timestamp != "" {
		if t, err := time.Parse(time.RFC3339Nano, r.Timestamp); err == nil {
			return t.UTC()
		}
		if t, err := time.Parse(time.RFC3339, r.Timestamp); err == nil {
			return t.UTC()
		}
	}
	return time.Now().UTC()
}

func (r *WebhookRoutes) handleUsage(c *gin.Context) {
	r.recordWebhookReceived()

	var record UsageRecord
	if err := c.ShouldBindJSON(&record); err != nil {
		r.recordWebhookFailed()
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}

	// Try Redis queue first (consistent with batch handler)
	if r.db.Redis != nil {
		data, _ := json.Marshal(record)
		if pushErr := r.db.Redis.RPush(c.Request.Context(), "webhook:usage:queue", data).Err(); pushErr != nil {
			metrics.GetMetrics().WebhookRedisQueueFailed.Add(1)
			r.logger.Error("Failed to push usage record to Redis queue", zap.Error(pushErr))
		} else {
			c.JSON(http.StatusOK, gin.H{"ok": true, "queued": true})
			return
		}
	}

	// Fallback: process directly
	if err := r.processRecord(c.Request.Context(), &record); err != nil {
		r.recordWebhookFailed()
		r.logger.Error("Failed to process usage record", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Processing failed"}})
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// maxUsageAuditBatchSize caps the audit batch defensively so a misbehaving
// proxy cannot force arbitrarily large single-statement inserts.
const maxUsageAuditBatchSize = 5000

// handleUsageBatch handles audit-logger batch (debug/audit trail only, not used for billing).
// Records are persisted to proxy_request_audits so failed upstream requests are
// quantifiable; persistence failures are logged but never fail the HTTP response
// (this is audit data and must not backpressure the proxy).
func (r *WebhookRoutes) handleUsageBatch(c *gin.Context) {
	r.recordWebhookReceived()

	var batch BatchUsageReport
	if err := c.ShouldBindJSON(&batch); err != nil {
		r.recordWebhookFailed()
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}

	if len(batch.Records) > maxUsageAuditBatchSize {
		r.logger.Warn("Usage audit batch exceeds maximum size, discarding",
			zap.Int("count", len(batch.Records)),
			zap.Int("max", maxUsageAuditBatchSize))
		c.JSON(http.StatusOK, gin.H{"ok": true, "count": len(batch.Records), "audit": true, "dropped": true})
		return
	}

	inserted := 0
	if len(batch.Records) > 0 {
		if n, err := r.insertProxyRequestAudits(c.Request.Context(), &batch); err != nil {
			r.logger.Error("Failed to persist proxy request audits",
				zap.Error(err), zap.Int("count", len(batch.Records)))
		} else {
			inserted = n
		}
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "count": len(batch.Records), "audit": true, "inserted": inserted})
}

// handleUsageStatsBatch handles usage stats from the proxy-side UsageBuffer.
// This is the primary billing path: stats records are queued for INSERT + billing deduction.
func (r *WebhookRoutes) handleUsageStatsBatch(c *gin.Context) {
	r.recordWebhookReceived()

	var batch BatchUsageReport
	if err := c.ShouldBindJSON(&batch); err != nil {
		r.recordWebhookFailed()
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}

	// Queue in Redis for async processing using Pipeline to reduce round-trips.
	if r.db.Redis != nil && len(batch.Records) > 0 {
		ctx := c.Request.Context()
		pipe := r.db.Redis.Pipeline()
		for _, record := range batch.Records {
			data, _ := json.Marshal(record)
			pipe.RPush(ctx, "webhook:usage-stats:queue", data)
		}
		if _, err := pipe.Exec(ctx); err == nil {
			c.JSON(http.StatusOK, gin.H{"ok": true, "queued": true, "count": len(batch.Records)})
			return
		} else {
			metrics.GetMetrics().WebhookRedisQueueFailed.Add(1)
			r.logger.Error("Failed to push usage-stats batch to Redis queue", zap.Error(err))
		}
	}

	// Fallback: process directly in a batched DB transaction.
	failed := 0
	if len(batch.Records) > 0 {
		records := make([]*UsageRecord, len(batch.Records))
		for i := range batch.Records {
			records[i] = &batch.Records[i]
		}
		if err := r.processStatsBatch(c.Request.Context(), records); err != nil {
			r.recordWebhookFailed()
			r.logger.Error("Failed to process usage-stats batch fallback", zap.Error(err))
			failed = len(batch.Records)
		} else {
			// Stats are persisted but could not be queued (Redis unavailable);
			// bill synchronously so the usage is still charged.
			r.billRecordsDirectly(c.Request.Context(), records)
		}
	}

	c.JSON(http.StatusOK, gin.H{"ok": true, "count": len(batch.Records), "failed": failed})
}

func (r *WebhookRoutes) handleUpstreamUsageBatch(c *gin.Context) {
	r.recordWebhookReceived()

	var batch BatchUpstreamUsageReport
	if err := c.ShouldBindJSON(&batch); err != nil {
		r.recordWebhookFailed()
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}

	ctx := c.Request.Context()
	updated := 0
	for i := range batch.Records {
		if err := r.upsertUpstreamKeyUsage(ctx, &batch.Records[i]); err != nil {
			r.recordWebhookFailed()
			r.logger.Warn("Failed to upsert upstream key usage",
				zap.Error(err),
				zap.String("upstream_key_id", batch.Records[i].UpstreamKeyID),
			)
		} else {
			updated++
		}
	}

	c.JSON(http.StatusOK, gin.H{"ok": true, "updated": updated, "count": len(batch.Records)})
}

func (r *WebhookRoutes) upsertUpstreamKeyUsage(ctx context.Context, record *UpstreamUsageRecord) error {
	if record.UpstreamKeyID == "" || record.Date == "" {
		return fmt.Errorf("missing upstream_key_id or date")
	}

	_, err := r.db.Pool.Exec(ctx, `
		INSERT INTO upstream_key_usage (
			upstream_key_id, provider, model, date,
			request_count, input_tokens, output_tokens,
			cache_read_tokens, cache_creation_tokens, error_count,
			avg_latency_ms, total_latency_ms
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (upstream_key_id, model, date)
		DO UPDATE SET
			request_count = upstream_key_usage.request_count + EXCLUDED.request_count,
			input_tokens = upstream_key_usage.input_tokens + EXCLUDED.input_tokens,
			output_tokens = upstream_key_usage.output_tokens + EXCLUDED.output_tokens,
			cache_read_tokens = upstream_key_usage.cache_read_tokens + EXCLUDED.cache_read_tokens,
			cache_creation_tokens = upstream_key_usage.cache_creation_tokens + EXCLUDED.cache_creation_tokens,
			error_count = upstream_key_usage.error_count + EXCLUDED.error_count,
			total_latency_ms = upstream_key_usage.total_latency_ms + EXCLUDED.total_latency_ms,
			avg_latency_ms = (upstream_key_usage.total_latency_ms + EXCLUDED.total_latency_ms)
				/ GREATEST(1, upstream_key_usage.request_count + EXCLUDED.request_count),
			updated_at = NOW()
	`, record.UpstreamKeyID, record.Provider, record.Model, record.Date,
		record.RequestCount, record.InputTokens, record.OutputTokens,
		record.CacheReadTokens, record.CacheCreationTokens, record.ErrorCount,
		record.LatencyMs, int64(record.LatencyMs)*int64(record.RequestCount))

	return err
}

func (r *WebhookRoutes) processRecord(ctx context.Context, record *UsageRecord) error {
	// Start transaction for consistency
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Look up API key by hash — single query gets key→user→group→key_type→group_name
	var keyID, userID, keyGroupID, keyPrefix, groupName, keySubID string
	var keyStatus string
	var keyType string
	err = tx.QueryRow(ctx,
		`SELECT k.id::text, COALESCE(k.assigned_user_id, k.user_id)::text, COALESCE(k.group_id::text, '') as group_id,
		        k.status, COALESCE(k.key_type, 'general') as key_type, k.key_prefix,
		        COALESCE(g.name, '') as group_name, COALESCE(k.subscription_id::text, '')
		 FROM api_keys k
		 LEFT JOIN user_groups g ON k.group_id = g.id
		 WHERE k.key_hash = $1 AND k.deleted_at IS NULL`,
		record.APIKeyHash,
	).Scan(&keyID, &userID, &keyGroupID, &keyStatus, &keyType, &keyPrefix, &groupName, &keySubID)
	if err != nil {
		r.logger.Debug("No matching API key for usage record", zap.String("hash", record.APIKeyHash))
		keyID = ""
		userID = ""
		keyType = "general"
	}

	// Calculate cost from model pricing (uses cache for pricing + exchange rate)
	billingRec := services.UsageRecord(*record)
	cost := r.billingSvc.CalculateCost(ctx, &billingRec)
	record.PricingError = billingRec.PricingError

	// Apply group pricing ratio (uses cache, no DB query on cache hit)
	// Trust proxy-sent PricingRatio if available; otherwise derive from key/user group.
	// The proxy-sent value already folds in the enterprise multiplier (validate
	// computes groupChain × enterpriseRatio), so the enterprise factor is only
	// applied here when we derive the ratio locally.
	pricingRatio := 1.0
	actualGroupID := ""
	proxySentRatio := record.PricingRatio > 0
	if proxySentRatio {
		pricingRatio = record.PricingRatio
	} else if keyGroupID != "" {
		pricingRatio = r.billingSvc.GetPricingRatio(ctx, keyGroupID)
	} else if userID != "" {
		var userGroupID string
		_ = r.db.QueryRow(ctx,
			`SELECT COALESCE(group_id::text, '') FROM users WHERE id = $1::uuid`,
			userID,
		).Scan(&userGroupID)
		if userGroupID != "" {
			pricingRatio = r.billingSvc.GetPricingRatio(ctx, userGroupID)
		}
	}
	if !proxySentRatio && keyType == "enterprise" && userID != "" {
		var entRatio float64
		err := tx.QueryRow(ctx,
			`SELECT COALESCE(e.pricing_ratio, 1.0) FROM enterprises e
			 JOIN users u ON u.enterprise_id = e.id
			 WHERE u.id = $1::uuid`,
			userID,
		).Scan(&entRatio)
		if err == nil && entRatio > 0 {
			pricingRatio = pricingRatio * entRatio
		}
	}
	// actual_group_id: trust proxy-sent value; fallback to key/user group
	if record.ActualGroupID != "" {
		actualGroupID = record.ActualGroupID
	} else if keyGroupID != "" {
		actualGroupID = keyGroupID
	} else if userID != "" {
		var userGroupID string
		_ = r.db.QueryRow(ctx,
			`SELECT COALESCE(group_id::text, '') FROM users WHERE id = $1::uuid`,
			userID,
		).Scan(&userGroupID)
		actualGroupID = userGroupID
	}
	cost = int64(float64(cost) * pricingRatio)

	// Refund records (async media task charged on dispatch but failed upstream):
	// the proxy resends the full billing metadata (model/provider/duration), so
	// recomputing the price yields the original dispatch charge — negate it to
	// mirror it. The negative usage row plus the balance credit in the refund
	// branch below net the customer out to "not billed for failed work".
	if record.Status == "refund" && cost > 0 {
		cost = -cost
	}

	// Initialize deduction tracking. The usage_records row is inserted BEFORE
	// any balance/quota deduction so duplicate replays (proxy webhook retry,
	// Redis-fallback path, /reconciliation upload) cannot double-charge: the
	// `INSERT ... ON CONFLICT (trace_id, created_at) DO NOTHING` is the
	// idempotency boundary, and RowsAffected == 0 short-circuits the deduction
	// branch (S-U13). The deduction columns are written back to the row in a
	// single UPDATE at the end of the transaction, after the deduction values
	// are known.
	var subscriptionDeduction int64
	var giftDeduction int64
	var creditDeduction int64
	var balanceDeduction int64
	var refundApplied bool

	effectiveInput := record.EffectiveInputTokens
	if effectiveInput == 0 && record.InputTokens > 0 {
		effectiveInput = record.InputTokens - record.CachedTokens
		if effectiveInput < 0 {
			effectiveInput = 0
		}
	}

	cmdTag, insertErr := tx.Exec(ctx,
		`INSERT INTO usage_records (trace_id, user_id, user_api_key_id, upstream_key_id, model, provider, region,
			  input_tokens, output_tokens, cached_input_tokens, effective_input_tokens, reasoning_tokens, cache_creation_tokens,
			  total_cost, gift_deduction, recharge_deduction, credit_deduction, subscription_deduction,
			  latency_ms, request_count, cache_hit, error, created_at,
			  pricing_ratio_at_request, group_id, actual_group_id, is_fallback, routing_track, original_track,
			  key_prefix, project_name, project_id, endpoint, is_streaming, client_ip, ttft_ms, client_geo, reasoning_intensity, status, pricing_error, error_code, user_agent, agent_tools, called_tools)
			  VALUES ($1, $2::uuid, $3::uuid, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23,
			   $24, $25::uuid, $26::uuid, $27, $28, $29, $30, $31, $32, $33, $34, $35, $36, $37, $38, $39, $40, $41, $42, $43, $44)
			 ON CONFLICT (trace_id, created_at) DO NOTHING`,
		record.TraceID, nullStr(userID), nullStr(keyID), record.UpstreamKeyID, record.Model, record.Provider, record.Region,
		record.InputTokens, record.OutputTokens, record.CachedTokens, effectiveInput, record.ReasoningTokens, record.CacheCreationTokens,
		cost, 0, 0, 0, 0, // deductions filled in below once computed
		record.LatencyMs, record.RequestCount, record.CacheHit, record.Error, recordCreatedAt(*record),
		pricingRatio, nullStr(keyGroupID), nullStr(actualGroupID), record.IsFallback, record.RoutingTrack, record.OriginalTrack,
		keyPrefix, groupName, keyGroupID,
		record.Endpoint, record.IsStreaming, record.ClientIP, record.TTFTMs, record.ClientGeo, record.ReasoningIntensity, usageRecordStatus(*record),
		record.PricingError, deriveErrorCode(usageRecordStatus(*record), record.Error, record.ErrorCode), nullStr(record.UserAgent), nullStr(record.AgentTools), nullStr(record.CalledTools),
	)
	if insertErr != nil {
		return fmt.Errorf("insert usage record: %w", insertErr)
	}
	if cmdTag.RowsAffected() == 0 {
		// Same (trace_id, created_at) already exists — replay detected.
		// Commit the empty transaction and abort before any deduction
		// so we cannot double-charge balance or subscription quota (S-U13).
		_ = tx.Commit(ctx)
		return nil
	}

	// Billing path depends on key_type
	if userID != "" {
		costQuota := cost

		if costQuota < 0 {
			// Refund record: the proxy charged an async media task on dispatch
			// (providers like TokenRouter bill at task creation), the task then
			// failed upstream, and the proxy reports a negative-cost record with
			// status=refund. The INSERT ... ON CONFLICT idempotency boundary
			// above guarantees webhook replays cannot double-refund (S-U13).
			if keyType == "enterprise" {
				// Mirror the batch path (processBillingBatch): release the
				// post-paid enterprise credit instead of crediting balance.
				var entID string
				if err := tx.QueryRow(ctx,
					`SELECT COALESCE(enterprise_id::text, '') FROM users WHERE id = $1::uuid`,
					userID,
				).Scan(&entID); err != nil {
					return fmt.Errorf("select enterprise for refund: %w", err)
				}
				if entID != "" {
					// LIFO refund: the deduction order is prepaid → allowance, so
					// refunds repay the monthly allowance first, then the prepaid wallet.
					refund := -costQuota
					var creditUsed int64
					if err := tx.QueryRow(ctx,
						`SELECT COALESCE(credit_used, 0)
						 FROM enterprises WHERE id = $1::uuid`, entID,
					).Scan(&creditUsed); err != nil {
						return fmt.Errorf("select enterprise credit for refund: %w", err)
					}
					toAllowance := refund
					if creditUsed < toAllowance {
						toAllowance = creditUsed
					}
					toPrepaid := refund - toAllowance
					if _, err := tx.Exec(ctx,
						`UPDATE enterprises SET credit_used = credit_used - $1, prepaid_balance = prepaid_balance + $2, updated_at = NOW()
						 WHERE id = $3::uuid`, toAllowance, toPrepaid, entID); err != nil {
						return fmt.Errorf("release enterprise credit for refund: %w", err)
					}
					refundApplied = true
				}
			} else if err := r.applyBalanceRefund(ctx, tx, userID, record.TraceID, -costQuota); err != nil {
				return err
			} else {
				refundApplied = true
			}
		} else {
			switch keyType {
			case "subscription":
				// Subscription keys settle ONLY against the prepaid subscription
				// quota bucket. They never fall back to personal gift/balance:
				// when the quota is exhausted the proxy gate (apikeys_validate,
				// quota_exceeded=monthly) rejects the request up front, so the
				// billing path must not silently drain the wallet.
				// Deduction unit depends on plan_type:
				//   token plan -> input+output tokens
				//   code plan  -> request count (one "use" per request)
				var subID string
				var subQuotaRemaining int64
				var subDailyLimit int64
				var subDailyUsed int64
				var subDailyReset time.Time
				var subPlanType string

				err = tx.QueryRow(ctx,
					// Package keys bind a subscription at creation; prefer it. Unbound
					// (legacy) keys fall back to the newest active subscription.
					`SELECT id::text, quota_remaining, COALESCE(daily_limit, 0), daily_quota_used,
				        COALESCE(daily_quota_reset, '0001-01-01'::timestamp without time zone),
				        COALESCE(plan_type, 'token')
				 FROM user_subscriptions
				 WHERE user_id = $1::uuid AND status = 'active' AND expires_at > NOW()
				   AND ($2 = '' OR id = $2::uuid)
				 ORDER BY (id = NULLIF($2, '')::uuid) DESC, created_at DESC LIMIT 1`,
					userID, keySubID,
				).Scan(&subID, &subQuotaRemaining, &subDailyLimit, &subDailyUsed, &subDailyReset, &subPlanType)

				if err == nil {
					// Reset daily quota if needed (NULL/zero reset time means never initialized).
					if subDailyReset.IsZero() || time.Now().After(subDailyReset) {
						subDailyUsed = 0
						subDailyReset = time.Now().Truncate(24 * time.Hour).Add(24 * time.Hour)
						if _, err := tx.Exec(ctx, `UPDATE user_subscriptions SET daily_quota_used = 0, daily_quota_reset = $1
											 WHERE id = $2::uuid`, subDailyReset, subID); err != nil {
							return fmt.Errorf("database operation failed: %w", err)
						}
					}

					// units consumed by this record, per plan_type.
					units := int64(record.InputTokens + record.OutputTokens)
					if subPlanType == "code" {
						units = int64(record.RequestCount)
						if units <= 0 {
							units = 1
						}
					}
					if units > 0 && subQuotaRemaining > 0 {
						if units <= subQuotaRemaining {
							subscriptionDeduction = units
						} else {
							subscriptionDeduction = subQuotaRemaining
						}
						if _, err := tx.Exec(ctx, `UPDATE user_subscriptions
											 SET quota_used = quota_used + $1,
											     quota_remaining = quota_remaining - $1,
											     daily_quota_used = daily_quota_used + $1,
											     updated_at = NOW()
											 WHERE id = $2::uuid`, subscriptionDeduction, subID); err != nil {
							return fmt.Errorf("database operation failed: %w", err)
						}
					}
				}
				// No wallet fallback for subscription keys (see comment above).

			case "enterprise":
				// Path: Enterprise Credit ONLY — enterprise-key usage never
				// touches personal gift/balance (110% cap, post-paid model).
				if costQuota > 0 {
					var giftBalance, totalBalance int64
					var enterpriseID string
					err = tx.QueryRow(ctx,
						`SELECT COALESCE(gift_balance, 0), COALESCE(balance, 0), COALESCE(enterprise_id::text, '')
					 FROM users WHERE id = $1::uuid FOR UPDATE`,
						userID,
					).Scan(&giftBalance, &totalBalance, &enterpriseID)
					if err == nil {
						remaining := costQuota
						// Enterprise credit with 110% overflow cap
						if enterpriseID != "" {
							var prepaidBalance, creditTotal, creditUsed int64
							// NOTE: no FOR UPDATE — row-locking enterprises
							// deadlocks with the FK key-share from
							// enterprise_alerts inserts in checkEnterpriseLimits
							// (separate pool connection); the 110% buffer
							// absorbs the cap-check race.
							err = tx.QueryRow(ctx,
								`SELECT COALESCE(prepaid_balance, 0), COALESCE(credit_total, 0), COALESCE(credit_used, 0)
							 FROM enterprises WHERE id = $1::uuid`,
								enterpriseID,
							).Scan(&prepaidBalance, &creditTotal, &creditUsed)
							if err == nil && (prepaidBalance > 0 || creditTotal > 0) {
								// 1) Prepaid wallet first: d1 = min(cost, prepaidBalance).
								// Prepaid-first is a product decision; refunds
								// unwind in the reverse order (allowance → prepaid, LIFO).
								if prepaidBalance > 0 {
									prepaidDeduction := remaining
									if prepaidBalance < prepaidDeduction {
										prepaidDeduction = prepaidBalance
									}
									if _, err := tx.Exec(ctx, `UPDATE enterprises SET prepaid_balance = prepaid_balance - $1, updated_at = NOW()
																 WHERE id = $2::uuid`, prepaidDeduction, enterpriseID); err != nil {
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
									if creditRemaining <= 0 {
										// Already exceeded 110% — no credit available
										creditDeduction = 0
									} else if creditRemaining >= remaining {
										creditDeduction = remaining
										remaining = 0
									} else {
										creditDeduction = creditRemaining
										remaining -= creditRemaining
									}
									if creditDeduction > 0 {
										if _, err := tx.Exec(ctx, `UPDATE enterprises SET credit_used = credit_used + $1, updated_at = NOW()
																		 WHERE id = $2::uuid`, creditDeduction, enterpriseID); err != nil {
											return fmt.Errorf("database operation failed: %w", err)
										}
									}
								}
							}
						}
						// Note: Enterprise keys do NOT fall back to personal balance.
						// Post-paid model with 110% credit cap — overages are tracked as remaining.
						// Update balances
						if giftDeduction > 0 || balanceDeduction > 0 {
							if _, err := tx.Exec(ctx, `UPDATE users SET
													 gift_balance = GREATEST(gift_balance - $1, 0),
													 balance = GREATEST(balance - $2, 0),
													 updated_at = NOW()
													 WHERE id = $3::uuid`, giftDeduction, balanceDeduction, userID); err != nil {
								return fmt.Errorf("database operation failed: %w", err)
							}
						}
						// Log balance transactions
						r.logBalanceDeductions(ctx, tx, userID, record.TraceID,
							giftBalance, totalBalance,
							giftDeduction, balanceDeduction)
						if creditDeduction > 0 {
							if _, err := tx.Exec(ctx, `INSERT INTO balance_transactions
													 (user_id, transaction_type, amount, balance_type, balance_before, balance_after, reference_id, description)
													 VALUES ($1::uuid, 'usage', $2, 'credit', $3, $4, $5, 'API usage deduction')`, userID, -creditDeduction, 0, 0, record.TraceID); err != nil {
								return fmt.Errorf("database operation failed: %w", err)
							}
						}
						if remaining > 0 {
							r.logger.Warn("User balance insufficient",
								zap.String("user_id", userID),
								zap.Int64("remaining_cents", remaining))
						}
						// Check enterprise limits
						if enterpriseID != "" {
							// Track per-member token usage for quota cap enforcement
							totalTokens := int64(record.InputTokens + record.OutputTokens)
							if totalTokens > 0 {
								_, _ = tx.Exec(ctx,
									`UPDATE users SET quota_used = COALESCE(quota_used, 0) + $1, updated_at = NOW()
								 WHERE id = $2::uuid`,
									totalTokens, userID)
							}
							// Track per-member credit usage for credit cap enforcement
							if cost > 0 {
								_, _ = tx.Exec(ctx,
									`UPDATE users SET credit_used = COALESCE(credit_used, 0) + $1, updated_at = NOW()
								 WHERE id = $2::uuid`,
									cost, userID)
							}
							r.checkEnterpriseLimits(ctx, tx, enterpriseID, totalTokens, cost)
						}
					}
				}

			default: // "general"
				// Path: Gift → Balance (no subscription, no enterprise)
				if costQuota > 0 {
					var giftBalance, totalBalance int64
					err = tx.QueryRow(ctx,
						`SELECT COALESCE(gift_balance, 0), COALESCE(balance, 0)
					 FROM users WHERE id = $1::uuid FOR UPDATE`,
						userID,
					).Scan(&giftBalance, &totalBalance)
					if err == nil {
						remaining := costQuota
						// Gift first
						if giftBalance > 0 && remaining > 0 {
							if giftBalance >= remaining {
								giftDeduction = remaining
								remaining = 0
							} else {
								giftDeduction = giftBalance
								remaining -= giftBalance
							}
						}
						// Balance fallback
						if remaining > 0 {
							if totalBalance >= remaining {
								balanceDeduction = remaining
								remaining = 0
							} else {
								balanceDeduction = totalBalance
								remaining -= totalBalance
							}
						}
						// Update balances
						if giftDeduction > 0 || balanceDeduction > 0 {
							if _, err := tx.Exec(ctx, `UPDATE users SET
													 gift_balance = GREATEST(gift_balance - $1, 0),
													 balance = GREATEST(balance - $2, 0),
													 updated_at = NOW()
													 WHERE id = $3::uuid`, giftDeduction, balanceDeduction, userID); err != nil {
								return fmt.Errorf("database operation failed: %w", err)
							}
						}
						// Log balance transactions
						r.logBalanceDeductions(ctx, tx, userID, record.TraceID,
							giftBalance, totalBalance,
							giftDeduction, balanceDeduction)
						if remaining > 0 {
							r.logger.Warn("User balance insufficient",
								zap.String("user_id", userID),
								zap.Int64("remaining_cents", remaining))
						}
					}
				}
			}
		}
	}

	// The usage_records row was inserted up-front (the idempotency boundary).
	// Now that the deduction paths have computed the actual amounts, write
	// them back into that row in a single UPDATE so the audit shows what was
	// actually charged. charge order gift → balance → credit → subscription.
	if _, err := tx.Exec(ctx,
		`UPDATE usage_records
		    SET gift_deduction = $2,
		        credit_deduction = $3,
		        subscription_deduction = $4
		  WHERE trace_id = $1 AND created_at = $5`,
		record.TraceID, giftDeduction, creditDeduction, subscriptionDeduction, recordCreatedAt(*record),
	); err != nil {
		return fmt.Errorf("update usage record deductions: %w", err)
	}

	// Update API key stats
	if keyID != "" {
		if _, err := tx.Exec(ctx, `UPDATE api_keys SET total_requests = total_requests + $1, total_tokens = total_tokens + $2,
					 updated_at = NOW() WHERE id = $3::uuid`, record.RequestCount, int64(record.InputTokens+record.OutputTokens), keyID); err != nil {
			return fmt.Errorf("database operation failed: %w", err)
		}
	}

	// Update subscription usage counters (only for subscription key type)
	if userID != "" && keyType == "subscription" {
		tokens := int64(record.InputTokens + record.OutputTokens)
		if err := incrementSubscriptionCounters(ctx, tx, userID, keySubID, tokens, int64(record.RequestCount)); err != nil {
			return fmt.Errorf("update subscription counters: %w", err)
		}
	}

	// Notify Proxy if balance is low after deduction (processRecord fallback path)
	if userID != "" && (giftDeduction > 0 || balanceDeduction > 0) {
		var remainingGift, remainingTotal int64
		_ = tx.QueryRow(ctx,
			`SELECT COALESCE(gift_balance, 0), COALESCE(balance, 0)
				 FROM users WHERE id = $1::uuid`,
			userID,
		).Scan(&remainingGift, &remainingTotal)
		remainingBalance := remainingGift + remainingTotal
		if remainingBalance <= 1_000_000 {
			r.db.PublishBalanceUpdate(ctx, userID, remainingBalance, 1_000_000)
		}
	}

	// Invalidate the cached validation result for this user so the validate
	// endpoint sees the new balance immediately (prevents overspend / stale
	// 402 within the cache TTL after a balance change).
	committedBalance := refundApplied || giftDeduction > 0 || balanceDeduction > 0 || (keyType == "enterprise" && cost > 0)
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	if committedBalance && userID != "" {
		invalidateUserValidateCaches(ctx, r.db, r.logger, userID)
	}
	return nil
}

// applyBalanceRefund credits a user's balance for a negative-cost usage
// record. This happens when the proxy charged an async media task on dispatch
// (upstream provider bills at task creation) and the task later failed —
// the proxy then reports a matching negative-cost record so the user's
// balance ledger nets out to "not billed for failed work". The refund is
// logged in balance_transactions with type 'refund' for auditability.
func (r *WebhookRoutes) applyBalanceRefund(ctx context.Context, tx pgx.Tx, userID, traceID string, amount int64) error {
	var balanceBefore int64
	err := tx.QueryRow(ctx,
		`SELECT COALESCE(balance, 0) FROM users WHERE id = $1::uuid FOR UPDATE`,
		userID,
	).Scan(&balanceBefore)
	if err != nil {
		return fmt.Errorf("select user balance for refund: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE users SET balance = balance + $1, updated_at = NOW() WHERE id = $2::uuid`,
		amount, userID); err != nil {
		return fmt.Errorf("apply balance refund: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO balance_transactions
		 (user_id, transaction_type, amount, balance_type, balance_before, balance_after, reference_id, description)
		 VALUES ($1::uuid, 'refund', $2, 'balance', $3, $4, $5, 'Async media task failed — refund of dispatch charge')`,
		userID, amount, balanceBefore, balanceBefore+amount, traceID); err != nil {
		return fmt.Errorf("log balance refund transaction: %w", err)
	}
	r.logger.Info("Refunded dispatch charge for failed async media task",
		zap.String("user_id", userID),
		zap.String("trace_id", traceID),
		zap.Int64("refund_cents", amount))
	return nil
}

// processStatsQueue pops records from the usage queue, inserts usage_records + updates api_keys stats,
// then pushes billing data to the billing queue.
func (r *WebhookRoutes) processStatsQueue() {
	ctx := context.Background()
	var records []*UsageRecord

	for i := 0; i < 200; i++ {
		data, err := r.db.Redis.RPop(ctx, "webhook:usage:queue").Result()
		if err != nil {
			break
		}

		var record UsageRecord
		if err := json.Unmarshal([]byte(data), &record); err != nil {
			r.recordWebhookFailed()
			r.logger.Warn("Failed to unmarshal queued record", zap.Error(err))
			continue
		}

		records = append(records, &record)
	}

	// Batch DB write for all popped records.
	if len(records) > 0 {
		if err := r.processStatsBatch(ctx, records); err != nil {
			r.recordWebhookFailed()
			r.logger.Error("Failed to process stats batch", zap.Error(err), zap.Int("count", len(records)))
			// Re-queue the popped records so they are not silently lost.
			r.requeueStatsRecords(ctx, "webhook:usage:queue", "webhook:usage:deadletter", records)
			return
		}
	}

	// Batch push to billing queue.
	// Re-serialize from the mutated records so that any TraceID generated by
	// processStatsBatch is carried through — the billing batch UPDATE matches
	// on trace_id, so pushing the original JSON (with empty traceId) would
	// leave charge fields permanently at 0.
	if len(records) > 0 && r.db.Redis != nil {
		pipe := r.db.Redis.Pipeline()
		for _, rec := range records {
			data, _ := json.Marshal(rec)
			pipe.RPush(ctx, "webhook:billing:queue", data)
		}
		if _, err := pipe.Exec(ctx); err != nil {
			metrics.GetMetrics().WebhookRedisQueueFailed.Add(1)
			r.logger.Error("Failed to push to billing queue, billing directly", zap.Error(err))
			// Records are already persisted; bill them synchronously so the
			// usage is still charged instead of being silently dropped.
			r.billRecordsDirectly(ctx, records)
		}
	}
}

// processStats handles lightweight stats recording: INSERT usage_records + UPDATE api_keys.
// This is the fast path that runs before billing.
func (r *WebhookRoutes) processStats(ctx context.Context, record *UsageRecord) error {
	// Look up API key to get key_id, user_id, and group_name for the record
	var keyID, userID, keyGroupID, keyType, keyPrefix, groupName, keySubID string
	err := r.db.QueryRow(ctx,
		`SELECT k.id::text, COALESCE(k.assigned_user_id, k.user_id)::text, COALESCE(k.group_id::text, '') as group_id,
		        COALESCE(k.key_type, 'general') as key_type, k.key_prefix,
		        COALESCE(g.name, '') as group_name, COALESCE(k.subscription_id::text, '')
		 FROM api_keys k
		 LEFT JOIN user_groups g ON k.group_id = g.id
		 WHERE k.key_hash = $1 AND k.deleted_at IS NULL`,
		record.APIKeyHash,
	).Scan(&keyID, &userID, &keyGroupID, &keyType, &keyPrefix, &groupName, &keySubID)
	if err != nil {
		keyID = ""
		userID = ""
		keyType = "general"
	}

	// Defensive defaults for proxy-sent fields
	if record.RoutingTrack == "" {
		record.RoutingTrack = "standard"
	}
	if record.OriginalTrack == "" {
		record.OriginalTrack = ""
	}
	if record.TraceID == "" {
		record.TraceID = uuid.New().String()
	}

	// Calculate cost for the record
	billingRec := services.UsageRecord(*record)
	cost := r.billingSvc.CalculateCost(ctx, &billingRec)
	record.PricingError = billingRec.PricingError

	// Apply pricing ratio — trust proxy-sent value if available (it already
	// folds in the enterprise multiplier); otherwise derive from key/user
	// group and apply the enterprise multiplier on top.
	pricingRatio := 1.0
	actualGroupID := ""
	proxySentRatio := record.PricingRatio > 0
	if proxySentRatio {
		pricingRatio = record.PricingRatio
	} else if keyGroupID != "" {
		pricingRatio = r.billingSvc.GetPricingRatio(ctx, keyGroupID)
	} else if userID != "" {
		var userGroupID string
		_ = r.db.QueryRow(ctx,
			`SELECT COALESCE(group_id::text, '') FROM users WHERE id = $1::uuid`,
			userID,
		).Scan(&userGroupID)
		if userGroupID != "" {
			pricingRatio = r.billingSvc.GetPricingRatio(ctx, userGroupID)
		}
	}
	if !proxySentRatio && keyType == "enterprise" && userID != "" {
		var entRatio float64
		err := r.db.QueryRow(ctx,
			`SELECT COALESCE(e.pricing_ratio, 1.0) FROM enterprises e
			 JOIN users u ON u.enterprise_id = e.id
			 WHERE u.id = $1::uuid`,
			userID,
		).Scan(&entRatio)
		if err == nil && entRatio > 0 {
			pricingRatio = pricingRatio * entRatio
		}
	}
	if record.ActualGroupID != "" {
		actualGroupID = record.ActualGroupID
	} else if keyGroupID != "" {
		actualGroupID = keyGroupID
	} else if userID != "" {
		var userGroupID string
		_ = r.db.QueryRow(ctx,
			`SELECT COALESCE(group_id::text, '') FROM users WHERE id = $1::uuid`,
			userID,
		).Scan(&userGroupID)
		actualGroupID = userGroupID
	}
	cost = int64(float64(cost) * pricingRatio)

	// Wrap writes in transaction for atomicity
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Insert usage record
	effectiveInput := record.EffectiveInputTokens
	if effectiveInput == 0 && record.InputTokens > 0 {
		effectiveInput = record.InputTokens - record.CachedTokens
		if effectiveInput < 0 {
			effectiveInput = 0
		}
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO usage_records (trace_id, user_id, user_api_key_id, upstream_key_id, model, provider, region,
			  input_tokens, output_tokens, cached_input_tokens, effective_input_tokens, reasoning_tokens, cache_creation_tokens,
			  total_cost, gift_deduction, recharge_deduction, credit_deduction, subscription_deduction,
			  latency_ms, request_count, cache_hit, error, created_at,
			  pricing_ratio_at_request, group_id, actual_group_id, is_fallback, routing_track, original_track,
			  key_prefix, project_name, project_id, endpoint, is_streaming, client_ip, ttft_ms, client_geo, reasoning_intensity, status, pricing_error, error_code, user_agent, agent_tools, called_tools)
			 VALUES ($1, $2::uuid, $3::uuid, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, 0, 0, 0, 0, $14, $15, $16, $17, $18, $19,
			   $20, $21::uuid, $22::uuid, $23, $24, $25, $26, $27, $28, $29, $30, $31, $32, $33, $34, $35, $36, $37, $38, $39, $40)
			 ON CONFLICT (trace_id, created_at) DO NOTHING`,
		record.TraceID, nullStr(userID), nullStr(keyID), record.UpstreamKeyID, record.Model, record.Provider, record.Region,
		record.InputTokens, record.OutputTokens, record.CachedTokens, effectiveInput, record.ReasoningTokens, record.CacheCreationTokens,
		cost, record.LatencyMs, record.RequestCount, record.CacheHit, record.Error, recordCreatedAt(*record),
		pricingRatio, nullStr(keyGroupID), nullStr(actualGroupID), record.IsFallback, record.RoutingTrack, record.OriginalTrack,
		keyPrefix, groupName, keyGroupID,
		record.Endpoint, record.IsStreaming, record.ClientIP, record.TTFTMs, record.ClientGeo, record.ReasoningIntensity, usageRecordStatus(*record),
		record.PricingError, deriveErrorCode(usageRecordStatus(*record), record.Error, record.ErrorCode), nullStr(record.UserAgent), nullStr(record.AgentTools), nullStr(record.CalledTools),
	)
	if err != nil {
		return fmt.Errorf("insert usage record: %w", err)
	}

	// Update API key stats
	if keyID != "" {
		if _, err := tx.Exec(ctx,
			`UPDATE api_keys SET total_requests = total_requests + $1, total_tokens = total_tokens + $2,
			 updated_at = NOW() WHERE id = $3::uuid`,
			record.RequestCount, int64(record.InputTokens+record.OutputTokens), keyID,
		); err != nil {
			return fmt.Errorf("update api key stats: %w", err)
		}
	}

	// Update subscription usage counters (only for subscription key type)
	if userID != "" && keyType == "subscription" {
		tokens := int64(record.InputTokens + record.OutputTokens)
		if err := incrementSubscriptionCounters(ctx, tx, userID, keySubID, tokens, int64(record.RequestCount)); err != nil {
			return fmt.Errorf("update subscription counters: %w", err)
		}
	}

	return tx.Commit(ctx)
}

// processStatsBatch handles lightweight stats recording for a batch of records.
// It performs a single API-key lookup, a bulk INSERT into usage_records, and a
// bulk UPDATE of api_keys stats in one transaction, while preserving per-record
// subscription counter updates.
func (r *WebhookRoutes) processStatsBatch(ctx context.Context, records []*UsageRecord) error {
	if len(records) == 0 {
		return nil
	}

	type keyInfo struct {
		id        string
		userID    string
		groupID   string
		keyType   string
		keyPrefix string
		groupName string
		keyHash   string
		subID     string
	}

	// Apply defensive defaults and collect unique key hashes.
	seenHashes := make(map[string]struct{})
	keyHashes := make([]string, 0, len(records))
	// Filter out phantom records that arrive without model/provider info.
	// These are duplicates from a legacy proxy code path (recordTokenUsageAsync)
	// that sends a bare-bones record alongside the full one.
	filtered := make([]*UsageRecord, 0, len(records))
	for _, rec := range records {
		if rec.Model == "" {
			continue // Skip phantom records without model — they cause double-counting
		}
		if rec.RoutingTrack == "" {
			rec.RoutingTrack = "standard"
		}
		if rec.TraceID == "" {
			rec.TraceID = uuid.New().String()
		}
		if rec.APIKeyHash != "" {
			if _, ok := seenHashes[rec.APIKeyHash]; !ok {
				seenHashes[rec.APIKeyHash] = struct{}{}
				keyHashes = append(keyHashes, rec.APIKeyHash)
			}
		}
		filtered = append(filtered, rec)
	}
	records = filtered
	if len(records) == 0 {
		return nil
	}

	// Bulk lookup of API keys and group names.
	keyMap := make(map[string]keyInfo, len(keyHashes))
	if len(keyHashes) > 0 {
		rows, err := r.db.Query(ctx,
			`SELECT k.id::text, COALESCE(k.assigned_user_id, k.user_id)::text, COALESCE(k.group_id::text, '') as group_id,
			        COALESCE(k.key_type, 'general') as key_type, k.key_prefix,
			        COALESCE(g.name, '') as group_name, k.key_hash, COALESCE(k.subscription_id::text, '')
			 FROM api_keys k
			 LEFT JOIN user_groups g ON k.group_id = g.id
			 WHERE k.key_hash = ANY($1) AND k.deleted_at IS NULL`,
			keyHashes)
		if err != nil {
			return fmt.Errorf("lookup api keys: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var ki keyInfo
			if err := rows.Scan(&ki.id, &ki.userID, &ki.groupID, &ki.keyType, &ki.keyPrefix, &ki.groupName, &ki.keyHash, &ki.subID); err != nil {
				return fmt.Errorf("scan api key: %w", err)
			}
			keyMap[ki.keyHash] = ki
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate api keys: %w", err)
		}
	}

	type prepared struct {
		userID  string
		keyType string
		subID   string
	}

	preparedRows := make([]prepared, 0, len(records))
	insertArgs := make([]interface{}, 0, len(records)*30)
	apiKeyUpdates := make(map[string]struct {
		reqs   int
		tokens int64
	})

	// Batch model-pricing cost calculation: one rule lookup per (provider, region, model, minute).
	serviceRecords := make([]services.UsageRecord, len(records))
	for i, rec := range records {
		serviceRecords[i] = services.UsageRecord(*rec)
	}
	recordCosts := r.billingSvc.CalculateCostBatch(ctx, serviceRecords)

	// Collect group IDs and user IDs that need a group lookup for batch pricing_ratio resolution.
	groupIDs := make(map[string]struct{})
	userIDsNeedingGroup := make(map[string]struct{})
	for _, rec := range records {
		keyGroupID := ""
		userID := ""
		if ki, ok := keyMap[rec.APIKeyHash]; ok {
			keyGroupID = ki.groupID
			userID = ki.userID
		}
		if rec.PricingRatio > 0 {
			continue
		}
		if keyGroupID != "" {
			groupIDs[keyGroupID] = struct{}{}
		} else if userID != "" {
			userIDsNeedingGroup[userID] = struct{}{}
		}
	}

	// Batch lookup user group IDs.
	userGroupMap := make(map[string]string)
	if len(userIDsNeedingGroup) > 0 {
		userIDs := make([]string, 0, len(userIDsNeedingGroup))
		for id := range userIDsNeedingGroup {
			userIDs = append(userIDs, id)
		}
		rows, err := r.db.Query(ctx,
			`SELECT id::text, COALESCE(group_id::text, '') FROM users WHERE id = ANY($1::uuid[])`,
			userIDs)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var id, gid string
				if err := rows.Scan(&id, &gid); err == nil {
					userGroupMap[id] = gid
					if gid != "" {
						groupIDs[gid] = struct{}{}
					}
				}
			}
		}
	}

	// Batch pricing_ratio lookup.
	groupIDList := make([]string, 0, len(groupIDs))
	for id := range groupIDs {
		groupIDList = append(groupIDList, id)
	}
	ratioMap := r.billingSvc.GetPricingRatioBatch(ctx, groupIDList)

	for i, rec := range records {
		keyID := ""
		userID := ""
		keyGroupID := ""
		keyType := "general"
		keyPrefix := ""
		groupName := ""
		if ki, ok := keyMap[rec.APIKeyHash]; ok {
			keyID = ki.id
			userID = ki.userID
			keyGroupID = ki.groupID
			keyType = ki.keyType
			keyPrefix = ki.keyPrefix
			groupName = ki.groupName
		}

		// Apply model pricing cost.
		cost := recordCosts[i]

		// Apply pricing ratio — trust proxy-sent value if available.
		pricingRatio := 1.0
		actualGroupID := ""
		if rec.PricingRatio > 0 {
			pricingRatio = rec.PricingRatio
		} else if keyGroupID != "" {
			if ratio, ok := ratioMap[keyGroupID]; ok && ratio > 0 {
				pricingRatio = ratio
			}
		} else if userID != "" {
			if gid := userGroupMap[userID]; gid != "" {
				if ratio, ok := ratioMap[gid]; ok && ratio > 0 {
					pricingRatio = ratio
				}
			}
		}

		// actual_group_id: trust proxy-sent value; fallback to key/user group.
		if rec.ActualGroupID != "" {
			actualGroupID = rec.ActualGroupID
		} else if keyGroupID != "" {
			actualGroupID = keyGroupID
		} else if userID != "" {
			actualGroupID = userGroupMap[userID]
		}
		cost = int64(float64(cost) * pricingRatio)

		// Enterprise pricing_ratio override for stats path.
		if keyType == "enterprise" && userID != "" {
			var entRatio float64
			err := r.db.QueryRow(ctx,
				`SELECT COALESCE(e.pricing_ratio, 1.0) FROM enterprises e
				 JOIN users u ON u.enterprise_id = e.id
				 WHERE u.id = $1::uuid`,
				userID,
			).Scan(&entRatio)
			if err == nil && entRatio > 0 && entRatio != 1.0 {
				cost = int64(float64(cost) / pricingRatio * entRatio)
				pricingRatio = entRatio
			}
		}

		effectiveInput := rec.EffectiveInputTokens
		if effectiveInput == 0 && rec.InputTokens > 0 {
			effectiveInput = rec.InputTokens - rec.CachedTokens
			if effectiveInput < 0 {
				effectiveInput = 0
			}
		}

		// Aggregate api_keys stats per key to avoid duplicate updates in the bulk statement.
		if keyID != "" {
			agg := apiKeyUpdates[keyID]
			agg.reqs += rec.RequestCount
			agg.tokens += int64(rec.InputTokens + rec.OutputTokens)
			apiKeyUpdates[keyID] = agg
		}

		insertArgs = append(insertArgs,
			rec.TraceID, nullStr(userID), nullStr(keyID), rec.UpstreamKeyID, rec.Model, rec.Provider, rec.Region,
			rec.InputTokens, rec.OutputTokens, rec.CachedTokens, effectiveInput, rec.ReasoningTokens, rec.CacheCreationTokens,
			cost, 0, 0, 0, 0,
			rec.LatencyMs, rec.RequestCount, rec.CacheHit, rec.Error, recordCreatedAt(*rec),
			pricingRatio, nullStr(keyGroupID), nullStr(actualGroupID), rec.IsFallback, rec.RoutingTrack, rec.OriginalTrack,
			keyPrefix, groupName, keyGroupID,
			rec.Endpoint, rec.IsStreaming, rec.ClientIP, rec.TTFTMs, rec.ClientGeo, rec.ReasoningIntensity, usageRecordStatus(*rec),
			deriveErrorCode(usageRecordStatus(*rec), rec.Error, rec.ErrorCode), nullStr(rec.UserAgent), nullStr(rec.AgentTools), nullStr(rec.CalledTools),
		)
		preparedRows = append(preparedRows, prepared{userID: userID, keyType: keyType, subID: keyMap[rec.APIKeyHash].subID})
	}

	// Execute bulk writes in a single transaction.
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Bulk INSERT usage_records.
	var insertSQL strings.Builder
	insertSQL.WriteString(`INSERT INTO usage_records (trace_id, user_id, user_api_key_id, upstream_key_id, model, provider, region,
			  input_tokens, output_tokens, cached_input_tokens, effective_input_tokens, reasoning_tokens, cache_creation_tokens,
			  total_cost, gift_deduction, recharge_deduction, credit_deduction, subscription_deduction,
			  latency_ms, request_count, cache_hit, error, created_at,
			  pricing_ratio_at_request, group_id, actual_group_id, is_fallback, routing_track, original_track,
			  key_prefix, project_name, project_id, endpoint, is_streaming, client_ip, ttft_ms, client_geo, reasoning_intensity, status, error_code, user_agent, agent_tools, called_tools)
			 VALUES `)
	argIdx := 1
	for i := 0; i < len(records); i++ {
		if i > 0 {
			insertSQL.WriteString(",")
		}
		insertSQL.WriteString("(")
		// 43 target columns; created_at (position 23) is supplied per-record
		// so that UNIQUE(trace_id, created_at) can de-duplicate replays.
		for j := 0; j < 43; j++ {
			if j > 0 {
				insertSQL.WriteString(",")
			}
			fmt.Fprintf(&insertSQL, "$%d", argIdx)
			argIdx++
		}
		insertSQL.WriteString(")")
	}
	insertSQL.WriteString(` ON CONFLICT (trace_id, created_at) DO NOTHING`)

	if _, err := tx.Exec(ctx, insertSQL.String(), insertArgs...); err != nil {
		return fmt.Errorf("bulk insert usage records: %w", err)
	}

	// Bulk UPDATE api_keys stats.
	if len(apiKeyUpdates) > 0 {
		var updateSQL strings.Builder
		updateArgs := make([]interface{}, 0, len(apiKeyUpdates)*3)
		updateSQL.WriteString(`UPDATE api_keys SET total_requests = total_requests + v.requests,
			  total_tokens = total_tokens + v.tokens, updated_at = NOW()
			 FROM (VALUES `)
		idx := 0
		for keyID, agg := range apiKeyUpdates {
			if idx > 0 {
				updateSQL.WriteString(",")
			}
			fmt.Fprintf(&updateSQL, "($%d::uuid, $%d::integer, $%d::bigint)", idx*3+1, idx*3+2, idx*3+3)
			updateArgs = append(updateArgs, keyID, agg.reqs, agg.tokens)
			idx++
		}
		updateSQL.WriteString(`) AS v(id, requests, tokens) WHERE api_keys.id = v.id`)
		if _, err := tx.Exec(ctx, updateSQL.String(), updateArgs...); err != nil {
			return fmt.Errorf("bulk update api keys: %w", err)
		}
	}

	// Per-record subscription counter updates.
	for i, rec := range records {
		p := preparedRows[i]
		if p.userID != "" && p.keyType == "subscription" {
			tokens := int64(rec.InputTokens + rec.OutputTokens)
			if err := incrementSubscriptionCounters(ctx, tx, p.userID, p.subID, tokens, int64(rec.RequestCount)); err != nil {
				return fmt.Errorf("update subscription counters: %w", err)
			}
		}
	}

	return tx.Commit(ctx)
}

// incrementSubscriptionCounters updates all subscription rate-limit counters,
// resetting any counter whose window has expired.
func incrementSubscriptionCounters(ctx context.Context, tx pgx.Tx, userID, keySubID string, tokens, requests int64) error {
	now := time.Now().UTC()
	nextMinute := now.Truncate(time.Minute).Add(time.Minute)
	nextHour := now.Truncate(time.Hour).Add(time.Hour)
	// RP5H: next 5-hour block
	hourBlock := now.Hour() / 5
	nextRp5h := time.Date(now.Year(), now.Month(), now.Day(), (hourBlock+1)*5, 0, 0, 0, time.UTC)
	if !nextRp5h.After(now) {
		nextRp5h = nextRp5h.Add(5 * time.Hour)
	}
	// Weekly (TPW/RPW): next Sunday midnight
	daysUntilSunday := (7 - int(now.Weekday())) % 7
	if daysUntilSunday == 0 {
		daysUntilSunday = 7
	}
	nextWeek := time.Date(now.Year(), now.Month(), now.Day()+daysUntilSunday, 0, 0, 0, 0, time.UTC)

	_, err := tx.Exec(ctx, `
		UPDATE user_subscriptions
		SET tpm_used = CASE WHEN tpm_reset_at IS NULL OR $1 > tpm_reset_at THEN $2 ELSE tpm_used + $2 END,
		    tpm_reset_at = CASE WHEN tpm_reset_at IS NULL OR $1 > tpm_reset_at THEN $3 ELSE tpm_reset_at END,
		    tph_used = CASE WHEN tph_reset_at IS NULL OR $1 > tph_reset_at THEN $2 ELSE tph_used + $2 END,
		    tph_reset_at = CASE WHEN tph_reset_at IS NULL OR $1 > tph_reset_at THEN $4 ELSE tph_reset_at END,
		    tpw_used = CASE WHEN tpw_reset_at IS NULL OR $1 > tpw_reset_at THEN $2 ELSE tpw_used + $2 END,
		    tpw_reset_at = CASE WHEN tpw_reset_at IS NULL OR $1 > tpw_reset_at THEN $5 ELSE tpw_reset_at END,
		    rpm_used = CASE WHEN rpm_reset_at IS NULL OR $1 > rpm_reset_at THEN $6 ELSE rpm_used + $6 END,
		    rpm_reset_at = CASE WHEN rpm_reset_at IS NULL OR $1 > rpm_reset_at THEN $3 ELSE rpm_reset_at END,
		    rph_used = CASE WHEN rph_reset_at IS NULL OR $1 > rph_reset_at THEN $6 ELSE rph_used + $6 END,
		    rph_reset_at = CASE WHEN rph_reset_at IS NULL OR $1 > rph_reset_at THEN $4 ELSE rph_reset_at END,
		    rpw_used = CASE WHEN rpw_reset_at IS NULL OR $1 > rpw_reset_at THEN $6 ELSE rpw_used + $6 END,
		    rpw_reset_at = CASE WHEN rpw_reset_at IS NULL OR $1 > rpw_reset_at THEN $5 ELSE rpw_reset_at END,
		    rp5h_used = CASE WHEN rp5h_reset_at IS NULL OR $1 > rp5h_reset_at THEN $6 ELSE rp5h_used + $6 END,
		    rp5h_reset_at = CASE WHEN rp5h_reset_at IS NULL OR $1 > rp5h_reset_at THEN $7 ELSE rp5h_reset_at END,
		    updated_at = NOW()
		WHERE id = (SELECT id FROM user_subscriptions
		            WHERE user_id = $8::uuid AND status = 'active' AND expires_at > NOW()
		              AND ($9 = '' OR id = $9::uuid)
		            ORDER BY (id = NULLIF($9, '')::uuid) DESC, created_at DESC LIMIT 1)`,
		now, tokens, nextMinute, nextHour, nextWeek, requests, nextRp5h, userID, keySubID,
	)
	return err
}

package routes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// --- Pre-consume (Scheme A: Freeze + Charge-on-Timeout) ---
//
// Flow:
//   1. Proxy calls pre-consume before upstream request
//   2. Portal checks balance, freezes estimated amount in Redis
//   3. After upstream call, Proxy calls settle:
//      - "unfreeze": success/4xx/5xx → release freeze (normal billing handles cost)
//      - "charge": timeout → convert freeze to actual deduction

const (
	freezeKeyPrefix = "billing:freeze:"
	freezeTTL       = 5 * time.Minute // Auto-expire if settle never arrives (proxy crash)
)

// PreConsumeRequest from Proxy
type PreConsumeWebhookRequest struct {
	UserID        string  `json:"userId"`
	KeyHash       string  `json:"keyHash"`
	Model         string  `json:"model"`
	Provider      string  `json:"provider"`
	InputTokens   int     `json:"inputTokens"`
	RequestID     string  `json:"requestId"`
	Margin        float64 `json:"margin"`
	EstimatedCost int64   `json:"estimatedCost"` // micro-USD
}

// SettleWebhookRequest from Proxy
type SettleWebhookRequest struct {
	RequestID   string `json:"requestId"`
	UserID      string `json:"userId"`
	KeyHash     string `json:"keyHash"`
	Model       string `json:"model"`
	Provider    string `json:"provider"`
	PreConsumed int64  `json:"preConsumed"`
	Success     bool   `json:"success"`
	StatusCode  int    `json:"statusCode"`
	ErrorType   string `json:"errorType"`
	LatencyMs   int64  `json:"latencyMs"`
	Action      string `json:"action"` // "unfreeze" or "charge"
}

// freezeData stored in Redis
type freezeData struct {
	UserID       string `json:"userId"`
	KeyHash      string `json:"keyHash"`
	Model        string `json:"model"`
	Provider     string `json:"provider"`
	Amount       int64  `json:"amount"`
	CreatedAt    int64  `json:"createdAt"`
	KeyType      string `json:"keyType,omitempty"`
	EnterpriseID string `json:"enterpriseId,omitempty"`
}

// handlePreConsume checks user balance and freezes estimated cost.
// Returns 402 if balance insufficient. Fail-safe: if Redis unavailable, allow request.
func (r *WebhookRoutes) handlePreConsume(c *gin.Context) {
	r.recordWebhookReceived()

	var req PreConsumeWebhookRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		r.recordWebhookFailed()
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}

	if req.RequestID == "" || req.UserID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "requestId and userId required"}})
		return
	}

	if req.EstimatedCost <= 0 {
		// Nothing to freeze
		c.JSON(http.StatusOK, gin.H{"success": true, "amount": 0, "remaining": 0})
		return
	}

	ctx := c.Request.Context()

	// 1. Get user's available balance — also verify the account is active.
	// Suspended or deleted accounts must not be allowed to consume quota
	// even if a cached key-validation result hasn't expired yet.
	var balance, giftBalance int64
	err := r.db.QueryRow(ctx,
		`SELECT COALESCE(balance, 0), COALESCE(gift_balance, 0) FROM users
		 WHERE id = $1::uuid AND deleted_at IS NULL AND COALESCE(status, 'active') = 'active'`,
		req.UserID,
	).Scan(&balance, &giftBalance)
	if err != nil {
		r.logger.Warn("Pre-consume: user not found or inactive", zap.String("user_id", req.UserID), zap.Error(err))
		// User not found or suspended/deleted — reject the request.
		c.JSON(http.StatusPaymentRequired, gin.H{"error": gin.H{"message": "Account not found or inactive"}})
		return
	}

	// Enterprise keys bill the enterprise ONLY: check enterprise credit
	// (110% cap) instead of the member's personal balance.
	if req.KeyHash != "" {
		var keyType, enterpriseID string
		if err := r.db.QueryRow(ctx,
			`SELECT COALESCE(k.key_type, 'general'),
			        COALESCE(k.enterprise_id::text, COALESCE(u.enterprise_id::text, ''))
			 FROM api_keys k JOIN users u ON u.id = COALESCE(k.assigned_user_id, k.user_id)
			 WHERE k.key_hash = $1 AND k.deleted_at IS NULL LIMIT 1`,
			req.KeyHash,
		).Scan(&keyType, &enterpriseID); err == nil {
			if keyType == "enterprise" {
				r.handleEnterprisePreConsume(c, ctx, req, enterpriseID)
				return
			}
			if keyType == "subscription" {
				// Pre-consume is just a闸口 + 押金 (see Round-3 §11.1):
				// successful requests settle as unfreeze and the actual cost
				// is charged by the usage/billing queue from subscription
				// quota, while timeout/usage_missing charges go through
				// chargeFromFreeze against personal balance — which is wrong
				// for a pure-subscription user who pays in tokens, not USD.
				// Gate them only on their active subscription's remaining
				// quota (mirrors the validate check) and return amount=0 so
				// no personal-balance freeze is created. (S-U9)
				r.handleSubscriptionPreConsume(c, ctx, req)
				return
			}
		}
	}

	totalAvailable := balance + giftBalance

	// 2. Subtract any active freezes for this user
	activeFreezes := r.getActiveFreezeTotal(ctx, req.UserID)
	availableAfterFreeze := totalAvailable - activeFreezes

	// 3. Check if balance is sufficient
	if availableAfterFreeze < req.EstimatedCost {
		c.JSON(http.StatusPaymentRequired, gin.H{
			"success":   true,
			"rejected":  true,
			"amount":    0,
			"remaining": availableAfterFreeze,
		})
		return
	}

	// 4. Store freeze in Redis
	fd := freezeData{
		UserID:    req.UserID,
		KeyHash:   req.KeyHash,
		Model:     req.Model,
		Provider:  req.Provider,
		Amount:    req.EstimatedCost,
		CreatedAt: time.Now().Unix(),
	}
	if err := r.setFreeze(ctx, req.RequestID, req.UserID, &fd); err != nil {
		r.logger.Warn("Pre-consume: Redis freeze failed, allowing request",
			zap.String("request_id", req.RequestID), zap.Error(err))
		// Fail-open: Redis down, allow request without freeze
		c.JSON(http.StatusOK, gin.H{"success": true, "amount": 0, "remaining": availableAfterFreeze})
		return
	}

	r.logger.Info("Pre-consume: balance frozen",
		zap.String("user_id", req.UserID),
		zap.String("request_id", req.RequestID),
		zap.String("model", req.Model),
		zap.Int64("amount", req.EstimatedCost),
		zap.Int64("available_after", availableAfterFreeze-req.EstimatedCost),
	)

	c.JSON(http.StatusOK, gin.H{
		"success":   true,
		"rejected":  false,
		"amount":    req.EstimatedCost,
		"remaining": availableAfterFreeze - req.EstimatedCost,
	})
}

// handleEnterprisePreConsume handles pre-consumption for enterprise keys.
// Enterprise usage bills the enterprise account only: availability is
// enterprise credit with the 110% overflow cap, minus the member's active
// freezes. The freeze itself is still tracked per user (settle is
// key-agnostic).
func (r *WebhookRoutes) handleEnterprisePreConsume(c *gin.Context, ctx context.Context, req PreConsumeWebhookRequest, enterpriseID string) {
	if enterpriseID == "" {
		// Misconfigured enterprise key (no enterprise linkage): reject
		// loudly rather than creating unbillable usage.
		r.logger.Warn("Pre-consume: enterprise key without enterprise linkage",
			zap.String("user_id", req.UserID), zap.String("request_id", req.RequestID))
		c.JSON(http.StatusPaymentRequired, gin.H{"success": true, "rejected": true, "amount": 0, "remaining": 0})
		return
	}

	var creditTotal, creditUsed int64
	if err := r.db.QueryRow(ctx,
		`SELECT COALESCE(credit_total, 0), COALESCE(credit_used, 0) FROM enterprises WHERE id = $1::uuid`,
		enterpriseID,
	).Scan(&creditTotal, &creditUsed); err != nil || creditTotal <= 0 {
		r.logger.Warn("Pre-consume: enterprise credit unavailable",
			zap.String("enterprise_id", enterpriseID), zap.String("user_id", req.UserID))
		c.JSON(http.StatusPaymentRequired, gin.H{"success": true, "rejected": true, "amount": 0, "remaining": 0})
		return
	}

	// 110% overflow cap (10% buffer for in-flight requests), minus this
	// member's active freezes.
	creditCap := creditTotal + creditTotal/10
	activeFreezes := r.getActiveFreezeTotal(ctx, req.UserID)
	available := creditCap - creditUsed - activeFreezes

	// Per-member credit cap: if the enterprise has set a credit_limit for this
	// member, the available amount is further constrained by the member's budget.
	var memberCreditLimit, memberCreditUsed int64
	if err := r.db.QueryRow(ctx,
		`SELECT COALESCE(em.credit_limit, 0), COALESCE(u.credit_used, 0)
		 FROM users u
		 LEFT JOIN enterprise_members em ON em.enterprise_id = u.enterprise_id AND em.user_id = u.id AND em.removed_at IS NULL
		 WHERE u.id = $1::uuid`,
		req.UserID,
	).Scan(&memberCreditLimit, &memberCreditUsed); err == nil && memberCreditLimit > 0 {
		memberAvailable := memberCreditLimit - memberCreditUsed - activeFreezes
		if memberAvailable < available {
			available = memberAvailable
		}
	}

	if available < req.EstimatedCost {
		c.JSON(http.StatusPaymentRequired, gin.H{
			"success":   true,
			"rejected":  true,
			"amount":    0,
			"remaining": available,
		})
		return
	}

	fd := freezeData{
		UserID:       req.UserID,
		KeyHash:      req.KeyHash,
		Model:        req.Model,
		Provider:     req.Provider,
		Amount:       req.EstimatedCost,
		CreatedAt:    time.Now().Unix(),
		KeyType:      "enterprise",
		EnterpriseID: enterpriseID,
	}
	if err := r.setFreeze(ctx, req.RequestID, req.UserID, &fd); err != nil {
		r.logger.Warn("Pre-consume: Redis freeze failed, allowing request",
			zap.String("request_id", req.RequestID), zap.Error(err))
		c.JSON(http.StatusOK, gin.H{"success": true, "amount": 0, "remaining": available})
		return
	}

	r.logger.Info("Pre-consume: enterprise credit frozen",
		zap.String("user_id", req.UserID),
		zap.String("enterprise_id", enterpriseID),
		zap.String("request_id", req.RequestID),
		zap.String("model", req.Model),
		zap.Int64("amount", req.EstimatedCost),
		zap.Int64("available_after", available-req.EstimatedCost),
	)

	c.JSON(http.StatusOK, gin.H{
		"success":   true,
		"rejected":  false,
		"amount":    req.EstimatedCost,
		"remaining": available - req.EstimatedCost,
	})
}

// handleSubscriptionPreConsume gates subscription keys to "an active
// subscription with remaining quota" — but unlike personal/enterprise
// pre-consume it does NOT freeze a USD amount, because subscription cost is
// settled in tokens by the usage/billing queue, not by the USD freeze→charge
// path. Without this branch a pure-subscription user (zero personal balance)
// would be rejected as 402 even though their plan still had unused quota
// (S-U9). Returns success with amount=0; settle becomes a no-op (preConsumed
// == 0 short-circuits in pkg/billing.Settle).
func (r *WebhookRoutes) handleSubscriptionPreConsume(c *gin.Context, ctx context.Context, req PreConsumeWebhookRequest) {
	var hasActive bool
	var quotaRemaining int64
	err := r.db.QueryRow(ctx,
		`SELECT EXISTS (
			  SELECT 1 FROM user_subscriptions s
			  WHERE s.user_id = $1::uuid
			    AND s.status = 'active'
			    AND s.deleted_at IS NULL
			    AND (s.expires_at IS NULL OR s.expires_at > NOW())
			    AND COALESCE(s.quota_remaining, 0) > 0
			),
			COALESCE((
			  SELECT s.quota_remaining FROM user_subscriptions s
			  WHERE s.user_id = $1::uuid
			    AND s.status = 'active'
			    AND s.deleted_at IS NULL
			    AND (s.expires_at IS NULL OR s.expires_at > NOW())
			  ORDER BY s.created_at DESC
			  LIMIT 1
			), 0)`,
		req.UserID,
	).Scan(&hasActive, &quotaRemaining)
	if err != nil || !hasActive {
		// No active subscription with remaining quota — reject so the
		// caller surfaces the error to the user. Keep the 402 semantics
		// so the proxy classifies it as `Rejected` and returns
		// `INSUFFICIENT_BALANCE` (matching the personal-balance path).
		r.logger.Warn("Pre-consume: subscription quota unavailable",
			zap.String("user_id", req.UserID),
			zap.String("request_id", req.RequestID),
			zap.Error(err))
		c.JSON(http.StatusPaymentRequired, gin.H{
			"success":   true,
			"rejected":  true,
			"amount":    0,
			"remaining": 0,
		})
		return
	}

	// No freeze: amount=0 makes the subsequent Settle a no-op
	// (pkg/billing.Settle short-circuits when PreConsumed == 0). The
	// validate gate gate at /api/apikeys/validate already enforces the
	// subscription's per-window limits (TPM/TPMonth/RPM/...); those are
	// authoritative and freshly cached (~5min) on the proxy.
	c.JSON(http.StatusOK, gin.H{
		"success":   true,
		"rejected":  false,
		"amount":    0,
		"remaining": quotaRemaining,
	})
}

// handleSettle resolves a freeze: either release it or convert to a charge.
func (r *WebhookRoutes) handleSettle(c *gin.Context) {
	r.recordWebhookReceived()

	var req SettleWebhookRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		r.recordWebhookFailed()
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}

	if req.RequestID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "requestId required"}})
		return
	}

	ctx := c.Request.Context()

	// 1. Get and delete the freeze
	fd, err := r.getAndDeleteFreeze(ctx, req.RequestID, req.UserID)
	if err != nil || fd == nil {
		// Freeze not found (expired or never set) — nothing to do
		c.JSON(http.StatusOK, gin.H{"success": true, "charged": 0})
		return
	}

	// 2. Execute action
	switch req.Action {
	case "unfreeze":
		// Just release — freeze already deleted above
		r.logger.Info("Settle: freeze released (unfreeze)",
			zap.String("request_id", req.RequestID),
			zap.String("user_id", fd.UserID),
			zap.Int64("amount", fd.Amount),
			zap.String("reason", req.ErrorType),
		)
		c.JSON(http.StatusOK, gin.H{"success": true, "charged": 0})

	case "charge":
		// Convert freeze to actual deduction
		charged, err := r.chargeFromFreeze(ctx, fd, req.RequestID, req.LatencyMs)
		if err != nil {
			r.logger.Error("Settle: charge failed, re-freezing amount",
				zap.String("request_id", req.RequestID),
				zap.String("user_id", fd.UserID),
				zap.Int64("amount", fd.Amount),
				zap.Error(err))
			// Re-create the freeze so the frozen amount is not silently lost.
			if refreezeErr := r.setFreeze(ctx, req.RequestID, fd.UserID, fd); refreezeErr != nil {
				// Re-freeze failed — write to dead-letter for manual recovery.
				r.logger.Error("Settle: re-freeze failed, writing to dead-letter",
					zap.String("request_id", req.RequestID),
					zap.String("user_id", fd.UserID),
					zap.Int64("amount", fd.Amount),
					zap.Error(refreezeErr))
				deadData, _ := json.Marshal(fd)
				_ = r.db.Redis.RPush(ctx, "billing:freeze:dead", deadData).Err()
			}
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "charge failed"})
			return
		}
		r.logger.Info("Settle: freeze converted to charge (timeout)",
			zap.String("request_id", req.RequestID),
			zap.String("user_id", fd.UserID),
			zap.Int64("charged", charged),
			zap.String("model", fd.Model),
		)
		c.JSON(http.StatusOK, gin.H{"success": true, "charged": charged})

	default:
		// Unknown action — treat as unfreeze (safe default)
		r.logger.Warn("Settle: unknown action, treating as unfreeze",
			zap.String("action", req.Action), zap.String("request_id", req.RequestID))
		c.JSON(http.StatusOK, gin.H{"success": true, "charged": 0})
	}
}

// --- Redis helpers ---

func freezeKey(requestID string) string {
	return freezeKeyPrefix + requestID
}

func userFreezeSetKey(userID string) string {
	return "billing:freeze-set:" + userID
}

func (r *WebhookRoutes) setFreeze(ctx context.Context, requestID, userID string, fd *freezeData) error {
	if r.db.Redis == nil {
		return fmt.Errorf("redis not available")
	}

	data, _ := json.Marshal(fd)

	pipe := r.db.Redis.Pipeline()
	// Store freeze data with TTL
	pipe.Set(ctx, freezeKey(requestID), data, freezeTTL)
	// Add to user's freeze set (for sum calculation)
	pipe.SAdd(ctx, userFreezeSetKey(userID), requestID)
	pipe.Expire(ctx, userFreezeSetKey(userID), freezeTTL+time.Minute)
	_, err := pipe.Exec(ctx)
	return err
}

func (r *WebhookRoutes) getAndDeleteFreeze(ctx context.Context, requestID, userID string) (*freezeData, error) {
	if r.db.Redis == nil {
		return nil, fmt.Errorf("redis not available")
	}

	// Atomically get and delete the freeze to eliminate the double-charge
	// window that exists with non-atomic GET+DEL under concurrent settle calls.
	// GETDEL is available since Redis 6.2.
	data, err := r.db.Redis.GetDel(ctx, freezeKey(requestID)).Bytes()
	if err != nil {
		if err == redis.Nil {
			return nil, nil // Not found (expired or already settled)
		}
		return nil, err
	}

	var fd freezeData
	if err := json.Unmarshal(data, &fd); err != nil {
		return nil, err
	}

	// Remove from user set (freeze key already deleted atomically by GetDel)
	pipe := r.db.Redis.Pipeline()
	if userID != "" {
		pipe.SRem(ctx, userFreezeSetKey(userID), requestID)
	} else if fd.UserID != "" {
		pipe.SRem(ctx, userFreezeSetKey(fd.UserID), requestID)
	}
	_, _ = pipe.Exec(ctx)

	return &fd, nil
}

func (r *WebhookRoutes) getActiveFreezeTotal(ctx context.Context, userID string) int64 {
	if r.db.Redis == nil {
		return 0
	}

	// Get all request IDs in user's freeze set
	requestIDs, err := r.db.Redis.SMembers(ctx, userFreezeSetKey(userID)).Result()
	if err != nil || len(requestIDs) == 0 {
		return 0
	}

	// Batch fetch all freeze keys in one round-trip (MGET)
	keys := make([]string, len(requestIDs))
	for i, reqID := range requestIDs {
		keys[i] = freezeKey(reqID)
	}
	results, err := r.db.Redis.MGet(ctx, keys...).Result()
	if err != nil {
		return 0
	}

	var total int64
	var expired []interface{}
	for i, val := range results {
		if val == nil {
			// Key expired, mark for cleanup
			expired = append(expired, requestIDs[i])
			continue
		}
		data, ok := val.(string)
		if !ok {
			continue
		}
		var fd freezeData
		if json.Unmarshal([]byte(data), &fd) == nil {
			total += fd.Amount
		}
	}
	// Batch cleanup expired entries from the set
	if len(expired) > 0 {
		r.db.Redis.SRem(ctx, userFreezeSetKey(userID), expired...)
	}
	return total
}

// chargeFromFreeze deducts the frozen amount from the appropriate account.
// Enterprise keys charge the enterprise credit; personal keys charge user balance.
func (r *WebhookRoutes) chargeFromFreeze(ctx context.Context, fd *freezeData, requestID string, latencyMs int64) (int64, error) {
	amount := fd.Amount
	if amount <= 0 {
		return 0, nil
	}

	// Enterprise key: deduct from enterprise credit instead of personal funds
	if fd.KeyType == "enterprise" && fd.EnterpriseID != "" {
		return r.chargeFromFreezeEnterprise(ctx, fd, requestID, latencyMs)
	}

	// Deduct from balance (gift first, then main balance)
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Attribute the charge to the API key that made the request (frozen key
	// hash), so usage analytics such as key distribution stay complete even
	// when the pre-consume webhook times out. Deleted keys are still matched.
	var keyID, keyName, keyPrefix string
	_ = tx.QueryRow(ctx,
		`SELECT id::text, COALESCE(name, ''), COALESCE(key_prefix, '')
		 FROM api_keys WHERE key_hash = $1 ORDER BY created_at DESC LIMIT 1`,
		fd.KeyHash,
	).Scan(&keyID, &keyName, &keyPrefix)

	var giftBalance, balance int64
	err = tx.QueryRow(ctx,
		`SELECT COALESCE(gift_balance, 0), COALESCE(balance, 0)
		 FROM users WHERE id = $1::uuid FOR UPDATE`,
		fd.UserID,
	).Scan(&giftBalance, &balance)
	if err != nil {
		return 0, fmt.Errorf("lock user: %w", err)
	}

	var giftDeduction, balanceDeduction int64
	remaining := amount

	// Gift balance first
	if giftBalance > 0 && remaining > 0 {
		if giftBalance >= remaining {
			giftDeduction = remaining
			remaining = 0
		} else {
			giftDeduction = giftBalance
			remaining -= giftBalance
		}
	}
	// Main balance
	if remaining > 0 {
		balanceDeduction = remaining
		if balanceDeduction > balance {
			balanceDeduction = balance // Don't go negative
		}
	}

	if unpaid := remaining - balanceDeduction; unpaid > 0 {
		r.logger.Warn("Personal balance insufficient for timeout charge",
			zap.String("user_id", fd.UserID),
			zap.Int64("unpaid", unpaid))
	}

	// Update balances
	if giftDeduction > 0 || balanceDeduction > 0 {
		_, err = tx.Exec(ctx,
			`UPDATE users SET
			 gift_balance = GREATEST(gift_balance - $1, 0),
			 balance = GREATEST(balance - $2, 0),
			 updated_at = NOW()
			 WHERE id = $3::uuid`,
			giftDeduction, balanceDeduction, fd.UserID,
		)
		if err != nil {
			return 0, fmt.Errorf("deduct balance: %w", err)
		}
	}

	// Insert usage record for audit trail, attributed to the requesting key
	// (resolved from the frozen key hash above).
	_, err = tx.Exec(ctx,
		`INSERT INTO usage_records (
			trace_id, user_id, model, provider,
			input_tokens, output_tokens, total_cost,
			gift_deduction, balance_deduction,
			user_api_key_id, key_prefix, api_key_name,
			request_count, latency_ms, error, created_at
		) VALUES (
			$1, $2::uuid, $3, $4,
			$5, 0, $6,
			$7, $8,
			NULLIF($10, '')::uuid, $11, NULLIF($12, ''),
			1, $9, 'timeout_pre_consume_charge', NOW()
		)`,
		requestID, fd.UserID, fd.Model, fd.Provider,
		0, amount, // input_tokens=0 (estimated), total_cost=frozen amount
		giftDeduction, balanceDeduction,
		latencyMs,
		keyID, keyPrefix, keyName,
	)
	if err != nil {
		// Fatal: an INSERT failure aborts the transaction, so the whole settle
		// rolls back and returns 500; the freeze is re-applied and later
		// released by its TTL.
		return 0, fmt.Errorf("insert usage record: %w", err)
	}

	// Balance transaction log
	if giftDeduction > 0 {
		_, _ = tx.Exec(ctx,
			`INSERT INTO balance_transactions (user_id, transaction_type, amount, balance_type, balance_before, balance_after, reference_id, description)
			 VALUES ($1::uuid, 'usage', $2, 'gift', $3, $4, $5, 'Timeout pre-consume charge')`,
			fd.UserID, -giftDeduction, giftBalance, giftBalance-giftDeduction, requestID)
	}
	if balanceDeduction > 0 {
		_, _ = tx.Exec(ctx,
			`INSERT INTO balance_transactions (user_id, transaction_type, amount, balance_type, balance_before, balance_after, reference_id, description)
			 VALUES ($1::uuid, 'usage', $2, 'balance', $3, $4, $5, 'Timeout pre-consume charge')`,
			fd.UserID, -balanceDeduction, balance, balance-balanceDeduction, requestID)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}

	return giftDeduction + balanceDeduction, nil
}

// chargeFromFreezeEnterprise deducts the frozen amount from enterprise credit.
func (r *WebhookRoutes) chargeFromFreezeEnterprise(ctx context.Context, fd *freezeData, requestID string, latencyMs int64) (int64, error) {
	amount := fd.Amount

	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Attribute the charge to the enterprise API key that made the request
	// (frozen key hash), so key distribution analytics stay complete even on
	// timeout. Deleted keys are still matched.
	var keyID, keyName, keyPrefix string
	_ = tx.QueryRow(ctx,
		`SELECT id::text, COALESCE(name, ''), COALESCE(key_prefix, '')
		 FROM api_keys WHERE key_hash = $1 ORDER BY created_at DESC LIMIT 1`,
		fd.KeyHash,
	).Scan(&keyID, &keyName, &keyPrefix)

	// Get enterprise credit info
	var creditTotal, creditUsed int64
	err = tx.QueryRow(ctx,
		`SELECT COALESCE(credit_total, 0), COALESCE(credit_used, 0)
		 FROM enterprises WHERE id = $1::uuid FOR UPDATE`,
		fd.EnterpriseID,
	).Scan(&creditTotal, &creditUsed)
	if err != nil {
		return 0, fmt.Errorf("get enterprise credit: %w", err)
	}

	// Allow up to 110% overflow (same as billing path)
	creditCap := creditTotal + creditTotal/10
	creditRemaining := creditCap - creditUsed
	creditDeduction := amount
	if creditDeduction > creditRemaining {
		creditDeduction = creditRemaining
	}
	if creditDeduction < 0 {
		creditDeduction = 0
	}

	if creditDeduction > 0 {
		_, err = tx.Exec(ctx,
			`UPDATE enterprises SET credit_used = credit_used + $1, updated_at = NOW()
			 WHERE id = $2::uuid`,
			creditDeduction, fd.EnterpriseID,
		)
		if err != nil {
			return 0, fmt.Errorf("deduct enterprise credit: %w", err)
		}
	}

	// Track per-member credit usage for per-member credit cap enforcement.
	// The frozen amount is the best estimate of member spend for a timeout.
	if amount > 0 {
		if _, err := tx.Exec(ctx,
			`UPDATE users SET credit_used = COALESCE(credit_used, 0) + $1, updated_at = NOW()
			 WHERE id = $2::uuid`,
			amount, fd.UserID); err != nil {
			return 0, fmt.Errorf("update member credit_used: %w", err)
		}
	}

	if unpaid := amount - creditDeduction; unpaid > 0 {
		r.logger.Warn("Enterprise credit insufficient for timeout charge",
			zap.String("user_id", fd.UserID),
			zap.String("enterprise_id", fd.EnterpriseID),
			zap.Int64("unpaid", unpaid))
	}

	// Insert usage record for audit trail, attributed to the requesting key
	// (resolved from the frozen key hash above).
	_, err = tx.Exec(ctx,
		`INSERT INTO usage_records (
			trace_id, user_id, model, provider,
			input_tokens, output_tokens, total_cost,
			credit_deduction,
			user_api_key_id, key_prefix, api_key_name,
			request_count, latency_ms, error, created_at
		) VALUES (
			$1, $2::uuid, $3, $4,
			$5, 0, $6,
			$7,
			NULLIF($9, '')::uuid, $10, NULLIF($11, ''),
			1, $8, 'timeout_pre_consume_charge', NOW()
		)`,
		requestID, fd.UserID, fd.Model, fd.Provider,
		0, amount,
		creditDeduction,
		latencyMs,
		keyID, keyPrefix, keyName,
	)
	if err != nil {
		// Fatal: an INSERT failure aborts the transaction, so the whole settle
		// rolls back and returns 500; the freeze is re-applied and later
		// released by its TTL.
		return 0, fmt.Errorf("insert usage record: %w", err)
	}

	// Balance transaction log for enterprise credit
	if creditDeduction > 0 {
		_, _ = tx.Exec(ctx,
			`INSERT INTO balance_transactions (user_id, transaction_type, amount, balance_type, balance_before, balance_after, reference_id, description)
			 VALUES ($1::uuid, 'usage', $2, 'credit', $3, $4, $5, 'Timeout pre-consume charge (enterprise)')`,
			fd.UserID, -creditDeduction, creditUsed, creditUsed+creditDeduction, requestID)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}

	// Invalidate cached validation results so the per-member credit cap is
	// visible on the next validate/pre-consume check.
	if amount > 0 {
		invalidateUserValidateCaches(ctx, r.db, r.logger, fd.UserID)
	}

	return creditDeduction, nil
}

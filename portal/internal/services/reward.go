package services

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ai-proxy/ai-portal-go/internal/db"
	"github.com/ai-proxy/ai-portal-go/pkg/audit"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

// RewardService handles welcome bonus and invitation code logic using the unified vouchers table.
type RewardService struct {
	db     *db.DB
	logger *zap.Logger
	audit  *audit.Logger
}

// NewRewardService creates a new reward service.
func NewRewardService(db *db.DB, logger *zap.Logger, audit *audit.Logger) *RewardService {
	return &RewardService{db: db, logger: logger, audit: audit}
}

// ApplyWelcomeBonus applies the welcome bonus to a newly registered user.
// It finds the active welcome voucher, updates the user's balance,
// records the use, and increments the voucher's used_count.
func (s *RewardService) ApplyWelcomeBonus(ctx context.Context, userID, email string) {
	var voucherID, code string
	var amount int64
	var expiresAt *time.Time
	err := s.db.QueryRow(ctx,
		`SELECT id::text, code, amount, expires_at FROM vouchers
		 WHERE type = 'welcome' AND status = 'active' AND deleted_at IS NULL
		 ORDER BY created_at DESC LIMIT 1`,
	).Scan(&voucherID, &code, &amount, &expiresAt)
	if err != nil {
		s.logger.Warn("No active welcome voucher found", zap.String("email", email), zap.Error(err))
		return
	}
	if expiresAt != nil && expiresAt.Before(time.Now()) {
		s.logger.Warn("Welcome voucher expired", zap.String("email", email), zap.String("code", code))
		return
	}

	err = s.db.WithTx(ctx, func(tx pgx.Tx) error {
		var giftBalanceBefore int64
		if err := tx.QueryRow(ctx, `SELECT COALESCE(gift_balance, 0) FROM users WHERE id = $1::uuid`, userID).Scan(&giftBalanceBefore); err != nil {
			return fmt.Errorf("query gift_balance: %w", err)
		}

		// Plan B: promo credits go to gift_balance only; balance = real funds
		if _, err := tx.Exec(ctx,
			`UPDATE users SET gift_balance = COALESCE(gift_balance, 0) + $1, updated_at = NOW() WHERE id = $2::uuid`,
			amount, userID); err != nil {
			return fmt.Errorf("update user gift_balance: %w", err)
		}

		if _, err := tx.Exec(ctx,
			`INSERT INTO voucher_uses (voucher_id, user_id, consumer_type, amount, balance_before, balance_after)
			 VALUES ($1::uuid, $2::uuid, 'redeemer', $3, $4, $5)`,
			voucherID, userID, amount, giftBalanceBefore, giftBalanceBefore+amount); err != nil {
			return fmt.Errorf("insert voucher_use: %w", err)
		}

		if _, err := tx.Exec(ctx,
			`UPDATE vouchers SET used_count = used_count + 1, updated_at = NOW() WHERE id = $1::uuid`,
			voucherID); err != nil {
			return fmt.Errorf("update voucher used_count: %w", err)
		}

		return nil
	})
	if err != nil {
		s.logger.Error("Welcome bonus failed", zap.String("user_id", userID), zap.String("email", email), zap.Error(err))
		return
	}

	s.logger.Info("Welcome bonus applied", zap.String("user_id", userID), zap.String("email", email), zap.String("code", code), zap.Int64("amount", amount))
}

// ProcessInvitationCode handles referral code processing during registration.
// It validates the voucher, records the use, and grants rewards to both inviter and invitee.
func (s *RewardService) ProcessInvitationCode(ctx context.Context, userID, invitationCode string) {
	if invitationCode == "" {
		return
	}

	var voucherID, inviterID, rewardConfigJSON string
	var amount int64
	var maxUses, usedCount int
	var status string
	var expiresAt *time.Time
	err := s.db.QueryRow(ctx,
		`SELECT id::text, user_id::text, amount, reward_config, max_uses, used_count, status, expires_at
		 FROM vouchers WHERE code = $1 AND type = 'referral' AND deleted_at IS NULL`, invitationCode).Scan(
		&voucherID, &inviterID, &amount, &rewardConfigJSON, &maxUses, &usedCount, &status, &expiresAt)
	if err != nil || status != "active" {
		s.logger.Debug("Invalid referral code", zap.String("code", invitationCode))
		return
	}
	if expiresAt != nil && expiresAt.Before(time.Now()) {
		s.logger.Debug("Referral code expired", zap.String("code", invitationCode))
		return
	}
	if maxUses > 0 && usedCount >= maxUses {
		s.logger.Debug("Referral code max uses reached", zap.String("code", invitationCode))
		return
	}
	if inviterID == "" {
		s.logger.Debug("Referral code has no inviter", zap.String("code", invitationCode))
		return
	}
	if inviterID == userID {
		s.logger.Debug("User cannot use own referral code", zap.String("code", invitationCode))
		return
	}

	// Parse reward_config
	var rewardConfig struct {
		Inviter struct {
			Amount      int64  `json:"amount"`
			Destination string `json:"destination"`
		} `json:"inviter"`
		Invitee struct {
			Amount      int64  `json:"amount"`
			Destination string `json:"destination"`
		} `json:"invitee"`
	}
	if err := json.Unmarshal([]byte(rewardConfigJSON), &rewardConfig); err != nil {
		s.logger.Warn("Failed to parse referral reward_config", zap.String("code", invitationCode), zap.Error(err))
		// Fall back to using the voucher amount for invitee and zero for inviter
		rewardConfig.Invitee.Amount = amount
	}

	applied := true
	err = s.db.WithTx(ctx, func(tx pgx.Tx) error {
		// Lock the voucher row and revalidate redemption limits inside the transaction
		// so concurrent registrations cannot over-redeem the same referral code.
		var lockedMaxUses, lockedUsedCount int
		var lockedStatus string
		var lockedExpiresAt *time.Time
		if err := tx.QueryRow(ctx,
			`SELECT max_uses, used_count, status, expires_at
			 FROM vouchers WHERE id = $1::uuid FOR UPDATE`, voucherID).Scan(
			&lockedMaxUses, &lockedUsedCount, &lockedStatus, &lockedExpiresAt); err != nil {
			return fmt.Errorf("lock voucher: %w", err)
		}
		if lockedStatus != "active" || (lockedExpiresAt != nil && lockedExpiresAt.Before(time.Now())) || (lockedMaxUses > 0 && lockedUsedCount >= lockedMaxUses) {
			applied = false
			return nil
		}

		// Mark inviter_id on user
		if _, err := tx.Exec(ctx, "UPDATE users SET inviter_id = $1::uuid WHERE id = $2::uuid", inviterID, userID); err != nil {
			return fmt.Errorf("set inviter_id: %w", err)
		}

		// Grant invitee reward (Plan B: gift_balance only)
		if rewardConfig.Invitee.Amount > 0 {
			if _, err := tx.Exec(ctx,
				`UPDATE users SET gift_balance = COALESCE(gift_balance, 0) + $1, updated_at = NOW() WHERE id = $2::uuid`,
				rewardConfig.Invitee.Amount, userID); err != nil {
				return fmt.Errorf("grant invitee reward: %w", err)
			}
		}

		// Grant inviter reward (Plan B: gift_balance only)
		if rewardConfig.Inviter.Amount > 0 {
			if _, err := tx.Exec(ctx,
				`UPDATE users SET gift_balance = COALESCE(gift_balance, 0) + $1, aff_history_quota = aff_history_quota + $1, updated_at = NOW() WHERE id = $2::uuid`,
				rewardConfig.Inviter.Amount, inviterID); err != nil {
				return fmt.Errorf("grant inviter reward: %w", err)
			}
		}

		// Record voucher use for invitee
		var inviteeBalanceBefore int64
		_ = tx.QueryRow(ctx, `SELECT COALESCE(gift_balance, 0) FROM users WHERE id = $1::uuid`, userID).Scan(&inviteeBalanceBefore)
		if rewardConfig.Invitee.Amount > 0 {
			if _, err := tx.Exec(ctx,
				`INSERT INTO voucher_uses (voucher_id, user_id, consumer_type, amount, balance_before, balance_after)
				 VALUES ($1::uuid, $2::uuid, 'invitee', $3, $4, $5)`,
				voucherID, userID, rewardConfig.Invitee.Amount, inviteeBalanceBefore, inviteeBalanceBefore+rewardConfig.Invitee.Amount); err != nil {
				return fmt.Errorf("record invitee use: %w", err)
			}
		}

		// Record voucher use for inviter
		if rewardConfig.Inviter.Amount > 0 {
			var inviterBalanceBefore int64
			_ = tx.QueryRow(ctx, `SELECT COALESCE(gift_balance, 0) FROM users WHERE id = $1::uuid`, inviterID).Scan(&inviterBalanceBefore)
			if _, err := tx.Exec(ctx,
				`INSERT INTO voucher_uses (voucher_id, user_id, consumer_type, amount, balance_before, balance_after)
				 VALUES ($1::uuid, $2::uuid, 'inviter', $3, $4, $5)`,
				voucherID, inviterID, rewardConfig.Inviter.Amount, inviterBalanceBefore, inviterBalanceBefore+rewardConfig.Inviter.Amount); err != nil {
				return fmt.Errorf("record inviter use: %w", err)
			}
		}

		// Update voucher used_count
		if _, err := tx.Exec(ctx,
			`UPDATE vouchers SET used_count = used_count + 1, updated_at = NOW() WHERE id = $1::uuid`,
			voucherID); err != nil {
			return fmt.Errorf("update voucher used_count: %w", err)
		}

		return nil
	})
	if err != nil {
		s.logger.Error("Invitation code processing failed", zap.String("code", invitationCode), zap.String("user_id", userID), zap.Error(err))
		return
	}
	if !applied {
		s.logger.Debug("Referral code no longer applicable", zap.String("code", invitationCode), zap.String("user_id", userID))
		return
	}

	if s.audit != nil {
		s.audit.Log(ctx, userID, "", "auth", "invitation_used", "info", "User registered with referral code: "+invitationCode)
	}
	s.logger.Info("Referral code processed", zap.String("code", invitationCode), zap.String("user_id", userID))
}

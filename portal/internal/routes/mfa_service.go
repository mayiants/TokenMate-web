package routes

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mayiants/ai-portal-go/internal/config"
	"github.com/mayiants/ai-portal-go/internal/db"
	"github.com/mayiants/ai-portal-go/internal/middleware"
	"github.com/mayiants/ai-portal-go/pkg/audit"
	"github.com/mayiants/ai-portal-go/pkg/crypto"
	"github.com/mayiants/ai-portal-go/pkg/totp"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

const (
	// mfaIssuer is the label authenticator apps show next to the account.
	mfaIssuer = "AI Portal"

	// Brute-force limits for every place a second factor (or the password that
	// guards a sensitive MFA change) is checked: 5 failures per 10 minutes per
	// user, then MFA attempts are locked for 15 minutes. The counter is per user,
	// not per IP, because an attacker holding a stolen password can rotate IPs.
	mfaMaxFailures = 5
	mfaFailWindow  = 10 * time.Minute
	mfaLockout     = 15 * time.Minute

	// recoveryCodeCount codes of 80 random bits each are issued at enrolment.
	recoveryCodeCount = 10
	recoveryCodeBytes = 10 // 80 bits -> 16 base32 characters
)

// mfaMinFailureDuration pads every failed verification to the same minimum
// latency so wrong code / locked / unknown challenge are not distinguishable by
// timing. Tests set it to zero.
var mfaMinFailureDuration = 250 * time.Millisecond

// mfaNow is the clock used for TOTP validation; tests advance it to spend
// several distinct time steps without sleeping.
var mfaNow = time.Now

var (
	// ErrMFAInvalid is the single, generic failure for wrong code, replayed code,
	// used recovery code, locked-out user and bad/expired challenge.
	ErrMFAInvalid = errors.New("invalid verification code")
	// ErrMFAUnavailable means the limiter/challenge store is unusable; MFA checks
	// fail closed rather than skip the brute-force protection.
	ErrMFAUnavailable = errors.New("mfa temporarily unavailable")
	// ErrMFAPasswordIncorrect is returned by sensitive changes for a wrong password.
	ErrMFAPasswordIncorrect = errors.New("current password is incorrect")
	// ErrMFANotEnabled / ErrMFAAlreadyEnabled guard the enrolment state machine.
	ErrMFANotEnabled     = errors.New("mfa is not enabled")
	ErrMFAAlreadyEnabled = errors.New("mfa is already enabled")
	// ErrMFANoPendingSetup: enable was called before setup.
	ErrMFANoPendingSetup = errors.New("mfa setup has not been started")
	// ErrMFARequiredByPolicy: the user may not turn MFA off while a policy requires it.
	ErrMFARequiredByPolicy = errors.New("mfa is required by policy")
)

// SecondFactor carries what the user typed: a TOTP code or a recovery code.
type SecondFactor struct {
	Code         string
	RecoveryCode string
}

// MFAStatus is the user-visible enrolment state.
type MFAStatus struct {
	Enabled           bool       `json:"enabled"`
	EnabledAt         *time.Time `json:"enabled_at"`
	SetupPending      bool       `json:"setup_pending"`
	RecoveryRemaining int        `json:"recovery_codes_remaining"`
	Required          bool       `json:"required"`
}

// MFAService holds all TOTP business logic. It is shared by the login flow
// (AuthRoutes), the profile management API and the admin reset endpoints so
// there is exactly one implementation of verification, limiting and auditing.
type MFAService struct {
	db     *db.DB
	encKey string
	logger *zap.Logger
	audit  *audit.Logger
}

// encryptionKeyOf tolerates a nil config (unit tests construct routes without one).
func encryptionKeyOf(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	return cfg.Auth.EncryptionKey
}

// NewMFAService creates the service. encKey is the configured ENCRYPTION_KEY.
func NewMFAService(database *db.DB, encKey string, logger *zap.Logger, auditLog *audit.Logger) *MFAService {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &MFAService{db: database, encKey: encKey, logger: logger, audit: auditLog}
}

func (s *MFAService) log(ctx context.Context, userID, category, action, level, msg string) {
	if s.audit == nil {
		return
	}
	// Email is resolved from the user id by the audit logger.
	s.audit.Log(ctx, userID, "", category, action, level, msg)
}

// ---- brute-force limiter -------------------------------------------------

func mfaFailKey(userID string) string { return "mfa:fail:" + userID }
func mfaLockKey(userID string) string { return "mfa:lock:" + userID }

func (s *MFAService) isLocked(ctx context.Context, userID string) (bool, error) {
	if s.db == nil || s.db.Redis == nil {
		return false, ErrMFAUnavailable
	}
	n, err := s.db.Redis.Exists(ctx, mfaLockKey(userID)).Result()
	if err != nil {
		return false, ErrMFAUnavailable
	}
	return n > 0, nil
}

// recordFailure counts a failure and starts the lockout on the 5th one.
func (s *MFAService) recordFailure(ctx context.Context, userID string) {
	if s.db == nil || s.db.Redis == nil {
		return
	}
	n, err := s.db.Redis.Incr(ctx, mfaFailKey(userID)).Result()
	if err != nil {
		return
	}
	if n == 1 {
		_ = s.db.Redis.Expire(ctx, mfaFailKey(userID), mfaFailWindow).Err()
	}
	if n >= mfaMaxFailures {
		_ = s.db.Redis.Set(ctx, mfaLockKey(userID), "1", mfaLockout).Err()
		_ = s.db.Redis.Del(ctx, mfaFailKey(userID)).Err()
		s.log(ctx, userID, "auth", "mfa_locked", "warning",
			fmt.Sprintf("MFA attempts locked for %d minutes after %d failures", int(mfaLockout.Minutes()), mfaMaxFailures))
	}
}

func (s *MFAService) clearFailures(ctx context.Context, userID string) {
	if s.db != nil && s.db.Redis != nil {
		_ = s.db.Redis.Del(ctx, mfaFailKey(userID), mfaLockKey(userID)).Err()
	}
}

func padFailure(start time.Time) {
	if d := mfaMinFailureDuration - time.Since(start); d > 0 {
		time.Sleep(d)
	}
}

// ---- secret handling -----------------------------------------------------

func (s *MFAService) loadSecret(ctx context.Context, userID string, wantEnabled bool) (secret string, lastStep int64, err error) {
	var enc *string
	var enabled bool
	if err = s.db.QueryRow(ctx,
		`SELECT totp_secret_enc, COALESCE(totp_enabled, false), COALESCE(totp_last_step, 0)
		   FROM users WHERE id = $1::uuid AND deleted_at IS NULL`, userID,
	).Scan(&enc, &enabled, &lastStep); err != nil {
		return "", 0, ErrMFAInvalid
	}
	if enabled != wantEnabled || enc == nil || *enc == "" {
		return "", 0, ErrMFAInvalid
	}
	secret, err = crypto.Decrypt(*enc, s.encKey)
	if err != nil {
		// Wrong ENCRYPTION_KEY or corrupt value: fail closed, never skip MFA.
		s.logger.Error("Failed to decrypt TOTP secret", zap.String("user_id", userID), zap.Error(err))
		return "", 0, ErrMFAInvalid
	}
	return secret, lastStep, nil
}

// checkTOTP validates code against the stored secret and records the step with
// a compare-and-set so two concurrent requests cannot both spend one code.
func (s *MFAService) checkTOTP(ctx context.Context, userID, code string, wantEnabled bool) bool {
	secret, last, err := s.loadSecret(ctx, userID, wantEnabled)
	if err != nil {
		return false
	}
	step, ok := totp.Validate(secret, code, mfaNow(), last)
	if !ok {
		return false
	}
	tag, err := s.db.Pool.Exec(ctx,
		`UPDATE users SET totp_last_step = $2 WHERE id = $1::uuid AND COALESCE(totp_last_step, 0) < $2`, userID, step)
	return err == nil && tag.RowsAffected() == 1
}

// ---- recovery codes ------------------------------------------------------

var recoveryEnc = base32.StdEncoding.WithPadding(base32.NoPadding)

func normalizeRecoveryCode(code string) string {
	return strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(code)))
}

func hashRecoveryCode(normalized string) string {
	sum := sha256.Sum256([]byte("recovery:" + normalized))
	return hex.EncodeToString(sum[:])
}

// generateRecoveryCodes returns display codes (XXXX-XXXX-XXXX-XXXX, 80 random
// bits each) together with their storage hashes.
func generateRecoveryCodes(n int) (plain, hashes []string, err error) {
	for i := 0; i < n; i++ {
		raw := make([]byte, recoveryCodeBytes)
		if _, err := rand.Read(raw); err != nil {
			return nil, nil, err
		}
		c := recoveryEnc.EncodeToString(raw) // 16 chars
		plain = append(plain, c[0:4]+"-"+c[4:8]+"-"+c[8:12]+"-"+c[12:16])
		hashes = append(hashes, hashRecoveryCode(c))
	}
	return plain, hashes, nil
}

func (s *MFAService) consumeRecoveryCode(ctx context.Context, userID, code string) bool {
	n := normalizeRecoveryCode(code)
	if len(n) != 16 {
		return false
	}
	tag, err := s.db.Pool.Exec(ctx,
		`UPDATE user_recovery_codes SET used_at = NOW()
		  WHERE user_id = $1::uuid AND code_hash = $2 AND used_at IS NULL`, userID, hashRecoveryCode(n))
	return err == nil && tag.RowsAffected() == 1
}

// replaceRecoveryCodes swaps the user's codes inside tx and returns the new
// plaintext codes (the only time they exist).
func replaceRecoveryCodes(ctx context.Context, tx pgx.Tx, userID string) ([]string, error) {
	plain, hashes, err := generateRecoveryCodes(recoveryCodeCount)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM user_recovery_codes WHERE user_id = $1::uuid`, userID); err != nil {
		return nil, err
	}
	for _, h := range hashes {
		if _, err := tx.Exec(ctx,
			`INSERT INTO user_recovery_codes (user_id, code_hash) VALUES ($1::uuid, $2)`, userID, h); err != nil {
			return nil, err
		}
	}
	return plain, nil
}

// ---- verification --------------------------------------------------------

func looksLikeTOTP(code string) bool {
	c := totp.NormalizeCode(code)
	if len(c) != totp.Digits {
		return false
	}
	for _, r := range c {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// VerifySecondFactor checks a TOTP or recovery code for an MFA-enabled user.
// It enforces the lockout, counts failures, pads failure latency and writes the
// auth/mfa_failed and auth/mfa_success audit events. purpose is free text for
// the audit message ("login", "disable", ...). The error is always the generic
// ErrMFAInvalid except when the limiter store itself is down.
func (s *MFAService) VerifySecondFactor(ctx context.Context, userID string, in SecondFactor, purpose string) (err error) {
	start := time.Now()
	defer func() {
		if err != nil {
			padFailure(start)
		}
	}()

	locked, lerr := s.isLocked(ctx, userID)
	if lerr != nil {
		return lerr
	}
	if locked {
		s.log(ctx, userID, "auth", "mfa_failed", "warning", fmt.Sprintf("MFA %s attempt rejected: user is locked out", purpose))
		return ErrMFAInvalid
	}

	ok := false
	method := "totp"
	switch {
	case in.RecoveryCode != "":
		method = "recovery_code"
		ok = s.consumeRecoveryCode(ctx, userID, in.RecoveryCode)
	case looksLikeTOTP(in.Code):
		ok = s.checkTOTP(ctx, userID, in.Code, true)
	case in.Code != "":
		// The login form has one input; anything that is not 6 digits is tried
		// as a recovery code.
		method = "recovery_code"
		ok = s.consumeRecoveryCode(ctx, userID, in.Code)
	}

	if !ok {
		s.recordFailure(ctx, userID)
		s.log(ctx, userID, "auth", "mfa_failed", "warning", fmt.Sprintf("MFA %s verification failed (%s)", purpose, method))
		return ErrMFAInvalid
	}
	s.clearFailures(ctx, userID)
	s.log(ctx, userID, "auth", "mfa_success", "info", fmt.Sprintf("MFA %s verification succeeded (%s)", purpose, method))
	if method == "recovery_code" {
		s.log(ctx, userID, "security", "mfa_recovery_code_used", "warning", "A recovery code was used")
	}
	return nil
}

// verifyPassword checks the current password for sensitive changes. Failures
// count against the same limiter as wrong codes: a stolen session must not be
// able to guess the password unthrottled through these endpoints.
func (s *MFAService) verifyPassword(ctx context.Context, userID, password string) bool {
	var hash string
	if err := s.db.QueryRow(ctx,
		`SELECT password_hash FROM users WHERE id = $1::uuid AND deleted_at IS NULL`, userID).Scan(&hash); err != nil {
		// Keep timing close to the real path.
		_ = bcrypt.CompareHashAndPassword([]byte("$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"), []byte(password))
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// guardedPassword runs the lockout check, then the password check, counting a
// wrong password as a failure.
func (s *MFAService) guardedPassword(ctx context.Context, userID, password, purpose string) (err error) {
	start := time.Now()
	defer func() {
		if err != nil {
			padFailure(start)
		}
	}()
	locked, lerr := s.isLocked(ctx, userID)
	if lerr != nil {
		return lerr
	}
	if locked {
		return ErrMFAInvalid
	}
	if !s.verifyPassword(ctx, userID, password) {
		s.recordFailure(ctx, userID)
		s.log(ctx, userID, "auth", "mfa_failed", "warning", fmt.Sprintf("MFA %s rejected: wrong password", purpose))
		return ErrMFAPasswordIncorrect
	}
	return nil
}

// ---- enrolment -----------------------------------------------------------

// Status returns the enrolment state for the profile page.
func (s *MFAService) Status(ctx context.Context, userID string) (*MFAStatus, error) {
	var st MFAStatus
	var hasSecret bool
	if err := s.db.QueryRow(ctx,
		`SELECT COALESCE(totp_enabled, false), totp_enabled_at, totp_secret_enc IS NOT NULL
		   FROM users WHERE id = $1::uuid AND deleted_at IS NULL`, userID,
	).Scan(&st.Enabled, &st.EnabledAt, &hasSecret); err != nil {
		return nil, err
	}
	st.SetupPending = hasSecret && !st.Enabled
	if st.Enabled {
		_ = s.db.QueryRow(ctx,
			`SELECT COUNT(*) FROM user_recovery_codes WHERE user_id = $1::uuid AND used_at IS NULL`, userID,
		).Scan(&st.RecoveryRemaining)
	}
	if pol, err := middleware.LoadMFAPolicy(ctx, s.db, userID); err == nil {
		st.Required = pol.Required()
	}
	return &st, nil
}

// BeginSetup creates and stores (encrypted) a fresh secret. The factor stays
// inactive until ConfirmEnable proves the user's app can generate codes.
func (s *MFAService) BeginSetup(ctx context.Context, userID string) (secret, uri string, err error) {
	var email string
	var enabled bool
	if err = s.db.QueryRow(ctx,
		`SELECT email, COALESCE(totp_enabled, false) FROM users WHERE id = $1::uuid AND deleted_at IS NULL`, userID,
	).Scan(&email, &enabled); err != nil {
		return "", "", err
	}
	if enabled {
		return "", "", ErrMFAAlreadyEnabled
	}
	secret, err = totp.GenerateSecret()
	if err != nil {
		return "", "", err
	}
	enc, err := crypto.Encrypt(secret, s.encKey)
	if err != nil {
		return "", "", fmt.Errorf("encrypt totp secret: %w", err)
	}
	tag, err := s.db.Pool.Exec(ctx,
		`UPDATE users SET totp_secret_enc = $2, totp_last_step = 0, updated_at = NOW()
		  WHERE id = $1::uuid AND NOT COALESCE(totp_enabled, false)`, userID, enc)
	if err != nil {
		return "", "", err
	}
	if tag.RowsAffected() != 1 {
		return "", "", ErrMFAAlreadyEnabled
	}
	s.log(ctx, userID, "security", "mfa_setup_started", "info", "MFA setup started")
	return secret, totp.URI(mfaIssuer, email, secret), nil
}

// ConfirmEnable verifies the first code, activates MFA, replaces the recovery
// codes and bumps token_version so every other session is signed out.
func (s *MFAService) ConfirmEnable(ctx context.Context, userID, code string) (codes []string, err error) {
	start := time.Now()
	defer func() {
		if err != nil {
			padFailure(start)
		}
	}()
	locked, lerr := s.isLocked(ctx, userID)
	if lerr != nil {
		return nil, lerr
	}
	if locked {
		return nil, ErrMFAInvalid
	}
	var enabled, hasSecret bool
	if err = s.db.QueryRow(ctx,
		`SELECT COALESCE(totp_enabled, false), totp_secret_enc IS NOT NULL FROM users WHERE id = $1::uuid AND deleted_at IS NULL`, userID,
	).Scan(&enabled, &hasSecret); err != nil {
		return nil, ErrMFAInvalid
	}
	if enabled {
		return nil, ErrMFAAlreadyEnabled
	}
	if !hasSecret {
		return nil, ErrMFANoPendingSetup
	}
	if !s.checkTOTP(ctx, userID, code, false) {
		s.recordFailure(ctx, userID)
		s.log(ctx, userID, "auth", "mfa_failed", "warning", "MFA enable verification failed")
		return nil, ErrMFAInvalid
	}

	err = s.db.WithTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE users SET totp_enabled = true, totp_enabled_at = NOW(),
			        token_version = COALESCE(token_version, 0) + 1, updated_at = NOW()
			  WHERE id = $1::uuid AND NOT COALESCE(totp_enabled, false) AND totp_secret_enc IS NOT NULL`, userID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrMFAAlreadyEnabled
		}
		codes, err = replaceRecoveryCodes(ctx, tx, userID)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.clearFailures(ctx, userID)
	s.log(ctx, userID, "security", "mfa_enabled", "info", "MFA enabled; other sessions invalidated")
	return codes, nil
}

// Disable turns MFA off after password + second factor. It refuses while a
// policy requires MFA for the user, otherwise the user would be locked into the
// enrolment-only state by their own action.
func (s *MFAService) Disable(ctx context.Context, userID, password string, in SecondFactor) error {
	if err := s.guardedPassword(ctx, userID, password, "disable"); err != nil {
		return err
	}
	pol, err := middleware.LoadMFAPolicy(ctx, s.db, userID)
	if err != nil {
		return err
	}
	if !pol.TOTPEnabled {
		return ErrMFANotEnabled
	}
	if pol.Required() {
		return ErrMFARequiredByPolicy
	}
	if err := s.VerifySecondFactor(ctx, userID, in, "disable"); err != nil {
		return err
	}
	if err := s.clearMFA(ctx, userID); err != nil {
		return err
	}
	s.log(ctx, userID, "security", "mfa_disabled", "warning", "MFA disabled; other sessions invalidated")
	return nil
}

// RegenerateRecoveryCodes replaces all recovery codes after password + a TOTP
// code (a recovery code is deliberately not accepted here).
func (s *MFAService) RegenerateRecoveryCodes(ctx context.Context, userID, password, code string) ([]string, error) {
	if err := s.guardedPassword(ctx, userID, password, "recovery-codes regenerate"); err != nil {
		return nil, err
	}
	var enabled bool
	if err := s.db.QueryRow(ctx,
		`SELECT COALESCE(totp_enabled, false) FROM users WHERE id = $1::uuid AND deleted_at IS NULL`, userID).Scan(&enabled); err != nil {
		return nil, ErrMFAInvalid
	}
	if !enabled {
		return nil, ErrMFANotEnabled
	}
	if !looksLikeTOTP(code) {
		s.recordFailure(ctx, userID)
		s.log(ctx, userID, "auth", "mfa_failed", "warning", "MFA recovery-codes regenerate rejected: authenticator code required")
		padFailure(time.Now())
		return nil, ErrMFAInvalid
	}
	if err := s.VerifySecondFactor(ctx, userID, SecondFactor{Code: code}, "recovery-codes regenerate"); err != nil {
		return nil, err
	}
	var codes []string
	if err := s.db.WithTx(ctx, func(tx pgx.Tx) error {
		var err error
		codes, err = replaceRecoveryCodes(ctx, tx, userID)
		return err
	}); err != nil {
		return nil, err
	}
	s.log(ctx, userID, "security", "mfa_recovery_codes_regenerated", "warning", "MFA recovery codes regenerated")
	return codes, nil
}

// clearMFA removes the factor and revokes sessions (token_version bump).
func (s *MFAService) clearMFA(ctx context.Context, userID string) error {
	err := s.db.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`UPDATE users SET totp_enabled = false, totp_secret_enc = NULL, totp_enabled_at = NULL,
			        totp_last_step = 0, token_version = COALESCE(token_version, 0) + 1, updated_at = NOW()
			  WHERE id = $1::uuid`, userID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM user_recovery_codes WHERE user_id = $1::uuid`, userID)
		return err
	})
	if err == nil {
		s.clearFailures(ctx, userID)
	}
	return err
}

// AdminReset removes a user's MFA (lost device + lost recovery codes). The
// caller is responsible for authorisation; this method audits and revokes
// sessions.
func (s *MFAService) AdminReset(ctx context.Context, actorID, targetID string) error {
	if err := s.clearMFA(ctx, targetID); err != nil {
		return err
	}
	s.log(ctx, actorID, "security", "mfa_reset_by_admin", "warning",
		fmt.Sprintf("MFA reset for user %s by admin %s; sessions invalidated", targetID, actorID))
	s.log(ctx, targetID, "security", "mfa_reset", "warning", "MFA was reset by an administrator")
	return nil
}

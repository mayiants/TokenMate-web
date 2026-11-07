package keypool

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mayiants/ai-proxy-go/pkg/metrics"
	"go.uber.org/zap"
)

// RecordUpstreamRequest increments the request count for an upstream key.
// Should be called after a request is successfully dispatched. The model
// argument feeds per-(key, model) ModelLimits accounting; pass "" when the
// model is unknown.
func (m *Manager) RecordUpstreamRequest(provider, keyID, model string) {
	k := provider + ":" + keyID
	usage := m.getOrCreateUpstreamUsage(k)

	usage.mu.Lock()
	// Reset window if expired before incrementing. RPM and TPM share the same
	// window — reset BOTH counters to prevent tpmUsed from accumulating
	// across multiple windows.
	if time.Now().After(usage.windowEnd) {
		usage.rpmUsed = 0
		usage.tpmUsed = 0
		usage.models = nil
		usage.windowEnd = time.Now().Add(time.Minute)
	}
	usage.rpmUsed++
	if model != "" {
		if usage.models == nil {
			usage.models = make(map[string]*modelUsageCounter)
		}
		mc := usage.models[model]
		if mc == nil {
			mc = &modelUsageCounter{}
			usage.models[model] = mc
		}
		mc.rpm++
	}
	usage.mu.Unlock()
}

// RecordUpstreamTokens increments the token count for an upstream key.
// If TPM limit is exceeded, the key enters cooldown.
//
// For AWS providers (amazon-*), cache-read tokens are excluded: Bedrock's
// account TPM quota does not meter cache reads (verified by load test on
// 2026-11-02: ~12M token-TPM with 99% cache reads passed unthrottled),
// so counting them made our limiter ~100x stricter than the real quota.
// Other providers keep full-count semantics — their cache metering is
// unverified, so we stay conservative there.
func (m *Manager) RecordUpstreamTokens(provider, keyID, model string, tokens, cacheReadTokens int64) {
	if strings.HasPrefix(provider, "amazon") {
		tokens -= cacheReadTokens
		if tokens < 0 {
			tokens = 0
		}
	}
	k := provider + ":" + keyID
	usage := m.getOrCreateUpstreamUsage(k)

	usage.mu.Lock()
	// Reset window if expired before incrementing. RPM and TPM share the same
	// window — reset BOTH counters to prevent tpmUsed from accumulating
	// across multiple windows.
	if time.Now().After(usage.windowEnd) {
		usage.rpmUsed = 0
		usage.tpmUsed = 0
		usage.models = nil
		usage.windowEnd = time.Now().Add(time.Minute)
	}
	usage.tpmUsed += tokens
	if model != "" {
		if usage.models == nil {
			usage.models = make(map[string]*modelUsageCounter)
		}
		mc := usage.models[model]
		if mc == nil {
			mc = &modelUsageCounter{}
			usage.models[model] = mc
		}
		mc.tpm += tokens
	}
	usage.mu.Unlock()

	// Check if TPM limit exceeded and trigger cooldown
	m.checkUpstreamTPMOverflow(provider, keyID)
}

// checkUpstreamTPMOverflow checks if a key's TPM usage exceeds its limit
// and triggers cooldown if necessary.
func (m *Manager) checkUpstreamTPMOverflow(provider, keyID string) {
	// Read the limit under a read lock; most calls will not exceed it and can
	// avoid serializing through the provider write lock.
	lock := m.getLock(provider)
	lock.RLock()
	pool, ok := m.getPool(provider)
	tpmLimit := -1
	if ok {
		for _, key := range pool.Keys {
			if key.ID == keyID {
				tpmLimit = key.TPMLimit
				break
			}
		}
	}
	lock.RUnlock()
	if tpmLimit <= 0 {
		return // No TPM limit
	}

	k := provider + ":" + keyID
	usage := m.getUpstreamUsage(k)
	if usage == nil {
		return
	}
	usage.mu.Lock()
	exceeded := usage.tpmUsed >= int64(tpmLimit)
	tpmUsed := usage.tpmUsed
	usage.mu.Unlock()
	if !exceeded {
		return
	}

	// Limit exceeded — take the write lock only to mutate key state.
	lock.Lock()
	defer lock.Unlock()

	pool, ok = m.getPool(provider)
	if !ok {
		return
	}
	for _, key := range pool.Keys {
		if key.ID != keyID {
			continue
		}
		if key.TPMLimit <= 0 {
			return
		}
		now := time.Now()
		key.CooldownUntil = now.Add(60 * time.Second)
		key.Status = "cooldown"
		key.StatusHistory = append(key.StatusHistory, StatusEntry{
			Status: "cooldown",
			At:     now,
			Reason: fmt.Sprintf("TPM_LIMIT_EXCEEDED (%d/%d)", tpmUsed, key.TPMLimit),
		})
		metrics.GetMetrics().IncrementKeyCooldown(provider)
		m.logger.Warn("Upstream key TPM limit exceeded, entering cooldown",
			zap.String("provider", provider),
			zap.String("key_preview", key.KeyPreview),
			zap.Int64("tpm_used", tpmUsed),
			zap.Int("tpm_limit", key.TPMLimit),
		)
		return
	}
}

type failureAction string

const (
	actionNone                failureAction = ""
	actionQuotaCooldown       failureAction = "quota_cooldown"
	actionDailyQuotaCooldown  failureAction = "daily_quota_cooldown"
	actionKeyInvalid          failureAction = "key_invalid"
	actionModelLevelAuth      failureAction = "model_level_auth"
	actionExcessiveFailures   failureAction = "excessive_failures"
	actionMaxFailuresCooldown failureAction = "max_failures_cooldown"
)

// ReportKeySuccess resets the key-level failure counter after a successful
// upstream call. Previously Failures only decayed via cooldown expiry and never
// reset on success, so sparse intermittent errors on an otherwise-healthy key
// could accumulate to the 3-strike threshold over time and trigger spurious
// cooldowns. Resetting on success keeps the counter reflecting *consecutive*
// trouble rather than lifetime trouble. It also clears the per-model failure
// count so a healthy model never inherits a stale strike streak.
func (m *Manager) ReportKeySuccess(provider, keyID, model string) {
	lock := m.getLock(provider)
	lock.Lock()
	pool, ok := m.getPool(provider)
	if ok {
		for _, key := range pool.Keys {
			if key.ID == keyID {
				if key.Failures != 0 {
					key.Failures = 0
				}
				break
			}
		}
		// Stamp last-success on the internal key for success-aware LRU.
		now := time.Now()
		for _, ik := range pool.internalKeys {
			if ik.ID == keyID {
				ik.SetLastSuccess(now)
				break
			}
		}
	}
	lock.Unlock()

	m.modelCooldownsMu.Lock()
	if models, ok := m.modelFailures[keyID]; ok {
		delete(models, model)
		if len(models) == 0 {
			delete(m.modelFailures, keyID)
		}
	}
	m.modelCooldownsMu.Unlock()
}

// ReportKeyRateLimited cools a single (key, model) pair for retryAfterSeconds
// (falling back to 60s when the upstream gave no usable Retry-After). This is
// the 429 path: unlike ReportKeyFailure it does not touch the key-level failure
// counter (a rate limit is not a key malfunction) and respects the upstream's
// own backoff hint so we retry no sooner than it asked.
func (m *Manager) ReportKeyRateLimited(provider, keyID, model string, retryAfterSeconds int) {
	d := 60 * time.Second
	if retryAfterSeconds > 0 {
		d = time.Duration(retryAfterSeconds) * time.Second
		// Guard against an upstream sending an absurd Retry-After that would
		// freeze the (key, model) for an unreasonable time.
		if d > 10*time.Minute {
			d = 10 * time.Minute
		}
	}
	until := time.Now().Add(d)
	lock := m.getLock(provider)
	lock.Lock()
	pool, ok := m.getPool(provider)
	if ok {
		for _, key := range pool.Keys {
			if key.ID == keyID {
				key.LastFailure = time.Now()
				break
			}
		}
	}
	lock.Unlock()

	m.modelCooldownsMu.Lock()
	if m.modelCooldowns == nil {
		m.modelCooldowns = make(map[string]map[string]time.Time)
	}
	if m.modelCooldowns[keyID] == nil {
		m.modelCooldowns[keyID] = make(map[string]time.Time)
	}
	m.modelCooldowns[keyID][model] = until
	m.modelCooldownsMu.Unlock()

	metrics.GetMetrics().IncrementKeyCooldown(provider)
	m.logger.Warn("Key rate-limited (model-level cooldown)",
		zap.String("provider", provider),
		zap.String("model", model),
		zap.Int("retry_after_seconds", retryAfterSeconds),
		zap.Duration("cooldown", d),
	)
}

// ReportKeyFailure reports a key failure and handles cooldown/inactive logic
// Similar to Node.js proxy's reportFailure method
func (m *Manager) ReportKeyFailure(provider, keyID, model string, httpStatus int, errorCode, errorType, errorMsg string) {
	// Classify the error outside the provider lock so other goroutines are not
	// blocked while we do string matching.
	isQuota := m.isQuotaExceededError(httpStatus, errorCode, errorType, errorMsg)
	isAuthError := httpStatus == 401 || httpStatus == 403 ||
		strings.Contains(errorType, "authentication") ||
		strings.Contains(errorType, "invalid")
	isKeyInvalid := isAuthError && isDefinitelyKeyInvalid(httpStatus, errorCode, errorType, errorMsg)
	isModelLevelAuth := isAuthError && !isKeyInvalid
	isModelLevelNonAuth := httpStatus == 400 && IsModelLevelError(errorMsg)

	// Client-side cancellations and closed connections must not count as key
	// failures: they reflect the caller going away (often a CDN timeout cut),
	// not the upstream key misbehaving. Counting them put the single approach
	// key into cooldown during the 2026-10-24 validation, taking every model
	// on the provider down with it. Model-level health filters the same class
	// (isTransportCancellation in model_health.go).
	if isTransportCancellation(errorMsg) {
		m.logger.Debug("Key failure ignored (transport cancellation)",
			zap.String("provider", provider),
			zap.String("model", model),
			zap.String("error", truncateString(errorMsg, 100)),
		)
		return
	}

	now := time.Now()
	var (
		action         failureAction
		keyPreview     string
		recentFailures int
		failures       int
		cooldownUntil  time.Time
	)

	lock := m.getLock(provider)
	lock.Lock()

	pool, ok := m.getPool(provider)
	if !ok {
		lock.Unlock()
		return
	}

	for _, key := range pool.Keys {
		if key.ID != keyID {
			continue
		}

		key.LastFailure = now
		keyPreview = key.KeyPreview

		// Quota/rate limit errors are not real failures — just cooldown.
		if isQuota {
			if m.isDailyQuotaExceeded(errorMsg) {
				// Daily quotas are key-level (total spend), keep key-level cooldown.
				key.Status = "cooldown"
				tomorrow := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day()+1, 0, 0, 0, 0, time.UTC)
				key.RateLimitUntil = tomorrow
				key.CooldownUntil = tomorrow
				cooldownUntil = tomorrow
				key.StatusHistory = append(key.StatusHistory, StatusEntry{
					Status: "cooldown",
					At:     now,
					Reason: fmt.Sprintf("DAILY_QUOTA_EXHAUSTED (%s)", truncateString(errorMsg, 50)),
				})
				action = actionDailyQuotaCooldown
			} else {
				// Model-level rate limit: only cool this (key, model), not the entire key.
				// Other models on the same key stay available.
				until := now.Add(60 * time.Second)
				m.modelCooldownsMu.Lock()
				if m.modelCooldowns == nil {
					m.modelCooldowns = make(map[string]map[string]time.Time)
				}
				if m.modelCooldowns[keyID] == nil {
					m.modelCooldowns[keyID] = make(map[string]time.Time)
				}
				m.modelCooldowns[keyID][model] = until
				m.modelCooldownsMu.Unlock()
				cooldownUntil = until
				action = actionQuotaCooldown
			}
			break
		}

		// Model-level issues (ambiguous auth or HTTP 400 with model-specific messages)
		// should NOT penalize the key and should NOT be recorded in FailureHistory.
		if isModelLevelAuth || isModelLevelNonAuth {
			action = actionModelLevelAuth
			break
		}

		// Real failure - record history.
		key.FailureHistory = append(key.FailureHistory, FailureEntry{
			Time:      now,
			Status:    httpStatus,
			Error:     errorMsg,
			ErrorType: errorType,
		})
		if len(key.FailureHistory) > 100 {
			key.FailureHistory = key.FailureHistory[len(key.FailureHistory)-100:]
		}

		// Confirmed key-level auth failure → inactive immediately.
		if isKeyInvalid {
			if key.Enabled {
				pool.ActiveKeys--
			}
			key.Enabled = false
			key.Status = "inactive"
			key.InactiveInfo = &InactiveInfo{
				At:           now,
				Reason:       fmt.Sprintf("AUTH_ERROR_%d", httpStatus),
				FailureCount: key.Failures,
				LastError:    errorMsg,
			}
			key.StatusHistory = append(key.StatusHistory, StatusEntry{
				Status: "inactive",
				At:     now,
				Reason: fmt.Sprintf("AUTH_ERROR_%d", httpStatus),
			})
			action = actionKeyInvalid
			break
		}

		// Non-auth errors: increment failure counter and check thresholds.
		key.Failures++

		// Per-model consecutive strike counter (feeds the 3-strike cooldown below).
		m.modelCooldownsMu.Lock()
		if m.modelFailures == nil {
			m.modelFailures = make(map[string]map[string]int)
		}
		if m.modelFailures[keyID] == nil {
			m.modelFailures[keyID] = make(map[string]int)
		}
		m.modelFailures[keyID][model]++
		modelStrikeCount := m.modelFailures[keyID][model]
		m.modelCooldownsMu.Unlock()

		windowStart := now.Add(-10 * time.Minute)
		for _, f := range key.FailureHistory {
			if f.Time.After(windowStart) {
				recentFailures++
			}
		}
		if recentFailures >= 10 {
			if key.Enabled {
				pool.ActiveKeys--
			}
			key.Enabled = false
			key.InactiveInfo = &InactiveInfo{
				At:           now,
				Reason:       "EXCESSIVE_FAILURES_10MIN",
				FailureCount: recentFailures,
			}
			key.StatusHistory = append(key.StatusHistory, StatusEntry{
				Status: "inactive",
				At:     now,
				Reason: fmt.Sprintf("EXCESSIVE_FAILURES_10MIN (%d failures)", recentFailures),
			})
			action = actionExcessiveFailures
			break
		}

		// 3 consecutive failures of the SAME model: cool only that (key, model)
		// for 60s. Sibling models on the key keep serving — a key-level cooldown
		// here let one flaky model (e.g. a CF-cut 40k request) take every model
		// on a single-key provider down (seen on approach, 2026-10-29).
		if modelStrikeCount >= 3 {
			until := now.Add(60 * time.Second)
			m.modelCooldownsMu.Lock()
			if m.modelCooldowns == nil {
				m.modelCooldowns = make(map[string]map[string]time.Time)
			}
			if m.modelCooldowns[keyID] == nil {
				m.modelCooldowns[keyID] = make(map[string]time.Time)
			}
			m.modelCooldowns[keyID][model] = until
			m.modelCooldownsMu.Unlock()
			cooldownUntil = until
			failures = modelStrikeCount
			action = actionMaxFailuresCooldown
		}

		break
	}
	lock.Unlock()

	// Logging and metrics happen outside the provider lock.
	switch action {
	case actionDailyQuotaCooldown:
		metrics.GetMetrics().IncrementKeyCooldown(provider)
		m.logger.Warn("Key daily quota exhausted",
			zap.String("provider", provider),
			zap.String("key_preview", keyPreview),
			zap.Time("cooldown_until", cooldownUntil),
		)
	case actionQuotaCooldown:
		metrics.GetMetrics().IncrementKeyCooldown(provider)
		m.logger.Warn("Key rate limited",
			zap.String("provider", provider),
			zap.String("key_preview", keyPreview),
			zap.Int("cooldown_seconds", 60),
		)
	case actionKeyInvalid:
		m.logger.Error("Key auth error confirmed, marked as inactive",
			zap.String("provider", provider),
			zap.String("key_preview", keyPreview),
			zap.Int("status", httpStatus),
			zap.String("error", truncateString(errorMsg, 100)),
			zap.Time("will_purge_after", now.Add(24*time.Hour)),
		)
	case actionModelLevelAuth:
		m.logger.Warn("Auth error treated as model-level (key not penalized)",
			zap.String("provider", provider),
			zap.String("key_preview", keyPreview),
			zap.Int("status", httpStatus),
			zap.String("error_type", errorType),
			zap.String("error", truncateString(errorMsg, 100)),
		)
	case actionExcessiveFailures:
		m.logger.Error("Key excessive failures, marked as inactive",
			zap.String("provider", provider),
			zap.String("key_preview", keyPreview),
			zap.Int("recent_failures", recentFailures),
		)
	case actionMaxFailuresCooldown:
		metrics.GetMetrics().IncrementKeyCooldown(provider)
		m.logger.Warn("Key max failures reached, entering model-level cooldown",
			zap.String("provider", provider),
			zap.String("key_preview", keyPreview),
			zap.String("model", model),
			zap.Int("failures", failures),
		)
	}
}

// isQuotaExceededError checks if the error is a quota/rate limit error
func (m *Manager) isQuotaExceededError(httpStatus int, errorCode, errorType, errorMsg string) bool {
	// 429: Rate limit
	if httpStatus == 429 {
		return true
	}

	// Check for quota-related error codes
	quotaCodes := []string{"rate_limit_exceeded", "quota_exceeded", "insufficient_quota"}
	for _, code := range quotaCodes {
		if strings.Contains(strings.ToLower(errorCode), code) {
			return true
		}
	}

	// Check for quota-related keywords in error message
	quotaKeywords := []string{"quota", "rate limit", "too many requests", "tokens.total", "business.total"}
	msgLower := strings.ToLower(errorMsg)
	for _, kw := range quotaKeywords {
		if strings.Contains(msgLower, kw) {
			return true
		}
	}

	return false
}

// isDailyQuotaExceeded checks if the error indicates daily quota exhaustion
// (as opposed to a short per-minute/per-hour rate limit).
//
// Matching is intentionally narrow: the bare substring "5h" was previously
// matched here, but that collides with unrelated error text (e.g. durations
// like "5h35m", hex fragments, retry-after hints) and would freeze a key
// until UTC midnight on a false positive. Only explicit daily-quota signals
// are matched; provider-specific duration hints are handled by the regular
// rate-limit cooldown path instead.
func (m *Manager) isDailyQuotaExceeded(errorMsg string) bool {
	msgLower := strings.ToLower(errorMsg)
	return strings.Contains(msgLower, "tokens.total") ||
		strings.Contains(msgLower, "business.total") ||
		strings.Contains(msgLower, "daily") ||
		strings.Contains(msgLower, "5小时")
}

// isDefinitelyKeyInvalid determines whether a 401/403 error indicates the API
// key itself is invalid (should be deactivated) vs a model-level or permission
// error that should not disable the entire key.
//
// Industry standard (OpenAI/Anthropic/Google):
//   - 401 = key invalid/expired/revoked → key-level
//   - 403 = permission denied for resource → often model-level
//   - But intermediary gateways (e.g. accessapi) may misuse 401 for model errors
//
// Strategy: look for explicit key-invalid signals; if model/permission keywords
// are present in the message, treat as model-level even on 401.
func isDefinitelyKeyInvalid(httpStatus int, errorCode, errorType, errorMsg string) bool {
	msgLower := strings.ToLower(errorMsg)
	typeLower := strings.ToLower(errorType)
	codeLower := strings.ToLower(errorCode)

	// Definite key-invalid signals (from OpenAI, Anthropic, Google docs)
	keyInvalidSignals := []string{
		"invalid api key",
		"incorrect api key",
		"api key not found",
		"key has been revoked",
		"key has been deactivated",
		"key has expired",
		"invalid x-api-key",
		"invalid authentication",
		"must be a member of an organization",
		// Aggregator (OpenRouter/TokenRouter/etc.) dead-key signals: the key's
		// account no longer exists / was deleted. Observed from OpenRouter as a
		// 401 "User not found." — must disable the key, not keep retrying it.
		"user not found",
		"no auth credentials found",
		"invalid token",
		"token not found",
	}
	for _, signal := range keyInvalidSignals {
		if strings.Contains(msgLower, signal) {
			return true
		}
	}

	// Check error type/code for definitive auth signals
	if typeLower == "authentication_error" || codeLower == "invalid_api_key" {
		// But first check if message contradicts (model-level)
		if containsModelLevelSignal(msgLower) {
			return false
		}
		return true
	}

	// Model/permission level signals → NOT key invalid
	if containsModelLevelSignal(msgLower) {
		return false
	}

	// 403 without clear key-invalid signal → assume model/permission level
	if httpStatus == 403 {
		return false
	}

	// 401 without any model-level signal and no clear key-invalid signal either:
	// Default to key invalid (conservative for direct provider APIs).
	// But check for common gateway patterns that misuse 401 for model errors.
	gatewayModelErrorSignals := []string{
		"无效的令牌", // accessapi Chinese error for unsupported model
		"token invalid for model",
		"not available",
		"not supported",
		"does not support",
	}
	for _, signal := range gatewayModelErrorSignals {
		if strings.Contains(msgLower, signal) {
			return false
		}
	}

	return true
}

// containsModelLevelSignal checks if an error message indicates a model-level
// or permission-level issue rather than a key validity issue.
func containsModelLevelSignal(msgLower string) bool {
	signals := []string{
		"model",
		"not found",
		"not supported",
		"not available",
		"not permitted",
		"not allowed",
		"permission",
		"access denied",
		"region",
		"country",
	}
	for _, s := range signals {
		if strings.Contains(msgLower, s) {
			return true
		}
	}
	return false
}

// IsModelLevelError detects errors that are caused by the model being
// invalid/unsupported/unavailable rather than the key being bad. These errors
// should NOT count toward key failure thresholds or trigger cooldown.
func IsModelLevelError(errorMsg string) bool {
	msgLower := strings.ToLower(errorMsg)
	modelErrorSignals := []string{
		"model identifier is invalid",
		"model is not available",
		"model not found",
		"model not available",
		"does not support",
		"not support the",
		"not supported for",
		"model is not supported",
		"access denied",
		"access to model",
		"not have access",
		"legacy model",
		"deprecated model",
		"model is disabled",
		"model is not enabled",
		"not enabled for",
		"not activated",
		"not subscribed",
		"resource not found",
		"unknown model",
		"invalid model",
		"unrecognized model",
	}
	for _, signal := range modelErrorSignals {
		if strings.Contains(msgLower, signal) {
			return true
		}
	}
	return false
}

// IsKeyAvailable checks if a key is available for use.
//
// Status reflects local runtime verdicts that portal sync must not override:
//   - "inactive": key killed locally by ReportKeyFailure (auth failure /
//     excessive failures). Enabled is set false at kill time, but sync merge
//     overwrites KeyStatic.Enabled with the portal-supplied value, so checking
//     Status here is what actually keeps a locally-killed key out of rotation
//     across sync cycles (otherwise it flaps back in and re-hammers 401s).
//   - "purged": 24h post-inactive purge.
//
// "active"/"cooldown"/"" remain governed by Enabled/Cooldown/RateLimit below.
func (m *Manager) IsKeyAvailable(key *Key) bool {
	if !key.Enabled {
		return false
	}
	if key.Status == "inactive" || key.Status == "purged" {
		return false
	}
	if key.PurgedFromPool {
		return false
	}
	if !key.CooldownUntil.IsZero() && key.CooldownUntil.After(time.Now()) {
		return false
	}
	if !key.RateLimitUntil.IsZero() && key.RateLimitUntil.After(time.Now()) {
		return false
	}
	return true
}

// HealthCheck performs health check on all keys (should be called periodically)
func (m *Manager) HealthCheck() {
	m.poolsMu.RLock()
	providers := make([]string, 0, len(m.pools))
	for k := range m.pools {
		providers = append(providers, k)
	}
	m.poolsMu.RUnlock()

	for _, provider := range providers {
		lock := m.getLock(provider)
		lock.Lock()

		pool, ok := m.getPool(provider)
		if !ok {
			lock.Unlock()
			continue
		}

		for _, key := range pool.Keys {
			// Cooldown recovery
			if !key.CooldownUntil.IsZero() && key.CooldownUntil.Before(time.Now()) {
				key.CooldownUntil = time.Time{}
				if key.Failures > 0 {
					key.Failures--
				}
				if key.Status == "cooldown" {
					key.Status = "active"
					key.StatusHistory = append(key.StatusHistory, StatusEntry{
						Status: "active",
						At:     time.Now(),
						Reason: "COOLDOWN_EXPIRED",
					})
				}
			}

			// Rate limit recovery
			if !key.RateLimitUntil.IsZero() && key.RateLimitUntil.Before(time.Now()) {
				key.RateLimitUntil = time.Time{}
			}

			// 24-hour purge for inactive keys
			if key.Status == "inactive" && !key.PurgedFromPool && key.InactiveInfo != nil {
				inactiveDuration := time.Since(key.InactiveInfo.At)
				if inactiveDuration >= 24*time.Hour {
					key.PurgedFromPool = true
					key.PurgedAt = time.Now()
					key.Status = "purged"
					key.StatusHistory = append(key.StatusHistory, StatusEntry{
						Status: "purged",
						At:     time.Now(),
						Reason: "INACTIVE_24H_PURGE",
					})
					m.logger.Info("Key purged after 24h inactive",
						zap.String("provider", provider),
						zap.String("key_preview", key.KeyPreview),
					)
				}
			}
		}

		lock.Unlock()
	}

	// Cleanup expired model-level cooldowns (and their strike counters so an
	// expired cooldown never resumes mid-streak).
	m.modelCooldownsMu.Lock()
	now := time.Now()
	for keyID, models := range m.modelCooldowns {
		for model, until := range models {
			if now.After(until) {
				delete(models, model)
				if strikes, ok := m.modelFailures[keyID]; ok {
					delete(strikes, model)
				}
			}
		}
		if len(models) == 0 {
			delete(m.modelCooldowns, keyID)
		}
		if strikes, ok := m.modelFailures[keyID]; ok && len(strikes) == 0 {
			delete(m.modelFailures, keyID)
		}
	}
	m.modelCooldownsMu.Unlock()
}

// IsModelOnCooldown checks if a specific model is on cooldown for the given key.
func (m *Manager) IsModelOnCooldown(keyID, model string) bool {
	m.modelCooldownsMu.RLock()
	defer m.modelCooldownsMu.RUnlock()
	if m.modelCooldowns == nil {
		return false
	}
	models, ok := m.modelCooldowns[keyID]
	if !ok {
		return false
	}
	until, ok := models[model]
	if !ok {
		return false
	}
	return time.Now().Before(until)
}

// StartHealthCheck starts periodic health check
func (m *Manager) StartHealthCheck(ctx context.Context) {
	interval := m.healthCheckInterval
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.HealthCheck()
				m.updateKeyMetrics()
				// Periodically clean up stale model health entries to prevent
				// unbounded map growth (disabled entries past their recovery
				// window, and recovered entries with no recent activity).
				if m.modelHealth != nil {
					m.modelHealth.Cleanup(24 * time.Hour)
				}
			}
		}
	}()
	m.logger.Info("Key pool health check started", zap.Duration("interval", interval))
}

// updateKeyMetrics counts keys by status across all providers and publishes them to metrics.
func (m *Manager) updateKeyMetrics() {
	var active, cooldown, inactive int32

	m.poolsMu.RLock()
	providers := make([]string, 0, len(m.pools))
	for k := range m.pools {
		providers = append(providers, k)
	}
	m.poolsMu.RUnlock()

	for _, provider := range providers {
		lock := m.getLock(provider)
		lock.RLock()

		pool, ok := m.getPool(provider)
		if !ok {
			lock.RUnlock()
			continue
		}

		for _, key := range pool.Keys {
			switch key.Status {
			case "active":
				active++
			case "cooldown":
				cooldown++
			case "inactive":
				inactive++
			}
		}

		lock.RUnlock()
	}

	metrics.GetMetrics().UpdateKeyStats(active, cooldown, inactive)
}

// truncateString truncates a string to maxLen characters
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

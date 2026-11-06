package keypool

import (
	"sort"
	"time"
)

// LiveHealthTier is the proxy-side, real-time per-key health signal (it does
// not depend on the Portal-synced keyHealth, which is coarse and stale). It
// mirrors the three-level spirit of DeriveInternalStatus but from live
// in-proxy outcomes:
//
//	0 healthy — no current failure streak and last outcome was a success
//	            (or the key has no history yet — treat a fresh key as healthy so
//	            it can be tried; a genuinely bad new key degrades on its first
//	            failures).
//	1 suspect — mild trouble: 1-2 consecutive failures, or the last outcome was
//	            a failure (lastFailure >= lastSuccess) within a recent window.
//	2 failing — 3+ consecutive failures (usually already cooled down by
//	            ReportKeyFailure, but covers the window before cooldown applies).
//
// Lower tier sorts first, so healthy keys are always preferred, suspect keys
// are used only when no healthy key exists, and failing keys are last resort.
func LiveHealthTier(k *KeyInternal) int {
	f := k.GetFailures()
	if f >= 3 {
		return 2
	}
	if f >= 1 {
		return 1
	}
	// No active failure streak. If the most recent outcome was still a failure
	// (lastFailure at/after lastSuccess) within the last 2 minutes, treat as
	// suspect so a key that just started erroring is deprioritized immediately.
	lf, ls := k.GetLastFailure(), k.GetLastSuccess()
	if !lf.IsZero() && !lf.Before(ls) && time.Since(lf) < 2*time.Minute {
		return 1
	}
	return 0
}

// UsageWindowState is a snapshot of a key's fixed 60s RPM/TPM rate window.
type UsageWindowState struct {
	RPMUsed   int                        `json:"rpm_used"`
	TPMUsed   int64                      `json:"tpm_used"`
	WindowEnd time.Time                  `json:"window_end"`
	Models    map[string]ModelUsageState `json:"models,omitempty"`
}

// ModelUsageState holds per-(key, model) counters within the parent window.
type ModelUsageState struct {
	RPM int   `json:"rpm"`
	TPM int64 `json:"tpm"`
}

// KeySelectionState is a read-only dump of every runtime signal that
// influences whether a key is currently selectable. It powers the admin
// selection-state endpoint used for routing forensics (e.g. the 2026-10-28
// incident where a provider silently served zero traffic for 32h and only a
// container restart recovered it). It never contains key material.
type KeySelectionState struct {
	KeyID          string                `json:"key_id"`
	KeyPreview     string                `json:"key_preview"`
	Enabled        bool                  `json:"enabled"`
	Status         string                `json:"status"`
	Priority       string                `json:"priority"`
	Tier           string                `json:"tier"`
	Failures       int                   `json:"failures"`
	LastFailure    time.Time             `json:"last_failure,omitempty"`
	LastSuccess    time.Time             `json:"last_success,omitempty"`
	LastUsed       time.Time             `json:"last_used,omitempty"`
	CooldownUntil  time.Time             `json:"cooldown_until,omitempty"`
	RateLimitUntil time.Time             `json:"rate_limit_until,omitempty"`
	InactiveInfo   *InactiveInfo         `json:"inactive_info,omitempty"`
	PurgedFromPool bool                  `json:"purged_from_pool"`
	RPMLimit       int                   `json:"rpm_limit"`
	TPMLimit       int                   `json:"tpm_limit"`
	ModelLimits    map[string]ModelLimit `json:"model_limits,omitempty"`
	QuotaLimit     int64                 `json:"quota_limit"`
	QuotaUsed      int64                 `json:"quota_used"`
	HealthTier     int                   `json:"health_tier"`
	Window         *UsageWindowState     `json:"window,omitempty"`
	ModelCooldowns map[string]time.Time  `json:"model_cooldowns,omitempty"`
	ModelFailures  map[string]int        `json:"model_failures,omitempty"`
	DisabledModels []string              `json:"disabled_models,omitempty"`
}

// ProviderSelectionState groups the per-key dump for one provider.
type ProviderSelectionState struct {
	Provider  string              `json:"provider"`
	TotalKeys int                 `json:"total_keys"`
	Keys      []KeySelectionState `json:"keys"`
}

// DumpSelectionState returns a consistent snapshot of the selection-relevant
// runtime state for every key, optionally restricted to one provider. It is
// read-only and safe to call concurrently with traffic: per-provider read
// locks are taken one at a time and lock ordering (provider → modelCooldowns →
// usage shard) matches the write paths.
func (m *Manager) DumpSelectionState(providerFilter string) []ProviderSelectionState {
	m.poolsMu.RLock()
	providers := make([]string, 0, len(m.pools))
	for p := range m.pools {
		if providerFilter != "" && p != providerFilter {
			continue
		}
		providers = append(providers, p)
	}
	m.poolsMu.RUnlock()
	sort.Strings(providers)

	out := make([]ProviderSelectionState, 0, len(providers))
	for _, provider := range providers {
		lock := m.getLock(provider)
		lock.RLock()
		pool, ok := m.getPool(provider)
		if !ok {
			lock.RUnlock()
			continue
		}
		ps := ProviderSelectionState{
			Provider:  provider,
			TotalKeys: len(pool.internalKeys),
			Keys:      make([]KeySelectionState, 0, len(pool.internalKeys)),
		}
		for _, ik := range pool.internalKeys {
			ks := KeySelectionState{
				KeyID:          ik.ID,
				KeyPreview:     ik.KeyPreview,
				Enabled:        ik.Enabled,
				Status:         ik.Status,
				Priority:       ik.Priority,
				Tier:           ik.Tier,
				Failures:       ik.Failures,
				LastFailure:    ik.LastFailure,
				LastSuccess:    ik.GetLastSuccess(),
				LastUsed:       ik.GetLastUsed(),
				CooldownUntil:  ik.CooldownUntil,
				RateLimitUntil: ik.RateLimitUntil,
				InactiveInfo:   ik.InactiveInfo,
				PurgedFromPool: ik.PurgedFromPool,
				RPMLimit:       ik.RPMLimit,
				TPMLimit:       ik.TPMLimit,
				ModelLimits:    ik.ModelLimits,
				QuotaLimit:     ik.QuotaLimit,
				QuotaUsed:      ik.GetQuotaUsed(),
				HealthTier:     LiveHealthTier(ik),
			}

			if usage := m.getUpstreamUsage(provider + ":" + ik.ID); usage != nil {
				usage.mu.Lock()
				w := &UsageWindowState{
					RPMUsed:   usage.rpmUsed,
					TPMUsed:   usage.tpmUsed,
					WindowEnd: usage.windowEnd,
				}
				if len(usage.models) > 0 {
					w.Models = make(map[string]ModelUsageState, len(usage.models))
					for model, mc := range usage.models {
						w.Models[model] = ModelUsageState{RPM: mc.rpm, TPM: mc.tpm}
					}
				}
				usage.mu.Unlock()
				ks.Window = w
			}

			m.modelCooldownsMu.RLock()
			if cds := m.modelCooldowns[ik.ID]; len(cds) > 0 {
				ks.ModelCooldowns = make(map[string]time.Time, len(cds))
				for model, until := range cds {
					ks.ModelCooldowns[model] = until
				}
			}
			if fs := m.modelFailures[ik.ID]; len(fs) > 0 {
				ks.ModelFailures = make(map[string]int, len(fs))
				for model, n := range fs {
					ks.ModelFailures[model] = n
				}
			}
			m.modelCooldownsMu.RUnlock()

			if m.modelHealth != nil {
				if disabled := m.modelHealth.DisabledModels(ik.ID); len(disabled) > 0 {
					ks.DisabledModels = disabled
				}
			}

			ps.Keys = append(ps.Keys, ks)
		}
		lock.RUnlock()
		out = append(out, ps)
	}
	return out
}

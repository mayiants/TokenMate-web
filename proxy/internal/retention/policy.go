package retention

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"
)

// Policy is the retention window currently configured in the Portal.
// Platform applies to the _personal tenant (keys without an enterprise);
// Enterprises maps a canonical enterprise UUID to its window in days.
type Policy struct {
	Platform    int
	Enterprises map[string]int
}

// PolicySource fetches the current Policy. It is called once per cleanup pass.
type PolicySource func(ctx context.Context) (Policy, error)

// daysFor returns the current window for a tenant directory name.
func (p Policy) daysFor(tenant string) (int, bool) {
	if tenant == personalTenant {
		return p.Platform, validPolicyDays(p.Platform)
	}
	d, ok := p.Enterprises[strings.ToLower(tenant)]
	return d, ok && validPolicyDays(d)
}

func validPolicyDays(d int) bool { return d >= 1 && d <= maxRetentionDays }

// SetPolicySource makes cleanup follow the Portal's *current* policy instead
// of the <N>d directory a file happened to be written under. The directory
// name stays as the fallback for tenants the Portal does not know and for the
// case where no policy has ever been fetched, so a Portal outage can never
// make files expire earlier than they were stamped with.
func (s *DiskStore) SetPolicySource(src PolicySource) {
	s.policyMu.Lock()
	s.policySrc = src
	s.policyMu.Unlock()
}

// refreshPolicy pulls the current policy; on failure the last good one is kept.
func (s *DiskStore) refreshPolicy() {
	s.policyMu.Lock()
	src := s.policySrc
	s.policyMu.Unlock()
	if src == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	p, err := src(ctx)
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("Retention policy fetch failed; using last known policy or directory days", zap.Error(err))
		}
		return
	}
	s.policyMu.Lock()
	s.policy = &p
	s.policyMu.Unlock()
}

func (s *DiskStore) currentPolicy() *Policy {
	s.policyMu.Lock()
	defer s.policyMu.Unlock()
	return s.policy
}

// NewPortalPolicySource reads GET <portalURL>/api/admin/retention/policies/sync
// using the shared proxy secret.
func NewPortalPolicySource(portalURL, secret string, client *http.Client) PolicySource {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	url := strings.TrimRight(portalURL, "/") + "/api/admin/retention/policies/sync"
	return func(ctx context.Context) (Policy, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return Policy{}, err
		}
		req.Header.Set("x-proxy-secret", secret)
		resp, err := client.Do(req)
		if err != nil {
			return Policy{}, err
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return Policy{}, fmt.Errorf("portal returned status %d", resp.StatusCode)
		}
		var body struct {
			Platform struct {
				Days int `json:"days"`
			} `json:"platform"`
			Enterprises []struct {
				ID   string `json:"id"`
				Days int    `json:"days"`
			} `json:"enterprises"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			return Policy{}, fmt.Errorf("decode policy: %w", err)
		}
		p := Policy{Platform: body.Platform.Days, Enterprises: make(map[string]int, len(body.Enterprises))}
		for _, e := range body.Enterprises {
			if validPolicyDays(e.Days) {
				p.Enterprises[strings.ToLower(e.ID)] = e.Days
			}
		}
		return p, nil
	}
}

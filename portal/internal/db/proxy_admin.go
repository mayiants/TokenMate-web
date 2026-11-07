package db

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mayiants/ai-portal-go/pkg/telemetry"
	"go.uber.org/zap"
)

// ErrProxyNotConfigured is returned when no Proxy base URL is configured.
var ErrProxyNotConfigured = errors.New("proxy URL is not configured")

// ErrProxySecretMissing is returned when no signing secret is configured.
var ErrProxySecretMissing = errors.New("proxy admin secret is not configured")

// ProxyPurgeTarget is the outcome of the purge call against one Proxy instance.
type ProxyPurgeTarget struct {
	Status       int    `json:"status"`
	DeletedFiles int64  `json:"deleted_files"`
	DeletedBytes int64  `json:"deleted_bytes"`
	Error        string `json:"error,omitempty"`
}

// ProxyPurgeResult aggregates the purge outcome over every configured Proxy.
type ProxyPurgeResult struct {
	Targets      []ProxyPurgeTarget `json:"targets"`
	DeletedFiles int64              `json:"deleted_files"`
	DeletedBytes int64              `json:"deleted_bytes"`
}

// proxyAdminHTTPClient is used for Portal -> Proxy admin calls. A purge walks
// a tenant directory, so it gets a longer timeout than the 5s webhook client.
var proxyAdminHTTPClient = &http.Client{Timeout: 30 * time.Second}

// proxyBaseURLs lists the Proxy instances to call: the explicit PROXY_URL plus
// every PROXY_CALLBACK_URLS entry (deduplicated). Payloads are stored on the
// Proxy host that served the request, so a purge must reach all of them.
// Environment is read per call (not cached) so rotations and tests take effect.
func proxyBaseURLs() []string {
	var out []string
	seen := map[string]bool{}
	add := func(u string) {
		u = strings.TrimSuffix(strings.TrimSpace(u), "/")
		if u != "" && !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	add(os.Getenv("PROXY_URL"))
	for _, u := range strings.Split(os.Getenv("PROXY_CALLBACK_URLS"), ",") {
		add(u)
	}
	return out
}

// proxyAdminSecret returns the HMAC key for the Proxy /admin API. The Proxy's
// admin secret is the same value Portal already holds as PROXY_SYNC_SECRET (see
// validateViaProxyFilter); PROXY_ADMIN_SECRET may override it if they diverge.
func proxyAdminSecret() string {
	if s := os.Getenv("PROXY_ADMIN_SECRET"); s != "" {
		return s
	}
	return os.Getenv("PROXY_SYNC_SECRET")
}

// SignProxyAdminRequest sets the X-Timestamp / X-Nonce / X-Signature headers the
// Proxy's admin middleware requires: HMAC-SHA256(secret, METHOD+PATH+timestamp+nonce),
// hex encoded. The query string is intentionally not part of the signed message.
func SignProxyAdminRequest(req *http.Request, secret string, now time.Time) error {
	nonceBytes := make([]byte, 16)
	if _, err := rand.Read(nonceBytes); err != nil {
		return fmt.Errorf("generate nonce: %w", err)
	}
	ts := strconv.FormatInt(now.Unix(), 10)
	nonce := hex.EncodeToString(nonceBytes)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(req.Method + req.URL.Path + ts + nonce))
	req.Header.Set("X-Timestamp", ts)
	req.Header.Set("X-Nonce", nonce)
	req.Header.Set("X-Signature", hex.EncodeToString(mac.Sum(nil)))
	return nil
}

// PurgeEnterpriseRetention asks every configured Proxy to delete the stored
// request/response payloads of one enterprise
// (DELETE /admin/retention/enterprises/:ent_id?confirm=<ent_id>).
//
// The returned error is non-nil when any Proxy could not be reached or did not
// answer 2xx; the result still describes what each instance reported so the
// caller can audit a partial purge.
func (db *DB) PurgeEnterpriseRetention(ctx context.Context, enterpriseID string) (*ProxyPurgeResult, error) {
	if enterpriseID == "" || strings.ContainsAny(enterpriseID, "/?#% ") {
		return nil, fmt.Errorf("invalid enterprise id")
	}
	bases := proxyBaseURLs()
	if len(bases) == 0 {
		return nil, ErrProxyNotConfigured
	}
	secret := proxyAdminSecret()
	if secret == "" {
		return nil, ErrProxySecretMissing
	}

	res := &ProxyPurgeResult{}
	var failures []string
	for i, base := range bases {
		target := db.purgeOneProxy(ctx, base, secret, enterpriseID)
		if target.Error != "" {
			failures = append(failures, fmt.Sprintf("proxy #%d: %s", i+1, target.Error))
			if db.logger != nil {
				db.logger.Warn("Proxy retention purge failed",
					zap.String("proxy", base), zap.String("reason", target.Error))
			}
		}
		res.DeletedFiles += target.DeletedFiles
		res.DeletedBytes += target.DeletedBytes
		res.Targets = append(res.Targets, target)
	}
	if len(failures) > 0 {
		return res, fmt.Errorf("proxy purge failed: %s", strings.Join(failures, "; "))
	}
	return res, nil
}

func (db *DB) purgeOneProxy(ctx context.Context, base, secret, enterpriseID string) ProxyPurgeTarget {
	var t ProxyPurgeTarget
	u := base + "/admin/retention/enterprises/" + url.PathEscape(enterpriseID) + "?confirm=" + url.QueryEscape(enterpriseID)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u, nil)
	if err != nil {
		t.Error = "build request failed"
		return t
	}
	if err := SignProxyAdminRequest(req, secret, time.Now()); err != nil {
		t.Error = "sign request failed"
		return t
	}
	telemetry.InjectTraceparent(ctx, req)

	resp, err := proxyAdminHTTPClient.Do(req)
	if err != nil {
		// Never echo err: it embeds the Proxy URL (internal topology).
		t.Error = "proxy unreachable"
		return t
	}
	defer func() { _ = resp.Body.Close() }()
	t.Status = resp.StatusCode
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.Error = fmt.Sprintf("proxy returned HTTP %d", resp.StatusCode)
		return t
	}
	// The Proxy reports what it removed; tolerate an empty/foreign body.
	var parsed struct {
		DeletedFiles int64 `json:"deleted_files"`
		DeletedBytes int64 `json:"deleted_bytes"`
	}
	if json.Unmarshal(body, &parsed) == nil {
		t.DeletedFiles, t.DeletedBytes = parsed.DeletedFiles, parsed.DeletedBytes
	}
	return t
}

// Command probe-upstream-models checks which catalog models are actually
// servable by their upstream provider.
//
// It sends one minimal request directly to the upstream (bypassing the
// platform), so a dead model costs nothing and never reaches user billing:
//
//   - text models:  POST {base}/chat/completions, max_tokens=1
//   - embedding:    POST {base}/embeddings, input "hi"
//   - image/video/audio: skipped unless -media (a real generation has cost)
//
// Providers without an OpenAI-compatible API (Bedrock, Mantle, ...) are
// reported as skipped; reconcile those against the vendor's model-list API.
//
// Usage (from the portal repo, with .env.go-platform loaded):
//
//	go run ./scripts/probe-upstream-models [-provider tokenrouter] [-media] [-pause-dead] [-pause-on-error]
//
// Pausing safeguards (added after the 2026-09-27 LZ mass-pause incident, when
// a transient upstream "reseller group 503" took down 60+ healthy models):
//
//   - only "dead" (provider says: no channel / model removed) pauses
//     unconditionally; 429/5xx/network are retried and NEVER pause
//   - 401/403 auth failures never pause (that is a key/account problem)
//   - per-provider circuit breaker: if >25% of probed models are non-ok, NO
//     pauses are applied for that provider at all — the run is treating an
//     account-level outage as model deaths, which is exactly the failure
//     mode that caused the incident
//   - recommended: use -pause-dead only; treat -pause-on-error as expert mode
package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ai-proxy/ai-portal-go/pkg/crypto"
)

type modelRow struct {
	Provider  string
	ProductID string
	APIModel  string
	ModelType string
}

type keyRow struct {
	Provider string
	Key      string
	BaseURL  string
}

type result struct {
	model  modelRow
	status string // ok | dead | error | skipped
	detail string
}

func env(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func main() {
	providerFlag := flag.String("provider", "", "only probe this provider")
	withMedia := flag.Bool("media", false, "also probe image/video/audio models (incurs real generation cost)")
	pauseDead := flag.Bool("pause-dead", false, "pause models whose upstream has no channel for them")
	pauseOnErr := flag.Bool("pause-on-error", false, "pause models whose upstream returns persistent errors")
	flag.Parse()

	ctx := context.Background()
	connStr := fmt.Sprintf("host=%s port=%s dbname=%s user=%s password=%s sslmode=disable",
		env("DB_HOST", "127.0.0.1"), env("DB_PORT", "5432"), env("DB_NAME", "ai_proxy_portal"),
		env("DB_USER", "postgres"), os.Getenv("DB_PASSWORD"))
	conn, err := pgx.Connect(ctx, connStr)
	if err != nil {
		fatal("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	encKey := os.Getenv("ENCRYPTION_KEY")
	if encKey == "" {
		fatal("ENCRYPTION_KEY is required")
	}

	// Active catalog models.
	models, err := loadModels(ctx, conn, *providerFlag)
	if err != nil {
		fatal("load models: %v", err)
	}
	// One enabled upstream key per provider (all of them, rotated per request
	// so the probe itself does not trip per-key RPM limits).
	keys, err := loadKeys(ctx, conn, encKey, *providerFlag)
	if err != nil {
		fatal("load keys: %v", err)
	}

	httpClient := &http.Client{Timeout: 90 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}}

	var results []result
	keyRoundRobin := map[string]int{}
	for _, m := range models {
		krs, ok := keys[m.Provider]
		if !ok || len(krs) == 0 {
			results = append(results, result{m, "skipped", "no enabled upstream key"})
			continue
		}
		start := keyRoundRobin[m.Provider]
		keyRoundRobin[m.Provider] = start + 1
		switch m.ModelType {
		case "text", "":
			results = append(results, probeChat(ctx, httpClient, krs, start, m))
		case "embedding":
			results = append(results, probeEmbedding(ctx, httpClient, krs, start, m))
		case "image", "video", "audio":
			if *withMedia {
				results = append(results, probeChat(ctx, httpClient, krs, start, m))
			} else {
				results = append(results, result{m, "skipped", m.ModelType + " model (use -media to probe, costs money)"})
			}
		default:
			results = append(results, probeChat(ctx, httpClient, krs, start, m))
		}
		time.Sleep(700 * time.Millisecond) // be polite to upstream rate limits
	}

	// Report.
	sort.Slice(results, func(i, j int) bool {
		if results[i].model.Provider != results[j].model.Provider {
			return results[i].model.Provider < results[j].model.Provider
		}
		return results[i].model.APIModel < results[j].model.APIModel
	})
	counts := map[string]int{}
	for _, r := range results {
		counts[r.status]++
	}
	fmt.Printf("\n%-14s %-42s %-10s %s\n", "PROVIDER", "MODEL", "STATUS", "DETAIL")
	fmt.Println(strings.Repeat("-", 110))
	for _, r := range results {
		d := r.detail
		if len(d) > 60 {
			d = d[:60] + "…"
		}
		fmt.Printf("%-14s %-42s %-10s %s\n", r.model.Provider, r.model.APIModel, r.status, d)
	}
	fmt.Println(strings.Repeat("-", 110))
	fmt.Printf("summary: ")
	for _, s := range []string{"ok", "dead", "transient", "auth", "error", "skipped"} {
		fmt.Printf("%s=%d ", s, counts[s])
	}
	fmt.Println()

	// Optional pausing.
	if *pauseDead || *pauseOnErr {
		// Circuit breaker: if a large fraction of one provider's models came
		// back non-ok, the problem is almost always account-level (bad key,
		// upstream group outage, rate-limit storm) — NOT the models. Pausing
		// in that state mass-pauses healthy models (seen 2026-09-27: a
		// transient LZ "reseller group 503" took down 60+ models). Skip the
		// whole provider and demand a human look instead.
		byProvider := map[string][]result{}
		for _, r := range results {
			byProvider[r.model.Provider] = append(byProvider[r.model.Provider], r)
		}
		tripped := map[string]string{}
		for p, rs := range byProvider {
			var bad int
			for _, r := range rs {
				if r.status != "ok" && r.status != "skipped" {
					bad++
				}
			}
			probed := 0
			for _, r := range rs {
				if r.status != "skipped" {
					probed++
				}
			}
			if probed > 0 && bad*4 > probed { // >25% bad
				tripped[p] = fmt.Sprintf("%d/%d non-ok", bad, probed)
			}
		}
		if len(tripped) > 0 {
			fmt.Println("\nCIRCUIT BREAKER TRIPPED — no pauses applied for these providers:")
			for p, why := range tripped {
				fmt.Printf("  %s: %s (likely account/key/outage issue, investigate before pausing)\n", p, why)
			}
		}

		var paused []string
		for _, r := range results {
			if _, skip := tripped[r.model.Provider]; skip {
				continue
			}
			// "dead" (no channel / model removed) is the only unconditional
			// pause signal. "error" pausing is opt-in and never applies to
			// auth or transient failures.
			var reason string
			switch {
			case r.status == "dead" && *pauseDead:
				reason = "probe: upstream has no channel for model"
			case r.status == "error" && *pauseOnErr:
				reason = "probe: persistent upstream error"
			default:
				continue
			}
			tag, err := conn.Exec(ctx,
				`UPDATE service_models SET status='paused', updated_at=now(),
				        paused_at=COALESCE(paused_at, now()),
				        paused_reason=$3, paused_notes=$4
				 WHERE provider=$1 AND api_model_name=$2 AND status='active'`,
				r.model.Provider, r.model.APIModel, reason,
				fmt.Sprintf("%s probe-upstream-models: %s", time.Now().UTC().Format("2006-01-02"), firstLine(r.detail)))
			if err != nil {
				fmt.Printf("pause %s/%s failed: %v\n", r.model.Provider, r.model.APIModel, err)
				continue
			}
			if tag.RowsAffected() > 0 {
				paused = append(paused, r.model.Provider+"/"+r.model.APIModel)
			}
		}
		fmt.Printf("\npaused %d models:\n", len(paused))
		for _, p := range paused {
			fmt.Println("  " + p)
		}
	}
}

func loadModels(ctx context.Context, conn *pgx.Conn, onlyProvider string) ([]modelRow, error) {
	q := `SELECT provider, product_id, COALESCE(api_model_name, product_id), COALESCE(capabilities->>'type','')
	      FROM service_models
	      WHERE status = 'active' AND deleted_at IS NULL`
	var args []interface{}
	if onlyProvider != "" {
		q += " AND provider=$1"
		args = append(args, onlyProvider)
	}
	q += " ORDER BY provider, api_model_name"
	rows, err := conn.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []modelRow
	for rows.Next() {
		var m modelRow
		if err := rows.Scan(&m.Provider, &m.ProductID, &m.APIModel, &m.ModelType); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func loadKeys(ctx context.Context, conn *pgx.Conn, encKey, onlyProvider string) (map[string][]keyRow, error) {
	q := `SELECT provider, key_encrypted, COALESCE(base_url,'')
	      FROM upstream_keys
	      WHERE enabled AND deleted_at IS NULL`
	var args []interface{}
	if onlyProvider != "" {
		q += " AND provider=$1"
		args = append(args, onlyProvider)
	}
	q += " ORDER BY provider, created_at"
	rows, err := conn.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]keyRow{}
	for rows.Next() {
		var p, enc, base string
		if err := rows.Scan(&p, &enc, &base); err != nil {
			return nil, err
		}
		if base == "" || !strings.HasPrefix(base, "http") {
			continue
		}
		// Providers without an OpenAI-compatible chat API.
		if strings.Contains(base, "amazonaws.com") || strings.Contains(base, "api.aws") {
			continue
		}
		key, err := crypto.Decrypt(enc, encKey)
		if err != nil || key == "" {
			continue
		}
		out[p] = append(out[p], keyRow{Provider: p, Key: key, BaseURL: strings.TrimRight(base, "/")})
	}
	return out, rows.Err()
}

// probeKeys runs probe against successive keys when the upstream answers 429
// (per-key rate limit), so one hot key cannot false-negative a whole batch.
// Attempts are capped: when an upstream is account-wide rate-limited every
// key 429s, rotating through dozens of keys turns a per-model check into an
// hours-long retry storm (seen 2026-09-27 with a saturated tokenrouter pool).
//
// Transient failures (429/5xx/network) are also retried here with the next
// key and a short backoff — a single 503 must never be read as "model dead".
func probeKeys(ctx context.Context, hc *http.Client, krs []keyRow, start int, m modelRow, probe func(kr keyRow) result) result {
	var last result
	attempts := len(krs)
	if attempts > 3 {
		attempts = 3
	}
	for attempt := 0; attempt < attempts; attempt++ {
		kr := krs[(start+attempt)%len(krs)]
		last = probe(kr)
		if last.status != "transient" {
			return last
		}
		time.Sleep(5 * time.Second)
	}
	return last
}

func probeChat(ctx context.Context, hc *http.Client, krs []keyRow, start int, m modelRow) result {
	return probeKeys(ctx, hc, krs, start, m, func(kr keyRow) result {
		body, _ := json.Marshal(map[string]interface{}{
			"model":      m.APIModel, // probe with the same name the proxy sends
			"messages":   []map[string]string{{"role": "user", "content": "hi"}},
			"max_tokens": 16, // some providers reject max_tokens < 16 (Sakana, gpt-5.x)
		})
		req, err := http.NewRequestWithContext(ctx, "POST", kr.BaseURL+"/chat/completions", strings.NewReader(string(body)))
		if err != nil {
			return result{m, "error", err.Error()}
		}
		req.Header.Set("Authorization", "Bearer "+kr.Key)
		req.Header.Set("Content-Type", "application/json")
		resp, err := hc.Do(req)
		return classify(m, resp, err)
	})
}

func probeEmbedding(ctx context.Context, hc *http.Client, krs []keyRow, start int, m modelRow) result {
	return probeKeys(ctx, hc, krs, start, m, func(kr keyRow) result {
		body, _ := json.Marshal(map[string]interface{}{
			"model": m.APIModel,
			"input": "hi",
		})
		req, err := http.NewRequestWithContext(ctx, "POST", kr.BaseURL+"/embeddings", strings.NewReader(string(body)))
		if err != nil {
			return result{m, "error", err.Error()}
		}
		req.Header.Set("Authorization", "Bearer "+kr.Key)
		req.Header.Set("Content-Type", "application/json")
		resp, err := hc.Do(req)
		return classify(m, resp, err)
	})
}

// classify maps an upstream response to ok / dead / transient / auth / error.
//
// "dead" is the ONLY signal safe to pause on: the provider explicitly says
// it has no channel for this model (or the model is unknown). Everything
// else can be account-level or transient and must not pause models:
//
//   - transient (429/5xx/network): retry; upstream saturation or blips
//     (2026-09-27 LZ "reseller group 503" mass-pause incident)
//   - auth (401/403): key/account problem — pausing all models of a provider
//     because one key expired would take down healthy models
//   - error: anything persistent but ambiguous; pause only via -pause-on-error
func classify(m modelRow, resp *http.Response, reqErr error) result {
	if reqErr != nil {
		return result{m, "transient", "request: " + reqErr.Error()}
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	s := string(b)
	lc := strings.ToLower(s)
	switch {
	case resp.StatusCode == http.StatusOK:
		return result{m, "ok", "200"}
	case strings.Contains(lc, "no available channel") || strings.Contains(lc, `"code":"model_not_found"`):
		// Provider has no channel for this model — safe to pause.
		return result{m, "dead", firstLine(s)}
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return result{m, "auth", fmt.Sprintf("auth %d", resp.StatusCode)}
	case resp.StatusCode == 429 || resp.StatusCode >= 500:
		return result{m, "transient", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, firstLine(s))}
	case strings.Contains(lc, "requires that either input content or output modality contain audio"):
		// Upstream channel exists; the model just demands its native modality.
		return result{m, "ok", "alive (needs audio modality)"}
	default:
		return result{m, "error", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, firstLine(s))}
	}
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}

func fatal(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "probe-upstream-models: "+format+"\n", args...)
	os.Exit(1)
}

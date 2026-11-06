package db

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ai-proxy/ai-portal-go/pkg/auditchain"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

const (
	// anchorWebhookEnv, when set, receives every new anchor as JSON so a copy
	// exists outside the database (and outside the attacker's reach).
	anchorWebhookEnv = "AUDIT_ANCHOR_WEBHOOK_URL"
	// usageAnchorSettle is how long after UTC midnight we wait before sealing a
	// day's usage digest, so in-flight webhook retries land first.
	usageAnchorSettle = 30 * time.Minute
	// usageAnchorCatchupDays bounds how many missed days one run back-fills.
	usageAnchorCatchupDays = 7
)

// anchoredTables are the hash-chained tables that get head anchors.
var anchoredTables = []string{auditchain.TableAuditLogs, auditchain.TableEnterpriseAuditLog}

// AnchorRunResult summarises one CreateAuditAnchors run.
type AnchorRunResult struct {
	Created []string // "<date>/<table>" of each new anchor
}

// chainHead is the newest chained row of a table.
type chainHead struct {
	Seq  int64
	ID   string
	Hash string
}

// tableHead returns the head of a chained table, optionally restricted to rows
// created before `before` (the end of a past UTC day). ok=false when empty.
func (db *DB) tableHead(ctx context.Context, table string, before *time.Time) (h chainHead, rows int64, ok bool, err error) {
	q := `SELECT chain_seq, id::text, row_hash FROM ` + table + ` WHERE chain_seq IS NOT NULL AND row_hash IS NOT NULL`
	args := []any{}
	if before != nil {
		q += ` AND created_at < $1`
		args = append(args, *before)
	}
	q += ` ORDER BY chain_seq DESC LIMIT 1`
	if err = db.Pool.QueryRow(ctx, q, args...).Scan(&h.Seq, &h.ID, &h.Hash); err != nil {
		if err == pgx.ErrNoRows {
			return h, 0, false, nil
		}
		return h, 0, false, err
	}
	// Rows still present at or before the head (purged rows are gone).
	if err = db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM `+table+` WHERE chain_seq <= $1`, h.Seq).Scan(&rows); err != nil {
		return h, 0, false, err
	}
	return h, rows, true, nil
}

// insertAnchor signs and stores an anchor. It is insert-only: an existing
// (anchor_date, table_name) is left untouched. Returns true when a row was
// written.
func (db *DB) insertAnchor(ctx context.Context, key []byte, a auditchain.Anchor, now time.Time) (bool, error) {
	a.CreatedAt = now.UTC().Truncate(time.Microsecond)
	a.HMAC = a.Sign(key)
	tag, err := db.Pool.Exec(ctx,
		`INSERT INTO audit_anchors
		   (anchor_date, table_name, row_count, last_id, last_seq, head_hash,
		    usage_rows, usage_cost_sum, usage_digest, hmac, created_at)
		 VALUES ($1::date, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		 ON CONFLICT (anchor_date, table_name) DO NOTHING`,
		a.AnchorDate, a.TableName, a.RowCount, a.LastID, a.LastSeq, a.HeadHash,
		a.UsageRows, a.UsageCostSum, a.UsageDigest, a.HMAC, a.CreatedAt)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	db.publishAnchor(a)
	return true, nil
}

// publishAnchor POSTs the anchor to AUDIT_ANCHOR_WEBHOOK_URL. Best effort: a
// slow or failing receiver must never fail or stall the anchoring job.
func (db *DB) publishAnchor(a auditchain.Anchor) {
	url := strings.TrimSpace(os.Getenv(anchorWebhookEnv))
	if url == "" {
		return
	}
	payload := map[string]any{
		"anchor_date": a.AnchorDate, "table_name": a.TableName, "row_count": a.RowCount,
		"last_id": a.LastID, "last_seq": a.LastSeq, "head_hash": a.HeadHash,
		"usage_rows": a.UsageRows, "usage_cost_sum": a.UsageCostSum, "usage_digest": a.UsageDigest,
		"hmac": a.HMAC, "created_at": a.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		db.warn("audit anchor webhook: bad URL", zap.Error(err))
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		db.warn("audit anchor webhook failed", zap.Error(err))
		return
	}
	_ = resp.Body.Close()
	if resp.StatusCode >= 300 {
		db.warn("audit anchor webhook non-2xx", zap.Int("status", resp.StatusCode))
	}
}

func (db *DB) warn(msg string, fields ...zap.Field) {
	if db.logger != nil {
		db.logger.Warn(msg, fields...)
	}
}

// UsageDay computes the sealed figures for one UTC day of usage_records:
// row count, SUM(total_cost) and an ordered digest over trace_id|total_cost|
// created_at. Rows stream through the digester so memory stays flat.
func (db *DB) UsageDay(ctx context.Context, day time.Time) (rows, costSum int64, digest string, err error) {
	return usageDay(ctx, db.Pool, day)
}

func usageDay(ctx context.Context, q queryer, day time.Time) (rows, costSum int64, digest string, err error) {
	start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 1)
	r, err := q.Query(ctx,
		`SELECT trace_id::text, total_cost, to_char(created_at, '`+auditchain.TimestampSQLFormat+`')
		   FROM usage_records WHERE created_at >= $1 AND created_at < $2
		  ORDER BY created_at, id`, start, end)
	if err != nil {
		return 0, 0, "", err
	}
	defer r.Close()
	d := auditchain.NewUsageDigester()
	for r.Next() {
		var trace, ts string
		var cost *int64
		if err := r.Scan(&trace, &cost, &ts); err != nil {
			return 0, 0, "", err
		}
		d.Add(trace, cost, ts)
	}
	if err := r.Err(); err != nil {
		return 0, 0, "", err
	}
	return d.Rows(), d.CostSum(), d.Hex(), nil
}

// CreateAuditAnchors writes the daily tamper-evidence anchors:
//   - for each chained table: a head anchor for yesterday (only if the previous
//     run missed it; computed from rows created before today 00:00 UTC) and a
//     head anchor for today (the current head);
//   - for usage_records: the sealed digest of each missed UTC day, up to a week.
//
// Every anchor is HMAC-signed with a key derived from encryptionKey. Existing
// anchors are never modified (insert-only), so the job is safely re-runnable.
func (db *DB) CreateAuditAnchors(ctx context.Context, encryptionKey string, now time.Time) (AnchorRunResult, error) {
	var res AnchorRunResult
	if encryptionKey == "" {
		return res, fmt.Errorf("ENCRYPTION_KEY is not set: audit anchors cannot be signed")
	}
	key := auditchain.DeriveAnchorKey(encryptionKey)
	now = now.UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	yesterday := today.AddDate(0, 0, -1)

	for _, table := range anchoredTables {
		// Yesterday's closing head (fills a gap if the portal was down).
		if h, rows, ok, err := db.tableHead(ctx, table, &today); err != nil {
			return res, fmt.Errorf("head of %s: %w", table, err)
		} else if ok {
			if err := db.addHeadAnchor(ctx, key, &res, yesterday, table, h, rows, now); err != nil {
				return res, err
			}
		}
		// Current head.
		if h, rows, ok, err := db.tableHead(ctx, table, nil); err != nil {
			return res, fmt.Errorf("head of %s: %w", table, err)
		} else if ok {
			if err := db.addHeadAnchor(ctx, key, &res, today, table, h, rows, now); err != nil {
				return res, err
			}
		}
	}

	for i := 1; i <= usageAnchorCatchupDays; i++ {
		day := today.AddDate(0, 0, -i)
		if now.Before(day.AddDate(0, 0, 1).Add(usageAnchorSettle)) {
			continue
		}
		var exists bool
		if err := db.Pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM audit_anchors WHERE anchor_date = $1::date AND table_name = $2)`,
			day.Format("2006-01-02"), auditchain.TableUsageRecords).Scan(&exists); err != nil {
			return res, err
		}
		if exists {
			continue
		}
		rows, cost, digest, err := db.UsageDay(ctx, day)
		if err != nil {
			return res, fmt.Errorf("usage digest %s: %w", day.Format("2006-01-02"), err)
		}
		a := auditchain.Anchor{
			AnchorDate: day.Format("2006-01-02"), TableName: auditchain.TableUsageRecords,
			RowCount: rows, UsageRows: &rows, UsageCostSum: &cost, UsageDigest: &digest,
		}
		if ok, err := db.insertAnchor(ctx, key, a, now); err != nil {
			return res, err
		} else if ok {
			res.Created = append(res.Created, a.AnchorDate+"/"+a.TableName)
		}
	}
	return res, nil
}

func (db *DB) addHeadAnchor(ctx context.Context, key []byte, res *AnchorRunResult, day time.Time, table string, h chainHead, rows int64, now time.Time) error {
	seq, id, hash := h.Seq, h.ID, h.Hash
	a := auditchain.Anchor{
		AnchorDate: day.Format("2006-01-02"), TableName: table, RowCount: rows,
		LastID: &id, LastSeq: &seq, HeadHash: &hash,
	}
	ok, err := db.insertAnchor(ctx, key, a, now)
	if err != nil {
		return fmt.Errorf("insert anchor %s/%s: %w", a.AnchorDate, table, err)
	}
	if ok {
		res.Created = append(res.Created, a.AnchorDate+"/"+table)
	}
	return nil
}

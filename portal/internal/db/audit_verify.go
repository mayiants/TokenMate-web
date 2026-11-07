package db

import (
	"context"
	"fmt"
	"time"

	"github.com/mayiants/ai-portal-go/pkg/auditchain"
	"github.com/jackc/pgx/v5"
)

// queryer is the read surface shared by *pgxpool.Pool and pgx.Tx, so tests can
// verify a transaction's uncommitted (tampered) state and roll it back.
type queryer interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// verifyBatchSize bounds how many chain rows are held in memory at once.
const verifyBatchSize = 5000

// VerifyOptions selects what VerifyAuditChain checks.
type VerifyOptions struct {
	Table string     // audit_logs or enterprise_audit_log
	From  *time.Time // only rows created at/after From (nil = from the start)
	To    *time.Time // only rows created before To (nil = to the head)
	// AnchorKey is the raw ENCRYPTION_KEY; empty disables anchor HMAC checks
	// (reported as a warning, never as success).
	AnchorKey string
	// IncludeUsage also recomputes usage_records day digests against their
	// anchors. Late-arriving usage rows legitimately change a sealed day, so
	// these mismatches are informational and do not flip OK.
	IncludeUsage bool
}

// AnchorMismatch describes one anchor that failed verification.
type AnchorMismatch struct {
	AnchorID   int64  `json:"anchor_id"`
	AnchorDate string `json:"anchor_date"`
	TableName  string `json:"table_name"`
	Reason     string `json:"reason"`
}

// VerifyResult is the JSON payload of GET /api/admin/audit/verify.
type VerifyResult struct {
	OK                    bool             `json:"ok"`
	Table                 string           `json:"table"`
	Checked               int64            `json:"checked"`
	FirstBrokenID         any              `json:"first_broken_id"`
	Reason                string           `json:"reason"`
	AnchorsChecked        int              `json:"anchors_checked"`
	AnchorMismatches      []AnchorMismatch `json:"anchor_mismatches"`
	SoftDeletedCount      int64            `json:"soft_deleted_count"`
	PurgedBeforeID        any              `json:"purged_before_id"`
	BackfilledRows        int64            `json:"backfilled_rows"`
	UnchainedRows         int64            `json:"unchained_rows"`
	HeadSeq               int64            `json:"head_seq"`
	HeadHash              string           `json:"head_hash"`
	AnchorsPurged         int              `json:"anchors_purged"`
	UsageAnchorsChecked   int              `json:"usage_anchors_checked,omitempty"`
	UsageAnchorMismatches []AnchorMismatch `json:"usage_anchor_mismatches,omitempty"`
	Warnings              []string         `json:"warnings"`
}

func (r *VerifyResult) breakAt(id, reason string) {
	if r.FirstBrokenID != nil {
		return
	}
	r.FirstBrokenID = id
	r.Reason = reason
}

// loadAnchors reads every anchor of a table (a handful of rows per day).
func loadAnchors(ctx context.Context, q queryer, table string) ([]auditchain.Anchor, error) {
	rows, err := q.Query(ctx,
		`SELECT id, to_char(anchor_date, 'YYYY-MM-DD'), table_name, row_count, last_id, last_seq,
		        head_hash, usage_rows, usage_cost_sum, usage_digest, hmac, created_at
		   FROM audit_anchors WHERE table_name = $1 ORDER BY anchor_date, id`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []auditchain.Anchor
	for rows.Next() {
		var a auditchain.Anchor
		if err := rows.Scan(&a.ID, &a.AnchorDate, &a.TableName, &a.RowCount, &a.LastID, &a.LastSeq,
			&a.HeadHash, &a.UsageRows, &a.UsageCostSum, &a.UsageDigest, &a.HMAC, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// VerifyAuditChain recomputes the hash chain of one audit table in Go
// (streaming, keyset-paginated) and cross-checks it against signed anchors.
func (db *DB) VerifyAuditChain(ctx context.Context, opt VerifyOptions) (*VerifyResult, error) {
	return verifyAuditChain(ctx, db.Pool, opt)
}

func verifyAuditChain(ctx context.Context, q queryer, opt VerifyOptions) (*VerifyResult, error) {
	spec, ok := auditchain.SpecFor(opt.Table)
	if !ok {
		return nil, fmt.Errorf("unsupported table %q", opt.Table)
	}
	res := &VerifyResult{Table: opt.Table, AnchorMismatches: []AnchorMismatch{}, Warnings: []string{}}
	t := opt.Table

	// --- sequence window from the optional created_at range -----------------
	var minSeq, maxSeq *int64
	if err := q.QueryRow(ctx, `SELECT MIN(chain_seq), MAX(chain_seq) FROM `+t+` WHERE chain_seq IS NOT NULL`).Scan(&minSeq, &maxSeq); err != nil {
		return nil, err
	}
	startSeq, endSeq := int64(0), int64(0)
	if minSeq != nil {
		startSeq, endSeq = *minSeq, *maxSeq
	}
	if opt.From != nil && minSeq != nil {
		var s *int64
		if err := q.QueryRow(ctx, `SELECT MIN(chain_seq) FROM `+t+` WHERE chain_seq IS NOT NULL AND created_at >= $1`, *opt.From).Scan(&s); err != nil {
			return nil, err
		}
		if s == nil {
			startSeq, endSeq = 1, 0 // empty window
		} else {
			startSeq = *s
		}
	}
	if opt.To != nil && minSeq != nil && endSeq >= startSeq {
		var s *int64
		if err := q.QueryRow(ctx, `SELECT MAX(chain_seq) FROM `+t+` WHERE chain_seq IS NOT NULL AND created_at < $1`, *opt.To).Scan(&s); err != nil {
			return nil, err
		}
		if s == nil || *s < startSeq {
			startSeq, endSeq = 1, 0
		} else if *s < endSeq {
			endSeq = *s
		}
	}

	// --- anchors, indexed by the chain_seq they pin -------------------------
	anchors, err := loadAnchors(ctx, q, t)
	if err != nil {
		return nil, err
	}
	key := []byte(nil)
	if opt.AnchorKey != "" {
		key = auditchain.DeriveAnchorKey(opt.AnchorKey)
	} else if len(anchors) > 0 {
		res.Warnings = append(res.Warnings, "ENCRYPTION_KEY unavailable: anchor HMACs were not verified")
	}
	bySeq := map[int64][]auditchain.Anchor{}
	for _, a := range anchors {
		if key != nil && !a.VerifyHMAC(key) {
			res.AnchorMismatches = append(res.AnchorMismatches, AnchorMismatch{
				AnchorID: a.ID, AnchorDate: a.AnchorDate, TableName: t,
				Reason: "anchor HMAC invalid (anchor row modified or signed with a different key)"})
			continue
		}
		if a.LastSeq == nil || a.HeadHash == nil {
			continue // nothing to pin
		}
		seq := *a.LastSeq
		switch {
		case minSeq != nil && seq < *minSeq:
			res.AnchorsPurged++ // covered rows were retired by retention
		case seq < startSeq || (endSeq >= startSeq && seq > endSeq && opt.To != nil):
			// outside the requested window
		default:
			bySeq[seq] = append(bySeq[seq], a)
		}
	}

	// --- stream the chain ---------------------------------------------------
	if endSeq >= startSeq && minSeq != nil {
		if err := streamChain(ctx, q, spec, res, startSeq, endSeq, minSeq, bySeq); err != nil {
			return nil, err
		}
	}

	// Anchors that pin a seq beyond the head we reached: the tail was cut off.
	windowEmpty := minSeq != nil && endSeq < startSeq
	if res.FirstBrokenID == nil && opt.To == nil && !windowEmpty {
		for seq, as := range bySeq {
			if seq > res.HeadSeq {
				for _, a := range as {
					res.AnchorMismatches = append(res.AnchorMismatches, AnchorMismatch{
						AnchorID: a.ID, AnchorDate: a.AnchorDate, TableName: t,
						Reason: fmt.Sprintf("anchor pins chain_seq %d but the chain ends at %d (rows truncated)", seq, res.HeadSeq)})
				}
			}
		}
	}

	// --- rows that bypassed the INSERT trigger ------------------------------
	var firstUnchained *string
	if err := q.QueryRow(ctx,
		`SELECT COUNT(*), (SELECT id::text FROM `+t+` WHERE row_hash IS NULL OR chain_seq IS NULL ORDER BY created_at LIMIT 1)
		   FROM `+t+` WHERE row_hash IS NULL OR chain_seq IS NULL`).Scan(&res.UnchainedRows, &firstUnchained); err != nil {
		return nil, err
	}
	if res.UnchainedRows > 0 && firstUnchained != nil {
		res.breakAt(*firstUnchained, fmt.Sprintf("%d row(s) carry no chain hash (inserted while the trigger was disabled)", res.UnchainedRows))
	}

	if t == auditchain.TableAuditLogs {
		if err := q.QueryRow(ctx, `SELECT COUNT(*) FROM audit_logs WHERE deleted_at IS NOT NULL`).Scan(&res.SoftDeletedCount); err != nil {
			return nil, err
		}
	}

	if opt.IncludeUsage {
		if err := verifyUsageAnchors(ctx, q, res, key); err != nil {
			return nil, err
		}
	}

	res.OK = res.FirstBrokenID == nil && len(res.AnchorMismatches) == 0
	if !res.OK && res.Reason == "" {
		res.Reason = "anchor verification failed"
	}
	return res, nil
}

// streamChain walks chain_seq startSeq..endSeq in keyset batches.
func streamChain(ctx context.Context, q queryer, spec auditchain.Spec, res *VerifyResult,
	startSeq, endSeq int64, tableMinSeq *int64, bySeq map[int64][]auditchain.Anchor) error {

	exprs := spec.SelectExprs()
	cols := ""
	for _, e := range exprs {
		cols += ", " + e
	}
	batchSQL := `SELECT id::text, chain_seq, prev_hash, row_hash` + cols + ` FROM ` + spec.Table +
		` WHERE chain_seq >= $1 AND chain_seq <= $2 ORDER BY ` + spec.Table + `.chain_seq LIMIT $3`

	// Expected predecessor hash for the first examined row.
	var expectPrev *string
	havePrevSeq := false
	var prevSeq int64
	if startSeq > *tableMinSeq {
		var h *string
		err := q.QueryRow(ctx, `SELECT row_hash FROM `+spec.Table+` WHERE chain_seq = $1`, startSeq-1).Scan(&h)
		if err != nil && err != pgx.ErrNoRows {
			return err
		}
		if h != nil {
			expectPrev, havePrevSeq, prevSeq = h, true, startSeq-1
		}
	}

	cursor := startSeq
	first := true
	for cursor <= endSeq {
		rows, err := q.Query(ctx, batchSQL, cursor, endSeq, verifyBatchSize)
		if err != nil {
			return err
		}
		n := 0
		for rows.Next() {
			var id string
			var seq int64
			var prevHash, rowHash *string
			vals := make([]*string, len(exprs))
			dest := []any{&id, &seq, &prevHash, &rowHash}
			for i := range vals {
				dest = append(dest, &vals[i])
			}
			if err := rows.Scan(dest...); err != nil {
				rows.Close()
				return err
			}
			n++
			cursor = seq + 1

			if first {
				first = false
				if startSeq == *tableMinSeq && expectPrev == nil {
					// Start of the retained chain: a pristine chain begins at
					// seq 1 with the genesis hash; anything else means the head
					// of the table was legitimately purged.
					if seq != 1 || prevHash == nil || *prevHash != auditchain.Genesis(spec.Table) {
						res.PurgedBeforeID = id
					}
				}
			}
			if havePrevSeq && seq != prevSeq+1 {
				res.breakAt(id, fmt.Sprintf("gap in chain_seq: expected %d, found %d (rows deleted)", prevSeq+1, seq))
			} else if prevHash == nil || rowHash == nil {
				res.breakAt(id, "row has no prev_hash/row_hash")
			} else if expectPrev != nil && *prevHash != *expectPrev {
				res.breakAt(id, "prev_hash does not match the previous row's row_hash (row removed or replaced)")
			} else if want := auditchain.RowHash(spec.Table, *prevHash, vals); want != *rowHash {
				res.breakAt(id, "row_hash mismatch: row content was modified")
			}
			if res.FirstBrokenID != nil {
				// First inconsistency found: nothing after it can be trusted,
				// so stop streaming instead of draining the table.
				rows.Close()
				return nil
			}
			res.Checked++
			if vals[0] != nil && *vals[0] == "0" {
				res.BackfilledRows++
			}
			for _, a := range bySeq[seq] {
				if *a.HeadHash != *rowHash {
					res.AnchorMismatches = append(res.AnchorMismatches, AnchorMismatch{
						AnchorID: a.ID, AnchorDate: a.AnchorDate, TableName: a.TableName,
						Reason: fmt.Sprintf("head hash differs at chain_seq %d: history was rewritten after the anchor", seq)})
				} else {
					res.AnchorsChecked++
				}
			}
			delete(bySeq, seq)
			expectPrev, havePrevSeq, prevSeq = rowHash, true, seq
			res.HeadSeq, res.HeadHash = seq, *rowHash
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if n == 0 {
			break
		}
	}

	// Anchors that pin a seq inside the window that we never saw: row missing.
	if res.FirstBrokenID == nil {
		for seq, as := range bySeq {
			if seq <= res.HeadSeq {
				for _, a := range as {
					res.AnchorMismatches = append(res.AnchorMismatches, AnchorMismatch{
						AnchorID: a.ID, AnchorDate: a.AnchorDate, TableName: a.TableName,
						Reason: fmt.Sprintf("anchored chain_seq %d no longer exists", seq)})
				}
				delete(bySeq, seq)
			}
		}
	}
	return nil
}

// verifyUsageAnchors recomputes sealed usage_records day digests.
func verifyUsageAnchors(ctx context.Context, q queryer, res *VerifyResult, key []byte) error {
	anchors, err := loadAnchors(ctx, q, auditchain.TableUsageRecords)
	if err != nil {
		return err
	}
	for _, a := range anchors {
		if key != nil && !a.VerifyHMAC(key) {
			res.UsageAnchorMismatches = append(res.UsageAnchorMismatches, AnchorMismatch{
				AnchorID: a.ID, AnchorDate: a.AnchorDate, TableName: a.TableName, Reason: "anchor HMAC invalid"})
			continue
		}
		day, err := time.Parse("2006-01-02", a.AnchorDate)
		if err != nil || a.UsageRows == nil || a.UsageCostSum == nil || a.UsageDigest == nil {
			continue
		}
		rows, cost, digest, err := usageDay(ctx, q, day)
		if err != nil {
			return err
		}
		res.UsageAnchorsChecked++
		if rows != *a.UsageRows || cost != *a.UsageCostSum || digest != *a.UsageDigest {
			res.UsageAnchorMismatches = append(res.UsageAnchorMismatches, AnchorMismatch{
				AnchorID: a.ID, AnchorDate: a.AnchorDate, TableName: a.TableName,
				Reason: fmt.Sprintf("usage day differs from the sealed anchor (anchored rows=%d cost=%d, now rows=%d cost=%d)",
					*a.UsageRows, *a.UsageCostSum, rows, cost)})
		}
	}
	return nil
}

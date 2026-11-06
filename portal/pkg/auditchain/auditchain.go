// Package auditchain implements, in Go, the hash-chain canonicalisation that
// migration 0034 implements in plpgsql, so verification can recompute every
// row hash without trusting (or even calling) the database triggers.
//
// The serialisation is documented in migrations/0034_audit_tamper_evidence.sql;
// any change here must be mirrored there and the chain version bumped.
package auditchain

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"strconv"
	"strings"
	"time"
)

const (
	// TableAuditLogs and TableEnterpriseAuditLog are the chained tables.
	TableAuditLogs          = "audit_logs"
	TableEnterpriseAuditLog = "enterprise_audit_log"
	// TableUsageRecords is only used as an anchor table_name for usage digests.
	TableUsageRecords = "usage_records"

	hashTag    = "audit-chain-v1|"
	genesisTag = "audit-chain-v1-genesis|"
	anchorTag  = "audit-anchor-v1"

	// TimestampLayout renders timestamps exactly like the SQL expression
	// to_char(col, 'YYYY-MM-DD"T"HH24:MI:SS.US') (always 6 fractional digits).
	TimestampLayout = "2006-01-02T15:04:05.000000"
	// TimestampSQLFormat is the to_char pattern matching TimestampLayout.
	TimestampSQLFormat = `YYYY-MM-DD"T"HH24:MI:SS.US`
)

// Kind selects how a column is rendered to canonical text.
type Kind int

const (
	// KindText renders col::text.
	KindText Kind = iota
	// KindTimestamp renders to_char(col, TimestampSQLFormat).
	KindTimestamp
)

// Field is one hashed column.
type Field struct {
	Column string
	Kind   Kind
}

// Spec describes a chained table: its hashed fields in canonical order.
type Spec struct {
	Table  string
	Fields []Field
}

func text(cols ...string) []Field {
	out := make([]Field, len(cols))
	for i, c := range cols {
		out[i] = Field{Column: c, Kind: KindText}
	}
	return out
}

// AuditLogsSpec lists the hashed audit_logs columns. user_email, ip_address,
// user_agent (GDPR-rewritable) and deleted_at/deleted_by/delete_reason
// (soft delete) are deliberately excluded.
var AuditLogsSpec = Spec{
	Table: TableAuditLogs,
	Fields: append(text(
		"chain_version", "chain_seq", "id", "log_id", "user_id", "category", "action", "level",
		"message", "details", "target_type", "target_id", "old_value", "new_value"),
		Field{Column: "created_at", Kind: KindTimestamp}),
}

// EnterpriseAuditLogSpec lists the hashed enterprise_audit_log columns.
var EnterpriseAuditLogSpec = Spec{
	Table: TableEnterpriseAuditLog,
	Fields: append(text(
		"chain_version", "chain_seq", "id", "enterprise_id", "action", "amount", "balance_before",
		"balance_after", "description", "created_by", "category", "change_type", "changed_by",
		"delta", "field_name", "reason", "user_id", "old_value", "new_value", "old_value_str",
		"new_value_str", "target_type", "target_id"), Field{Column: "created_at", Kind: KindTimestamp}),
}

// SpecFor returns the spec for a chained table name.
func SpecFor(table string) (Spec, bool) {
	switch table {
	case TableAuditLogs:
		return AuditLogsSpec, true
	case TableEnterpriseAuditLog:
		return EnterpriseAuditLogSpec, true
	}
	return Spec{}, false
}

// SelectExprs returns the SQL expressions (one per field, canonical order)
// that render each hashed column to the same text the trigger hashes.
func (s Spec) SelectExprs() []string {
	out := make([]string, len(s.Fields))
	for i, f := range s.Fields {
		switch f.Kind {
		case KindTimestamp:
			out[i] = "to_char(" + f.Column + ", '" + TimestampSQLFormat + "')"
		default:
			out[i] = f.Column + "::text"
		}
	}
	return out
}

// Encode serialises field values: nil -> "N;", otherwise "V<len>:<value>;"
// where <len> is the byte length. Length prefixes make the encoding injective.
func Encode(vals []*string) string {
	var b strings.Builder
	for _, v := range vals {
		if v == nil {
			b.WriteString("N;")
			continue
		}
		b.WriteByte('V')
		b.WriteString(strconv.Itoa(len(*v)))
		b.WriteByte(':')
		b.WriteString(*v)
		b.WriteByte(';')
	}
	return b.String()
}

// Genesis is the prev_hash of the first row of a table's chain.
func Genesis(table string) string {
	sum := sha256.Sum256([]byte(genesisTag + table))
	return hex.EncodeToString(sum[:])
}

// RowHash computes the row_hash for a row given its predecessor's hash.
func RowHash(table, prevHash string, vals []*string) string {
	sum := sha256.Sum256([]byte(hashTag + table + "|" + prevHash + "|" + Encode(vals)))
	return hex.EncodeToString(sum[:])
}

// Str is a small helper to build a *string field value.
func Str(s string) *string { return &s }

// ---------------------------------------------------------------------------
// Anchors
// ---------------------------------------------------------------------------

// Anchor is one row of audit_anchors.
type Anchor struct {
	ID           int64
	AnchorDate   string // YYYY-MM-DD (UTC)
	TableName    string
	RowCount     int64
	LastID       *string
	LastSeq      *int64
	HeadHash     *string
	UsageRows    *int64
	UsageCostSum *int64
	UsageDigest  *string
	HMAC         string
	CreatedAt    time.Time
}

// DeriveAnchorKey derives the anchor-signing key from ENCRYPTION_KEY
// (HKDF-like single-step expand: HMAC(secret, "audit-anchor-v1")) so the raw
// encryption key is never used directly as an HMAC key.
func DeriveAnchorKey(encryptionKey string) []byte {
	m := hmac.New(sha256.New, []byte(encryptionKey))
	m.Write([]byte(anchorTag))
	return m.Sum(nil)
}

func optInt(v *int64) *string {
	if v == nil {
		return nil
	}
	s := strconv.FormatInt(*v, 10)
	return &s
}

// Canonical returns the byte string covered by the anchor HMAC. created_at is
// included (UTC, RFC3339 with microseconds) so an attacker cannot re-date an
// anchor.
func (a Anchor) Canonical() []byte {
	vals := []*string{
		Str(a.AnchorDate),
		Str(a.TableName),
		Str(strconv.FormatInt(a.RowCount, 10)),
		a.LastID,
		optInt(a.LastSeq),
		a.HeadHash,
		optInt(a.UsageRows),
		optInt(a.UsageCostSum),
		a.UsageDigest,
		Str(a.CreatedAt.UTC().Format("2006-01-02T15:04:05.000000Z")),
	}
	return []byte(anchorTag + "|" + Encode(vals))
}

// Sign returns the hex HMAC-SHA256 of the anchor under key.
func (a Anchor) Sign(key []byte) string {
	m := hmac.New(sha256.New, key)
	m.Write(a.Canonical())
	return hex.EncodeToString(m.Sum(nil))
}

// VerifyHMAC reports whether a.HMAC is the valid signature under key.
func (a Anchor) VerifyHMAC(key []byte) bool {
	want, err := hex.DecodeString(a.Sign(key))
	if err != nil {
		return false
	}
	got, err := hex.DecodeString(a.HMAC)
	if err != nil {
		return false
	}
	return hmac.Equal(want, got)
}

// UsageDigester accumulates the ordered digest of usage_records rows for one
// day: sha256 over "trace_id|total_cost|created_at\n" lines.
type UsageDigester struct {
	h    hash.Hash
	rows int64
	cost int64
}

// NewUsageDigester returns an empty digester.
func NewUsageDigester() *UsageDigester { return &UsageDigester{h: sha256.New()} }

// Add appends one row. createdAt must already be rendered with TimestampLayout.
func (d *UsageDigester) Add(traceID string, totalCost *int64, createdAt string) {
	c := ""
	if totalCost != nil {
		c = strconv.FormatInt(*totalCost, 10)
		d.cost += *totalCost
	}
	d.h.Write([]byte(traceID + "|" + c + "|" + createdAt + "\n"))
	d.rows++
}

// Rows returns the number of rows added.
func (d *UsageDigester) Rows() int64 { return d.rows }

// CostSum returns the sum of total_cost over added rows.
func (d *UsageDigester) CostSum() int64 { return d.cost }

// Hex returns the lower-hex digest.
func (d *UsageDigester) Hex() string { return hex.EncodeToString(d.h.Sum(nil)) }

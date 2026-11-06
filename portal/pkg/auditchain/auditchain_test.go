package auditchain

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncode_NullEmptyAndLengthPrefix(t *testing.T) {
	assert.Equal(t, "", Encode(nil))
	assert.Equal(t, "N;", Encode([]*string{nil}))
	assert.Equal(t, "V0:;", Encode([]*string{Str("")}))
	assert.Equal(t, "V3:abc;N;V1:x;", Encode([]*string{Str("abc"), nil, Str("x")}))
	// Byte length, not rune count: "é" is 2 bytes, "☃" is 3.
	assert.Equal(t, "V5:é☃;", Encode([]*string{Str("é☃")}))
}

func TestEncode_IsInjective(t *testing.T) {
	// Values that would collide under naive delimiter joining must not.
	a := Encode([]*string{Str("a;V1:b"), Str("c")})
	b := Encode([]*string{Str("a"), Str("b;V1:c")})
	assert.NotEqual(t, a, b)
	// NULL is distinct from the literal text "N;" and from "".
	assert.NotEqual(t, Encode([]*string{nil}), Encode([]*string{Str("N;")}))
	assert.NotEqual(t, Encode([]*string{nil}), Encode([]*string{Str("")}))
	// Field boundaries matter.
	assert.NotEqual(t, Encode([]*string{Str("ab"), Str("c")}), Encode([]*string{Str("a"), Str("bc")}))
}

func TestGenesisAndRowHash_KnownVectors(t *testing.T) {
	sum := sha256.Sum256([]byte("audit-chain-v1-genesis|audit_logs"))
	assert.Equal(t, hex.EncodeToString(sum[:]), Genesis(TableAuditLogs))
	assert.NotEqual(t, Genesis(TableAuditLogs), Genesis(TableEnterpriseAuditLog))

	vals := []*string{Str("1"), nil, Str("x")}
	prev := Genesis(TableAuditLogs)
	want := sha256.Sum256([]byte("audit-chain-v1|audit_logs|" + prev + "|V1:1;N;V1:x;"))
	assert.Equal(t, hex.EncodeToString(want[:]), RowHash(TableAuditLogs, prev, vals))
}

func TestRowHash_BindsPrevTableAndEveryField(t *testing.T) {
	prev := Genesis(TableAuditLogs)
	base := []*string{Str("a"), Str("b"), nil}
	h := RowHash(TableAuditLogs, prev, base)

	assert.Equal(t, h, RowHash(TableAuditLogs, prev, base), "deterministic")
	assert.NotEqual(t, h, RowHash(TableEnterpriseAuditLog, prev, base), "table is bound")
	assert.NotEqual(t, h, RowHash(TableAuditLogs, Genesis(TableEnterpriseAuditLog), base), "prev hash is bound")
	for i := range base {
		mut := append([]*string(nil), base...)
		if mut[i] == nil {
			mut[i] = Str("")
		} else {
			mut[i] = Str(*mut[i] + "!")
		}
		assert.NotEqual(t, h, RowHash(TableAuditLogs, prev, mut), "field %d is bound", i)
	}
}

// TestChainRecompute simulates what the verifier does: walk rows, relink, and
// detect a modification, deletion and reordering.
func TestChainRecompute(t *testing.T) {
	type row struct {
		vals []*string
		prev string
		hash string
	}
	build := func(msgs ...string) []row {
		var rows []row
		prev := Genesis(TableAuditLogs)
		for i, m := range msgs {
			vals := []*string{Str("1"), Str(string(rune('1' + i))), Str(m)}
			h := RowHash(TableAuditLogs, prev, vals)
			rows = append(rows, row{vals: vals, prev: prev, hash: h})
			prev = h
		}
		return rows
	}
	verify := func(rows []row) int {
		prev := Genesis(TableAuditLogs)
		for i, r := range rows {
			if r.prev != prev || RowHash(TableAuditLogs, r.prev, r.vals) != r.hash {
				return i
			}
			prev = r.hash
		}
		return -1
	}

	rows := build("a", "b", "c", "d")
	assert.Equal(t, -1, verify(rows))

	mod := append([]row(nil), rows...)
	mod[1] = row{vals: []*string{Str("1"), Str("2"), Str("FORGED")}, prev: rows[1].prev, hash: rows[1].hash}
	assert.Equal(t, 1, verify(mod), "content change is caught at the modified row")

	del := append(append([]row(nil), rows[:1]...), rows[2:]...)
	assert.Equal(t, 1, verify(del), "deletion breaks the next link")

	swap := []row{rows[0], rows[2], rows[1], rows[3]}
	assert.Equal(t, 1, verify(swap), "reordering breaks linkage")
}

func TestSpecs_ExcludePIIAndSoftDeleteColumns(t *testing.T) {
	for _, spec := range []Spec{AuditLogsSpec, EnterpriseAuditLogSpec} {
		cols := map[string]bool{}
		for _, f := range spec.Fields {
			cols[f.Column] = true
		}
		for _, banned := range []string{"user_email", "ip_address", "user_agent", "deleted_at", "deleted_by", "delete_reason", "row_hash", "prev_hash"} {
			assert.False(t, cols[banned], "%s must not be hashed in %s", banned, spec.Table)
		}
		assert.True(t, cols["chain_seq"])
		assert.True(t, cols["created_at"])
	}
	s, ok := SpecFor(TableAuditLogs)
	require.True(t, ok)
	assert.Equal(t, AuditLogsSpec.Table, s.Table)
	_, ok = SpecFor("users")
	assert.False(t, ok)
}

func TestSelectExprs(t *testing.T) {
	exprs := AuditLogsSpec.SelectExprs()
	require.Len(t, exprs, len(AuditLogsSpec.Fields))
	assert.Equal(t, "chain_version::text", exprs[0])
	assert.Equal(t, `to_char(created_at, 'YYYY-MM-DD"T"HH24:MI:SS.US')`, exprs[len(exprs)-1])
}

func TestTimestampLayoutMatchesSQLFormat(t *testing.T) {
	ts := time.Date(2026, 11, 6, 13, 0, 5, 120000000, time.UTC)
	assert.Equal(t, "2026-11-06T13:00:05.120000", ts.Format(TimestampLayout), "always six fractional digits, like to_char US")
}

func TestAnchorSignVerify(t *testing.T) {
	key := DeriveAnchorKey("0123456789abcdef0123456789abcdef")
	id, hash, digest := "42", "deadbeef", "cafe"
	seq, rows, cost := int64(7), int64(3), int64(900)
	a := Anchor{
		AnchorDate: "2026-11-05", TableName: TableAuditLogs, RowCount: 7, LastID: &id, LastSeq: &seq, HeadHash: &hash,
		UsageRows: &rows, UsageCostSum: &cost, UsageDigest: &digest,
		CreatedAt: time.Date(2026, 11, 6, 0, 5, 0, 123456000, time.UTC),
	}
	a.HMAC = a.Sign(key)
	assert.True(t, a.VerifyHMAC(key))
	assert.False(t, a.VerifyHMAC(DeriveAnchorKey("another-key-another-key-another-key")))

	mutations := map[string]func(Anchor) Anchor{
		"date":  func(x Anchor) Anchor { x.AnchorDate = "2026-11-04"; return x },
		"table": func(x Anchor) Anchor { x.TableName = TableEnterpriseAuditLog; return x },
		"count": func(x Anchor) Anchor { x.RowCount++; return x },
		"id":    func(x Anchor) Anchor { v := "43"; x.LastID = &v; return x },
		"seq":   func(x Anchor) Anchor { v := int64(8); x.LastSeq = &v; return x },
		"head":  func(x Anchor) Anchor { v := "other"; x.HeadHash = &v; return x },
		"urows": func(x Anchor) Anchor { v := int64(4); x.UsageRows = &v; return x },
		"ucost": func(x Anchor) Anchor { v := int64(1); x.UsageCostSum = &v; return x },
		"udig":  func(x Anchor) Anchor { v := "x"; x.UsageDigest = &v; return x },
		"time":  func(x Anchor) Anchor { x.CreatedAt = x.CreatedAt.Add(time.Microsecond); return x },
		"nil":   func(x Anchor) Anchor { x.LastID = nil; return x },
	}
	for name, mut := range mutations {
		assert.False(t, mut(a).VerifyHMAC(key), "changing %s must invalidate the HMAC", name)
	}

	bad := a
	bad.HMAC = "not-hex"
	assert.False(t, bad.VerifyHMAC(key))
}

func TestDeriveAnchorKey_IsDomainSeparated(t *testing.T) {
	k := DeriveAnchorKey("secret-secret-secret-secret-secret")
	assert.Len(t, k, 32)
	assert.NotEqual(t, []byte("secret-secret-secret-secret-secret"), k, "raw encryption key must never be the HMAC key")
	assert.Equal(t, k, DeriveAnchorKey("secret-secret-secret-secret-secret"))
	assert.NotEqual(t, k, DeriveAnchorKey("secret-secret-secret-secret-secreT"))
}

func TestUsageDigester(t *testing.T) {
	c1, c2 := int64(100), int64(250)
	d := NewUsageDigester()
	d.Add("11111111-1111-1111-1111-111111111111", &c1, "2026-11-05T10:00:00.000000")
	d.Add("22222222-2222-2222-2222-222222222222", &c2, "2026-11-05T10:00:01.000000")
	d.Add("33333333-3333-3333-3333-333333333333", nil, "2026-11-05T10:00:02.000000")
	assert.Equal(t, int64(3), d.Rows())
	assert.Equal(t, int64(350), d.CostSum())

	want := sha256.Sum256([]byte(
		"11111111-1111-1111-1111-111111111111|100|2026-11-05T10:00:00.000000\n" +
			"22222222-2222-2222-2222-222222222222|250|2026-11-05T10:00:01.000000\n" +
			"33333333-3333-3333-3333-333333333333||2026-11-05T10:00:02.000000\n"))
	assert.Equal(t, hex.EncodeToString(want[:]), d.Hex())

	// Order matters.
	d2 := NewUsageDigester()
	d2.Add("22222222-2222-2222-2222-222222222222", &c2, "2026-11-05T10:00:01.000000")
	d2.Add("11111111-1111-1111-1111-111111111111", &c1, "2026-11-05T10:00:00.000000")
	assert.NotEqual(t, d.Hex(), d2.Hex())
}

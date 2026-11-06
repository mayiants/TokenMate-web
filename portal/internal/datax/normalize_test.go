package datax

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNormalizeUUIDRoundTrip(t *testing.T) {
	var raw [16]byte
	for i := range raw {
		raw[i] = byte(i*7 + 3)
	}

	out := NormalizeExportValue(raw, Column{})
	s, ok := out.(string)
	if !ok {
		t.Fatalf("uuid: expected string, got %T (%v)", out, out)
	}

	parsed, err := uuid.Parse(s)
	if err != nil {
		t.Fatalf("uuid: %q is not a valid uuid string: %v", s, err)
	}
	if parsed != uuid.UUID(raw) {
		t.Fatalf("uuid: roundtrip mismatch: got %v want %v", parsed, uuid.UUID(raw))
	}
}

func TestNormalizeByteaRoundTrip(t *testing.T) {
	col := Column{Name: "payload", DataType: "bytea", UDTName: "bytea"}
	raw := []byte{0x00, 0x01, 0x02, 0x7f, 0x80, 0xff, 'h', 'i'}

	out := NormalizeExportValue(raw, col)
	s, ok := out.(string)
	if !ok {
		t.Fatalf("bytea: expected string, got %T", out)
	}
	if !strings.HasPrefix(s, ByteaPrefix) {
		t.Fatalf("bytea: missing prefix, got %q", s)
	}

	back := ImportArg(s, col)
	got, ok := back.([]byte)
	if !ok {
		t.Fatalf("bytea import: expected []byte, got %T", back)
	}
	if !bytes.Equal(got, raw) {
		t.Fatalf("bytea: roundtrip mismatch: got %v want %v", got, raw)
	}
}

func TestNormalizeTimeRFC3339(t *testing.T) {
	col := Column{Name: "created_at", DataType: "timestamp with time zone", UDTName: "timestamptz"}
	tt := time.Date(2026, 7, 9, 18, 43, 0, 0, time.UTC)

	out := NormalizeExportValue(tt, col)
	s, ok := out.(string)
	if !ok {
		t.Fatalf("time: expected string, got %T", out)
	}
	if s != "2026-07-09T18:43:00Z" {
		t.Fatalf("time: unexpected RFC3339 rendering %q", s)
	}

	back, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("time: cannot re-parse %q: %v", s, err)
	}
	if !back.Equal(tt) {
		t.Fatalf("time: roundtrip mismatch: got %v want %v", back, tt)
	}
}

func TestNormalizeJSONBRoundTrip(t *testing.T) {
	col := Column{Name: "prices", DataType: "jsonb", UDTName: "jsonb"}

	// pgx delivers jsonb as raw bytes; export should decode to a native value.
	exported := NormalizeExportValue([]byte(`{"a":1,"b":[2,3]}`), col)
	m, ok := exported.(map[string]interface{})
	if !ok {
		t.Fatalf("jsonb export: expected map, got %T (%v)", exported, exported)
	}
	if m["a"] != float64(1) {
		t.Fatalf("jsonb export: bad decode: %v", m)
	}

	// Import should re-marshal the native value back to JSON text for pgx.
	arg := ImportArg(m, col)
	s, ok := arg.(string)
	if !ok {
		t.Fatalf("jsonb import: expected string, got %T", arg)
	}
	if !strings.Contains(s, `"a":1`) || !strings.Contains(s, `"b":[2,3]`) {
		t.Fatalf("jsonb import: unexpected json %q", s)
	}
}

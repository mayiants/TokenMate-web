package datax

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// ByteaPrefix marks a JSON string that encodes a bytea value as base64. On
// import any string with this prefix is decoded back to []byte.
const ByteaPrefix = "__bytea_base64__:"

// NormalizeExportValue converts a driver value (as returned by pgx
// rows.Values()) into a JSON-portable equivalent that can be re-imported
// losslessly:
//
//   - json/jsonb columns (detected via col) are decoded into their native
//     map/array/scalar form so the export is readable and editable.
//   - bytea columns (and any unexpected []byte) become "<prefix><base64>".
//   - time.Time -> RFC3339 string (UTC).
//   - [16]byte (pgx uuid) -> standard UUID string.
//   - pgtype.Numeric -> int64 when integral, else float64.
//   - everything else (int64/int32/string/bool/float64/nested maps) passes through.
func NormalizeExportValue(v interface{}, col Column) interface{} {
	if v == nil {
		return nil
	}

	// Column-aware decoding first: a jsonb value arrives as []byte but must NOT
	// be mistaken for bytea.
	if col.isJSON() {
		return normalizeJSON(v)
	}
	if col.isBytea() {
		if b, ok := v.([]byte); ok {
			return ByteaPrefix + base64.StdEncoding.EncodeToString(b)
		}
	}

	switch x := v.(type) {
	case time.Time:
		return x.UTC().Format(time.RFC3339)
	case [16]byte:
		return uuid.UUID(x).String()
	case []byte:
		// Defensive: a []byte that is neither json nor bytea. Encode as bytea.
		return ByteaPrefix + base64.StdEncoding.EncodeToString(x)
	case pgtype.Numeric:
		return numericToInterface(x)
	case pgtype.Int8:
		if !x.Valid {
			return nil
		}
		return x.Int64
	case pgtype.Int4:
		if !x.Valid {
			return nil
		}
		return int64(x.Int32)
	case pgtype.Int2:
		if !x.Valid {
			return nil
		}
		return int64(x.Int16)
	case pgtype.Float8:
		if !x.Valid {
			return nil
		}
		return x.Float64
	case pgtype.Float4:
		if !x.Valid {
			return nil
		}
		return float64(x.Float32)
	case pgtype.Bool:
		if !x.Valid {
			return nil
		}
		return x.Bool
	case pgtype.Date:
		if !x.Valid {
			return nil
		}
		return x.Time.UTC().Format("2006-01-02")
	case pgtype.Timestamp:
		if !x.Valid {
			return nil
		}
		return x.Time.UTC().Format(time.RFC3339)
	case pgtype.Timestamptz:
		if !x.Valid {
			return nil
		}
		return x.Time.UTC().Format(time.RFC3339)
	default:
		// int64/int32/int/string/bool/float64/float32 and nested maps/slices
		// (already-decoded jsonb) marshal directly.
		return v
	}
}

func normalizeJSON(v interface{}) interface{} {
	switch x := v.(type) {
	case []byte:
		return decodeJSON(x)
	case string:
		return decodeJSON([]byte(x))
	default:
		// Already-decoded (e.g. map[string]interface{}); pass through.
		return v
	}
}

func decodeJSON(b []byte) interface{} {
	if len(b) == 0 {
		return nil
	}
	var out interface{}
	if err := json.Unmarshal(b, &out); err != nil {
		// Not valid JSON (should not happen for json/jsonb) — keep the raw text.
		return string(b)
	}
	return out
}

func numericToInterface(n pgtype.Numeric) interface{} {
	if !n.Valid {
		return nil
	}
	fv, err := n.Float64Value()
	if err != nil || !fv.Valid {
		return nil
	}
	f := fv.Float64
	if f == math.Trunc(f) && math.Abs(f) < 9.007199254740992e15 { // < 2^53
		return int64(f)
	}
	return f
}

// ImportArg converts a JSON-decoded value back into a driver argument suitable
// for the target column:
//
//   - json/jsonb columns: re-marshal to a JSON string (pgx accepts text for json).
//   - bytea columns: decode a "<prefix><base64>" string back to []byte.
//   - integer columns: coerce JSON numbers (json.Number / float64) to int64 so
//     pgx does not reject a float for an int8/int4 column.
//   - float / numeric columns: coerce to float64.
//   - uuid / time / date columns: pass the string through; pgx parses RFC3339
//     for timestamptz and UUID text for uuid columns.
func ImportArg(v interface{}, col Column) interface{} {
	if v == nil {
		return nil
	}

	if col.isJSON() {
		b, err := json.Marshal(v)
		if err != nil {
			return nil
		}
		return string(b)
	}
	if col.isBytea() {
		if s, ok := v.(string); ok && strings.HasPrefix(s, ByteaPrefix) {
			b, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(s, ByteaPrefix))
			if err != nil {
				return nil
			}
			return b
		}
		return v
	}

	switch {
	case col.isInteger():
		if i, ok := toInt64(v); ok {
			return i
		}
	case col.isFloat(), col.isNumeric():
		if f, ok := toFloat64(v); ok {
			return f
		}
	case col.isBool():
		// JSON gives us a real bool; pass through.
		return v
	}

	// uuid / time / date / text / varchar: the JSON string is accepted by pgx as-is.
	return v
}

func toInt64(v interface{}) (int64, bool) {
	switch x := v.(type) {
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return i, true
		}
		if f, err := strconv.ParseFloat(x.String(), 64); err == nil {
			return int64(f), true
		}
	case float64:
		return int64(x), true
	case float32:
		return int64(x), true
	case int64:
		return x, true
	case int:
		return int64(x), true
	case int32:
		return int64(x), true
	case string:
		if i, err := strconv.ParseInt(x, 10, 64); err == nil {
			return i, true
		}
	}
	return 0, false
}

func toFloat64(v interface{}) (float64, bool) {
	switch x := v.(type) {
	case json.Number:
		if f, err := x.Float64(); err == nil {
			return f, true
		}
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int64:
		return float64(x), true
	case int:
		return float64(x), true
	case string:
		if f, err := strconv.ParseFloat(x, 64); err == nil {
			return f, true
		}
	}
	return 0, false
}

// validateIdentifier rejects anything that is not a plain snake_case
// identifier, guarding the dynamic SQL we build from the whitelist and
// information_schema.
func validateIdentifier(s string) error {
	if s == "" {
		return fmt.Errorf("empty identifier")
	}
	for i, r := range s {
		if r == '_' || (r >= 'a' && r <= 'z') || (i > 0 && r >= '0' && r <= '9') {
			continue
		}
		return fmt.Errorf("invalid identifier %q", s)
	}
	if s[0] >= '0' && s[0] <= '9' {
		return fmt.Errorf("invalid identifier %q", s)
	}
	return nil
}

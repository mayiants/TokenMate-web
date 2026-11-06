// Package datax implements the L2 "business data portability" layer from the
// data-backup-and-migration strategy: a domain-level export / import tool for
// core master data. It dumps business rows to one JSON file per entity (plus a
// manifest.json) and loads them back into a database whose schema may have
// evolved, matching columns by name and reconciling primary keys via
// information_schema.
package datax

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// CoreEntities is the ordered whitelist of core master-data tables. The order
// follows foreign-key dependencies so that importing in this order keeps
// references valid (a row is inserted only after the rows it references).
//
// Large observational tables (usage_records, audit_logs, provider_health_stats*)
// are intentionally excluded: they are handled by L0 pg_dump or time-windowed
// CSV archives.
var CoreEntities = []string{
	"providers",
	"user_groups",
	"subscription_plans",
	"enterprises",
	"users",
	"enterprise_members",
	"api_keys",
	"upstream_keys",
	"system_settings",
	"service_models",
	"model_pricing_rules",
	"model_provider_routing",
	"exchange_rates",
	"vouchers",
	"voucher_uses",
	"orders",
	"subscription_purchases",
	"balance_transactions",
	"announcements",
	"announcement_reads",
	"payment_gateways",
	"payment_config",
	"enterprise_audit_log",
	"enterprise_member_role_log",
}

// coreEntityIndex lets resolveEntities preserve whitelist order for an
// arbitrary user-supplied subset.
var coreEntityIndex = func() map[string]int {
	m := make(map[string]int, len(CoreEntities))
	for i, name := range CoreEntities {
		m[name] = i
	}
	return m
}()

// Column describes a single column as reported by information_schema.
type Column struct {
	Name     string
	DataType string // information_schema data_type, e.g. "bigint", "jsonb"
	UDTName  string // underlying type name, e.g. "int8", "jsonb", "uuid"
}

func (c Column) isJSON() bool    { return c.UDTName == "json" || c.UDTName == "jsonb" }
func (c Column) isBytea() bool   { return c.UDTName == "bytea" }
func (c Column) isBool() bool    { return c.DataType == "boolean" }
func (c Column) isNumeric() bool { return c.DataType == "numeric" }
func (c Column) isInteger() bool {
	switch c.DataType {
	case "smallint", "integer", "bigint":
		return true
	}
	return false
}
func (c Column) isFloat() bool {
	switch c.DataType {
	case "real", "double precision":
		return true
	}
	return false
}

// ResolveEntities returns the subset of CoreEntities requested by names,
// preserving whitelist (foreign-key) order. An empty names slice means "all
// core entities". Unknown names are returned in unknown so the caller can warn.
func ResolveEntities(names []string) (ordered []string, unknown []string) {
	if len(names) == 0 {
		return append([]string(nil), CoreEntities...), nil
	}
	want := make(map[string]bool, len(names))
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		want[n] = true
		if _, ok := coreEntityIndex[n]; !ok {
			unknown = append(unknown, n)
		}
	}
	for _, n := range CoreEntities {
		if want[n] {
			ordered = append(ordered, n)
		}
	}
	return ordered, unknown
}

// ParseEntityList splits a comma-separated --entities flag value into trimmed
// names. An empty input yields an empty slice (meaning "all").
func ParseEntityList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// TableExists reports whether a table exists in the public schema.
func TableExists(ctx context.Context, pool *pgxpool.Pool, table string) (bool, error) {
	var exists bool
	err := pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM information_schema.tables
			WHERE table_schema = 'public' AND table_name = $1)`, table,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check table %q exists: %w", table, err)
	}
	return exists, nil
}

// TableColumns returns the columns of table in ordinal order. If the table does
// not exist it returns (nil, nil).
func TableColumns(ctx context.Context, pool *pgxpool.Pool, table string) ([]Column, error) {
	rows, err := pool.Query(ctx,
		`SELECT column_name, data_type, udt_name
		 FROM information_schema.columns
		 WHERE table_schema = 'public' AND table_name = $1
		 ORDER BY ordinal_position`, table)
	if err != nil {
		return nil, fmt.Errorf("list columns of %q: %w", table, err)
	}
	defer rows.Close()

	var cols []Column
	for rows.Next() {
		var c Column
		if err := rows.Scan(&c.Name, &c.DataType, &c.UDTName); err != nil {
			return nil, fmt.Errorf("scan column of %q: %w", table, err)
		}
		cols = append(cols, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate columns of %q: %w", table, err)
	}
	return cols, nil
}

// PrimaryKeyColumns returns the primary-key column names of table in their
// declaration order (supports composite keys). It returns an empty slice when
// the table has no primary key.
func PrimaryKeyColumns(ctx context.Context, pool *pgxpool.Pool, table string) ([]string, error) {
	rows, err := pool.Query(ctx,
		`SELECT k.column_name
		 FROM information_schema.table_constraints tc
		 JOIN information_schema.key_column_usage k
		   ON k.constraint_name = tc.constraint_name
		  AND k.table_schema   = tc.table_schema
		  AND k.table_name     = tc.table_name
		 WHERE tc.table_schema = 'public'
		   AND tc.table_name   = $1
		   AND tc.constraint_type = 'PRIMARY KEY'
		 ORDER BY k.ordinal_position`, table)
	if err != nil {
		return nil, fmt.Errorf("list primary key of %q: %w", table, err)
	}
	defer rows.Close()

	var pk []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan primary key of %q: %w", table, err)
		}
		pk = append(pk, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate primary key of %q: %w", table, err)
	}
	return pk, nil
}

// HasColumn reports whether table currently has a column named col.
func HasColumn(cols []Column, col string) bool {
	for _, c := range cols {
		if c.Name == col {
			return true
		}
	}
	return false
}

// quoteIdent quotes a SQL identifier (table or column name) for safe
// interpolation. Identifiers originate from the whitelist and
// information_schema, but we still escape embedded double quotes defensively.
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// joinQuoted returns the identifiers quoted and comma-joined.
func joinQuoted(names []string) string {
	q := make([]string, len(names))
	for i, n := range names {
		q[i] = quoteIdent(n)
	}
	return strings.Join(q, ", ")
}

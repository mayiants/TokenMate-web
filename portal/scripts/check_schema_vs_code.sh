#!/bin/bash
# ============================================================
# Schema vs Code Static Analysis
# ============================================================
# Scans Go source SQL references and compares every
# (table, column) pair against the columns defined in
# internal/db/schema.go. Reports any column referenced in code
# but missing from the schema, and any table referenced in code
# that does not exist in the schema.
#
# Usage: bash scripts/check_schema_vs_code.sh
# ============================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
SCHEMA="${PROJECT_DIR}/internal/db/schema.go"
CODE_DIRS=(
  "${PROJECT_DIR}/internal/routes"
  "${PROJECT_DIR}/internal/web"
  "${PROJECT_DIR}/internal/middleware"
  "${PROJECT_DIR}/internal/services"
  "${PROJECT_DIR}/internal/db"
)

TMPDIR=$(mktemp -d)
trap "rm -rf $TMPDIR" EXIT

SCHEMA_COLS="$TMPDIR/schema_cols.txt"
CODE_COLS="$TMPDIR/code_cols.txt"
SQL_DUMP="$TMPDIR/sql.txt"

mkdir -p "$TMPDIR"

echo "=============================================="
echo " Schema vs Code — Missing Column Detection"
echo "=============================================="

# ============================================================
# Phase 1: Extract all (table, column) from schema.go
# ============================================================

# 1a. From CREATE TABLE blocks by scanning schema.go line by line. This avoids
# issues with nested parentheses inside DEFAULT/CHECK/REFERENCES clauses.
current_table=""
while IFS= read -r line || [[ -n "$line" ]]; do
  if [[ "$line" =~ CREATE[[:space:]]+TABLE[[:space:]]+IF[[:space:]]+NOT[[:space:]]+EXISTS[[:space:]]+([a-z_][a-z0-9_]*)[[:space:]]*\( ]]; then
    current_table="${BASH_REMATCH[1]}"
    continue
  fi
  if [[ -n "$current_table" && "$line" =~ ^[[:space:]]*\) ]]; then
    current_table=""
    continue
  fi
  if [[ -n "$current_table" ]]; then
    col=$(echo "$line" | sed 's/^[[:space:]]*//' | awk '{print $1}')
    case "$col" in
      ''|PRIMARY|KEY|UNIQUE|CHECK|FOREIGN|CONSTRAINT|INDEX|DEFAULT|REFERENCES|_) continue ;;
    esac
    echo "${current_table}.${col}"
  fi
done < "$SCHEMA" > "$SCHEMA_COLS"

# 1b. From ALTER TABLE ADD COLUMN statements. Multi-column blocks are scanned
# line by line so that nested newlines do not break the extraction.
alter_tbl=""
while IFS= read -r line || [[ -n "$line" ]]; do
  if [[ "$line" =~ ALTER[[:space:]]+TABLE[[:space:]]+([a-z_][a-z0-9_]*) ]]; then
    alter_tbl="${BASH_REMATCH[1]}"
  fi
  if [[ -n "$alter_tbl" && "$line" =~ ADD[[:space:]]+COLUMN[[:space:]]+IF[[:space:]]+NOT[[:space:]]+EXISTS[[:space:]]+([a-z_][a-z0-9_]*) ]]; then
    echo "${alter_tbl}.${BASH_REMATCH[1]}"
  fi
  # Raw strings in schema.go are delimited by backticks; reset when we leave one.
  if [[ -n "$alter_tbl" && "$line" == *"\`"* ]]; then
    alter_tbl=""
  fi
done < "$SCHEMA" >> "$SCHEMA_COLS"

sort -u -o "$SCHEMA_COLS" "$SCHEMA_COLS"

KNOWN_TABLES="$TMPDIR/known_tables.txt"
cut -d. -f1 "$SCHEMA_COLS" | sort -u > "$KNOWN_TABLES"

echo ""
echo "[Phase 1] Schema columns extracted: $(wc -l < "$SCHEMA_COLS") entries"

# ============================================================
# Phase 2: Extract raw SQL string literals from Go code
# ============================================================

: > "$SQL_DUMP"

# Use a small Go helper to extract string literals from the source tree. This
# is faster and more accurate than parsing Go with shell regexes, especially
# for multi-line raw string literals.
go run "${PROJECT_DIR}/scripts/extract_sql_literals.go" "${CODE_DIRS[@]}" > "$SQL_DUMP"

# Helper to emit (table, column) references
emit_ref() {
  local tbl="$1" col="$2"
  [[ -z "$tbl" || -z "$col" ]] && return
  echo "${tbl}.${col}"
}

: > "$CODE_COLS"

# 2a. INSERT INTO table (col1, col2, ...)
grep -ioP 'INSERT INTO\s+\w+\s*\([^)]+\)' "$SQL_DUMP" 2>/dev/null | while IFS= read -r stmt; do
  tbl=$(echo "$stmt" | grep -ioP 'INSERT INTO\s+\K\w+')
  cols=$(echo "$stmt" | grep -oP '\(\K[^)]+')
  echo "$cols" | tr ',' '\n' | sed 's/^[[:space:]]*//;s/[[:space:]]*$//' | while IFS= read -r col; do
    col=$(echo "$col" | grep -oP '^[a-z_][a-z0-9_]*' || true)
    case "$col" in
      true|false|NULL|NOW|CURRENT_DATE|CURRENT_TIMESTAMP|'') continue ;;
    esac
    emit_ref "$tbl" "$col"
  done
done >> "$CODE_COLS" || true

# 2b. UPDATE table SET col1 = ..., col2 = ...
grep -ioP 'UPDATE\s+\w+\s+SET\s+.+?(WHERE|RETURNING|$)' "$SQL_DUMP" 2>/dev/null | while IFS= read -r stmt; do
  tbl=$(echo "$stmt" | grep -ioP 'UPDATE\s+\K\w+')
  set_clause=$(echo "$stmt" | sed -E 's/UPDATE\s+\w+\s+SET\s+//;s/WHERE.*//i;s/RETURNING.*//i')
  echo "$set_clause" | grep -oP '\w+\s*=' | sed 's/\s*=//' | while IFS= read -r col; do
    case "$col" in
      SET|WHERE|RETURNING|true|false|TRUE|FALSE|NULL|NOW|CURRENT_DATE|CURRENT_TIMESTAMP|'') continue ;;
    esac
    emit_ref "$tbl" "$col"
  done
done >> "$CODE_COLS" || true

# 2c. SELECT col1, col2, ... FROM table
grep -ioP 'SELECT\s+.+?\s+FROM\s+\w+' "$SQL_DUMP" 2>/dev/null | while IFS= read -r stmt; do
  tbl=$(echo "$stmt" | grep -ioP 'FROM\s+\K\w+')
  select_clause=$(echo "$stmt" | sed -E 's/SELECT\s+//i;s/\s+FROM\s+\w+//i')
  # Qualified references: a.col or table.col. Only keep the reference if the
  # left-hand side is a known table name; otherwise it is an alias we cannot
  # reliably resolve and would produce false positives.
  echo "$select_clause" | grep -oP '\w+\s*\.\s*\w+' | while IFS= read -r qualified; do
    ref_tbl=$(echo "$qualified" | awk -F. '{print $1}')
    ref_col=$(echo "$qualified" | awk -F. '{print $2}')
    if grep -qx "$ref_tbl" "$KNOWN_TABLES" 2>/dev/null; then
      emit_ref "$ref_tbl" "$ref_col"
    fi
  done
  # Unqualified direct column names
  echo "$select_clause" | tr ',' '\n' | sed 's/^[[:space:]]*//;s/[[:space:]]*$//' | while IFS= read -r token; do
    col=$(echo "$token" | grep -oP '^[a-z_][a-z0-9_]*' || true)
    case "$col" in
      ''|SELECT|DISTINCT|FROM|AS|ON|WHERE|JOIN|LEFT|RIGHT|INNER|OUTER|CROSS|id|COUNT|SUM|COALESCE|MAX|MIN|AVG|ARRAY_AGG|STRING_AGG|json_agg|jsonb_agg|NOW|CURRENT_DATE|CURRENT_TIMESTAMP|true|false|TRUE|FALSE|NULL) continue ;;
    esac
    # Short tokens are almost always table aliases, not real columns.
    if [[ ${#col} -le 2 ]]; then
      continue
    fi
    emit_ref "$tbl" "$col"
  done
done >> "$CODE_COLS" || true

# 2d. WHERE / JOIN / ON table.col references. Only keep known tables to avoid
# treating string literals (e.g. email domains) or aliases as table references.
grep -oP '\w+\.\w+' "$SQL_DUMP" 2>/dev/null | while IFS= read -r qualified; do
  tbl=$(echo "$qualified" | cut -d. -f1)
  col=$(echo "$qualified" | cut -d. -f2)
  if grep -qx "$tbl" "$KNOWN_TABLES" 2>/dev/null; then
    emit_ref "$tbl" "$col"
  fi
done >> "$CODE_COLS" || true

# 2e. RETURNING col1, col2, ...
grep -ioP 'RETURNING\s+\w+(\s*,\s*\w+)*' "$SQL_DUMP" 2>/dev/null | while IFS= read -r stmt; do
  echo "$stmt" | sed -E 's/RETURNING\s+//i' | tr ',' '\n' | sed 's/^[[:space:]]*//;s/[[:space:]]*$//' | while IFS= read -r col; do
    [[ "$col" != "id" ]] && emit_ref "__returning__" "$col"
  done
done >> "$CODE_COLS" || true

sort -u -o "$CODE_COLS" "$CODE_COLS"

echo "[Phase 2] Code column references extracted: $(wc -l < "$CODE_COLS") entries"

# ============================================================
# Phase 3: Cross-reference and report
# ============================================================

echo ""
echo "--- Missing Columns ---"
echo ""

missing_count=0

while IFS= read -r entry; do
  tbl="${entry%%.*}"
  col="${entry#*.}"

  [[ "$tbl" == "__returning__" ]] && continue
  [[ "$col" =~ ^\$[0-9] ]] && continue
  [[ "$col" =~ ^%[sSdDv] ]] && continue

  # Skip columns present in most tables (too noisy for this check)
  case "$col" in
    id|created_at|updated_at|deleted_at|'*') continue ;;
  esac

  # Skip if the table is not defined in schema at all
  if ! grep -q "^${tbl}\." "$SCHEMA_COLS" 2>/dev/null; then
    continue
  fi

  if ! grep -q "^${tbl}.${col}$" "$SCHEMA_COLS" 2>/dev/null; then
    if echo "$col" | grep -qE '^[a-z][a-z0-9_]+$' && [[ ${#col} -ge 2 ]]; then
      case "$col" in
        ctx|err|row|rows|req|res|resp|gin|nil|0x|[0-9]*) continue ;;
      esac
      echo "  [MISSING] ${tbl}.${col}"
      missing_count=$((missing_count + 1))
    fi
  fi
done < "$CODE_COLS"

# ============================================================
# Phase 4: Tables referenced in code but missing from schema
# ============================================================

# CTE names (WITH ... AS / ), name AS) are legitimate FROM/JOIN targets but
# are not real tables; exclude them from the missing-table report.
CTE_NAMES="$TMPDIR/cte_names.txt"
grep -ioP '(?:WITH\s+(?:RECURSIVE\s+)?|\)\s*,\s*)\K\w+(?=\s+AS\s*\()' "$SQL_DUMP" 2>/dev/null | sort -u > "$CTE_NAMES" || true

echo ""
echo "--- Tables in Code Missing from Schema ---"
echo ""

table_missing_count=0

# Only report a missing table if the code references at least one column on it
# that looks like a real database column (snake_case). This filters out Go
# identifiers such as strconv.Itoa or email domains such as test.com.
while IFS= read -r tbl; do
  [[ "$tbl" == "__returning__" ]] && continue
  [[ ${#tbl} -lt 3 ]] && continue
  if grep -qx "$tbl" "$CTE_NAMES" 2>/dev/null; then
    continue
  fi
  if ! grep -q "^${tbl}\." "$SCHEMA_COLS" 2>/dev/null; then
    has_real_col=false
    while IFS= read -r entry; do
      col="${entry#*.}"
      if echo "$col" | grep -qE '^[a-z][a-z0-9_]+$' && [[ ${#col} -ge 2 ]]; then
        has_real_col=true
        break
      fi
    done < <(grep "^${tbl}\." "$CODE_COLS" 2>/dev/null)
    if [[ "$has_real_col" == true ]]; then
      echo "  [MISSING TABLE] $tbl"
      table_missing_count=$((table_missing_count + 1))
    fi
  fi
done < <(grep -oP '^\w+' "$CODE_COLS" | sort -u)

# ============================================================
# Phase 5: Informational — schema columns not referenced in code
# ============================================================
# This is a best-effort, NON-BLOCKING report. The Phase 2 code-reference
# extractor is deliberately conservative (it drops unresolved aliases, short
# tokens, and RETURNING targets), so a column showing up here does NOT prove
# the column is dead — it may be referenced only through an alias the static
# scanner could not resolve, via SELECT *, or from code paths outside
# CODE_DIRS. Treat it as a cleanup hint, never as a failure condition.

echo ""
echo "--- [informational] Schema Columns Not Referenced in Code ---"
echo "    (best-effort; aliases / SELECT * / RETURNING may hide real usage)"
echo ""

# Build a lookup of every column referenced in code, in table.column form,
# plus a set of bare column names seen anywhere (to suppress columns that are
# referenced but only through an alias we could not bind to their table).
CODE_BARE_COLS="$TMPDIR/code_bare_cols.txt"
cut -d. -f2- "$CODE_COLS" | sort -u > "$CODE_BARE_COLS"

unreferenced_count=0
suppressed_common=0

while IFS= read -r entry; do
  tbl="${entry%%.*}"
  col="${entry#*.}"

  # Only consider well-formed snake_case column identifiers. This drops
  # constraint fragments that leaked into Phase 1 extraction, e.g.
  # "UNIQUE(user_id," or other multi-column clause remnants.
  if ! echo "$col" | grep -qE '^[a-z][a-z0-9_]*$'; then
    continue
  fi

  # Skip ubiquitous bookkeeping columns present on almost every table.
  case "$col" in
    id|created_at|updated_at|deleted_at) suppressed_common=$((suppressed_common + 1)); continue ;;
  esac

  # Exact table.column reference in code → definitely used.
  if grep -qx "${tbl}.${col}" "$CODE_COLS" 2>/dev/null; then
    continue
  fi

  # Referenced by bare column name elsewhere (likely via an alias we could not
  # resolve to this table). Report separately-suppressed to avoid false noise.
  if grep -qx "$col" "$CODE_BARE_COLS" 2>/dev/null; then
    continue
  fi

  echo "  [unused?] ${tbl}.${col}"
  unreferenced_count=$((unreferenced_count + 1))
done < "$SCHEMA_COLS"

if [[ $unreferenced_count -eq 0 ]]; then
  echo "  (none — every schema column is referenced somewhere in code)"
fi
echo ""
echo "  Columns with no code reference: $unreferenced_count"
echo "  Common columns skipped (id/created_at/updated_at/deleted_at): $suppressed_common"

echo ""
echo "=============================================="
if [[ $missing_count -eq 0 && $table_missing_count -eq 0 ]]; then
  echo " All columns and tables accounted for."
else
  echo " Missing columns detected: $missing_count"
  echo " Missing tables detected: $table_missing_count"
fi
echo ""
echo " Schema columns:  $(wc -l < "$SCHEMA_COLS")"
echo " Code references: $(wc -l < "$CODE_COLS")"
echo " Unreferenced schema columns (informational): $unreferenced_count"
echo "=============================================="

if [[ $missing_count -gt 0 || $table_missing_count -gt 0 ]]; then
  exit 1
fi

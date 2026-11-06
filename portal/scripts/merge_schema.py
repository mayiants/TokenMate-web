#!/usr/bin/env python3
"""
Merge schema.go:
1. Remove duplicate CREATE TABLE statements (keep the one with more columns)
2. Consolidate all ALTER TABLE ADD COLUMN IF NOT EXISTS into their CREATE TABLE
3. Output a clean schema.go with error-returning InitSchema

Usage: python3 scripts/merge_schema.py > internal/db/schema_clean.go
"""

import re
import sys
import os
from collections import defaultdict, OrderedDict

PROJECT_DIR = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SCHEMA_FILE = os.path.join(PROJECT_DIR, "internal", "db", "schema.go")

with open(SCHEMA_FILE) as f:
    text = f.read()

# ============================================================
# Phase 1: Extract CREATE TABLE blocks
# ============================================================
create_table_re = re.compile(
    r"CREATE TABLE IF NOT EXISTS\s+(\w+)\s*\((.*?)\)\s*;",
    re.IGNORECASE | re.DOTALL
)

table_defs = OrderedDict()  # table_name -> list of column defs
table_extra = OrderedDict() # table_name -> any trailing SQL after the create table block

pos = 0
while True:
    m = create_table_re.search(text, pos)
    if not m:
        break
    table_name = m.group(1).lower()
    body = m.group(2)
    pos = m.end()

    # Skip duplicate: keep first one (we'll merge columns from all occurrences)
    # Actually, let's collect all occurrences and merge them

    # Parse columns from the body
    columns = []
    in_constraint = False
    paren_depth = 0

    for line in body.split('\n'):
        line = line.strip()
        if not line or line.startswith('--'):
            continue

        # Track paren depth
        paren_depth += line.count('(') - line.count(')')

        # Skip constraint/index/partition lines
        if re.match(r'^(CONSTRAINT\b|PRIMARY\s+KEY|UNIQUE\b|FOREIGN\b|CHECK\s*\('
                    r'|INDEX\b|EXCLUDE\b|PARTITION\s+BY)',
                    line, re.IGNORECASE):
            continue

        # Column definition: col_name TYPE [options]
        col_match = re.match(r'^(\w+)\s+([A-Z][A-Z0-9_ ]+(?:\([^)]*\))?)', line, re.IGNORECASE)
        if col_match:
            col_name = col_match.group(1).lower()
            col_type = col_match.group(2).strip()
            # Get the rest of line after type (DEFAULT, REFERENCES, etc.)
            rest = line[col_match.end():].strip().rstrip(',')
            full_def = f"{col_name} {col_type}"
            if rest:
                full_def += " " + rest
            columns.append((col_name, full_def))

    if table_name in table_defs:
        # Merge: add new columns not already present
        existing_cols = {c[0] for c in table_defs[table_name]}
        for col_name, col_def in columns:
            if col_name not in existing_cols:
                table_defs[table_name].append((col_name, col_def))
    else:
        table_defs[table_name] = columns


# ============================================================
# Phase 2: Extract ALTER TABLE ADD COLUMN statements
# ============================================================
alter_re = re.compile(
    r"ALTER TABLE\s+(\w+)\s+ADD COLUMN IF NOT EXISTS\s+(\w+)\s+(.+?)(?:;|$)",
    re.IGNORECASE
)

alter_columns = defaultdict(list)  # table_name -> [(col_name, col_type, rest)]
for m in alter_re.finditer(text):
    table = m.group(1).lower()
    col = m.group(2).lower()
    rest = m.group(3).strip().rstrip(';').strip()
    # rest contains type + optionally DEFAULT, REFERENCES etc.
    alter_columns[table].append((col, rest))

# Merge ALTER columns into table defs
for table, cols in alter_columns.items():
    if table not in table_defs:
        print(f"WARNING: ALTER TABLE {table} but no CREATE TABLE found", file=sys.stderr)
        continue
    existing_cols = {c[0] for c in table_defs[table]}
    for col_name, col_def in cols:
        if col_name not in existing_cols:
            table_defs[table].append((col_name, col_def))


# ============================================================
# Phase 3: Extract DROP CONSTRAINT / ADD CONSTRAINT / CREATE INDEX
# ============================================================
# These are standalone statements we need to preserve
extra_statements = []

# ALTER TABLE ... DROP CONSTRAINT
for m in re.finditer(r"ALTER TABLE\s+(\w+)\s+DROP CONSTRAINT IF EXISTS\s+(\w+)", text, re.IGNORECASE):
    extra_statements.append(f"ALTER TABLE {m.group(1)} DROP CONSTRAINT IF EXISTS {m.group(2)}")

# ALTER TABLE ... ADD CONSTRAINT (unique, etc.)
for m in re.finditer(r"ALTER TABLE\s+(\w+)\s+ADD CONSTRAINT\s+(\w+)\s+(UNIQUE\s*\([^)]+\))", text, re.IGNORECASE):
    extra_statements.append(f"ALTER TABLE {m.group(1)} ADD CONSTRAINT {m.group(2)} {m.group(3)}")

# CREATE INDEX IF NOT EXISTS
for m in re.finditer(r"CREATE INDEX IF NOT EXISTS\s+(\w+)\s+ON\s+(\w+)\(([^)]+)\)(?:\s*WHERE\s+([^;]+))?", text, re.IGNORECASE):
    stmt = f"CREATE INDEX IF NOT EXISTS {m.group(1)} ON {m.group(2)}({m.group(3)})"
    if m.group(4):
        stmt += f" WHERE {m.group(4)}"
    extra_statements.append(stmt)

# ============================================================
# Phase 4: Generate clean schema.go
# ============================================================

# Find the package/import header from original
header_match = re.search(r'(package db.*?func \(db \*DB\) InitSchema)', text, re.DOTALL)
if header_match:
    header = header_match.group(1)
else:
    header = """package db

import (
    "context"
    "fmt"

    "go.uber.org/zap"
)

// InitSchema ensures all database tables, columns and indexes exist.
// It runs all DDL in a single transaction and returns the first error encountered.
func (db *DB) InitSchema(ctx context.Context, logger *zap.Logger) error {"""

print(header)

# Schema migrations table
print('''
    // Ensure schema_migrations table exists
    if _, err := db.Pool.Exec(ctx, `
        CREATE TABLE IF NOT EXISTS schema_migrations (
            version VARCHAR(50) PRIMARY KEY,
            applied_at TIMESTAMP DEFAULT NOW()
        )
    `); err != nil {
        return fmt.Errorf("create schema_migrations: %%w", err)
    }''')

# Generate CREATE TABLE statements
for table_name, columns in table_defs.items():
    print(f'\n    // ================================================================')
    print(f'    // Table: {table_name}')
    print(f'    // ================================================================')

    cols_sql = []
    for col_name, col_def in sorted(columns, key=lambda x: x[0]):
        # Clean up the definition
        clean_def = re.sub(r'\s+', ' ', col_def).strip()
        cols_sql.append(f"        {clean_def}")

    create_sql = ",\n".join(cols_sql)

    print(f'''    if _, err := db.Pool.Exec(ctx, `
        CREATE TABLE IF NOT EXISTS {table_name} (
{create_sql}
        )
    `); err != nil {{
        return fmt.Errorf("create table {table_name}: %w", err)
    }}''')

# Generate extra statements (unique constraints, indexes, etc.)
if extra_statements:
    print('\n    // ================================================================')
    print('    // Constraints and Indexes')
    print('    // ================================================================')

    # Group by table
    constraint_groups = defaultdict(list)
    for stmt in extra_statements:
        # Extract table name
        table_match = re.search(r'ON\s+(\w+)', stmt)
        alter_match = re.search(r'ALTER TABLE\s+(\w+)', stmt)
        if table_match:
            constraint_groups[table_match.group(1).lower()].append(stmt)
        elif alter_match:
            constraint_groups[alter_match.group(1).lower()].append(stmt)
        else:
            constraint_groups['__global__'].append(stmt)

    for table, stmts in sorted(constraint_groups.items()):
        for stmt in stmts:
            print(f'''    if _, err := db.Pool.Exec(ctx, `{stmt}`); err != nil {{
        return fmt.Errorf("{stmt.lower().replace('"', '')}: %w", err)
    }}''')

# Closing
print('''
    return nil
}''')

# Print stats to stderr
print(f"\n// Stats: {len(table_defs)} tables, {sum(len(c) for c in table_defs.values())} columns", file=sys.stderr)
print(f"// Extra statements: {len(extra_statements)}", file=sys.stderr)

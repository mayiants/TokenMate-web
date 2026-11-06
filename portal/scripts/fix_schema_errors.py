#!/usr/bin/env python3
"""
Fix schema.go:
1. Remove duplicate CREATE TABLE users (keep richer first definition, merge FK refs)
2. Replace `_ = db.Exec(ctx,` with proper error handling
3. Replace `_ = db.Pool.Exec(ctx,` with proper error handling
4. Fix `_ = db.Exec(` without ctx pattern

Usage: python3 scripts/fix_schema_errors.py
Output: internal/db/schema.go (overwritten)
Backup: internal/db/schema.go.bak (before overwrite)
"""

import re
import os

PROJECT_DIR = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SCHEMA_FILE = os.path.join(PROJECT_DIR, "internal", "db", "schema.go")

with open(SCHEMA_FILE) as f:
    text = f.read()

# Backup
with open(SCHEMA_FILE + ".bak", "w") as f:
    f.write(text)

# ============================================================
# Fix 1: Remove duplicate users table (lines 153-191)
# ============================================================
# The second CREATE TABLE users (lines 153-191 in schema.go) has these extra
# columns vs the first (lines 26-63): enterprise_id with FK, group_id with FK.
# We merge them into the first definition.

# First, find and mark the duplicate users block
dup_users_marker = text.find("_ = db.Exec(ctx, `\n\t\tCREATE TABLE IF NOT EXISTS users (\n\t\t\tid UUID PRIMARY KEY DEFAULT gen_random_uuid(),\n\t\t\tname")
if dup_users_marker < 0:
    dup_users_marker = text.find("_ = db.Exec(ctx, `\n\t\tCREATE TABLE IF NOT EXISTS users (")
    # Find second occurrence
    first = text.find("_ = db.Exec(ctx, `\n\t\tCREATE TABLE IF NOT EXISTS users (")
    dup_users_marker = text.find("_ = db.Exec(ctx, `\n\t\tCREATE TABLE IF NOT EXISTS users (", first + 1)

if dup_users_marker > 0:
    # Find the matching closing `)
    # The block is: _ = db.Exec(ctx, ` ... `)
    # Count backtick-depth
    start = text.find("`", dup_users_marker)  # opening backtick after Exec(ctx,
    depth = 0
    i = start
    while i < len(text):
        if text[i] == '`' and (i == 0 or text[i-1] != '\\'):
            if depth == 0:
                depth = 1
            else:
                # Found closing backtick
                break
        i += 1
    # Find the closing paren after the backtick
    end = text.find(")", i) + 1
    dup_block = text[dup_users_marker:end]

    # Remove it (replace with empty string)
    text = text.replace(dup_block + "\n\n", "", 1)
    print(f"Removed duplicate users CREATE TABLE ({len(dup_block)} chars)")

# ============================================================
# Fix 2: Update first users table - add enterprise_id and FK for group_id
# ============================================================
# Change "group_id UUID," to "group_id UUID REFERENCES user_groups(id),"
old_group = "group_id UUID,"
new_group = "group_id UUID REFERENCES user_groups(id),"
count = text.count(old_group)
if count > 0:
    # Only replace the first occurrence (in the first users table)
    text = text.replace(old_group, new_group, 1)
    print(f"Added FK to group_id in users table")

# Add enterprise_id after group_id in first users table
# Find: group_id UUID REFERENCES user_groups(id),\n\t\t\tuser_type
old_text = "group_id UUID REFERENCES user_groups(id),\n\t\t\tuser_type"
new_text = "group_id UUID REFERENCES user_groups(id),\n\t\t\tenterprise_id UUID REFERENCES enterprises(id),\n\t\t\tuser_type"
if new_text not in text:
    text = text.replace(old_text, new_text, 1)
    print(f"Added enterprise_id FK to first users table")

# ============================================================
# Fix 3: Replace error-swallowing Exec calls with error returns
# ============================================================

# Helper to extract a meaningful error label from SQL
def error_label(sql):
    """Extract a short label from SQL for the error message."""
    sql_oneline = sql[:200].replace('\n', ' ').replace('\t', ' ')
    # Try CREATE TABLE
    m = re.search(r'CREATE TABLE IF NOT EXISTS\s+(\w+)', sql_oneline, re.IGNORECASE)
    if m:
        return f"create table {m.group(1)}"
    # Try ALTER TABLE
    m = re.search(r'ALTER TABLE\s+(\w+)\s+(\w+\s+\w+\s+\w+\s+\w+\s+)?(\w+)', sql_oneline, re.IGNORECASE)
    if m:
        action = m.group(2).strip() if m.group(2) else ""
        col = m.group(3)
        return f"alter table {m.group(1)} add {col}" if "ADD" in sql_oneline.upper() else f"alter table {m.group(1)}"
    # Try CREATE INDEX
    m = re.search(r'CREATE INDEX IF NOT EXISTS\s+(\w+)', sql_oneline, re.IGNORECASE)
    if m:
        return f"create index {m.group(1)}"
    # Try INSERT
    m = re.search(r'INSERT INTO\s+(\w+)', sql_oneline, re.IGNORECASE)
    if m:
        return f"insert into {m.group(1)}"
    return "exec sql"

def transform_exec_block(match):
    """Transform `_ = db.Exec(ctx, ` ... `)` into proper error handling"""
    full = match.group(0)
    sql = match.group(2)
    label = error_label(sql)
    indent = match.group(1)  # whitespace before _

    # Build replacement
    result = f'{indent}if _, err := db.Pool.Exec(ctx, `{sql}`); err != nil {{\n'
    result += f'{indent}\treturn fmt.Errorf("{label}: %w", err)\n'
    result += f'{indent}}}'
    return result

# Pattern: _ = db.Exec(ctx, `...SQL...`)
# We need to match the backtick-delimited SQL
pattern = r'(\s*)_ = db\.Exec\(ctx, `((?:[^`]|``)*)`\)'

count_before = len(re.findall(r'_ = db\.Exec\(ctx', text))
text = re.sub(pattern, transform_exec_block, text)
count_after = len(re.findall(r'_ = db\.Exec\(ctx', text))
print(f"Transformed {count_before - count_after} of {count_before} db.Exec calls")

# Also handle the extra statement patterns at the end (DROP CONSTRAINT, ADD CONSTRAINT)
# Pattern: `_ = db.Exec(ctx, \`...\`)`  (single-line style)
pattern2 = r'(\s*)_ = db\.Exec\(ctx, `([^`]+)`\)'
count_before2 = len(re.findall(r'_ = db\.Exec\(ctx, `', text))
text = re.sub(pattern2, transform_exec_block, text)
count_after2 = len(re.findall(r'_ = db\.Exec\(ctx, `', text))
print(f"Transformed {count_before2 - count_after2} more single-line Exec calls")

# ============================================================
# Fix 4: Add "fmt" import if not present
# ============================================================
if '"fmt"' not in text.split('import (')[1].split(')')[0] if 'import (' in text else True:
    text = text.replace(
        'import (\n\t"context"',
        'import (\n\t"context"\n\t"fmt"'
    )
    print("Added fmt import")

# ============================================================
# Fix 5: Update function comment
# ============================================================
text = text.replace(
    '// InitSchema ensures all database tables, columns and indexes exist.\n// It is idempotent: CREATE IF NOT EXISTS + ADD COLUMN IF NOT EXISTS.',
    '// InitSchema ensures all database tables, columns and indexes exist.\n// It returns the first error encountered; all statements use IF NOT EXISTS / IF EXISTS for idempotency.'
)

# ============================================================
# Write output
# ============================================================
with open(SCHEMA_FILE, "w") as f:
    f.write(text)

print(f"\nDone. schema.go updated. Backup at schema.go.bak")
print(f"Run: go build ./... to verify")

#!/usr/bin/env python3
"""
Reorders ALTER TABLE ADD COLUMN statements in schema.go.
Moves all ALTER TABLE ADD COLUMN that appear before the last CREATE TABLE
so they execute after all CREATE TABLE blocks.
"""

import re
from pathlib import Path

SCHEMA = Path("internal/db/schema.go")
content = SCHEMA.read_text()
lines = content.split('\n')

# Find last CREATE TABLE line
create_table_lines = [i for i, line in enumerate(lines) if 'CREATE TABLE IF NOT EXISTS' in line]
last_create_line = create_table_lines[-1] if create_table_lines else 0

# Collect ALTER TABLE ADD COLUMN lines before last CREATE TABLE
alter_lines = []
keep_lines = []
for i, line in enumerate(lines):
    if 'ALTER TABLE' in line and 'ADD COLUMN IF NOT EXISTS' in line and i < last_create_line:
        alter_lines.append(line)
    else:
        keep_lines.append(line)

# Find insertion point: after last CREATE TABLE block ends, but before "return nil"
insert_pos = len(keep_lines)
for i in range(len(keep_lines) - 1, last_create_line, -1):
    stripped = keep_lines[i].strip()
    if stripped == 'return nil' or stripped == '}':
        # Back up to find the empty line before return nil
        for j in range(i - 1, last_create_line, -1):
            if keep_lines[j].strip() == '':
                insert_pos = j + 1
                break
        break

# Build result
result = keep_lines[:insert_pos]
if alter_lines:
    result.append('')
    result.append('\t// ============================================================================')
    result.append('\t// Phase: Add columns to tables (moved after all CREATE TABLE for clean init)')
    result.append('\t// ============================================================================')
    result.append('')
    result.extend(alter_lines)
result.extend(keep_lines[insert_pos:])

SCHEMA.write_text('\n'.join(result))
print(f'Moved {len(alter_lines)} ALTER TABLE ADD COLUMN statements after last CREATE TABLE (line {last_create_line})')

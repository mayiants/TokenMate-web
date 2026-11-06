#!/usr/bin/env python3
"""
Check ON CONFLICT constraints against schema.go
Scans all Go SQL code for ON CONFLICT (...) clauses and verifies
the corresponding UNIQUE / PRIMARY KEY constraints exist in schema.go.

Usage: python3 scripts/check_on_conflict_constraints.py
"""

import re
import os
import sys
from pathlib import Path

PROJECT_DIR = Path(__file__).parent.parent
SCHEMA_FILE = PROJECT_DIR / "internal" / "db" / "schema.go"
CODE_DIRS = [
    PROJECT_DIR / "internal" / "routes",
    PROJECT_DIR / "internal" / "web",
    PROJECT_DIR / "internal" / "middleware",
    PROJECT_DIR / "internal" / "services",
    PROJECT_DIR / "internal" / "db",
]


def extract_create_table_blocks(schema_text):
    """Extract all CREATE TABLE blocks with proper parenthesis nesting."""
    pattern = re.compile(r'CREATE TABLE IF NOT EXISTS\s+(\w+)\s*\(', re.IGNORECASE)
    blocks = []
    for m in pattern.finditer(schema_text):
        table = m.group(1)
        start = m.end() - 1  # position of '('
        depth = 0
        i = start
        while i < len(schema_text):
            if schema_text[i] == '(':
                depth += 1
            elif schema_text[i] == ')':
                depth -= 1
                if depth == 0:
                    body = schema_text[start + 1:i]
                    blocks.append((table, body))
                    break
            i += 1
    return blocks


def extract_schema_constraints(schema_text):
    """Extract all (table, constraint_columns) from schema.go."""
    constraints = {}  # table -> list of frozenset(columns)

    # 1. PRIMARY KEY / UNIQUE in CREATE TABLE blocks
    for table, body in extract_create_table_blocks(schema_text):
        if table not in constraints:
            constraints[table] = []

        # Find PRIMARY KEY(col1, col2, ...) - table-level
        for pk in re.finditer(
            r'PRIMARY\s+KEY\s*\(([^)]+)\)',
            body, re.IGNORECASE
        ):
            cols = frozenset(c.strip() for c in pk.group(1).split(','))
            constraints[table].append(cols)

        # Find col TYPE PRIMARY KEY - inline single-column PK
        for pk in re.finditer(
            r'^\s*(\w+)\s+\w+(?:\([^)]*\))?\s+PRIMARY\s+KEY\b',
            body, re.IGNORECASE | re.MULTILINE
        ):
            cols = frozenset({pk.group(1).strip()})
            constraints[table].append(cols)

        # Find UNIQUE(col1, col2, ...) - table-level
        for uq in re.finditer(
            r'UNIQUE\s*\(([^)]+)\)',
            body, re.IGNORECASE
        ):
            cols = frozenset(c.strip() for c in uq.group(1).split(','))
            constraints[table].append(cols)

        # Find col TYPE UNIQUE - inline single-column UNIQUE
        for uq in re.finditer(
            r'^\s*(\w+)\s+\w+(?:\([^)]*\))?\s+UNIQUE\b',
            body, re.IGNORECASE | re.MULTILINE
        ):
            cols = frozenset({uq.group(1).strip()})
            constraints[table].append(cols)

    # 2. ALTER TABLE ADD CONSTRAINT ... UNIQUE (col1, col2)
    for m in re.finditer(
        r'ALTER TABLE\s+(\w+)\s+ADD CONSTRAINT\s+\w+\s+UNIQUE\s*\(([^)]+)\)',
        schema_text, re.IGNORECASE
    ):
        table = m.group(1)
        cols = frozenset(c.strip() for c in m.group(2).split(','))
        if table not in constraints:
            constraints[table] = []
        constraints[table].append(cols)

    # 3. CREATE UNIQUE INDEX [IF NOT EXISTS] name ON table (col1, col2) [WHERE ...]
    # Partial unique indexes (with a WHERE clause) are valid ON CONFLICT targets
    # too (via the WHERE arbiter), so recognize them so the check does not emit
    # false positives for dedup indexes defined this way.
    for m in re.finditer(
        r'CREATE\s+UNIQUE\s+INDEX\s+(?:IF\s+NOT\s+EXISTS\s+)?\w+\s+ON\s+(\w+)\s*\(([^)]+)\)',
        schema_text, re.IGNORECASE
    ):
        table = m.group(1)
        cols = frozenset(c.strip() for c in m.group(2).split(','))
        if table not in constraints:
            constraints[table] = []
        constraints[table].append(cols)

    return constraints


def extract_on_conflict_clauses(code_text):
    """Extract (table, conflict_columns) from ON CONFLICT clauses."""
    results = []

    # Process each backtick string separately to avoid cross-string matches
    for backtick_match in re.finditer(r'`([^`]*)`', code_text, re.DOTALL):
        sql = backtick_match.group(1)
        # Normalize whitespace
        sql = re.sub(r'\s+', ' ', sql)

        # Find all INSERT INTO table blocks that contain ON CONFLICT within this string
        for m in re.finditer(
            r'INSERT INTO\s+(\w+).*?ON CONFLICT\s*\(([^)]*)\)',
            sql, re.IGNORECASE
        ):
            table = m.group(1)
            cols_str = m.group(2).strip()
            if cols_str:
                cols = frozenset(c.strip() for c in cols_str.split(','))
            else:
                cols = frozenset()  # ON CONFLICT DO NOTHING without columns
            results.append((table, cols))

    return results


def main():
    schema_text = SCHEMA_FILE.read_text()
    schema_constraints = extract_schema_constraints(schema_text)

    all_code = ""
    for dir_path in CODE_DIRS:
        if dir_path.exists():
            for f in dir_path.glob("*.go"):
                all_code += f.read_text() + "\n"

    on_conflicts = extract_on_conflict_clauses(all_code)

    print("=" * 60)
    print(" ON CONFLICT Constraint Validation")
    print("=" * 60)
    print(f"\nSchema tables with constraints: {len(schema_constraints)}")
    print(f"ON CONFLICT clauses found: {len(on_conflicts)}\n")

    missing = []
    checked = set()

    for table, cols in on_conflicts:
        key = (table, tuple(sorted(cols)))
        if key in checked:
            continue
        checked.add(key)

        if table not in schema_constraints:
            # Table might not be in schema at all (handled by other checks)
            continue

        if not cols:
            # ON CONFLICT DO NOTHING without columns - needs ANY unique constraint
            if not schema_constraints[table]:
                missing.append((table, cols, "ANY unique/primary key"))
            continue

        # Check if any constraint matches the conflict columns
        found = False
        for constraint_cols in schema_constraints[table]:
            if cols == constraint_cols:
                found = True
                break

        if not found:
            missing.append((table, cols, ", ".join(sorted(cols))))

    if missing:
        print("-" * 60)
        print(" MISSING CONSTRAINTS (ON CONFLICT but no matching UNIQUE/PK)")
        print("-" * 60)
        for table, cols, desc in missing:
            print(f"  [MISSING] {table}: UNIQUE({desc})")
        print(f"\n  Total missing: {len(missing)}")
        print("-" * 60)
        sys.exit(1)
    else:
        print("-" * 60)
        print("  All ON CONFLICT clauses have matching constraints.")
        print("-" * 60)
        sys.exit(0)


if __name__ == "__main__":
    main()

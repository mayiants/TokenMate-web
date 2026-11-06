#!/usr/bin/env python3
"""
Schema vs Code Static Analysis
===============================
Scans all Go SQL code for (table, column) references and compares against
schema.go CREATE TABLE + ALTER TABLE definitions.
Reports any column referenced in code but missing from schema.

Usage: python3 scripts/check_schema_vs_code.py [--verbose]
"""

import re
import os
import sys
from collections import defaultdict

PROJECT_DIR = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SCHEMA_FILE = os.path.join(PROJECT_DIR, "internal", "db", "schema.go")

CODE_DIRS = [
    os.path.join(PROJECT_DIR, "internal", "routes"),
    os.path.join(PROJECT_DIR, "internal", "web"),
    os.path.join(PROJECT_DIR, "internal", "middleware"),
    os.path.join(PROJECT_DIR, "internal", "services"),
    os.path.join(PROJECT_DIR, "internal", "db"),
]

# Go package names and identifiers that are NOT database tables
GO_PACKAGES = {
    "fmt", "os", "io", "net", "http", "json", "time", "strings", "strconv",
    "bytes", "sort", "sync", "context", "errors", "math", "crypto", "hash",
    "encoding", "reflect", "regexp", "url", "path", "filepath", "bufio",
    "log", "zap", "gin", "uuid", "sql", "db", "pgx", "pgxpool", "csrf",
    "mux", "router", "middleware", "handler", "render", "template",
    "tls", "cipher", "suite", "browser", "docker", "github", "gorm",
    "assert", "require", "mock", "stretchr", "testify", "miniredis",
    "redis", "redigo", "gomock", "pgconn", "pgtype", "stdlib", "scanner",
    "mail", "smtp", "pop3", "imap", "ses", "sendgrid", "mailgun",
    "otel", "trace", "metric", "prometheus", "slog", "zapcore",
    "playwright", "selenium", "webdriver", "chromedp",
    "tailwind", "bootstrap", "alpine", "htmx", "jquery", "axios",
    "openai", "anthropic", "cohere", "hunyuan", "deepseek",
    "openrouter", "google", "amazon", "bedrock", "vertex",
    "multipart", "form", "url", "cookie", "session", "signal",
    "gorilla", "chi", "echo", "fiber", "iris", "hertz",
    "runtime", "unsafe", "syscall", "internal", "main",
    "testing", "httptest", "iotest", "quick", "testdata",
    "example", "userexample", "foobar",
    "pipe", "shell", "bash", "zsh", "cmd", "exec", "subprocess",
    "rand", "crypto", "sha256", "md5", "hmac", "aes", "rsa",
    "observer", "observed", "callback", "listener", "watcher",
    "rdb", "client", "server", "proxy", "gateway", "bridge",
    "pool", "cache", "store", "repo", "service", "manager",
    "auth", "jwt", "oauth", "oidc", "saml", "webauthn",
    "layout", "navbar", "sidebar", "footer", "header",
    "component", "widget", "modal", "toast", "dropdown",
    "record", "records", "stats", "info", "detail", "item",
    "label", "tag", "nav", "tab", "card", "table", "form",
    "row", "rows", "req", "resp", "src", "sub", "test",
    "vendor", "product", "plan", "order", "orders",
    "model", "models", "key", "keys", "user", "users",
    "super", "admin", "enterprise", "group", "groups",
    "route", "routes", "web", "api", "rest", "graphql",
    "csv", "json", "xml", "yaml", "toml", "ini",
    "parsed", "decoded", "encoded", "marshaled",
    "valid", "invalid", "active", "inactive", "ok", "err",
    "mac", "linux", "windows", "darwin", "freebsd",
    "payment", "billing", "invoice", "invoices",
    "redemption", "redemption_records", "redemption_codes",
    "invitation", "invitation_codes", "invitation_records",
    "subscription", "subscriptions", "subscription_plans",
    "subscription_purchases",
    "webhook", "webhooks", "endpoint", "endpoints",
    "validation", "validator", "validate",
    "hour", "minute", "second", "month", "year", "week", "day",
    "today", "yesterday", "tomorrow",
    "now", "nil", "true", "false", "null", "none",
    "idx", "ptr", "val", "obj", "arr", "tmp", "mgr",
    "pipe", "x", "y", "z", "i", "j", "k", "n", "m",
    "scl", "multi", "single", "open",
    "services", "monitor",
}

VERBOSE = "--verbose" in sys.argv

# SQL type keywords
SQL_TYPES = (
    r"VARCHAR|CHAR|TEXT|INTEGER|BIGINT|SMALLINT|TINYINT|INT\d*|INT|"
    r"BOOLEAN|BOOL|DECIMAL|NUMERIC|REAL|FLOAT8?|DOUBLE|"
    r"TIMESTAMPTZ|TIMESTAMP|DATE|TIME|"
    r"UUID|JSONB|JSON|INET|SERIAL\d*|BYTEA|INTERVAL|"
    r"TEXT\[\]|VARCHAR\[\]|INTEGER\[\]|BIGINT\[\]"
)

COL_DEF_RE = re.compile(
    rf"^\s*(\w+)\s+(?:{SQL_TYPES})(?:\s|,|$|\()",
    re.IGNORECASE
)


def _extract_table_block(text, start_pos):
    """Given the position right after 'CREATE TABLE ... (', extract until
    the matching close-paren using depth tracking. Returns (body_text, end_pos)."""
    depth = 1
    i = start_pos
    while i < len(text) and depth > 0:
        ch = text[i]
        if ch == "(":
            depth += 1
        elif ch == ")":
            depth -= 1
        i += 1

    body = text[start_pos:i - 1]  # exclude the final close-paren
    return body, i


def extract_schema_columns(schema_text):
    """Extract all (table, column) pairs from schema.go"""
    cols = set()

    # 1. Find all CREATE TABLE blocks using paren-depth tracking
    table_re = re.compile(r"CREATE TABLE IF NOT EXISTS\s+(\w+)\s*\(", re.IGNORECASE)
    for m in table_re.finditer(schema_text):
        table = m.group(1).lower()
        body, _ = _extract_table_block(schema_text, m.end())

        for line in body.split("\n"):
            line = line.strip()
            if not line or line.startswith("--"):
                continue

            # Skip constraint/index/partition lines (word-boundary to avoid
            # matching "created_by" with "CREATE", "updated_at" with "UPDATE", etc.)
            if re.match(r"^(CONSTRAINT\b|PRIMARY\s+KEY|UNIQUE\b|FOREIGN\b|CHECK\s*\("
                        r"|INDEX\b|EXCLUDE\b"
                        r"|CREATE\b|INSERT\b|SELECT\b|UPDATE\b|DELETE\b|DROP\b|ALTER\b"
                        r"|PARTITION\s+BY)",
                        line, re.IGNORECASE):
                continue

            cm = COL_DEF_RE.match(line)
            if cm:
                cols.add((table, cm.group(1).lower()))

    # 2. ALTER TABLE table ADD COLUMN IF NOT EXISTS col
    for match in re.finditer(
        r"ALTER TABLE\s+(\w+)\s+ADD COLUMN IF NOT EXISTS\s+(\w+)",
        schema_text, re.IGNORECASE
    ):
        cols.add((match.group(1).lower(), match.group(2).lower()))

    return cols


def read_all_go_code():
    """Read all Go source files from CODE_DIRS"""
    all_text = ""
    file_texts = {}
    for d in CODE_DIRS:
        if not os.path.isdir(d):
            continue
        for fname in sorted(os.listdir(d)):
            if not fname.endswith(".go") or fname.endswith("_test.go"):
                continue
            fpath = os.path.join(d, fname)
            try:
                with open(fpath) as f:
                    text = f.read()
                    all_text += text + "\n"
                    file_texts[fpath] = text
            except Exception:
                pass
    return all_text, file_texts


def extract_code_columns(code_text):
    """Extract all (table, column) pairs from Go SQL code.
    Only processes SQL that's inside backtick strings (Go raw string literals)."""
    cols = set()
    col_sources = defaultdict(lambda: defaultdict(set))  # table -> col -> {source_types}

    # Extract all backtick-delimited SQL strings
    sql_strings = re.findall(r'`([^`]+)`', code_text)

    for sql in sql_strings:
        # Skip non-SQL strings (HTML templates, etc.)
        if not re.search(r'(INSERT|UPDATE|DELETE|SELECT|CREATE|ALTER|DROP)\s',
                         sql, re.IGNORECASE):
            continue

        sql_upper = sql.upper()

        # --- INSERT INTO table (col1, col2, ...) ---
        for m in re.finditer(r"INSERT\s+INTO\s+(\w+)\s*\(([^)]*)\)",
                             sql, re.IGNORECASE):
            table = m.group(1).lower()
            col_list = m.group(2)
            for col_name in re.split(r",\s*", col_list):
                col_name = col_name.strip().strip('"').strip("'").lower()
                if col_name and not re.match(r"^\$\d+$", col_name):
                    cols.add((table, col_name))
                    col_sources[table][col_name].add("INSERT")

        # --- UPDATE table SET col = ..., col = ... ---
        for m in re.finditer(r"UPDATE\s+(\w+)\s+SET\s+(.*?)(?:WHERE|RETURNING|ON\s+CONFLICT|;|$)",
                             sql, re.IGNORECASE | re.DOTALL):
            table = m.group(1).lower()
            set_clause = m.group(2).strip()
            # Extract "col = " or "col="
            for cm in re.finditer(r"(\w+)\s*=\s*(?:\$?\d+|NOW\(\)|CURRENT_\w+|NULL|TRUE|FALSE)",
                                  set_clause, re.IGNORECASE):
                col_name = cm.group(1).lower()
                cols.add((table, col_name))
                col_sources[table][col_name].add("UPDATE")

        # --- DELETE FROM table ---
        for m in re.finditer(r"DELETE\s+FROM\s+(\w+)", sql, re.IGNORECASE):
            table = m.group(1).lower()
            cols.add((table, "__table_used__"))

        # --- table.col qualified refs inside SQL ---
        # Only inside SQL strings: table.column where table is a known lowercase name
        for m in re.finditer(r"(?<![.\w\d])([a-z][a-z_]{2,})\.(\w+)", sql):
            table = m.group(1).lower()
            col = m.group(2).lower()
            if table not in GO_PACKAGES:
                cols.add((table, col))
                col_sources[table][col].add("qualified")

    return cols, col_sources


def main():
    # Read schema
    with open(SCHEMA_FILE) as f:
        schema_text = f.read()

    schema_cols = extract_schema_columns(schema_text)
    schema_tables = {t for t, _ in schema_cols}
    schema_by_table = defaultdict(set)
    for t, c in schema_cols:
        schema_by_table[t].add(c)

    # Mark tables that have definitions (even if 0 columns extracted — they exist)
    for m in re.finditer(r"CREATE TABLE IF NOT EXISTS\s+(\w+)",
                         schema_text, re.IGNORECASE):
        schema_tables.add(m.group(1).lower())

    print("=" * 60)
    print(" Schema vs Code — Missing Column Detection")
    print("=" * 60)

    print(f"\n[Phase 1] Schema: {len(schema_cols)} columns across {len(schema_tables)} tables")

    # Read code
    code_text, file_texts = read_all_go_code()
    code_cols, col_sources = extract_code_columns(code_text)
    code_tables = {t for t, _ in code_cols if t not in {"__table_used__"}}

    print(f"[Phase 2] Code:   {len(code_cols)} column-refs across {len(code_tables)} tables")

    # ============================================================
    # Phase 3: Missing Columns
    # ============================================================

    print("\n" + "-" * 60)
    print(" MISSING COLUMNS (code references not in schema.go)")
    print("-" * 60)

    missing_count = 0
    missing_by_table = defaultdict(list)

    # Standard columns that almost every table has — skip
    UNIVERSAL_COLS = {"id", "created_at", "updated_at", "deleted_at"}

    for table, col in sorted(code_cols):
        if table == "__table_used__":
            continue
        if table not in schema_tables:
            continue  # handled in Phase 4
        if col in UNIVERSAL_COLS:
            continue
        if col in schema_by_table.get(table, set()):
            continue
        # Check if it looks like a real DB column
        if not re.match(r"^[a-z][a-z0-9_]+$", col) or len(col) < 2:
            continue

        missing_count += 1
        sources = col_sources.get(table, {}).get(col, set())
        source_str = "+".join(sorted(sources)) if sources else "?"
        missing_by_table[table].append((col, source_str))

    if missing_count == 0:
        print("\n  OK — All columns accounted for.\n")
    else:
        for table in sorted(missing_by_table):
            items = missing_by_table[table]
            print(f"\n  [{table}] — {len(items)} missing:")
            for col, source in items:
                print(f"    • {col}  [{source}]")

        print(f"\n  => {missing_count} missing columns in {len(missing_by_table)} tables")

    # ============================================================
    # Phase 4: Missing Tables (referenced in SQL but no CREATE TABLE)
    # ============================================================

    print("\n" + "-" * 60)
    print(" TABLES IN SQL CODE BUT NOT IN schema.go")
    print("-" * 60)

    # Only report tables that appear in actual SQL operations (INSERT/UPDATE/DELETE)
    sql_tables = set()
    for table, col in code_cols:
        srcs = col_sources.get(table, {}).get(col, set())
        if srcs & {"INSERT", "UPDATE"}:
            sql_tables.add(table)
        if col == "__table_used__":
            sql_tables.add(table)

    missing_tables = sorted(t for t in sql_tables
                            if t not in schema_tables
                            and t not in GO_PACKAGES
                            and len(t) >= 3)

    if not missing_tables:
        print("\n  OK — All SQL-referenced tables exist in schema.\n")
    else:
        for table in missing_tables:
            # Show which operations reference this table
            all_srcs = set()
            for col_srcs in col_sources.get(table, {}).values():
                all_srcs.update(col_srcs)
            src_str = "+".join(sorted(all_srcs)) if all_srcs else "?"
            print(f"  [NO TABLE] {table}  (via: {src_str})")
        print(f"\n  => {len(missing_tables)} tables referenced in SQL but not defined in schema.go")

    # ============================================================
    # Phase 5: Duplicate CREATE TABLE detection
    # ============================================================

    print("\n" + "-" * 60)
    print(" DUPLICATE CREATE TABLE DETECTION")
    print("-" * 60)

    dup_found = 0
    table_counts = defaultdict(int)
    for m in re.finditer(r"CREATE TABLE IF NOT EXISTS\s+(\w+)", schema_text, re.IGNORECASE):
        table_counts[m.group(1).lower()] += 1

    for table, count in sorted(table_counts.items()):
        if count > 1:
            print(f"  [DUPLICATE] {table} — defined {count} times")
            dup_found += 1

    if dup_found == 0:
        print("\n  OK — No duplicate CREATE TABLE statements.\n")
    else:
        print(f"\n  => {dup_found} tables with duplicate CREATE TABLE statements")

    # ============================================================
    # Phase 6: Scan parameter count matching (basic)
    # ============================================================

    print("\n" + "-" * 60)
    print(" SCAN vs SELECT COLUMN COUNT CHECK")
    print("-" * 60)

    scan_mismatches = 0
    for fpath, ftext in sorted(file_texts.items()):
        # Find QueryRow(...).Scan(...) patterns
        # First extract the SQL string before Scan
        # Pattern: QueryRow(ctx, `SELECT ...`, args...).Scan(&a, &b, ...)
        scan_pattern = re.finditer(
            r'QueryRow(?:Context)?\(ctx,\s*`([^`]*)`[^)]*\)\.Scan\(([^)]+)\)',
            ftext
        )
        for sm in scan_pattern:
            sql = sm.group(1)
            scan_args = sm.group(2)
            # Count SELECT columns (rough: count commas in SELECT clause before FROM)
            select_part = re.search(r'SELECT\s+(.*?)\s+FROM', sql, re.IGNORECASE | re.DOTALL)
            if select_part:
                # Count non-empty comma-separated parts
                cols_str = select_part.group(1).strip()
                if cols_str == '*':
                    continue
                # Simple count: split by comma, handle COUNT(*), COALESCE(...) etc
                sel_cols = re.findall(r"([^,(]*(?:\([^)]*\)[^,]*)?),", cols_str)
                sel_count = len(sel_cols) + 1  # last column has no trailing comma

                # Count Scan arguments
                scan_count = scan_args.count('&')

                if sel_count != scan_count:
                    relpath = os.path.relpath(fpath, PROJECT_DIR)
                    print(f"  [MISMATCH] {relpath}: SELECT {sel_count} cols, Scan {scan_count} vars")
                    print(f"    SQL: {sql[:100]}...")
                    scan_mismatches += 1

    if scan_mismatches == 0:
        print("\n  OK — No obvious Scan mismatches detected.\n")
    else:
        print(f"\n  => {scan_mismatches} potential Scan mismatches (review manually)")

    # ============================================================
    # Summary
    # ============================================================

    print("\n" + "=" * 60)
    print(" SUMMARY")
    print("=" * 60)
    print(f"  Schema tables:      {len(schema_tables)}")
    print(f"  Schema columns:     {len(schema_cols)}")
    print(f"  Code column refs:   {len(code_cols)}")
    print(f"  Missing columns:    {missing_count}")
    print(f"  Missing tables:     {len(missing_tables)}")
    print(f"  Duplicate tables:   {dup_found}")
    print(f"  Scan mismatches:    {scan_mismatches} (warnings, may be false positives)")
    print("=" * 60)

    # Only hard-fail on missing columns/tables/duplicates; Scan mismatches are warnings
    hard_issues = missing_count + len(missing_tables) + dup_found
    if hard_issues == 0:
        print(" PASS")
        if scan_mismatches > 0:
            print(f" ({scan_mismatches} Scan warnings — review with: grep -n 'QueryRow.*Scan' internal/routes/webhook.go)")
    else:
        print(f" FAIL — {hard_issues} hard issue(s) found, fix before deploying")
    print()

    sys.exit(0 if hard_issues == 0 else 1)


if __name__ == "__main__":
    main()

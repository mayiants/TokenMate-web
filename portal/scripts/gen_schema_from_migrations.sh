#!/bin/bash
# This script scans all migration files and compares against internal/db/schema.go
# to find any missing tables, columns, or indexes.

cd /home/ubuntu/workspace/ai-portal-go

SCHEMA="internal/db/schema.go"
SEED="internal/db/seed.go"
MIG_DIR="migrations"

echo "=========================================="
echo "Schema Diff: migrations vs schema.go"
echo "=========================================="

# --- 1. Find tables in migrations but missing from schema.go ---
echo ""
echo "--- Tables in migrations but possibly missing from schema.go ---"
mig_tables=$(grep -rhoE 'CREATE TABLE IF NOT EXISTS [a-z_]+' "$MIG_DIR"/*.sql | sed 's/CREATE TABLE IF NOT EXISTS //' | sort | uniq)
code_tables=$(grep -oE 'CREATE TABLE IF NOT EXISTS [a-z_]+' "$SCHEMA" | sed 's/CREATE TABLE IF NOT EXISTS //' | sort | uniq)
for t in $mig_tables; do
    if ! echo "$code_tables" | grep -qx "$t"; then
        echo "  [MISSING TABLE] $t"
    fi
done

# --- 2. Find ALTER TABLE ADD COLUMN in migrations but missing from schema.go ---
echo ""
echo "--- ALTER TABLE ADD COLUMN in migrations but missing from schema.go ---"
for f in "$MIG_DIR"/*.sql; do
    grep -iE 'ALTER TABLE [a-z_]+ ADD COLUMN' "$f" | while IFS= read -r line; do
        tbl=$(echo "$line" | grep -oE 'ALTER TABLE [a-z_]+' | awk '{print $3}')
        col=$(echo "$line" | grep -oE 'ADD COLUMN( IF NOT EXISTS)? [a-z_]+' | sed 's/ADD COLUMN IF NOT EXISTS //;s/ADD COLUMN //' | awk '{print $1}')
        if [ -n "$tbl" ] && [ -n "$col" ]; then
            found=$(grep -c "ALTER TABLE $tbl ADD COLUMN IF NOT EXISTS $col" "$SCHEMA" || echo 0)
            if [ "$found" = "0" ]; then
                echo "  [MISSING COLUMN] $f: $tbl.$col"
            fi
        fi
    done
done

# --- 3. Find CREATE INDEX in migrations but missing from schema.go ---
echo ""
echo "--- CREATE INDEX in migrations but missing from schema.go ---"
for f in "$MIG_DIR"/*.sql; do
    grep -iE 'CREATE( UNIQUE)? INDEX' "$f" | while IFS= read -r line; do
        idx=$(echo "$line" | grep -oE 'INDEX IF NOT EXISTS [a-z_]+' | awk '{print $4}')
        if [ -z "$idx" ]; then
            idx=$(echo "$line" | grep -oE 'INDEX [a-z_]+' | awk '{print $2}')
        fi
        if [ -n "$idx" ]; then
            found=$(grep -c "$idx" "$SCHEMA" || echo 0)
            if [ "$found" = "0" ]; then
                echo "  [MISSING INDEX] $f: $idx"
            fi
        fi
    done
done

# --- 4. Check Go code direct field references against schema.go ---
echo ""
echo "--- Go code field references not found in schema.go ---"
grep -rhoE '\b(provider_health_stats|upstream_key_usage|openrouter_models|enterprise_audit_log|enterprise_member_role_log|model_provider_routing|provider_health_stats|upstream_key_usage)\.[a-z_]+' --include="*.go" internal/ | sort | uniq | while IFS= read -r ref; do
    tbl=$(echo "$ref" | cut -d'.' -f1)
    col=$(echo "$ref" | cut -d'.' -f2)
    found_create=$(grep -c "CREATE TABLE IF NOT EXISTS $tbl" "$SCHEMA" || echo 0)
    found_col=$(grep -c "$col" "$SCHEMA" || echo 0)
    if [ "$found_create" -gt 0 ] && [ "$found_col" = "0" ]; then
        echo "  [MAYBE MISSING] Go ref: $ref"
    fi
done

echo ""
echo "=========================================="
echo "Done. If no [MISSING] lines above, schema is complete."
echo "=========================================="

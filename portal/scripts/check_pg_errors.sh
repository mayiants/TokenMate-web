#!/bin/bash
# Check PostgreSQL logs for schema-related errors
# Usage: bash scripts/check_pg_errors.sh
echo "=== PostgreSQL Schema Errors (last 500 lines) ==="
docker logs ai-portal-postgres 2>&1 | tail -500 | grep 'ERROR:' | grep -oP '(ERROR: .*?)(?=;|$)' | sort | uniq -c | sort -rn

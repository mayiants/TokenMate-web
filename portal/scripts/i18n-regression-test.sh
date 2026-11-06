#!/usr/bin/env bash
set -euo pipefail

# i18n regression test for new / changed pages.
# Fails if any used translation key is missing from en.json/zh.json,
# or if templates fail to parse.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT}"

echo "=== i18n completeness audit ==="
python3 scripts/i18n_full_audit.py --strict

echo "=== template smoke tests ==="
go test ./internal/web/... -v

echo "=== i18n regression tests passed ==="

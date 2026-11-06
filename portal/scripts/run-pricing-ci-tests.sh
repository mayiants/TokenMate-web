#!/usr/bin/env bash
set -euo pipefail

# Pricing regression test runner for CI/local verification.
# Runs the billing/pricing unit tests for both Portal and Proxy.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

echo "=== Portal pricing tests ==="
cd "${ROOT}"
go test ./internal/services/... ./internal/routes/... -run 'Pricing|Billing' -v

echo "=== Proxy pricing tests ==="
cd "${ROOT}/../ai-proxy-go"
go test ./pkg/billing/... -v

echo "=== Pricing CI tests passed ==="

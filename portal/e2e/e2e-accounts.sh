#!/usr/bin/env bash
# ============================================================
# E2E test-account lifecycle helper
# ============================================================
# Enables the seeded E2E test accounts (e2e-admin / e2e-user /
# e2e-enterprise) right before a UI/a11y/console test run, and disables them
# again afterwards. Keeping these privileged test accounts 'suspended' while
# no test is running avoids leaving usable admin/enterprise logins active on a
# shared/long-lived database between runs.
#
# The accounts themselves are seeded by cmd/server (SEED_E2E_ACCOUNTS=true) or,
# locally, created once by hand. This script only flips users.status.
#
# Usage:
#   bash e2e/e2e-accounts.sh enable        # set status='active'  (run BEFORE tests)
#   bash e2e/e2e-accounts.sh disable       # set status='suspended' for the 3 fixed accounts
#   bash e2e/e2e-accounts.sh disable-all   # set status='suspended' for all email LIKE 'e2e-%'
#                                          # (override pattern with E2E_EMAIL_PATTERN)
#
# DB connection is taken from env with sensible local-container defaults:
#   DB_HOST (127.0.0.1) DB_PORT (5432) DB_USER (postgres) DB_NAME (ai_proxy_portal)
#   DB_PASSWORD (optional). Set PSQL_DOCKER=<container> to exec psql inside a
#   docker container instead of using a local psql client.
# ============================================================
set -euo pipefail

ACTION="${1:-}"
E2E_EMAIL_PATTERN="${E2E_EMAIL_PATTERN:-}"
case "$ACTION" in
  enable)       NEW_STATUS="active" ;;
  disable)      NEW_STATUS="suspended" ;;
  disable-all)
    NEW_STATUS="suspended"
    E2E_EMAIL_PATTERN="${E2E_EMAIL_PATTERN:-e2e-%}"
    ;;
  *) echo "usage: $0 enable|disable|disable-all" >&2; exit 2 ;;
esac

E2E_ADMIN_EMAIL="${E2E_ADMIN_EMAIL:-e2e-admin@example.com}"
E2E_USER_EMAIL="${E2E_USER_EMAIL:-e2e-user@example.com}"
E2E_ENTERPRISE_EMAIL="${E2E_ENTERPRISE_EMAIL:-e2e-enterprise@example.com}"

DB_HOST="${DB_HOST:-127.0.0.1}"
DB_PORT="${DB_PORT:-5432}"
DB_USER="${DB_USER:-postgres}"
DB_NAME="${DB_NAME:-ai_proxy_portal}"

if [ -n "$E2E_EMAIL_PATTERN" ]; then
  SQL="UPDATE users SET status = '${NEW_STATUS}', updated_at = NOW()
       WHERE email LIKE '${E2E_EMAIL_PATTERN}'
         AND deleted_at IS NULL;"
  echo "[e2e-accounts] ${ACTION} -> status='${NEW_STATUS}' for email LIKE '${E2E_EMAIL_PATTERN}'"
else
  SQL="UPDATE users SET status = '${NEW_STATUS}', updated_at = NOW()
       WHERE email IN ('${E2E_ADMIN_EMAIL}', '${E2E_USER_EMAIL}', '${E2E_ENTERPRISE_EMAIL}')
         AND deleted_at IS NULL;"
  echo "[e2e-accounts] ${ACTION} -> status='${NEW_STATUS}' for e2e-admin/user/enterprise"
fi

if [ -n "${PSQL_DOCKER:-}" ]; then
  docker exec "${PSQL_DOCKER}" psql -U "${DB_USER}" -d "${DB_NAME}" -c "${SQL}"
else
  # Preserve an inherited PGPASSWORD when DB_PASSWORD is not explicitly set.
  export PGPASSWORD="${DB_PASSWORD:-${PGPASSWORD:-}}"
  if [ -z "$PGPASSWORD" ]; then
    echo "[e2e-accounts] warning: no DB password configured (set DB_PASSWORD or PGPASSWORD)" >&2
  fi
  psql -h "${DB_HOST}" -p "${DB_PORT}" -U "${DB_USER}" -d "${DB_NAME}" -c "${SQL}"
fi

echo "[e2e-accounts] done"

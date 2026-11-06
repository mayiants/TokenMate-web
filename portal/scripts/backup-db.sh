#!/bin/bash
# PostgreSQL Backup Script (supports local Docker container or external Aurora/PG)
# Usage: ./backup-db.sh [retention_days]
#
# Local mode (default): docker exec ai-portal-postgres pg_dump
# External mode: set DB_HOST (and DB_PASSWORD/DB_USER/DB_NAME/DB_PORT/DB_SSLMODE) to a
#                non-local endpoint; uses a throwaway postgres client container so the
#                host does not need psql/pg_dump installed.

set -e

BACKUP_DIR="${BACKUP_DIR:-/home/ubuntu/backups}"
RETENTION_DAYS="${1:-7}"
DB_NAME="${DB_NAME:-ai_proxy_portal}"
DB_USER="${DB_USER:-postgres}"
DB_HOST="${DB_HOST:-}"
DB_PORT="${DB_PORT:-5432}"
DB_SSLMODE="${DB_SSLMODE:-require}"
CONTAINER="${DB_CONTAINER:-ai-portal-postgres}"
DATE=$(date +%Y%m%d_%H%M%S)
BACKUP_FILE="${BACKUP_DIR}/portal_${DATE}.sql.gz"

mkdir -p "${BACKUP_DIR}"

is_external() {
    case "${DB_HOST}" in
        ""|postgres|127.0.0.1|localhost) return 1 ;;
        *) return 0 ;;
    esac
}

echo "[$(date)] Starting backup: ${BACKUP_FILE}"

if is_external; then
    echo "[$(date)] External DB: ${DB_HOST}:${DB_PORT}/${DB_NAME} (sslmode=${DB_SSLMODE})"
    docker run --rm --network host \
        -e PGPASSWORD="${DB_PASSWORD:-}" \
        -e PGSSLMODE="${DB_SSLMODE}" \
        postgres:15-alpine \
        pg_dump -h "${DB_HOST}" -p "${DB_PORT}" -U "${DB_USER}" -d "${DB_NAME}" --no-owner --no-acl \
        | gzip > "${BACKUP_FILE}"
else
    echo "[$(date)] Local Docker DB: container ${CONTAINER}"
    docker exec "${CONTAINER}" pg_dump -U "${DB_USER}" "${DB_NAME}" | gzip > "${BACKUP_FILE}"
fi

FILE_SIZE=$(du -h "${BACKUP_FILE}" | cut -f1)
echo "[$(date)] Backup completed: ${BACKUP_FILE} (${FILE_SIZE})"

# Clean up old backups
DELETED=$(find "${BACKUP_DIR}" -name "portal_*.sql.gz" -mtime +${RETENTION_DAYS} -delete -print | wc -l)
echo "[$(date)] Cleaned up ${DELETED} old backups (>${RETENTION_DAYS} days)"

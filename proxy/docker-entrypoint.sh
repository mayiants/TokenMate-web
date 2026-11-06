#!/bin/sh
set -e

# Named volumes are created root-owned by Docker. Ensure the retention
# directory is writable by the app user before dropping privileges.
if [ -d /data/retention ]; then
    chown -R appuser:appuser /data/retention
fi

exec su-exec appuser ./ai-proxy "$@"

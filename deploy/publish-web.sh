#!/bin/bash
# Build the React SPA and publish it into the portal's embed directory.
#
# portal/ now lives in the TokenMate monorepo (it was folded out of this repo
# when TokenMate-web was split off), so the target is that checkout's
# portal/webui/dist — portal go:embed's it at build time.
#
# Usage:
#   ./deploy/publish-web.sh                                  # sibling checkout at ../ai-gateway-go
#   TOKENMATE_REPO=/path/to/ai-gateway-go ./deploy/publish-web.sh
set -e
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TOKENMATE_REPO="${TOKENMATE_REPO:-$ROOT/../ai-gateway-go}"
WEBUI_DIST="$TOKENMATE_REPO/portal/webui/dist"

if [ ! -d "$(dirname "$WEBUI_DIST")" ]; then
  echo "error: portal webui directory not found: $(dirname "$WEBUI_DIST")" >&2
  echo "       set TOKENMATE_REPO to a TokenMate monorepo checkout (the one containing portal/)" >&2
  exit 1
fi

cd "$ROOT/web"
if [ ! -d node_modules ]; then
  npm ci --no-audit --no-fund
fi
npm run build
rsync -a --delete dist/ "$WEBUI_DIST/"
rsync -a --delete dist/ "$ROOT/web/dist-preview/"
echo "SPA published -> $WEBUI_DIST (portal rebuild required to embed)"

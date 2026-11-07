#!/bin/bash
# Build the React SPA and publish it into the portal's embed directory.
# Usage: ./deploy/publish-web.sh   (from the woodgit-platform root)
set -e
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

cd "$ROOT/web"
if [ ! -d node_modules ]; then
  npm ci --no-audit --no-fund
fi
npm run build
rsync -a --delete dist/ "$ROOT/portal/webui/dist/"
rsync -a --delete dist/ "$ROOT/web/dist-preview/"
echo "SPA published -> portal/webui/dist (portal rebuild required to embed)"

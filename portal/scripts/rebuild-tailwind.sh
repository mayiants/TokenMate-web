#!/bin/bash
# Rebuild Tailwind CSS from templates
# Usage: ./scripts/rebuild-tailwind.sh

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"

echo "[Tailwind] Building CSS from templates..."

# Use pre-installed tailwindcss in /tmp/tw-build
if [ ! -d /tmp/tw-build/node_modules ]; then
    echo "[Tailwind] Installing tailwindcss + daisyui..."
    mkdir -p /tmp/tw-build
    cd /tmp/tw-build
    npm init -y >/dev/null 2>&1
    npm install tailwindcss@3 daisyui@4 --cache /tmp/npm-cache >/dev/null 2>&1
fi

cd /tmp/tw-build

# Write config
cat > tailwind.config.js << EOF
/** @type {import('tailwindcss').Config} */
module.exports = {
  content: [
    "$PROJECT_DIR/web/templates/**/*.html",
  ],
  darkMode: ['class', '[data-theme="dark"]'],
  theme: {
    extend: {
      colors: {
        primary: 'var(--primary)',
        secondary: 'var(--text-muted)',
        success: 'var(--success)',
        warning: 'var(--warning)',
        danger: 'var(--error)',
        info: 'var(--color-info)',
        violet: 'var(--color-violet)',
        cyan: 'var(--color-cyan)',
        orange: 'var(--color-orange)',
        indigo: 'var(--color-indigo)',
      },
      backgroundImage: {
        'brand-gradient': 'var(--brand-gradient)',
        'banner-brand': 'var(--banner-gradient-brand)',
        'banner-blue': 'var(--banner-gradient-blue)',
        'banner-purple': 'var(--banner-gradient-purple)',
        'banner-amber': 'var(--banner-gradient-amber)',
        'banner-rose': 'var(--banner-gradient-rose)',
        'banner-teal': 'var(--banner-gradient-teal)',
      }
    }
  },
  plugins: [require('daisyui')],
  daisyui: {
    // Prefix ALL daisyUI classes with "du-" so component class names (modal,
    // btn, card, badge, table, ...) never collide with the app's existing
    // app.css design system. Without this, daisyUI's global .modal/.btn/etc.
    // component CSS leaks onto app components and breaks them (e.g. .modal
    // became position:fixed;pointer-events:none). Use classes as du-table,
    // du-input, du-select, du-badge, du-badge-outline in templates.
    prefix: "du-",
    // Theme keys MUST match the existing data-theme values the app sets on
    // <html> ('light' / 'dark'), otherwise daisyUI theme variables (--b2 etc.)
    // never activate and component styling (e.g. table-zebra) has no effect.
    themes: [
      {
        light: {
          "primary": "#2563eb",
          "secondary": "#64748b",
          "accent": "#0891b2",
          "neutral": "#1f2937",
          "base-100": "#ffffff",
          "info": "#0ea5e9",
          "success": "#16a34a",
          "warning": "#b45309",
          "error": "#dc2626",
        },
      },
      "dark",
    ],
    base: false,
    styled: true,
    utils: true,
    logs: false,
  },
}
EOF

cat > input.css << 'EOF'
@tailwind base;
@tailwind components;
@tailwind utilities;
EOF

npx tailwindcss -i input.css -o "$PROJECT_DIR/web/static/css/tailwind.min.css" --minify 2>&1 | grep -v "npm notice\|browserslist"

SIZE=$(stat -c%s "$PROJECT_DIR/web/static/css/tailwind.min.css")
echo "[Tailwind] Done: $(numfmt --to=iec $SIZE) ($SIZE bytes)"

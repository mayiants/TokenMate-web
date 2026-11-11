import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import purgecss from '@fullhuman/postcss-purgecss';

// Tailwind-style CSS purging: Semi ships only a monolithic semi.min.css
// (~700KB raw). We import it wholesale and let PurgeCSS drop rules our JSX
// never references. Safelist covers Semi runtime state classes (opened,
// focused, animation states) that only exist in Semi JS at runtime.
// PurgeCSS disabled 2026-11-07: over-purged runtime-composed classes (sider
// background, SideSheet positioning) breaking the whole app in dev AND prod.
// Full semi.min.css (~700KB raw / 84KB gzip) is acceptable; revisit only with
// a class inventory + visual diff harness.
const purgeDisabled = undefined;

// Visible build tag removed from the sider (reviewer confusion) — the define
// stays so external tooling can still inject a build identifier if needed.
const BUILD_TAG = process.env.BUILD_TAG || Date.now().toString(36);

// Backend the dev/preview server proxies /api to. Overridable so a CI runner or
// an alternate environment can point the UI gates at another portal instance.
const API_TARGET = process.env.API_TARGET || 'http://127.0.0.1:3001';

export default defineConfig({
  define: {
    __BUILD_TAG__: JSON.stringify(BUILD_TAG),
  },
  plugins: [react()],
  server: {
    host: process.env.HOST || '127.0.0.1',
    port: Number(process.env.PORT) || 5173,
    proxy: {
      '/api': {
        target: API_TARGET,
        changeOrigin: true,
      },
    },
  },
  preview: {
    host: process.env.HOST || '127.0.0.1',
    port: Number(process.env.PREVIEW_PORT) || 4174,
    proxy: {
      '/api': {
        target: API_TARGET,
        changeOrigin: true,
      },
    },
  },
  build: {
    outDir: 'dist',
    sourcemap: false,
    chunkSizeWarningLimit: 1500,
  },

});

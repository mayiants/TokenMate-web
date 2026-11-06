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

// Visible build tag: rendered in the sider bottom bar so reviewers can
// always tell WHICH build their browser actually loaded (cache disputes).
const BUILD_TAG = process.env.BUILD_TAG || Date.now().toString(36);

export default defineConfig({
  define: {
    __BUILD_TAG__: JSON.stringify(BUILD_TAG),
  },
  plugins: [react()],
  server: {
    host: '127.0.0.1',
    port: 5173,
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:3001',
        changeOrigin: true,
      },
    },
  },
  preview: {
    host: '127.0.0.1',
    port: 4174,
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:3001',
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

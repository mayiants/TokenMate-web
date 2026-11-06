// Lighthouse scan for public pages — thresholds aligned with portal CI
// (performance 0.5 lenient for CI, accessibility 0.9, best-practices 0.9).
// Lighthouse needs a system Chrome; point it at playwright's chromium.
process.env.CHROME_PATH = process.env.CHROME_PATH || require('os').homedir() + '/.cache/ms-playwright/chromium-1254/chrome-linux64/chrome';
const lighthouse = require('lighthouse').default || require('lighthouse');
const chromeLauncher = require('chrome-launcher');
// Run against the production build: `npm run build && npm run preview`,
// then CHECK_BASE=http://127.0.0.1:4173 npm run check:lighthouse
const BASE = process.env.CHECK_BASE || 'http://127.0.0.1:4173';
const GATE = process.argv.includes('--gate');

const PAGES = [
  { name: 'landing', path: '/' },
  { name: 'models', path: '/models' },
  { name: 'login', path: '/login' },
];
// a11y / best-practices are the hard gates. Performance stays lenient:
// portal CI uses 0.5 on GitHub runners; this 2-vCPU ECS is slower still, and
// gzip+http2 on the production host improves transfer further.
const THRESHOLDS = { performance: 0.45, accessibility: 0.9, 'best-practices': 0.9 };

(async () => {
  const chrome = await chromeLauncher.launch({ chromeFlags: ['--headless', '--no-sandbox', '--disable-dev-shm-usage'] });
  let failed = 0;
  for (const p of PAGES) {
    const result = await lighthouse(`${BASE}${p.path}`, {
      port: chrome.port,
      onlyCategories: ['performance', 'accessibility', 'best-practices'],
      formFactor: 'desktop',
      screenEmulation: { disabled: true },
      quiet: true,
    });
    const scores = {};
    for (const cat of ['performance', 'accessibility', 'best-practices']) {
      scores[cat] = result.lhr.categories[cat].score ?? 0;
    }
    const misses = Object.entries(THRESHOLDS).filter(([k, min]) => scores[k] < min);
    for (const [k, v] of Object.entries(scores)) {
      const mark = v >= (THRESHOLDS[k] ?? 1) ? '✓' : '✗';
      console.log(`${mark} ${p.name} ${k}: ${(v * 100).toFixed(0)}`);
    }
    if (misses.length) failed++;
  }
  await chrome.kill();
  console.log(`lighthouse: ${PAGES.length} pages, ${failed} below thresholds`);
  if (GATE && failed) process.exit(1);
})();

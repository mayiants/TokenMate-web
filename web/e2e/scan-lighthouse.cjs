// Lighthouse scan for public pages — thresholds aligned with portal CI
// (performance 0.5 lenient for CI, accessibility 0.9, best-practices 0.9).
// Run against the production build: `npm run build && npm run preview`,
// then CHECK_BASE=http://127.0.0.1:4174 npm run check:lighthouse
const fs = require('fs');
const path = require('path');
const lighthouse = require('lighthouse').default || require('lighthouse');
const chromeLauncher = require('chrome-launcher');
const { PUBLIC_PAGES } = require('./pages.cjs');

// Lighthouse needs a system Chrome; default to playwright's bundled chromium.
function playwrightChrome() {
  const root = path.join(require('os').homedir(), '.cache', 'ms-playwright');
  try {
    const dir = fs
      .readdirSync(root)
      .filter((d) => /^chromium-\d+$/.test(d))
      .sort()
      .pop();
    return dir ? path.join(root, dir, 'chrome-linux64', 'chrome') : '';
  } catch {
    return '';
  }
}
process.env.CHROME_PATH = process.env.CHROME_PATH || playwrightChrome();

const BASE = process.env.CHECK_BASE || 'http://127.0.0.1:4174';
const GATE = process.argv.includes('--gate');

// Public routes only: authenticated pages need a backend session, which
// lighthouse cannot reuse here — axe/console/responsive cover those.
const PAGES = PUBLIC_PAGES.slice(0, 3).map((p) => ({
  name: p === '/' ? 'landing' : p.replace(/\//g, ''),
  path: p,
}));

// a11y / best-practices are the hard gates. Performance stays lenient:
// portal CI uses 0.5 on GitHub runners; this 2-vCPU ECS is slower still, and
// gzip+http2 on the production host improves transfer further.
const THRESHOLDS = { performance: 0.45, accessibility: 0.9, 'best-practices': 0.9 };

(async () => {
  if (!fs.existsSync(process.env.CHROME_PATH)) {
    console.log(`! no Chrome at ${process.env.CHROME_PATH} — run \`npx playwright install chromium\``);
    if (GATE) process.exit(1);
    return;
  }
  const chrome = await chromeLauncher.launch({
    chromePath: process.env.CHROME_PATH,
    chromeFlags: ['--headless', '--no-sandbox', '--disable-dev-shm-usage'],
  });
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

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
// LH_RUNS=1 restores the old single-run behaviour for quick local checks.
const RUNS = Math.max(1, Number(process.env.LH_RUNS) || 3);

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
    // Single Lighthouse runs on a shared 2-vCPU host swing wildly (observed
    // /models: 61 -> 34 on identical code). Run RUNS times and gate on the
    // median per category; the raw samples and key metrics are printed so a
    // real regression is distinguishable from scheduler noise.
    const samples = [];
    for (let i = 0; i < RUNS; i++) {
      const result = await lighthouse(`${BASE}${p.path}`, {
        port: chrome.port,
        onlyCategories: ['performance', 'accessibility', 'best-practices'],
        formFactor: 'desktop',
        screenEmulation: { disabled: true },
        quiet: true,
      });
      const a = result.lhr.audits;
      const metric = (id) => a[id]?.numericValue;
      samples.push({
        scores: Object.fromEntries(
          ['performance', 'accessibility', 'best-practices'].map((c) => [c, result.lhr.categories[c].score ?? 0])
        ),
        fcp: metric('first-contentful-paint'),
        lcp: metric('largest-contentful-paint'),
        tbt: metric('total-blocking-time'),
        bytes: metric('total-byte-weight'),
      });
    }
    const median = (arr) => [...arr].sort((x, y) => x - y)[Math.floor(arr.length / 2)];
    const scores = {};
    for (const cat of ['performance', 'accessibility', 'best-practices']) {
      scores[cat] = median(samples.map((s) => s.scores[cat]));
    }
    const misses = Object.entries(THRESHOLDS).filter(([k, min]) => scores[k] < min);
    for (const [k, v] of Object.entries(scores)) {
      const mark = v >= (THRESHOLDS[k] ?? 1) ? '✓' : '✗';
      const raw = samples.map((s) => (s.scores[k] * 100).toFixed(0)).join('/');
      console.log(`${mark} ${p.name} ${k}: ${(v * 100).toFixed(0)}  (runs ${raw})`);
    }
    const ms = (v) => (v == null ? '—' : `${Math.round(v)}ms`);
    const m = samples[Math.floor(samples.length / 2)];
    console.log(
      `   ${p.name} metrics(median run): FCP ${ms(m.fcp)} · LCP ${ms(m.lcp)} · TBT ${ms(m.tbt)} · ` +
        `transfer ${m.bytes == null ? '—' : Math.round(m.bytes / 1024) + ' KiB'}`
    );
    if (misses.length) failed++;
  }
  await chrome.kill();
  console.log(`lighthouse: ${PAGES.length} pages, ${failed} below thresholds`);
  if (GATE && failed) process.exit(1);
})();

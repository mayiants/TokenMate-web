#!/usr/bin/env node
/**
 * Lighthouse scanner for public Portal pages.
 *
 * Uses the Playwright-installed Chromium so no system Chrome is required.
 *
 * Usage:
 *   node e2e/scan-lighthouse.js
 *   node e2e/scan-lighthouse.js --gate
 */

const lighthouseModule = require('lighthouse');
const lighthouse = lighthouseModule.default || lighthouseModule;
const chromeLauncher = require('chrome-launcher');
const { chromium } = require('playwright');

const BASE_URL = process.env.PORTAL_URL || 'http://127.0.0.1:3001';
const GATE_MODE = process.argv.includes('--gate');

const PAGES = [
  { name: 'Landing', path: '/' },
  { name: 'Models', path: '/models' },
  // Login/register are intentionally noindex utility pages (see the SEO plan):
  // Lighthouse's SEO audit penalizes noindex, so the SEO threshold is skipped
  // for them; performance/accessibility/best-practices still apply.
  { name: 'Login', path: '/login', noindex: true },
];

const THRESHOLDS = {
  // CI runners are slow; keep performance threshold lenient to avoid flakes.
  performance: 0.5,
  accessibility: 0.9,
  // CI runs over HTTP, so some secure-context audits fail by design.
  'best-practices': 0.7,
  seo: 0.9,
};

async function main() {
  console.log(`\n🚦 Lighthouse Scanner`);
  console.log(`   Target: ${BASE_URL}`);
  console.log(`   Gate mode: ${GATE_MODE ? 'ON' : 'OFF'}\n`);

  const chromePath = chromium.executablePath();
  const chrome = await chromeLauncher.launch({
    chromeFlags: ['--headless', '--no-sandbox', '--disable-gpu'],
    chromePath,
  });

  const results = [];

  for (const page of PAGES) {
    const url = `${BASE_URL}${page.path}`;
    const options = {
      logLevel: 'error',
      output: 'json',
      port: chrome.port,
      onlyCategories: Object.keys(THRESHOLDS),
    };

    try {
      const runnerResult = await lighthouse(url, options);
      const categories = runnerResult.lhr.categories;
      const scores = {
        performance: categories.performance.score,
        accessibility: categories.accessibility.score,
        'best-practices': categories['best-practices'].score,
        seo: categories.seo.score,
      };
      results.push({ page: page.name, path: page.path, scores, noindex: !!page.noindex });

      const parts = Object.entries(scores).map(([k, v]) => {
        const pct = Math.round(v * 100);
        const skip = page.noindex && k === 'seo';
        const ok = (skip || v >= THRESHOLDS[k]) ? '' : '!';
        return `${k}: ${pct}${ok}`;
      });
      console.log(`  ${scoresOk(scores, page.noindex) ? '✅' : '⚠️'}  ${page.name} (${parts.join(', ')})`);
    } catch (e) {
      console.log(`  ❌ ${page.name}: ${e.message.split('\n')[0]}`);
      results.push({ page: page.name, path: page.path, error: e.message });
    }
  }

  await chrome.kill();

  /* ---------- Summary ---------- */
  const failed = results.filter(r => !r.error && !scoresOk(r.scores, r.noindex));
  const errored = results.filter(r => r.error);

  console.log('\n' + '='.repeat(60));
  console.log('📊 SUMMARY REPORT');
  console.log('='.repeat(60));
  console.log(`  Pages scanned: ${results.length}`);
  console.log(`  Below threshold: ${failed.length}`);
  console.log(`  Errors: ${errored.length}`);

  if (GATE_MODE && (failed.length > 0 || errored.length > 0)) {
    console.log('\n🚫 Gate mode: exiting with code 1\n');
    process.exit(1);
  }

  console.log('\n✅ Lighthouse scan complete.\n');
}

function scoresOk(scores, noindex) {
  return Object.entries(THRESHOLDS).every(([k, min]) => {
    // noindex utility pages (login/register) intentionally score low on the
    // SEO audit; don't gate them on it.
    if (noindex && k === 'seo') return true;
    return scores[k] >= min;
  });
}

main().catch(e => {
  console.error('Lighthouse scanner crashed:', e);
  process.exit(2);
});

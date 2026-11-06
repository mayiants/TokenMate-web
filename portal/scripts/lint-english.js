#!/usr/bin/env node
/**
 * English copy linting using Vale.
 *
 * Checks web/i18n/en.json product copy. With --docs, also checks Markdown
 * files in docs/ modified within the last 14 days.
 *
 * Usage:
 *   node scripts/lint-english.js
 *   node scripts/lint-english.js --docs
 *   node scripts/lint-english.js --gate
 *
 * Vale is downloaded automatically if not found on PATH.
 */

const fs = require('fs');
const path = require('path');
const { execFileSync, execSync } = require('child_process');
const GATE_MODE = process.argv.includes('--gate');
const SCAN_DOCS = process.argv.includes('--docs');
const ROOT = path.resolve(__dirname, '..');

const VALE_VERSION = '3.7.1';
const VALE_DIR = path.join(ROOT, '.bin');
const VALE_BIN = path.join(VALE_DIR, 'vale');

function findVale() {
  try {
    const stdout = execFileSync('which', ['vale'], { encoding: 'utf8' });
    return stdout.trim();
  } catch {
    if (fs.existsSync(VALE_BIN)) return VALE_BIN;
    return null;
  }
}

function downloadVale() {
  const url = `https://github.com/errata-ai/vale/releases/download/v${VALE_VERSION}/vale_${VALE_VERSION}_Linux_64-bit.tar.gz`;
  const tarPath = path.join(VALE_DIR, 'vale.tar.gz');
  fs.mkdirSync(VALE_DIR, { recursive: true });
  console.log(`Downloading Vale ${VALE_VERSION}...`);
  // GitHub release downloads occasionally return an error page instead of the
  // archive (rate limiting / CDN flake); curl -s hides that and tar then fails
  // with a confusing "not in gzip format". Validate the gzip magic bytes and
  // retry with a clean slate instead.
  let ok = false;
  for (let attempt = 1; attempt <= 3 && !ok; attempt++) {
    try { fs.unlinkSync(tarPath); } catch { /* not present yet */ }
    try {
      execFileSync('curl', [
        '-fsSL', '--retry', '3', '--retry-delay', '2',
        '--max-time', '120', url, '-o', tarPath,
      ], {
        encoding: 'utf8',
        stdio: ['ignore', 'ignore', 'pipe'],
      });
      const magic = Buffer.alloc(2);
      const fd = fs.openSync(tarPath, 'r');
      fs.readSync(fd, magic, 0, 2, 0);
      fs.closeSync(fd);
      ok = magic[0] === 0x1f && magic[1] === 0x8b;
    } catch {
      ok = false;
    }
    if (!ok) console.log(`Vale download attempt ${attempt} failed, retrying...`);
  }
  if (!ok) throw new Error(`Failed to download a valid Vale archive from ${url}`);
  execFileSync('tar', ['-xzf', tarPath, '-C', VALE_DIR]);
  fs.chmodSync(VALE_BIN, 0o755);
  fs.unlinkSync(tarPath);
  return VALE_BIN;
}

async function ensureVale() {
  let vale = findVale();
  if (!vale) {
    vale = downloadVale();
  }
  const version = execFileSync(vale, ['--version'], { encoding: 'utf8' }).trim();
  console.log(`Using ${version}\n`);
  return vale;
}

function getRecentDocs(days = 14) {
  const docsDir = path.join(ROOT, 'docs');
  if (!fs.existsSync(docsDir)) return [];
  const cutoff = Date.now() - days * 24 * 60 * 60 * 1000;
  const files = [];
  for (const entry of fs.readdirSync(docsDir)) {
    if (!entry.endsWith('.md')) continue;
    const fp = path.join(docsDir, entry);
    const stat = fs.statSync(fp);
    if (stat.mtimeMs >= cutoff) files.push(fp);
  }
  return files.sort();
}

function runVale(vale, targets) {
  if (targets.length === 0) return { output: '', code: 0 };
  try {
    const output = execFileSync(vale, targets, {
      encoding: 'utf8',
      cwd: ROOT,
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    return { output, code: 0 };
  } catch (e) {
    return { output: e.stdout || e.message, code: e.status || 1 };
  }
}

function parseSummary(output) {
  const match = output.match(/✖\s+(\d+)\s+errors?,\s+(\d+)\s+warnings?\s+and\s+(\d+)\s+suggestions?/);
  if (!match) return null;
  return {
    errors: parseInt(match[1], 10),
    warnings: parseInt(match[2], 10),
    suggestions: parseInt(match[3], 10),
  };
}

async function main() {
  console.log('✍️  English Copy Linter (Vale)');
  console.log(`   Docs scan: ${SCAN_DOCS ? 'last 14 days' : 'disabled (use --docs)'}`);
  console.log(`   Gate mode: ${GATE_MODE ? 'ON' : 'OFF'}\n`);

  const vale = await ensureVale();

  // Ensure packages are present
  try {
    execFileSync(vale, ['sync'], { encoding: 'utf8', cwd: ROOT });
  } catch (e) {
    console.error('vale sync failed:', e.stderr || e.message);
    process.exit(2);
  }

  const targets = [path.join(ROOT, 'web', 'i18n', 'en.json')];
  const docs = SCAN_DOCS ? getRecentDocs() : [];
  targets.push(...docs);

  const result = runVale(vale, targets);
  if (result.output) console.log(result.output);

  const summary = parseSummary(result.output) || { errors: 0, warnings: 0, suggestions: 0 };

  console.log('\n' + '='.repeat(60));
  console.log('📊 SUMMARY REPORT');
  console.log('='.repeat(60));
  console.log(`  Files scanned:      ${targets.length}`);
  console.log(`  en.json:            ✅`);
  console.log(`  Recent docs (*.md): ${docs.length}`);
  console.log(`  Errors:             ${summary.errors}`);
  console.log(`  Warnings:           ${summary.warnings}`);
  console.log(`  Suggestions:        ${summary.suggestions}`);

  if (GATE_MODE && summary.errors > 0) {
    console.log(`\n🚫 Gate mode: ${summary.errors} error(s) found\n`);
    process.exit(1);
  }

  console.log('\n✅ English copy lint complete.\n');
}

main().catch(e => {
  console.error('Linter crashed:', e);
  process.exit(2);
});

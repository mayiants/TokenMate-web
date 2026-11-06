// i18n gate for woodgit-web — aligned with portal CI intent:
//  1. zh/en key parity (same key set both sides)
//  2. no untranslated entries (value identical to the key)
// Exits 1 on any failure.
const fs = require('fs');
const path = require('path');

const root = path.join(__dirname, '..', 'src', 'i18n', 'locales');
const zh = JSON.parse(fs.readFileSync(path.join(root, 'zh.json'), 'utf8'));
const en = JSON.parse(fs.readFileSync(path.join(root, 'en.json'), 'utf8'));

let failures = 0;
const zhKeys = Object.keys(zh);
const enKeys = Object.keys(en);
const zhSet = new Set(zhKeys);
const enSet = new Set(enKeys);

for (const k of zhKeys) if (!enSet.has(k)) { console.log(`MISSING in en: ${k}`); failures++; }
for (const k of enKeys) if (!zhSet.has(k)) { console.log(`MISSING in zh: ${k}`); failures++; }

for (const [name, dict] of [['zh', zh], ['en', en]]) {
  for (const [k, v] of Object.entries(dict)) {
    if (v === k) { console.log(`UNTRANSLATED(${name}): ${k}`); failures++; }
    if (typeof v === 'string' && /[\u4e00-\u9fff]/.test(v) && name === 'en') {
      console.log(`CHINESE IN EN: ${k} = ${v}`); failures++;
    }
  }
}

console.log(`i18n gate: ${zhKeys.length} zh / ${enKeys.length} en keys, ${failures} problems`);
process.exit(failures ? 1 : 0);

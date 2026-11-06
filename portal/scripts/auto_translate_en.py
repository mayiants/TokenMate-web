#!/usr/bin/env python3
"""
Auto-translate remaining Chinese values in en.json using Google Translate free API.
"""
import json, urllib.request, urllib.parse, time, re

with open('web/i18n/en.json') as f:
    en = json.load(f)

# Find all keys with Chinese values
pending = []
for k, v in en.items():
    if isinstance(v, str) and any(ord(c) > 0x4e00 and ord(c) < 0x9fff for c in v):
        pending.append((k, v))

print(f'Pending translations: {len(pending)}')

def translate_google(text):
    """Use Google Translate free API."""
    if not text or not re.search(r'[\u4e00-\u9fff]', text):
        return text
    try:
        encoded = urllib.parse.quote(text)
        url = f"https://translate.googleapis.com/translate_a/single?client=gtx&sl=zh&tl=en&dt=t&q={encoded}"
        req = urllib.request.Request(url, headers={'User-Agent': 'Mozilla/5.0'})
        with urllib.request.urlopen(req, timeout=10) as resp:
            data = json.loads(resp.read().decode('utf-8'))
            if data and len(data) > 0 and data[0]:
                result = ''.join(part[0] for part in data[0] if part[0])
                return result
    except Exception as e:
        print(f'  Error translating [{text[:40]}]: {e}')
    return text

# Process in batches with delay to avoid rate limiting
batch_size = 50
for i in range(0, len(pending), batch_size):
    batch = pending[i:i+batch_size]
    for k, v in batch:
        en[k] = translate_google(v)
        time.sleep(0.3)
    print(f'  Processed {min(i+batch_size, len(pending))}/{len(pending)}')
    if i + batch_size < len(pending):
        time.sleep(2)

with open('web/i18n/en.json', 'w') as f:
    json.dump(en, f, ensure_ascii=False, indent=2)

print('Done')

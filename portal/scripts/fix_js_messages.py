#!/usr/bin/env python3
"""Translate missing JS messages and update layout.html window._i18nMessages."""

import json
import re
import time
import urllib.parse
import urllib.request
import concurrent.futures

LAYOUT_HTML = "/home/ubuntu/workspace/ai-portal-go/web/templates/layout.html"
MISSING_FILE = "/tmp/missing_js_msgs.txt"


def translate_text(text):
    url = (
        "https://translate.googleapis.com/translate_a/single"
        f"?client=gtx&sl=zh-CN&tl=en&dt=t&q={urllib.parse.quote(text)}"
    )
    try:
        with urllib.request.urlopen(url, timeout=10) as resp:
            data = json.loads(resp.read().decode("utf-8"))
            parts = data[0]
            result = "".join(p[0] for p in parts if p[0])
            return result
    except Exception as e:
        print(f"  FAIL: {text[:50]}... -> {e}")
        return None


def main():
    with open(MISSING_FILE, "r", encoding="utf-8") as f:
        msgs = [line.strip() for line in f if line.strip()]

    print(f"Translating {len(msgs)} missing JS messages...")

    results = {}
    errors = []

    def worker(text):
        time.sleep(0.15)
        en = translate_text(text)
        return text, en

    with concurrent.futures.ThreadPoolExecutor(max_workers=15) as ex:
        futures = [ex.submit(worker, m) for m in msgs]
        done = 0
        for future in concurrent.futures.as_completed(futures):
            zh, en = future.result()
            done += 1
            if done % 20 == 0:
                print(f"  Progress: {done}/{len(msgs)}")
            if en:
                results[zh] = en
            else:
                errors.append(zh)

    print(f"\nTranslated: {len(results)}, Failed: {len(errors)}")

    # Read current layout.html
    with open(LAYOUT_HTML, "r", encoding="utf-8") as f:
        content = f.read()

    # Find the window._i18nMessages object
    pattern = r'(window\._i18nMessages = \{)([^}]*)(\})'
    match = re.search(pattern, content, re.DOTALL)
    if not match:
        print("ERROR: Could not find window._i18nMessages in layout.html")
        return

    existing = match.group(2).strip()
    # Parse existing entries
    existing_dict = {}
    for line in existing.split(","):
        line = line.strip()
        if not line:
            continue
        m = re.match(r'"(.*?)"\s*:\s*"(.*?)"', line)
        if m:
            existing_dict[m.group(1)] = m.group(2)

    # Merge new translations
    for zh, en in results.items():
        existing_dict[zh] = en

    # Rebuild the object string
    pairs = []
    for k, v in sorted(existing_dict.items()):
        # Escape quotes in key and value
        k_escaped = k.replace('"', '\\"')
        v_escaped = v.replace('"', '\\"')
        pairs.append(f'"{k_escaped}": "{v_escaped}"')

    new_obj = "window._i18nMessages = {\n      " + ",\n      ".join(pairs) + "\n    };"

    # Replace in content
    new_content = re.sub(pattern, new_obj, content, flags=re.DOTALL)

    with open(LAYOUT_HTML, "w", encoding="utf-8") as f:
        f.write(new_content)

    print(f"\nUpdated {LAYOUT_HTML} with {len(results)} new translations")
    print(f"Total _i18nMessages entries: {len(existing_dict)}")


if __name__ == "__main__":
    main()

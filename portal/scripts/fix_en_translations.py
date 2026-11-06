#!/usr/bin/env python3
"""Fix 777 missing EN translations in en.json using Google Translate API."""

import json
import re
import time
import urllib.parse
import urllib.request
import concurrent.futures

EN_JSON = "/home/ubuntu/workspace/ai-portal-go/web/i18n/en.json"
ZH_JSON = "/home/ubuntu/workspace/ai-portal-go/web/i18n/zh.json"


def has_chinese(text):
    return bool(re.search(r'[\u4e00-\u9fff]', text))


def translate_text(text):
    """Translate Chinese text to English via Google Translate free API."""
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
    with open(EN_JSON, "r", encoding="utf-8") as f:
        en_data = json.load(f)
    with open(ZH_JSON, "r", encoding="utf-8") as f:
        zh_data = json.load(f)

    # Collect keys whose EN value still contains Chinese
    to_translate = []
    for key, val in en_data.items():
        if has_chinese(val):
            to_translate.append(key)

    print(f"Found {len(to_translate)} keys with Chinese values in en.json")

    # Translate with 15 concurrent workers + small delay
    results = {}
    errors = []

    def worker(key):
        zh_val = zh_data.get(key, "")
        if not zh_val:
            return key, None
        time.sleep(0.15)  # rate limit
        en = translate_text(zh_val)
        return key, en

    with concurrent.futures.ThreadPoolExecutor(max_workers=15) as ex:
        futures = {ex.submit(worker, k): k for k in to_translate}
        done = 0
        for future in concurrent.futures.as_completed(futures):
            key, en_val = future.result()
            done += 1
            if done % 50 == 0:
                print(f"  Progress: {done}/{len(to_translate)}")
            if en_val:
                results[key] = en_val
            else:
                errors.append(key)

    print(f"\nTranslated: {len(results)}, Failed: {len(errors)}")

    # Update en.json
    for key, en_val in results.items():
        en_data[key] = en_val

    # For any remaining Chinese values, fall back to the original (keep unchanged)
    if errors:
        print(f"Keys that failed to translate (keeping original): {errors}")

    with open(EN_JSON, "w", encoding="utf-8") as f:
        json.dump(en_data, f, ensure_ascii=False, indent=2)
        f.write("\n")

    print(f"\nUpdated {EN_JSON}")


if __name__ == "__main__":
    main()

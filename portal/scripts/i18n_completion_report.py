#!/usr/bin/env python3
"""
i18n completion report generator.

Outputs:
- report.json: statistics and lists of missing keys / hardcoded strings.
- missing-frontend-en.json: template for English translations of frontend _t strings.
- missing-backend-en.json: template for English translations of backend hardcoded strings.

Usage:
    python3 scripts/i18n_completion_report.py
"""

import json
import os
import re
from collections import defaultdict

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


def load_json(path):
    with open(path, "r", encoding="utf-8") as f:
        return json.load(f)


def save_json(path, data):
    with open(path, "w", encoding="utf-8") as f:
        json.dump(data, f, ensure_ascii=False, indent=2)


def scan_frontend_strings():
    """Find all strings passed to _t/showMessage/showToast/showConfirm in JS and HTML."""
    used = set()
    pattern = re.compile(r"(_t|showMessage|showToast|showConfirm)\(['\"](.+?)['\"]")
    for subdir in ("web/static/js", "web/templates"):
        for root, _, files in os.walk(os.path.join(ROOT, subdir)):
            for fn in files:
                if not fn.endswith((".js", ".html")):
                    continue
                path = os.path.join(root, fn)
                try:
                    text = open(path, "r", encoding="utf-8").read()
                except Exception:
                    continue
                for m in pattern.finditer(text):
                    used.add(m.group(2))
    return used


def scan_backend_hardcoded():
    """Find Chinese text in templates that is not inside {{t ...}} or comments."""
    results = defaultdict(list)
    chinese_re = re.compile(r"[\u4e00-\u9fff]{2,}")
    for root, _, files in os.walk(os.path.join(ROOT, "web/templates")):
        for fn in files:
            if not fn.endswith(".html"):
                continue
            path = os.path.join(root, fn)
            text = open(path, "r", encoding="utf-8").read()
            # strip {{t "..."}} / {{t '...'}}
            cleaned = re.sub(r"\{\{\s*t\s+['\"].*?['\"]\s*\}\}", "", text, flags=re.S)
            # strip Go template comments and HTML comments
            cleaned = re.sub(r"\{\{#.*?#\}\}", "", cleaned, flags=re.S)
            cleaned = re.sub(r"<!--.*?-->", "", cleaned, flags=re.S)
            # strip <script> blocks because they are checked separately via _t
            cleaned = re.sub(r"<script.*?</script>", "", cleaned, flags=re.S)
            # strip Alpine attributes that contain _t(...) since they are frontend strings
            cleaned = re.sub(r'\s+x-text="[^"]*_t\([^"]*\)[^"]*"', "", cleaned)
            cleaned = re.sub(r"\s+x-text='[^']*_t\([^']*\)[^']*'", "", cleaned)
            cleaned = re.sub(r'\s+x-show="[^"]*_t\([^"]*\)[^"]*"', "", cleaned)
            cleaned = re.sub(r"\s+x-show='[^']*_t\([^']*\)[^']*'", "", cleaned)
            for m in chinese_re.finditer(cleaned):
                snippet = m.group(0)
                # ignore plain numbers / common short words
                if len(snippet) < 2:
                    continue
                results[snippet].append(f"{os.path.relpath(path, ROOT)}:{m.start()}")
    return results


def main():
    zh = load_json(os.path.join(ROOT, "web/i18n/zh.json"))
    en = load_json(os.path.join(ROOT, "web/i18n/en.json"))
    frontend = load_json(os.path.join(ROOT, "web/static/js/i18n-messages.json"))

    frontend_used = scan_frontend_strings()
    frontend_missing = sorted(frontend_used - set(frontend.keys()))

    backend_hardcoded = scan_backend_hardcoded()

    report = {
        "backend": {
            "zh_keys": len(zh),
            "en_keys": len(en),
            "missing_in_en": sorted(set(zh) - set(en)),
            "missing_in_zh": sorted(set(en) - set(zh)),
        },
        "frontend": {
            "i18n_messages_keys": len(frontend),
            "strings_used": len(frontend_used),
            "missing_in_i18n_messages": len(frontend_missing),
            "missing_strings": frontend_missing,
        },
        "templates": {
            "hardcoded_chinese_unique": len(backend_hardcoded),
            "hardcoded_chinese_total_occurrences": sum(len(v) for v in backend_hardcoded.values()),
            "hardcoded_strings": {
                s: {"count": len(locs), "locations": locs[:5]} for s, locs in backend_hardcoded.items()
            },
        },
    }

    out_dir = os.path.join(ROOT, "tmp/i18n-report")
    os.makedirs(out_dir, exist_ok=True)

    save_json(os.path.join(out_dir, "report.json"), report)
    save_json(
        os.path.join(out_dir, "missing-frontend-en.json"),
        {s: "" for s in frontend_missing},
    )
    save_json(
        os.path.join(out_dir, "missing-backend-en.json"),
        {s: "" for s in backend_hardcoded.keys()},
    )

    print(f"Backend: zh={len(zh)} en={len(en)} missing_in_en={len(report['backend']['missing_in_en'])}")
    print(f"Frontend _t strings: {len(frontend_used)}, missing translations: {len(frontend_missing)}")
    print(f"Template hardcoded Chinese: {len(backend_hardcoded)} unique / {report['templates']['hardcoded_chinese_total_occurrences']} occurrences")
    print(f"Report written to {out_dir}/")


if __name__ == "__main__":
    main()

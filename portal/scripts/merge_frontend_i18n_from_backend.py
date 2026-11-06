#!/usr/bin/env python3
"""
Merge English translations from backend i18n files into frontend i18n-messages.json.

For every string used in frontend JS/templates via _t()/showMessage()/showToast()/showConfirm(),
if the exact same key exists in web/i18n/en.json, copy its English value into
web/static/js/i18n-messages.json. This reduces manual translation work.

Usage:
    python3 scripts/merge_frontend_i18n_from_backend.py
"""

import json
import os
import re
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


def load_json(path):
    with open(path, "r", encoding="utf-8") as f:
        return json.load(f)


def save_json(path, data):
    with open(path, "w", encoding="utf-8") as f:
        json.dump(data, f, ensure_ascii=False, indent=2)


def scan_frontend_strings():
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


def main():
    backend_en = load_json(os.path.join(ROOT, "web/i18n/en.json"))
    frontend = load_json(os.path.join(ROOT, "web/static/js/i18n-messages.json"))

    used = scan_frontend_strings()
    filled = 0
    for s in used:
        if s in frontend:
            continue
        if s in backend_en:
            frontend[s] = backend_en[s]
            filled += 1

    save_json(os.path.join(ROOT, "web/static/js/i18n-messages.json"), frontend)

    remaining = used - set(frontend.keys())
    print(f"Filled {filled} frontend strings from backend en.json.")
    print(f"Remaining strings without English translation: {len(remaining)}")
    if remaining:
        out = os.path.join(ROOT, "tmp/i18n-report/missing-frontend-en.json")
        save_json(out, {s: "" for s in sorted(remaining)})
        print(f"Wrote template to {out}")


if __name__ == "__main__":
    main()

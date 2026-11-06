#!/usr/bin/env python3
"""
Ensure proper nouns are never translated in i18n files.

Reads web/static/js/proper-nouns.json and forces any exact-match key in
web/static/js/i18n-messages.json and web/i18n/en.json to keep its original form.
"""

import json
import os

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


def load_json(path):
    with open(path, "r", encoding="utf-8") as f:
        return json.load(f)


def save_json(path, data):
    with open(path, "w", encoding="utf-8") as f:
        json.dump(data, f, ensure_ascii=False, indent=2)


def protect(path, nouns):
    data = load_json(path)
    updated = 0
    for term in nouns:
        if term in data and data[term] != term:
            data[term] = term
            updated += 1
    save_json(path, data)
    return updated


def main():
    nouns_data = load_json(os.path.join(ROOT, "web/static/js/proper-nouns.json"))
    nouns = set(nouns_data.get("nouns", {}).keys())

    updated_en = protect(os.path.join(ROOT, "web/i18n/en.json"), nouns)
    updated_frontend = protect(os.path.join(ROOT, "web/static/js/i18n-messages.json"), nouns)

    print(f"Protected proper nouns: en.json={updated_en}, i18n-messages.json={updated_frontend}")


if __name__ == "__main__":
    main()

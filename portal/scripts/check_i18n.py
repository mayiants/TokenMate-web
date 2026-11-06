#!/usr/bin/env python3
"""Comprehensive i18n coverage checker.

Scans templates for translation key usage and validates against all i18n JSON files.
Reports file:line locations for every missing key so they can be fixed quickly.
"""
import json, os, re, sys

BASE = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
TEMPLATE_DIR = os.path.join(BASE, "web", "templates")
I18N_DIR = os.path.join(BASE, "web", "i18n")
LAYOUT_HTML = os.path.join(TEMPLATE_DIR, "layout.html")

def load_json(name):
    path = os.path.join(I18N_DIR, name)
    if not os.path.exists(path):
        return {}
    with open(path, "r", encoding="utf-8") as f:
        return json.load(f)

def _unescape_js(s):
    """Unescape JS string literals (\\n, \\t, etc.)."""
    return s.replace('\\n', '\n').replace('\\t', '\t').replace("\\'", "'").replace('\\"', '"')

def collect_refs():
    """Return dict: key -> list of (file, line_no) for each usage type."""
    refs = {"go_template": {}, "js_t": {}, "js_dollar_t": {}}
    patterns = {
        "go_template": re.compile(r'{{t\s+"([^"]+)"|{{t\s+\'([^\']+)\''),
        "js_t": re.compile(r'\b_t\s*\(\s*"([^"]+)"|\b_t\s*\(\s*\'([^\']+)\''),
        "js_dollar_t": re.compile(r'\$t\s*\(\s*"([^"]+)"|\$t\s*\(\s*\'([^\']+)\''),
    }
    for root, _dirs, files in os.walk(TEMPLATE_DIR):
        for fn in files:
            if not fn.endswith(".html"):
                continue
            path = os.path.join(root, fn)
            rel = os.path.relpath(path, BASE)
            with open(path, "r", encoding="utf-8") as f:
                lines = f.readlines()
            for line_no, line in enumerate(lines, 1):
                for kind, pat in patterns.items():
                    for m in pat.finditer(line):
                        key = m.group(1) or m.group(2)
                        if kind != "go_template":
                            key = _unescape_js(key)
                        refs[kind].setdefault(key, []).append((rel, line_no))
    return refs

def collect_layout_i18n_messages():
    keys = set()
    if not os.path.exists(LAYOUT_HTML):
        return keys
    with open(LAYOUT_HTML, "r", encoding="utf-8") as f:
        content = f.read()
    m = re.search(r'window\._i18nMessages\s*=\s*\{([^}]+)\}', content, re.S)
    if m:
        obj_text = m.group(1)
        for km in re.finditer(r'"([^"]+)"\s*:|\'([^\']+)\'\s*:', obj_text):
            keys.add(km.group(1) or km.group(2))
    return keys

def main():
    zh = load_json("zh.json")
    en = load_json("en.json")
    im = load_json("i18n-messages.json")
    all_i18n_keys = set(zh.keys()) | set(en.keys()) | set(im.keys())
    layout_msg_keys = collect_layout_i18n_messages()
    all_known_keys = all_i18n_keys | layout_msg_keys

    refs = collect_refs()
    all_used = set(refs["go_template"]) | set(refs["js_t"]) | set(refs["js_dollar_t"])

    missing_go = {k: v for k, v in refs["go_template"].items() if k not in all_known_keys}
    missing_js_t = {k: v for k, v in refs["js_t"].items() if k not in all_known_keys}
    missing_js_dt = {k: v for k, v in refs["js_dollar_t"].items() if k not in all_known_keys}

    orphaned = (set(zh.keys()) | set(en.keys())) - all_used - layout_msg_keys

    chinese_in_en = []
    for k, v in en.items():
        if re.search(r'[\u4e00-\u9fff]', str(v)):
            chinese_in_en.append(k)

    print("=" * 70)
    print("i18n Coverage Report")
    print("=" * 70)
    print(f"Total unique keys in JSON files (zh + en + i18n-messages): {len(all_i18n_keys)}")
    print(f"Total keys used in templates: {len(all_used)}")
    print(f"  - {{t ...}} keys: {len(refs['go_template'])}")
    print(f"  - _t(...) keys:   {len(refs['js_t'])}")
    print(f"  - $t(...) keys:   {len(refs['js_dollar_t'])}")
    print(f"Keys in layout.html _i18nMessages: {len(layout_msg_keys)}")
    print()

    issues = 0

    def report_missing(title, missing_map):
        nonlocal issues
        if not missing_map:
            return
        issues += len(missing_map)
        print(f"MISSING — {title}: {len(missing_map)} key(s)")
        for key in sorted(missing_map.keys()):
            locs = missing_map[key][:3]
            loc_str = ", ".join(f"{f}:{ln}" for f, ln in locs)
            if len(missing_map[key]) > 3:
                loc_str += f" (+{len(missing_map[key]) - 3} more)"
            # Truncate very long keys for display
            disp = (key[:60] + '...') if len(key) > 60 else key
            print(f"  - '{disp}'  ({loc_str})")
        print()

    report_missing("{{t}} usage not in any i18n source", missing_go)
    report_missing("_t(...) usage not in any i18n source", missing_js_t)
    report_missing("$t(...) usage not in any i18n source", missing_js_dt)

    if chinese_in_en:
        issues += len(chinese_in_en)
        print(f"CHINESE strings in en.json: {len(chinese_in_en)}")
        for k in sorted(chinese_in_en)[:20]:
            print(f"  - {k}: {en[k]}")
        if len(chinese_in_en) > 20:
            print(f"  ... and {len(chinese_in_en) - 20} more")
        print()

    print(f"ORPHANED keys (in zh/en JSON but not in templates/_i18nMessages): {len(orphaned)}")
    for k in sorted(orphaned)[:20]:
        print(f"  - {k}")
    if len(orphaned) > 20:
        print(f"  ... and {len(orphaned) - 20} more")
    print()

    print("=" * 70)
    if issues == 0:
        print("Result: PASSED — no missing i18n keys or Chinese in en.json")
        sys.exit(0)
    else:
        print(f"Result: FAILED — {issues} issue(s) found")
        sys.exit(1)

if __name__ == "__main__":
    main()

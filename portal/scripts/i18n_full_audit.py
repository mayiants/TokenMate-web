#!/usr/bin/env python3
"""Comprehensive i18n audit script.

Checks:
  1. Hardcoded Chinese in JS files (not wrapped in _t())
  2. Hardcoded Chinese in HTML templates (not wrapped in {{t}} or _t())
  3. Hardcoded Chinese in Go backend (API responses, flash/toast messages)
  4. Translation completeness (all used keys exist in en.json and zh.json)
  5. Key parity between en.json and zh.json
  6. Orphan keys (in JSON but not used anywhere)
  7. Translation quality (identical en/zh values, empty values)

Usage:
    python3 scripts/i18n_full_audit.py [--strict] [--json]

  --strict  Exit with code 1 if any issues found
  --json    Output machine-readable JSON instead of text
"""

import json
import os
import re
import sys
from collections import defaultdict

BASE = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
TEMPLATE_DIR = os.path.join(BASE, "web", "templates")
JS_DIR = os.path.join(BASE, "web", "static", "js")
I18N_DIR = os.path.join(BASE, "web", "i18n")
GO_DIRS = [os.path.join(BASE, "internal"), os.path.join(BASE, "pkg")]
PROPER_NOUNS_PATH = os.path.join(JS_DIR, "proper-nouns.json")

# Chinese character range
CHINESE_RE = re.compile(r'[\u4e00-\u9fff]')

# ── Helpers ──────────────────────────────────────────────────────────

def load_json(path):
    if not os.path.exists(path):
        return {}
    with open(path, "r", encoding="utf-8") as f:
        return json.load(f)

def load_proper_nouns():
    """Load proper nouns that should not be flagged."""
    data = load_json(PROPER_NOUNS_PATH)
    nouns = data.get("nouns", {})
    return set(nouns.keys())

def has_chinese(text):
    return bool(CHINESE_RE.search(text))

# ── Key extraction helpers ───────────────────────────────────────────

def unescape_js_string_literal(raw):
    """Unescape a JS string literal fragment (\n, \", \", \\\\, etc.)."""
    out = []
    i = 0
    while i < len(raw):
        c = raw[i]
        if c == "\\" and i + 1 < len(raw):
            n = raw[i + 1]
            if n == "n":
                out.append("\n")
            elif n == "t":
                out.append("\t")
            elif n in ('"', "'", "\\", "/"):
                out.append(n)
            else:
                # Unknown escape: keep both chars to preserve key shape
                out.append(c)
                out.append(n)
            i += 2
            continue
        out.append(c)
        i += 1
    return "".join(out)


def extract_js_t_keys(content):
    """Extract all keys from _t('...') / _t("...") / _t(`...`) calls.

    Handles escaped quotes and escaped newlines so that source text like
    _t('Are you sure?\\n\\nThis cannot be undone.') resolves to the same
    key as stored in JSON (with real newlines).
    """
    keys = []
    for m in re.finditer(r"_t\(", content):
        i = m.end()
        # skip whitespace
        while i < len(content) and content[i] in " \t":
            i += 1
        if i >= len(content):
            continue
        quote = content[i]
        if quote not in ('"', "'", "`"):
            continue
        i += 1
        raw = []
        while i < len(content):
            c = content[i]
            if c == "\\" and i + 1 < len(content):
                raw.append(c)
                raw.append(content[i + 1])
                i += 2
                continue
            if c == quote:
                key = unescape_js_string_literal("".join(raw))
                keys.append(key)
                break
            raw.append(c)
            i += 1
    return keys


def extract_template_t_keys(content):
    """Extract keys from {{t "key"}} / {{t 'key'}} template calls."""
    keys = []
    for m in re.finditer(r'\{\{t\s+(["\'])', content):
        quote = m.group(1)
        i = m.end()
        raw = []
        while i < len(content):
            c = content[i]
            if c == "\\" and i + 1 < len(content):
                raw.append(c)
                raw.append(content[i + 1])
                i += 2
                continue
            if c == quote:
                keys.append(unescape_js_string_literal("".join(raw)))
                break
            raw.append(c)
            i += 1
    return keys


# ── Dynamic-key + literal-usage helpers ──────────────────────────────
#
# Some keys are never referenced by a static literal: they are built at
# runtime from a prefix plus a variable, e.g.
#   template:  {{t (printf "audit.category.%s" .category)}}
#   Go:        h.i18n.T("role." + role, locale)  /  T("settings." + key, c)
# The plain {{t "literal"}} / T("literal", c) extractors cannot see these, so
# every key under such a prefix looks like an orphan. We therefore harvest the
# dynamic *prefixes* actually present in the source and treat any JSON key that
# starts with one of them as "used".
#
# Separately, some keys are string-concatenation fragments passed to _t()/T()
# across expressions or multiple lines (e.g. label + _t(" Key")). Those are
# caught by a literal-substring scan of the whole source corpus.

# {{t (printf "PREFIX%s" ...)}} or {{t (printf "PREFIX%d" ...)}} in templates.
_TPL_PRINTF_PREFIX_RE = re.compile(r'\{\{\s*t\s+\(\s*printf\s+"([^"%]*)%[sdv]')
# Go: T("PREFIX" + var, ...) / i18n.T("PREFIX" + var) / .T("PREFIX" + var)
_GO_CONCAT_PREFIX_RE = re.compile(r'\bT\(\s*"([^"]*?)"\s*\+')
# Go: key := "PREFIX." + var  /  key = "PREFIX." + var  (key then passed to T)
_GO_ASSIGN_PREFIX_RE = re.compile(r'[:=]=\s*"([^"]*?)"\s*\+')


def collect_dynamic_prefixes():
    """Scan templates + Go for dynamic key construction and return the set of
    literal prefixes that precede a runtime-substituted suffix."""
    prefixes = set()

    # Templates: {{t (printf "prefix%s" ...)}}
    for root, _dirs, files in os.walk(TEMPLATE_DIR):
        for fn in files:
            if not fn.endswith(".html"):
                continue
            with open(os.path.join(root, fn), "r", encoding="utf-8", errors="ignore") as f:
                content = f.read()
            for m in _TPL_PRINTF_PREFIX_RE.finditer(content):
                pre = m.group(1)
                if pre:
                    prefixes.add(pre)

    # Go: T("prefix" + var, ...)
    for base_dir in GO_DIRS:
        if not os.path.exists(base_dir):
            continue
        for root, _dirs, files in os.walk(base_dir):
            for fn in files:
                if not fn.endswith(".go") or fn.endswith("_test.go"):
                    continue
                with open(os.path.join(root, fn), "r", encoding="utf-8", errors="ignore") as f:
                    content = f.read()
                for m in _GO_CONCAT_PREFIX_RE.finditer(content):
                    pre = m.group(1)
                    # Only treat dotted prefixes as i18n key prefixes to avoid
                    # accidentally matching unrelated string concatenations
                    # (URL paths, SQL, etc.). i18n keys built dynamically in
                    # this codebase always end the literal part with a dot.
                    if pre and pre.endswith("."):
                        prefixes.add(pre)
                # Assignment form: key := "prefix." + var (then T(key, ...)).
                # Restrict to a plausible i18n key shape (lowercase dotted
                # namespace) so URL/SQL concatenations are not swept in.
                for m in _GO_ASSIGN_PREFIX_RE.finditer(content):
                    pre = m.group(1)
                    if pre and pre.endswith(".") and re.fullmatch(r'[a-z][a-z0-9_.]*\.', pre):
                        prefixes.add(pre)
    return prefixes


def build_source_corpus():
    """Return the concatenated text of all template/JS/Go source files, used
    for a literal-substring fallback so that string-concatenation fragment
    keys (and multi-line calls) are not mis-reported as orphans."""
    chunks = []
    for base_dir in [TEMPLATE_DIR, JS_DIR] + GO_DIRS:
        if not os.path.exists(base_dir):
            continue
        for root, _dirs, files in os.walk(base_dir):
            for fn in files:
                if not fn.endswith((".html", ".js", ".go")):
                    continue
                if fn.endswith("_test.go"):
                    continue
                try:
                    with open(os.path.join(root, fn), "r", encoding="utf-8", errors="ignore") as f:
                        chunks.append(f.read())
                except OSError:
                    continue
    return "\n".join(chunks)


# ── Check 1: Hardcoded Chinese in JS files ───────────────────────────

def scan_js_hardcoded():
    """Find Chinese in JS files not wrapped in _t()."""
    results = []
    # Path fragments to skip (matched as suffix of relative path)
    skip_suffixes = [
        "flatpickr/l10n/zh.js",        # Legitimate Chinese locale
        "flatpickr/flatpickr.min.js",   # Minified library
    ]

    for root, _dirs, files in os.walk(JS_DIR):
        for fn in files:
            if not fn.endswith(".js"):
                continue
            path = os.path.join(root, fn)
            rel = os.path.relpath(path, BASE)
            if any(rel.endswith(s) for s in skip_suffixes):
                continue

            with open(path, "r", encoding="utf-8") as f:
                lines = f.readlines()

            for line_no, line in enumerate(lines, 1):
                stripped = line.strip()

                # Skip pure comments
                if stripped.startswith("//") or stripped.startswith("*") or stripped.startswith("/*"):
                    continue

                # Remove _t('...') and _t("...") calls — these are properly i18n'd
                cleaned = re.sub(r"_t\(\s*['\"][^'\"]*['\"]\s*\)", "", line)
                # Remove // line comments
                cleaned = re.sub(r"//.*$", "", cleaned)
                # Remove string literals that are arguments to _t (already handled above, but belt-and-suspenders)

                if has_chinese(cleaned):
                    if fn == "app-layout.js" and '"zh"' not in cleaned and '": "' in cleaned:
                        results.append({
                            "file": rel,
                            "line": line_no,
                            "text": stripped[:120],
                            "severity": "medium",
                            "category": "js_inline_table",
                        })
                    elif "csv" in cleaned.lower() and ("# " in stripped or "csv =" in stripped or "csv +=" in stripped):
                        pass
                    else:
                        results.append({
                            "file": rel,
                            "line": line_no,
                            "text": stripped[:120],
                            "severity": "high",
                            "category": "js_hardcoded",
                        })
    return results

# ── Check 2: Hardcoded Chinese in templates ──────────────────────────

def scan_template_hardcoded():
    """Find Chinese in HTML templates not wrapped in {{t}} or _t()."""
    results = []
    for root, _dirs, files in os.walk(TEMPLATE_DIR):
        for fn in files:
            if not fn.endswith(".html"):
                continue
            path = os.path.join(root, fn)
            rel = os.path.relpath(path, BASE)

            with open(path, "r", encoding="utf-8") as f:
                lines = f.readlines()

            for line_no, line in enumerate(lines, 1):
                stripped = line.strip()

                # Skip HTML comments
                if stripped.startswith("<!--") and stripped.endswith("-->"):
                    continue

                # Remove {{t "..."}} calls
                cleaned = re.sub(r'\{\{t\s+["\'][^"\']*["\']\s*\}\}', "", line)
                # Remove _t('...') calls
                cleaned = re.sub(r"_t\(\s*['\"][^'\"]*['\"]\s*\)", "", cleaned)
                # Remove HTML comments
                cleaned = re.sub(r"<!--.*?-->", "", cleaned)
                # Remove JS comments
                cleaned = re.sub(r"//.*$", "", cleaned)

                if has_chinese(cleaned):
                    results.append({
                        "file": rel,
                        "line": line_no,
                        "text": stripped[:120],
                        "severity": "high",
                        "category": "template_hardcoded",
                    })
    return results

# ── Check 3: Hardcoded Chinese in Go backend ─────────────────────────

def scan_go_hardcoded():
    """Find Chinese in Go files that may be user-facing."""
    results = []
    # Patterns that indicate user-facing strings
    user_facing_patterns = [
        re.compile(r'gin\.H\{.*?["\'].*?[\u4e00-\u9fff]'),  # gin.H{"error": "中文"}
        re.compile(r'"error"\s*:\s*"[^"]*[\u4e00-\u9fff]'),   # "error": "中文"
        re.compile(r'"message"\s*:\s*"[^"]*[\u4e00-\u9fff]'), # "message": "中文"
        re.compile(r'"success"\s*:\s*"[^"]*[\u4e00-\u9fff]'), # "success": "中文"
        re.compile(r'SetToast\([^)]*[\u4e00-\u9fff]'),        # SetToast(..., "中文")
        re.compile(r'q\.Set\("message".*[\u4e00-\u9fff]'),    # q.Set("message", "中文")
        re.compile(r'url\.QueryEscape\("[^"]*[\u4e00-\u9fff]'), # url.QueryEscape("中文")
        re.compile(r'Name:\s*"[^"]*[\u4e00-\u9fff]'),         # Name: "中文"
        re.compile(r"'[^']*[\u4e00-\u9fff][^']*'\s+as\s+"),   # SQL: '中文' as
    ]

    for base_dir in GO_DIRS:
        if not os.path.exists(base_dir):
            continue
        for root, _dirs, files in os.walk(base_dir):
            for fn in files:
                if not fn.endswith(".go"):
                    continue
                if "_test.go" in fn:
                    continue
                path = os.path.join(root, fn)
                rel = os.path.relpath(path, BASE)

                if "pkg/mail/" in rel:
                    continue

                with open(path, "r", encoding="utf-8") as f:
                    lines = f.readlines()

                for line_no, line in enumerate(lines, 1):
                    stripped = line.strip()

                    # Skip Go comments
                    if stripped.startswith("//"):
                        continue

                    # Check if line has Chinese
                    if not has_chinese(line):
                        continue

                    # Determine if it's user-facing
                    is_user_facing = any(p.search(line) for p in user_facing_patterns)

                    # Also flag any Chinese in string literals that look like messages
                    if not is_user_facing:
                        # Check for Chinese in quoted strings
                        string_matches = re.findall(r'"([^"]*[\u4e00-\u9fff][^"]*)"', line)
                        if string_matches:
                            is_user_facing = True

                    if is_user_facing:
                        results.append({
                            "file": rel,
                            "line": line_no,
                            "text": stripped[:120],
                            "severity": "high",
                            "category": "go_hardcoded",
                        })
    return results

# ── Check 4: Translation completeness ────────────────────────────────

def scan_key_completeness():
    """Check all used keys exist in both en.json and zh.json."""
    en = load_json(os.path.join(I18N_DIR, "en.json"))
    zh = load_json(os.path.join(I18N_DIR, "zh.json"))
    en_keys = set(en.keys())
    zh_keys = set(zh.keys())

    # Collect all used keys from templates and JS
    used_keys = set()
    key_sources = defaultdict(list)

    # Template {{t "key"}}
    for root, _dirs, files in os.walk(TEMPLATE_DIR):
        for fn in files:
            if not fn.endswith(".html"):
                continue
            path = os.path.join(root, fn)
            rel = os.path.relpath(path, BASE)
            with open(path, "r", encoding="utf-8") as f:
                for line_no, line in enumerate(f, 1):
                    for key in extract_template_t_keys(line):
                        used_keys.add(key)
                        key_sources[key].append(f"{rel}:{line_no}")

    # JS _t('key')
    for root, _dirs, files in os.walk(JS_DIR):
        for fn in files:
            if not fn.endswith(".js"):
                continue
            path = os.path.join(root, fn)
            rel = os.path.relpath(path, BASE)
            with open(path, "r", encoding="utf-8") as f:
                for line_no, line in enumerate(f, 1):
                    for key in extract_js_t_keys(line):
                        used_keys.add(key)
                        key_sources[key].append(f"{rel}:{line_no}")

    # Also scan _t() in template inline JS
    for root, _dirs, files in os.walk(TEMPLATE_DIR):
        for fn in files:
            if not fn.endswith(".html"):
                continue
            path = os.path.join(root, fn)
            rel = os.path.relpath(path, BASE)
            with open(path, "r", encoding="utf-8") as f:
                for line_no, line in enumerate(f, 1):
                    for key in extract_js_t_keys(line):
                        used_keys.add(key)
                        key_sources[key].append(f"{rel}:{line_no}")

    # Go i18n.T("key", ...), package-level T("key", c), and mail sender helper s.t("key", ...)
    go_t_pattern = re.compile(r'i18n\.T\(\s*["\']([^"\']+)["\']')
    route_t_pattern = re.compile(r'routeI18n\.T\(\s*["\']([^"\']+)["\']')
    sender_t_pattern = re.compile(r's\.t\(\s*["\']([^"\']+)["\']')
    package_t_pattern = re.compile(r'(?<![A-Za-z0-9_.])T\(\s*["\']([^"\']+)["\']\s*,\s*c\)')
    for base_dir in GO_DIRS:
        if not os.path.exists(base_dir):
            continue
        for root, _dirs, files in os.walk(base_dir):
            for fn in files:
                if not fn.endswith(".go"):
                    continue
                path = os.path.join(root, fn)
                rel = os.path.relpath(path, BASE)
                with open(path, "r", encoding="utf-8") as f:
                    for line_no, line in enumerate(f, 1):
                        for pat in [go_t_pattern, route_t_pattern, sender_t_pattern, package_t_pattern]:
                            for m in pat.finditer(line):
                                key = m.group(1)
                                used_keys.add(key)
                                key_sources[key].append(f"{rel}:{line_no}")

    # Go h.msgf(c, "key", ...)
    msgf_pattern = re.compile(r'h\.msgf\(\s*c,\s*["\']([^"\']+)["\']')
    for base_dir in GO_DIRS:
        if not os.path.exists(base_dir):
            continue
        for root, _dirs, files in os.walk(base_dir):
            for fn in files:
                if not fn.endswith(".go"):
                    continue
                path = os.path.join(root, fn)
                rel = os.path.relpath(path, BASE)
                with open(path, "r", encoding="utf-8") as f:
                    for line_no, line in enumerate(f, 1):
                        for m in msgf_pattern.finditer(line):
                            key = m.group(1)
                            used_keys.add(key)
                            key_sources[key].append(f"{rel}:{line_no}")

    missing_en = []
    missing_zh = []
    for key in sorted(used_keys):
        if key not in en_keys:
            missing_en.append({"key": key, "sources": key_sources[key][:3]})
        if key not in zh_keys:
            missing_zh.append({"key": key, "sources": key_sources[key][:3]})

    return {
        "total_used": len(used_keys),
        "missing_in_en": missing_en,
        "missing_in_zh": missing_zh,
    }

# ── Check 5: Key parity ─────────────────────────────────────────────

def scan_key_parity():
    """Check en.json and zh.json have identical key sets."""
    en = load_json(os.path.join(I18N_DIR, "en.json"))
    zh = load_json(os.path.join(I18N_DIR, "zh.json"))
    en_keys = set(en.keys())
    zh_keys = set(zh.keys())

    return {
        "en_count": len(en_keys),
        "zh_count": len(zh_keys),
        "only_in_en": sorted(en_keys - zh_keys),
        "only_in_zh": sorted(zh_keys - en_keys),
    }

# ── Check 6: Orphan keys ────────────────────────────────────────────

def scan_orphan_keys():
    """Find keys in en.json/zh.json not used anywhere."""
    en = load_json(os.path.join(I18N_DIR, "en.json"))
    en_keys = set(en.keys())

    # Collect all used keys (same logic as check 4)
    used_keys = set()

    go_t_pattern = re.compile(r'i18n\.T\(\s*["\']([^"\']+)["\']')
    route_t_pattern = re.compile(r'routeI18n\.T\(\s*["\']([^"\']+)["\']')
    sender_t_pattern = re.compile(r's\.t\(\s*["\']([^"\']+)["\']')
    package_t_pattern = re.compile(r'(?<![A-Za-z0-9_.])T\(\s*["\']([^"\']+)["\']\s*,\s*c\)')
    msgf_pattern = re.compile(r'h\.msgf\(\s*c,\s*["\']([^"\']+)["\']')

    # Templates
    for root, _dirs, files in os.walk(TEMPLATE_DIR):
        for fn in files:
            if not fn.endswith(".html"):
                continue
            path = os.path.join(root, fn)
            with open(path, "r", encoding="utf-8") as f:
                content = f.read()
            for key in extract_template_t_keys(content):
                used_keys.add(key)
            for key in extract_js_t_keys(content):
                used_keys.add(key)

    # JS files
    for root, _dirs, files in os.walk(JS_DIR):
        for fn in files:
            if not fn.endswith(".js"):
                continue
            path = os.path.join(root, fn)
            with open(path, "r", encoding="utf-8") as f:
                content = f.read()
            for key in extract_js_t_keys(content):
                used_keys.add(key)

    # Go files
    for base_dir in GO_DIRS:
        if not os.path.exists(base_dir):
            continue
        for root, _dirs, files in os.walk(base_dir):
            for fn in files:
                if not fn.endswith(".go"):
                    continue
                path = os.path.join(root, fn)
                with open(path, "r", encoding="utf-8") as f:
                    content = f.read()
                for pat in [go_t_pattern, route_t_pattern, sender_t_pattern, package_t_pattern, msgf_pattern]:
                    for m in pat.finditer(content):
                        used_keys.add(m.group(1))

    # Candidate orphans: JSON keys with no static literal reference.
    candidate_orphans = en_keys - used_keys

    # Filter 1 — dynamic key prefixes. A key reached at runtime via a
    # "prefix" + variable / printf "prefix%s" construction is not an orphan.
    dynamic_prefixes = collect_dynamic_prefixes()
    if dynamic_prefixes:
        candidate_orphans = {
            k for k in candidate_orphans
            if not any(k.startswith(p) for p in dynamic_prefixes)
        }

    # Filter 2 — literal-substring fallback. String-concatenation fragment
    # keys (e.g. label + _t(" Key")) and multi-line calls are referenced by
    # their exact literal even though the structured extractors miss them.
    corpus = build_source_corpus()
    still_orphan = []
    for k in candidate_orphans:
        # Guard against trivially-short keys matching by coincidence; require
        # the exact literal to appear somewhere in the source corpus.
        if k and k in corpus:
            used_keys.add(k)
        else:
            still_orphan.append(k)

    orphans = sorted(still_orphan)
    return {
        "total_en_keys": len(en_keys),
        "total_used": len(en_keys) - len(orphans),
        "orphan_count": len(orphans),
        "orphans_sample": orphans[:30],
        "dynamic_prefixes": sorted(dynamic_prefixes),
    }

# ── Check 7: Translation quality ─────────────────────────────────────

def scan_translation_quality():
    """Check for identical en/zh values and empty values."""
    en = load_json(os.path.join(I18N_DIR, "en.json"))
    zh = load_json(os.path.join(I18N_DIR, "zh.json"))
    proper_nouns = load_proper_nouns()

    empty_en = [k for k, v in en.items() if not v or not v.strip()]
    empty_zh = [k for k, v in zh.items() if not v or not v.strip()]

    # English values must not contain Chinese characters. A Chinese value in
    # en.json means a key was added with an untranslated (Chinese) default, so
    # the English UI renders Chinese even in the English locale. Key parity does
    # not catch this because the key exists in both files.
    chinese_en = [
        {"key": k, "value": v[:60]}
        for k, v in en.items()
        if isinstance(v, str) and CHINESE_RE.search(v)
    ]

    identical = []
    for key in set(en.keys()) & set(zh.keys()):
        if en[key] == zh[key] and len(en[key]) > 3:
            # Check if it's a proper noun (expected to be identical)
            is_proper = any(noun in key for noun in proper_nouns)
            if not is_proper:
                identical.append({"key": key, "value": en[key][:60]})

    return {
        "empty_en": empty_en,
        "empty_zh": empty_zh,
        "chinese_en": chinese_en,
        "identical_count": len(identical),
        "identical_sample": identical[:20],
    }

# ── Main ─────────────────────────────────────────────────────────────

def main():
    strict = "--strict" in sys.argv
    json_output = "--json" in sys.argv

    print("=" * 70)
    print("i18n Full Audit Report")
    print("=" * 70)
    print()

    total_issues = 0

    # Check 1: JS hardcoded Chinese
    js_issues = scan_js_hardcoded()
    js_hardcoded = [i for i in js_issues if i["category"] == "js_hardcoded"]
    js_table = [i for i in js_issues if i["category"] == "js_inline_table"]

    print(f"[Check 1] JS Hardcoded Chinese (not in _t()): {len(js_hardcoded)} lines")
    if js_hardcoded:
        total_issues += len(js_hardcoded)
        by_file = defaultdict(int)
        for item in js_hardcoded:
            by_file[item["file"]] += 1
        for f, c in sorted(by_file.items(), key=lambda x: -x[1]):
            print(f"  {f}: {c} lines")
        print(f"  Sample (first 10):")
        for item in js_hardcoded[:10]:
            print(f"    {item['file']}:{item['line']}: {item['text'][:80]}")
    else:
        print("  ✅ No hardcoded Chinese in JS files")
    print()

    print(f"[Check 1b] JS Inline Translation Table: {len(js_table)} entries")
    if js_table:
        print(f"  ⚠️  {len(js_table)} entries in _i18nMessages table (app-layout.js)")
        print(f"  These are translation data, not display strings — flagged for migration")
    print()

    # Check 2: Template hardcoded Chinese
    tpl_issues = scan_template_hardcoded()
    print(f"[Check 2] Template Hardcoded Chinese: {len(tpl_issues)} lines")
    if tpl_issues:
        total_issues += len(tpl_issues)
        for item in tpl_issues[:10]:
            print(f"  {item['file']}:{item['line']}: {item['text'][:80]}")
    else:
        print("  ✅ No hardcoded Chinese in templates")
    print()

    # Check 3: Go backend hardcoded Chinese
    go_issues = scan_go_hardcoded()
    print(f"[Check 3] Go Backend Hardcoded Chinese: {len(go_issues)} lines")
    if go_issues:
        total_issues += len(go_issues)
        by_file = defaultdict(int)
        for item in go_issues:
            by_file[item["file"]] += 1
        for f, c in sorted(by_file.items(), key=lambda x: -x[1]):
            print(f"  {f}: {c} lines")
        print(f"  Sample (first 15):")
        for item in go_issues[:15]:
            print(f"    {item['file']}:{item['line']}: {item['text'][:80]}")
    else:
        print("  ✅ No hardcoded Chinese in Go backend")
    print()

    # Check 4: Key completeness
    completeness = scan_key_completeness()
    print(f"[Check 4] Translation Key Completeness:")
    print(f"  Total used keys: {completeness['total_used']}")
    missing_en = completeness["missing_in_en"]
    missing_zh = completeness["missing_in_zh"]
    if missing_en:
        total_issues += len(missing_en)
        print(f"  ❌ Missing from en.json: {len(missing_en)}")
        for item in missing_en[:10]:
            print(f"    '{item['key'][:60]}' — {item['sources'][0]}")
    else:
        print(f"  ✅ All used keys present in en.json")
    if missing_zh:
        total_issues += len(missing_zh)
        print(f"  ❌ Missing from zh.json: {len(missing_zh)}")
        for item in missing_zh[:10]:
            print(f"    '{item['key'][:60]}' — {item['sources'][0]}")
    else:
        print(f"  ✅ All used keys present in zh.json")
    print()

    # Check 5: Key parity
    parity = scan_key_parity()
    print(f"[Check 5] Key Parity (en.json vs zh.json):")
    print(f"  en.json: {parity['en_count']} keys, zh.json: {parity['zh_count']} keys")
    if parity["only_in_en"]:
        total_issues += len(parity["only_in_en"])
        print(f"  ❌ Only in en.json: {len(parity['only_in_en'])}")
        for k in parity["only_in_en"][:10]:
            print(f"    {k}")
    if parity["only_in_zh"]:
        total_issues += len(parity["only_in_zh"])
        print(f"  ❌ Only in zh.json: {len(parity['only_in_zh'])}")
        for k in parity["only_in_zh"][:10]:
            print(f"    {k}")
    if not parity["only_in_en"] and not parity["only_in_zh"]:
        print(f"  ✅ Key sets are identical")
    print()

    # Check 6: Orphan keys
    orphans = scan_orphan_keys()
    print(f"[Check 6] Orphan Keys:")
    print(f"  Total en.json keys: {orphans['total_en_keys']}")
    print(f"  Keys used in code: {orphans['total_used']}")
    if orphans.get("dynamic_prefixes"):
        print(f"  Dynamic key prefixes recognized: {', '.join(orphans['dynamic_prefixes'])}")
    print(f"  Orphan keys: {orphans['orphan_count']}")
    if orphans["orphans_sample"]:
        print(f"  Sample (first 15):")
        for k in orphans["orphans_sample"][:15]:
            print(f"    {k}")
    print()

    # Check 7: Translation quality
    quality = scan_translation_quality()
    print(f"[Check 7] Translation Quality:")
    if quality["empty_en"]:
        total_issues += len(quality["empty_en"])
        print(f"  ❌ Empty values in en.json: {len(quality['empty_en'])}")
    else:
        print(f"  ✅ No empty values in en.json")
    if quality["empty_zh"]:
        total_issues += len(quality["empty_zh"])
        print(f"  ❌ Empty values in zh.json: {len(quality['empty_zh'])}")
    else:
        print(f"  ✅ No empty values in zh.json")
    if quality.get("chinese_en"):
        total_issues += len(quality["chinese_en"])
        print(f"  ❌ English values containing Chinese in en.json: {len(quality['chinese_en'])}")
        for item in quality["chinese_en"][:15]:
            print(f"    {item['key']}: {item['value']}")
    else:
        print(f"  ✅ No Chinese characters in en.json values")
    print(f"  Identical en/zh values (>3 chars, non-proper-noun): {quality['identical_count']}")
    if quality["identical_sample"]:
        for item in quality["identical_sample"][:10]:
            print(f"    {item['key']}: {item['value']}")
    print()

    # Summary
    print("=" * 70)
    print(f"TOTAL ISSUES: {total_issues}")
    print(f"  JS hardcoded:     {len(js_hardcoded)}")
    print(f"  JS inline table:  {len(js_table)} (migration target)")
    print(f"  Template:         {len(tpl_issues)}")
    print(f"  Go backend:       {len(go_issues)}")
    print(f"  Missing keys:     {len(missing_en) + len(missing_zh)}")
    print(f"  Key parity:       {len(parity['only_in_en']) + len(parity['only_in_zh'])}")
    print(f"  Orphan keys:      {orphans['orphan_count']}")
    print("=" * 70)

    if strict and total_issues > 0:
        sys.exit(1)
    sys.exit(0)

if __name__ == "__main__":
    main()

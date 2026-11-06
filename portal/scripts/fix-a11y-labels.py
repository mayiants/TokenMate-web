#!/usr/bin/env python3
"""
Batch fix axe-core select-name and label violations in HTML templates.

Adds aria-label attributes to <select> and <input> elements that lack an
accessible name (no aria-label, no aria-labelledby, no explicit <label>,
no placeholder, no title).

Run from repo root:
    python3 scripts/fix-a11y-labels.py
"""

import re
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
TEMPLATES_DIR = ROOT / "web" / "templates"

# i18n keys that already exist in en.json (verified). Use {{t "..."}} for these.
KNOWN_KEYS = {
    "Filter": "Filter",
    "Filter by status": "Filter by status",
    "Filter by type": "Filter by type",
    "Filter by provider": "Filter by provider",
    "Filter by role": "Filter by role",
    "Filter by model": "Filter by model",
    "Filter by category": "Filter by category",
    "Filter by level": "Filter by level",
    "Period": "Period",
    "Status": "Status",
    "Type": "Type",
    "Provider": "Provider",
    "Group Type": "Group Type",
    "Billing Mode": "Billing Mode",
    "Routing Track": "Routing Track",
    "Billing Cycle": "Billing Cycle",
    "Display Currency": "Display Currency",
    "Tier": "Tier",
    "Plan Type": "Plan Type",
    "Voucher Type": "Voucher Type",
    "Page Size": "Page Size",
    "Sort": "Sort",
    "Health": "Health",
    "Duration": "Duration",
    "Name": "Name",
    "Description": "Description",
    "Expires": "Expires",
    "Count": "Count",
    "Max Uses": "Max Uses",
    "Pricing Ratio": "Pricing Ratio",
    "Quota Total": "Quota Total",
    "Credit Total": "Credit Total",
    "Max Members": "Max Members",
    "Max Admins": "Max Admins",
    "Daily Limit": "Daily Limit",
    "Concurrent Limit": "Concurrent Limit",
    "RPM Limit": "RPM Limit",
    "TPM Limit": "TPM Limit",
    "RPH Limit": "RPH Limit",
    "RP5H Limit": "RP5H Limit",
    "RPD Limit": "RPD Limit",
    "RPW Limit": "RPW Limit",
    "TPH Limit": "TPH Limit",
    "TPD Limit": "TPD Limit",
    "TPW Limit": "TPW Limit",
    "Duration Days": "Duration Days",
    "Select All": "Select All",
    "Select Row": "Select Row",
    "Key Name": "Key Name",
}

# id/name suffix/prefix -> (label text, use_i18n_key_if_exists)
SELECT_LABELS = {
    "status-filter": "Filter by status",
    "type-filter": "Filter by type",
    "provider-filter": "Filter by provider",
    "category-filter": "Filter by category",
    "level-filter": "Filter by level",
    "vendor-filter": "Filter by provider",
    "codes-limit": "Page Size",
    "keyperiod": "Period",
    "period": "Period",
    "status": "Status",
    "type": "Type",
    "provider": "Provider",
    "group_type": "Group Type",
    "billing_mode": "Billing Mode",
    "routing_track": "Routing Track",
    "billing_cycle": "Billing Cycle",
    "display_currency": "Display Currency",
    "tier": "Tier",
    "plan_type": "Plan Type",
    "import_mode": "Import Mode",
    "import-provider": "Provider",
    "single_provider": "Provider",
    "edit-status": "Status",
    "edit-group-type": "Group Type",
    "edit-billing-mode": "Billing Mode",
    "edit-routing-track": "Routing Track",
    "edit-billing_cycle": "Billing Cycle",
    "edit-display_currency": "Display Currency",
    "edit-tier": "Tier",
    "edit-provider": "Provider",
    "aws-edit-status": "Status",
    "aws-provider-select": "Provider",
    "secondary-filter": "Filter",
    "providerfilter": "Filter by provider",
    "statusfilter": "Filter by status",
    "healthfilter": "Filter by health",
    "sortfilter": "Sort",
}

INPUT_LABELS = {
    "pricing_ratio": "Pricing Ratio",
    "edit-name": "Name",
    "edit-pricing-ratio": "Pricing Ratio",
    "quota_total": "Quota Total",
    "credit_total": "Credit Total",
    "max_members": "Max Members",
    "max_admins": "Max Admins",
    "edit-quota_total": "Quota Total",
    "edit-credit_total": "Credit Total",
    "edit-max_members": "Max Members",
    "edit-max_admins": "Max Admins",
    "keys_file": "Key File",
    "duration_days": "Duration Days",
    "daily_limit": "Daily Limit",
    "tph_limit": "TPH Limit",
    "tpd_limit": "TPD Limit",
    "tpw_limit": "TPW Limit",
    "concurrent_limit": "Concurrent Limit",
    "rpm_limit": "RPM Limit",
    "tpm_limit": "TPM Limit",
    "rph_limit": "RPH Limit",
    "rp5h_limit": "RP5H Limit",
    "rpd_limit": "RPD Limit",
    "rpw_limit": "RPW Limit",
    "create-count": "Count",
    "create-max-uses": "Max Uses",
    "create-expires": "Expires",
    "edit-description": "Description",
    "edit-expires": "Expires",
    "aws-confirm-name": "Model Name",
    "aws-edit-name": "Model Name",
    "aws-edit-input": "Input Cost",
    "aws-edit-output": "Output Cost",
    "chart-month": "Month",
    "select-all": "Select All",
    "row-checkbox": "Select Row",
    "group-rpm": "RPM Limit",
    "edit-key-name": "Key Name",
}


def has_accessible_name(tag_html: str, file_text: str) -> bool:
    """Check if element already has an accessible name."""
    if re.search(r'\baria-label\s*=\s*"[^"]*"', tag_html):
        # non-empty aria-label
        m = re.search(r'\baria-label\s*=\s*"([^"]*)"', tag_html)
        if m and m.group(1).strip():
            return True
    if 'aria-labelledby=' in tag_html:
        return True
    if re.search(r'\btitle\s*=\s*"[^"]+"', tag_html):
        return True
    if re.search(r'\bplaceholder\s*=\s*"[^"]+"', tag_html):
        return True
    # explicit label by id
    m = re.search(r'\bid\s*=\s*"([^"]+)"', tag_html)
    if m:
        el_id = m.group(1)
        if re.search(rf'<label[^>]*\bfor\s*=\s*"{re.escape(el_id)}"', file_text, re.IGNORECASE):
            return True
    # implicit label (element wrapped in <label>)
    return False


def normalize_key(key: str) -> str:
    return key.lower().replace('-', '').replace('_', '')


def lookup_label(mapping: dict, identifier: str) -> str | None:
    if not identifier:
        return None
    identifier_norm = normalize_key(identifier)
    # exact match first
    if identifier in mapping:
        return mapping[identifier]
    # normalized match
    for k, v in mapping.items():
        if normalize_key(k) == identifier_norm:
            return v
    return None


def make_aria_label(label_text: str) -> str:
    if label_text in KNOWN_KEYS:
        return f'{{{{t "{label_text}"}}}}'
    return label_text


def fix_selects(text: str) -> tuple[str, int]:
    count = 0

    def repl(m: re.Match) -> str:
        nonlocal count
        tag = m.group(0)
        if has_accessible_name(tag, text):
            return tag
        # Determine identifier
        id_m = re.search(r'\bid\s*=\s*"([^"]+)"', tag)
        name_m = re.search(r'\bname\s*=\s*"([^"]+)"', tag)
        identifier = id_m.group(1) if id_m else (name_m.group(1) if name_m else None)
        label = lookup_label(SELECT_LABELS, identifier) if identifier else None
        if not label:
            # generic filter fallback
            if identifier and ('filter' in identifier.lower() or identifier.lower().endswith('filter')):
                label = "Filter"
            elif identifier and identifier.lower().endswith('period'):
                label = "Period"
            elif identifier and identifier.lower().endswith('status'):
                label = "Status"
            elif identifier and identifier.lower().endswith('type'):
                label = "Type"
            elif identifier:
                label = identifier.replace('-', ' ').replace('_', ' ').title()
            else:
                label = "Select"
        aria = make_aria_label(label)
        count += 1
        # insert aria-label before the closing >
        return tag[:-1] + f' aria-label="{aria}"' + tag[-1:]

    # Match <select ...> but avoid those inside comments or already handled
    new_text = re.sub(r'<select\b[^>]*>', repl, text)
    return new_text, count


def fix_inputs(text: str) -> tuple[str, int]:
    count = 0

    def repl(m: re.Match) -> str:
        nonlocal count
        tag = m.group(0)
        # Skip hidden, submit, button, image inputs
        type_m = re.search(r'\btype\s*=\s*"([^"]+)"', tag)
        input_type = type_m.group(1).lower() if type_m else "text"
        if input_type in ("hidden", "submit", "button", "image", "reset"):
            return tag
        if has_accessible_name(tag, text):
            return tag
        id_m = re.search(r'\bid\s*=\s*"([^"]+)"', tag)
        name_m = re.search(r'\bname\s*=\s*"([^"]+)"', tag)
        identifier = id_m.group(1) if id_m else (name_m.group(1) if name_m else None)
        label = lookup_label(INPUT_LABELS, identifier) if identifier else None
        if not label:
            if identifier:
                label = identifier.replace('-', ' ').replace('_', ' ').title()
            else:
                label = "Input"
        aria = make_aria_label(label)
        count += 1
        return tag[:-1] + f' aria-label="{aria}"' + tag[-1:]

    new_text = re.sub(r'<input\b[^>]*>', repl, text)
    return new_text, count


def main():
    files = sorted(TEMPLATES_DIR.rglob("*.html"))
    total_selects = 0
    total_inputs = 0
    changed_files = []

    for path in files:
        text = path.read_text(encoding="utf-8")
        new_text, s_count = fix_selects(text)
        new_text, i_count = fix_inputs(new_text)
        if s_count or i_count:
            path.write_text(new_text, encoding="utf-8")
            total_selects += s_count
            total_inputs += i_count
            changed_files.append((path.relative_to(ROOT), s_count, i_count))

    print(f"Updated {len(changed_files)} file(s)")
    print(f"  <select> aria-label added: {total_selects}")
    print(f"  <input> aria-label added:  {total_inputs}")
    for rel, s, i in changed_files:
        print(f"  {rel}: +{s} selects, +{i} inputs")


if __name__ == "__main__":
    main()

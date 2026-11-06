#!/usr/bin/env python3
"""
scan-field-alignment.py

Scan frontend HTML forms / JS submit functions and backend Go handler request
structs for field mismatches. Reports fields present in the UI but missing from
the backend (and vice versa), which often cause silent data loss bugs like
pricing_ratio being ignored on enterprise creation.

The scanner supports two frontend extraction modes:
1. HTML form: extract all `name="..."` fields inside `<form id="...">`.
2. JS submit function: extract object keys from the request body defined in the
   named submit function (for drawers built purely in JS).

Usage:
    python3 scripts/scan-field-alignment.py

Exit code:
    0 if no mismatches found, 1 otherwise.
"""

import re
import sys
from dataclasses import dataclass
from pathlib import Path
from typing import List, Optional, Set, Tuple

# ---------------------------------------------------------------------------
# Configuration
# ---------------------------------------------------------------------------

REPO_ROOT = Path(__file__).resolve().parent.parent

# Fields that are expected to exist only on the frontend and should not be
# reported as missing in the backend.
FRONTEND_ONLY_FIELDS = {"csrf_token", "id", "model_id"}

# Fields that are expected to exist only on the backend and should not be
# reported as missing in the frontend. These are intentional server-side fields
# (optional defaults, computed values, or fields managed by separate flows).
BACKEND_ONLY_FIELDS: Set[str] = {
    # Groups / group-edit: models and perms are managed on the dedicated
    # group-edit page; rate_limit_rpm is an optional top-level default.
    "model_permissions",
    "models",
    "rate_limit_rpm",
    # Keypool: notes/models are optional; enabled/key edits are handled by
    # separate toggle / create flows.
    "notes",
    "enabled",
    "key",
    # Vouchers: reward_type defaults server-side; plan_id is sent conditionally.
    "reward_type",
    # API keys: rate limits and allowed_models are not exposed in the create drawer.
    "access_mode",
    "allowed_models",
    "rate_limit_rpm",
    "rate_limit_tpm",
    # Subscription plans: optional marketing/permission fields not in the drawer.
    "description",
    "features",
    "model_permissions",
    # Status is toggled through a dedicated endpoint, not the edit drawer.
    "status",
    # Provider sync_source is defaulted to "manual" server-side.
    "sync_source",
}


@dataclass
class FormScan:
    name: str
    endpoint: str
    handler_file: str
    handler_func: str
    # Frontend source (provide one of the two)
    form_id: Optional[str] = None
    submit_func: Optional[str] = None
    # Optional associated JS file (e.g. page-admin-*.js) to also scan
    js_file: Optional[str] = None
    # Extra fields that the static scanner cannot discover (e.g. fields added in
    # JS loops). They are unioned with extracted frontend fields.
    extra_frontend_fields: Optional[Set[str]] = None


@dataclass
class PageScan:
    page: str
    template: str
    forms: List[FormScan]


# Mapping of the main CRUD pages to inspect. Extend as pages are added/refactored.
SCANS: List[PageScan] = [
    PageScan(
        page="/admin/enterprise",
        template="web/templates/admin-enterprise.html",
        forms=[
            FormScan(
                name="create",
                form_id="create-form",
                endpoint="POST /api/admin/enterprise",
                handler_file="internal/routes/enterprise_common.go",
                handler_func="createEnterprise",
            ),
            FormScan(
                name="edit",
                form_id="edit-form",
                endpoint="PATCH /api/admin/enterprise/:id",
                handler_file="internal/routes/enterprise_common.go",
                handler_func="updateEnterprise",
            ),
        ],
    ),
    PageScan(
        page="/admin/groups",
        template="web/templates/admin-groups.html",
        forms=[
            FormScan(
                name="create",
                form_id="create-form",
                endpoint="POST /api/admin/groups",
                handler_file="internal/routes/groups.go",
                handler_func="createGroup",
            ),
            FormScan(
                name="edit",
                submit_func="submitEditGroup",
                endpoint="PATCH /api/admin/groups/:id",
                handler_file="internal/routes/groups.go",
                handler_func="updateGroup",
                extra_frontend_fields={
                    "rpm", "rph", "rp5h", "rpw", "rpmonth",
                    "tpm", "tph", "tpd", "tpw", "tpmonth",
                },
            ),
        ],
    ),
    PageScan(
        page="/admin/group-edit",
        template="web/templates/admin-group-edit.html",
        forms=[
            FormScan(
                name="create/edit",
                form_id="group-form",
                endpoint="POST/PUT /api/admin/groups",
                handler_file="internal/routes/groups.go",
                handler_func="updateGroup",
            ),
        ],
    ),
    PageScan(
        page="/admin/subscriptions",
        template="web/templates/admin-subscriptions.html",
        forms=[
            FormScan(
                name="create",
                submit_func="submitCreatePlan",
                endpoint="POST /api/admin/subscriptions/plans",
                handler_file="internal/routes/subscriptions_plans.go",
                handler_func="createPlan",
            ),
            FormScan(
                name="edit",
                submit_func="submitEditForm",
                endpoint="PUT /api/admin/subscriptions/plans/:id",
                handler_file="internal/routes/subscriptions_plans.go",
                handler_func="updatePlan",
            ),
        ],
    ),
    PageScan(
        page="/admin/vouchers",
        template="web/templates/admin-vouchers.html",
        forms=[
            FormScan(
                name="create",
                submit_func="submitCreate",
                endpoint="POST /api/admin/vouchers",
                handler_file="internal/routes/vouchers_admin.go",
                handler_func="create",
            ),
            FormScan(
                name="edit",
                submit_func="submitEdit",
                endpoint="PATCH /api/admin/vouchers/:id",
                handler_file="internal/routes/vouchers_admin.go",
                handler_func="update",
            ),
            FormScan(
                name="welcome",
                submit_func="saveWelcome",
                endpoint="POST /api/admin/vouchers/welcome",
                handler_file="internal/routes/vouchers_admin.go",
                handler_func="saveWelcome",
            ),
        ],
    ),
    PageScan(
        page="/admin/keypool",
        template="web/templates/admin-keypool.html",
        forms=[
            FormScan(
                name="create",
                submit_func="submitAddKey",
                endpoint="POST /api/admin/keypool",
                handler_file="internal/routes/admin_keypool.go",
                handler_func="addKeyPoolKey",
            ),
            FormScan(
                name="edit",
                submit_func="submitEditKey",
                endpoint="PATCH /api/admin/keypool/:id",
                handler_file="internal/routes/admin_keypool.go",
                handler_func="updateKeyPoolKey",
            ),
        ],
    ),
    PageScan(
        page="/admin/providers",
        template="web/templates/admin-providers.html",
        forms=[
            FormScan(
                name="create",
                submit_func="submitAddProvider",
                endpoint="POST /api/admin/providers",
                handler_file="internal/routes/admin_providers.go",
                handler_func="createProvider",
            ),
            FormScan(
                name="edit",
                submit_func="submitEditProvider",
                endpoint="PATCH /api/admin/providers/:id",
                handler_file="internal/routes/admin_providers.go",
                handler_func="updateProvider",
            ),
        ],
    ),
    PageScan(
        page="/admin/models",
        template="web/templates/admin-models-general.html",
        forms=[
            FormScan(
                name="create",
                submit_func="submitAddModel",
                endpoint="POST /api/admin/models",
                handler_file="internal/routes/models_crud.go",
                handler_func="createServiceModel",
            ),
        ],
    ),
    PageScan(
        page="/apikeys",
        template="web/templates/apikeys.html",
        forms=[
            FormScan(
                name="create",
                submit_func="submitCreateKey",
                endpoint="POST /api/keys",
                handler_file="internal/routes/apikeys_crud.go",
                handler_func="createKey",
            ),
        ],
    ),
]


# ---------------------------------------------------------------------------
# HTML form field extraction
# ---------------------------------------------------------------------------

FORM_TAG_RE = re.compile(r"<form\b[^>]*?\bid=['\"](?P<id>[^'\"]+)['\"][^>]*>", re.IGNORECASE)
FIELD_RE = re.compile(
    r"<(?:input|select|textarea)\b[^>]*?\bname=['\"](?P<name>[^'\"]+)['\"]",
    re.IGNORECASE | re.DOTALL,
)


def extract_form_fields(template_text: str, form_id: str) -> Set[str]:
    """Extract all `name="..."` fields inside a specific <form id=...>."""
    start_match = None
    for m in FORM_TAG_RE.finditer(template_text):
        if m.group("id") == form_id:
            start_match = m
            break

    if not start_match:
        return set()

    start = start_match.end()

    # Naive brace matching for nested forms
    depth = 1
    pos = start
    while depth > 0 and pos < len(template_text):
        next_open = template_text.find("<form", pos)
        next_close = template_text.find("</form>", pos)

        if next_close == -1:
            break
        if next_open != -1 and next_open < next_close:
            depth += 1
            pos = next_open + len("<form")
        else:
            depth -= 1
            pos = next_close + len("</form>")

    form_text = template_text[start:pos]
    return {m.group("name") for m in FIELD_RE.finditer(form_text)}


# ---------------------------------------------------------------------------
# JS submit function body extraction
# ---------------------------------------------------------------------------

JS_OBJECT_KEY_RE = re.compile(
    r"(?<![.\w])(?P<key>[a-zA-Z_][a-zA-Z0-9_]*)\s*:\s*",
    re.DOTALL,
)


def find_js_function_body(text: str, func_name: str) -> str:
    """Find a JS function body by name (supports `function name()` and `name = function()`)."""
    patterns = [
        rf"function\s+{re.escape(func_name)}\s*\([^)]*\)\s*\{{",
        rf"{re.escape(func_name)}\s*=\s*function\s*\([^)]*\)\s*\{{",
        rf"{re.escape(func_name)}\s*:\s*function\s*\([^)]*\)\s*\{{",
        rf"{re.escape(func_name)}\s*\([^)]*\)\s*\{{",  # arrow/method shorthand fallback
    ]

    start = -1
    for pat in patterns:
        m = re.search(pat, text)
        if m:
            start = m.end()
            break

    if start == -1:
        return ""

    depth = 1
    pos = start
    while depth > 0 and pos < len(text):
        if text[pos] == "{":
            depth += 1
        elif text[pos] == "}":
            depth -= 1
        pos += 1

    return text[start : pos - 1]


def _extract_object_keys_from_braces(text: str, start_pos: int) -> Set[str]:
    """Given the position of an opening '{', extract keys from the object."""
    depth = 1
    pos = start_pos + 1
    while pos < len(text) and depth > 0:
        if text[pos] == "{":
            depth += 1
        elif text[pos] == "}":
            depth -= 1
        pos += 1
    if depth != 0:
        return set()
    obj_body = text[start_pos + 1 : pos - 1]
    return set(JS_OBJECT_KEY_RE.findall(obj_body))


def extract_body_object_keys(func_body: str, endpoint: str) -> Set[str]:
    """
    Extract object keys from a request body inside a JS function.
    Looks for patterns like:
        body = { name: ..., type: ... }
        data = { name: ..., type: ... }
        JSON.stringify({ name: ..., type: ... })
        fetch(url, { method: 'POST', body: { name: ..., type: ... } })
        api(url, { method: 'POST', body: { name: ..., type: ... } })
    """
    keys: Set[str] = set()

    # body = { ... }  /  data = { ... }  /  var body = { ... }
    for var_name in ("body", "data"):
        pattern = re.compile(
            rf"\b{var_name}\s*=\s*\{{",
            re.DOTALL | re.IGNORECASE,
        )
        for m in pattern.finditer(func_body):
            keys.update(_extract_object_keys_from_braces(func_body, m.end() - 1))

    # JSON.stringify({ ... })
    pattern = re.compile(
        r"JSON\.stringify\s*\(\s*\{",
        re.DOTALL,
    )
    for m in pattern.finditer(func_body):
        keys.update(_extract_object_keys_from_braces(func_body, m.end() - 1))

    # body: { ... } inside fetch/api options object
    pattern = re.compile(
        r"\bbody\s*:\s*\{",
        re.DOTALL | re.IGNORECASE,
    )
    for m in pattern.finditer(func_body):
        keys.update(_extract_object_keys_from_braces(func_body, m.end() - 1))

    # Dynamic assignments such as `body.plan_id = planId` or `data.models = selected`
    assign_pattern = re.compile(
        r"\b(body|data|req)\.(?P<key>[a-zA-Z_][a-zA-Z0-9_]*)\s*=",
        re.DOTALL | re.IGNORECASE,
    )
    for m in assign_pattern.finditer(func_body):
        keys.add(m.group("key"))

    return keys


def extract_js_submit_fields(text: str, func_name: str) -> Set[str]:
    """Extract request body field names from a JS submit function."""
    func_body = find_js_function_body(text, func_name)
    if not func_body:
        return set()
    return extract_body_object_keys(func_body, "")


# ---------------------------------------------------------------------------
# Go handler request struct extraction
# ---------------------------------------------------------------------------

JSON_TAG_RE = re.compile(r'\bjson:\s*"([^"]+)"')


def extract_handler_struct_fields(go_path: Path, func_name: str) -> Set[str]:
    """
    Find the request struct inside the named Go function and return the JSON tag
    field names. Supports both `var req struct { ... }` and
    `req := struct { ... }{ ... }` patterns.
    """
    text = go_path.read_text(encoding="utf-8")

    # Locate function start (supports both method receivers and plain funcs)
    func_pattern = re.compile(rf"func \(.*\) {re.escape(func_name)}\(.*\) \{{")
    func_match = func_pattern.search(text)
    if not func_match:
        return set()

    start = func_match.end()
    depth = 1
    pos = start
    while depth > 0 and pos < len(text):
        if text[pos] == "{":
            depth += 1
        elif text[pos] == "}":
            depth -= 1
        pos += 1
    func_body = text[start:pos]

    # Try `var req struct {` first, then `req := struct {`
    struct_start_match = re.search(r"var\s+req\s+struct\s*\{", func_body)
    if not struct_start_match:
        struct_start_match = re.search(r"req\s*:=\s*struct\s*\{", func_body)
    if not struct_start_match:
        return set()

    start = struct_start_match.end()
    depth = 1
    pos = start
    while depth > 0 and pos < len(func_body):
        if func_body[pos] == "{":
            depth += 1
        elif func_body[pos] == "}":
            depth -= 1
        pos += 1
    struct_body = func_body[start : pos - 1]

    fields = set()
    for tag in JSON_TAG_RE.findall(struct_body):
        field_name = tag.split(",")[0].strip()
        if field_name:
            fields.add(field_name)

    return fields


# ---------------------------------------------------------------------------
# Reporting
# ---------------------------------------------------------------------------

@dataclass
class Mismatch:
    page: str
    form: str
    endpoint: str
    frontend_fields: Set[str]
    backend_fields: Set[str]
    frontend_source: str

    @property
    def missing_in_backend(self) -> Set[str]:
        return (self.frontend_fields - self.backend_fields) - FRONTEND_ONLY_FIELDS

    @property
    def missing_in_frontend(self) -> Set[str]:
        return (self.backend_fields - self.frontend_fields) - BACKEND_ONLY_FIELDS

    def has_issue(self) -> bool:
        return bool(self.missing_in_backend or self.missing_in_frontend)


def run_scan() -> Tuple[List[Mismatch], int]:
    mismatches: List[Mismatch] = []
    issue_count = 0

    for page_scan in SCANS:
        template_path = REPO_ROOT / page_scan.template
        if not template_path.exists():
            print(f"⚠️  Template not found: {page_scan.template}")
            continue

        template_text = template_path.read_text(encoding="utf-8")

        for form_scan in page_scan.forms:
            go_path = REPO_ROOT / form_scan.handler_file
            if not go_path.exists():
                print(f"⚠️  Handler file not found: {form_scan.handler_file}")
                continue

            # Determine frontend fields
            frontend: Set[str] = set()
            frontend_source = "unknown"

            if form_scan.form_id:
                frontend = extract_form_fields(template_text, form_scan.form_id)
                frontend_source = f"<form id='{form_scan.form_id}'>"

            if not frontend and form_scan.submit_func:
                # Search in template first, then optional JS file
                frontend = extract_js_submit_fields(template_text, form_scan.submit_func)
                frontend_source = f"JS function {form_scan.submit_func}()"

                if not frontend and form_scan.js_file:
                    js_path = REPO_ROOT / form_scan.js_file
                    if js_path.exists():
                        js_text = js_path.read_text(encoding="utf-8")
                        frontend = extract_js_submit_fields(js_text, form_scan.submit_func)
                        frontend_source = f"{form_scan.js_file}::{form_scan.submit_func}()"

            if form_scan.extra_frontend_fields:
                frontend = frontend | form_scan.extra_frontend_fields

            backend = extract_handler_struct_fields(go_path, form_scan.handler_func)

            mismatch = Mismatch(
                page=page_scan.page,
                form=form_scan.name,
                endpoint=form_scan.endpoint,
                frontend_fields=frontend,
                backend_fields=backend,
                frontend_source=frontend_source,
            )

            if mismatch.has_issue():
                mismatches.append(mismatch)
                issue_count += len(mismatch.missing_in_backend) + len(mismatch.missing_in_frontend)

    return mismatches, issue_count


def print_report(mismatches: List[Mismatch], issue_count: int) -> None:
    print("=" * 75)
    print("Frontend ↔ Backend Field Alignment Report")
    print("=" * 75)

    if not mismatches:
        print("\n✅ No mismatches found.")
        return

    for m in mismatches:
        print(f"\n{m.page} [{m.form} form] → {m.endpoint}")
        print("-" * 75)
        print(f"  Frontend source: {m.frontend_source}")
        print(f"  Frontend fields ({len(m.frontend_fields)}): {sorted(m.frontend_fields)}")
        print(f"  Backend fields  ({len(m.backend_fields)}): {sorted(m.backend_fields)}")

        if m.missing_in_backend:
            print(f"  ❌ Missing in backend: {sorted(m.missing_in_backend)}")
        if m.missing_in_frontend:
            print(f"  ⚠️  Missing in frontend (backend only): {sorted(m.missing_in_frontend)}")

    print("\n" + "=" * 75)
    print(f"Total issues: {issue_count}")
    print("=" * 75)


def main() -> int:
    mismatches, issue_count = run_scan()
    print_report(mismatches, issue_count)
    return 1 if issue_count > 0 else 0


if __name__ == "__main__":
    sys.exit(main())

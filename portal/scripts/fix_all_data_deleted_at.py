#!/usr/bin/env python3
"""Add deleted_at IS NULL filter to ALL queries in data.go that need it."""

import re

with open('internal/web/data.go') as f:
    text = f.read()

# Tables that have deleted_at column and queries need filtering
# For each: find FROM <table> patterns not followed by deleted_at in the WHERE clause

fixes_applied = 0

# === enterprises ===
# Line 1774: FROM enterprises WHERE id = $1::uuid (no deleted_at)
old = "FROM enterprises WHERE id = $1::uuid`)"
new = "FROM enterprises WHERE id = $1::uuid AND deleted_at IS NULL`)"
count = text.count(old)
if count > 0 and 'deleted_at' not in old:
    text = text.replace(old, new)
    fixes_applied += 1
    print(f'Fixed enterprises detail query ({count} occurrences)')

# === api_keys ===
# Line 114: FROM api_keys ak (list query, likely missing deleted_at)
# Find the specific query context and add WHERE
# Line 218: FROM api_keys ak (another list)
# Line 1681: FROM api_keys k (enterprise keys list)
# Line 2353: FROM api_keys ak (downstream keys list)

# For api_keys queries that have a WHERE clause, add deleted_at IS NULL
# Line 114 context
api_key_list_patterns = [
    # keys list for user (line ~114-117)
    ("FROM api_keys ak\n\t\t\t WHERE ak.user_id = $1::uuid",
     "FROM api_keys ak\n\t\t\t WHERE ak.deleted_at IS NULL AND ak.user_id = $1::uuid"),
    # keys list (line ~218)
    ("FROM api_keys ak\n\t\t\t WHERE ak.user_id = $1::uuid AND ak.status = 'active'",
     "FROM api_keys ak\n\t\t\t WHERE ak.deleted_at IS NULL AND ak.user_id = $1::uuid AND ak.status = 'active'"),
    # dashboard keys query (line ~276)
    ("FROM api_keys k\n\t\t\t WHERE k.deleted_at IS NULL",
     None),  # already has it
    # enterprise keys (line ~1681)
    ("FROM api_keys k\n\t\t\t WHERE k.enterprise_id = $1::uuid",
     "FROM api_keys k\n\t\t\t WHERE k.deleted_at IS NULL AND k.enterprise_id = $1::uuid"),
    # downstream keys (line ~2353)
    ("FROM api_keys ak\n\t\t\t LEFT JOIN users u",
     None),  # complex join, handle separately
]

for old, new in api_key_list_patterns:
    if new and old in text:
        text = text.replace(old, new, 1)
        fixes_applied += 1
        print(f'Fixed api_keys query: {old[:60]}...')

# === orders ===
# Line 379: FROM orders WHERE user_id
old = "FROM orders WHERE user_id = $1::uuid ORDER BY created_at DESC"
new = "FROM orders WHERE deleted_at IS NULL AND user_id = $1::uuid ORDER BY created_at DESC"
if old in text:
    text = text.replace(old, new, 1)
    fixes_applied += 1
    print('Fixed orders list query')

# Line 1995: FROM orders WHERE order_id
old = "FROM orders WHERE order_id = $1 AND user_id = $2::uuid"
new = "FROM orders WHERE deleted_at IS NULL AND order_id = $1 AND user_id = $2::uuid"
if old in text:
    text = text.replace(old, new, 1)
    fixes_applied += 1
    print('Fixed orders detail query')

# === service_models ===
# Line 971: FROM service_models + providerWhere
old = "FROM service_models` + providerWhere + `"
new = "FROM service_models WHERE deleted_at IS NULL` + providerWhere + `"
count = text.count(old)
if count > 0:
    text = text.replace(old, new)
    fixes_applied += 1
    print(f'Fixed service_models list query ({count} occurrences)')

# Line 2158: FROM service_models (no WHERE at all)
old = "FROM service_models\n\t\t\t WHERE 1=1 AND status != 'ignored'"
if old in text:
    new_sm = "FROM service_models\n\t\t\t WHERE deleted_at IS NULL AND status != 'ignored'"
    text = text.replace(old, new_sm)
    fixes_applied += 1
    print('Fixed service_models status query')

# === subscription_plans ===
# Line 1112: Already has ORDER BY deleted_at NULLS FIRST — add WHERE filter too
old = "FROM subscription_plans ORDER BY deleted_at NULLS FIRST"
new = "FROM subscription_plans WHERE deleted_at IS NULL ORDER BY sort_order"
if old in text:
    text = text.replace(old, new)
    fixes_applied += 1
    print('Fixed subscription_plans query')

# === api_keys subquery in dashboard (line 1636) ===
old = "(SELECT COUNT(*) FROM api_keys WHERE user_id = u.id AND deleted_at IS NULL)"
# already has deleted_at, skip

print(f'\nTotal fixes: {fixes_applied}')

with open('internal/web/data.go', 'w') as f:
    f.write(text)

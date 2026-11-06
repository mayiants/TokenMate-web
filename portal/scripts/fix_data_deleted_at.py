#!/usr/bin/env python3
"""Add deleted_at IS NULL filter to page-rendering queries in data.go"""

with open('internal/web/data.go') as f:
    text = f.read()

fixes = [
    # admin groups page — already fixed manually, skip

    # user_groups for user dashboard
    ("FROM user_groups g\n\t\t\t WHERE g.status = 'active' AND (g.enterprise_id IS NULL AND g.group_type != 'enterprise')",
     "FROM user_groups g\n\t\t\t WHERE g.deleted_at IS NULL AND g.status = 'active' AND (g.enterprise_id IS NULL AND g.group_type != 'enterprise')"),

    # user_groups count query
    ("FROM user_groups g\n\t\t\t WHERE g.status = 'active'",
     "FROM user_groups g\n\t\t\t WHERE g.deleted_at IS NULL AND g.status = 'active'"),

    # user_groups for enterprise
    ("FROM user_groups g\n\t\t\t WHERE g.enterprise_id = $1::uuid",
     "FROM user_groups g\n\t\t\t WHERE g.deleted_at IS NULL AND g.enterprise_id = $1::uuid"),

    # enterprises detail
    ("FROM enterprises WHERE id = $1::uuid",
     "FROM enterprises WHERE id = $1::uuid AND deleted_at IS NULL"),

    # service_models list
    ("FROM service_models\n\t\t\t WHERE 1=1",
     "FROM service_models\n\t\t\t WHERE deleted_at IS NULL"),
]

count = 0
for old, new in fixes:
    if old in text:
        text = text.replace(old, new, 1)
        count += 1
        print(f'Fixed: {old[:80]}...')
    else:
        print(f'SKIP: {old[:80]}...')

with open('internal/web/data.go', 'w') as f:
    f.write(text)
print(f'\nFixed {count} queries')

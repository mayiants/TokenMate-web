#!/usr/bin/env python3
import json, os

zh_path = 'web/i18n/zh.json'
en_path = 'web/i18n/en.json'

with open(zh_path, encoding='utf-8') as f:
    zh = json.load(f)
with open(en_path, encoding='utf-8') as f:
    en = json.load(f)

# 1. Fix Enterprise translation
zh['Enterprise'] = '企业'
# Ensure Enterprise Management identity mapping exists
zh.setdefault('Enterprise Management', '企业管理')
en.setdefault('Enterprise Management', 'Enterprise Management')

# 2. Add missing Go page title keys
zh.setdefault('nav.reconciliation', '对账管理')
en.setdefault('nav.reconciliation', 'Reconciliation')
zh.setdefault('nav.my_subscriptions', '我的订阅')
en.setdefault('nav.my_subscriptions', 'My Subscriptions')

# 3. Add/refactor JS full-phrase keys (actual newline chars)
zh['Are you sure you want to disable enterprise "{name}"? All members will lose access.'] = '确定要禁用企业"{name}"吗？所有成员将失去访问权限。'
en['Are you sure you want to disable enterprise "{name}"? All members will lose access.'] = 'Are you sure you want to disable enterprise "{name}"? All members will lose access.'

zh['Are you sure you want to enable enterprise "{name}"?'] = '确定要启用企业"{name}"吗？'
en['Are you sure you want to enable enterprise "{name}"?'] = 'Are you sure you want to enable enterprise "{name}"?'

zh['⚠️ Are you sure you want to permanently delete enterprise "{name}"?\n\nThis action will:\n- Remove all member enterprise associations\n- Delete enterprise quota and settings\n- This action is irreversible'] = '⚠️ 确定要永久删除企业"{name}"吗？\n\n此操作将：\n- 移除所有成员的企业关联\n- 删除企业配额和设置\n- 此操作不可逆'
en['⚠️ Are you sure you want to permanently delete enterprise "{name}"?\n\nThis action will:\n- Remove all member enterprise associations\n- Delete enterprise quota and settings\n- This action is irreversible'] = '⚠️ Are you sure you want to permanently delete enterprise "{name}"?\n\nThis action will:\n- Remove all member enterprise associations\n- Delete enterprise quota and settings\n- This action is irreversible'

zh['Are you sure you want to delete provider "{name}"? This cannot be undone.'] = '确定要删除供应商"{name}"吗？此操作不可撤销。'
en['Are you sure you want to delete provider "{name}"? This cannot be undone.'] = 'Are you sure you want to delete provider "{name}"? This cannot be undone.'

# 4. Replace old multiline admin-enterprise template key (backslash-n) with actual newline key
old_delete_key = '⚠️ Are you sure you want to permanently delete enterprise "{name}"?\\n\\nThis will:\\n- Remove all member enterprise associations\\n- Delete enterprise quota and settings\\n- This action cannot be undone'
new_delete_key = '⚠️ Are you sure you want to permanently delete enterprise "{name}"?\n\nThis will:\n- Remove all member enterprise associations\n- Delete enterprise quota and settings\n- This action cannot be undone'

if old_delete_key in zh:
    old_val = zh.pop(old_delete_key)
    # Convert value's \\n to actual newline as well
    new_val = old_val.replace('\\n', '\n')
    zh[new_delete_key] = new_val
if old_delete_key in en:
    en.pop(old_delete_key)
    en[new_delete_key] = new_delete_key

# 5. Remove now-unused fragment keys
fragments_to_remove = [
    'Are you sure you want to disable enterprise ',
    'Are you sure you want to enable enterprise ',
    'Are you sure you want to delete provider ',
    '⚠️ Are you sure you want to permanently delete enterprise ',
]
for k in fragments_to_remove:
    zh.pop(k, None)
    en.pop(k, None)

# 6. Cleanup stale second-half fragments if they exist in JSON
for k in list(zh.keys()):
    if k.startswith('"?') or k.startswith('"?\\n') or 'All members will lose access' in k:
        zh.pop(k, None)
        en.pop(k, None)

with open(zh_path, 'w', encoding='utf-8') as f:
    json.dump(zh, f, ensure_ascii=False, indent=2)
with open(en_path, 'w', encoding='utf-8') as f:
    json.dump(en, f, ensure_ascii=False, indent=2)

print('Done. zh keys:', len(zh), 'en keys:', len(en))

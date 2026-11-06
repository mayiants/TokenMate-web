#!/usr/bin/env python3
"""Refactor confirmation dialogs so the entity name/email appears at the end on its own line."""
import json, os

REPLACEMENTS = {
    'web/templates/admin-subscriptions.html': [
        (
            """function confirmTogglePlan(planId,planName,action) {
  var msg=_t('Are you sure you want to {action} plan \\"{name}\\"?').replace('{action}', action).replace('{name}', planName);
  if(typeof showConfirm==='function'){
    showConfirm(_t('Confirm').replace('{action}', action), msg, function(){doTogglePlan(planId);});
  }else if(confirm(msg)){
    doTogglePlan(planId);
  }
}""",
            """function confirmTogglePlan(planId,planName,action) {
  var body = action === 'disable'
    ? _t('Are you sure you want to disable this plan?')
    : _t('Are you sure you want to enable this plan?');
  var msg = body + '\\n\\n' + planName;
  if(typeof showConfirm==='function'){
    showConfirm(_t('Confirm'), msg, function(){doTogglePlan(planId);});
  }else if(confirm(msg)){
    doTogglePlan(planId);
  }
}"""
        ),
        (
            """  var msgText=_t('Are you sure you want to delete plan \\"{name}\\"? This cannot be undone!').replace('{name}', planName);""",
            """  var msgText=_t('Are you sure you want to delete this plan?\\n\\nThis action cannot be undone.') + '\\n\\n' + planName;"""
        ),
    ],
    'web/templates/admin-enterprise.html': [
        (
            """function confirmSuspend(id, name) {
  var msg = _t('Are you sure you want to disable enterprise \\"{name}\\"? All members of this enterprise will be unable to use the service.').replace('{name}', name);
  showConfirm(_t('Confirm Disable'), msg, function() {""",
            """function confirmSuspend(id, name) {
  var msg = _t('Are you sure you want to disable this enterprise?\\n\\nAll members will be unable to use the service.') + '\\n\\n' + name;
  showConfirm(_t('Confirm Disable'), msg, function() {"""
        ),
        (
            """function confirmActivate(id, name) {
  var msg = _t('Are you sure you want to enable enterprise \\"{name}\\"?').replace('{name}', name);
  showConfirm(_t('Confirm Activate'), msg, function() {""",
            """function confirmActivate(id, name) {
  var msg = _t('Are you sure you want to enable this enterprise?') + '\\n\\n' + name;
  showConfirm(_t('Confirm Activate'), msg, function() {"""
        ),
        (
            """function confirmDelete(id, name) {
  var msg = _t('⚠️ Are you sure you want to permanently delete enterprise \\"{name}\\"?\n\nThis will:\n- Remove all member enterprise associations\n- Delete enterprise quota and settings\n- This action cannot be undone').replace('{name}', name);
  showConfirm(_t('Delete'), msg, function() {""",
            """function confirmDelete(id, name) {
  var msg = _t('⚠️ Are you sure you want to permanently delete this enterprise?\\n\\nThis will:\\n- Remove all member enterprise associations\\n- Delete enterprise quota and settings\\n- This action cannot be undone.') + '\\n\\n' + name;
  showConfirm(_t('Delete'), msg, function() {"""
        ),
    ],
    'web/static/js/page-admin-enterprise.js': [
        (
            """function confirmSuspend(id, name) {
  showConfirm(_t('Confirm Disable'), _t('Are you sure you want to disable enterprise "{name}"? All members will lose access.').replace('{name}', name), function() {""",
            """function confirmSuspend(id, name) {
  showConfirm(_t('Confirm Disable'), _t('Are you sure you want to disable this enterprise?\\n\\nAll members will lose access.') + '\\n\\n' + name, function() {"""
        ),
        (
            """function confirmActivate(id, name) {
  showConfirm(_t('Confirm Enable'), _t('Are you sure you want to enable enterprise "{name}"?').replace('{name}', name), function() {""",
            """function confirmActivate(id, name) {
  showConfirm(_t('Confirm Enable'), _t('Are you sure you want to enable this enterprise?') + '\\n\\n' + name, function() {"""
        ),
        (
            """function confirmDelete(id, name) {
  showConfirm(_t('Confirm Delete'), _t('⚠️ Are you sure you want to permanently delete enterprise "{name}"?\n\nThis action will:\n- Remove all member enterprise associations\n- Delete enterprise quota and settings\n- This action is irreversible').replace('{name}', name), function() {""",
            """function confirmDelete(id, name) {
  showConfirm(_t('Confirm Delete'), _t('⚠️ Are you sure you want to permanently delete this enterprise?\\n\\nThis will:\\n- Remove all member enterprise associations\\n- Delete enterprise quota and settings\\n- This action cannot be undone.') + '\\n\\n' + name, function() {"""
        ),
    ],
    'web/templates/admin-providers.html': [
        (
            """function deleteProvider(providerId, name) {
  showConfirm(_t('Delete'), _t('Are you sure you want to delete provider "{name}"? This action cannot be undone.').replace('{name}', name || providerId), function() {""",
            """function deleteProvider(providerId, name) {
  showConfirm(_t('Delete'), _t('Are you sure you want to delete this provider?\\n\\nThis action cannot be undone.') + '\\n\\n' + (name || providerId), function() {"""
        ),
    ],
    'web/static/js/page-admin-providers.js': [
        (
            """function deleteProvider(providerId, name) {
  showConfirm(_t('Confirm Delete'), _t('Are you sure you want to delete provider "{name}"? This cannot be undone.').replace('{name}', name || providerId), function() {""",
            """function deleteProvider(providerId, name) {
  showConfirm(_t('Confirm Delete'), _t('Are you sure you want to delete this provider?\\n\\nThis action cannot be undone.') + '\\n\\n' + (name || providerId), function() {"""
        ),
    ],
    'web/templates/apikeys.html': [
        (
            """function confirmDeleteKey(keyId, keyName) {
  showConfirm(_t('Delete Key'), _t('Are you sure you want to delete "{name}"? This cannot be undone.').replace('{name}', keyName), function() {""",
            """function confirmDeleteKey(keyId, keyName) {
  showConfirm(_t('Delete Key'), _t('Are you sure you want to delete this key?\\n\\nThis action cannot be undone.') + '\\n\\n' + keyName, function() {"""
        ),
    ],
    'web/templates/enterprise/keys.html': [
        (
            """function confirmRevoke(keyId, keyName) {
  showConfirm(_t('Disable Key'), _t('Are you sure you want to disable key "{name}"? Requests using this key will be rejected.').replace('{name}', keyName), function() {""",
            """function confirmRevoke(keyId, keyName) {
  showConfirm(_t('Disable Key'), _t('Are you sure you want to disable this key?\\n\\nRequests using this key will be rejected.') + '\\n\\n' + keyName, function() {"""
        ),
        (
            """function confirmDelete(keyId, keyName) {
  showConfirm(_t('Delete Key'), _t('Are you sure you want to delete key "{name}"? This action cannot be undone.').replace('{name}', keyName), function() {""",
            """function confirmDelete(keyId, keyName) {
  showConfirm(_t('Delete Key'), _t('Are you sure you want to delete this key?\\n\\nThis action cannot be undone.') + '\\n\\n' + keyName, function() {"""
        ),
    ],
    'web/templates/admin-groups.html': [
        (
            """function confirmDeleteGroup(id, name) {
  showConfirm(_t('Delete User Group'), _t('Are you sure you want to delete user group "{name}"? This action cannot be undone.').replace('{name}', name), function() {""",
            """function confirmDeleteGroup(id, name) {
  showConfirm(_t('Delete User Group'), _t('Are you sure you want to delete this user group?\\n\\nThis action cannot be undone.') + '\\n\\n' + name, function() {"""
        ),
    ],
    'web/templates/enterprise/users.html': [
        (
            """function confirmDeleteUser(id, name) {
  showConfirm(_t('Remove member'), _t('Are you sure you want to remove member "{name}"? Their enterprise keys will be revoked.').replace('{name}', name), function() {""",
            """function confirmDeleteUser(id, name) {
  showConfirm(_t('Remove member'), _t('Are you sure you want to remove this member?\\n\\nTheir enterprise keys will be revoked.') + '\\n\\n' + name, function() {"""
        ),
        (
            """function cancelInvitation(id, email) {
  showConfirm(_t('Cancel Invitation'), _t('Cancel invitation to {email}?').replace('{email}', email), function() {""",
            """function cancelInvitation(id, email) {
  showConfirm(_t('Cancel Invitation'), _t('Are you sure you want to cancel this invitation?') + '\\n\\n' + email, function() {"""
        ),
    ],
}

for path, reps in REPLACEMENTS.items():
    text = open(path, encoding='utf-8').read()
    for old, new in reps:
        if old not in text:
            print(f'WARN: snippet not found in {path}')
            continue
        text = text.replace(old, new)
    open(path, 'w', encoding='utf-8').write(text)
    print(f'Updated {path}')

# Update JSON translations
zh = json.load(open('web/i18n/zh.json', encoding='utf-8'))
en = json.load(open('web/i18n/en.json', encoding='utf-8'))

new_keys = {
    # admin-subscriptions
    'Are you sure you want to disable this plan?': '确定要禁用该套餐吗？',
    'Are you sure you want to enable this plan?': '确定要启用该套餐吗？',
    'Are you sure you want to delete this plan?\n\nThis action cannot be undone.': '确定要删除该套餐吗？\n\n此操作不可撤销。',
    # admin-enterprise / page-admin-enterprise
    'Are you sure you want to disable this enterprise?\n\nAll members will be unable to use the service.': '确定要禁用该企业吗？\n\n所有成员将无法使用服务。',
    'Are you sure you want to enable this enterprise?': '确定要启用该企业吗？',
    '⚠️ Are you sure you want to permanently delete this enterprise?\n\nThis will:\n- Remove all member enterprise associations\n- Delete enterprise quota and settings\n- This action cannot be undone.':
        '⚠️ 确定要永久删除该企业吗？\n\n此操作将：\n- 移除所有成员的企业关联\n- 删除企业配额和设置\n- 此操作不可撤销。',
    # providers
    'Are you sure you want to delete this provider?\n\nThis action cannot be undone.': '确定要删除该供应商吗？\n\n此操作不可撤销。',
    # apikeys / enterprise keys
    'Are you sure you want to delete this key?\n\nThis action cannot be undone.': '确定要删除该密钥吗？\n\n此操作不可撤销。',
    'Are you sure you want to disable this key?\n\nRequests using this key will be rejected.': '确定要禁用该密钥吗？\n\n使用该密钥的请求将被拒绝。',
    # admin-groups
    'Are you sure you want to delete this user group?\n\nThis action cannot be undone.': '确定要删除该用户组吗？\n\n此操作不可撤销。',
    # enterprise users
    'Are you sure you want to remove this member?\n\nTheir enterprise keys will be revoked.': '确定要移除该成员吗？\n\n其企业密钥将被回收。',
    'Are you sure you want to cancel this invitation?': '确定要取消该邀请吗？',
}

for k, zh_val in new_keys.items():
    zh[k] = zh_val
    en[k] = k

# Remove old mid-sentence / {action} keys
old_keys_to_remove = [
    'Are you sure you want to {action} plan "{name}"?',
    'Are you sure you want to delete plan "{name}"? This cannot be undone!',
    'Are you sure you want to delete provider "{name}"? This action cannot be undone.',
    'Are you sure you want to delete provider "{name}"? This cannot be undone.',
    'Are you sure you want to disable enterprise "{name}"? All members of this enterprise will be unable to use the service.',
    'Are you sure you want to enable enterprise "{name}"?',
    '⚠️ Are you sure you want to permanently delete enterprise "{name}"?\n\nThis will:\n- Remove all member enterprise associations\n- Delete enterprise quota and settings\n- This action cannot be undone',
    '⚠️ Are you sure you want to permanently delete enterprise "{name}"?\n\nThis action will:\n- Remove all member enterprise associations\n- Delete enterprise quota and settings\n- This action is irreversible',
    'Are you sure you want to delete "{name}"? This cannot be undone.',
    'Are you sure you want to delete key "{name}"? This action cannot be undone.',
    'Are you sure you want to disable key "{name}"? Requests using this key will be rejected.',
    'Are you sure you want to delete user group "{name}"? This action cannot be undone.',
    'Are you sure you want to remove member "{name}"? Their enterprise keys will be revoked.',
    'Cancel invitation to {email}?',
]
for k in old_keys_to_remove:
    zh.pop(k, None)
    en.pop(k, None)

# Clean up any leftover fragment keys
for k in list(zh.keys()):
    if k.startswith('Are you sure you want to delete provider ') or \
       k.startswith('Are you sure you want to disable enterprise ') or \
       k.startswith('Are you sure you want to enable enterprise ') or \
       k.startswith('⚠️ Are you sure you want to permanently delete enterprise ') or \
       k.startswith('"?') or k.startswith('"?\\n'):
        zh.pop(k, None)
        en.pop(k, None)

with open('web/i18n/zh.json', 'w', encoding='utf-8') as f:
    json.dump(zh, f, ensure_ascii=False, indent=2, sort_keys=True)
with open('web/i18n/en.json', 'w', encoding='utf-8') as f:
    json.dump(en, f, ensure_ascii=False, indent=2, sort_keys=True)

print('Updated zh.json/en.json')

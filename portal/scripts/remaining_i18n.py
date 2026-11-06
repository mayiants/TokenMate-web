#!/usr/bin/env python3
"""
Convert P0 hardcoded Chinese UI snippets and P1 remaining non-terms Chinese keys
to English-default i18n keys.
"""
import json, re, os
from collections import OrderedDict

BASE = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
ZH_PATH = os.path.join(BASE, "web", "i18n", "zh.json")
EN_PATH = os.path.join(BASE, "web", "i18n", "en.json")

with open(ZH_PATH, "r", encoding="utf-8") as f:
    zh = json.load(f, object_pairs_hook=OrderedDict)
with open(EN_PATH, "r", encoding="utf-8") as f:
    en = json.load(f, object_pairs_hook=OrderedDict)

# Ensure new keys are appended
zh_new = OrderedDict()
en_new = OrderedDict()

def add_key(eng_key, zh_value, en_value=None):
    if en_value is None:
        en_value = eng_key
    if eng_key not in zh:
        zh[eng_key] = zh_value
    if eng_key not in en:
        en[eng_key] = en_value

# P0: file -> list of (old_text, new_text, english_key, zh_translation, en_translation)
P0_REPLACEMENTS = {
    "web/templates/admin-groups.html": [
        (
            '''showConfirm(_t('Delete User Group'), _t('确定要删除用户组 "') + name + _t('" 吗？此操作不可恢复。'), function() {''',
            '''showConfirm(_t('Delete User Group'), _t('Are you sure you want to delete user group "{name}"? This action cannot be undone.').replace('{name}', name), function() {''',
            "Are you sure you want to delete user group \"{name}\"? This action cannot be undone.",
            "确定要删除用户组 \"{name}\" 吗？此操作不可恢复。",
        ),
    ],
    "web/templates/admin-subscriptions.html": [
        (
            "body.innerHTML='<div class=\"text-center py-8 text-muted\">'+(typeof _t==='function'?_t('Loading...'):'加载中...')+'</div>';",
            "body.innerHTML='<div class=\"text-center py-8 text-muted\">'+(typeof _t==='function'?_t('Loading...'):'Loading...')+'</div>';",
            "Loading...",
            "加载中...",
        ),
        (
            '''var msg=_t('确定要{action}计划 "{name}" 吗？').replace('{action}', action).replace('{name}', planName);''',
            '''var msg=_t('Are you sure you want to {action} plan "{name}"?').replace('{action}', action).replace('{name}', planName);''',
            "Are you sure you want to {action} plan \"{name}\"?",
            "确定要{action}计划 \"{name}\" 吗？",
        ),
        (
            '''var msgText=_t('确定要删除计划 "{name}" 吗？删除后无法恢复！').replace('{name}', planName);''',
            '''var msgText=_t('Are you sure you want to delete plan "{name}"? This cannot be undone!').replace('{name}', planName);''',
            "Are you sure you want to delete plan \"{name}\"? This cannot be undone!",
            "确定要删除计划 \"{name}\" 吗？删除后无法恢复！",
        ),
        (
            "tbody.innerHTML = '<tr><td colspan=\"9\" class=\"text-center text-muted py-4\">' + (typeof _t === 'function' ? _t('No Data') : '暂无数据') + '</td></tr>';",
            "tbody.innerHTML = '<tr><td colspan=\"9\" class=\"text-center text-muted py-4\">' + (typeof _t === 'function' ? _t('No Data') : 'No data') + '</td></tr>';",
            "No data",
            "暂无数据",
        ),
        (
            "'<td><button class=\"btn btn-sm btn-secondary\" onclick=\"openEditUserSubDrawer(\\'' + s.id + '\\')\">' + (typeof _t === 'function' ? _t('Edit') : '编辑') + '</button></td>' +",
            "'<td><button class=\"btn btn-sm btn-secondary\" onclick=\"openEditUserSubDrawer(\\'' + s.id + '\\')\">' + (typeof _t === 'function' ? _t('Edit') : 'Edit') + '</button></td>' +",
            "Edit",
            "编辑",
        ),
    ],
    "web/templates/admin-keypool.html": [
        (
            "showConfirm('删除Key', '确定要删除此Key吗？', function() {",
            "showConfirm(_t('Delete Key'), _t('Are you sure you want to delete this key?'), function() {",
            "Are you sure you want to delete this key?",
            "确定要删除此Key吗？",
        ),
        (
            "showConfirm(action + 'Key', '确定要' + action + '此Key吗？', function() {",
            "showConfirm(action + ' Key', _t('Are you sure you want to {action} this key?').replace('{action}', action), function() {",
            "Are you sure you want to {action} this key?",
            "确定要{action}此Key吗？",
        ),
        (
            "showError(action + '失败: ' + JSON.stringify(data.error));",
            "showError(_t('{action} failed: {error}').replace('{action}', action).replace('{error}', JSON.stringify(data.error)));",
            "{action} failed: {error}",
            "{action}失败: {error}",
        ),
        (
            ".catch(e => showError(action + '失败: ' + e.message));",
            ".catch(e => showError(_t('{action} failed: {message}').replace('{action}', action).replace('{message}', e.message)));",
            "{action} failed: {message}",
            "{action}失败: {message}",
        ),
        (
            "showSuccess('Key验证成功! 状态: ' + data.status + (data.models ? ', 模型数: ' + data.models.length : ''));",
            "showSuccess(_t('Key verified successfully! Status: {status}').replace('{status}', data.status) + (data.models ? _t(', Models: {count}').replace('{count}', data.models.length) : ''));",
            "Key verified successfully! Status: {status}",
            "Key验证成功! 状态: {status}",
        ),
        (
            ", Models: {count}",
            ", 模型数: {count}",
        ),
        (
            "showError('Key验证结果: ' + data.status);",
            "showError(_t('Key verification result: {status}').replace('{status}', data.status));",
            "Key verification result: {status}",
            "Key验证结果: {status}",
        ),
        (
            ".catch(e => showError('验证失败: ' + e.message));",
            ".catch(e => showError(_t('Verification failed: {message}').replace('{message}', e.message)));",
            "Verification failed: {message}",
            "验证失败: {message}",
        ),
        (
            "showConfirm('批量验证', '确定要验证所有Key吗？', function() {",
            "showConfirm(_t('Batch Verification'), _t('Are you sure you want to verify all keys?'), function() {",
            "Are you sure you want to verify all keys?",
            "确定要验证所有Key吗？",
        ),
        (
            "showSuccess('验证完成! 有效: ' + data.validated + ', 无效: ' + data.invalid);",
            "showSuccess(_t('Verification complete! Valid: {validated}, Invalid: {invalid}').replace('{validated}', data.validated).replace('{invalid}', data.invalid));",
            "Verification complete! Valid: {validated}, Invalid: {invalid}",
            "验证完成! 有效: {validated}, 无效: {invalid}",
        ),
        (
            "showError('超过 ' + maxLines + ' 行限制，请使用文件上传');",
            "showError(_t('Exceeded {maxLines} line limit, please use file upload').replace('{maxLines}', maxLines));",
            "Exceeded {maxLines} line limit, please use file upload",
            "超过 {maxLines} 行限制，请使用文件上传",
        ),
        (
            "showError('请选择供应商');",
            "showError(_t('Please select a provider'));",
            "Please select a provider",
            "请选择供应商",
        ),
        (
            "showError('请输入要导入的Key');",
            "showError(_t('Please enter keys to import'));",
            "Please enter keys to import",
            "请输入要导入的Key",
        ),
        (
            "showError('没有有效的Key');",
            "showError(_t('No valid keys'));",
            "No valid keys",
            "没有有效的Key",
        ),
        (
            "showError('导入失败: ' + (data.error.message || JSON.stringify(data.error)));",
            "showError(_t('Import failed: {message}').replace('{message}', data.error.message || JSON.stringify(data.error)));",
            "Import failed: {message}",
            "导入失败: {message}",
        ),
        (
            ".catch(e => showError('导入失败: ' + e.message));",
            ".catch(e => showError(_t('Import failed: {message}').replace('{message}', e.message)));",
            "Import failed: {message}",
            "导入失败: {message}",
        ),
        (
            "showError('请输入要导入的Key数据');",
            "showError(_t('Please enter key data to import'));",
            "Please enter key data to import",
            "请输入要导入的Key数据",
        ),
        (
            "showError('第 ' + (i + 1) + ' 行格式错误，需要: 供应商,Key');",
            "showError(_t('Line {line} format error, expected: Provider,Key').replace('{line}', i + 1));",
            "Line {line} format error, expected: Provider,Key",
            "第 {line} 行格式错误，需要: 供应商,Key",
        ),
        (
            "showError('没有有效的Key数据');",
            "showError(_t('No valid key data'));",
            "No valid key data",
            "没有有效的Key数据",
        ),
        (
            "showError('导出失败: ' + (data.error.message || JSON.stringify(data.error)));",
            "showError(_t('Export failed: {message}').replace('{message}', data.error.message || JSON.stringify(data.error)));",
            "Export failed: {message}",
            "导出失败: {message}",
        ),
        (
            "showError('没有可导出的Key');",
            "showError(_t('No keys to export'));",
            "No keys to export",
            "没有可导出的Key",
        ),
        (
            ".catch(e => showError('导出失败: ' + e.message));",
            ".catch(e => showError(_t('Export failed: {message}').replace('{message}', e.message)));",
            "Export failed: {message}",
            "导出失败: {message}",
        ),
    ],
    "web/templates/admin-enterprise.html": [
        (
            '''var msg = _t('确定要禁用企业 "{name}" 吗？禁用后该企业所有成员将无法使用。').replace('{name}', name);''',
            '''var msg = _t('Are you sure you want to disable enterprise "{name}"? All members of this enterprise will be unable to use the service.').replace('{name}', name);''',
            "Are you sure you want to disable enterprise \"{name}\"? All members of this enterprise will be unable to use the service.",
            "确定要禁用企业 \"{name}\" 吗？禁用后该企业所有成员将无法使用。",
        ),
        (
            '''var msg = _t('确定要启用企业 "{name}" 吗？').replace('{name}', name);''',
            '''var msg = _t('Are you sure you want to enable enterprise "{name}"?').replace('{name}', name);''',
            "Are you sure you want to enable enterprise \"{name}\"?",
            "确定要启用企业 \"{name}\" 吗？",
        ),
        (
            '''var msg = _t('⚠️ 确定要永久删除企业 "{name}" 吗？\n\n此操作将：\n- 移除所有成员的企业关联\n- 删除企业配额和设置\n- 此操作不可撤销').replace('{name}', name);''',
            '''var msg = _t('⚠️ Are you sure you want to permanently delete enterprise "{name}"?\n\nThis will:\n- Remove all member enterprise associations\n- Delete enterprise quota and settings\n- This action cannot be undone').replace('{name}', name);''',
            "⚠️ Are you sure you want to permanently delete enterprise \"{name}\"?\\n\\nThis will:\\n- Remove all member enterprise associations\\n- Delete enterprise quota and settings\\n- This action cannot be undone",
            "⚠️ 确定要永久删除企业 \"{name}\" 吗？\\n\\n此操作将：\\n- 移除所有成员的企业关联\\n- 删除企业配额和设置\\n- 此操作不可撤销",
        ),
    ],
    "web/templates/admin-providers.html": [
        (
            '''showConfirm(_t('Delete'), _t('确定要删除供应商 "') + (name || providerId) + _t('" 吗？此操作不可恢复。'), function() {''',
            '''showConfirm(_t('Delete'), _t('Are you sure you want to delete provider "{name}"? This action cannot be undone.').replace('{name}', name || providerId), function() {''',
            "Are you sure you want to delete provider \"{name}\"? This action cannot be undone.",
            "确定要删除供应商 \"{name}\" 吗？此操作不可恢复。",
        ),
    ],
    "web/templates/enterprise/users.html": [
        (
            '''showConfirm(_t('Remove member'), _t('确定要移除成员 "') + name + _t('" 吗？移除后将回收其企业密钥。'), function() {''',
            '''showConfirm(_t('Remove member'), _t('Are you sure you want to remove member "{name}"? Their enterprise keys will be revoked.').replace('{name}', name), function() {''',
            "Are you sure you want to remove member \"{name}\"? Their enterprise keys will be revoked.",
            "确定要移除成员 \"{name}\" 吗？移除后将回收其企业密钥。",
        ),
    ],
    "web/templates/enterprise/keys.html": [
        (
            '''showConfirm(_t('Disable Key'), _t('确定要禁用密钥 "') + keyName + _t('" 吗？禁用后使用该密钥的请求将被拒绝。'), function() {''',
            '''showConfirm(_t('Disable Key'), _t('Are you sure you want to disable key "{name}"? Requests using this key will be rejected.').replace('{name}', keyName), function() {''',
            "Are you sure you want to disable key \"{name}\"? Requests using this key will be rejected.",
            "确定要禁用密钥 \"{name}\" 吗？禁用后使用该密钥的请求将被拒绝。",
        ),
        (
            '''showConfirm(_t('Delete Key'), _t('确定要删除密钥 "') + keyName + _t('" 吗？此操作不可恢复。'), function() {''',
            '''showConfirm(_t('Delete Key'), _t('Are you sure you want to delete key "{name}"? This action cannot be undone.').replace('{name}', keyName), function() {''',
            "Are you sure you want to delete key \"{name}\"? This action cannot be undone.",
            "确定要删除密钥 \"{name}\" 吗？此操作不可恢复。",
        ),
    ],
    "web/templates/apikeys.html": [
        (
            "if (!nameInput.value.trim()) { showMessage('提示', '请输入 Key 名称'); return; }",
            "if (!nameInput.value.trim()) { showMessage(_t('Notice'), _t('Please enter a key name')); return; }",
            "Please enter a key name",
            "请输入 Key 名称",
        ),
        (
            "if (data.selected_models.length === 0) { showMessage('提示', '请至少选择一个模型'); return; }",
            "if (data.selected_models.length === 0) { showMessage(_t('Notice'), _t('Please select at least one model')); return; }",
            "Please select at least one model",
            "请至少选择一个模型",
        ),
        (
            "if (!data.current_model) { showMessage('提示', '请选择一个模型'); return; }",
            "if (!data.current_model) { showMessage(_t('Notice'), _t('Please select a model')); return; }",
            "Please select a model",
            "请选择一个模型",
        ),
        (
            "if (data.group_priority.length === 0) { showMessage('提示', '请至少选择一个 Group'); return; }",
            "if (data.group_priority.length === 0) { showMessage(_t('Notice'), _t('Please select at least one group')); return; }",
            "Please select at least one group",
            "请至少选择一个 Group",
        ),
        (
            "showMessage('创建失败', (res.error && res.error.message) || '请重试');",
            "showMessage(_t('Create failed'), (res.error && res.error.message) || _t('Please try again'));",
            "Create failed",
            "创建失败",
        ),
        (
            "Please try again",
            "请重试",
        ),
        (
            ".catch(function() { showMessage('错误', '请求失败，请重试'); });",
            ".catch(function() { showMessage(_t('Error'), _t('Request failed, please try again')); });",
            "Request failed, please try again",
            "请求失败，请重试",
        ),
        (
            "showMessage('提示', '请至少选择一个模型');",
            "showMessage(_t('Notice'), _t('Please select at least one model'));",
            "Please select at least one model",
            "请至少选择一个模型",
        ),
        (
            "showMessage('保存失败', (res.error && res.error.message) || '请重试');",
            "showMessage(_t('Save failed'), (res.error && res.error.message) || _t('Please try again'));",
            "Save failed",
            "保存失败",
        ),
        (
            ".catch(function() { showMessage('错误', '请求失败'); });",
            ".catch(function() { showMessage(_t('Error'), _t('Request failed')); });",
            "Request failed",
            "请求失败",
        ),
        (
            '''showConfirm(_t('Delete Key'), _t('确定要删除 "') + keyName + _t('"? This cannot be undone.'), function() {''',
            '''showConfirm(_t('Delete Key'), _t('Are you sure you want to delete "{name}"? This cannot be undone.').replace('{name}', keyName), function() {''',
            "Are you sure you want to delete \"{name}\"? This cannot be undone.",
            "确定要删除 \"{name}\"? This cannot be undone.",
        ),
        (
            "showMessage('错误', res.error.message || '获取密钥失败');",
            "showMessage(_t('Error'), res.error.message || _t('Failed to get key'));",
            "Failed to get key",
            "获取密钥失败",
        ),
        (
            "showMessage('提示', '无法获取完整密钥');",
            "showMessage(_t('Notice'), _t('Unable to get full key'));",
            "Unable to get full key",
            "无法获取完整密钥",
        ),
        (
            ".catch(function() { showMessage('错误', '请求失败'); });",
            ".catch(function() { showMessage(_t('Error'), _t('Request failed')); });",
            "Request failed",
            "请求失败",
        ),
    ],
    "web/templates/invoices.html": [
        (
            "document.getElementById('request-result').innerHTML = _t('<div class=\"flash flash-error text-sm\">邮箱格式不正确</div>');",
            "document.getElementById('request-result').innerHTML = '<div class=\"flash flash-error text-sm\">' + _t('Email format is incorrect') + '</div>';",
            "Email format is incorrect",
            "邮箱格式不正确",
        ),
    ],
    "web/templates/subscriptions.html": [
        (
            "window.location.href = '/subscriptions?toast=success&message=支付成功';",
            "window.location.href = '/subscriptions?toast=success&message=Payment successful';",
            "Payment successful",
            "支付成功",
        ),
    ],
    "web/templates/my-subscriptions.html": [
        (
            "window.location.href = '/subscriptions?toast=success&message=支付成功';",
            "window.location.href = '/subscriptions?toast=success&message=Payment successful';",
            "Payment successful",
            "支付成功",
        ),
    ],
    "web/templates/plan-and-group.html": [
        (
            "window.location.href = '/subscriptions?toast=success&message=支付成功';",
            "window.location.href = '/subscriptions?toast=success&message=Payment successful';",
            "Payment successful",
            "支付成功",
        ),
    ],
}

# P1: key replacements (old Chinese key -> new English key, zh_value)
P1_KEY_MAP = {
    # layout modals
    "nav_提示": ("Notice", "提示"),
    "nav_确定": ("OK", "确定"),
    "nav_确认_3": ("Confirm", "确认"),
    "nav_取消": ("Cancel", "取消"),
    "nav_确认_4": ("Confirm", "确认"),
    "nav_公告": ("Announcement", "公告"),
    "nav_我知道了": ("Got it", "我知道了"),
    "ui_跳过": ("Skip", "跳过"),
    # audit log detail
    "label_时间": ("Time", "时间"),
    "label_用户": ("User", "用户"),
    "label_分类": ("Category", "分类"),
    "ui_操作": ("Action", "操作"),
    "label_级别": ("Level", "级别"),
    "label_消息": ("Message", "消息"),
    "label_详细信息": ("Details", "详细信息"),
    # page suffix
    "页": ("Page", "页"),
    # group descriptions
    "ui_企业内部管理的分组由企业管理员配置模型和调用限制企业分组不计倍率统一使用企业定价": (
        "Enterprise-managed groups are configured by the enterprise admin to control available models and usage limits. Enterprise groups do not use pricing multipliers and use enterprise pricing uniformly.",
        "企业内部管理的分组，由企业管理员配置模型和调用限制。企业分组不计倍率（统一使用企业定价）。",
    ),
    "ui_如果您只是暂时不需要可以考虑到期后自然失效而不是主动取消": (
        "If you only temporarily do not need it, consider letting it expire naturally rather than canceling actively.",
        "如果您只是暂时不需要，可以考虑到期后自然失效，而不是主动取消。",
    ),
    # checkout labels
    "label_微信支付": ("WeChat Pay", "微信支付"),
    "label_支付宝": ("Alipay", "支付宝"),
}

# Apply P0 replacements
for file_path, repls in P0_REPLACEMENTS.items():
    full_path = os.path.join(BASE, file_path)
    text = open(full_path, encoding="utf-8").read()
    for item in repls:
        if len(item) == 4:
            old, new, eng_key, zh_val = item
            if old not in text:
                print(f"WARN: not found in {file_path}: {old[:80]}")
                continue
            text = text.replace(old, new)
            add_key(eng_key, zh_val)
        elif len(item) == 2:
            # extra key-value pairs (e.g. Please try again, Payment successful) without template replacement
            eng_key, zh_val = item
            add_key(eng_key, zh_val)
    with open(full_path, "w", encoding="utf-8") as f:
        f.write(text)

# Apply P1 key replacements across all templates
P1_FILES = []
for root, dirs, files in os.walk(os.path.join(BASE, "web/templates")):
    for fn in files:
        if fn.endswith(".html"):
            P1_FILES.append(os.path.join(root, fn))

for full_path in P1_FILES:
    text = open(full_path, encoding="utf-8").read()
    changed = False
    for old_key, (new_key, zh_val) in P1_KEY_MAP.items():
        # Replace {{t "old"}} and _t('old')/_t("old")
        # Use regex to avoid false matches
        new_text = re.sub(
            r'\{\{\s*t\s+(["\'])' + re.escape(old_key) + r'\1\s*\}\}',
            lambda m: f'{{{{t {m.group(1)}{new_key}{m.group(1)}}}}}',
            text,
        )
        new_text = re.sub(
            r"_t\((['\"])" + re.escape(old_key) + r"\1\)",
            lambda m: f"_t({m.group(1)}{new_key}{m.group(1)})",
            new_text,
        )
        if new_text != text:
            text = new_text
            add_key(new_key, zh_val)
            changed = True
    if changed:
        with open(full_path, "w", encoding="utf-8") as f:
            f.write(text)

# Fix pagination suffix for usage-logs.html and voucher.html
for file_path in ["web/templates/usage-logs.html", "web/templates/voucher.html"]:
    full_path = os.path.join(BASE, file_path)
    text = open(full_path, encoding="utf-8").read()
    old = "_t('Page') + ' ' + logsPage + ' / ' + totalPages + ' ' + _t('页')"
    new = "_t('Page {page} / {total}').replace('{page}', logsPage).replace('{total}', totalPages)"
    if old in text:
        text = text.replace(old, new)
    # voucher uses data.page
    old2 = "_t('Page') + ' ' + data.page + ' / ' + totalPages + ' ' + _t('页')"
    new2 = "_t('Page {page} / {total}').replace('{page}', data.page).replace('{total}', totalPages)"
    if old2 in text:
        text = text.replace(old2, new2)
    with open(full_path, "w", encoding="utf-8") as f:
        f.write(text)
add_key("Page {page} / {total}", "第 {page} 页 / 共 {total} 页")

# Write JSON
with open(ZH_PATH, "w", encoding="utf-8") as f:
    json.dump(zh, f, ensure_ascii=False, indent=2)
    f.write("\n")
with open(EN_PATH, "w", encoding="utf-8") as f:
    json.dump(en, f, ensure_ascii=False, indent=2)
    f.write("\n")

print("P0 + P1 conversion completed.")

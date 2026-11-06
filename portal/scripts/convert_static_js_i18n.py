#!/usr/bin/env python3
# Convert the remaining Chinese _t() keys in static JS to English phrase keys.
import json, re, os

JS_DIRS = ['web/static/js']
ZH_PATH = 'web/i18n/zh.json'
EN_PATH = 'web/i18n/en.json'

# old_key -> new_key
KEY_MAP = {
    ' 个': ' items',
    ' 个上行Key，共 ': ' upstream keys, total ',
    ' 个模型': ' models',
    ' 个模型, 跳过 ': ' models, skipped ',
    ' 个模型吗？': ' models?',
    ' 个模型添加到服务模型表？': ' models to the service model table?',
    ' 个（已存在）': ' (already exists)',
    'AWS 格式: access_key_id:secret_access_key (将被加密存储)': 'AWS format: access_key_id:secret_access_key (encrypted at rest)',
    'Key将被加密存储': 'Keys will be encrypted at rest',
    'OpenRouter 同步成功! 已同步 ': 'OpenRouter sync successful! Synced ',
    '共 ': 'Total ',
    '信用额度': 'Credit Limit',
    '元': 'CNY',
    '创建时间': 'Created At',
    '删除': 'Delete',
    '加载中...': 'Loading...',
    '加载失败': 'Load failed',
    '加载失败: ': 'Load failed: ',
    '同步失败: ': 'Sync failed: ',
    '同步成功！已更新 ': 'Sync successful! Updated ',
    '名称': 'Name',
    '启用': 'Enable',
    '复制失败': 'Copy failed',
    '姓名': 'Name',
    '定价倍率': 'Pricing Multiplier',
    '导出失败，请重试': 'Export failed, please try again',
    '导出成功，文件已下载': 'Export successful, file downloaded',
    '已加入成员': 'Joined Members',
    '已复制': 'Copied',
    '已复制到剪贴板': 'Copied to clipboard',
    '已映射': 'Mapped',
    '已用信用': 'Credit Used',
    '已用配额': 'Quota Used',
    '已确认邮箱': 'Email Confirmed',
    '待处理邀请': 'Pending Invitations',
    '待确认': 'Pending',
    '将 ': 'Add ',
    '成员': 'Member',
    '成员数': 'Member Count',
    '所有模型': 'All Models',
    '拒绝': 'Reject',
    '提示': 'Notice',
    '操作': 'Actions',
    '暂无成员': 'No members',
    '暂无路由规则': 'No routing rules',
    '未映射': 'Unmapped',
    '未知错误': 'Unknown error',
    '添加到服务模型': 'Add to Service Models',
    '添加成功! 已添加 ': 'Added successfully! Added ',
    '添加模型': 'Add Model',
    '确定要添加 ': 'Are you sure you want to add ',
    '禁用': 'Disable',
    '移除': 'Remove',
    '管理员': 'Admin',
    '联系邮箱': 'Contact Email',
    '角色': 'Role',
    '请手动复制': 'Please copy manually',
    '请求失败: ': 'Request failed: ',
    '选择模型': 'Select Model',
    '通过': 'Approve',
    '邀请失败': 'Invitation failed',
    '邀请已发送': 'Invitation sent',
    '邀请邮件已发送至 ': 'Invitation email sent to ',
    '邮箱': 'Email',
    '配额': 'Quota',
    '状态': 'Status',
    '正在导出...': 'Exporting...',
}

# new_key -> desired Chinese value (only used if key not already present in zh.json)
ZH_VALUES = {
    ' items': ' 个',
    ' upstream keys, total ': ' 个上行Key，共 ',
    ' models': ' 个模型',
    ' models, skipped ': ' 个模型, 跳过 ',
    ' models?': ' 个模型吗？',
    ' models to the service model table?': ' 个模型添加到服务模型表？',
    ' (already exists)': ' 个（已存在）',
    'AWS format: access_key_id:secret_access_key (encrypted at rest)': 'AWS 格式: access_key_id:secret_access_key (将被加密存储)',
    'Keys will be encrypted at rest': 'Key将被加密存储',
    'OpenRouter sync successful! Synced ': 'OpenRouter 同步成功! 已同步 ',
    'Total ': '共 ',
    'Credit Limit': '信用额度',
    'CNY': '元',
    'Created At': '创建时间',
    'Delete': '删除',
    'Loading...': '加载中...',
    'Load failed': '加载失败',
    'Load failed: ': '加载失败: ',
    'Sync failed: ': '同步失败: ',
    'Sync successful! Updated ': '同步成功！已更新 ',
    'Name': '名称',
    'Enable': '启用',
    'Copy failed': '复制失败',
    'Pricing Multiplier': '定价倍率',
    'Export failed, please try again': '导出失败，请重试',
    'Export successful, file downloaded': '导出成功，文件已下载',
    'Joined Members': '已加入成员',
    'Copied': '已复制',
    'Copied to clipboard': '已复制到剪贴板',
    'Mapped': '已映射',
    'Credit Used': '已用信用',
    'Quota Used': '已用配额',
    'Email Confirmed': '已确认邮箱',
    'Pending Invitations': '待处理邀请',
    'Pending': '待确认',
    'Add ': '将 ',
    'Member': '成员',
    'Member Count': '成员数',
    'All Models': '所有模型',
    'Reject': '拒绝',
    'Notice': '提示',
    'Actions': '操作',
    'No members': '暂无成员',
    'No routing rules': '暂无路由规则',
    'Unmapped': '未映射',
    'Unknown error': '未知错误',
    'Add to Service Models': '添加到服务模型',
    'Added successfully! Added ': '添加成功! 已添加 ',
    'Add Model': '添加模型',
    'Are you sure you want to add ': '确定要添加 ',
    'Disable': '禁用',
    'Remove': '移除',
    'Admin': '管理员',
    'Contact Email': '联系邮箱',
    'Role': '角色',
    'Please copy manually': '请手动复制',
    'Request failed: ': '请求失败: ',
    'Select Model': '选择模型',
    'Approve': '通过',
    'Invitation failed': '邀请失败',
    'Invitation sent': '邀请已发送',
    'Invitation email sent to ': '邀请邮件已发送至 ',
    'Email': '邮箱',
    'Quota': '配额',
    'Status': '状态',
    'Exporting...': '正在导出...',
}


def replace_in_js(text):
    def repl(m):
        quote = m.group(1)
        key = m.group(2)
        new_key = KEY_MAP.get(key)
        if new_key is None:
            return m.group(0)
        # Escape the new key for insertion; it has no backslashes/quotes of the same type.
        return "_t(" + quote + new_key.replace('\\', '\\\\').replace(quote, '\\' + quote) + quote + ")"
    # Match both _t('...') and _t("...")
    return re.sub(r"_t\((['\"])(.*?)\1\)", repl, text)


def main():
    # Update JS files
    changed_files = []
    for d in JS_DIRS:
        for root, _, files in os.walk(d):
            for fn in files:
                if not fn.endswith('.js'):
                    continue
                p = os.path.join(root, fn)
                text = open(p, encoding='utf-8').read()
                new_text = replace_in_js(text)
                if new_text != text:
                    open(p, 'w', encoding='utf-8').write(new_text)
                    changed_files.append(p)
    print('Modified JS files:', changed_files)

    # Update JSON
    with open(ZH_PATH, encoding='utf-8') as f:
        zh = json.load(f)
    with open(EN_PATH, encoding='utf-8') as f:
        en = json.load(f)

    for old_key, new_key in KEY_MAP.items():
        zh.pop(old_key, None)
        en.pop(old_key, None)
        if new_key not in zh:
            zh[new_key] = ZH_VALUES.get(new_key, new_key)
        if new_key not in en:
            en[new_key] = new_key

    with open(ZH_PATH, 'w', encoding='utf-8') as f:
        json.dump(zh, f, ensure_ascii=False, indent=2)
    with open(EN_PATH, 'w', encoding='utf-8') as f:
        json.dump(en, f, ensure_ascii=False, indent=2)
    print('Updated', ZH_PATH, EN_PATH)


if __name__ == '__main__':
    main()

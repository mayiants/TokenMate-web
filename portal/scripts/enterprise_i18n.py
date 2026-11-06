#!/usr/bin/env python3
"""
Convert enterprise page i18n keys to English-default UI chrome keys (Plan B).
Target files: web/templates/enterprise/*.html and the enterprise section of
web/templates/partials/layout-sidebar.html.
"""
import json, re, os
from collections import OrderedDict

BASE = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
ZH_PATH = os.path.join(BASE, "web", "i18n", "zh.json")
EN_PATH = os.path.join(BASE, "web", "i18n", "en.json")
TPL_DIR = os.path.join(BASE, "web", "templates")

with open(ZH_PATH, "r", encoding="utf-8") as f:
    zh = json.load(f, object_pairs_hook=OrderedDict)
with open(EN_PATH, "r", encoding="utf-8") as f:
    en = json.load(f, object_pairs_hook=OrderedDict)

# Keys actually used inside enterprise templates
ENT_FILES = [os.path.join(TPL_DIR, "enterprise", x) for x in os.listdir(os.path.join(TPL_DIR, "enterprise")) if x.endswith(".html")]
ent_keys = set()
pat_t = re.compile(r'\{\{\s*t\s+["\']([^"\']+)["\']')
pat_js = re.compile(r"_t\(['\"]([^'\"]+)['\"]\)")
for path in ENT_FILES:
    text = open(path, encoding="utf-8").read()
    ent_keys.update(pat_t.findall(text))
    ent_keys.update(pat_js.findall(text))

# Also transform the enterprise-specific sidebar keys
SIDEBAR_KEYS = {
    "nav.enterprise.overview",
    "nav.enterprise.member",
    "nav.enterprise.group",
    "nav.enterprise.keys",
    "nav.enterprise.billing",
    "nav.enterprise.usage",
    "nav.enterprise.audit",
    "sidebar.enterprise_console",
}
ent_keys |= SIDEBAR_KEYS

# Start from the existing English translation as the new key, then override below.
def default_new_key(old):
    v = en.get(old)
    if v and isinstance(v, str):
        v = v.strip()
        if v and not re.search(r'[\u4e00-\u9fff]', v):
            return v
    return old

KEY_MAP = {old: default_new_key(old) for old in ent_keys}

OVERRIDES = {
    # Sidebar nav
    "nav.enterprise.member": "Members",
    "nav.enterprise.group": "Groups",
    "nav.enterprise.usage": "Usage",
    "nav.enterprise.audit": "Audit Logs",

    # Generic / shared labels with better enterprise wording
    "label.admin": "Administrator",
    "form_邮箱_6": "Email *",
    "form_选择企业分组": "Select Enterprise Group",
    "form_分配给成员": "Assigned to Members",
    "form_所属分组": "Group",
    "form_分组可见性": "Group Visibility",
    "form_密钥名称": "Key Name *",
    "form_选择平台分组": "Select Platform Group *",
    "form_状态_12": "Status",
    "form_角色_3": "Role",
    "form_角色_4": "Role",
    "form_模型": "Models",
    "form_维度": "Dimensions",
    "form_日期范围_4": "Date Range",
    "form_重新分配给": "Reassign To",

    # Titles
    "enterprise.title_quota_overview": "Quota Overview",
    "title_信用额度概览": "Credit Limit Overview",
    "title_告警历史": "Alert History",
    "title_按模型用量": "Usage by Model",
    "title_按用户": "Usage by User",
    "title_本月用量top用户": "Top Users This Month",
    "title_角色变更记录": "Role Change Log",
    "title_配额信用变更记录": "Quota / Credit Change Log",
    "title_审计日志": "Audit Logs",

    # Stats / labels
    "stat_下次重置": "Next Reset",
    "stat_企业定价倍率": "Enterprise Pricing Ratio",
    "stat_使用率": "Usage Rate",
    "stat_总费用_微美元": "Total Cost (micro-USD)",
    "stat_输入_token": "Input Tokens",
    "stat_输出_token": "Output Tokens",
    "stat_输入缓存命中": "Input Cache Hit",
    "label_已使用": "Used",
    "enterprise.rpm": "RPM",
    "enterprise.keys_monthly_requests": "Requests / Month",
    "enterprise.keys_monthly_tokens": "Tokens / Month",

    # Table headers
    "table_基于平台分组": "Platform Group",
    "table_上限值": "Upper Limit",
    "table_密钥数_4": "Keys",
    "table_token_数": "Tokens",
    "table_总_token": "Total Tokens",
    "table_费用_微美元": "Cost (micro-USD)",
    "table_输入_token": "Input Tokens",
    "table_输出_token": "Output Tokens",
    "table_占比_4": "Proportion",
    "table_占比_5": "Proportion",
    "table_占比_6": "Proportion",
    "table_用户_11": "Member",
    "table_用户_12": "Member",

    # Actions / buttons
    "action_复制邀请链接": "Copy Invite Link",
    "action_项已选择": "Items Selected",
    "action_确认删除_3": "Confirm Delete",
    "action_确认删除_4": "Confirm Delete",
    "action_确认禁用": "Disable",
    "action_确认移除": "Confirm Remove",
    "ui_删除_22": "Delete",
    "ui_删除_23": "Delete",
    "ui_编辑_10": "Edit",
    "ui_编辑_16": "Edit",
    "ui_编辑_17": "Edit",
    "ui_编辑_18": "Edit",
    "ui_查看完整密钥": "View Full Key",
    "ui_查看详情": "View Details →",
    "ui_新建分组": "Create Group",
    "ui_查询": "Filter",
    "ui_取消邀请": "Cancel Invitation",
    "ui_批量移除": "Batch Remove",
    "ui_未配置信用额度": "No Credit Limit Configured",

    # Modal titles
    "modal_添加成员": "Add Member",
    "modal_移除成员": "Remove Member",
    "modal_编辑成员": "Edit Members",
    "modal_删除分组": "Delete Group",
    "modal_禁用密钥": "Disable Key",
    "modal_删除密钥": "Delete Key",
    "modal_编辑密钥": "Edit Key",
    "modal_密钥详情": "Key Details",

    # Confirm fragments (trailing spaces live in translation values, not keys)
    "ui_确定要删除分组": "Are you sure you want to delete group",
    "ui_确定要删除密钥": "Are you sure you want to delete key",
    "ui_确定要禁用密钥": "Are you sure you want to disable key",
    "ui_确定要移除成员": "Are you sure you want to remove member",
    "ui_吗": "?",

    # Long hints / messages
    "ui_请妥善保存以下密钥关闭后无法再次查看完整密钥": "Please save this key securely. You won't be able to view the full key again after closing.",
    "ui_请立即复制保存此信息仅显示一次": "Please copy and save this information now. It will only be shown once.",
    "ui_选择企业分组可限制该成员可用的模型和调用频率": "Selecting an enterprise group limits the models and call frequency available to this member.",
    "ui_企业成员角色变更配额信用变更记录": "Records of enterprise member role changes and quota/credit changes.",

    # JS toast messages (previously untranslated or Chinese keys)
    "error.unknown": "Unknown error",
    "未知错误": "Unknown error",
    "密钥已删除": "Key deleted",
    "添加失败": "Add failed",
    "添加成功": "Added",
    "激活失败": "Activation failed",
    "用户已添加": "User added",
    "禁用失败": "Disable failed",
    "移除失败": "Remove failed",
    "邀请已发送": "Invitation sent",

    # Resolve collisions
    "status_禁用_4": "Disabled",
    "status_待处理_4": "Pending",
    "status_pending": "Pending Confirmation",
    "status_email_confirmed": "Confirmed",
    "status_已确认": "Confirmed",
    "table_变动": "Change",
    "table_变更": "Change",
    "已复制到剪贴板": "Copied to clipboard",
    "table_成员_7": "Members",
    "table_成员_8": "Members",
    "option_成员_3": "Members",
    "option_成员_4": "Members",
    "enterprise.members": "Member Count",
    "ui_移除": "Remove",
    "action_确认移除": "Confirm Remove",
    "移除成员": "Remove member",
    "common.members_selected": "members",
    "common.by": "by",
    "common.remove_selected": "Remove selected",
}

KEY_MAP.update(OVERRIDES)

# Chinese value overrides for specific new keys (if the auto-copied legacy value is poor)
ZH_OVERRIDES = {
    # Fragment trailing spaces
    "Are you sure you want to delete group": "确定要删除分组 ",
    "Are you sure you want to delete key": "确定要删除密钥 ",
    "Are you sure you want to disable key": "确定要禁用密钥 ",
    "Are you sure you want to remove member": "确定要移除成员 ",
    "?": "？",
    "? This operation is irreversible.": "？此操作不可恢复。",
    "? Requests to use this key while disabled will be denied.": "？禁用后使用该密钥的请求将被拒绝。",
    "? Their enterprise keys will be recycled upon removal.": "？移除后将回收其企业密钥。",

    "Remove selected": "移除选中的",
    "members": "位成员",
    "by": "按",

    # Better wording
    "Please save this key securely. You won't be able to view the full key again after closing.": "请妥善保存以下密钥，关闭后无法再次查看完整密钥。",
    "Please copy and save this information now. It will only be shown once.": "请立即复制保存此信息，仅显示一次。",
    "Selecting an enterprise group limits the models and call frequency available to this member.": "选择企业分组可限制该成员可用的模型和调用频率。",
    "Records of enterprise member role changes and quota/credit changes.": "企业成员角色变更、配额/信用变更记录。",
    "Enterprise Pricing Ratio": "企业定价倍率",
    "Credit Limit Overview": "信用额度概览",
    "Alert History": "告警历史",
    "Usage by Model": "按模型用量",
    "Usage by User": "按用户",
    "Top Users This Month": "本月用量TOP用户",
    "Role Change Log": "角色变更记录",
    "Quota / Credit Change Log": "配额/信用变更记录",
    "Next Reset": "下次重置",
    "Usage Rate": "使用率",
    "Total Cost (micro-USD)": "总费用 (微美元)",
    "Input Tokens": "输入 Token",
    "Output Tokens": "输出 Token",
    "Input Cache Hit": "输入缓存命中",
    "Platform Group": "基于平台分组",
    "Upper Limit": "上限值",
    "Cost (micro-USD)": "费用（微美元）",
    "Proportion": "占比",
    "Member": "用户",
    "Members": "成员",
    "Member Count": "成员数",
    "Key Name *": "密钥名称 *",
    "Select Enterprise Group": "选择企业分组",
    "Select Platform Group *": "选择平台分组 *",
    "Group Visibility": "分组可见性",
    "Assigned to Members": "分配给成员",
    "Reassign To": "重新分配给",
    "Requests / Month": "请求/月",
    "Tokens / Month": "Token/月",
    "Create Group": "新建分组",
    "View Full Key": "查看完整密钥",
    "View Details →": "查看详情 →",
    "Filter": "查询",
    "No Credit Limit Configured": "未配置信用额度",
    "Add Member": "添加成员",
    "Remove Member": "移除成员",
    "Edit Members": "编辑成员",
    "Delete Group": "删除分组",
    "Disable Key": "禁用密钥",
    "Delete Key": "删除密钥",
    "Edit Key": "编辑密钥",
    "Key Details": "密钥详情",
    "Cancel Invitation": "取消邀请",
    "Copy Invite Link": "复制邀请链接",
    "Items Selected": "项已选择",
    "Confirm Delete": "确认删除",
    "Confirm Remove": "确认移除",
    "Batch Remove": "批量移除",
    "Administrator": "管理员",
    "Email *": "邮箱 *",
    "Pending Confirmation": "待确认",
    "Suspended": "已暂停",
    "Pause": "暂停",
    "Public": "公开",
    "Disabled": "已禁用",
    "Remove member": "移除成员",
    "Remove selected": "移除选中的",
    "Key deleted": "密钥已删除",
    "Add failed": "添加失败",
    "Added": "添加成功",
    "Activation failed": "激活失败",
    "User added": "用户已添加",
    "Disable failed": "禁用失败",
    "Remove failed": "移除失败",
    "Invitation sent": "邀请已发送",
    "Unknown error": "未知错误",
    "Copied to clipboard": "已复制到剪贴板",
    "Cancel failed": "取消失败",
    "Invitation cancelled": "邀请已取消",
    "Invite link copied": "邀请链接已复制",
    "Cancelled": "已取消",
    "Records of corporate member role changes and quota/credit changes.": "企业成员角色变更、配额/信用变更记录。",
    "Batch remove feature coming soon": "批量移除功能即将上线",
    "No groups yet.": "暂无分组。",
    "No data": "暂无数据",
    "No keys": "暂无密钥",
    "No members": "暂无成员",
    "No record of role changes yet": "暂无角色变更记录",
    "No quota/credit change record yet": "暂无配额/信用变更记录",
    "No usage data": "暂无用量数据",
    "No invoices": "暂无账单",
    "I have saved": "我已保存",
    "Not assigned": "未分配",
    "Unlimited": "无限制",
    "This Month": "本月",
    "Admin Operation": "管理员操作",
    "Please select a platform group first": "请先选择平台分组",
    "Deselect": "取消选择",
    "Grouping based on platform": "基于平台分组",
    "Number of keys": "密钥数",
    "Number of Tokens": "Token 数",
    "Quota cap": "配额上限",
    "Upper Limit Value": "上限值",
    "Current Value": "当前值",
    "Field": "字段",
    "Pending Invitations": "待处理邀请",
    "Expires At": "过期时间",
    "Contact Email": "联系邮箱",
    "Enterprise Name": "企业名称",
    "Threshold": "阈值",
    "Fees (micro-dollars)": "费用（微美元）",
    "Total Cost (Micro Dollars)": "总费用 (微美元)",
    "Reset Next Time": "下次重置",
    "Pricing Ratio": "企业定价倍率",
    "Usage rate": "使用率",
    "Billing Cycle": "计费周期",
    "Consumed": "已消费",
    "Remaining": "剩余",
    "Total Amount": "总额度",
    "Alarm history": "告警历史",
    "Role change record": "角色变更记录",
    "Quota/credit change history": "配额/信用变更记录",
    "Credit limit overview": "信用额度概览",
    "Dosage by model": "按模型用量",
    "Top users by usage this month": "本月用量TOP用户",
    "By User": "按用户",
    "No enterprise admin assigned. Contact platform admin. Members can use existing keys but cannot manage.": "当前无企业管理员，请联系平台管理员指派。成员可正常使用现有密钥，但无法进行管理操作。",
    "Enterprise suspended. Contact platform admin to restore. Existing keys still work but management is disabled.": "企业运营禁用，请联系平台管理员恢复。现有密钥仍可使用，但无法进行管理操作。",
    "Enterprise info updated": "企业信息已更新",
    "Cancel invitation to {email}?": "确定取消对 {email} 的邀请吗？",
    "Public - visible to all members, members can create their own keys and use them": "公开 — 所有成员可见，成员可自行创建密钥使用",
    "Admin Only - Assignable only by admins, hidden from members": "管理员专用 — 仅管理员可分配，对成员隐藏",
    "Not specified (use the default method)": "不指定（使用默认方式）",
    "Not specified": "不指定",
    "-- Select platform enterprise group --": "-- 选择平台企业分组 --",
    "Select business group": "选择企业分组",
    "Select Model": "选择模型",
    "Start Date ~ End Date": "开始日期 ~ 结束日期",
    "Search name/key name...": "搜索名称/密钥名...",
    "Search mailbox": "搜索邮箱",
    "Optional": "可选",
    "Member display name": "成员显示名称",
    "For example: production environment key": "例如：生产环境密钥",
    "For example: R&D team-production environment": "例如：研发团队-生产环境",
    "0 means no limit": "0 表示无限制",
    "0 = Unlimited": "0 = 不限制",
    "Group to which it belongs": "所属分组",
    "Assigned To Members": "分配给成员",
    "Date Range": "日期范围",
    "Models": "模型",
    "Dimensions": "维度",
    "Role": "角色",
    "Mail *": "邮箱 *",
    "Name *": "名称 *",
    "Status": "状态",
    "RPM (requests per minute)": "RPM (每分钟请求数)",
    "TPM (Tokens per minute)": "TPM (每分钟Token数)",
    "Token quota limit (0=no limit)": "Token配额上限（0=无限制）",
    "Double-click to copy model name": "双击复制模型名",
    "Member name will be applied to existing users or used as the default name for invitation recipients.": "成员姓名会对已存在用户生效，或作为被邀请人接受邀请时的默认姓名。",
    "Invoice ID": "账单ID",
    "System": "系统",
    "Creation failed": "创建失败",
    "Failed to retrieve key": "获取密钥失败",
    "Load failed": "加载失败",
    "Operation failed": "操作失败",
    "Please enter email": "请输入邮箱",
    "Please enter an enterprise name": "请输入企业名称",
    "Please enter a key name": "请填写密钥名称",
    "Please select a member": "请选择分配成员",
    "Request failed": "请求失败",
    "Save failed": "保存失败",
    "Credit Exceeded": "信用超限",
    "Credit Warning": "信用告警",
    "Quota Exceeded": "配额超限",
    "Quota Warning": "配额告警",
    "Credit Top-up": "信用充值",
    "Monthly Reset": "月度重置",
    "Quota Top-up": "配额充值",
    "Request Usage": "请求用量",
    "Token Usage": "Token用量",
    "Balance": "余额",
    "Active Keys": "激活密钥",
    "Export Records": "导出记录",
    "Billing History": "账单历史",
    "Billing Management": "账单管理",
    "Edit Enterprise Info": "编辑企业信息",
    "Enterprise Ratio": "企业倍率",
    "Group Management": "分组管理",
    "Total Groups": "分组总数",
    "Key Management": "密钥管理",
    "Total Keys": "密钥总数",
    "Member Management": "成员管理",
    "No invoices": "暂无账单",
    "Overview": "总览",
    "% of Total": "占比",
    "Platform Ratio": "平台倍率",
    "Remaining Quota": "剩余配额",
    "Total Quota": "配额总量",
    "Quota Used": "配额使用",
    "15 Days": "15天",
    "1 Day": "1天",
    "30 Days": "30天",
    "7 Days": "7天",
    "Quarter": "按季",
    "Analytics": "分析",
    "Spend": "花费",
    "Users": "用户",
    "Total Requests": "总请求数",
    "Total Tokens": "总Token数",
    "Usage by User": "用户用量分布",
    "Usage Details": "用量明细",
    "Monthly Usage": "本月用量",
    "User": "用户",
    "Requests": "请求数",
    "Tokens": "Token数",
    "All": "全部",
    "Normal": "正常",
    "Active": "活跃",
    "Confirm you want to delete the group": "确定要删除分组",
    "Are you sure you want to delete the key?": "确定要删除密钥",
    "Confirm you want to disable the key": "确定要禁用密钥",
    "Confirm you want to remove member": "确定要移除成员",
    "New": "新建",
    "Add": "添加",
    "Save": "保存",
    "Create": "创建",
    "Delete": "删除",
    "Disable": "禁用",
    "Remove": "移除",
    "Close": "关闭",
    "Cancel": "取消",
    "Copy": "复制",
    "Export": "导出",
    "Edit": "编辑",
    "Loading...": "加载中...",
    "Enterprise": "企业",
    "Enterprise Billing": "企业账单",
    "Enterprise Keys": "企业密钥",
    "Enterprise Console": "企业管理",
    "Audit Logs": "审计日志",
    "Keys": "密钥",
    "Groups": "分组",
    "Usage": "用量明细",
}

# Apply overrides
for old, new in OVERRIDES.items():
    if old in KEY_MAP:
        KEY_MAP[old] = new

# Clean up: if new key is the same as old (e.g. missing translation), keep old? Actually we want English keys.
# If new == old and old is not English, leave it unchanged to avoid breaking.
KEY_MAP = {old: new for old, new in KEY_MAP.items() if old != new}

# Files to rewrite
TARGET_FILES = ENT_FILES + [os.path.join(TPL_DIR, "partials", "layout-sidebar.html")]

def replace_in_text(text):
    # Replace {{t "old"}} / {{t 'old'}} preserving quote style
    def repl_t(m):
        quote = m.group(1)
        key = m.group(2)
        if key in KEY_MAP:
            return f'{{{{t {quote}{KEY_MAP[key]}{quote}}}}}'
        return m.group(0)
    text = re.sub(r'\{\{\s*t\s+(["\'])([^"\']+)\1\s*\}\}', repl_t, text)

    # Replace _t('old') / _t("old")
    def repl_js(m):
        quote = m.group(1)
        key = m.group(2)
        if key in KEY_MAP:
            return f'_t({quote}{KEY_MAP[key]}{quote})'
        return m.group(0)
    text = re.sub(r"_t\(([\"'])([^\"']+)\1\)", repl_js, text)
    return text

for path in TARGET_FILES:
    text = open(path, encoding="utf-8").read()
    new_text = replace_in_text(text)
    if new_text != text:
        with open(path, "w", encoding="utf-8") as f:
            f.write(new_text)

# Update dictionaries
for old, new in KEY_MAP.items():
    if not new:
        continue
    # Chinese value: override > old zh value > fallback to new key
    zh_val = ZH_OVERRIDES.get(new)
    if zh_val is None:
        zh_val = zh.get(old)
    if zh_val is None:
        zh_val = new
    # Only set zh if not already present (preserve existing user-page translations)
    if new not in zh:
        zh[new] = zh_val
    # English value is always the key itself (identity)
    if new not in en:
        en[new] = new

with open(ZH_PATH, "w", encoding="utf-8") as f:
    json.dump(zh, f, ensure_ascii=False, indent=2)
    f.write("\n")
with open(EN_PATH, "w", encoding="utf-8") as f:
    json.dump(en, f, ensure_ascii=False, indent=2)
    f.write("\n")

print(f"Transformed {len(KEY_MAP)} keys in {len(TARGET_FILES)} files.")
print("Sample mapping:")
for old in sorted(KEY_MAP)[:20]:
    print(f"  {old!r} -> {KEY_MAP[old]!r}")

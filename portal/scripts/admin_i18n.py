#!/usr/bin/env python3
"""
Convert admin page i18n keys to English-default UI chrome keys (Plan B).
Target files: web/templates/admin*.html and the admin section of
web/templates/partials/layout-sidebar.html.
"""
import json, re, os
from collections import defaultdict

BASE = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
ZH_PATH = os.path.join(BASE, "web", "i18n", "zh.json")
EN_PATH = os.path.join(BASE, "web", "i18n", "en.json")
TPL_DIR = os.path.join(BASE, "web", "templates")

with open(ZH_PATH, "r", encoding="utf-8") as f:
    zh = json.load(f, object_pairs_hook=OrderedDict) if 'OrderedDict' in globals() else json.load(f)
with open(EN_PATH, "r", encoding="utf-8") as f:
    en = json.load(f, object_pairs_hook=OrderedDict) if 'OrderedDict' in globals() else json.load(f)

# OrderedDict fallback
from collections import OrderedDict
if not isinstance(zh, OrderedDict):
    zh = OrderedDict(zh)
if not isinstance(en, OrderedDict):
    en = OrderedDict(en)

# Parse EN_MAP from translate_templates.py
src = open(os.path.join(BASE, "scripts", "translate_templates.py"), encoding="utf-8").read()
EN_MAP = {}
m = re.search(r'EN_MAP = \{.*?\n\}', src, re.S)
if m:
    EN_MAP = eval(m.group(0).split('=', 1)[1])

ADMIN_FILES = [os.path.join(TPL_DIR, x) for x in os.listdir(TPL_DIR) if x.startswith('admin-') and x.endswith('.html')]
ADMIN_FILES.append(os.path.join(TPL_DIR, "admin.html"))

# Keys in admin templates
admin_keys = set()
pat_t = re.compile(r'\{\{\s*t\s+["\']([^"\']+)["\']')
pat_js = re.compile(r"_t\(['\"]([^'\"]+)['\"]\)")
for path in ADMIN_FILES:
    text = open(path, encoding="utf-8").read()
    admin_keys.update(pat_t.findall(text))
    admin_keys.update(pat_js.findall(text))

# Sidebar admin keys
SIDEBAR_ADMIN_KEYS = {
    "nav.admin.overview", "nav.admin.users", "nav.admin.groups", "nav.admin.enterprise",
    "nav.admin.keypool", "nav.admin.models", "nav.admin.models_aws", "nav.admin.providers",
    "nav.admin.subscriptions", "nav.admin.vouchers", "nav.admin.announcements",
    "nav.admin.audit_logs", "nav.admin.settings", "nav.admin.monitor_upstream",
    "nav.admin.monitor_downstream", "nav.admin.upstream_usage", "nav.admin.provider_health",
    "nav.admin.rate_limits", "nav.admin.reconciliation", "sidebar.system_console",
}
admin_keys |= SIDEBAR_ADMIN_KEYS

# Reverse index: Chinese value -> English candidates
rev = defaultdict(list)
for k, v in zh.items():
    ev = en.get(k)
    if ev and isinstance(ev, str) and not re.search(r'[\u4e00-\u9fff]', ev):
        rev[v.strip()].append((k, ev.strip()))


def auto_english(k):
    v = en.get(k)
    if v and isinstance(v, str):
        vs = v.strip()
        if vs and not re.search(r'[\u4e00-\u9fff]', vs):
            return vs
    if k in EN_MAP:
        return EN_MAP[k]
    zv = zh.get(k, k).strip()
    cands = rev.get(zv)
    if cands:
        # Prefer shortest/cleanest English value
        cands = sorted(cands, key=lambda x: (len(x[1].split()), len(x[1]), x[1]))
        return cands[0][1]
    return None


KEY_MAP = {}
for k in admin_keys:
    e = auto_english(k)
    if e:
        KEY_MAP[k] = e

# Manual overrides for keys that could not be auto-mapped or need disambiguation
MANUAL = {
    # Unmapped fragments / Chinese-only keys
    " 个": "items",
    " 个Key, 跳过 ": " keys, skipped ",
    " 条记录": " records",
    " 条，第 ": " entries, page ",
    "AWS Bedrock Runtime 需要 AK/SK 格式: ACCESS_KEY_ID:SECRET_ACCESS_KEY": "AWS Bedrock Runtime requires AK/SK format: ACCESS_KEY_ID:SECRET_ACCESS_KEY",
    "AWS 格式: access_key_id:secret_access_key (将被加密存储)": "AWS format: access_key_id:secret_access_key (will be encrypted at rest)",
    "Key将被加密存储": "Keys will be encrypted at rest",
    "Key已添加": "Key added",
    "form_在商店显示": "Show in Store",
    "form_排序_8": "Sort Order",
    "上一页": "Previous",
    "下一页": "Next",
    "企业信息已更新": "Enterprise info updated",
    "企业已创建": "Enterprise created",
    "企业配额/信用已更新": "Enterprise quota/credit updated",
    "优先级已更新": "Priority updated",
    "供应商信息已更新": "Provider info updated",
    "供应商已": "Provider ",
    "供应商已成功添加": "Provider added successfully",
    "促销": "Promotion",
    "信用额度": "Credit Limit",
    "充值失败": "Recharge failed",
    "充值成功": "Recharged",
    "兑换": "Redeem",
    "共 ": "Total ",
    "创建时间": "Created At",
    "删除": "Delete",
    "删除用户组": "Delete User Group",
    "加载失败: ": "Load failed: ",
    "加载详情失败: ": "Failed to load details: ",
    "单供应商导入模板": "Single-provider import template",
    "受邀者奖励": "Invitee Reward",
    "名称": "Name",
    "天": "Days",
    "失效": "Invalid",
    "姓名": "Name",
    "定价倍率": "Pricing Multiplier",
    "审批失败": "Approval failed",
    "导入成功! 已添加 ": "Import successful! Added ",
    "尚未配置欢迎码": "Welcome code not configured",
    "已加入成员": "Joined Members",
    "已用信用": "Credit Used",
    "已用完": "Exhausted",
    "已用配额": "Quota Used",
    "已确认邮箱": "Email Confirmed",
    "平均延迟": "Average Latency",
    "延迟(ms)": "Latency (ms)",
    "引导": "Guide",
    "引导向导": "Onboarding Guide",
    "弹窗": "Popup",
    "当前值由系统每 8 小时自动从 API 获取更新。手动修改仅作为临时 fallback，下次自动更新后会被覆盖": "Current value is auto-updated from the API every 8 hours. Manual edits are temporary and will be overwritten on next sync.",
    "当前状态基于列表页选定的时间范围：": "Current status is based on the time range selected on the list page:",
    "待处理邀请": "Pending Invitations",
    "成功": "Success",
    "成功率": "Success Rate",
    "成员数": "Member Count",
    "拒绝": "Reject",
    "拒绝失败": "Reject failed",
    "故障转移": "Failover",
    "敏感词列表": "Sensitive Word List",
    "敏感词列表，用逗号分隔": "Sensitive word list, comma-separated",
    "敏感词设置": "Sensitive Word Settings",
    "新建公告": "New Announcement",
    "无法找到供应商数据": "No provider data found",
    "暂无历史数据": "No historical data",
    "暂无成员": "No members",
    "暂无记录": "No records",
    "暂无配置": "No configuration",
    "最大奖励": "Max Reward",
    "最大重试": "Max Retries",
    "最小奖励": "Min Reward",
    "有效": "Active",
    "格式: 供应商,Key,Base URL,RPM限制,TPM限制,激活状态": "Format: Provider,Key,Base URL,RPM Limit,TPM Limit,Active Status",
    "格式错误": "Format error",
    "检测 API 响应中的敏感词": "Detect sensitive words in API responses",
    "检测用户输入中的敏感词": "Detect sensitive words in user input",
    "欢迎": "Welcome",
    "欢迎码": "Welcome Code",
    "正在批量验证Key...": "Batch verifying keys...",
    "正在验证Key...": "Verifying key...",
    "此用户组吗？": " this user group?",
    "每人最大码数": "Max codes per user",
    "每行一个Key，无需其他信息": "One key per line, no other information needed",
    "永久有效": "Permanent",
    "没有匹配的日志": "No matching logs",
    "混合供应商导入模板": "Mixed-provider import template",
    "添加失败": "Add failed",
    "添加成功": "Added",
    "激活": "Activate",
    "激活失败": "Activation failed",
    "激活每日签到奖励": "Enable daily check-in rewards",
    "激活自动故障转移": "Enable automatic failover",
    "状态": "Status",
    "用户已删除": "User deleted",
    "确定删除吗？": "Confirm deletion?",
    "确定要": "Are you sure you want to ",
    "确定要修改用户角色吗？": "Are you sure you want to change this user's role?",
    "确定要拒绝该邀请吗？": "Are you sure you want to reject this invitation?",
    "确定要移除此成员吗？": "Are you sure you want to remove this member?",
    "确认": "Confirm",
    "确认拒绝": "Confirm Reject",
    "确认激活": "Confirm Activate",
    "确认禁用": "Confirm Disable",
    "确认移除": "Confirm Remove",
    "礼品": "Gift",
    "禁用失败": "Disable failed",
    "移除": "Remove",
    "移除失败": "Remove failed",
    "签到功能": "Check-in Feature",
    "签到最大 Token 奖励": "Check-in max Token reward",
    "签到最小 Token 奖励": "Check-in min Token reward",
    "签到设置": "Check-in Settings",
    "编辑公告": "Edit Announcement",
    "编辑失败": "Edit failed",
    "编辑成功": "Edited",
    "美元汇率": "USD Exchange Rate",
    "联系邮箱": "Contact Email",
    "角色": "Role",
    "计费设置": "Billing Settings",
    "详情": "Details",
    "请求数": "Requests",
    "请求量": "Request Volume",
    "请至少填写一项充值金额": "Please enter at least one recharge amount",
    "请输入名称和金额": "Please enter name and amount",
    "请输入邮箱": "Please enter email",
    "路由失败后最大重试次数": "Max retries after routing failure",
    "路由策略": "Routing Strategy",
    "路由设置": "Routing Settings",
    "输入检测": "Input Detection",
    "输出检测": "Output Detection",
    "通过": "Approve",
    "邀请人": "Inviter",
    "邀请失败": "Invitation failed",
    "邀请已拒绝": "Invitation rejected",
    "邀请已通过": "Invitation approved",
    "邀请者奖励": "Inviter Reward",
    "邀请邮件已发送至 ": "Invitation email sent to ",
    "邮箱": "Email",
    "配额": "Quota",
    "重试之间的延迟毫秒数": "Delay between retries (milliseconds)",
    "重试延迟(ms)": "Retry Delay (ms)",
    "错误率": "Error Rate",
    "错误率(%)": "Error Rate (%)",
    "顶部横幅": "Top Banner",
    "验证中": "Verifying",
    "默认使用次数": "Default Max Uses",
    "默认有效期": "Default Validity",
    "默认路由策略（priority / round-robin）": "Default routing strategy (priority / round-robin)",

    # Disambiguation / better wording
    "action_确认删除": "Confirm Delete",
    "action_确认启用": "Confirm Enable",
    "action_确认禁用": "Confirm Disable",
    "action_确认移除": "Confirm Remove",
    "modal_确认删除": "Confirm Delete",
    "status_待处理_4": "Pending",
    "status_pending": "Pending Confirmation",
    "stat_待确认_4": "Pending Confirmation",
    "option_待确认": "Pending Confirmation",
    "待处理": "Pending",
    "待确认": "Pending Confirmation",
    "成员数": "Member Count",
    "table_成员数": "Member Count",
    "status_禁用_6": "Disabled",
    "ui_禁用": "Disable",
    "option_禁用_7": "Disabled",
    "option_禁用_11": "Disabled",
    "status.disabled": "Disabled",
    "label_已禁用": "Disabled",
    "禁用成功": "Disabled",
    "禁用": "Disable",
    "option_活跃_10": "Active",
    "status_活跃": "Active",
    "status_活跃_4": "Active",
    "stat_active": "Active",
    "status.active": "Active",
    "有效": "Active",
    "option_无效": "Invalid",
    "status_无效": "Invalid",
    "失效": "Invalid",
    "option_企业_9": "Enterprise",
    "option_企业_6": "Enterprise",
    "table_所属企业": "Enterprise",
    "ui_企业": "Enterprise",
    "option_enterprise企业_4": "Enterprise",
    "option_折扣_6": "Economy",
    "option_折扣_5": "Economy",
    "option_折扣_4": "Economy",
    "label_折扣": "Economy",
    "option_economy经济_4": "Economy",
    "option_标准_5": "General",
    "option_标准_7": "General",
    "option_标准_6": "General",
    "option_标准_8": "General",
    "label_标准": "General",
    "table_状态_35": "Status",
    "table_状态_41": "Status",
    "form_状态_7": "Status",
    "form_status": "Status",
    "状态": "Status",
    "table_类型_14": "Type",
    "table_类型_20": "Type",
    "table_类型_16": "Type",
    "form_类型_9": "Type",
    "form_类型_10": "Type",
    "table_操作_21": "Actions",
    "table_操作_26": "Actions",
    "table_操作_27": "Actions",
    "table_操作_19": "Actions",
    "table_操作_22": "Actions",
    "table_操作_23": "Actions",
    "ui_取消_51": "Cancel",
    "ui_取消_46": "Cancel",
    "ui_取消_44": "Cancel",
    "ui_取消_45": "Cancel",
    "ui_取消": "Cancel",
    "action_create": "Create",
    "action_创建_8": "Create",
    "action_创建_10": "Create",
    "action_创建_7": "Create",
    "action_save": "Save",
    "common.save": "Save",
    "ui_保存_11": "Save",
    "action_保存_12": "Save",
    "table_名称_13": "Name",
    "table_名称_15": "Name",
    "table_名称_14": "Name",
    "table_名称_17": "Name",
    "form_name": "Name",
    "table_用户_10": "User",
    "table_user": "User",
    "table_用户_8": "User",
    "table_用户_2": "User",
    "table_用户_7": "User",
    "table_成员_5": "Members",
    "ui_成员": "Members",
    "table_成员_6": "Members",
    "table_时间_11": "Time",
    "时间": "Time",
    "table_时间_13": "Time",
    "table_time": "Time",
    "table_时间_14": "Time",
    "table_创建时间_9": "Created At",
    "table_创建时间_13": "Created At",
    "table_创建时间_14": "Created At",
    "创建时间": "Created At",
    "ui_删除_24": "Delete",
    "ui_删除_13": "Delete",
    "ui_删除_17": "Delete",
    "ui_编辑_15": "Edit",
    "common.edit": "Edit",
    "title_编辑_3": "Edit",
    "ui_编辑_11": "Edit",
    "ui_编辑_12": "Edit",
    "ui_下一页_25": "Next",
    "ui_下一页_23": "Next",
    "ui_下一页_20": "Next",
    "ui_next": "Next",
    "ui_上一页_23": "Previous",
    "ui_上一页_16": "Previous",
    "ui_上一页_17": "Previous",
    "上一页": "Previous",
    "ui_上一页_25": "Previous",
    "ui_上一页_17": "Previous",
    "option_全部状态_7": "All Statuses",
    "option_所有状态_4": "All Statuses",
    "option_全部状态": "All Statuses",
    "option_全部状态_8": "All Statuses",
    "option_全部状态_10": "All Statuses",
    "option_全部类型_4": "All Types",
    "option_全部类型_3": "All Types",
    "option_所有类型_4": "All Types",
    "option_all_types": "All Types",
    "option_全部_provider": "All Providers",
    "option_所有供应商_6": "All Providers",
    "option_所有供应商_5": "All Providers",
    "option_所有供应商_4": "All Providers",
    "table_供应商_3": "Provider",
    "form_供应商_7": "Provider",
    "form_供应商_5": "Provider",
    "table_供应商_4": "Provider",
    "form_供应商_6": "Provider",
    "table_倍率_7": "Multiplier",
    "table_倍率_8": "Multiplier",
    "table_倍率_6": "Multiplier",
    "form_倍率": "Multiplier",
    "table_请求数_7": "Requests",
    "table_请求数_9": "Requests",
    "请求数": "Requests",
    "table_请求数_8": "Requests",
    "table_路由通道_4": "Routing Track",
    "form_路由通道_3": "Routing Track",
    "form_路由通道_4": "Routing Track",
    "table_路由通道_3": "Routing Track",
    "table_配额_3": "Quota",
    "table_配额": "Quota",
    "table_配额_4": "Quota",
    "form_配额": "Quota",
    "配额": "Quota",
    "form_计费": "Billing",
    "table_计费_3": "Billing",
    "table_计费_4": "Billing",
    "option_账单": "Billing",
    "option_套餐_5": "Subscription",
    "option_套餐_8": "Subscription",
    "option_套餐_6": "Subscription",
    "ui_订阅_3": "Subscription",
    "label_套餐": "Subscription",
    "ui_刷新_5": "Refresh",
    "ui_刷新_6": "Refresh",
    "ui_刷新_7": "Refresh",
    "action_刷新": "Refresh",
    "ui_刷新_8": "Refresh",
    "ui_添加": "Add",
    "action_添加_8": "Add",
    "action_添加_7": "Add",
    "action_添加_6": "Add",
    "announcement.dismiss_permanent": "Dismiss Permanently",
    "永久有效": "Permanent",
    "ui_永久": "Permanent",
    "stat_总密钥数_4": "Total Keys",
    "stat_总密钥数_3": "Total Keys",
    "stat_总key数": "Total Keys",
    "stat_活跃密钥_4": "Active Keys",
    "stat_活跃key": "Active Keys",
    "stat_活跃密钥_6": "Active Keys",
    "stat_活跃密钥_5": "Active Keys",
    "ui_留空使用默认url_4": "Leave empty for default URL",
    "ui_留空使用默认url_3": "Leave empty for default URL",
    "placeholder_留空使用默认url": "Leave empty for default URL",

    # Sidebar nav wording
    "nav.admin.overview": "Overview",
    "nav.admin.users": "User Management",
    "nav.admin.groups": "User Groups",
    "nav.admin.enterprise": "Enterprise Management",
    "nav.admin.keypool": "API Key Pool",
    "nav.admin.models": "Model Management",
    "nav.admin.models_aws": "AWS Models",
    "nav.admin.providers": "Provider Management",
    "nav.admin.subscriptions": "Subscription Plans",
    "nav.admin.vouchers": "Voucher Management",
    "nav.admin.announcements": "Announcements",
    "nav.admin.audit_logs": "Audit Logs",
    "nav.admin.settings": "System Settings",
    "nav.admin.monitor_upstream": "Upstream Monitor",
    "nav.admin.monitor_downstream": "Downstream Monitor",
    "nav.admin.upstream_usage": "Upstream Usage",
    "nav.admin.provider_health": "Provider Health",
    "nav.admin.rate_limits": "Rate Limits",
    "nav.admin.reconciliation": "Reconciliation & Invoices",
    "sidebar.system_console": "System Console",
}

KEY_MAP.update(MANUAL)

# Remove self-mappings and empty keys
KEY_MAP = {old: new for old, new in KEY_MAP.items() if old != new and new}

# Chinese value overrides for new keys
ZH_OVERRIDES = {
    "items": " 个",
    " keys, skipped ": " 个Key, 跳过 ",
    " records": " 条记录",
    " entries, page ": " 条，第 ",
    "AWS Bedrock Runtime requires AK/SK format: ACCESS_KEY_ID:SECRET_ACCESS_KEY": "AWS Bedrock Runtime 需要 AK/SK 格式: ACCESS_KEY_ID:SECRET_ACCESS_KEY",
    "AWS format: access_key_id:secret_access_key (will be encrypted at rest)": "AWS 格式: access_key_id:secret_access_key (将被加密存储)",
    "Keys will be encrypted at rest": "Key将被加密存储",
    "Key added": "Key已添加",
    "Show in Store": "在商店显示",
    "Sort Order": "排序",
    "Enterprise created": "企业已创建",
    "Enterprise quota/credit updated": "企业配额/信用已更新",
    "Priority updated": "优先级已更新",
    "Provider info updated": "供应商信息已更新",
    "Provider ": "供应商已",
    "Provider added successfully": "供应商已成功添加",
    "Promotion": "促销",
    "Recharge failed": "充值失败",
    "Recharged": "充值成功",
    "Redeem": "兑换",
    "Total ": "共 ",
    "Delete User Group": "删除用户组",
    "Load failed: ": "加载失败: ",
    "Failed to load details: ": "加载详情失败: ",
    "Single-provider import template": "单供应商导入模板",
    "Invitee Reward": "受邀者奖励",
    "Days": "天",
    "Pricing Multiplier": "定价倍率",
    "Approval failed": "审批失败",
    "Import successful! Added ": "导入成功! 已添加 ",
    "Welcome code not configured": "尚未配置欢迎码",
    "Joined Members": "已加入成员",
    "Credit Used": "已用信用",
    "Exhausted": "已用完",
    "Quota Used": "已用配额",
    "Email Confirmed": "已确认邮箱",
    "Average Latency": "平均延迟",
    "Latency (ms)": "延迟(ms)",
    "Guide": "引导",
    "Onboarding Guide": "引导向导",
    "Popup": "弹窗",
    "Current value is auto-updated from the API every 8 hours. Manual edits are temporary and will be overwritten on next sync.": "当前值由系统每 8 小时自动从 API 获取更新。手动修改仅作为临时 fallback，下次自动更新后会被覆盖",
    "Current status is based on the time range selected on the list page:": "当前状态基于列表页选定的时间范围：",
    "Pending Invitations": "待处理邀请",
    "Success": "成功",
    "Success Rate": "成功率",
    "Member Count": "成员数",
    "Reject": "拒绝",
    "Reject failed": "拒绝失败",
    "Failover": "故障转移",
    "Sensitive Word List": "敏感词列表",
    "Sensitive word list, comma-separated": "敏感词列表，用逗号分隔",
    "Sensitive Word Settings": "敏感词设置",
    "New Announcement": "新建公告",
    "No provider data found": "无法找到供应商数据",
    "No historical data": "暂无历史数据",
    "No records": "暂无记录",
    "No configuration": "暂无配置",
    "Max Reward": "最大奖励",
    "Max Retries": "最大重试",
    "Min Reward": "最小奖励",
    "Format: Provider,Key,Base URL,RPM Limit,TPM Limit,Active Status": "格式: 供应商,Key,Base URL,RPM限制,TPM限制,激活状态",
    "Format error": "格式错误",
    "Detect sensitive words in API responses": "检测 API 响应中的敏感词",
    "Detect sensitive words in user input": "检测用户输入中的敏感词",
    "Welcome": "欢迎",
    "Welcome Code": "欢迎码",
    "Batch verifying keys...": "正在批量验证Key...",
    "Verifying key...": "正在验证Key...",
    " this user group?": "此用户组吗？",
    "Max codes per user": "每人最大码数",
    "One key per line, no other information needed": "每行一个Key，无需其他信息",
    "Mixed-provider import template": "混合供应商导入模板",
    "Add failed": "添加失败",
    "Added": "添加成功",
    "Activate": "激活",
    "Activation failed": "激活失败",
    "Enable daily check-in rewards": "激活每日签到奖励",
    "Enable automatic failover": "激活自动故障转移",
    "User deleted": "用户已删除",
    "Confirm deletion?": "确定删除吗？",
    "Are you sure you want to ": "确定要",
    "Are you sure you want to change this user's role?": "确定要修改用户角色吗？",
    "Are you sure you want to reject this invitation?": "确定要拒绝该邀请吗？",
    "Are you sure you want to remove this member?": "确定要移除此成员吗？",
    "Confirm": "确认",
    "Confirm Reject": "确认拒绝",
    "Confirm Activate": "确认激活",
    "Confirm Disable": "确认禁用",
    "Confirm Remove": "确认移除",
    "Gift": "礼品",
    "Disable failed": "禁用失败",
    "Remove failed": "移除失败",
    "Check-in Feature": "签到功能",
    "Check-in max Token reward": "签到最大 Token 奖励",
    "Check-in min Token reward": "签到最小 Token 奖励",
    "Check-in Settings": "签到设置",
    "Edit Announcement": "编辑公告",
    "Edit failed": "编辑失败",
    "Edited": "编辑成功",
    "USD Exchange Rate": "美元汇率",
    "Billing Settings": "计费设置",
    "Request Volume": "请求量",
    "Please enter at least one recharge amount": "请至少填写一项充值金额",
    "Please enter name and amount": "请输入名称和金额",
    "Please enter email": "请输入邮箱",
    "Max retries after routing failure": "路由失败后最大重试次数",
    "Routing Strategy": "路由策略",
    "Routing Settings": "路由设置",
    "Input Detection": "输入检测",
    "Output Detection": "输出检测",
    "Approve": "通过",
    "Inviter": "邀请人",
    "Invitation failed": "邀请失败",
    "Invitation rejected": "邀请已拒绝",
    "Invitation approved": "邀请已通过",
    "Inviter Reward": "邀请者奖励",
    "Invitation email sent to ": "邀请邮件已发送至 ",
    "Quota": "配额",
    "Delay between retries (milliseconds)": "重试之间的延迟毫秒数",
    "Retry Delay (ms)": "重试延迟(ms)",
    "Error Rate": "错误率",
    "Error Rate (%)": "错误率(%)",
    "Top Banner": "顶部横幅",
    "Verifying": "验证中",
    "Default Max Uses": "默认使用次数",
    "Default Validity": "默认有效期",
    "Default routing strategy (priority / round-robin)": "默认路由策略（priority / round-robin）",

    # Colors
    "Primary (Blue)": "Primary (蓝)",
    "Success (Green)": "Success (绿)",
    "Warning (Yellow)": "Warning (黄)",
    "Danger (Red)": "Danger (红)",

    # Disambiguation values
    "Pending": "待处理",
    "Pending Confirmation": "待确认",
    "Member Count": "成员数",
    "Disabled": "已禁用",
    "Disable": "禁用",
    "Active": "激活",
    "Invalid": "无效",
    "Dismiss Permanently": "永久关闭",
    "All Statuses": "全部状态",
    "All Types": "全部类型",
    "All Providers": "所有供应商",
    "Previous": "上一页",
    "Next": "下一页",
    "Total Keys": "总密钥数",
    "Active Keys": "激活密钥",
    "Leave empty for default URL": "留空使用默认URL",
    "System Console": "系统管理",
    "Reconciliation & Invoices": "对账与发票",
    "Rate Limits": "限流使用统计",
    "Provider Health": "供应商状态监控",
    "Upstream Usage": "上行Key使用统计",
    "Upstream Monitor": "上行Key监控",
    "Downstream Monitor": "下行Key监控",
    "System Settings": "系统设置",
    "Subscription Plans": "订阅计划",
    "Voucher Management": "兑换码管理",
    "Announcements": "公告管理",
    "Enterprise Management": "企业管理",
    "User Groups": "用户组管理",
    "API Key Pool": "API密钥池",
    "Model Management": "模型管理",
    "AWS Models": "AWS 模型管理",
    "Provider Management": "供应商管理",
    "User Management": "用户管理",
    "Overview": "总览",
}

TARGET_FILES = ADMIN_FILES + [os.path.join(TPL_DIR, "partials", "layout-sidebar.html")]


def replace_in_text(text):
    # Hardcoded color options in admin-announcements.html
    text = text.replace('Primary (蓝)', '{{t "Primary (Blue)"}}')
    text = text.replace('Success (绿)', '{{t "Success (Green)"}}')
    text = text.replace('Warning (黄)', '{{t "Warning (Yellow)"}}')
    text = text.replace('Danger (红)', '{{t "Danger (Red)"}}')

    def repl_t(m):
        quote = m.group(1)
        key = m.group(2)
        if key in KEY_MAP:
            return f'{{{{t {quote}{KEY_MAP[key]}{quote}}}}}'
        return m.group(0)
    text = re.sub(r'\{\{\s*t\s+(["\'])([^"\']+)\1\s*\}\}', repl_t, text)

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

for old, new in KEY_MAP.items():
    if not new:
        continue
    zh_val = ZH_OVERRIDES.get(new)
    if zh_val is None:
        zh_val = zh.get(old)
    if zh_val is None:
        zh_val = new
    if new not in zh:
        zh[new] = zh_val
    if new not in en:
        en[new] = new

with open(ZH_PATH, "w", encoding="utf-8") as f:
    json.dump(zh, f, ensure_ascii=False, indent=2)
    f.write("\n")
with open(EN_PATH, "w", encoding="utf-8") as f:
    json.dump(en, f, ensure_ascii=False, indent=2)
    f.write("\n")

print(f"Transformed {len(KEY_MAP)} keys in {len(TARGET_FILES)} files.")

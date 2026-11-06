#!/usr/bin/env python3
"""
Polish button / action / status labels to single words or short phrases.
Avoid verbose "Confirm to X" / "Agree to X" styles.
Keep proper nouns (Token, TPM, RPM, API, Key, Group, EOL, etc.) untranslated.
"""

import json
import os

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

IMPROVEMENTS = {
    # Frontend i18n-messages.json
    "确认": "Confirm",
    "确认 EOL": "Confirm EOL",
    "确认{action}计划": "Confirm",
    "确认充值": "Top Up",
    "确认删除": "Delete",
    "确认删除用户": "Delete User",
    "确认取消订阅": "Unsubscribe",
    "确认启用": "Enable",
    "确认拒绝": "Reject",
    "确认支付": "Pay",
    "确认模型": "Confirm Model",
    "确认激活": "Activate",
    "确认禁用": "Disable",
    "确认移除": "Remove",
    "确认续费": "Renew",
    "确认降级": "Demote",
    "确认批准该发票申请？": "Approve invoice request?",
    "稍后处理": "Later",
    "同意": "Agree",
    "同意发布": "Publish",
    "发布": "Publish",
    "已发布": "Published",
    "已使用": "Used",
    "已过期": "Expired",
    "已确认邮箱": "Email confirmed",
    "已加入成员": "Member added",
    "已标记为已开具": "Marked as issued",
    "已用完": "Exhausted",
    "已用信用": "Credit used",
    "已用配额": "Quota used",
    "已用额度": "Credit used",
    "已选模型顺序": "Selected model order",
    "保存": "Save",
    "编辑": "Edit",
    "创建": "Create",
    "添加": "Add",
    "复制": "Copy",
    "删除": "Delete",
    "激活": "Activate",
    "禁用": "Disable",
    "启用": "Enable",
    "停用": "Deactivate",
    "移除": "Remove",
    "撤销": "Revoke",
    "驳回": "Reject",
    "通过": "Approve",
    "批准": "Approve",
    "拒绝": "Reject",
    "发送": "Send",
    "导入": "Import",
    "导出": "Export",
    "设置": "Settings",
    "切换": "Switch",
    "重置": "Reset",
    "筛选": "Filter",
    "搜索": "Search",
    "查看": "View",
    "返回": "Back",
    "关闭": "Close",
    "取消": "Cancel",
    "确定": "OK",
    "申请": "Apply",
    "提交": "Submit",
    "充值": "Top Up",
    "兑换": "Redeem",
    "续费": "Renew",
    "订阅": "Subscribe",
    "购买": "Buy",
    "支付": "Pay",
    "登录": "Log In",
    "注册": "Sign Up",
    "退出": "Log Out",
    "邀请": "Invite",
    "审核": "Review",
    "验证": "Verify",
    "同步": "Sync",
    "刷新": "Refresh",
    "排序": "Sort",

    "ui_稍后支付": "Pay Later",
    "ui_确认": "Confirm",
    "ui_确认_3": "Confirm",
    "ui_确认_4": "Confirm",
    "ui_确认eol": "Confirm EOL",
    "ui_确认修改": "Save",
    "ui_确认修改_3": "Save",
    "ui_确认修改_4": "Save",
    "ui_确认充值": "Top Up",
    "ui_确认创建": "Create",
    "ui_确认删除此key操作不可恢复": "Delete key? This cannot be undone.",
    "ui_确认删除此模型操作不可恢复": "Delete model? This cannot be undone.",
    "ui_确认删除此计划操作不可恢复": "Delete plan? This cannot be undone.",
    "ui_确认取消订阅": "Unsubscribe",
    "ui_确认同步": "Sync",
    "ui_确认同步_3": "Sync",
    "ui_确认同步_4": "Sync",
    "ui_确认导出": "Export",

    # Backend en.json action / ui / modal / status labels
    "action_已阅读同意": "Agreed",
    "action_接受邀请并注册": "Accept & Register",
    "action_确认": "Confirm",
    "action_确认充值": "Top Up",
    "action_确认兑换": "Redeem",
    "action_确认删除": "Delete",
    "action_确认删除_3": "Delete",
    "action_确认删除_4": "Delete",
    "action_确认取消": "Cancel",
    "action_确认启用": "Enable",
    "action_确认拒绝": "Reject",
    "action_确认支付": "Pay",
    "action_确认禁用": "Disable",
    "action_确认移除": "Remove",

    "modal_批量导入key": "Import Keys",
    "modal_批量生成兑换码": "Generate Codes",
    "modal_新建_api_key": "Create API Key",
    "modal_确认修改角色": "Change Role",
    "modal_确认删除": "Delete",
    "modal_确认删除_3": "Delete",
    "modal_确认删除_4": "Delete",
    "modal_确认删除数据": "Delete Data",
    "modal_确认同步": "Sync",
    "modal_邀请码生成成功": "Codes generated",

    "status_email_confirmed": "Confirmed",
    "status_已确认": "Confirmed",

    "title_客户对账与发票审核": "Reconciliation & Invoices",
}


def apply(path):
    with open(path, "r", encoding="utf-8") as f:
        data = json.load(f)
    updated = 0
    for key, value in IMPROVEMENTS.items():
        if key in data and data[key] != value:
            data[key] = value
            updated += 1
    with open(path, "w", encoding="utf-8") as f:
        json.dump(data, f, ensure_ascii=False, indent=2)
    return updated


def main():
    paths = [
        os.path.join(ROOT, "web/static/js/i18n-messages.json"),
        os.path.join(ROOT, "web/i18n/en.json"),
    ]
    for p in paths:
        updated = apply(p)
        print(f"{p}: updated {updated} labels")


if __name__ == "__main__":
    main()

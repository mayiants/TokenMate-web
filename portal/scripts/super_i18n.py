#!/usr/bin/env python3
"""
Convert super page i18n keys to English-default UI chrome keys (Plan B).
Target files: web/templates/super/*.html and the super section of
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

KEY_MAP = {
    # Sidebar nav
    "nav.super.admin_management": "Admin Config",
    "nav.super.payment": "Payment Config",
    "sidebar.super_console": "Super Console",

    # Dot-notation keys
    "apikeys.actions": "Actions",
    "auth.email": "Email Address",
    "error.payment_gateway_not_configured": "The payment gateway is not configured. Please contact the administrator.",
    "form.save": "Save",
    "super.add_admin": "Add Admin",
    "super.admin_created": "Created",
    "super.admin_email": "Email",
    "super.admin_management": "Admin Config",
    "super.admin_management_title": "System / Super Admin Settings",
    "super.admin_name": "Name",
    "super.admin_role": "Role",
    "super.admin_warning": "Super admins have the highest privileges. Proceed with caution.",
    "super.alipay_app_id": "App ID",
    "super.alipay_config": "Alipay Configuration",
    "super.alipay_private_key": "Private Key",
    "super.alipay_public_key": "Alipay Public Key",
    "super.demote": "Demote",
    "super.no_admins": "No admins",
    "super.payment": "Payment Configuration",
    "super.payment_subtitle": "Configure payment gateway settings",
    "super.paypal_client_id": "Client ID",
    "super.paypal_config": "PayPal Configuration",
    "super.paypal_mode": "Mode",
    "super.paypal_secret": "Secret",
    "super.promote": "Promote",
    "super.secret_key_hint": "Leave empty to keep current value",
    "super.stripe_config": "Stripe Configuration",
    "super.stripe_public_key": "Public Key",
    "super.stripe_secret_key": "Secret Key",
    "super.stripe_webhook_secret": "Webhook Secret",
    "super.warning": "Warning",
    "super.wechat_api_key": "API Key",
    "super.wechat_app_id": "App ID",
    "super.wechat_config": "WeChat Pay Configuration",
    "super.wechat_mch_id": "Merchant ID",
    "title_支付模式": "Payment Mode",

    # Chinese keys
    "form_当前模式真实支付": "Current mode: real payment",
    "form_模拟充值模式下用户充值将直接成功不经过支付网关适用于测试环境": "In simulated recharge mode, user recharges succeed directly without a payment gateway. Suitable for test environments.",
    "ui_导出": "Export",
    "⚠ 已切换到模拟充值模式，用户充值将直接成功。": "⚠ Switched to simulated recharge mode. User recharges will succeed directly.",
    "✓ 已切换到真实支付模式，用户充值需要通过支付网关。": "✓ Switched to real payment mode. User recharges require a payment gateway.",
    "当前模式：模拟充值（测试）": "Current mode: simulated recharge (test)",
    "当前模式：真实支付": "Current mode: real payment",
    "提升失败": "Promotion failed",
    "提升成功": "Promoted",
    "未知错误": "Unknown error",
    "用户已提升为超级管理员": "User promoted to super admin",
    "用户已降级": "User demoted",
    "确定要提升该用户为超级管理员吗？": "Are you sure you want to promote this user to super admin?",
    "确定要降级该用户吗？": "Are you sure you want to demote this user?",
    "确认提升": "Confirm Promotion",
    "确认降级": "Confirm Demotion",
    "设置保存失败": "Settings save failed",
    "请求失败": "Request failed",
    "降级失败": "Demotion failed",
    "降级成功": "Demoted",
}

ZH_OVERRIDES = {
    "Admin Config": "管理员配置",
    "Payment Config": "支付配置",
    "Super Console": "超级控制台",
    "Actions": "操作",
    "Email Address": "邮箱地址",
    "The payment gateway is not configured. Please contact the administrator.": "支付网关未配置，请联系管理员。",
    "Add Admin": "添加管理员",
    "Created": "已创建",
    "Email": "邮箱",
    "System / Super Admin Settings": "系统/超级管理员设置",
    "Name": "姓名",
    "Role": "角色",
    "Super admins have the highest privileges. Proceed with caution.": "超级管理员拥有最高权限，请谨慎操作。",
    "App ID": "App ID",
    "Alipay Configuration": "支付宝配置",
    "Private Key": "私钥",
    "Alipay Public Key": "支付宝公钥",
    "Demote": "降级",
    "No admins": "暂无管理员",
    "Payment Configuration": "支付配置",
    "Configure payment gateway settings": "配置支付网关设置",
    "Client ID": "Client ID",
    "PayPal Configuration": "PayPal 配置",
    "Mode": "模式",
    "Secret": "Secret",
    "Promote": "提升",
    "Leave empty to keep current value": "留空表示保持当前值",
    "Stripe Configuration": "Stripe 配置",
    "Public Key": "公钥",
    "Secret Key": "密钥",
    "Webhook Secret": "Webhook Secret",
    "Warning": "警告",
    "API Key": "API Key",
    "WeChat Pay Configuration": "微信支付配置",
    "Merchant ID": "商户号",
    "Payment Mode": "支付模式",
    "Current mode: real payment": "当前模式：真实支付",
    "In simulated recharge mode, user recharges succeed directly without a payment gateway. Suitable for test environments.": "模拟充值模式下用户充值将直接成功，不经过支付网关，适用于测试环境。",
    "Export": "导出",
    "⚠ Switched to simulated recharge mode. User recharges will succeed directly.": "⚠ 已切换到模拟充值模式，用户充值将直接成功。",
    "✓ Switched to real payment mode. User recharges require a payment gateway.": "✓ 已切换到真实支付模式，用户充值需要通过支付网关。",
    "Current mode: simulated recharge (test)": "当前模式：模拟充值（测试）",
    "Promotion failed": "提升失败",
    "Promoted": "提升成功",
    "Unknown error": "未知错误",
    "User promoted to super admin": "用户已提升为超级管理员",
    "User demoted": "用户已降级",
    "Are you sure you want to promote this user to super admin?": "确定要提升该用户为超级管理员吗？",
    "Are you sure you want to demote this user?": "确定要降级该用户吗？",
    "Confirm Promotion": "确认提升",
    "Confirm Demotion": "确认降级",
    "Settings save failed": "设置保存失败",
    "Request failed": "请求失败",
    "Demotion failed": "降级失败",
    "Demoted": "降级成功",
}

TARGET_FILES = [os.path.join(TPL_DIR, "super", x) for x in os.listdir(os.path.join(TPL_DIR, "super")) if x.endswith(".html")]
TARGET_FILES.append(os.path.join(TPL_DIR, "partials", "layout-sidebar.html"))

def replace_in_text(text):
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

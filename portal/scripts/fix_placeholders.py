#!/usr/bin/env python3
import re, json, glob

ZH_PATH = 'web/i18n/zh.json'
EN_PATH = 'web/i18n/en.json'

with open(ZH_PATH) as f: zh = json.load(f)
with open(EN_PATH) as f: en = json.load(f)
seen = set(zh.keys()) | set(en.keys())
new_trans = {}

def make_key(text):
    t = re.sub(r'[^\w\s-]', '', text.strip())
    t = re.sub(r'\s+', '_', t).lower()[:50].strip('_')
    return t or 'text'

def ensure_key(text):
    k = make_key(text)
    if k in seen:
        i = 2
        while f'{k}_{i}' in seen:
            i += 1
        k = f'{k}_{i}'
    seen.add(k)
    return k

EN_MAP = {
    '搜索用户或操作...': 'Search users or actions...',
    '搜索用户...': 'Search users...',
    '搜索模型...': 'Search models...',
    '搜索用户/密钥名...': 'Search user/key name...',
    '搜索供应商...': 'Search providers...',
    '公告标题': 'Announcement Title',
    '公告内容': 'Announcement Content',
    '例如: general-1': 'e.g. general-1',
    '可选': 'Optional',
    '公司全称': 'Full Company Name',
    '留空=无限制': 'Leave empty = unlimited',
    '0 = 不追加': '0 = Do not add',
    '充值原因（可选）': 'Recharge reason (optional)',
    '街道、门牌号、楼层等': 'Street, house number, floor, etc.',
    '北京': 'Beijing',
    '北京市': 'Beijing',
    '朝阳区': 'Chaoyang',
    '自定义密钥名（可选）': 'Custom key name (optional)',
    '请输入兑换码': 'Enter redemption code',
    '自定义金额': 'Custom amount',
    '每行一个Key，最多100行': 'One key per line, max 100 lines',
    'Key数据 (最多100行)': 'Key Data (max 100 lines)',
    '留空使用默认URL': 'Leave empty for default URL',
    '小写+数字+连字符，如 claude-opus-4-6': 'Lowercase+numbers+hyphens, e.g. claude-opus-4-6',
    '留空则由供应商通知时填写': 'Leave empty, fill when notified by provider',
    '如：供应商通知链接、下线原因等': 'e.g. provider notice link, reason for discontinuation',
    '用逗号分隔，只允许这些域名的邮箱加入。留空不限制。': 'Comma-separated. Only these domains are allowed. Leave empty for no restriction.',
    '1000000 = $1.00，0 = 未配置': '1000000 = $1.00, 0 = Not configured',
    '0 = 不限制': '0 = Unlimited',
    '1000000 = $1.00，在现有信用额度基础上增加': '1000000 = $1.00, added on top of existing credit',
    '在现有配额基础上增加': 'Added on top of existing quota',
}

def get_en(text):
    return EN_MAP.get(text, text)

for path in glob.glob('web/templates/*.html') + glob.glob('web/templates/**/*.html', recursive=True):
    with open(path, 'r', encoding='utf-8') as f:
        content = f.read()
    original = content
    
    def repl_placeholder(m):
        attr = m.group(1)
        val = m.group(2)
        key = ensure_key('placeholder_' + val)
        new_trans[key] = {'zh': val, 'en': get_en(val)}
        return attr + '"{{t "' + key + '"}}"'
    
    content = re.sub(r'(placeholder=)"([^"]*[\u4e00-\u9fff][^"]*)"', repl_placeholder, content)
    
    def repl_title(m):
        attr = m.group(1)
        val = m.group(2)
        key = ensure_key('title_' + val)
        new_trans[key] = {'zh': val, 'en': get_en(val)}
        return attr + '"{{t "' + key + '"}}"'
    
    content = re.sub(r'(title=)"([^"]*[\u4e00-\u9fff][^"]*)"', repl_title, content)
    
    if content != original:
        with open(path, 'w', encoding='utf-8') as f:
            f.write(content)

for k, v in new_trans.items():
    zh[k] = v['zh']
    en[k] = v['en']

with open(ZH_PATH, 'w') as f: json.dump(zh, f, ensure_ascii=False, indent=2)
with open(EN_PATH, 'w') as f: json.dump(en, f, ensure_ascii=False, indent=2)

print(f'Added {len(new_trans)} translations')

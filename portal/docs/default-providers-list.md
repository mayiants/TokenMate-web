# 默认 Provider 配置列表

> **ACTIVE** - Admin 首次部署时参考此列表添加 Provider

Created: 2026-05-08  
**Updated: 2026-05-16** — Base URL 已校验并修正（meta、microsoft）

## 说明

系统启动时不会自动创建 Provider。Admin 需要通过 Dashboard (`/admin/providers`) 手动添加。

本文档提供推荐的默认 Provider 列表（共 20 个），供 Admin 参考。所有 Base URL 已做准确性校验。

> **设计原则**：
> 1. Provider 表以**公司名**为唯一标识（如 `anthropic`、`microsoft`），而非具体产品名（如 `claude`、`azure`）。
> 2. `base_url` 字段仅保存该 provider **最常见/默认的 API 入口**。对于多区域部署的 provider（如 Microsoft Azure、Amazon AWS），此处保留一个通用占位地址即可。
> 3. **具体模型的调用 URL 在 model 级别配置**，不依赖 provider 表的 `base_url`。添加 model 时可单独设置 endpoint，覆盖 provider 默认值。
> 4. Provider 列表本身极少变动（均为大型 AI 公司），因此以静态预设方式维护。

---

## International Providers (12)

| ID | Name | Base URL | 说明 |
|----|------|----------|------|
| openai | OpenAI | https://api.openai.com/v1 | GPT-4o, GPT-4, etc. |
| anthropic | Anthropic | https://api.anthropic.com/v1 | Claude 3.5, Claude 3, etc. |
| google | Google | https://generativelanguage.googleapis.com/v1beta | Gemini Pro, Gemini Flash |
| microsoft | Microsoft | https://api.openai.azure.com | Azure OpenAI Service（通用占位，model 级可覆盖） |
| mistral | Mistral | https://api.mistral.ai/v1 | Mistral Large, Mistral Small |
| groq | Groq | https://api.groq.com/openai/v1 | Fast inference |
| cohere | Cohere | https://api.cohere.ai/v1 | Command, Embed |
| perplexity | Perplexity | https://api.perplexity.ai | Perplexity Pro |
| replicate | Replicate | https://api.replicate.com/v1 | Open source models |
| meta | Meta | https://api.llama.com/compat/v1 | Llama 3/4 系列 |
| amazon | Amazon | https://bedrock-runtime.us-east-1.amazonaws.com | AWS Bedrock（us-east-1 默认，model 级可覆盖区域） |
| xai | xAI | https://api.x.ai/v1 | Grok |

## Domestic Providers (8)

| ID | Name | Base URL | 说明 |
|----|------|----------|------|
| deepseek | DeepSeek | https://api.deepseek.com/v1 | DeepSeek V2, DeepSeek Coder |
| zhipu | Zhipu | https://open.bigmodel.cn/api/paas/v4 | GLM-4, GLM-3 |
| moonshot | Moonshot | https://api.moonshot.cn/v1 | Kimi (月之暗面) |
| minimax | MiniMax | https://api.minimax.chat/v1 | ABAB |
| baidu | Baidu | https://aip.baidubce.com/rpc/2.0/ai_custom/v1/wenxinworkshop | 文心一言 |
| alibaba | Alibaba | https://dashscope.aliyuncs.com/compatible-mode/v1 | 通义千问 |
| tencent | Tencent | https://hunyuan.tencentcloudapi.com | 混元 |
| bytedance | ByteDance | https://ark.cn-beijing.volces.com/api/v3 | 豆包 |

## Other (1)

| ID | Name | Base URL | 说明 |
|----|------|----------|------|
| other | Other | - | 其他未分类 Provider |

---

## 快速添加方法

### 方法一：通过 Dashboard 手动添加

1. 登录 Admin Dashboard
2. 进入 `/admin/providers`
3. 点击"添加供应商"
4. 填写 ID、Name、Type、Base URL
5. 保存

### 方法二：通过 SQL 批量添加（可选）

```sql
-- Seed default providers (20 preset providers)
-- Run this SQL after migrations to populate the providers table
INSERT INTO providers (id, name, type, base_url, enabled, sort_order) VALUES
    ('openai',     'OpenAI',     'international', 'https://api.openai.com/v1', true, 1),
    ('anthropic',  'Anthropic',  'international', 'https://api.anthropic.com/v1', true, 2),
    ('google',     'Google',     'international', 'https://generativelanguage.googleapis.com/v1beta', true, 3),
    ('microsoft',  'Microsoft',  'international', 'https://api.openai.azure.com', true, 4),
    ('mistral',    'Mistral',    'international', 'https://api.mistral.ai/v1', true, 5),
    ('groq',       'Groq',       'international', 'https://api.groq.com/openai/v1', true, 6),
    ('cohere',     'Cohere',     'international', 'https://api.cohere.ai/v1', true, 7),
    ('perplexity', 'Perplexity', 'international', 'https://api.perplexity.ai', true, 8),
    ('meta',       'Meta',       'international', 'https://api.llama.com/compat/v1', true, 9),
    ('amazon',     'Amazon',     'international', 'https://bedrock-runtime.us-east-1.amazonaws.com', true, 10),
    ('xai',        'xAI',        'international', 'https://api.x.ai/v1', true, 11),
    ('deepseek',   'DeepSeek',   'domestic',      'https://api.deepseek.com/v1', true, 12),
    ('zhipu',      'Zhipu',      'domestic',      'https://open.bigmodel.cn/api/paas/v4', true, 13),
    ('moonshot',   'Moonshot',   'domestic',      'https://api.moonshot.cn/v1', true, 14),
    ('minimax',    'MiniMax',    'domestic',      'https://api.minimax.chat/v1', true, 15),
    ('baidu',      'Baidu',      'domestic',      'https://aip.baidubce.com/rpc/2.0/ai_custom/v1/wenxinworkshop', true, 16),
    ('alibaba',    'Alibaba',    'domestic',      'https://dashscope.aliyuncs.com/compatible-mode/v1', true, 17),
    ('tencent',    'Tencent',    'domestic',      'https://hunyuan.tencentcloudapi.com', true, 18),
    ('bytedance',  'ByteDance',  'domestic',      'https://ark.cn-beijing.volces.com/api/v3', true, 19),
    ('xiaomi',     'Xiaomi',     'domestic',      'https://api.xiaomimimo.com/v1', true, 20)
ON CONFLICT (id) DO NOTHING;
```

---

## 注意事项

1. **ID 命名规范**：使用小写字母，如 `openai`、`anthropic`
2. **Type 分类**：`international` 或 `domestic`
3. **Base URL**：填写 API 基础地址，不含具体端点路径
4. **启用状态**：新添加的 Provider 默认启用

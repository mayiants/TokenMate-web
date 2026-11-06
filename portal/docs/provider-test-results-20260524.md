# Provider API Key Test Report

> **Generated:** 2026-05-25 13:30 UTC
> **Portal:** http://127.0.0.1:3001 | **Proxy:** http://127.0.0.1:8080

## Test Configuration

| User | Role | Test Key |
|------|------|----------|
| admin@example.com | super_admin | sk-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx |

**Providers tested:** minimax, moonshot, deepseek, alibaba, xiaomi, accessapi, zhipu, xunfei
**Test prompt:** "Hi" (max_tokens=10)

---

## Results Summary

| Provider | Model | Status | Content |
|----------|-------|--------|---------|
| minimax | abab6.5s-chat | ✅ PASS | 你好！有什么可以帮助你的吗？ |
| moonshot | moonshot-v1-8k | ✅ PASS | Hi there! How can I assist you today? |
| deepseek | deepseek-chat | ✅ PASS | Hello! How can I help you today? |
| alibaba | qwen-turbo | ✅ PASS | Hello! How can I assist you today? |
| xiaomi | xiaomi/mimo-v2-flash | ✅ PASS | Hello! How can I help you today? |
| accessapi | gpt-4o | ✅ PASS | Hello! How can I assist you today? 😊 |
| zhipu | glm-4 | ⚠️ BALANCE | 余额不足或无可用资源包,请充值 (429) |
| xunfei | astron-code-latest | ❌ AUTH | AppIdNoAuthError (key format mismatch) |

**6/8 passed, 1 balance issue, 1 auth issue**

---

## Provider Details

### ✅ Minimax (3 keys)
- **Base URL:** `https://api.minimax.chat/v1`
- **Models:** `abab6.5s-chat`
- **Status:** Working — Chinese responses confirmed

### ✅ Moonshot / Kimi (3 keys)
- **Base URL:** `https://api.moonshot.cn/v1`
- **Models:** `moonshot-v1-8k`
- **Status:** Working — English responses confirmed

### ✅ Deepseek (1 key)
- **Base URL:** `https://api.deepseek.com/v1`
- **Models:** `deepseek-chat`
- **Status:** Working

### ✅ Alibaba (1 key)
- **Base URL:** `https://dashscope.aliyuncs.com/compatible-mode/v1`
- **Models:** `qwen-turbo`
- **Status:** Working

### ✅ Xiaomi (3 keys)
- **Base URL:** `https://api.xiaomimimo.com/v1`
- **Models:** `xiaomi/mimo-v2-flash`
- **Status:** Working — model name must use `xiaomi/` prefix

### ✅ Accessapi (1 key)
- **Base URL:** `https://provider.example.com/v1`
- **Models:** `gpt-4o`
- **Status:** Working

### ⚠️ Zhipu (2 keys)
- **Base URL:** `https://open.bigmodel.cn/api/paas/v4`
- **Models:** `glm-4`
- **Status:** Key valid but account balance exhausted (HTTP 429)
- **Action needed:** Top up Zhipu account

### ❌ Xunfei (2 keys)
- **Base URL:** `https://maas-coding-api.cn-huabei-1.xf-yun.com/v2`
- **Models:** `astron-code-latest`
- **Status:** AppIdNoAuthError — key format mismatch
- **Issue:** Provided keys are `app_id:secret` format (Xunfei Open Platform standard API), but Coding Plan's OpenAI-compatible API requires `sk-...` format exclusive API Key.
- **Action needed:** Obtain Coding Plan exclusive API Key from Xunfei MaaS console

---

## Proxy Provider Support

| Provider | Proxy Adaptor | Auth Type | Auto-routing |
|----------|---------------|-----------|--------------|
| minimax | OpenAI-compatible | bearer | ✅ `abab*` → minimax |
| moonshot | OpenAI-compatible | bearer | ✅ `moonshot*`, `kimi*` → moonshot |
| deepseek | OpenAI-compatible | bearer | ✅ `deepseek*` → deepseek |
| alibaba | OpenAI-compatible | bearer | ✅ `qwen*` → alibaba |
| xiaomi | OpenAI-compatible | bearer | ✅ `xiaomi*`, `mimo*` → xiaomi |
| accessapi | OpenAI-compatible | bearer | — |
| zhipu | OpenAI-compatible | bearer | ✅ `glm*`, `chatglm*` → zhipu |
| xunfei | OpenAI-compatible | bearer | ✅ `astron*` → xunfei |

---

## Notes

- All keys added via Portal Admin → Key Pool (`POST /api/admin/upstream-keys`)
- Proxy syncs automatically every 60s via `/api/admin/key-pool/sync`
- Service models registered in `service_models` table for routing
- Total upstream keys in pool: **16** (14 active, 2 zhipu keys pending balance)

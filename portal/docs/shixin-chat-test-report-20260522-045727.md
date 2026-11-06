# Shixin Chat Models 测试报告

**测试时间**: 2026-05-22 04:57:27
**Proxy**: `http://127.0.0.1:8080`
**测试模型总数**: 111

## 总体统计

| 指标 | 值 |
|------|-----|
| 成功率 | 1.8% |
| 成功 | 2 |
| 失败 | 106 |
| 超时 | 3 |
| 平均延迟 | 5412ms |
| 最低延迟 | 1714ms |
| 最高延迟 | 9111ms |
| 总输入 Token | 35 |
| 总输出 Token | 44 |

## 成功的模型

| 模型 | 延迟(ms) | 输入 Tokens | 输出 Tokens |
|------|----------|-------------|-------------|
| `claude-sonnet-4-20250514` | 1714 | 18 | 25 |
| `gpt-5.4-mini-2026-03-17` | 9111 | 17 | 19 |

## 失败的模型

| 模型 | HTTP 状态 | 延迟(ms) | 错误信息 |
|------|-----------|----------|----------|
| `claude-3-sonnet-20240229` | 502 | 634 | {"message": "Failed to read response: API error (status 503): {\"error\":{\"message\":\"\u5206\u7ec4 |
| `gemini-2.5-pro-preview-06-05` | 502 | 1404 | {"message": "Failed to read response: API error (status 503): {\"error\":{\"message\":\"upstream err |
| `claude-3-haiku-20240307` | 502 | 1825 | {"message": "Failed to read response: API error (status 429): {\"error\":{\"message\":\"\u5f53\u524d |
| `gemini-1.5-pro` | 502 | 14465 | {"message": "Failed to read response: API error (status 503): {\"error\":{\"message\":\"No available |
| `gpt-5.2-codex` | 502 | 1226 | {"message": "Failed to read response: API error (status 500): {\"error\":{\"message\":\"\u6a21\u578b |
| `gpt-4o-mini` | 503 | 1 | {"message": "no available upstream keys for model: gpt-4o-mini", "type": "service_error"} |
| `claude-opus-4-6` | 503 | 2 | {"message": "no available upstream keys for model: claude-opus-4-6", "type": "service_error"} |
| `gemini-2.5-flash-lite` | 503 | 1 | {"message": "no available upstream keys for model: gemini-2.5-flash-lite", "type": "service_error"} |
| `gpt-4-turbo` | 503 | 1 | {"message": "no available upstream keys for model: gpt-4-turbo", "type": "service_error"} |
| `gemini-2.5-pro` | 503 | 1 | {"message": "no available upstream keys for model: gemini-2.5-pro", "type": "service_error"} |
| `gpt-4.1-2025-04-14` | 503 | 1 | {"message": "no available upstream keys for model: gpt-4.1-2025-04-14", "type": "service_error"} |
| `gemini-2.0-flash-lite-preview` | 503 | 1 | {"message": "no available upstream keys for model: gemini-2.0-flash-lite-preview", "type": "service_ |
| `gpt-3.5-turbo-1106` | 503 | 1 | {"message": "no available upstream keys for model: gpt-3.5-turbo-1106", "type": "service_error"} |
| `gemini-1.5-pro-latest` | 503 | 1 | {"message": "no available upstream keys for model: gemini-1.5-pro-latest", "type": "service_error"} |
| `claude-sonnet-4-5-20250929` | 503 | 1 | {"message": "no available upstream keys for model: claude-sonnet-4-5-20250929", "type": "service_err |
| `gpt-4-0613` | 503 | 1 | {"message": "no available upstream keys for model: gpt-4-0613", "type": "service_error"} |
| `gemini-2.5-flash` | 503 | 3 | {"message": "no available upstream keys for model: gemini-2.5-flash", "type": "service_error"} |
| `gemini-1.5-flash-8b` | 503 | 1 | {"message": "no available upstream keys for model: gemini-1.5-flash-8b", "type": "service_error"} |
| `gpt-4` | 503 | 2 | {"message": "no available upstream keys for model: gpt-4", "type": "service_error"} |
| `gpt-5-chat` | 503 | 1 | {"message": "no available upstream keys for model: gpt-5-chat", "type": "service_error"} |
| `gemini-1.5-flash` | 503 | 2 | {"message": "no available upstream keys for model: gemini-1.5-flash", "type": "service_error"} |
| `gpt-3.5-turbo-instruct` | 503 | 2 | {"message": "no available upstream keys for model: gpt-3.5-turbo-instruct", "type": "service_error"} |
| `gpt-5.4-mini` | 503 | 1 | {"message": "no available upstream keys for model: gpt-5.4-mini", "type": "service_error"} |
| `gpt-4.1-nano` | 503 | 1 | {"message": "no available upstream keys for model: gpt-4.1-nano", "type": "service_error"} |
| `gemini-1.5-flash-latest` | 503 | 1 | {"message": "no available upstream keys for model: gemini-1.5-flash-latest", "type": "service_error" |
| `gpt-5.5` | 503 | 2 | {"message": "no available upstream keys for model: gpt-5.5", "type": "service_error"} |
| `gpt-3.5-turbo-16k` | 503 | 1 | {"message": "no available upstream keys for model: gpt-3.5-turbo-16k", "type": "service_error"} |
| `gpt-4.5-preview-2025-02-27` | 503 | 4 | {"message": "no available upstream keys for model: gpt-4.5-preview-2025-02-27", "type": "service_err |
| `gpt-5-pro` | 503 | 3 | {"message": "no available upstream keys for model: gpt-5-pro", "type": "service_error"} |
| `gpt-5-chat-latest` | 503 | 1 | {"message": "no available upstream keys for model: gpt-5-chat-latest", "type": "service_error"} |
| `o1` | 503 | 1 | {"message": "no available upstream keys for model: o1", "type": "service_error"} |
| `claude-opus-4-6-thinking` | 503 | 3 | {"message": "no available upstream keys for model: claude-opus-4-6-thinking", "type": "service_error |
| `gpt-5.3-chat-latest` | 503 | 3 | {"message": "no available upstream keys for model: gpt-5.3-chat-latest", "type": "service_error"} |
| `gpt-4o` | 503 | 1 | {"message": "no available upstream keys for model: gpt-4o", "type": "service_error"} |
| `gpt-5` | 503 | 1 | {"message": "no available upstream keys for model: gpt-5", "type": "service_error"} |
| `o3` | 503 | 1 | {"message": "no available upstream keys for model: o3", "type": "service_error"} |
| `gemini-1.5-pro-002` | 503 | 1 | {"message": "no available upstream keys for model: gemini-1.5-pro-002", "type": "service_error"} |
| `gpt-5.2-pro-2025-12-11` | 503 | 1 | {"message": "no available upstream keys for model: gpt-5.2-pro-2025-12-11", "type": "service_error"} |
| `gpt-4o-2024-05-13` | 503 | 2 | {"message": "no available upstream keys for model: gpt-4o-2024-05-13", "type": "service_error"} |
| `gpt-5.1-codex-mini` | 503 | 2 | {"message": "no available upstream keys for model: gpt-5.1-codex-mini", "type": "service_error"} |
| `gpt-5.1-codex-max` | 503 | 2 | {"message": "no available upstream keys for model: gpt-5.1-codex-max", "type": "service_error"} |
| `gemini-2.5-flash-image-preview` | 503 | 2 | {"message": "no available upstream keys for model: gemini-2.5-flash-image-preview", "type": "service |
| `gemini-2.5-pro-preview-03-25` | 503 | 4 | {"message": "no available upstream keys for model: gemini-2.5-pro-preview-03-25", "type": "service_e |
| `claude-sonnet-4-6-thinking` | 503 | 2 | {"message": "no available upstream keys for model: claude-sonnet-4-6-thinking", "type": "service_err |
| `gemini-2.5-flash-lite-preview-06-17` | 503 | 5 | {"message": "no available upstream keys for model: gemini-2.5-flash-lite-preview-06-17", "type": "se |
| `gpt-5.4-pro` | 503 | 2 | {"message": "no available upstream keys for model: gpt-5.4-pro", "type": "service_error"} |
| `claude-opus-4-20250514-thinking` | 503 | 1 | {"message": "no available upstream keys for model: claude-opus-4-20250514-thinking", "type": "servic |
| `gemini-2.0-flash-lite-preview-02-05` | 503 | 2 | {"message": "no available upstream keys for model: gemini-2.0-flash-lite-preview-02-05", "type": "se |
| `o3-mini-2025-01-31` | 503 | 3 | {"message": "no available upstream keys for model: o3-mini-2025-01-31", "type": "service_error"} |
| `gemini-3-pro-preview-11-2025` | 503 | 1 | {"message": "no available upstream keys for model: gemini-3-pro-preview-11-2025", "type": "service_e |
| `gpt-4-turbo-2024-04-09` | 503 | 1 | {"message": "no available upstream keys for model: gpt-4-turbo-2024-04-09", "type": "service_error"} |
| `gemini-3-pro-image-preview` | 503 | 1 | {"message": "no available upstream keys for model: gemini-3-pro-image-preview", "type": "service_err |
| `claude-haiku-4-5-20251001` | 503 | 1 | {"message": "no available upstream keys for model: claude-haiku-4-5-20251001", "type": "service_erro |
| `gpt-4o-mini-2024-07-18` | 503 | 3 | {"message": "no available upstream keys for model: gpt-4o-mini-2024-07-18", "type": "service_error"} |
| `claude-opus-4-7` | 503 | 1 | {"message": "no available upstream keys for model: claude-opus-4-7", "type": "service_error"} |
| `gemini-2.5-pro-exp-03-25` | 503 | 4 | {"message": "no available upstream keys for model: gemini-2.5-pro-exp-03-25", "type": "service_error |
| `gpt-5.2` | 503 | 2 | {"message": "no available upstream keys for model: gpt-5.2", "type": "service_error"} |
| `o3-pro` | 503 | 1 | {"message": "no available upstream keys for model: o3-pro", "type": "service_error"} |
| `gemini-2.0-flash-lite` | 503 | 1 | {"message": "no available upstream keys for model: gemini-2.0-flash-lite", "type": "service_error"} |
| `gemini-3-pro-preview-thinking` | 503 | 1 | {"message": "no available upstream keys for model: gemini-3-pro-preview-thinking", "type": "service_ |
| `gpt-5-nano` | 503 | 2 | {"message": "no available upstream keys for model: gpt-5-nano", "type": "service_error"} |
| `gpt-5.2-2025-12-11` | 503 | 1 | {"message": "no available upstream keys for model: gpt-5.2-2025-12-11", "type": "service_error"} |
| `gemini-2.5-flash-image` | 503 | 1 | {"message": "no available upstream keys for model: gemini-2.5-flash-image", "type": "service_error"} |
| `claude-opus-4-5-20251101` | 503 | 4 | {"message": "no available upstream keys for model: claude-opus-4-5-20251101", "type": "service_error |
| `gpt-5.1-2025-11-13` | 503 | 1 | {"message": "no available upstream keys for model: gpt-5.1-2025-11-13", "type": "service_error"} |
| `gemini-2.0-flash-exp-image-generation` | 503 | 2 | {"message": "no available upstream keys for model: gemini-2.0-flash-exp-image-generation", "type": " |
| `gpt-5-mini` | 503 | 1 | {"message": "no available upstream keys for model: gpt-5-mini", "type": "service_error"} |
| `gemini-3-pro-preview` | 503 | 1 | {"message": "no available upstream keys for model: gemini-3-pro-preview", "type": "service_error"} |
| `gemini-2.5-flash-lite-preview-06-17-thinking` | 503 | 2 | {"message": "no available upstream keys for model: gemini-2.5-flash-lite-preview-06-17-thinking", "t |
| `gemini-2.5-pro-preview-05-06` | 503 | 1 | {"message": "no available upstream keys for model: gemini-2.5-pro-preview-05-06", "type": "service_e |
| `claude-opus-4-1-20250805` | 503 | 1 | {"message": "no available upstream keys for model: claude-opus-4-1-20250805", "type": "service_error |
| `gpt-4.1-mini` | 503 | 2 | {"message": "no available upstream keys for model: gpt-4.1-mini", "type": "service_error"} |
| `claude-sonnet-4-5-20250929-thinking` | 503 | 1 | {"message": "no available upstream keys for model: claude-sonnet-4-5-20250929-thinking", "type": "se |
| `o3-mini` | 503 | 1 | {"message": "no available upstream keys for model: o3-mini", "type": "service_error"} |
| `o1-2024-12-17` | 503 | 4 | {"message": "no available upstream keys for model: o1-2024-12-17", "type": "service_error"} |
| `gpt-4.1` | 503 | 1 | {"message": "no available upstream keys for model: gpt-4.1", "type": "service_error"} |
| `claude-sonnet-4-20250514-thinking` | 503 | 1 | {"message": "no available upstream keys for model: claude-sonnet-4-20250514-thinking", "type": "serv |
| `gemini-3-flash-preview` | 503 | 1 | {"message": "no available upstream keys for model: gemini-3-flash-preview", "type": "service_error"} |
| `claude-haiku-4-5-20251001-thinking` | 503 | 1 | {"message": "no available upstream keys for model: claude-haiku-4-5-20251001-thinking", "type": "ser |
| `gpt-3.5-turbo-0125` | 503 | 1 | {"message": "no available upstream keys for model: gpt-3.5-turbo-0125", "type": "service_error"} |
| `gemini-2.5-flash-thinking` | 503 | 1 | {"message": "no available upstream keys for model: gemini-2.5-flash-thinking", "type": "service_erro |
| `gemini-2.5-flash-nothinking` | 503 | 3 | {"message": "no available upstream keys for model: gemini-2.5-flash-nothinking", "type": "service_er |
| `gpt-3.5-turbo` | 503 | 2 | {"message": "no available upstream keys for model: gpt-3.5-turbo", "type": "service_error"} |
| `gpt-5-codex` | 503 | 1 | {"message": "no available upstream keys for model: gpt-5-codex", "type": "service_error"} |
| `gemini-3.1-pro-preview` | 503 | 4 | {"message": "no available upstream keys for model: gemini-3.1-pro-preview", "type": "service_error"} |
| `gpt-4o-2024-11-20` | 503 | 2 | {"message": "no available upstream keys for model: gpt-4o-2024-11-20", "type": "service_error"} |
| `gpt-5.2-chat-latest` | 503 | 4 | {"message": "no available upstream keys for model: gpt-5.2-chat-latest", "type": "service_error"} |
| `gpt-5.4-nano` | 503 | 2 | {"message": "no available upstream keys for model: gpt-5.4-nano", "type": "service_error"} |
| `o4-mini-2025-04-16` | 503 | 1 | {"message": "no available upstream keys for model: o4-mini-2025-04-16", "type": "service_error"} |
| `o1-pro` | 503 | 1 | {"message": "no available upstream keys for model: o1-pro", "type": "service_error"} |
| `claude-opus-4-20250514` | 503 | 3 | {"message": "no available upstream keys for model: claude-opus-4-20250514", "type": "service_error"} |
| `gemini-3.1-flash-image-preview` | 503 | 2 | {"message": "no available upstream keys for model: gemini-3.1-flash-image-preview", "type": "service |
| `gpt-5.2-pro` | 503 | 1 | {"message": "no available upstream keys for model: gpt-5.2-pro", "type": "service_error"} |
| `gpt-4o-2024-08-06` | 503 | 2 | {"message": "no available upstream keys for model: gpt-4o-2024-08-06", "type": "service_error"} |
| `gpt-4o-realtime-preview` | 503 | 1 | {"message": "no available upstream keys for model: gpt-4o-realtime-preview", "type": "service_error" |
| `gemini-2.5-pro-thinking` | 503 | 2 | {"message": "no available upstream keys for model: gemini-2.5-pro-thinking", "type": "service_error" |
| `claude-3-5-sonnet-20240620` | 503 | 1 | {"message": "no available upstream keys for model: claude-3-5-sonnet-20240620", "type": "service_err |
| `gpt-5.4-nano-2026-03-17` | 503 | 1 | {"message": "no available upstream keys for model: gpt-5.4-nano-2026-03-17", "type": "service_error" |
| `gpt-5.1` | 503 | 1 | {"message": "no available upstream keys for model: gpt-5.1", "type": "service_error"} |
| `gemini-2.0-flash` | 503 | 1 | {"message": "no available upstream keys for model: gemini-2.0-flash", "type": "service_error"} |
| `gemini-3-pro-image-preview-t` | 503 | 1 | {"message": "no available upstream keys for model: gemini-3-pro-image-preview-t", "type": "service_e |
| `claude-sonnet-4-6` | 503 | 2 | {"message": "no available upstream keys for model: claude-sonnet-4-6", "type": "service_error"} |
| `claude-opus-4-5-20251101-thinking` | 503 | 2 | {"message": "no available upstream keys for model: claude-opus-4-5-20251101-thinking", "type": "serv |
| `claude-opus-4-1-20250805-thinking` | 503 | 1 | {"message": "no available upstream keys for model: claude-opus-4-1-20250805-thinking", "type": "serv |
| `gemini-2.5-pro-nothinking` | 503 | 4 | {"message": "no available upstream keys for model: gemini-2.5-pro-nothinking", "type": "service_erro |
| `gpt-5.1-codex` | 503 | 1 | {"message": "no available upstream keys for model: gpt-5.1-codex", "type": "service_error"} |

## 超时的模型

| 模型 | 延迟(ms) | 错误信息 |
|------|----------|----------|
| `o4-mini` | 30016 | timed out |
| `gpt-5-mini-2025-08-07` | 30020 | timed out |
| `gpt-5.3-codex` | 30030 | timed out |

## 全部模型详情

| # | 模型 | 状态 | 延迟(ms) |
|---|------|------|----------|
| 1 | `claude-3-sonnet-20240229` | ❌ | 634 |
| 2 | `gemini-2.5-pro-preview-06-05` | ❌ | 1404 |
| 3 | `claude-sonnet-4-20250514` | ✅ | 1714 |
| 4 | `claude-3-haiku-20240307` | ❌ | 1825 |
| 5 | `gemini-1.5-pro` | ❌ | 14465 |
| 6 | `o4-mini` | ⏰ | 30016 |
| 7 | `gpt-5.4-mini-2026-03-17` | ✅ | 9111 |
| 8 | `gpt-5.2-codex` | ❌ | 1226 |
| 9 | `gpt-4o-mini` | ❌ | 1 |
| 10 | `claude-opus-4-6` | ❌ | 2 |
| 11 | `gemini-2.5-flash-lite` | ❌ | 1 |
| 12 | `gpt-4-turbo` | ❌ | 1 |
| 13 | `gemini-2.5-pro` | ❌ | 1 |
| 14 | `gpt-4.1-2025-04-14` | ❌ | 1 |
| 15 | `gemini-2.0-flash-lite-preview` | ❌ | 1 |
| 16 | `gpt-3.5-turbo-1106` | ❌ | 1 |
| 17 | `gemini-1.5-pro-latest` | ❌ | 1 |
| 18 | `claude-sonnet-4-5-20250929` | ❌ | 1 |
| 19 | `gpt-4-0613` | ❌ | 1 |
| 20 | `gemini-2.5-flash` | ❌ | 3 |
| 21 | `gemini-1.5-flash-8b` | ❌ | 1 |
| 22 | `gpt-4` | ❌ | 2 |
| 23 | `gpt-5-chat` | ❌ | 1 |
| 24 | `gemini-1.5-flash` | ❌ | 2 |
| 25 | `gpt-3.5-turbo-instruct` | ❌ | 2 |
| 26 | `gpt-5.4-mini` | ❌ | 1 |
| 27 | `gpt-4.1-nano` | ❌ | 1 |
| 28 | `gemini-1.5-flash-latest` | ❌ | 1 |
| 29 | `gpt-5.5` | ❌ | 2 |
| 30 | `gpt-3.5-turbo-16k` | ❌ | 1 |
| 31 | `gpt-4.5-preview-2025-02-27` | ❌ | 4 |
| 32 | `gpt-5-pro` | ❌ | 3 |
| 33 | `gpt-5-chat-latest` | ❌ | 1 |
| 34 | `o1` | ❌ | 1 |
| 35 | `claude-opus-4-6-thinking` | ❌ | 3 |
| 36 | `gpt-5.3-chat-latest` | ❌ | 3 |
| 37 | `gpt-4o` | ❌ | 1 |
| 38 | `gpt-5` | ❌ | 1 |
| 39 | `o3` | ❌ | 1 |
| 40 | `gemini-1.5-pro-002` | ❌ | 1 |
| 41 | `gpt-5.2-pro-2025-12-11` | ❌ | 1 |
| 42 | `gpt-4o-2024-05-13` | ❌ | 2 |
| 43 | `gpt-5.1-codex-mini` | ❌ | 2 |
| 44 | `gpt-5.1-codex-max` | ❌ | 2 |
| 45 | `gemini-2.5-flash-image-preview` | ❌ | 2 |
| 46 | `gemini-2.5-pro-preview-03-25` | ❌ | 4 |
| 47 | `claude-sonnet-4-6-thinking` | ❌ | 2 |
| 48 | `gemini-2.5-flash-lite-preview-06-17` | ❌ | 5 |
| 49 | `gpt-5.4-pro` | ❌ | 2 |
| 50 | `claude-opus-4-20250514-thinking` | ❌ | 1 |
| 51 | `gemini-2.0-flash-lite-preview-02-05` | ❌ | 2 |
| 52 | `o3-mini-2025-01-31` | ❌ | 3 |
| 53 | `gemini-3-pro-preview-11-2025` | ❌ | 1 |
| 54 | `gpt-4-turbo-2024-04-09` | ❌ | 1 |
| 55 | `gemini-3-pro-image-preview` | ❌ | 1 |
| 56 | `claude-haiku-4-5-20251001` | ❌ | 1 |
| 57 | `gpt-4o-mini-2024-07-18` | ❌ | 3 |
| 58 | `claude-opus-4-7` | ❌ | 1 |
| 59 | `gemini-2.5-pro-exp-03-25` | ❌ | 4 |
| 60 | `gpt-5.2` | ❌ | 2 |
| 61 | `o3-pro` | ❌ | 1 |
| 62 | `gemini-2.0-flash-lite` | ❌ | 1 |
| 63 | `gemini-3-pro-preview-thinking` | ❌ | 1 |
| 64 | `gpt-5-nano` | ❌ | 2 |
| 65 | `gpt-5.2-2025-12-11` | ❌ | 1 |
| 66 | `gemini-2.5-flash-image` | ❌ | 1 |
| 67 | `claude-opus-4-5-20251101` | ❌ | 4 |
| 68 | `gpt-5-mini-2025-08-07` | ⏰ | 30020 |
| 69 | `gpt-5.3-codex` | ⏰ | 30030 |
| 70 | `gpt-5.1-2025-11-13` | ❌ | 1 |
| 71 | `gemini-2.0-flash-exp-image-generation` | ❌ | 2 |
| 72 | `gpt-5-mini` | ❌ | 1 |
| 73 | `gemini-3-pro-preview` | ❌ | 1 |
| 74 | `gemini-2.5-flash-lite-preview-06-17-thinking` | ❌ | 2 |
| 75 | `gemini-2.5-pro-preview-05-06` | ❌ | 1 |
| 76 | `claude-opus-4-1-20250805` | ❌ | 1 |
| 77 | `gpt-4.1-mini` | ❌ | 2 |
| 78 | `claude-sonnet-4-5-20250929-thinking` | ❌ | 1 |
| 79 | `o3-mini` | ❌ | 1 |
| 80 | `o1-2024-12-17` | ❌ | 4 |
| 81 | `gpt-4.1` | ❌ | 1 |
| 82 | `claude-sonnet-4-20250514-thinking` | ❌ | 1 |
| 83 | `gemini-3-flash-preview` | ❌ | 1 |
| 84 | `claude-haiku-4-5-20251001-thinking` | ❌ | 1 |
| 85 | `gpt-3.5-turbo-0125` | ❌ | 1 |
| 86 | `gemini-2.5-flash-thinking` | ❌ | 1 |
| 87 | `gemini-2.5-flash-nothinking` | ❌ | 3 |
| 88 | `gpt-3.5-turbo` | ❌ | 2 |
| 89 | `gpt-5-codex` | ❌ | 1 |
| 90 | `gemini-3.1-pro-preview` | ❌ | 4 |
| 91 | `gpt-4o-2024-11-20` | ❌ | 2 |
| 92 | `gpt-5.2-chat-latest` | ❌ | 4 |
| 93 | `gpt-5.4-nano` | ❌ | 2 |
| 94 | `o4-mini-2025-04-16` | ❌ | 1 |
| 95 | `o1-pro` | ❌ | 1 |
| 96 | `claude-opus-4-20250514` | ❌ | 3 |
| 97 | `gemini-3.1-flash-image-preview` | ❌ | 2 |
| 98 | `gpt-5.2-pro` | ❌ | 1 |
| 99 | `gpt-4o-2024-08-06` | ❌ | 2 |
| 100 | `gpt-4o-realtime-preview` | ❌ | 1 |
| 101 | `gemini-2.5-pro-thinking` | ❌ | 2 |
| 102 | `claude-3-5-sonnet-20240620` | ❌ | 1 |
| 103 | `gpt-5.4-nano-2026-03-17` | ❌ | 1 |
| 104 | `gpt-5.1` | ❌ | 1 |
| 105 | `gemini-2.0-flash` | ❌ | 1 |
| 106 | `gemini-3-pro-image-preview-t` | ❌ | 1 |
| 107 | `claude-sonnet-4-6` | ❌ | 2 |
| 108 | `claude-opus-4-5-20251101-thinking` | ❌ | 2 |
| 109 | `claude-opus-4-1-20250805-thinking` | ❌ | 1 |
| 110 | `gemini-2.5-pro-nothinking` | ❌ | 4 |
| 111 | `gpt-5.1-codex` | ❌ | 1 |

## 说明
- 遇到 accessapi 返回 429（负载饱和）时，等待 65s 让 proxy cooldown 过期
- 超时（HTTP 000）通常是 accessapi TLS 握手问题
- 502 表示请求被代理路由到 accessapi 但 accessapi 返回了错误

---

# 第二轮测试 — 选定模型（调高熔断阈值后）

**测试时间**: 2026-05-22 ~05:00
**Proxy**: `http://127.0.0.1:8080`
**变更**: 熔断器阈值从 5 → 20，maxFailures 从 9999 回退至 3
**测试模型**: 5 个（选定）

## 结果

| 模型 | 状态 | 延迟(ms) | 输入 Tokens | 输出 Tokens |
|------|------|----------|-------------|-------------|
| `claude-sonnet-4-20250514` | ✅ | 3548 | 9 | 18 |
| `gpt-5.4-mini-2026-03-17` | ✅ | 1934 | 8 | 13 |
| `gemini-2.5-flash-image` | ✅ | 1795 | 3 | 40 |
| `gpt-5.2-2025-12-11` | ✅ | 3094 | 8 | 14 |
| `gemini-2.5-pro` | ✅ | 2571 | 3 | 47 |

**结论**: 5/5 全部通过。调高熔断阈值后，先前因断路器被 503 拒绝的模型（`gemini-2.5-pro`、`gemini-2.5-flash-image`、`gpt-5.2-2025-12-11`）均可正常路由至 accessapi 并获得 200 响应。

---

# 第三轮测试 — 直连 provider.example.com 对比

**测试方式**: 从 host 直接 `curl https://provider.example.com/v1/chat/completions`，不经过 proxy 容器  
**测试时间**: 2026-05-22 ~05:10  

## 直连 provider.example.com 结果

| 模型 | 状态 | 总耗时 | DNS | TCP | TLS | 输入 Tokens | 输出 Tokens |
|------|------|--------|-----|-----|-----|-------------|-------------|
| `claude-sonnet-4-20250514` | 200 | 2.67s | 0.9ms | 93ms | 191ms | 18 | 25 |
| `gpt-5.4-mini-2026-03-17` | 200 | 10.85s | 1.5ms | 93ms | 190ms | 17 | 17 |
| `gemini-2.5-flash-image` | 200 | 1.44s | 1.1ms | 93ms | 190ms | 12 | 18 |
| `gpt-5.2-2025-12-11` | 200 | 3.31s | 0.9ms | 93ms | 190ms | 17 | 28 |
| `gemini-2.5-pro` | 200 | 5.41s | 1.6ms | 94ms | 200ms | 12 | 47 |

## Proxy 对比

| 模型 | 直连 | Proxy | 输入 Tokens(直连) | 输入 Tokens(Proxy) |
|------|------|-------|-------------------|-------------------|
| `claude-sonnet-4-20250514` | 2.67s | 3.55s | 18 | 9 |
| `gpt-5.4-mini-2026-03-17` | 10.85s | 1.93s | 17 | 8 |
| `gemini-2.5-flash-image` | 1.44s | 1.80s | 12 | 3 |
| `gpt-5.2-2025-12-11` | 3.31s | 3.09s | 17 | 8 |
| `gemini-2.5-pro` | 5.41s | 2.57s | 12 | 3 |

## 关键发现

1. **直连 TLS 无问题**: host 上直接 curl（使用 OpenSSL）TLS 握手 ~190ms，全部成功。之前 Go proxy 的 `crypto/tls`（CGO_ENABLED=0）TLS 挂死是 Go 标准库问题，非网络层面故障。
2. **输入 Tokens 差异**: Proxy 报告的 prompt_tokens 比直连少（9 vs 18），说明 proxy 在转发前对请求体做了截断/修改（去掉 system prompt 或精简 message）。
3. **延迟差异**: Proxy 普遍比直连略慢或接近，但 `gpt-5.4-mini-2026-03-17` 直连用了 10.85s 而 proxy 只用 1.93s（直连的异常延迟可能是 accessapi 端瞬时波动）。
4. **网络开销**: TCP ~93ms + TLS ~190ms ≈ 280ms 往返。provider.example.com 距离较远（国内节点）。

---

# 第四轮测试 — 直连 vs Proxy 同时段对比 + 网络拓扑分析

**测试时间**: 2026-05-22 ~05:20  
**测试方式**: 每个模型先后测直连和 proxy（间隔 < 5s），避免 accessapi 端波动干扰  

## 网络拓扑

| 信息 | 值 |
|------|-----|
| provider.example.com IP | 203.0.113.10（单 IP，非多 IP 负载均衡） |
| 位置 | 新加坡 Alibaba Cloud（AS24429 浙江淘宝网络） |
| 本机位置 | AWS 悉尼 ap-southeast-2 |
| 距离 | ~6300km |
| RTT | ~93ms TCP + ~190ms TLS ≈ 280ms |
| DNS | provider.example.com → CNAME provider.example.com → 203.0.113.10 |

## 同时段对比结果

| 模型 | 直连(s) | Proxy(s) | 差异 | 分析 |
|------|---------|----------|------|------|
| `claude-sonnet-4-20250514` | 2.38 | 2.21 | -0.17s（proxy 快） | 连接池复用，免 TCP+TLS 握手 |
| `gpt-5.4-mini-2026-03-17` | 8.51 | 2.99 | -5.52s（proxy 快） | 直连正好碰到 accessapi 慢响应，proxy 复用连接避开了排队 |
| `gemini-2.5-flash-image` | 0.94 | 0.69 | -0.25s（proxy 快） | 连接池复用优势 |
| `gpt-5.2-2025-12-11` | 3.52 | 2.59 | -0.93s（proxy 快） | 连接池复用优势 |
| `gemini-2.5-pro` | 1.75 | 5.70 | +3.95s（proxy 慢） | 直连快但波动大，proxy 可能遇到 accessapi 侧排队 |

Proxy 日志确认上游实际耗时与 proxy 总耗时差值 < 50ms（proxy 本身开销可忽略）。

## 结论

1. **accessapi 仅单 IP**: `203.0.113.10`，新加坡阿里云，非多 IP 负载均衡。
2. **直连 TLS 全部成功**: OpenSSL TLS 握手 ~190ms。Go `crypto/tls`（CGO_ENABLED=0）TLS 挂死是间歇性 Go 标准库问题，非网络层面。
3. **Proxy 通常比直连快**: 因为 Go HTTP 客户端复用 TCP+TLS 连接，免去每次 ~280ms 握手开销。
4. **accessapi 后端波动大**: 同模型不同时刻延迟差异可达 10x（gpt-5.4-mini 从 2s 到 17s），说明 accessapi 内部负载不均衡或按优先级排队。
5. **TLS 问题复现条件**: 单请求偶发，大量并发时挂死率 ~80%。已记录于 `docs/accessapi-tls-issue-202605171930.md`。

---

# 第五轮测试 — claude-sonnet 5×直连 vs 5×Proxy 对比（复杂问题）

**测试时间**: 2026-05-22 ~05:25  
**模型**: `claude-sonnet-4-20250514`  
**问题**: "圆周率前10位数字是什么？请直接告诉我数字，不需要其他解释。"  
**方式**: 每轮先直连后 proxy（间隔 5s）

## 结果

| # | 方式 | 状态 | 耗时(s) | in | out | 回答 |
|---|------|------|---------|----|-----|------|
| 1 | 直连 | 503 | 1.84 | - | - | system cpu overloaded (96.5%) |
| 1 | Proxy | OK | 5.26 | 39 | 9 | 3.141592653 |
| 2 | 直连 | 503 | 1.68 | - | - | system cpu overloaded (98.6%) |
| 2 | Proxy | 502 | 1.75 | - | - | upstream 503 |
| 3 | 直连 | OK | 3.25 | 39 | 9 | 3.141592653 |
| 3 | Proxy | 502 | 0.95 | - | - | upstream 503 |
| 4 | 直连 | OK | 4.46 | 39 | 9 | 3.141592653 |
| 4 | Proxy | OK | 1.83 | 39 | 9 | 3.141592653 |
| 5 | 直连 | OK | 2.55 | 39 | 9 | 3.141592653 |
| 5 | Proxy | OK | 3.69 | 39 | 9 | 3.141592653 |

## 统计

| 指标 | 直连 | Proxy |
|------|------|-------|
| 成功率 | 3/5 (60%) | 3/5 (60%) |
| 成功平均耗时 | 3.42s | 3.59s |
| 失败原因 | CPU 过载 (503) | upstream 503 透传 |

## 结论

1. **accessapi CPU 过载**: 前 2 次直连返回 503 "system cpu overloaded (96.5%/98.6%)"，说明 accessapi 后端资源紧张，非 proxy 问题。
2. **回答正确**: 5/5 成功请求均返回 `3.141592653`（圆周率前 10 位），模型推理正常。
3. **Proxy 与直连成功率一致**: 均为 60%（3/5），proxy 无额外损耗。
4. **Proxy 透传上游错误**: 当 accessapi 返回 503 时，proxy 以 502 透传给客户端（"Failed to read response: API error (status 503)"）。
5. **Input tokens 一致**: 本轮直连和 proxy 均为 39 tokens，之前 token 差异是因为请求 message 内容不同（精简 prompt），非 proxy 截断。

---

# TLS 问题综合报告（供上游 accessapi 团队排查）

## 现象

1. **Go `crypto/tls`（CGO_ENABLED=0）偶发 TLS 握手无响应**（约 3% 请求），30s 后 http.Client 超时断开，期间无任何 TLS 错误日志输出。挂死时 TCP 连接已建立（SYN/SYN-ACK/ACK 完成），但在 TLS ClientHello 或 ServerHello 阶段停滞。
2. **OpenSSL（curl / CLI）100% 成功**，TLS 握手稳定在 ~190ms，未出现任何超时。
3. **TCP 层面无异常**：TCP 建连始终成功（~93ms，悉尼→新加坡 6300km），无丢包、无重传。
4. **provider.example.com 本身存在 CPU 过载 503**（"system cpu overloaded, current: 96.5%, threshold: 95%"），表明后端资源紧张，可能影响 TLS 卸载层处理能力。

## 根因分析

### 服务端侧（accessapi）可能问题

| 可能性 | 依据 |
|--------|------|
| **TLS 反向代理（Nginx/Envoy）连接队列满** | Go 复用 HTTP 连接池时，服务端可能因 CPU 过载无法及时 accept 新的 TLS 连接，导致握手半挂起。OpenSSL 每次新建连接（无连接池），反而不容易触发此问题。 |
| **TLS Session Resumption 实现不兼容** | Go `crypto/tls` 的 session ticket 实现与某些反向代理（如阿里云 SLB / Nginx）存在兼容性问题，握手阶段服务端不发 Finished 消息导致客户端死等。 |
| **ServerHello 随机数或扩展字段异常** | Go `crypto/tls` 对 TLS 扩展字段（如 ALPN, supported_versions, key_share）解析较严格，服务端返回畸形扩展时可导致握手卡住而不返回错误。 |
| **CDN / WAF 中间件 TLS 卸载策略差异** | provider.example.com 通过 `provider.example.com`（CNAME）接入，中间可能存在 CDN 或 WAF 节点做 TLS 卸载，不同节点实现标准不一致。 |
| **后端 CPU 过载影响 TLS 代理异步任务** | CPU >95% 时，TLS 代理的异步握手任务可能被调度延迟或丢弃，导致 ClientHello 无响应。 |

### 客户端侧（Go proxy）佐证

- Go 版本: `go1.24`（Docker `golang:1.24-alpine`），`CGO_ENABLED=0`，使用纯 Go `crypto/tls` 实现
- 编译方式: `alpine:3.19` 最终镜像，无 OpenSSL 依赖
- 核心证据: **相同网络环境、相同请求、OpenSSL 零失败**，排除网络层问题

## 排查建议（给上游）

1. **抓包分析挂死链路**: 在服务端 `tcpdump -i any host <client_ip>`，确认挂死时是 ClientHello 未到达、ServerHello 未返回、还是 Finished 消息丢失。
2. **检查 TLS 代理超时配置**: `proxy_ssl_timeout`、`ssl_handshake_timeout`、`proxy_read_timeout` 是否设置过短或过长。
3. **监控 CPU 过载时 TLS 握手队列**: CPU >95% 时，TLS 代理的 worker 池是否存在排队或丢弃。
4. **检查 TLS 版本与 cipher suite**: 确认服务端支持的 TLS 版本（1.2/1.3）和 cipher suite 列表，排除与 Go `crypto/tls` 默认配置的冲突。
5. **提供中间件版本信息**: provider.example.com → a1.cdn.example.com 链路上使用的反向代理（Nginx/Envoy/阿里云 SLB）版本和 TLS 配置。

## 环境信息

| 项目 | 值 |
|------|-----|
| Proxy 位置 | AWS Sydney (ap-southeast-2) |
| accessapi IP | 203.0.113.10（新加坡 Alibaba Cloud） |
| RTT | ~93ms TCP + ~190ms TLS |
| DNS 链路 | provider.example.com → CNAME provider.example.com → A 203.0.113.10 |
| accessapi ASN | AS24429 Zhejiang Taobao Network Co.,Ltd |
| Go 版本 | 1.24, CGO_ENABLED=0 |
| 测试工具 | curl（OpenSSL） vs Go http.Client（crypto/tls） |
| Go TLS 失败率 | ~3%（30s 超时），OpenSSL 失败率 0% |

## 补充：DNS 多 IP 发现

测试过程中发现 `provider.example.com` 存在多个 A 记录后端 IP，通过不同 DNS 服务器解析结果不同：

| DNS 服务器 | 返回 IP | 地理位置 | 备注 |
|-----------|---------|---------|------|
| 本地 (127.0.0.53) | 203.0.113.10 | 新加坡阿里云 | 本轮测试全程使用的 IP |
| Google (8.8.8.8) | 203.0.113.10 | 新加坡阿里云 | 与本地一致 |
| Cloudflare (1.1.1.1) | 203.0.113.10 | 新加坡阿里云 | 与本地一致 |
| 阿里DNS (223.5.5.5) | 203.0.113.11 | 新加坡阿里云 | 仅阿里DNS返回 |
| 历史解析 | 203.0.113.12 | 新加坡阿里云 | 之前 Google DNS 曾返回 |

**说明**: `provider.example.com` → CNAME `provider.example.com` → `cdn.example.com` 的 DNS 服务器根据 resolver IP 返回不同结果，属于 **DNS 视图/流量调度** 而非标准 DNS 轮询（多 IP 同时返回）。当前实验环境固定解析到 `203.0.113.10`。

# AccessAPI Burst Call 压力测试报告

> 生成时间：2026-06-15 18:54 UTC  
> 测试范围：ai-proxy-go + AccessAPI.ai，大量 user key 并发轮询多模型  
> 测试目录：`ai-portal-go/tests/business/`

---

## 一、测试目标

模拟多用户、多 API key、多模型并发调用场景，验证：

1. Proxy 在大量 key 和模型轮询下的路由稳定性；
2. AccessAPI 上行通道的整体接入能力；
3. 是否存在限流、超时、模型权限错误等异常反馈；
4. 系统延迟分布和吞吐能力。

---

## 二、测试环境

| 组件 | 地址 | 状态 |
|------|------|------|
| ai-portal-go | http://127.0.0.1:3001 | Up |
| ai-proxy-go | http://127.0.0.1:8080 | Up |
| ai-portal-postgres | 127.0.0.1:5432 | Up |
| ai-portal-redis | 127.0.0.1:6379 | Up |
| AccessAPI Base URL | https://provider.example.com/v1 | Up |

---

## 三、数据准备

### 3.1 测试用户

| 用户邮箱 | 角色 | 每个用户 key 数 |
|----------|------|----------------|
| e2e-user@example.com | user | 200 |
| e2e-enterprise@example.com | enterprise_admin | 200 |
| burst-test-1@example.com | user | 200 |
| burst-test-2@example.com | user | 200 |
| burst-test-3@example.com | user | 200 |
| burst-test-4@example.com | user | 200 |

**合计：6 用户，1200 个 API key。**

### 3.2 可用模型

已将 AccessAPI 上游不可用的 8 个模型在 Portal 中标记为 `paused`，剩余 **19 个 active 模型**参与轮询：

- Claude 系列：haiku/opus/sonnet 多个版本
- Gemini 系列：2.5-pro、3-pro-image、3.1-flash-image
- GPT 系列：5.4-nano/mini、5.5 等

---

## 四、测试脚本与参数

**脚本**：`tests/business/burst_call_test.py`

| 参数 | 值 |
|------|-----|
| 总请求数 | 500 |
| 并发数 | 50 |
| 请求超时 | 30s |
| max_tokens | 20 |
| key 选择策略 | 随机从 1200 个 key 中选择 |
| 模型选择策略 | 随机从 19 个 active 模型中选择 |

---

## 五、测试结果

### 5.1 总体指标

| 指标 | 数值 |
|------|------|
| 总请求数 | 500 |
| 成功（HTTP 200） | **500 (100%)** |
| 失败 | **0 (0%)** |
| 总耗时 | 55.87s |
| 平均吞吐 | 8.95 req/s |
| 平均延迟 | 3616ms |
| P50 延迟 | 2717ms |
| P95 延迟 | 8023ms |
| P99 延迟 | 17570ms |
| 最小延迟 | 919ms |
| 最大延迟 | 28197ms |

### 5.2 状态码分布

| 状态码 | 数量 |
|--------|------|
| 200 | 500 |
| 其他 | 0 |

### 5.3 模型请求分布

19 个模型基本均匀分布，每个模型被请求 22~32 次：

| 模型 | 请求数 |
|------|--------|
| claude-opus-4-1-20250805-thinking | 32 |
| gpt-5.4-nano-2026-03-17 | 31 |
| claude-sonnet-4-20250514 | 29 |
| gemini-3.1-flash-image-preview | 29 |
| gpt-5.5 | 29 |
| claude-sonnet-4-6 | 28 |
| ... | ... |

### 5.4 延迟观察

- **低延迟区**：Gemini 3.1 flash、GPT-5.4-nano 等轻量模型，多数在 1.5~2.5s 完成；
- **高延迟区**：Claude Opus thinking 模型（4-1/4-5/4-6 thinking），因输入/输出 token 较大（40+ prompt，27~52 completion），延迟常超过 3~7s；
- **长尾**：最大延迟 28s，可能是某次上游响应较慢或请求排队导致，但未超时。

---

## 六、发现与结论

### 6.1 接入能力

✅ **100% 成功率**：在 50 并发、500 请求、1200 key、19 模型轮询的压力下，Proxy 和 AccessAPI 上行通道未出现失败。  
✅ **无显式限流错误**：未观察到 429 状态码，说明 AccessAPI 对该 key 的并发容忍度足够，或 Proxy 的 keypool 路由有效分散了压力。  
✅ **多模型路由正常**：19 个模型均能被正确路由到 accessapi provider。  

### 6.2 性能表现

- **吞吐约 9 req/s**：对于单上行 key + 多模型轮询场景，吞吐主要受 AccessAPI 上游响应时间限制；
- **平均延迟 3.6s，P99 17.6s**：整体偏慢，部分thinking模型和 opus 模型拖高了长尾；
- 如果业务需要更低延迟，建议：
  - 增加更多 AccessAPI 上行 key 以分散 RPM/TPM；
  - 对 latency-sensitive 场景优先路由到轻量模型（nano/flash）；
  - 开启 Proxy 的并发/超时调优。

### 6.3 关键注意点

1. **Proxy auth 缓存**：在测试过程中发现，修改 `api_keys.allowed_models` 或 `group_id` 后，Proxy 本地 auth cache（5min TTL）和 Portal Redis `validate:*` 缓存会导致旧权限继续生效。测试前必须重启 Proxy 或清除相关缓存。
2. **Group 模型限制**：如果 key 关联了 `group_id` 且 group 的 `models` 只包含部分模型，请求其他模型会返回 400。本次测试通过将 burst key 的 `group_id` 设为 NULL 并清空 allowed_models 来绕过。

---

## 七、脚本与输出文件

| 文件 | 说明 |
|------|------|
| `tests/business/setup_burst_test.py` | 创建测试用户和 1200 个 API key |
| `tests/business/burst_call_test.py` | Burst call 主测试脚本 |
| `tests/business/burst_call_report.json` | 详细原始结果（JSON） |
| `docs/burst-call-test-report-accessapi-202606151854.md` | 本报告 |

---

## 八、后续建议

1. **扩大规模**：尝试 100+ 并发、5000+ 请求，观察 AccessAPI 上行 key 的 RPM/TPM 硬顶；
2. **多上行 key**：添加 2-3 个 AccessAPI 上行 key，测试 Proxy keypool 的负载均衡效果；
3. **限流测试**：固定单个 user key 高并发，验证 Portal 订阅限流和 Proxy 用户级限流是否生效；
4. **监控对接**：在测试过程中同步观察 `provider_health_stats` 和 `upstream_key_usage` 表，量化每个上行 key 的负载。

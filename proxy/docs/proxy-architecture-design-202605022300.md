# Go Proxy 实现架构与设计方案

> **ACTIVE** - Created: 2026-05-02

---

## 一、概述

本文档总结 Go Proxy 的实现架构、路由逻辑、计费统计、缓存策略等设计方案，基于与 Node.js Proxy 的对比分析和业务需求讨论。

---

## 二、用户类型与路由策略

### 2.1 用户类型定义

| 类型 | access_mode | allowed_models | 优先级池 |
|------|-------------|----------------|----------|
| **单一产品专属** | `single_model` | 1 个模型 | 高优先级 |
| **轮询模式** | `rotating` | 最多 5 个 | 高优先级 |
| **自动均衡** | `auto_balance` | 最多 5 个 | 高优先级 |
| **企业用户** | `enterprise` | 自由选择 | 高优先级 |
| **通用用户** | `general` | 默认模型池 | 普通池 |
| **经济模式** | `economy` | 默认模型池 | 普通池 |

### 2.2 优先级池分层

```
Key Pool + Affinity Cache (耦合)
│
├── 高优先级池 (enterprise + subscription + single_model)
│   ├── Key List: [key-A(RPM余量大), key-B(RPM余量大), ...]
│   └── Affinity Cache: {fingerprint → key_id}
│
└── 普通池 (general + economy)
    ├── Key List: [key-X, key-Y, ...]
    └── Affinity Cache: {fingerprint → key_id}
```

---

## 三、请求处理流程

### 3.1 完整流程

```
1. Auth (验证 User Key)
   ├─ 验证 Key 有效性 (调用 Portal)
   ├─ 获取 access_mode, allowed_models, rate_limit_rpm
   └─ 检查缓存的余额信息 (余额为 0 则拒绝)

2. Rate Limit (基于 Key 的 RPM 配置)
   ├─ 维护本地 RPM 计数器
   └─ 检查 Key 的 RPM 限制

3. 确定优先级池
   ├─ enterprise/subscription/single_model → 高优先级池
   └─ general/economy → 普通池

4. 检查 allowed_models
   └─ 用户请求的 model 必须在 allowed_models 列表内

5. 查 Affinity Cache (该池内)
   ├─ 命中 → 检查该 key RPM 是否有余量
   │         ├─ 有余量 → 使用该 key
   │         └─ 无余量 → 从该池选其他有余量的 key
   └─ 未命中 → 从该池选有余量的 key

6. GetProviderFromModel (检测 provider)

7. CreateAdapter (OpenAI/Claude/Gemini/Compatible)

8. ToProvider (转换请求)

9. DoRequest (调用上游)
   └─ 截留原始请求/响应 (用于审计、缓存分析)

10. FromProvider (转换响应)

11. ExtractUsage (提取 usage)

12. RecordChannel + ObserveUsageCache (Affinity Cache)
    └─ 记录到对应优先级池

13. AuditLogger.Log (统计上报)

14. 返回响应
```

### 3.2 流程图

```
请求进入
    ↓
Auth (验证 + 获取配置)
    ↓
Rate Limit (RPM 检查)
    ↓
检查 allowed_models
    ↓
确定优先级池
    ↓
查 Affinity Cache
    ├─ 命中 → 检查 RPM 余量
    │         ├─ 有余量 → 使用缓存 key
    │         └─ 无余量 → 选其他 key
    └─ 未命中 → 选有余量的 key
    ↓
GetProviderFromModel
    ↓
CreateAdapter
    ↓
ToProvider → DoRequest → FromProvider
    ↓
ExtractUsage
    ↓
RecordChannel + ObserveUsageCache
    ↓
AuditLogger.Log
    ↓
返回响应
```

---

## 四、Key Pool 设计

### 4.1 索引方式

按 provider 分组，每个 key 记录自己的 RPM 限制：

```
Key Pool
├── openai: [key1(RPM:60), key2(RPM:120)]
├── anthropic: [key3(RPM:30)]
├── minimax: [key4(RPM:500), key5(RPM:500)]
└── xiaomi: [key6(RPM:100)]
```

### 4.2 Key 选择逻辑

1. 根据 model 确定 provider
2. 从该 provider 的 pool 中选 **有余量的 key**（RPM < 限制）
3. 优先选余量大的 key
4. 如果 Affinity Cache 命中，优先使用缓存的 key（即使 RPM 不是最低）

### 4.3 模型检测

**不硬编码模型名**，由 Portal 配置驱动：

- Admin 在 Portal 添加 Upstream Key 时填写支持的模型列表
- Proxy 同步 Key 时获取 models 列表
- 路由时根据 model 匹配 Key

---

## 五、Affinity Cache 设计

### 5.1 分层 Cache Pool

```go
type AffinityCache struct {
    enterprisePool   sync.Map  // map[string]int
    subscriptionPool sync.Map  // map[string]int
    generalPool      sync.Map  // map[string]int
}
```

### 5.2 Cache Key 生成

```
Cache Key = hash(keyID + model + messages_fingerprint)
```

### 5.3 工作流程

```
1. 请求进入 → 生成 fingerprint
2. 根据用户类型选择对应的 pool
3. 查 pool → 命中则返回 key_id
4. 未命中 → 从 Key Pool 选 key → 记录到 pool
```

### 5.4 内存占用估算

| Cache 类型 | 每条大小 | 10,000 Key | 说明 |
|------------|----------|------------|------|
| Affinity Cache | ~100 bytes | ~1 MB | 分 3 层，各 ~333KB |
| RPM Counter | ~50 bytes | ~500 KB | 分 3 层，各 ~166KB |
| Balance Cache | ~80 bytes | ~800 KB | 不分层 |
| **总计** | | **~2.3 MB** | 完全可接受 |

---

## 六、统计与计费

### 6.1 统计项（所有用户类型）

| 统计项 | 说明 |
|--------|------|
| **requests** | RPM, RPH, RP5H, RPW, RPM（多时间窗口） |
| **cache hit/miss** | input tokens 中的缓存命中/未命中 |
| **tokens** | input, output, cached, reasoning 等 |

### 6.2 计费职责

```
Proxy: 统计 + 上报 → Portal
Portal: 计费 + 扣减
```

### 6.3 审计日志

- **Proxy 采集**: timestamp, key_id, user_id, provider, model, tokens, cost, latency
- **Proxy 上报**: 通过 webhook 发送到 Portal
- **Proxy 存储**: 本地 JSONL 文件作为备份（可选）
- **Portal 存储**: PostgreSQL + 冷存储归档（7年 GDPR）

---

## 七、数据截留点

### 7.1 截留位置

```
请求进入
    ↓
Auth (可截留: 用户信息、Key 信息)
    ↓
Rate Limit (可截留: 请求频率)
    ↓
Adapter.ToProvider (可截留: 转换后的请求)
    ↓
DoRequest (可截留: 原始请求/响应)
    ↓
Adapter.FromProvider (可截留: 转换后的响应)
    ↓
ExtractUsage (可截留: usage 统计)
    ↓
返回响应
```

### 7.2 截留用途

| 截留点 | 用途 |
|--------|------|
| DoRequest 之后 | Cache hit 分析、审计日志、内容审核 |
| ExtractUsage 之后 | 计费、配额检查 |

---

## 八、Provider Adapter 架构

### 8.1 设计选择

采用 new-api 的方式：**3 个独立 adapter + OpenAI 兼容**

```
openai    → OpenAIAdaptor (独立)
anthropic → ClaudeAdaptor (独立)
google    → GeminiAdaptor (独立)
其他全部  → OpenAICompatibleAdaptor (复用)
```

### 8.2 覆盖范围

- Kimi、Xiaomi、Minimax 等都是 OpenAI 兼容
- 只有 Anthropic、Google 需要独立 adapter
- 覆盖 90%+ Provider

### 8.3 不需要 Sanitizer

- Proxy 是 API 网关，不应修改上游响应
- 安全责任在 Portal（用户管理、内容审核）
- new-api 也没有 Sanitizer

---

## 九、实施优先级

| 优先级 | 功能 | 说明 |
|--------|------|------|
| **P0** | User Key 路由逻辑 | 按类型分流 |
| **P0** | 多时间窗口限流 | RPM/RPH/RP5H/RPW/RPM |
| **P0** | Allowed Models 检查 | 所有用户类型 |
| **P1** | Key Pool 分层 | 高优先级池 + 普通池 |
| **P1** | Affinity Cache 分层 | 按用户类型分池 |
| **P2** | Economy 模式路由 | 选有富余的 key |
| **P2** | 余额快速检查 | Auth 时检查缓存余额 |

---

## 十、与 Node.js Proxy 对比

### 10.1 功能对比

| 功能 | Node.js | Go Proxy | 状态 |
|------|---------|----------|------|
| **Provider 支持** | 16 个独立 adapter | 3 个 + 兼容 | ✅ 覆盖 90%+ |
| **Affinity Cache** | ❌ 未实现 | ✅ 已实现 | Go 胜 |
| **Key Pool 同步** | ✅ | ✅ | 平手 |
| **Rate Limit** | ✅ Redis | ✅ Redis + 本地 | Go 胜 |
| **Auth 降级** | ❌ | ✅ | Go 胜 |
| **Economy 模式** | ✅ | ❌ 待实现 | Node 胜 |
| **模型权限控制** | ✅ | ❌ 待实现 | Node 胜 |

### 10.2 测试结果

| Provider | Node.js | Go Proxy |
|----------|---------|----------|
| Kimi 非流式 | ✅ | ✅ |
| Kimi 流式 | ✅ | ✅ |
| Xiaomi 非流式 | ✅ | ✅ |
| Minimax M2.7 | ✅ | ✅ |
| Minimax M2.5 | ✅ | ✅ |

---

## 十一、关键设计决策

### 11.1 Adapter 架构

**选择**: 3 个独立 + OpenAI 兼容（new-api 方式）

**原因**:
- Kimi、Xiaomi、Minimax 都是 OpenAI 兼容
- 只有 Anthropic、Google 需要独立 adapter
- 维护成本低，覆盖 90%+ 场景

### 11.2 模型检测

**选择**: Portal 配置驱动，不硬编码

**原因**:
- 新模型层出不穷，代码无法预知
- Admin 在 Portal 配置即可
- Proxy 同步 Key 时获取 models 列表

### 11.3 计费职责

**选择**: Proxy 只统计上报，Portal 负责计费

**原因**:
- Proxy 轻量化，不直接访问数据库
- Portal 负责用户管理、余额扣减
- 架构清晰，职责分离

### 11.4 缓存存储

**选择**: 内存 + Redis 双层

**原因**:
- 内存快速读取
- Redis 多实例共享
- 进程重启不丢失

---

## 十二、待实现功能

| 功能 | 状态 | 优先级 |
|------|------|--------|
| User Key 路由逻辑 | ❌ | P0 |
| 多时间窗口限流 | ❌ | P0 |
| Allowed Models 检查 | ❌ | P0 |
| Key Pool 分层 | ❌ | P1 |
| Affinity Cache 分层 | ❌ | P1 |
| Economy 模式路由 | ❌ | P2 |
| 余额快速检查 | ❌ | P2 |
| 数据截留接口 | ❌ | P2 |

---

## 十三、相关文档

- [ai-proxy-go-design.md](./ai-proxy-go-design.md) - Go Proxy 原始设计
- [phase3-model-optimization.md](./phase3-model-optimization.md) - 模型优化方案
- [portal-rewrite-plan-202605021942.md](../../ai-portal-go/docs/portal-rewrite-plan-202605021942.md) - Portal 重写计划

---

**文档更新时间**: 2026-05-02

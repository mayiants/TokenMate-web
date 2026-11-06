> **DRAFT / PROPOSAL** — 面向 AI-Portal 上游状态与用量可视化的改进方案（V2）

# AI-Portal 上游状态与用量可视化优化方案

> **更新说明（V2）**: 根据产品侧反馈，本次修订重点调整了展示维度：弱化具体 upstream provider，强调面向用户的 **Model 服务能力 / User Group 质量**；同时明确 status 页面前期登录可见、中期再公开；并补充了 UI 组件化与风格推广规划。

**文档编号**: upstream-status-optimization-proposal-202606201003  
**创建时间**: 2026-06-20  
> **状态**: Phase 1 ✅ | Phase 2 ✅ | Phase 3 ✅ (7/7 — 异常事件 Tab / Key 运维台扩展 经评估放弃) | Phase 4 ❌ (0/5 — 长期规划)  
**涉及项目**: `ai-portal-go` (Portal) + `ai-proxy-go` (Proxy)  
**参考对象**: [IKunCode Status](https://status.ikuncode.cc/)（基于 [RelayPulse](https://github.com/prehisle/relay-pulse)）

---

## 1. 背景与目标

### 1.1 业务意图

我们对 upstream key 和 provider 的统计，核心目的有两个：

1. **对外展示系统服务质量（用户侧）**：面向企业客户和普通用户，展示“某个 Model 在某个 User Group 下的可用率、延迟、趋势”，提升平台透明度与用户信任。用户侧不暴露具体 upstream provider。
2. **对内支持 admin 运营与对账（运营侧）**：保留 provider / upstream key 级别的用量与健康明细，方便对账、计费、审计，以及快速定位上游故障。

### 1.2 当前实现

Portal 目前已存在三个相关页面：

| 页面 | 路径 | 权限 | 主要数据 | 当前形态 |
|------|------|------|----------|----------|
| Service Status | `/status` | 登录用户（前期） | `model_group_health_stats` 表：按 model+group+5min-bucket 聚合的可用率/延迟/状态 | 摘要卡片 + 周期切换 + model×group 表格 |
| Upstream Operations Center | `/admin/upstream-ops` | Admin | `provider_health_stats` / `provider_health_stats_model` / `model_group_health_stats`：provider/model/group 多维健康 | 服务质量 / provider×model / 错误分布 Tab + 导出 |
| Provider Health | `/admin/provider-health` | Admin | `provider_health_stats` 表：按 provider+5min-bucket 聚合的请求/成功/失败/延迟/状态 | 旧版页面，功能已合并到 upstream-ops |
| Upstream Key Monitor | `/admin/monitor-upstream` | Admin | `upstream_keys` + `upstream_key_usage`：key 验证状态/用量/错误/利用率 | key 列表 + 周期筛选 + 导出 + 详情弹窗（延迟趋势/错误分布） |
| Upstream Key Usage | `/admin/upstream-usage` | Admin | `upstream_key_usage` 表：按 key+model+date 日级聚合的请求/token/cache/错误/延迟 | 已合并到 `/admin/upstream-ops` 的 Usage Details Tab |

### 1.3 问题意识

对比 IKunCode 的 RelayPulse 状态页，当前 Portal 在以下三方面存在明显差距：

1. **数据不够细致**：provider health 只到 provider 维度，没有 model/通道维度；错误只分 success/failure，缺少 timeout/rate_limit/5xx/4xx/content_mismatch 等细分；没有“可用率百分比”和“缺失数据”概念。
2. **可视化不够好**：只有表格和单张折线图，缺少时间范围热力图、状态渐变色彩、sparkline、多模型分层趋势等直观表达。
3. **仅限 admin 查看**：没有公开给用户的状态页，无法像 RelayPulse 一样把“系统健康”作为平台透明度的一部分展示出来。

---

## 2. 当前实现细节梳理

### 2.1 上游用量统计 (`/admin/upstream-usage`)

- **数据源**: `upstream_key_usage` 表，按 `(upstream_key_id, model, date)` 唯一键日级聚合。
- **字段**: `provider`, `model`, `date`, `request_count`, `input_tokens`, `output_tokens`, `cache_read_tokens`, `cache_creation_tokens`, `error_count`, `avg_latency_ms`, `key_preview`。
- **API**:
  - `GET /api/admin/upstream-usage` 分页列表
  - `GET /api/admin/upstream-usage/summary` 按 provider 汇总
  - `GET /api/admin/upstream-usage/export` CSV 导出
- **前端**: HTML 模板 + 原生 JS，汇总卡片 + 筛选表格 + 分页。

### 2.2 Provider 健康 (`/admin/provider-health`)

- **数据源**: `provider_health_stats` 表，按 `(provider, bucket)` 唯一键 5 分钟桶聚合。
- **字段**: `request_count`, `success_count`, `failure_count`, `timeout_count`, `rate_limit_count`, `error_5xx_count`, `error_4xx_count`, `total_latency_ms`, `avg_latency_ms`, `error_rate`, `status`。
- **状态判定**（当前）:
  - `healthy`: 错误率 < 5% 且 平均延迟 < 3000ms
  - `degraded`: 错误率 < 20% 且 平均延迟 < 10000ms
  - `unhealthy`: 其他
- **API**:
  - `GET /api/admin/provider-health?window=15m/30m/1h/12h/24h`
  - `GET /api/admin/provider-health/:provider?window=...`
  - `GET /api/admin/provider-health/export`
- **前端**: 状态卡片 + 表格 + 详情弹窗（Chart.js 折线图：请求/延迟/错误率），7 天小时级历史。

### 2.3 Proxy 侧数据上报

- **上游用量**: `UpstreamUsageBuffer` 按 `(upstreamKeyID, model, date)` 内存聚合，30 秒批量 `POST /api/webhook/upstream-usage-batch`。
- **Provider 健康**: `ProviderHealthBuffer` 逐条记录上游请求结果，30 秒批量 `POST /api/webhook/provider-health-batch`，Portal 端按 5 分钟桶聚合。
- **字段**: `Provider`, `TraceID`, `StatusCode`, `LatencyMs`, `ErrorType`, `ErrorMessage`, `ServerID`, `Timestamp`。

---

## 3. 与 RelayPulse 的对比分析

### 3.1 RelayPulse 的核心设计

RelayPulse 是一个开源的 LLM API 中转服务可用性观测台，其关键理念是**拒绝 API 假活**：不仅探测 HTTP 连通性，还消耗真实 Token 发起 API 调用并校验响应内容。

**数据维度**:
- Provider × Service（CC/CX/GM）× Channel × Model 四维矩阵
- 时间范围：90m / 24h / 7d / 30d
- 每个时间桶记录：status、latency、availability、status_counts
- 多模型通道使用 LayeredHeatmap 垂直堆叠展示

**状态定义**:
- `AVAILABLE` (1), `DEGRADED` (2), `UNAVAILABLE` (0), `MISSING` (3/-1)

**状态细分 (StatusCounts)**:
- 波动：`slow_latency`, `rate_limit`
- 不可用：`server_error`, `client_error`, `auth_error`, `invalid_request`, `network_error`, `response_timeout`, `content_mismatch`

**可视化**:
- 横向热力图趋势条（每个色块一个时间桶，颜色按 availability 渐变）
- 90m 模式下绿色/黄色块按延迟再做渐变
- 全局 Tooltip 悬浮显示：模型、时间范围、可用率、监测次数、状态统计、错误细分、延迟
- 表头说明 Tooltip、通道类型 Tooltip、模型名 Tooltip
- rpdiag 独立质量分 Sparkline

**对外性**:
- 整页公开访问，无需登录
- 截图模式 (`?screenshot=1`)

### 3.2 差距对照表

| 维度 | Portal 当前 | RelayPulse | 差距 |
|------|------------|-----------|------|
| **访问权限** | 仅 Admin | 公开 | ⬆️ 需新增公开视图 |
| **数据维度** | Provider / Model / Date | Provider/Service/Channel/Model/TimeBucket | ⬆️ 需细化到 model+bucket |
| **可用率指标** | 成功率/错误率 | availability % (0-100) | ⬆️ 需计算 availability |
| **状态枚举** | healthy/degraded/unhealthy/unknown | available/degraded/unavailable/missing | ⬆️ 增加 missing 语义 |
| **错误细分** | success/failure + timeout/5xx/4xx/rate_limit | 10+ 种细分状态 | ⬆️ 扩展错误分类 |
| **时间粒度** | 5min / 1h / 1d | 90m/24h/7d/30d 多档 | 🔶 基本满足，需多档切换 |
| **趋势可视化** | 单张 Chart.js 折线图 | 横向热力图条 | ⬆️ 需新增热力图 |
| **Tooltip** | 无 | 全局/表头/通道/模型/状态丰富提示 | ⬆️ 需补充 |
| **多模型展示** | 无 | LayeredHeatmap 垂直堆叠 | 🔶 可选增强 |
| **延迟可视化** | 单一数字 | 延迟渐变色彩 | ⬆️ 需增强 |
| **独立质量分** | 无 | rpdiag Sparkline | 🔶 长期规划 |
| **响应式/主题/多语言** | 基础 | 完善 | 🔶 可逐步改进 |

---

## 4. 优化建议总览

建议围绕 **“一个公开页 + 一个 admin 运营中心 + 一套更细的数据模型”** 进行改造。

### 4.1 状态页 (`/status`) — 前期登录可见，中期逐步公开

- 路径：`/status`（或 `/status/services`）
- 权限：
  - **前期（V1）**: 用户登录后可访问（`RequireAuthWeb`），让已登录用户（尤其是企业客户）能看到平台服务质量。
  - **中期（V2）**: 移除登录要求，作为公开透明度页面。
- 展示维度调整：
  - **弱化具体 Provider**：不暴露上游供应商名称/ID（如 openai/azure/anthropic 等内部标识）。
  - **强调 Model 服务能力**：以 `Model`（如 `gpt-4o`、`claude-sonnet-4`）作为核心行维度。
  - **强调 User Group 视角**：可切换视角查看不同 group（`enterprise`、`subscription`、`general`）对应的服务质量，或按 group 聚合展示。
  - **强调服务质量指标**：可用率、延迟分布、错误细分、最近监测时间。
- 内容：
  - 平台整体健康摘要（正常服务数 / 异常服务数）
  - Model / User Group 可用性矩阵表格
  - 每个 model+group 的趋势热力图（24h / 7d / 30d）
  - 最后更新时间、自动刷新开关
- 数据脱敏：
  - ❌ 不展示 upstream provider ID / upstream key ID / key_preview / 用户数据 / 成本 / provider base_url
  - ✅ 只展示 model 名、group 类型、可用率、延迟、趋势、状态

### 4.2 上游运营中心 (`/admin/upstream-ops`)

**仅合并 `/admin/upstream-usage` 与 `/admin/provider-health`**，统一为“上游运营中心”；`/admin/monitor-upstream` 作为独立的 **“Key 运维台”** 保留并扩展（见 4.3）。

合并后的运营中心按角色展示不同维度：

- **Admin / Super Admin**：可切换索引维度（provider / model / user group / key），可下钻到 key，可导出。
- **普通用户 / Enterprise 客户**：仅可按 model 或 user group 查看聚合服务质量，**不可切换 provider/key 维度，不可导出**。

Tab 规划：

1. **服务质量总览 Tab**
   - model × user group 矩阵（默认视图，面向用户）
   - 时间范围热力图、趋势 sparkline、状态/错误细分 tooltip
   - Admin 可额外切换为 provider × model 视图（用于排障）
2. **用量与对账 Tab**
   - 日级/小时级切换
   - Admin：provider / model / user group / key 多级下钻 + 导出
   - 用户/enterprise：model / group 聚合，只看用量与服务质量，不导出
   - 上游用量 token vs 用户侧计费 token vs 成本对比，差额率异常高亮
3. **异常事件 Tab**
   - 最近状态变为 degraded/unavailable 的事件
   - 错误率突增、延迟突增

### 4.3 Key 运维台（`/admin/monitor-upstream` 保留并扩展）

`admin/monitor-upstream` 不作为上游运营中心的一部分合并，而是继续作为单 key 运维台存在，目标是让 admin 快速判断“某个 upstream key 是否失效、是否需要下掉/限流”。

扩展信息：

- **当前**: key 验证状态、最近探测时间、错误信息
- **新增**:
  - 最近 24h / 7d 请求热度（request_count 趋势 sparkline）
  - 最近 24h / 7d token 利用率（input/output tokens 趋势）
  - 最近错误类型分布（timeout / rate_limit / 5xx / 4xx / auth_error / network 等）
  - 平均延迟与延迟趋势
  - 关联的 provider + model 列表
  - 快速操作：禁用 key、标记维护中、跳转该 key 的对证明细

### 4.4 数据模型增强

#### 4.4.1 `provider_health_stats` 扩展

当前表只支持 provider 维度。建议新增 **`provider_health_stats_model`** 表用于 provider+model 级聚合（admin 排障/对账）；若需区分 user group 的服务质量，可再新增 **`model_group_health_stats`** 表（用户侧状态页）

```sql
CREATE TABLE IF NOT EXISTS provider_health_stats_model (
    provider         VARCHAR(50)  NOT NULL,
    model            VARCHAR(100) NOT NULL,
    bucket           TIMESTAMP    NOT NULL,   -- 5-minute boundary
    request_count    INT DEFAULT 0,
    success_count    INT DEFAULT 0,
    failure_count    INT DEFAULT 0,
    -- 细分错误
    timeout_count    INT DEFAULT 0,
    rate_limit_count INT DEFAULT 0,
    error_5xx_count  INT DEFAULT 0,
    error_4xx_count  INT DEFAULT 0,
    auth_error_count INT DEFAULT 0,
    network_error_count INT DEFAULT 0,
    response_timeout_count INT DEFAULT 0,
    content_mismatch_count INT DEFAULT 0,
    -- 延迟
    total_latency_ms BIGINT DEFAULT 0,
    avg_latency_ms   INT DEFAULT 0,
    slow_latency_ms  INT DEFAULT 5000,       -- 该 provider+model 的慢延迟阈值
    -- 可用率
    availability     DECIMAL(5,2) DEFAULT -1, -- -1 表示无数据
    error_rate       DECIMAL(5,4) DEFAULT 0,
    status           VARCHAR(20) DEFAULT 'unknown',
    created_at       TIMESTAMP DEFAULT NOW(),
    updated_at       TIMESTAMP DEFAULT NOW(),
    PRIMARY KEY (provider, model, bucket)
);

CREATE INDEX idx_provider_health_model_bucket ON provider_health_stats_model(bucket DESC);
CREATE INDEX idx_provider_health_model_provider_bucket ON provider_health_stats_model(provider, model, bucket DESC);
```

#### 4.4.2a Model-Group 服务质量表（推荐新增）

面向用户视角，按 **model + user_group + bucket** 聚合，用于 status 页和 admin 服务质量 Tab：

```sql
CREATE TABLE IF NOT EXISTS model_group_health_stats (
    model            VARCHAR(100) NOT NULL,
    group_id         VARCHAR(50)  NOT NULL,   -- enterprise / subscription / general 等
    bucket           TIMESTAMP    NOT NULL,    -- 5-minute boundary
    request_count    INT DEFAULT 0,
    success_count    INT DEFAULT 0,
    failure_count    INT DEFAULT 0,
    timeout_count    INT DEFAULT 0,
    rate_limit_count INT DEFAULT 0,
    error_5xx_count  INT DEFAULT 0,
    error_4xx_count  INT DEFAULT 0,
    auth_error_count INT DEFAULT 0,
    network_error_count INT DEFAULT 0,
    response_timeout_count INT DEFAULT 0,
    content_mismatch_count INT DEFAULT 0,
    total_latency_ms BIGINT DEFAULT 0,
    avg_latency_ms   INT DEFAULT 0,
    slow_latency_ms  INT DEFAULT 5000,
    availability     DECIMAL(5,2) DEFAULT -1,
    error_rate       DECIMAL(5,4) DEFAULT 0,
    status           VARCHAR(20) DEFAULT 'unknown',
    created_at       TIMESTAMP DEFAULT NOW(),
    updated_at       TIMESTAMP DEFAULT NOW(),
    PRIMARY KEY (model, group_id, bucket)
);

CREATE INDEX idx_model_group_health_bucket ON model_group_health_stats(bucket DESC);
CREATE INDEX idx_model_group_health_model_group ON model_group_health_stats(model, group_id, bucket DESC);
```

**注意**: 保留现有 `provider_health_stats` 表用于 provider 级汇总，避免破坏现有 API；新增 `provider_health_stats_model` 用于 admin 内部 provider+model 排障与对账；新增 `model_group_health_stats` 用于面向用户的服务质量展示。三者互补，不要互相替代。

#### 4.4.3 `upstream_key_usage` 增强

当前日级聚合可保留用于对账。建议新增 **`upstream_key_usage_hourly`** 表支持更细粒度：

```sql
CREATE TABLE IF NOT EXISTS upstream_key_usage_hourly (
    upstream_key_id UUID NOT NULL REFERENCES upstream_keys(id),
    provider VARCHAR(50) NOT NULL,
    model VARCHAR(100) NOT NULL,
    hour TIMESTAMP NOT NULL,  -- 小时级
    request_count INT DEFAULT 0,
    input_tokens BIGINT DEFAULT 0,
    output_tokens BIGINT DEFAULT 0,
    cache_read_tokens BIGINT DEFAULT 0,
    cache_creation_tokens BIGINT DEFAULT 0,
    error_count INT DEFAULT 0,
    avg_latency_ms INT DEFAULT 0,
    total_latency_ms BIGINT DEFAULT 0,
    PRIMARY KEY (upstream_key_id, model, hour)
);
```

用于 admin 对账时的小时级下钻，以及公开页不展示此粒度（避免泄露 key 信息）。

#### 4.4.4 状态枚举对齐

建议统一状态语义：

| 状态名 | 数值 | 含义 | 可视化颜色 |
|--------|------|------|-----------|
| `available` | 1 | 可用 | 绿色 |
| `degraded` | 2 | 波动/降级 | 黄色 |
| `unavailable` | 0 | 不可用 | 红色 |
| `missing` | -1/3 | 无数据/未配置 | 灰色 |

Portal 当前 `provider_health_stats.status` 使用 `healthy/degraded/unhealthy/unknown`，建议公开 API 映射为 `available/degraded/unavailable/missing`，内部保留原有字段兼容。

### 4.5 Proxy 侧数据增强

在 `ai-proxy-go/pkg/billing/billing.go` 的 `ProviderHealthRecord` 中，当前 `ErrorType` 只有 `timeout`, `rate_limit`, `error_5xx`, `error_4xx`。建议扩展为：

```go
type ErrorType string

const (
    ErrorTypeNone            ErrorType = ""          // success
    ErrorTypeTimeout         ErrorType = "timeout"
    ErrorTypeRateLimit       ErrorType = "rate_limit"
    ErrorTypeServerError     ErrorType = "error_5xx"
    ErrorTypeClientError     ErrorType = "error_4xx"
    ErrorTypeAuthError       ErrorType = "auth_error"        // 401/403
    ErrorTypeInvalidRequest  ErrorType = "invalid_request"   // 400
    ErrorTypeNetworkError    ErrorType = "network_error"     // 连接失败/DNS
    ErrorTypeResponseTimeout ErrorType = "response_timeout"  // 连接成功但读响应超时
    ErrorTypeContentMismatch ErrorType = "content_mismatch"  // 响应内容校验失败
)
```

同时建议 Proxy 在失败路径中**按 HTTP 状态码和错误字符串规范化 ErrorType**，并随 `ProviderHealthRecord` 上报。

长期可借鉴 RelayPulse 的“真实 Token 探测”思想，对关键 provider 增加轻量级内容校验（例如检查响应中是否包含预期字段），用于识别“HTTP 200 但假活”。

---

## 5. API 设计

### 5.1 状态 API（前期需登录，中期可公开）

```http
GET /api/status?period=24h&group=enterprise
Authorization: Bearer <token>   # 前期需要；中期可移除
```

响应（参考 RelayPulse 结构，但只返回脱敏字段，以 model + group 为核心维度）：

```json
{
  "meta": {
    "period": "24h",
    "count": 12,
    "slow_latency_ms": 5000,
    "updated_at": "2026-06-20T10:00:00Z"
  },
  "data": [
    {
      "model": "gpt-4o",
      "group_id": "enterprise",
      "service_type": "cx",
      "uptime": 99.2,
      "current_status": "available",
      "last_check_latency_ms": 1234,
      "last_check_at": "2026-06-20T09:58:00Z",
      "history": [
        {
          "timestamp": "2026-06-20T09:00:00Z",
          "availability": 100,
          "latency_ms": 1200,
          "status": "available",
          "status_counts": {
            "available": 120,
            "degraded": 0,
            "unavailable": 0,
            "missing": 0
          }
        }
      ]
    }
  ]
}
```

### 5.2 上游运营中心 API（按角色隔离索引维度）

统一前缀 `/api/admin/upstream-ops`（admin 角色）或 `/api/status`（用户/enterprise 角色）。

```http
# 1. 服务质量列表（model × group 视角）
GET /api/admin/upstream-ops/health?period=24h&model=gpt-4o&group=enterprise

# 2. 服务质量摘要
GET /api/admin/upstream-ops/health/summary?period=24h

# 3. provider-model 级明细（仅 admin）
GET /api/admin/upstream-ops/health/providers?period=24h&provider=openai&model=gpt-4o

# 4. 用量对账（按角色返回不同粒度）
GET /api/admin/upstream-ops/usage?period=24h&model=gpt-4o&group=enterprise
GET /api/admin/upstream-ops/usage/hourly?model=gpt-4o&group=enterprise&date_from=2026-06-19&date_to=2026-06-20

# 5. 错误细分统计
GET /api/admin/upstream-ops/errors?model=gpt-4o&period=24h

# 6. 导出（仅 admin）
GET /api/admin/upstream-ops/export?period=7d
```

**角色权限控制**：

| 角色 | 可索引维度 | 可下钻 | 可导出 |
|------|-----------|--------|--------|
| super/system admin | model / group / provider / key | 到 key | ✅ |
| admin | model / group / provider | 到 provider | ✅（可配置） |
| enterprise 用户 | model / group | 无 | ❌ |
| 普通用户 | model / group（仅公开状态页） | 无 | ❌ |

- 非 admin 请求若携带 `provider` 或 `key` 参数，直接返回 403 或忽略参数。
- 导出接口必须校验 `RequireAdmin()`。

### 5.3 现有 API 兼容

- `/api/admin/provider-health` 与 `/api/admin/upstream-usage` 保持现有返回，作为旧版兼容。
- 新增 `/api/admin/upstream-ops` 系列作为合并后的增强版，逐步替代旧接口。
- `/admin/monitor-upstream` 保留并扩展，对应 API 增加热度/利用率/错误分布字段。
- `/api/status` 前期使用 `RequireAuthWeb` 鉴权（仅登录用户可访问），中期再移除鉴权作为公开 API。

---

## 6. UI/UX 改进建议

### 6.1 状态页 (`/status`)

建议采用 RelayPulse 类似的卡片/表格布局，但展示维度改为 **Model × User Group**：

1. **Header**: 平台名 + 最后更新时间 + 自动刷新开关 + 语言切换 + 登录提示（前期）
2. **统计摘要**: 正常服务数 / 异常服务数（按 model 或 group 聚合）
3. **控制栏**:
   - 时间范围选择（24h / 7d / 30d）
   - User Group 切换（All / Enterprise / Subscription / General）
   - Model 搜索
4. **矩阵表格**:
   - 列：Model | Service | User Group | 可用率 | 最后监测 | 趋势热力图
   - 可用率数字使用红→黄→绿渐变
   - 热力图每个色块代表一个时间桶
5. **Tooltip**: 悬浮热力图块显示时间、可用率、请求数、成功/失败、错误细分、延迟
6. **Footer**: 平台声明、数据来源说明、免责声明

### 6.2 上游运营中心（`/admin/upstream-ops`）

**仅合并 `/admin/upstream-usage` 与 `/admin/provider-health`**；`/admin/monitor-upstream` 作为独立的 Key 运维台保留（见 6.3）。

统一入口按角色展示不同维度：

- **Admin / Super Admin**：默认以 model × group 视图为主，可切换 provider × model 视图，可下钻到 key，可导出。
- **Enterprise / 普通用户**：仅可查看 model × group 聚合数据，无 provider/key 筛选，无导出。

#### Tab 1: 服务质量总览
- 默认视图：model × user group 矩阵
- Admin 可切换视图：provider × model 矩阵（用于排障）
- 多时间范围切换（24h / 7d / 30d，admin 额外支持 90m）
- 每行 mini sparkline + 横向热力图
- 状态/错误细分 tooltip
- 点击 model 行可下钻（admin 可到 provider/key，用户不可）

#### Tab 2: 用量与对账
- 日级/小时级切换
- Admin：provider / model / user group / key 多级下钻 + 导出
- 用户/enterprise：model / group 聚合，只读，不可导出
- 对比列：上游用量 token / 用户侧计费 token / 成本 / 差额
- 异常高亮：差额率超过阈值标红

#### Tab 3: 异常事件
- 最近 24h 内状态变为 degraded/unavailable 的事件
- 错误率突增（环比 > 50%）
- 延迟突增（环比 > 100%）

### 6.3 Key 运维台（`/admin/monitor-upstream` 保留并扩展）

`admin/monitor-upstream` 不作为运营中心合并，而是作为单 key 运维台保留，帮助 admin 快速判断“某个 upstream key 是否失效、是否需要下掉/限流”。

扩展内容：

- **当前已有**: key 验证状态、最近探测时间、错误信息
- **新增**:
  - 最近 24h / 7d 请求热度（request_count sparkline）
  - 最近 24h / 7d token 利用率（input/output tokens 趋势）
  - 错误类型分布（timeout / rate_limit / 5xx / 4xx / auth_error / network 等）
  - 平均延迟与延迟趋势
  - 关联 provider + model 列表
  - 快速操作：禁用 key、标记维护中、跳转到该 key 的对证明细

### 6.4 可视化组件建议（单页实现）

- **热力图条 (`<heatmap-bar>`)**: 纯 CSS/DOM 实现，每个 `div` 一个时间桶，背景色按 availability 计算。
- **颜色渐变**: 参考 RelayPulse 的 `availabilityToColor`/`latencyToColor` 函数，从 CSS 变量读取主题色。
- **全局 Tooltip (`<global-tooltip>`)**: Portal 当前使用原生 title 和简单弹窗，建议实现一个固定定位的全局 Tooltip 组件，跟随鼠标/点击触发。
- **Sparkline (`<sparkline-chart>`)**: 使用 SVG 或轻量库（如 [sparkline](https://github.com/fnando/sparkline)）绘制 30d/7d 趋势。
- **状态徽章 (`<status-badge>`)**: 统一 `available/degraded/unavailable/missing` 的色块/文字样式。
- **摘要卡片 (`<metric-card>`)**: 正常服务数、异常服务数、平均可用率、平均延迟等。

### 6.5 UI 设计系统沉淀与推广

本次 `/status`、`/admin/upstream-ops` 和 `/admin/monitor-upstream` 的可视化组件，应作为 Portal 统一设计系统的第一批组件进行沉淀，后续推广到用户控制台、admin 仪表盘等页面。

#### 6.5.1 设计 Tokens

建议在 `web/static/css/` 或 Tailwind 配置中统一定义以下变量：

```css
:root {
  --status-available: #22c55e;   /* green-500 */
  --status-degraded: #eab308;    /* yellow-500 */
  --status-unavailable: #ef4444; /* red-500 */
  --status-missing: #9ca3af;     /* gray-400 */
  --latency-low: #22c55e;
  --latency-mid: #eab308;
  --latency-high: #ef4444;
  --heatmap-empty: #e5e7eb;
}
```

#### 6.5.2 可复用组件清单

| 组件 | 文件建议 | 职责 | 使用场景 |
|------|---------|------|----------|
| `StatusBadge` | `js/components/status-badge.js` | 根据状态渲染色块+文字 | status 页、admin 表格、用户仪表盘 |
| `HeatmapBar` | `js/components/heatmap-bar.js` | 横向热力图条 | status 页矩阵、admin 健康页 |
| `Sparkline` | `js/components/sparkline.js` | SVG 迷你趋势图 | 矩阵行尾、metric card |
| `GlobalTooltip` | `js/components/tooltip.js` | 全局悬浮提示 | 热力图块、表头、状态徽章 |
| `MetricCard` | `js/components/metric-card.js` | 大数字+趋势 | status 摘要、admin dashboard |
| `TimeRangeSelector` | `js/components/time-range-selector.js` | 24h/7d/30d 切换 | status 页、admin 健康页 |
| `ResponsiveTable` | `js/components/responsive-table.js` | 桌面表格/移动端卡片自适应 | 所有列表页 |

#### 6.5.3 JS API 约定（vanilla JS）

为了与现有模板体系兼容，组件采用原生 Web Components 或函数式封装：

```js
// 热力图条
const bar = new HeatmapBar({
  data: [{ timestamp, availability, latency, status_counts }],
  colorFn: availabilityToColor,
  tooltip: globalTooltip,
  bucketSize: '5m'
});
container.appendChild(bar.el);

// Sparkline
Sparkline.draw(svgElement, values, { width: 120, height: 24, color: '#22c55e' });

// 全局 Tooltip
globalTooltip.show({ x, y, html });
globalTooltip.hide();
```

#### 6.5.4 推广路径

1. **第一期**：在 `/status`、`/admin/upstream-ops` 和 `/admin/monitor-upstream` 中落地 `HeatmapBar`、`Sparkline`、`GlobalTooltip`、`StatusBadge`。
2. **第二期**：将 `MetricCard`、`TimeRangeSelector`、`ResponsiveTable` 推广到用户控制台首页、admin dashboard。
3. **第三期**：整理为 Storybook/组件文档，建立 Portal 前端组件规范。

### 6.6 响应式

- 桌面：完整矩阵表格
- 平板/移动端：卡片视图，每张卡片包含 model / group / 可用率 / 趋势图 / 最后更新时间

---

## 7. 分阶段实施建议

### Phase 1: 数据基础增强（2-3 周）

**目标**: 让 health 数据支持 model 维度和更细错误分类。

- [x] Proxy: 扩展 `ProviderHealthRecord.ErrorType` 枚举
- [x] Proxy: 在失败路径中规范化错误类型（基本按 HTTP 状态码/transport error 归类；`response_timeout`/`content_mismatch` 的实际触发场景仍需接入）
- [x] Portal: 新增 `provider_health_stats_model` 表（admin 排障/对账）
- [x] Portal: 新增 `model_group_health_stats` 表（用户侧状态页）
- [x] Portal: 修改 `/api/webhook/provider-health-batch`，同时写入 provider 级、provider+model 级、model+group 级表
- [x] Portal: 新增 `/api/admin/upstream-ops` API（model / provider 多维度，admin 角色）
- [x] Portal: 扩展 `/api/admin/monitor-upstream` API（增加请求/token/错误/利用率/延迟趋势/错误分布）
- [x] Portal: 新增 `/api/status` API（脱敏、只返回聚合数据，前期登录鉴权）

### Phase 2: 登录可见状态页（2 周）

**目标**: 上线 `/status` 页面，前期仅登录用户可访问。

- [x] 新增 `web/templates/status.html`
- [x] 路由使用 `RequireAuthWeb()` 鉴权
- [x] 实现时间范围切换、自动刷新、User Group 筛选、Model 搜索
- [x] 实现 model × user group 矩阵表格 + 热力图条
- [x] 实现全局 Tooltip、sparkline、metric cards
- [x] 在登录后导航/用户菜单增加“系统状态”入口
- [x] 对 `/api/status` 加登录校验（通过 `RequireAuth()`；公开前需补充独立 rate limit）

### Phase 3: Admin 运营中心升级（3-4 周）

**目标**: 合并 upstream-usage 与 provider-health 为上游运营中心，扩展 Key 运维台。

- [x] 新增 `/admin/upstream-ops` 页面（合并 upstream-usage + provider-health）
- [x] 服务质量总览 Tab：model × group 矩阵（默认），admin 可切换 provider × model 视图
- [x] 用量与对账：独立页面 `/admin/reconciliation-ops` 已上线（日级 24h/7d/30d/90d 切换、custom flatpickr 日期、provider 下拉筛选、model+key 合并模糊搜索、差额分析、CSV 导出）；小时级切换已确认暂不需要（24h 粒度足够），group 维度评估后不需要（upstream_key_usage 无 group 字段，key 级对账已够细）
- [x] 异常事件 Tab：评估后放弃增强 — 静态错误分布表已足够定位问题，sparkline/警告色对运营决策增值有限
- [x] 扩展 `/admin/monitor-upstream`：评估后放弃增强 — 页面为只读监控，非管理台；详情弹窗已有 Chart.js 趋势图；heatmap 在 key 数量多时渲染负担大、信息密度低
- [x] 为 enterprise/普通用户展示只读的 model/group 视图（`/upstream-ops` 用户只读页已上线）
- [⏸️] 移除 `/status` 登录要求 → **暂缓**，等功能完善后再考虑

**Phase 3 — ✅ 全部完成 (7/7)**

### Phase 4: 高级功能（长期）

- [ ] 真实 Token 探测 / 内容校验（识别 API 假活）
- [ ] 独立质量评分系统（类似 rpdiag）
- [ ] 告警通知（Telegram / Email / Webhook）
- [ ] 多语言/主题完善
- [ ] 截图模式 (`?screenshot=1`)

---

## 7b. 🚧 项可视化优化建议（基于现有代码）

### 7b.1 异常事件 Tab — 评估后放弃 ✖️

> **结论**: 当前静态错误分布表已足够定位问题（哪个 model/group 错误最多）。period 切换已由全局 `#period-select` 统一控制（24h/7d/30d），无需额外添加。sparkline/警告色对运营决策增值有限，不做。

原提案内容（存档）：

当前 `tab-anomalies` 仅展示 model×group 的静态错误计数表（`/api/admin/upstream-ops/errors`），无法感知错误的时间分布和趋势。

**建议增强（低改动成本）**：

1. **错误趋势 Sparkline**：在表格每行末尾增加一个 mini SVG sparkline（复用 `admin-upstream-ops.html` 已有的 sparkline CSS），展示过去 24h 该 model+group 的总错误数趋势。数据源：`model_group_health_stats` 表按 bucket 聚合。
2. **错误率警告色**：表格中错误数超过阈值（如 > 100）的单元格用红色高亮，引起关注。
3. **时间维度切换**：在 Tab 顶部增加 period 选择器（24h / 7d），当前 period 参数已传到 API 但 UI 未提供切换控件。

```html
<!-- 建议在 anomalies tab 顶部增加 -->
<div class="flex gap-2 items-center mb-3">
  <select class="form-select form-select-sm" style="width:100px" onchange="loadAnomalies()">
    <option value="24h">24h</option><option value="7d">7d</option>
  </select>
</div>
```

### 7b.2 Key 运维台 — 评估后放弃 ✖️

> **结论**: 页面为只读监控非管理台，启用/禁用应走 key-pool 页面。heatmap 在 key 数量多时渲染负担大、信息密度低；详情弹窗已有 Chart.js 趋势图满足下钻需求。不做。

原提案内容（存档）：

当前 `/admin/monitor-upstream` 的 Key Monitor Tab 已经具备完整筛选体系（provider/status/health/sort），详情弹窗已有 Chart.js 延迟趋势和错误分布图。缺少的是 **列表行内的快速判断能力**。

**建议增强**：

1. **列表热度条**：在每行 key 后增加一个 mini heatmap bar（复用 status 页已有的 `.heatmap-bar` / `.heatmap-segment` CSS），展示该 key 24h 内每 5 分钟桶的请求量分布（绿=正常，黄=高负载，红=接近限流）。数据已存在于 `provider_health_stats_model`，需 API 扩展 `/api/admin/monitor-upstream/keys` 返回 per-key 的 bucket 数组。
2. **快速操作按钮**：列表行末增加操作列，包含「禁用/启用」toggle 按钮（调用已有 `POST /api/admin/key-pool/:id/toggle` API）和「跳转对账」链接（`/admin/reconciliation-ops?key_search=<key_preview>`）。
3. **验证状态实时图标**：已实现（`validation_status` 列），保持。

```html
<!-- 建议在 key 列表的每行 <tr> 末尾增加 -->
<td>
  <div class="heatmap-bar" style="width:80px">...</div>
</td>
<td class="flex gap-1">
  <button onclick="toggleKey('${k.id}')" class="btn btn-xs">禁用/启用</button>
  <a href="/admin/reconciliation-ops?key_search=${k.key_preview}" class="btn btn-xs btn-outline">对账</a>
</td>
```

### 7b.3 对账页 — UI 统一 ✅ 已完成

对账页 UI 已按 7b 方案完成对标 `/admin/monitor-upstream` 的升级：
- Period 下拉框（24h/7d/30d/90d/custom）
- Custom 日期使用 flatpickr range picker
- Provider 下拉从 `/api/admin/providers` 动态加载
- Model + Key 合并为一个模糊搜索框（后端 `search` 参数匹配 model + key_preview）
- 趋势图 X 轴标签间隔显示（避免密集重叠）
- 小时级粒度已确认暂不需要

---

## 8. 安全与隐私考虑

### 8.1 公开页必须隐藏的信息

- upstream key ID / key_preview
- provider base_url / api_key
- 用户数据、用户用量
- 成本、价格倍率、定价 ratio
- 详细的错误 message（可展示错误类型统计，不展示原始错误文本）

### 8.2 `/status` 与 `/api/status` 访问策略

- **Phase 2（前期）**: `/status` 页面和 `/api/status` 均使用 `RequireAuthWeb()` 鉴权，仅登录用户可见；按普通登录用户 rate limit 控制。
- **Phase 3（中期）**: 移除登录要求，变为公开页面；`/api/status` 按公开 API 加严格 Rate Limit（如 30 req/min / IP）+ CDN 缓存。
- 无论公开还是登录，都只返回聚合数据，不返回明细。
- 不暴露内部 provider ID 映射（如果 provider id 敏感，可配置显示名）。

### 8.3 角色权限与数据隔离

| 角色 | 可访问页面 | 可查看维度 | 可导出 |
|------|-----------|-----------|--------|
| super/system admin | `/admin/upstream-ops`, `/admin/monitor-upstream` | model/group/provider/key | ✅ |
| admin | `/admin/upstream-ops`, `/admin/monitor-upstream` | model/group/provider（可配置是否可见 key） | ✅（可配置） |
| enterprise 用户 | `/status` 或 `/admin/upstream-ops` 只读视图 | model / group | ❌ |
| 普通用户 | `/status`（中期公开，前期登录） | model / group | ❌ |

- 运营中心 API 必须在服务端校验角色：非 admin 请求携带 `provider`/`key` 参数时返回 403 或忽略。
- 导出接口必须校验 `RequireAdmin()`。
- 对账/成本相关数据默认仅对 super/system 角色开放，admin 角色可配置。

---

## 9. 参考代码位置

### Portal (`ai-portal-go`)

| 文件 | 当前作用 |
|------|---------|
| `web/templates/admin-upstream-usage.html` | 上游用量管理页 |
| `web/templates/admin-provider-health.html` | Provider 健康页 |
| `web/templates/admin-monitor-upstream.html` | Upstream Key 监控页 |
| `internal/routes/admin_usage.go` | upstream-usage / provider-health API |
| `internal/routes/channel_health.go` | channel_health 相关 API |
| `internal/routes/webhook_health.go` | `/api/webhook/provider-health-batch` 处理 |
| `internal/routes/webhook_usage.go` | `/api/webhook/upstream-usage-batch` 处理 |
| `internal/web/pages.go` | 页面路由注册 |
| `migrations/202605160000_provider_health_stats.sql` | provider_health_stats 表 |
| `migrations/202605160002_upstream_key_usage.sql` | upstream_key_usage 表 |

### Proxy (`ai-proxy-go`)

| 文件 | 当前作用 |
|------|---------|
| `pkg/billing/billing.go` | `ProviderHealthRecord`, `UpstreamUsageRecord`, 上报方法 |
| `internal/server/upstream_usage_buffer.go` | 上游用量缓冲与批量上报 |
| `internal/server/provider_health_buffer.go` | Provider 健康缓冲与批量上报 |

### RelayPulse 参考 (`ikuncode/relay-pulse`)

| 文件 | 参考价值 |
|------|---------|
| `frontend/src/components/StatusTable.tsx` | 矩阵表格、LayeredHeatmap、ChannelCell Tooltip、质量分 Sparkline |
| `frontend/src/components/LayeredHeatmapBlock.tsx` | 多模型分层热力图 |
| `frontend/src/components/HeatmapBlock.tsx` | 单层热力图块 |
| `frontend/src/components/Tooltip.tsx` | 全局时间桶详情 Tooltip |
| `frontend/src/components/ChannelTypeIcon.tsx` | 通道类型图标与提示 |
| `frontend/src/utils/color.ts` | availability/latency 颜色渐变 |
| `frontend/src/hooks/useMonitorData.ts` | 数据获取、自动刷新、轮询 |
| `frontend/src/types/index.ts` | StatusCounts、TimePoint、MonitorResult 等数据模型 |

---

## 10. 预期收益

1. **用户侧**: 提升透明度，用户（尤其是企业客户）可按 Model + User Group 自助查看服务质量，减少“服务是不是挂了”的咨询。
2. **运营侧**: Admin 可同时从 model/group 视角观察用户体验，并从 provider/key 视角快速定位根因，缩短 MTTR。
3. **对账侧**: 保留 provider / key 维度的小时级用量 + 错误细分，使与上游 provider 的账单核对更准确。
4. **品牌侧**: 公开状态页是 SaaS/平台型产品的标准配置，增强专业信任感。
5. **前端工程侧**: 沉淀一套可复用的可视化组件（热力图、sparkline、Tooltip、状态徽章），后续推广到用户控制台和 admin 仪表盘。

---

## 11. 结论

当前 Portal 已经具备了上游用量和健康统计的数据基础，但呈现方式仍偏向“admin 对账表格”。建议以 RelayPulse 为参考，重点补齐三方面：

1. **用户侧状态页** — 前期登录可见，以 **Model × User Group** 维度展示服务质量，不暴露具体 provider；中期再对外开放。
2. **Admin 上游运营中心** — 合并 `/admin/upstream-usage` 与 `/admin/provider-health`，admin 可按 provider 索引、下钻、导出；enterprise/普通用户仅可按 model/group 查看，不可导出。
3. **Key 运维台保留扩展** — `/admin/monitor-upstream` 继续作为单 key 运维入口，新增热度、利用率、错误分布、快速操作，用于迅速判断 key 是否失效。
4. **更细的数据模型与可视化** — health 到 model+group 维度、错误分类扩展、availability 计算；并通过可复用的热力图、sparkline、Tooltip、状态徽章组件，逐步推广到用户和 admin 仪表盘。

建议按 Phase 1 → Phase 2 → Phase 3 的顺序推进：先夯实数据，再上线登录版状态页，最后升级 admin 运营中心、扩展 Key 运维台并公开状态页。

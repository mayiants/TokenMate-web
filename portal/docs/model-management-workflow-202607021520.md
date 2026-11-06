# Model 管理工作流：展示、验证与激活

> 日期：2026-07-02  
> 状态：已实现（UI 待优化）  
> 关联页面：`admin/models`（General）、`admin/models-aws`（AWS）

## 核心概念：双 Status 设计

`service_models` 表有两个独立的状态字段：

| 字段 | 含义 | 控制者 | 可选值 |
|------|------|--------|--------|
| `status` | 业务状态（是否对外提供服务） | Admin 手动操作 | pending, active, paused, ignored, eol_pending, eol, discontinued |
| `validation_status` | 技术可用性（模型是否真的能调通） | Batch Validate 自动写入 | pending, valid, invalid, needs_review |

**设计原则：Batch Validate 永远不改变 `status`。** Admin 的业务决策优先。

## Status 状态流转

```
                    ┌─────────────────────────────────┐
                    │         service_models          │
                    ├─────────────┬───────────────────┤
                    │   status    │ validation_status │
                    ├─────────────┼───────────────────┤
 导入模型 ────────► │   pending   │     pending       │
                    │             │                   │
 Batch Validate ──► │   pending   │  valid/invalid    │  (只改右列)
                    │             │                   │
 Admin Confirm ───► │   active    │  valid            │  (只改左列)
                    │             │                   │
 Re-validate ─────► │   active    │  invalid ⚠️       │  (右列变了,左列不动)
                    │             │                   │
 Admin Pause ─────► │   paused    │  invalid          │  (admin 人工决定下线)
                    └─────────────┴───────────────────┘
```

## 组合状态含义

| status | validation_status | 含义 | Admin 应做什么 |
|--------|-------------------|------|---------------|
| pending | pending | 刚导入，未测试 | 点 Batch Validate |
| pending | valid | 确认可用 | 设价格 → Confirm 激活 |
| pending | invalid | 确认不可用 | Ignore 或等待 |
| pending | needs_review | 可用但建议换名 | 检查 suggested_api_name |
| active | valid | 正常服务中 | 无需操作 |
| active | invalid | ⚠️ 服务中但检测异常 | 确认是临时故障还是永久下线 |
| active | pending | 从未 validate 过 | 建议 validate 确认 |
| paused | * | Admin 主动暂停 | 等待恢复或下线 |
| ignored | * | Admin 明确不用 | 无需关注 |

## Admin 工作流

### 新 Provider/Key 导入模型

```
1. admin/keypool → 添加 upstream key → Validate Key
   └── key 有效 → models list 存入 upstream_keys.models

2. admin/models-aws → 选择 provider → Fetch Model List
   └── 模型导入到 service_models (status=pending)

3. admin/models-aws 或 admin/models → 点击 "Batch Validate"
   └── 系统对每个模型发真实请求 (max_tokens=1)
   └── 结果: validation_status = valid / invalid / needs_review

4. Admin 对 valid 的模型:
   └── 编辑价格 (input_price, output_price)
   └── Confirm → status = active

5. Admin 对 invalid 的模型:
   └── Ignore (status=ignored) 或保持 pending 等待
```

### 定期健康检查

```
1. admin/models → 筛选 status=active → 点击 "Batch Validate"
2. 查看结果: 如果有 active + invalid 的模型 → 调查原因
3. 决策: 暂停 (paused) / 等待恢复 / 切换 provider
```

## 计费定价来源

| 优先级 | 来源 | 说明 |
|--------|------|------|
| 1（最高） | `service_models.input_price / output_price` | Admin 在 UI 填入的价格 |
| 2（fallback） | Proxy `DefaultPricing()` 硬编码 | 按模型名模糊匹配的默认值 |

Portal 通过 `/api/admin/pricing/sync` 将 `service_models` 的价格同步给 Proxy。如果模型没有配价格（NULL），Proxy 使用硬编码 fallback。

**建议**：所有 active 模型都应有明确定价，避免依赖 fallback。

## 页面功能对照

### admin/models（General）

| 功能 | 状态 |
|------|------|
| Model Catalog 表格（所有 provider） | ✅ 已有 |
| 按 status 筛选 | ✅ 已有 |
| 按 provider 筛选 | ✅ 已有 |
| 新增模型 | ✅ 已有 |
| 编辑定价 | ✅ 已有 |
| Confirm / Pause / Ignore 操作 | ✅ 已有 |
| **Batch Validate 按钮** | ✅ 已加 |
| 显示 validation_status 列 | ❌ **待加** |
| Provider Models Fetch (非 AWS) | ✅ 已有 |
| OpenRouter 模型参考 | ✅ 已有 |
| Model Routing Config | ✅ 已有 |

### admin/models-aws（AWS）

| 功能 | 状态 |
|------|------|
| AWS Model Catalog 表格 | ✅ 已有 |
| 按 status 筛选 | ✅ 已有 |
| 按 Amazon provider 筛选 | ✅ 已有 |
| Fetch Model List (Runtime + Mantle) | ✅ 已有 |
| Add to Service List | ✅ 已有 |
| Mandatory Sync to Upstream Keys | ✅ 已有 |
| **Batch Validate 按钮** | ✅ 已加 |
| 显示 validation_status 列 | ❌ **待加** |
| 编辑定价 | ✅ 已有 |
| Confirm / Pause 操作 | ✅ 已有 |

## 待优化事项 (UI)

### 1. 表格显示 validation_status 列

**优先级：高**

两个页面的 Model Catalog 表格需要增加 `validation_status` 列，显示为 badge：
- `valid` → 绿色 badge ✓
- `invalid` → 红色 badge ✗
- `needs_review` → 黄色 badge ⚠
- `pending` → 灰色 badge（未检测）

**改动点：**
1. `listServiceModels` API 需返回 `validation_status` 和 `validated_at` 字段
2. 前端表格增加一列渲染 badge
3. 对 `active + invalid` 的行高亮警告

### 2. 筛选支持 validation_status

**优先级：中**

增加按 validation_status 筛选的下拉框，方便 admin 快速找到：
- 所有 invalid 的模型（可能需要处理）
- 所有 pending 的模型（从未 validate 过）

### 3. 单模型 Validate 按钮

**优先级：低**

在每行的 Actions 中增加单独的 "Test" 按钮，方便 admin 测试单个模型而非批量。

### 4. 定时自动 Validate

**优先级：低**

类似 keypool 的 24 小时自动验证，service_models 也可以设置定期（如每周）自动 validate active 模型，发现异常时邮件通知 admin。

## 相关数据库字段

```sql
-- service_models 表验证相关字段
validation_status      VARCHAR(20) DEFAULT 'pending'  -- valid/invalid/needs_review/pending
validation_result      JSONB                          -- 详细验证结果 JSON
validated_at           TIMESTAMP                      -- 上次验证时间
suggested_api_name     VARCHAR(100)                   -- 验证时发现的更好名称
avg_latency_ms         INTEGER                        -- 验证时测得的延迟
```

## API 端点

| 端点 | 方法 | 用途 |
|------|------|------|
| `/api/admin/service-models/batch-validate` | POST | 批量验证模型可用性 |
| `/api/admin/service-models/sync-bedrock` | GET/POST | 从 AWS 获取模型列表导入 |
| `/api/admin/keypool/:keyId/validate` | POST | 验证单个 upstream key |
| `/api/admin/keypool/validate-batch` | POST | 批量验证 upstream keys |

### batch-validate 请求格式

```json
// 按 ID 验证特定模型
{"model_ids": ["uuid1", "uuid2"]}

// 按 provider 验证
{"provider": "amazon-mantle-us-east-1"}

// 按状态验证
{"status": "pending"}

// 组合
{"provider": "amazon-us-east-1", "status": "active"}
```

### batch-validate 响应格式

```json
{
  "success": true,
  "validated": 51,
  "valid": 35,
  "invalid": 16
}
```

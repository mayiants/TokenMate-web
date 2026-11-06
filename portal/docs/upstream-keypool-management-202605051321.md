# Upstream Key Pool 管理

> **✅ IMPLEMENTED** - 功能已完成，持续维护中

## 1. 概述

上行 Key Pool 用于管理多个 AI 服务提供商的 API Key，支持加密存储、批量导入导出、能力管理等功能。

**设计原则：**
- 本表仅存储 Key 的**静态属性**（上游给的限制、能力范围等）
- 动态数据（使用量、rpm 状态等）由其他模块维护，Proxy 定期上报

## 2. 数据库设计

### 2.1 表结构

```sql
CREATE TABLE upstream_keys (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    provider        VARCHAR(50) NOT NULL,
    key_encrypted   TEXT NOT NULL,           -- 加密存储的完整 Key
    key_preview     VARCHAR(20) NOT NULL,     -- 显示用的预览: "sk-xxxx...yyyy"
    base_url        TEXT,                     -- 自定义 Base URL
    models          JSONB,                    -- 可用的模型列表
    enabled         BOOLEAN DEFAULT true,     -- 启用状态

    -- 上游限制（静态属性）
    rpm_limit       INT,                      -- 上游限制的每分钟请求数
    tpm_limit       INT,                      -- 上游限制的每分钟 token 数
    quota_limit     DECIMAL(10,2),            -- 上游限制的配额（美元）

    -- 分配与验证
    assignment_mode VARCHAR(20) DEFAULT 'shared',  -- 分配模式: shared, exclusive
    assigned_proxy  VARCHAR(50),              -- 指定分配的 Proxy（exclusive 模式）
    validation_status VARCHAR(20) DEFAULT 'pending',  -- pending, valid, invalid
    validation_at   TIMESTAMP,                -- 最后验证时间

    created_at      TIMESTAMP DEFAULT now(),
    updated_at      TIMESTAMP DEFAULT now()
);

-- 唯一索引：防止同一供应商下重复导入相同 Key
CREATE UNIQUE INDEX idx_upstream_keys_provider_key
    ON upstream_keys (provider, key_encrypted);

-- 查询索引
CREATE INDEX idx_upstream_keys_provider ON upstream_keys (provider);
CREATE INDEX idx_upstream_keys_enabled ON upstream_keys (enabled);
```

### 2.2 字段说明

| 字段 | 类型 | 说明 |
|------|------|------|
| `models` | JSONB | 该 Key 可访问的模型列表，如 `["gpt-4", "gpt-3.5-turbo"]`。为空表示该供应商所有模型 |
| `rpm_limit` | INT | 上游对该 Key 的 RPM 限制，如 `60`、`500` |
| `tpm_limit` | INT | 上游对该 Key 的 TPM 限制 |
| `quota_limit` | DECIMAL | 上游给该 Key 的配额上限（美元） |

### 2.3 Key 加密存储

- 使用 AES-256-GCM 加密
- 加密密钥通过环境变量 `ENCRYPTION_KEY` 配置
- 若未配置加密密钥，Key 以明文存储（不推荐）

### 2.4 Key 预览格式

```
原始 Key: sk-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
预览格式: sk-proj-...345pqr678  (前8位 + "..." + 后4位)
```

适用于各种长度（64位、128位 hash 等），预览字段限制为 20 字符。

## 3. Key 能力管理

### 3.1 能力自动更新

**问题**：上游模型升级（如 kimi-2.5 → kimi-2.6），Key 能力自动扩展，但数据库 `models` 字段未更新，导致 Proxy 拒绝新模型请求。

**解决方案**：

验证 Key 时，如果上游支持获取模型列表（如 OpenAI 的 `GET /models`），则自动更新数据库：

```go
func validateKey(key) {
    // 1. 验证 Key 有效性
    status := callProviderAPI(key)

    // 2. 尝试获取模型列表
    models := fetchModelsFromProvider(key)
    if models != nil {
        // 3. 更新数据库的 models 字段
        db.Exec("UPDATE upstream_keys SET models = $1, validation_status = 'valid' WHERE id = $2",
            models, key.ID)
    } else {
        // 不支持获取模型列表的供应商，保持 models 不变
        db.Exec("UPDATE upstream_keys SET validation_status = 'valid' WHERE id = $2", key.ID)
    }
}
```

### 3.2 手动批量更新能力

**场景**：部分供应商不支持 API 获取模型列表，需要 Admin 手动维护。

**接口设计**：

```
POST /api/admin/keypool/batch-update-models
{
    "provider": "anthropic",      // 必填：供应商
    "created_after": "2024-01-01", // 可选：创建时间筛选
    "existing_models": ["claude-3-opus", "claude-3-sonnet"],  // 可选：只更新包含这些模型的 Key
    "add_models": ["claude-3.5-sonnet", "claude-3.5-opus"],   // 必填：新增的模型
    "replace": false              // 可选：true=替换，false=追加
}
```

**响应**：
```json
{
    "updated": 150,
    "message": "已为 150 个 Key 添加模型能力"
}
```

### 3.3 模型能力查询

```
GET /api/admin/keypool/models-summary?provider=openai
```

返回该供应商下所有 Key 的模型能力汇总，方便 Admin 查看哪些模型已有 Key 支持。

## 4. 批量导入导出

### 4.1 导入格式

**格式1：单供应商批量导入（高效）**

适用于同一供应商批量添加多个 Key，共享相同属性。

```
供应商: openai
Base URL: https://api.openai.com/v1 (可选)
RPM 限制: 60 (可选)
TPM 限制: 100000 (可选)

Keys (每行一个):
sk-proj-abc123def456ghi789
sk-proj-xyz987wvu654tsr321
sk-proj-mno111pqr222stu333
```

**格式2：混合供应商导入（迁移）**

适用于从其他系统迁移，每行包含完整信息：

```csv
provider,key,base_url,rpm_limit,tpm_limit,enabled
openai,sk-proj-abc123def456ghi789,https://api.openai.com/v1,60,100000,true
anthropic,sk-ant-api03-xxx111yyy222,https://api.anthropic.com/v1,100,,true
deepseek,sk-ds-aaa333bbb444,https://api.deepseek.com/v1,,,false
```

### 4.2 导出格式

导出为 CSV 文件，格式同"格式2"，包含解密后的原始 Key：

```csv
provider,key,base_url,rpm_limit,tpm_limit,enabled
openai,sk-proj-abc123def456ghi789,https://api.openai.com/v1,60,100000,true
anthropic,sk-ant-api03-xxx111yyy222,https://api.anthropic.com/v1,100,,true
```

### 4.3 导入限制与容错

| 方式 | 最大数量 | 说明 |
|------|----------|------|
| 文本框粘贴 | 100 条 | 超过限制提示使用文件上传 |
| 文件上传 | 无限制 | 支持 .txt / .csv 文件 |

**去重策略：**

- 数据库唯一索引 `(provider, key_encrypted)` 保证不重复
- 导入使用 UPSERT：存在则跳过，不存在则插入
- 返回结果：新增数量、跳过数量（已存在）

**示例响应：**
```json
{
    "added": 850,
    "skipped": 150,
    "message": "导入完成，150 个 Key 已存在被跳过"
}
```

### 4.4 模板下载

提供标准模板文件供客户下载：
- `keypool_import_template_single.csv` - 单供应商模板
- `keypool_import_template_mixed.csv` - 混合供应商模板

## 5. 多 Proxy 部署架构

### 5.1 问题场景

```
┌─────────────┐
│   Portal    │ ← PostgreSQL (Key Pool)
│  (管理端)    │
└─────────────┘
      │
      ↓ Key Pool 同步
┌─────┴─────┬─────────────┬─────────────┐
│  Proxy A  │  Proxy B    │  Proxy C    │
│ (区域1)   │  (区域2)    │  (区域3)    │
└───────────┴─────────────┴─────────────┘
```

### 5.2 Key 分配策略

| 模式 | 说明 | 适用场景 |
|------|------|----------|
| `shared` | Key 共享给所有 Proxy（默认） | 高可用、负载均衡 |
| `exclusive` | Key 只分配给指定 Proxy | 区域隔离、合规要求 |

**数据库字段：**
- `assignment_mode`: 分配模式
- `assigned_proxy`: 指定 Proxy ID（exclusive 模式）

### 5.3 同步机制

**推荐方案：Proxy 拉取 + Redis 缓存**

1. Portal 将 Key Pool 写入 PostgreSQL + Redis
2. Proxy 启动时从 Redis 拉取（快速）
3. 定时从 PostgreSQL 拉取（兜底）
4. Key 更新时 Portal 发布 Redis 事件通知 Proxy 刷新

## 6. Key 验证机制

### 6.1 验证流程

```
导入 → 存入数据库 → 返回结果 → 后台异步验证 → 更新状态 + 更新 models
```

- 导入立即返回，不阻塞
- 后台任务逐个验证 Key
- 验证时尝试获取模型列表并更新数据库

### 6.2 验证 API

**单 Key 验证：**
```
POST /api/admin/keypool/:id/validate
```

**批量验证：**
```
POST /api/admin/keypool/validate-batch
{
    "provider": "openai",  // 可选
    "limit": 100           // 可选
}
```

### 6.3 验证逻辑

```go
func validateKey(provider, key, baseUrl string) {
    // 1. 获取供应商默认 Base URL
    if baseUrl == "" {
        baseUrl = getDefaultBaseUrl(provider)
    }

    // 2. 发送最小化验证请求
    //    - OpenAI: GET /models
    //    - Anthropic: POST /messages (最小请求)
    //    - 其他: 类似最小化请求

    // 3. 判断响应
    //    - 200: valid，同时更新 models
    //    - 401: invalid
    //    - 429: rate_limited
}
```

### 6.4 验证调度

- **定时验证**：每日定时验证所有 Key
- **触发验证**：导入完成后触发批量验证
- **手动触发**：管理员手动触发

## 7. 前端功能

### 7.1 Key Pool 页面

- 列表展示：供应商、预览、状态、RPM/TPM 限制、验证状态
- 操作：添加、编辑、删除、启用/禁用、验证
- 批量操作：批量导入、批量导出、批量验证、批量更新模型能力

### 7.2 导入弹窗

- 选择导入方式：单供应商 / 混合供应商
- 文本框输入（限制 100 行）
- 文件上传（无限制）
- 模板下载链接

### 7.3 批量更新模型能力弹窗

- 选择供应商
- 筛选条件：创建时间、现有模型
- 操作：追加模型 / 替换模型

## 8. API 接口

### 8.1 Key 管理

| 接口 | 方法 | 说明 |
|------|------|------|
| `/api/admin/keypool` | GET | 获取 Key 列表 |
| `/api/admin/keypool` | POST | 添加单个 Key |
| `/api/admin/keypool/:id` | PATCH | 更新 Key 属性 |
| `/api/admin/keypool/:id` | DELETE | 删除 Key |
| `/api/admin/keypool/:id/validate` | POST | 验证单个 Key |

### 8.2 批量操作

| 接口 | 方法 | 说明 |
|------|------|------|
| `/api/admin/keypool/import` | POST | 批量导入 |
| `/api/admin/keypool/export` | GET | 批量导出 |
| `/api/admin/keypool/validate-batch` | POST | 批量验证 |
| `/api/admin/keypool/batch-update-models` | POST | 批量更新模型能力 |
| `/api/admin/keypool/template` | GET | 下载模板 |
| `/api/admin/keypool/models-summary` | GET | 模型能力汇总 |

## 9. 实现状态

- [x] 数据库唯一索引 (`ON CONFLICT` + `idx_upstream_keys_provider_key`)
- [x] UPSERT 导入逻辑 (batch import with `ON CONFLICT DO NOTHING`)
- [x] 前端行数限制（导入弹窗已支持）
- [x] 模板下载 (`/api/admin/keypool/template`)
- [x] Key 验证机制（含 models 自动更新）- async validate + models auto-populate
- [x] 批量更新模型能力接口 (`/api/admin/keypool/batch-update-models`)
- [x] 多 Proxy 同步机制 (Portal sync endpoint + Proxy periodic pull)
- [x] Proxy 拉取 Key API (`/api/admin/key-pool/sync`)
- [x] 供应商默认 Base URL 配置 (`providerDefaultURLs` map)
- [x] Provider 管理界面 (Admin UI: CRUD + enable/disable)
- [x] sync-bedrock 预览模态框 (Admin Models page)

---

## 附录：供应商默认 Base URL

| Provider | Base URL | 支持获取模型列表 |
|----------|----------|------------------|
| openai | https://api.openai.com/v1 | ✅ GET /models |
| anthropic | https://api.anthropic.com/v1 | ❌ |
| google | https://generativelanguage.googleapis.com/v1beta | ❌ |
| deepseek | https://api.deepseek.com/v1 | ✅ GET /models |
| moonshot | https://api.moonshot.cn/v1 | ✅ GET /models |
| zhipu | https://open.bigmodel.cn/api/paas/v4 | ❌ |
| alibaba | https://dashscope.aliyuncs.com/compatible-mode/v1 | ❌ |
| baidu | https://aip.baidubce.com/rpc/2.0/ai_custom/v1/wenxinworkshop | ❌ |
| minimax | https://api.minimax.chat/v1 | ❌ |
| xai | https://api.x.ai/v1 | ✅ GET /models |
| mistral | https://api.mistral.ai/v1 | ✅ GET /models |
| cohere | https://api.cohere.ai/v1 | ❌ |
| groq | https://api.groq.com/openai/v1 | ✅ GET /models |
| perplexity | https://api.perplexity.ai | ❌ |
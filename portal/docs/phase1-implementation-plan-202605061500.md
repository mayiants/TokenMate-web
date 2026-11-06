# Phase 1: User + Group 数据模型实施计划

**Date:** 2026-05-06 15:00 UTC  
**Status:** HISTORICAL — 所有功能已完成并随 Stage 5/6 上线  
**Scope:** Portal (`ai-portal-go`) + Proxy (`ai-proxy-go`) — key-level group support  
**Migration:** `202605061500_user_group_phase1.sql`

---

## 1. 设计原则

1. **Group belongs to Key** — Portal 按 key 级别划分优先级，不按 user 级别
2. **Key prefix 决定 group_type** — `sk-wgit-` / `sk-wgit-tp-` / `sk-wgit-en-`
3. **静态快照** — key 创建时从 group 继承配置，group 变更不影响已有 key
4. **wgit-maas-latest 别名** — Phase 1 中直接映射为 `current_model`（Phase 2 再做 round-robin）
5. **Backward compatible** — 现有 API / frontend 接口不变

---

## 2. 数据库 Schema 变更

### 2.1 `user_groups` 表扩展

```sql
ALTER TABLE user_groups
  ADD COLUMN group_type    VARCHAR(20) NOT NULL DEFAULT 'general'
    CHECK (group_type IN ('economy', 'general', 'subscription', 'enterprise')),
  ADD COLUMN billing_mode  VARCHAR(20) DEFAULT 'token'
    CHECK (billing_mode IN ('token', 'request')),
  ADD COLUMN pricing_ratio NUMERIC(5,4) DEFAULT 1.0000,
  ADD COLUMN models        JSONB DEFAULT '[]',
  -- Rate limits (subscription/enterprise tier-specific)
  ADD COLUMN rpm     INT DEFAULT 0,
  ADD COLUMN rph     INT DEFAULT 0,
  ADD COLUMN rp5h    INT DEFAULT 0,
  ADD COLUMN rpw     INT DEFAULT 0,
  ADD COLUMN rpmonth INT DEFAULT 0,
  ADD COLUMN tpm     INT DEFAULT 0,
  ADD COLUMN tph     INT DEFAULT 0,
  ADD COLUMN tpd     INT DEFAULT 0,
  ADD COLUMN tpw     INT DEFAULT 0,
  ADD COLUMN tpmonth INT DEFAULT 0;
```

### 2.2 `api_keys` 表扩展

```sql
ALTER TABLE api_keys
  ADD COLUMN group_id        UUID REFERENCES user_groups(id),
  ADD COLUMN group_priority  JSONB DEFAULT '[]',
  ADD COLUMN current_model   VARCHAR(100),
  ADD COLUMN billing_mode    VARCHAR(20) DEFAULT 'token'
    CHECK (billing_mode IN ('token', 'request')),
  ADD COLUMN key_type        VARCHAR(20) DEFAULT 'general'
    CHECK (key_type IN ('economy', 'general', 'subscription', 'enterprise'));

CREATE INDEX idx_api_keys_group_id ON api_keys(group_id);
```

### 2.3 默认值与数据迁移

| 步骤 | 操作 |
|------|------|
| 旧 `groups` → `user_groups` | 迁移旧数据为 `group_type='general'` |
| 默认 general group | 插入 `default-general` 组（如不存在） |
| 用户补默认 group | `UPDATE users SET group_id = default_group_id WHERE group_id IS NULL` |
| 现有 key 补默认值 | `UPDATE api_keys SET key_type='general', billing_mode='token'` |
| 现有 key 关联 group | `UPDATE api_keys k SET group_id = u.group_id FROM users u WHERE k.user_id = u.id` |

---

## 3. Portal 后端变更清单

### 3.1 `internal/routes/apikeys.go`

| 方法 | 变更内容 |
|------|----------|
| `createKey` | 1. 接收 `key_type` 参数（economy/general/subscription/enterprise）<br>2. 根据 `key_type` 生成对应前缀：`sk-wgit-` / `sk-wgit-tp-` / `sk-wgit-en-`<br>3. 从 `users.group_id` 读取默认 group，写入 `api_keys.group_id`<br>4. 从 `user_groups` 读取 `models`, `pricing_ratio`, `rate_limit_rpm/tpm`，作为 key 默认值<br>5. 支持 `current_model` 参数（用户 dashboard 选择的模型） |
| `listKeys` | 返回新增字段：`group_id`, `key_type`, `current_model`, `billing_mode` |
| `getKey` | 同上 |
| `updateKey` | 支持更新 `current_model`, `group_priority` |
| `validateKey` | 返回新增字段：`group_id`, `key_type`, `current_model`, `billing_mode`, `pricing_ratio`, `group_priority`, `models` |

### 3.2 `internal/routes/auth.go`

| 方法 | 变更内容 |
|------|----------|
| `register` | 注册时自动分配默认 general group（`users.group_id = default_group_id`），`user_type = 'general'` |

### 3.3 `internal/routes/groups.go`

| 方法 | 变更内容 |
|------|----------|
| `listGroups` | 修复：从旧 `groups` 表切换到 `user_groups` 表 |
| `createGroup` | 支持 `group_type`, `billing_mode`, `pricing_ratio`, `models`, `rpm/rph/...` 参数 |
| `updateGroup` | 同上 |

### 3.4 `internal/routes/sync.go`

| 方法 | 变更内容 |
|------|----------|
| `syncKeyPool` | 无需变更（只 sync upstream keys，不涉及 user keys） |
| `validateKey` | 返回新增 group 相关字段给 Proxy |

---

## 4. Proxy 后端变更清单

### 4.1 `pkg/auth/claims.go`（或等效文件）

扩展 Claims 结构体：

```go
type Claims struct {
    // existing fields
    UserID     string `json:"user_id"`
    KeyID      string `json:"api_key_id"`
    Role       string `json:"role"`
    Balance    float64 `json:"balance"`
    
    // new group fields
    GroupID        string    `json:"group_id"`
    KeyType        string    `json:"key_type"`        // economy/general/subscription/enterprise
    CurrentModel   string    `json:"current_model"`
    BillingMode    string    `json:"billing_mode"`    // token | request
    PricingRatio   float64   `json:"pricing_ratio"`
    GroupPriority  []string  `json:"group_priority"`
    Models         []string  `json:"models"`          // allowed models from group
}
```

### 4.2 `pkg/relay/interface.go`（或路由层）

| 功能 | 变更内容 |
|------|----------|
| `resolveModel` | 新增：Phase 1 中 `wgit-maas-latest` → `claims.CurrentModel` 直接替换 |
| Model 验证 | 新增：如果请求使用真实模型名（非 `wgit-maas-latest`），验证是否与 `claims.CurrentModel` 一致，不一致返回 400 |

### 4.3 上游 key 选择

Phase 1 暂不改动上游 key 选择逻辑。上游 key pool 的选择基于 provider → model，与 key 级别 group 解耦。

---

## 5. Key 前缀规则

| 前缀 | key_type | group_type | 说明 |
|------|----------|-----------|------|
| `sk-wgit-{uuid}` | general | general | 标准计费 |
| `sk-wgit-{uuid}` | economy | economy | 85折计费 |
| `sk-wgit-tp-{uuid}` | subscription | subscription | 套餐（token/code plan） |
| `sk-wgit-en-{uuid}` | enterprise | enterprise | 企业客户 |

---

## 6. 两种 API 使用模式（Phase 1）

### 模式 A：wgit-maas-latest（通用名）

```
POST /v1/chat/completions
Authorization: Bearer sk-wgit-xxx
Body: { "model": "wgit-maas-latest", "messages": [...] }
```

Proxy 处理：`wgit-maas-latest` → `claims.CurrentModel` → 路由到对应 provider

### 模式 B：真实模型名

```
POST /v1/chat/completions
Authorization: Bearer sk-wgit-xxx
Body: { "model": "glm5", "messages": [...] }
```

Proxy 处理：验证 `model == claims.CurrentModel`，一致则路由，不一致返回 400

**Phase 1 结论**：两种模式最终行为一致（都路由到 `current_model`），只是入口参数不同。

---

## 7. 验证计划

### 7.1 数据库验证

```sql
-- 1. user_groups 有 group_type / pricing_ratio / models
SELECT name, group_type, pricing_ratio, models FROM user_groups LIMIT 3;

-- 2. api_keys 有 group_id / key_type / current_model
SELECT key_prefix, key_type, billing_mode, current_model FROM api_keys LIMIT 3;

-- 3. users 都有 group_id
SELECT COUNT(*) FROM users WHERE group_id IS NULL; -- => 0

-- 4. 旧 groups 数据已迁移
SELECT COUNT(*) FROM user_groups WHERE group_type = 'general'; -- >= 1
```

### 7.2 API 验证

| 测试 | 步骤 | 期望结果 |
|------|------|----------|
| 注册新用户 | `POST /api/auth/register` | 用户 `group_id` 不为 NULL |
| 创建 economy key | `POST /api/keys` + `{"key_type":"economy"}` | 返回 `sk-wgit-{uuid}` 前缀 key |
| 创建 subscription key | `POST /api/keys` + `{"key_type":"subscription"}` | 返回 `sk-wgit-tp-{uuid}` 前缀 key |
| 验证 key | `GET /api/keys/validate?key=sk-wgit-tp-xxx` | 返回 `key_type=subscription`, `group_id`, `pricing_ratio` 等 |
| list groups | `GET /api/admin/groups` | 返回 user_groups 数据（非旧 groups） |
| wgit-maas-latest | Proxy 请求 `model=wgit-maas-latest` | 路由到 `current_model` |
| 真实模型不匹配 | Proxy 请求 `model=glm5` 但 `current_model=kimi-k2.5` | 返回 400 |

---

## 8. 部署顺序

```
1. 备份数据库
   pg_dump -U postgres ai_proxy_portal > backup_$(date +%Y%m%d_%H%M).sql

2. 执行迁移
   psql -U postgres -d ai_proxy_portal -f migrations/202605061500_user_group_phase1.sql

3. 验证迁移结果（见 §7.1）

4. 部署 Portal（编译 + 重启）
   go build -o portal ./cmd/server
   # 或 docker compose up -d portal --build

5. 验证 Portal API（见 §7.2）

6. 部署 Proxy（编译 + 重启）
   go build -o proxy ./cmd/server
   # 或 docker compose up -d proxy --build

7. 端到端测试（Portal → Proxy → upstream）
```

---

## 9. 回滚计划

```sql
-- 如果需要回滚，删除新增列（但无法恢复数据）
ALTER TABLE api_keys DROP COLUMN IF EXISTS group_id;
ALTER TABLE api_keys DROP COLUMN IF EXISTS group_priority;
ALTER TABLE api_keys DROP COLUMN IF EXISTS current_model;
ALTER TABLE api_keys DROP COLUMN IF EXISTS billing_mode;
ALTER TABLE api_keys DROP COLUMN IF EXISTS key_type;

ALTER TABLE user_groups DROP COLUMN IF EXISTS group_type;
ALTER TABLE user_groups DROP COLUMN IF EXISTS billing_mode;
ALTER TABLE user_groups DROP COLUMN IF EXISTS pricing_ratio;
ALTER TABLE user_groups DROP COLUMN IF EXISTS models;
ALTER TABLE user_groups DROP COLUMN IF EXISTS rpm;
ALTER TABLE user_groups DROP COLUMN IF EXISTS rph;
ALTER TABLE user_groups DROP COLUMN IF EXISTS rp5h;
ALTER TABLE user_groups DROP COLUMN IF EXISTS rpw;
ALTER TABLE user_groups DROP COLUMN IF EXISTS rpmonth;
ALTER TABLE user_groups DROP COLUMN IF EXISTS tpm;
ALTER TABLE user_groups DROP COLUMN IF EXISTS tph;
ALTER TABLE user_groups DROP COLUMN IF EXISTS tpd;
ALTER TABLE user_groups DROP COLUMN IF EXISTS tpw;
ALTER TABLE user_groups DROP COLUMN IF EXISTS tpmonth;
```

**注意**：回滚前务必备份 `api_keys` 和 `user_groups` 表。

---

## 10. 后续工作（Phase 2 / Phase 3）

| Phase | 内容 | 预计工作量 |
|-------|------|----------|
| Phase 2 | Dashboard 页面（key 管理、模型选择器、group 优先级配置） | 4-6h |
| Phase 2 | Admin group 管理页面 | 2-3h |
| Phase 3 | Proxy `wgit-maas-latest` round-robin / balance 路由 | 2-3h |
| Phase 3 | Proxy 按 request 计费（code plan） | 1-2h |
| Phase 3 | 上游 key pool 实时状态上报 Dashboard | 2-3h |
| Phase 3 | 上游 usage 累积（provider bill reconciliation） | 3-4h |

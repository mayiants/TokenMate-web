# Management Token 特性方案（企业可编程管理凭据）

> **Status:** ACTIVE (P1+P2+P3 implemented 2026-09-11 — enterprise_admin issues tokens; scopes read:usage, read:pricing, read:members, manage:keys, manage:members; default 365d; see `internal/routes/management_tokens*.go`, migration `0013`. Follow-up: add-member-by-invite via token, promote/demote, and personal (non-enterprise) tokens.)
> 生成时间：2026-09-11 15:30 UTC
> 作者：Kimi Code
> 关联：`apikeys_*`（现有 sk- 推理 key）、`enterprise_*`（企业管理端点）、`apikeys_pricing.go`（Q1 key-authed 价目查询）

## 1. 背景与动机

当前平台只有两类凭据：

| 凭据 | 用途 | 生命周期 | 鉴权 |
| --- | --- | --- | --- |
| JWT session | Portal UI 登录态 | 短期（小时级，带 token_version 撤销） | 登录签发 |
| `sk-wgit-*` API key | **推理调用**（/v1/chat 等） | 长期 | key hash |

缺口：**没有一种长期、可编程、带最小权限 scope 的"管理凭据"**，让企业客户脚本化地做账号管理类操作——查用量/对账、拉价目、批量建/禁用/查询下游 key、管理企业成员等。企业客户目前只能：
- 在 Portal UI 手工点（不可自动化）；或
- hack 一个 JWT（会过期、scope 过大、不适合 CI/后台服务）。

对标 laozhang：其"控制台 token"（区别于 `sk-` API key）正是用于编程管理账号。研发/平台型企业客户对这个能力有真实需求（CI 里做成本归因、程序化管理 key、对接内部计费系统）。

## 2. 目标与非目标

**目标**
- 引入 **Management Token**（简称 mgmt token），长期有效、可吊销、带最小权限 scope。
- 用于**管理面**操作：读用量/对账、读价目、管理下游 key、（可选）管理企业成员。
- 与今日上线的"受控自助企业 key"配套：admin 用 mgmt token 脚本化维护边界，成员在边界内自助建 key。

**非目标**
- 不替代 `sk-` 推理 key（mgmt token 不能调 `/v1/chat` 等推理端点）。
- 不替代 JWT UI 登录。
- 首期不做 OAuth / 第三方授权。

## 3. 数据模型

新增表 `management_tokens`（迁移文件 + `internal/db/schema.go` 双写）：

```sql
CREATE TABLE IF NOT EXISTS management_tokens (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    token_hash     VARCHAR(255) NOT NULL UNIQUE,      -- SHA-256(token)，绝不存明文
    token_prefix   VARCHAR(20)  NOT NULL,             -- 展示用前缀 mgmt-xxxx...
    name           VARCHAR(255) NOT NULL,
    user_id        UUID NOT NULL REFERENCES users(id),-- 所属人（签发者）
    enterprise_id  UUID REFERENCES enterprises(id),   -- 绑定企业（NULL=个人）
    scopes         JSONB NOT NULL DEFAULT '[]',        -- 权限 scope 列表
    status         VARCHAR(20) DEFAULT 'active'
                   CHECK (status IN ('active','disabled','revoked')),
    last_used_at   TIMESTAMP,
    expires_at     TIMESTAMP,                          -- NULL=长期；建议默认 365 天
    created_at     TIMESTAMP DEFAULT NOW(),
    updated_at     TIMESTAMP DEFAULT NOW(),
    deleted_at     TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_mgmt_tokens_hash ON management_tokens(token_hash) WHERE deleted_at IS NULL;
```

Token 格式：`mgmt-<uuid>`（与 `sk-wgit-` 前缀区分，鉴权中间件据此分流）。明文只在创建时返回一次。

## 4. Scope 权限模型

最小权限，逗号/数组式 scope。首期集合：

| scope | 允许的操作 | 端点（示例） |
| --- | --- | --- |
| `read:usage` | 查询用量、对账、账单汇总 | `GET /api/enterprise/usage`, `/api/enterprise/reconciliation` |
| `read:pricing` | 查询价目/模型目录 | `GET /api/keys/pricing`, `/api/service-models/public` |
| `read:keys` | 列出/查看下游 key | `GET /api/admin/downstream-keys`（限本企业） |
| `manage:keys` | 建/禁用/删除下游 key | `POST/PATCH/DELETE /api/enterprise/keys` |
| `read:members` | 列出企业成员 | `GET /api/enterprise/members` |
| `manage:members` | 增删成员、改额度/角色 | `POST/PUT/DELETE /api/enterprise/members/*` |

原则：
- **读写分离**：首期先放开 `read:*`（低风险、高价值），`manage:*` 二期。
- **企业隔离**：企业绑定的 mgmt token 只能操作**本企业**资源（enterprise_id 强制过滤），复用现有 `requireEnterpriseContext` 语义。
- **不含推理**：任何 scope 都不授予 `/v1/*` 推理权限。

## 5. 鉴权中间件

新增 `middleware`（或复用现有 auth）分流逻辑：

```
Authorization: Bearer <token>
  ├─ 前缀 sk-wgit-  → 现有 API key 校验（推理）
  ├─ 前缀 mgmt-     → ManagementTokenHandler：
  │     1. SHA-256 hash → 查 management_tokens（active, 未过期, 未删除）
  │     2. 命中失败 → 401
  │     3. 校验请求端点所需 scope ⊆ token.scopes，否则 403
  │     4. 注入 ctx：user_id, enterprise_id, token_scopes
  │     5. 异步更新 last_used_at（不阻塞）
  └─ 其它（JWT）   → 现有 RequireAuth
```

关键安全点：
- token_hash 用 `subtle.ConstantTimeCompare` / 唯一索引查找；绝不存/日志明文。
- 端点声明所需 scope（如 `RequireScope("manage:keys")`），中间件按最小权限校验。
- 企业绑定 token 的所有查询都追加 `enterprise_id = <token.enterprise_id>` 过滤，防越权。
- 复用 `token_version` 式撤销：`status='revoked'` 即刻失效（无缓存或缓存 TTL ≤ 60s）。

## 6. 端点

**Token 自管理（JWT 或 admin 签发，UI + API）**

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `POST` | `/api/management-tokens` | 创建（返回一次性明文），指定 name/scopes/expires_in |
| `GET` | `/api/management-tokens` | 列出自己/本企业的 token（不含明文） |
| `POST` | `/api/management-tokens/:id/revoke` | 吊销 |
| `DELETE` | `/api/management-tokens/:id` | 软删除 |

签发权限：企业管理员可为本企业签发；普通成员是否可签发由策略决定（建议默认仅 enterprise_admin + system/super admin）。

**被 mgmt token 消费的管理端点**：现有 `/api/enterprise/*`、`/api/keys/pricing`、`/api/admin/downstream-keys`（限本企业）等，接受 `mgmt-` 鉴权 + scope 校验。

## 7. 分期实施

| 阶段 | 内容 | 风险 |
| --- | --- | --- |
| **P1（只读）** | 表 + 中间件 + `read:usage`/`read:pricing`/`read:keys` + token CRUD + UI 生成/吊销 | 低（只读，不改写路径） |
| **P2（写）** | `manage:keys`（建/禁/删下游 key）+ 审计强化 | 中（写操作，需严格企业隔离 + 审计） |
| **P3（成员/高级）** | `manage:members`、额度管理、（可选）webhook/事件订阅 | 中高 |

## 8. 安全考量（必须）

- 明文仅创建时返回一次；库内只存 SHA-256。
- 所有 mgmt-token 操作写 `audit_logs`（谁、用哪个 token、做了什么、企业上下文）。
- 强制企业隔离：绑定企业的 token 无法触达其它企业数据。
- 默认过期（建议 365 天）+ 可吊销；到期/吊销即 401。
- 速率限制：mgmt token 端点复用现有 IP/token 级限流，防枚举。
- scope 默认最小；创建时若未指定 scope 则拒绝（不给"全权"默认）。
- 不授予推理权限，避免和 `sk-` key 混淆用途。

## 9. 与现有能力的关系

- **Q1 `/api/keys/pricing`（已实现）**：目前用 `sk-` key 鉴权返回价目；后续可让 `read:pricing` 的 mgmt token 也访问，形成"管理凭据查价目"的统一入口。
- **受控自助企业 key（已上线）**：admin 用 `manage:keys` mgmt token 脚本化维护 key，成员在 admin 划定的公开组边界内自助建 key，二者互补。

## 10. 待确认（实施前）

1. 普通企业成员能否自行签发 mgmt token，还是仅 enterprise_admin？（建议仅 admin）
2. P1 是否只需要 `read:usage` + `read:pricing`（最小可用），先上这两个？
3. 默认过期时长（建议 365 天，可配）。
4. 是否需要个人（非企业）用户的 mgmt token，还是仅企业场景？

> 本文为 DRAFT，待确认第 10 节后进入 P1 实现（表 + 迁移 + 中间件 + 只读 scope + UI + 测试）。

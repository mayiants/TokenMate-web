> **✅ COMPLETED** — 6.1-6.6 全部实现，额外完成 11 项增强功能

# Stage 6: 产品化与运营能力建设

> 目标：从"能用"升级为"可运营的产品"，补齐用户转化、运营工具、透明度三大短板
> **Last Updated:** 2026-05-17

## 背景

Stage 1-5 完成了核心代理、计费、管理后台、模型生命周期等基础设施。当前系统功能完整但"产品感"不足——首页无内容、运营工具缺失、数据透明度低。Stage 6 聚焦产品化，参考 laozhang.ai、apiyi.com、ikuncode.cc 等中转站的最佳实践，补齐运营闭环。

---

## 6.1 首页与产品展示 — ✅ COMPLETE

**现状**：首页只有登录/注册表单，无任何产品介绍，新用户无法理解平台价值。

**目标**：建设有吸引力的 Landing Page，提升注册转化率。

### 6.1.1 Landing Page 内容

| 区域 | 内容 | 参考 |
|------|------|------|
| Hero | 大标题 + 副标题 + CTA 按钮（立即注册） | laozhang.ai 首屏 |
| 产品特性 | 3-4 个核心卖点卡片（多模型、稳定中转、按量计费、企业级安全） | apiyi.com features |
| 支持模型 | Provider + Model 网格展示，带价格标签 | ikuncode.cc 模型列表 |
| 价格方案 | 按量计费说明 + 充值档位 + 对比表 | laozhang.ai pricing |
| 用户评价 | 可选，后续运营补充 | — |
| Footer | 联系方式、文档链接、备案信息 | 通用 |

### 6.1.2 技术方案

- 模板：`web/templates/landing.html`
- 数据来源：`/api/service-models/public` 获取模型和价格
- 样式：与现有模板一致的 CSS 变量体系
- 响应式：移动端适配

### 6.1.3 导航调整

- 未登录用户 → Landing Page（`/`）
- 已登录用户 → Dashboard（`/dashboard`）
- 登录/注册按钮保留在导航栏

### 实现状态

| 区域 | 状态 | 说明 |
|------|------|------|
| Hero 区域 | ✅ 已实现 | 大标题 + 副标题 + CTA 按钮（立即注册） |
| 产品特性卡片 | ✅ 已实现 | 多模型、稳定中转、按量计费、企业级安全 |
| 支持模型展示 | ✅ 已实现 | Provider + Model 网格，从 `/api/service-models/public` 获取 |
| 价格方案 | ✅ 已实现 | 按量计费说明 + 充值档位 |
| Footer | ✅ 已实现 | 联系方式、文档链接 |
| 导航调整 | ✅ 已实现 | 未登录→Landing Page，已登录→Dashboard |
| 响应式适配 | ✅ 已实现 | 移动端适配 |

---

## 6.2 Upstream Key 用量统计 — ✅ COMPLETE

**现状**：计费仅面向 user key，upstream key 缺乏独立的用量统计。当 provider 出具账单时，双方数据可能不一致，无法对账。

**目标**：建立 upstream key 级别的 token/request/cache 统计体系，实现与 provider 的数据对账。

### 6.2.1 数据模型

```sql
-- upstream_key_usage: 按 upstream key + model + day 聚合
CREATE TABLE upstream_key_usage (
    id BIGSERIAL PRIMARY KEY,
    upstream_key_id INT NOT NULL REFERENCES upstream_keys(id),
    model VARCHAR(100) NOT NULL,           -- api_model_name
    date DATE NOT NULL,                    -- 聚合日期
    request_count INT DEFAULT 0,
    input_tokens BIGINT DEFAULT 0,
    output_tokens BIGINT DEFAULT 0,
    cache_read_tokens BIGINT DEFAULT 0,
    cache_creation_tokens BIGINT DEFAULT 0,
    error_count INT DEFAULT 0,
    avg_latency_ms INT DEFAULT 0,
    created_at TIMESTAMP DEFAULT NOW(),
    UNIQUE(upstream_key_id, model, date)
);

-- 索引
CREATE INDEX idx_upstream_key_usage_key_date ON upstream_key_usage(upstream_key_id, date);
CREATE INDEX idx_upstream_key_usage_date ON upstream_key_usage(date);
```

### 6.2.2 数据采集

- **写入时机**：Proxy 转发完成后，将 usage 数据写入 Redis 临时缓存
- **聚合方式**：Portal 定时（每 5 分钟）从 Redis 读取并 upsert 到 `upstream_key_usage`
- **Redis Key 格式**：`upstream_usage:{key_id}:{model}:{date}` → Hash(request_count, input_tokens, ...)

### 6.2.3 API 与界面

| 接口 | 说明 |
|------|------|
| `GET /api/admin/upstream-usage` | 查询 upstream key 用量（支持按 key/model/日期筛选） |
| `GET /api/admin/upstream-usage/summary` | 按 provider 汇总，用于对账 |
| `GET /api/admin/upstream-usage/export` | 导出 CSV，用于与 provider 账单比对 |

Admin 界面：新增 "Upstream 用量" 页面，表格展示 + 筛选 + 导出。

### 6.2.4 与 Proxy 的数据同步

- Go Proxy 在响应中已有 usage 数据，需增加 upstream_key_id 维度
- 同步协议复用现有 `/api/admin/sync` 机制，增加 `upstream_usage` 类型

---

## 6.3 Audit 日志增强 — ✅ COMPLETE

**现状**：Audit 日志仅记录 action + user_id + details（文本），内容单薄，难以追溯和审计。

**目标**：结构化审计日志，支持精确查询和合规审计。

### 6.3.1 Schema 增强

```sql
-- 扩展 audit_logs 表
ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS target_type VARCHAR(50);   -- user/api_key/group/model/order/...
ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS target_id VARCHAR(100);    -- 目标对象 ID
ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS old_value JSONB;           -- 变更前值
ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS new_value JSONB;           -- 变更后值
ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS ip_address VARCHAR(45);    -- 操作来源 IP
ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS user_agent VARCHAR(500);   -- 浏览器 UA

CREATE INDEX idx_audit_logs_target ON audit_logs(target_type, target_id);
CREATE INDEX idx_audit_logs_action ON audit_logs(action);
```

### 6.3.2 审计事件分类

| 类别 | 事件示例 | 优先级 |
|------|---------|--------|
| 认证 | 登录/登出/密码修改/2FA 启用 | P0 |
| 密钥 | 创建/删除/暂停/恢复 API Key | P0 |
| 资金 | 充值/消费/退款/余额变更 | P0 |
| 权限 | 角色变更/组变更/权限调整 | P0 |
| 模型 | 模型状态变更/EOL 处理/价格调整 | P1 |
| 配置 | 系统设置变更/上游密钥变更 | P1 |
| 用户 | 注册/信息修改/账号冻结 | P1 |
| 运营 | 公告发布/兑换码创建/邀请码创建 | P2 |

### 6.3.3 Audit API 增强

| 接口 | 说明 |
|------|------|
| `GET /api/admin/audit-logs` | 支持按 target_type/target_id/action/时间范围筛选 |
| `GET /api/admin/audit-logs/:id` | 查看详情（含 old_value/new_value diff） |
| `GET /api/admin/audit-logs/export` | 导出审计日志 |

### 6.3.4 前端展示

- Audit 列表页：增加筛选器（类型、操作、对象、时间）
- 详情弹窗：展示变更 diff（old → new 高亮）
- 关键操作实时告警：余额异常、权限变更等可配置 Webhook 通知

### 实现状态

| 项目 | 状态 | 说明 |
|------|------|------|
| Schema 增强 | ✅ 已实现 | 新增 target_type, target_id, old_value, new_value, ip_address, user_agent 字段 |
| 索引 | ✅ 已实现 | target_type+target_id, action 索引 |
| 审计事件分类 | ✅ 已实现 | 认证/密钥/资金/权限/模型/配置/用户/运营 8 大类 |
| API 筛选 | ✅ 已实现 | 支持按 target_type/target_id/action/时间范围筛选 |
| 前端筛选器 | ✅ 已实现 | 类型、操作、对象、时间筛选 |
| 详情弹窗 | ✅ 已实现 | 展示变更 diff（old → new） |

---

## 6.4 邀请码与邀请奖励 — ✅ COMPLETE

**现状**：已有 subscription 和兑换码功能，缺少邀请裂变机制。

**目标**：实现邀请码生成、使用追踪、双向奖励，驱动用户增长。

### 6.4.1 数据模型

```sql
-- invitation_codes: 邀请码
CREATE TABLE invitation_codes (
    id BIGSERIAL PRIMARY KEY,
    code VARCHAR(20) NOT NULL UNIQUE,       -- 邀请码
    inviter_id INT NOT NULL REFERENCES users(id),  -- 邀请人
    max_uses INT DEFAULT 0,                 -- 最大使用次数（0=无限）
    use_count INT DEFAULT 0,                -- 已使用次数
    reward_type VARCHAR(20) DEFAULT 'balance',  -- balance/subscription/quota
    reward_amount DECIMAL(10,2) DEFAULT 0,  -- 邀请人奖励金额
    invitee_reward_amount DECIMAL(10,2) DEFAULT 0, -- 被邀请人奖励金额
    status VARCHAR(20) DEFAULT 'active',    -- active/disabled
    expires_at TIMESTAMP,                   -- 过期时间
    created_at TIMESTAMP DEFAULT NOW()
);

-- invitation_records: 邀请记录
CREATE TABLE invitation_records (
    id BIGSERIAL PRIMARY KEY,
    invitation_code_id INT NOT NULL REFERENCES invitation_codes(id),
    inviter_id INT NOT NULL REFERENCES users(id),
    invitee_id INT NOT NULL REFERENCES users(id),
    reward_granted BOOLEAN DEFAULT false,   -- 邀请人奖励是否已发放
    invitee_reward_granted BOOLEAN DEFAULT false, -- 被邀请人奖励是否已发放
    created_at TIMESTAMP DEFAULT NOW(),
    UNIQUE(invitee_id)                      -- 每个用户只能被邀请一次
);

CREATE INDEX idx_invitation_codes_inviter ON invitation_codes(inviter_id);
CREATE INDEX idx_invitation_records_inviter ON invitation_records(inviter_id);
```

### 6.4.2 功能流程

```
邀请人生成邀请码 → 分享给被邀请人
→ 被邀请人注册时填写邀请码
→ 注册成功后：
   1. 被邀请人获得 invitee_reward（注册奖励）
   2. 邀请人获得 inviter_reward（邀请奖励）
   3. 记录到 invitation_records
```

### 6.4.3 API

| 接口 | 说明 |
|------|------|
| `POST /api/invitation/generate` | 生成邀请码 |
| `GET /api/invitation/my-codes` | 查看我的邀请码列表 |
| `GET /api/invitation/records` | 查看邀请记录 |
| `POST /api/auth/register` | 注册接口增加 `invitation_code` 参数 |

### 6.4.4 Admin 管理

- 查看所有邀请码和邀请记录
- 配置全局邀请奖励规则（默认奖励金额）
- 禁用/启用邀请码
- 邀请统计面板（邀请人数、奖励支出）

### 6.4.5 与现有功能的关系

- 邀请奖励通过 `balance` 充值实现，复用现有余额体系
- 注册流程复用 `auth.register`，增加邀请码校验
- 邀请码与兑换码独立，不共用表

### 实现状态

| 项目 | 状态 | 说明 |
|------|------|------|
| 邀请码生成 | ✅ 已实现 | INV-XXXXXXXX 格式，每用户最多 N 个（可配置） |
| 注册时邀请码输入 | ✅ 已实现 | 注册表单增加邀请码字段 + URL 参数自动填充 |
| 双向奖励 | ✅ 已实现 | 邀请人 + 被邀请人均获得 gift_balance（消费优先级最高） |
| 邀请记录追踪 | ✅ 已实现 | invitation_records 表记录每次邀请 |
| Admin 邀请管理 | ✅ 已实现 | 查看所有邀请码/记录，配置全局奖励规则 |
| Admin 筛选+分页 | ✅ 已实现 | 按状态筛选，分页浏览 |
| 用户邀请码页面 | ✅ 已实现 | 生成/查看邀请码，查看邀请记录 |
| 欢迎码管理 | ✅ 已实现 | 移至邀请码管理页面，与邀请码统一管理 |
| 5-type 优惠码体系 | ✅ 已实现 | welcome/invitation/balance/promo/plan 五类码完整文档 |

---

## 6.5 系统公告 — ✅ COMPLETE

**现状**：无公告机制，无法向用户传达系统变更、维护通知、新模型上线等信息。

**目标**：实现公告发布、展示、管理全流程。

### 6.5.1 数据模型

```sql
-- announcements: 系统公告
CREATE TABLE announcements (
    id BIGSERIAL PRIMARY KEY,
    title VARCHAR(200) NOT NULL,
    content TEXT NOT NULL,
    type VARCHAR(20) DEFAULT 'info',        -- info/warning/maintenance/update
    priority INT DEFAULT 0,                  -- 排序权重
    pinned BOOLEAN DEFAULT false,            -- 是否置顶
    show_popup BOOLEAN DEFAULT false,        -- 是否弹窗展示
    start_at TIMESTAMP,                      -- 展示开始时间
    end_at TIMESTAMP,                        -- 展示结束时间
    status VARCHAR(20) DEFAULT 'draft',      -- draft/published/archived
    created_by INT REFERENCES users(id),
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

-- announcement_reads: 已读记录
CREATE TABLE announcement_reads (
    announcement_id INT NOT NULL REFERENCES announcements(id),
    user_id INT NOT NULL REFERENCES users(id),
    read_at TIMESTAMP DEFAULT NOW(),
    PRIMARY KEY(announcement_id, user_id)
);
```

### 6.5.2 用户端展示

| 位置 | 形式 | 条件 |
|------|------|------|
| 导航栏 | 红点/数字徽标 | 有未读公告 |
| Dashboard 顶部 | 横幅公告 | pinned 且在展示期内 |
| 弹窗 | Modal 弹窗 | show_popup=true 且用户未读 |
| 公告列表页 | `/announcements` | 所有已发布公告 |

### 6.5.3 API

| 接口 | 说明 |
|------|------|
| `GET /api/announcements` | 获取公告列表（用户端，仅 published） |
| `GET /api/announcements/unread-count` | 未读公告数量 |
| `POST /api/announcements/:id/read` | 标记已读 |
| `POST /api/admin/announcements` | 创建公告 |
| `PUT /api/admin/announcements/:id` | 更新公告 |
| `DELETE /api/admin/announcements/:id` | 删除公告 |
| `GET /api/admin/announcements` | 管理端公告列表（含 draft） |

### 6.5.4 Admin 管理界面

- 公告 CRUD（富文本编辑）
- 发布/撤回/归档操作
- 预览功能
- 类型选择（信息/警告/维护/更新）
- 定时发布（start_at/end_at）
- 已读统计

### 实现状态

| 项目 | 状态 | 说明 |
|------|------|------|
| 公告 CRUD | ✅ 已实现 | Admin 创建/编辑/删除/发布/撤回公告 |
| 公告类型 | ✅ 已实现 | info/warning/maintenance/update |
| 定时发布 | ✅ 已实现 | start_at/end_at 控制展示时间 |
| 用户端展示 | ✅ 已实现 | Dashboard 横幅 + 导航栏红点 |
| 已读标记 | ✅ 已实现 | 用户标记已读，Admin 查看已读统计 |
| Admin 管理界面 | ✅ 已实现 | 公告列表 + 创建/编辑弹窗 |

---

## 6.6 Provider/Model 可用性监控 — ✅ COMPLETE

**现状**：无主动监控，用户发现模型不可用后才知道问题。

**目标**：每 10 分钟自动检测各 provider/model 的可用性，异常时告警，数据可视化。

### 6.6.1 数据模型

```sql
-- provider_health_checks: 健康检查记录
CREATE TABLE provider_health_checks (
    id BIGSERIAL PRIMARY KEY,
    provider VARCHAR(50) NOT NULL,           -- provider ID
    model VARCHAR(100) NOT NULL,             -- api_model_name
    status VARCHAR(20) NOT NULL,             -- healthy/degraded/down
    latency_ms INT,                          -- 响应延迟
    error_message TEXT,                       -- 错误信息
    checked_at TIMESTAMP DEFAULT NOW()
);

-- provider_health_summary: 聚合状态（最新）
CREATE TABLE provider_health_summary (
    provider VARCHAR(50) NOT NULL,
    model VARCHAR(100) NOT NULL,
    status VARCHAR(20) NOT NULL,
    uptime_24h DECIMAL(5,2) DEFAULT 100,     -- 24h 可用率
    avg_latency_ms INT DEFAULT 0,
    last_check_at TIMESTAMP,
    last_down_at TIMESTAMP,
    PRIMARY KEY(provider, model)
);

CREATE INDEX idx_health_checks_time ON provider_health_checks(checked_at);
```

### 6.6.2 检测机制

```
每 10 分钟触发：
1. 遍历所有 active 的 service_models
2. 对每个 model 发送轻量级请求（如 1 token completion）
3. 记录响应时间、成功/失败
4. 更新 provider_health_summary
5. 如果状态从 healthy → down，触发告警
```

### 6.6.3 告警方式

| 方式 | 条件 | 接收人 |
|------|------|--------|
| Admin Dashboard 红标 | 任何 model down | Admin |
| 邮件通知 | 连续 2 次检测失败 | Admin + 受影响用户 |
| Webhook | 连续 3 次检测失败 | 配置的 Webhook URL |
| 公告自动发布 | 连续 5 次检测失败 | 所有用户（自动创建 maintenance 公告） |

### 6.6.4 API 与界面

| 接口 | 说明 |
|------|------|
| `GET /api/admin/provider-health` | 所有 provider/model 健康状态 |
| `GET /api/admin/provider-health/:provider/history` | 历史检测记录 |
| `GET /api/provider-health/public` | 公开状态页（可选） |

Admin 界面：状态看板，显示各 provider/model 的实时状态、24h 可用率、平均延迟。

用户端：模型选择时显示可用性标识（绿/黄/红点）。

### 6.6.5 与现有功能的关系

- 复用 `channel_health.go` 的部分逻辑
- 检测使用 upstream key 发送真实请求（最小 token）
- 检测结果可触发模型状态变更（连续失败 → auto pause）

---

## 实施优先级

| 序号 | 模块 | 优先级 | 状态 | 说明 |
|------|------|--------|------|------|
| 1 | 6.1 首页与产品展示 | P0 | ✅ 完成 | Landing Page 已上线 |
| 2 | 6.3 Audit 日志增强 | P0 | ✅ 完成 | 结构化字段 + 筛选 + diff |
| 3 | 6.5 系统公告 | P0 | ✅ 完成 | CRUD + 用户端展示 + 已读 |
| 4 | 6.4 邀请码与邀请奖励 | P1 | ✅ 完成 | 双向奖励 + 5-type 体系 + 欢迎码 |
| 5 | 6.6 Provider 可用性监控 | P1 | ✅ 完成 | 基于真实流量监控，5分钟窗口聚合 |
| 6 | 6.2 Upstream 用量统计 | P1 | ✅ 完成 | Upstream key 级别 token/request/cache 统计 |

### 额外完成的工作（不在原计划中）

| 项目 | 说明 |
|------|------|
| 兑换码体系增强 | 新增 description, max_uses, reward_type 字段；新增 promo 类型；统一 WG- 前缀格式 |
| gift_balance 一致性修复 | 所有免费奖励（兑换码、欢迎码、签到）均同时写入 gift_balance + balance |
| 欢迎码管理迁移 | 从兑换码管理页移至邀请码管理页，与邀请码统一管理 |
| Badge/状态一致性 | 全站移除非状态 badge，统一 active→活跃、disabled→禁用 |
| 订单页修复 | 修复分页 DOM 缺失导致 JS 崩溃的问题 |
| 系统设置页重设计 | 移除原始 CRUD 表格，仅展示已接入代码的配置项，类型安全控件 |
| 5-type 优惠码体系文档 | 完整文档覆盖 welcome/invitation/balance/promo/plan 五类码 |
| 计费 Pipeline 优化 | key_type 短路（14→6-8 DB操作）；统计与计费分离（双 Redis 队列）；Proxy batch 报告（3秒缓冲）；余额缓存预检；Portal 主动推送 + Proxy 独立余额缓存（10秒 TTL） |
| Model Provider Routing | Admin 可配置每个模型的 provider fallback 顺序（`model_provider_routing` 表）；Portal sync 传递 `model_routing` 到 Proxy；Proxy `GetRoutingOrder()` 确定性排序替代随机 map 遍历 |
| Sortable 模型选择 | 用户端 `apikeys.html` 多模型选择增加拖拽排序面板（Sortable.js）；Admin 端 `admin-models.html` 路由配置支持拖拽排序；Settings 弹窗同样支持拖拽排序 |
| 模型名翻译层 | Proxy `resolveProviderAndKey` 返回 `upstreamModelName`；`containsMatch` 归一化匹配（点号→短横线）；`ModelNameMap`（Phase 2）显式映射 `-fast` ↔ `-thinking` |
| Sync JSONB 修复 | pgx v5 返回 `[]interface{}` 而非 `[]byte`，导致 key models 字段为空；修复后 fallback 路由不再选到不支持该模型的 provider |

---

## 从 Stage 5 结转的待办

| 项目 | 说明 | 状态 |
|------|------|------|
| Cross-group Fallback 路由 | 多 group fallback 路由 + 计费链 | ✅ 已实现（group 内跨 provider fallback + 计费链；跨 group 链按当前设计不存在） |
| Proxy 侧 product_id → api_model_name 映射同步 | P2 | ✅ 已实现（Portal sync 下发 model_name_map；Proxy 解析 upstreamModelName） |
| protected flag 实现 | 防止核心模型被自动清理 | 🚧 部分实现（DB 字段与清理逻辑已存在，缺少管理 UI/API） |

---

## 待讨论 / 待后续

- [x] 首页是否需要多语言支持（中/英） → 已支持中/英切换
- [x] 邀请奖励的金额和规则 → 固定金额，通过 system_settings 配置（invitation_inviter_reward / invitation_invitee_reward）
- [x] Upstream 用量统计是否需要与 Proxy 侧实时推送（当前方案是定时拉取） → 已实现实时推送（Proxy 异步批量上报 `/webhook/upstream-usage/batch`）
- [ ] Provider 监控公开状态页（如 status.github.com） → 公开 JSON API `/api/status/providers` 已存在，HTML 状态页待实现
- [ ] Audit 日志保留策略 → DB 中有 audit_retention_years=7 和 audit_log_retention_days=90，但后端尚未实现定时清理任务
- [ ] 系统设置中大量"死设置"（53 个中仅 15 个生效）的接入计划 — 逐个功能实现时逐步接入

# 消息提醒机制设计方案（Notification Center）

**Status: DRAFT-v2（已根据 2026-10-31 评审决策修订）**
**Date: 2026-10-31**

## 1. 背景与现状盘点

用户（system admin / enterprise admin / 普通用户）目前必须主动进入管理页面才能看到待办和预警。盘点已有设施——**基础设施比想象的齐全，缺的是统一的"触达层"**：

| 设施 | 状态 | 说明 |
|---|---|---|
| `enterprise_alerts` 表 + `/enterprise/alerts` 页面 | ✅ 在用 | `sendEnterpriseAlert()` 在企业 quota/credit 80%/90% 时写入；有 acknowledged 流程。仅页面内可见 |
| `announcements` + `announcement_reads` | ✅ 在用 | 全局广播 + 已读追踪（`LEFT JOIN reads` 读模型，最佳参考实现） |
| `email_notifications` 表 | ⚪ **死表** | 邮件队列 schema 齐全（status/sent_at/error_message），无 producer/consumer |
| `pkg/mail` | ✅ 在用 | SMTP 发送器，i18n 模板 |
| `invoice-requests` 流程 | ✅ 在用 | 用户发票申请 + admin approve/reject/complete 端点已存在（admin_common.go）——**缺的是"新申请到达"通知** |
| 后台定时任务 | ✅ 8 个 in-process ticker | cmd/server/background.go（8h/24h×2/6h×4/1h）——**无 job 注册表/心跳，无法感知"任务异常"** |
| 成员加入 | invite-only（保持不变） | 决策 Q2：不实现自助申请 |
| 个人配额/余额预警 | ❌ 缺失 | 仅企业维度有 |

## 2. 评审决策（2026-10-31）

1. **邮件策略**：高优先级事件默认发邮件；其余事件用户可自行配置开关（通知偏好）
2. **成员流程**：保持邀请制，不做自助申请
3. **system admin 通知清单**（终版）：
   - 上行错误提醒（upstream key 错误率/校验失败/异常事件）
   - 支付错误或需复核
   - 定时任务异常（新增 job 心跳机制）
   - **enterprise 用户加入时的二次确认**（新增审批环节）
   - enterprise 或个人的发票申请（流程已存在，补通知）
   - enterprise quota ≥110% 或 credit ≥100%（硬上限告警）
4. **IM 渠道**：需要微信/飞书/钉钉 → 见 §6（统一 Webhook 通道）

## 3. 详细设计

### 3.1 数据模型

```sql
-- 统一收件箱
CREATE TABLE notifications (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     uuid NOT NULL REFERENCES users(id),
    type        varchar(50)  NOT NULL,
    scope       varchar(20)  NOT NULL DEFAULT 'user',  -- user | enterprise_admin | system_admin
    priority    varchar(10)  NOT NULL DEFAULT 'normal',-- high（默认发邮件）| normal
    title       varchar(255) NOT NULL,
    body        text,
    link        varchar(255),
    ref_type    varchar(50),                            -- 去重键
    ref_id      varchar(100),
    read_at     timestamp,
    created_at  timestamp NOT NULL DEFAULT now()
);
CREATE INDEX idx_notifications_user_unread ON notifications(user_id) WHERE read_at IS NULL;
CREATE UNIQUE INDEX uq_notification_dedup ON notifications(user_id, type, ref_type, ref_id) WHERE read_at IS NULL;

-- 用户通知偏好（非高优先级事件的邮件/webhook 开关；高优先级不受偏好控制）
CREATE TABLE notification_preferences (
    user_id    uuid NOT NULL REFERENCES users(id),
    ntype      varchar(50) NOT NULL,     -- '*' = 全局默认
    channel    varchar(20) NOT NULL,     -- email | webhook
    enabled    boolean NOT NULL DEFAULT true,
    updated_at timestamp NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, ntype, channel)
);
```

- email 渠道继续用死表 `email_notifications` 做队列（worker 见 3.4）
- 读模型完全复用 `announcements` 的 LEFT JOIN reads 模式

### 3.2 UI

- **铃铛**：layout.html topbar user-menu 旁（Alpine dropdown 同款），HTMX `hx-trigger="every 30s"` 轮询未读徽标；下拉显最近 10 条 + /notifications 全量页（复用 table-pagination）
- **偏好设置**：/profile 或 /settings 增加"通知偏好"区块（按事件类型勾选 email/webhook）
- **i18n**：title/body 按接收人 locale 生成时渲染；en/zh 双语 key 走 i18n_full_audit

### 3.3 事件目录（终版）

**enterprise admin 维度**：

| type | 触发点 | 优先级 |
|---|---|---|
| `enterprise_member_invited` | 邀请发出 | normal |
| `enterprise_member_pending_ea` | 用户 accept 后待 enterprise admin 确认（域名匹配情形，现有 `email_confirmed` 状态） | high |
| `enterprise_join_confirm` | EA 已批但邮箱域名不在企业白名单，待 system admin 二次审批（现有 `ea_approved` 状态，`approveInvitation` 入口） | high |
| `enterprise_quota_warning` | 80%/90%（复用 sendEnterpriseAlert 埋点） | 90%→high |
| `enterprise_quota_exceeded` | **≥110%**（硬上限） | high |
| `enterprise_credit_warning` | 阈值临近 | 高阈→high |
| `enterprise_credit_exceeded` | **≥100%**（硬上限） | high |
| `enterprise_invoice_request` | 企业发票申请提交 | high |

**待审批计数徽标（与通知配套，评审已确认需求）**：`/admin/enterprise` 导航项 + 企业表项行内显示待二次审批计数（`enterprise_invitations WHERE status='ea_approved'` 按 enterprise 聚合）；enterprise admin 侧对应 `email_confirmed` 待确认计数（导航 + members 页签徽标）。计数端点走与铃铛相同的 30s 轮询。

**system admin 维度**：

| type | 触发点 | 优先级 |
|---|---|---|
| `upstream_error_alert` | **1 小时窗口 error rate > 30%**（数据源：model_group_health_stats / upstream_key_validation_log，评审已定阈值） | high |
| `payment_error` / `payment_review` | 支付回调失败 / 对账差异需复核（webhook 已有错误路径） | high |
| `cron_job_anomaly` | job 心跳缺失或失败（job_runs 巡检，§3.5） | high |
| `invoice_request` | 个人发票申请提交 | high |

**个人用户维度**：

| type | 触发点 | 优先级 |
|---|---|---|
| `quota_warning` | subscription quota 80%/90%（webhook 已有计算点） | 90%→high |
| `balance_low` | 余额低于阈值 | high |
| `announcement` | 全局公告镜像（可选） | normal |

去重：`uq_notification_dedup` 未读唯一；阈值类 acknowledged/已读后可再触发（与 enterprise_alerts 语义一致）。`enterprise_alerts` 表保留，通知中心做触达镜像。

### 3.4 投递 worker

portal 内 goroutine：每 30s 扫 `email_notifications WHERE status='pending' LIMIT 50`，pkg/mail 发送；webhook 类型（§6）同一循环按渠道发送。失败置 failed 保留由 retention 清理，不自动重试。

### 3.5 定时任务心跳（新设施）

```sql
CREATE TABLE job_runs (
    job_name    varchar(100) PRIMARY KEY,
    interval_min int NOT NULL,
    last_run_at  timestamp,
    last_status  varchar(20),       -- ok | failed
    last_error   text,
    last_notified_at timestamp      -- 异常通知去重（同一 job 未恢复前 24h 只报一次）
);
```

background.go 的每个 ticker 包一层 `withJobHeartbeat(name, interval, fn)`：执行前后更新 job_runs；巡检循环发现 `last_run_at` 超窗或 `last_status=failed` → `cron_job_anomaly` 通知 system admin。

## 4. 分期实施计划

| Phase | 内容 | 预估 |
|---|---|---|
| **P1** | notifications/preferences/job_runs 三表 + migration + 铃铛/通知页/偏好 UI + 事件埋点（企业维度全部 + 待审批计数徽标 + 个人 quota/balance + 发票申请） | 3 天 |
| **P2** | email worker + 高优先级外发；system admin 事件（upstream_error 1h>30% 接入 health stats、payment 错误路径、cron 心跳巡检）；`balance_low` 阈值 admin/settings 可配（默认 $1） | 1.5 天 |
| **P3** | 统一 webhook 通道（飞书/钉钉/企业微信，§6）+ 偏好接入 webhook | 1-2 天 |
| P4（可选） | 企微/飞书/钉钉自建应用私信通道、公告镜像、recon 异常订阅 | 按需 |

## 5. 已定决策摘要 & 剩余待确认

**已定（2026-10-31 评审）**：成员加入二次审批流程**已存在**（域名匹配→EA 确认；非域名→system admin 在 /admin/enterprise 二次审批），本期只做触达（通知 + 导航/表项待办计数徽标）；`upstream_error_alert` = 1h error rate >30%；`balance_low` 默认 $1 且 admin/settings 可配。

**剩余待确认**：
1. ~~发票申请入口~~ 已确认：个人侧 `POST /api/invoices/requests` → `invoice_requests` 写入（invoices.go:482，含 audit log），企业侧同表——埋点即"INSERT 后追加通知"
2. 通知铃铛对 RBAC 的呈现：system admin 是否也能看到 enterprise 维度的摘要（建议：铃铛只显示"与自己相关"的，admin 的企业列表计数走徽标体系，两者互补不重复）

## 6. IM 渠道（微信/飞书/钉钉）集成方案

### 6.1 先澄清一个事实

**个人微信没有官方机器人接口**（封号风险极高的第三方协议方案不可取）。"微信通知"的正路是**企业微信**。所以三个平台的现实选项：

| 平台 | 推荐方式 | 门槛 |
|---|---|---|
| 飞书 | 群自定义机器人（webhook + 加签） | 低：群里点几下就有 webhook URL |
| 钉钉 | 群自定义机器人（webhook + 加签） | 低：同上要密钥 |
| 微信 | **企业微信**群机器人（纯 webhook，无签名） | 低：群里添加即可 |

### 6.2 我方需要开发的组件（统一 Webhook 通道，一套代码三平台）

1. **`notification_channels` 表**（admin 自配置，替代硬编码）：
   ```sql
   CREATE TABLE notification_channels (
       id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
       user_id    uuid NOT NULL REFERENCES users(id),   -- 属主 admin
       type       varchar(20) NOT NULL,  -- feishu | dingtalk | wecom
       webhook_url text NOT NULL,
       secret     text,                 -- 加签密钥（钉钉/飞书），可空（企微）
       enabled    boolean DEFAULT true,
       filters    jsonb                 -- 接收的事件类型白名单，空=全部 admin 级
   );
   ```
2. **Webhook sender（pkg/notify/webhook.go，~250 行 Go）**：
   - 消息体按平台构造 markdown（三平台都支持 markdown 消息类型）
   - 签名：钉钉 = `timestamp+"\n"+secret` 做 HMAC-SHA256 → base64，URL 加 `&timestamp=&sign=`；飞书 = 同算法 sign 放 body；企微 = 无签名
   - 10s 超时、失败记录、不重试（ IM 群消息不重试防轰炸）
3. **配置 UI**：/admin/settings 或 profile 下"通知渠道"区块：选平台 → 粘贴 webhook URL（+密钥）→ 点"发送测试消息"验证 → 保存
4. **路由规则**：scope=system_admin 且 priority=high 的通知 → 投递给所有 enabled 且 filters 命中的渠道；enterprise/个人维度默认不进 IM（群是 admin 的）

### 6.3 如果以后要"私信到具体管理员"（而非群消息）

需要升级为企业**自建应用**：飞书/钉钉/企微开发者后台注册应用 → app_id/app_secret → 获取 tenant_access_token（企微 2h 过期需自动刷新）→ 调应用消息接口按用户 ID 发送。组件多 3 倍（token 管理、用户 ID 映射、应用审核），**P3 不做，有真实需求再立**。

### 6.4 工作量

| 项 | 预估 |
|---|---|
| notification_channels 表 + CRUD + 配置 UI（含测试消息按钮） | 0.5 天 |
| webhook sender 三平台适配 + 签名 + 投递接线 | 0.5-1 天 |
| 合计 | **1-1.5 天**（依赖 P1/P2 的通知中心基建） |

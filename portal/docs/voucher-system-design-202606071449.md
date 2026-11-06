# Voucher / 优惠码系统设计分析与改进建议

> **Status**: ✅ COMPLETED — Plan Voucher auto-subscribe (B1) implemented
> 日期：2026-06-07
> 更新：2026-06-10 — Verified implementation status
> 范围：vouchers、redemption_codes、余额消费链路

---

## 1. 概述

~~当前系统存在 **两套功能高度重叠的兑换码体系**~~  
**【2026-06-08 更新】已完成统一**：所有兑换码功能已合并到 `vouchers` + `voucher_uses` 单一体系。

| 系统 | 数据表 | 管理页面 | 用户兑换页面 | 状态 |
|------|--------|---------|------------|------|
| **Voucher** | `vouchers` + `voucher_uses` | `admin/vouchers` | `/voucher` | ✅ 活跃 |
| ~~Redemption~~ | ~~`redemption_codes` + `redemption_records`~~ | ~~`admin/redemption`~~ | ~~`/redemption`~~ | ✅ 已迁移 |
| ~~Invitation~~ | ~~`invitation_codes` + `invitation_records`~~ | ~~`admin/invitations`~~ | ~~`/invitation`~~ | ✅ 已迁移 |

**迁移完成清单**：
- `ApplyWelcomeBonus` → 从 `redemption_codes` 迁移到 `vouchers(type='welcome')`
- `ProcessInvitationCode` → 从 `invitation_codes` 迁移到 `vouchers(type='referral')`
- 删除冗余页面：`admin-redemption.html`、`redemption.html`、`admin-invitations.html`、`invitation.html`
- 删除冗余路由：`/redemption`、`/invitation`、`/admin/redemption`、`/admin/invitations`
- 旧数据表 `redemption_codes`、`invitation_codes` 已从 Go 代码中完全移除（schema.go 不再创建，admin.go 不再查询）；现有数据库中的表保留不动

---

## 2. 当前系统架构

### 2.1 Voucher 系统 (`vouchers` 表)

字段亮点：
- `owner_type`: `system` / `admin` / `user` —— 区分系统/管理员/用户创建的码
- `user_id`: 用户创建的码（如 referral）关联到具体用户
- `reward_type`: 默认 `gift_balance`，理论上可扩展为其他奖励类型
- `reward_config`: JSONB，用于 referral 的双边奖励配置（邀请人+被邀请人）
- `plan_id`: UUID，关联 `subscription_plans`，**当前为可选字段**
- `auto_apply_on_register`: 新用户注册时自动触发

#### 各类型创建与消费方式

| 类型 | 创建者 | 创建入口 | 消费触发 | 实际行为 |
|------|--------|---------|---------|---------|
| `gift` | Admin | admin/vouchers → Create Voucher | 用户在 `/voucher` 输入码兑换 | 充 `gift_balance`（Plan B：不增加 `balance`） |
| `promo` | Admin | admin/vouchers → Create Voucher | 用户在 `/voucher` 输入码兑换 | 充 `gift_balance`（Plan B：不增加 `balance`） |
| `referral` | User | `/voucher` → Generate Referral Code | 被邀请人注册时自动兑换 | 被邀请人充 `gift_balance`；邀请人自动入账 `gift_balance` |
| `welcome` | Admin（系统配置） | admin/vouchers → Welcome Settings | 新用户注册自动发放 | 充 `gift_balance`（Plan B：不增加 `balance`） |
| `plan_token` | Admin | admin/vouchers → Create Voucher（选 Token Plan） | 用户在 `/voucher` 输入码兑换 | ✅ 自动开通/续期对应 Token Plan 订阅 |
| `plan_code` | Admin | admin/vouchers → Create Voucher（选 Code Plan） | 用户在 `/voucher` 输入码兑换 | ✅ 自动开通/续期对应 Code Plan 订阅 |

#### Referral 详细流程

1. 用户在 `/voucher` 点击 "Generate Referral Code"
2. 后端调用 `generate()`，从 `system_settings` 读取默认参数：
   - `invitation_max_codes_per_user`: 每人最大码数（默认 10）
   - `invitation_inviter_reward`: 邀请人奖励（默认 $5）
   - `invitation_invitee_reward`: 被邀请人奖励（默认 $2）
   - `invitation_default_max_uses`: 默认最大使用次数（默认 10）
   - `invitation_default_expire_days`: 默认有效期（默认 365 天）
3. 生成 `referral` 类型码，存入 `vouchers` 表
4. 被邀请人注册时，如果填写了邀请码，系统自动兑换：
   - 被邀请人获得 `invitee_reward` → 充 `gift_balance`
   - 邀请人获得 `inviter_reward` → 自动充 `gift_balance`（默认 `invitation_auto_transfer_enabled=true`）
5. ~~邀请人可在 `/voucher` 将 `aff_quota` 转入 `gift_balance`~~ 已移除 Transfer 按钮和入口

### 2.2 ~~Redemption 系统 (`redemption_codes` 表)~~ — 已迁移

> **状态**：`redemption_codes` 和 `invitation_codes` 表仍存在于数据库中，但代码已完全迁移到 `vouchers` 表。
>
> `ApplyWelcomeBonus` 和 `ProcessInvitationCode` 现在统一读取 `vouchers` 表，写入 `voucher_uses` 表。

### 2.3 余额消费优先级链（Plan B）

用户消费（API 调用扣费）时的扣款顺序：

```
general 模式（普通用户）:
  gift_balance → balance（兜底）

enterprise 模式（企业用户）:
  gift_balance → enterprise_credit（110% cap）→ HARD STOP（不扣个人 balance）

subscription 模式:
  subscription_quota → balance
```

余额类型说明：
- `balance`: 真实资金（充值、现金）
- `gift_balance`: 赠送余额（优惠码、签到、邀请奖励、welcome 等来源）
- ~~`recharge_balance`: 充值余额~~ **已废弃**
- `aff_quota`: 邀请奖励额度（仅当 `invitation_auto_transfer_enabled=false` 时使用）
- **总余额** = `balance + gift_balance`（计算值，非存储字段）

---

## 3. 当前存在的问题

### ~~问题 1：两套并行系统（技术债务）~~ ✅ 已解决

`vouchers` 和 `redemption_codes` / `invitation_codes` 的统一迁移已完成：
- `ApplyWelcomeBonus` 现在查询 `vouchers(type='welcome')`，写入 `voucher_uses`
- `ProcessInvitationCode` 现在查询 `vouchers(type='referral')`，解析 `reward_config`，双边奖励写入 `voucher_uses`
- 冗余页面、路由、模板、handler 已全部删除
- `schema_test.go` 中的 `welcome_voucher` 测试已同步修复（同时更新 `users.balance` 和 `vouchers.used_count`）

### 问题 2：Plan Voucher 是半成品

`plan_token` / `plan_code` 类型 voucher 的当前状态：
- **创建时**：`plan_id` 是可选字段（不选也能创建）
- **兑换时**：`redeem()` 函数对 plan 类型没有任何特殊处理，只执行 `UPDATE users SET gift_balance = ..., balance = ...`
- **结果**：plan voucher 本质上就是 gift voucher，选 plan 和不选 plan 没有任何区别

根本矛盾：如果 plan voucher 只是充余额，那它和 `gift`/`promo` 类型没有区别；如果希望 plan voucher 能真正开通套餐，兑换逻辑必须走 `activateSubscription` 路径（创建/续期 `user_subscriptions`）。

### ~~问题 3：Redemption 系统被 Voucher 系统部分取代~~ ✅ 已解决

Redemption 和 Invitation 系统已完全下线，所有功能统一到 Voucher 系统。

### ~~问题 4：Welcome Voucher 在两个系统中都有痕迹~~ ✅ 已解决

- `vouchers` 表 Welcome Settings 管理保留并正常工作
- `redemption_codes` 相关代码已完全移除

### ~~问题 5：类型约束不一致~~ ✅ 已解决

- `vouchers` 表的 `type` 字段通过 `ALTER TABLE ... ADD COLUMN IF NOT EXISTS type VARCHAR(20) DEFAULT 'gift'` 补全
- `redemption_codes` 不再被代码使用

---

## 4. 改进建议

### 建议 A：合并两套系统（推荐长期做）

将 `redemption_codes` 的数据迁移到 `vouchers` 表，统一使用 Voucher 系统：

1. **数据迁移**：将 `redemption_codes` 中的 gift/promo 码导入 `vouchers`，类型标记为 `gift` 或 `promo`
2. **路由合并**：`/api/redemption/*` 的兑换功能统一到 `/api/vouchers/redeem`
3. **界面合并**：admin-redemption 页面功能合并到 admin-vouchers，用户 redemption 页面合并到 voucher 页面
4. **删除冗余**：下线 `redemption_codes` 表及相关代码

**收益**：减少 50% 的兑换码维护代码，消除数据分散问题。

### 建议 B：明确 Plan Voucher 的设计意图

根据产品定位，plan voucher 有两条可行的实现路径：

#### 路径 B1：Plan Voucher = 自动开通套餐（非余额）

- **创建时**：强制选择具体 plan（`plan_id` 必填）
- **兑换时**：不充余额，直接调用 `activateSubscription` 逻辑：
  - 查询 `subscription_plans` 获取 plan 详情
  - 创建/续期 `user_subscriptions` 记录
  - 可选：同时创建一条 `subscription_purchases` 记录
- **适用场景**："送用户 1 个月 Pro 套餐"这类明确赠送具体套餐的场景

#### 路径 B2：Plan Voucher = 通用余额券（简化）

- **删除** `plan_token` / `plan_code` 类型
- 统一使用 `gift` / `promo` 类型创建兑换码
- 用户拿到余额后自行去订阅页面购买任意 plan
- **适用场景**："送 $50 余额，可用于购买任意套餐或 API 调用"

**当前系统走的路径**：实际上是 B2（充余额），但 UI 上多了一套 plan 类型和 plan 选择器，给用户造成了"会开通套餐"的错觉。

#### 路径 B3：Plan Voucher = 兑换时自选套餐（折中）

- **创建时**：可选择绑定具体 plan，也可不绑定
- **兑换时**：
  - 如果绑定了 plan → 直接开通该 plan
  - 如果没绑定 plan → 用户从同类型（token/code）的 plan 列表中自选一个开通
- **适用场景**：运营活动发放"Token Plan 兑换券"，让用户自己选 Lite/Standard/Pro

### 建议 C：引入 `subscription_balance`（如果走专用余额路线）

如果产品需要 "只能用于购买套餐、不能用于 API 调用" 的余额类型：

1. 在 `users` 表新增 `subscription_balance` 字段
2. 在购买套餐时优先扣除 `subscription_balance`
3. 在 API 调用扣费时**不**使用 `subscription_balance`

**但此建议不轻推**：会增加余额系统的复杂度，需要修改所有消费扣费链路。除非业务上确有强需求（如监管要求赠送金只能买套餐不能提现/调用），否则建议用路径 B1（自动开通）替代。

### 建议 D：完善数据库约束

```sql
-- vouchers 表添加 type CHECK 约束
ALTER TABLE vouchers ADD CONSTRAINT chk_voucher_type 
  CHECK (type IN ('gift', 'promo', 'referral', 'welcome', 'plan_token', 'plan_code'));

-- 清理 redemption_codes 中的 welcome 数据或统一迁移
```

### 建议 E：Referral 奖励自动入账

当前 referral 奖励先进入 `aff_quota`，用户需手动 Transfer 到 `gift_balance`。建议：
- 邀请奖励直接入账 `gift_balance`，移除 `aff_quota` 中间态
- 简化用户路径，减少一步操作

---

## 5. 实施状态跟踪

| 优先级 | 事项 | 状态 | 备注 |
|--------|------|------|------|
| P0 | **明确 Plan Voucher 设计意图**（选 B1） | ✅ 已完成 | 方案 B1：自动开通套餐，按比例 quota |
| P1 | **修复 Plan Voucher 前端过滤** | ✅ 已完成 | 按 voucher type 过滤 plan 下拉 |
| P2 | **合并 Redemption 到 Voucher** | ✅ 已完成 | `ApplyWelcomeBonus` + `ProcessInvitationCode` 已迁移 |
| P2 | **删除冗余页面/路由/模板** | ✅ 已完成 | redemption、invitation 页面和路由已删除 |
| P2 | **Referral 奖励自动入账** | ✅ 已完成 | 默认自动转入 `gift_balance`（Plan B，不再增加 `balance`） |
| P3 | **数据库约束补齐** | ✅ 已完成 | `vouchers.type` 列已添加 |
| P3 | **实现 Plan Voucher 自动开通套餐** | ✅ 已完成 (2026-06-10) | `redeem()` 中增加 `redeemPlanVoucher()` 分支；quota 按比例缩放；已有 active subscription 则 extend |
| P3 | **引入 subscription_balance**（如业务需要） | ⏸ 搁置 | 暂不实施 |

---

## 6. 附：关键代码位置速查

| 功能 | 文件 | 函数/行号 |
|------|------|----------|
| Voucher 创建 | `internal/routes/vouchers.go` | `create()` |
| Voucher 兑换 | `internal/routes/vouchers.go` | `redeem()` |
| Voucher 列表 | `internal/routes/vouchers.go` | `list()` |
| Referral 生成 | `internal/routes/vouchers.go` | `generate()` |
| Welcome 配置 | `internal/routes/vouchers.go` | `welcome()` / `settings()` |
| Plan 列表 | `internal/routes/vouchers.go` | `listPlans()` |
| Welcome Bonus 应用 | `internal/services/reward.go` | `ApplyWelcomeBonus()` |
| Invitation 处理 | `internal/services/reward.go` | `ProcessInvitationCode()` |
| 余额扣除 | `internal/routes/webhook.go` | `processBillingBatch()` |
| Subscription 开通 | `internal/routes/payment.go` | `activateSubscription()` |
| Admin Voucher 页面 | `web/templates/admin-vouchers.html` | — |
| ~~Admin Redemption 页面~~ | ~~已删除~~ | — |
| ~~Admin Invitations 页面~~ | ~~已删除~~ | — |

---

## 7. 补充分析（2026-06-07 后续讨论）

### 7.1 ~~实际存在三套并行系统~~ ✅ 已完成统一

`schema.go:903` 的设计意图 **"Unified Voucher tables (replaces redemption_codes + invitation_codes)"** 已完全实现：

| 系统 | 数据表 | 当前使用者 | 状态 |
|------|--------|-----------|------|
| **Voucher** | `vouchers` + `voucher_uses` | admin-vouchers、用户兑换、注册奖励、邀请码 | ✅ 活跃 |
| ~~Redemption~~ | ~~`redemption_codes` + `redemption_records`~~ | ~~无~~ | ✅ 已迁移 |
| ~~Invitation~~ | ~~`invitation_codes` + `invitation_records`~~ | ~~无~~ | ✅ 已迁移 |

**迁移详情**：
1. `ApplyWelcomeBonus` → 查询 `vouchers(type='welcome')`，事务内更新 `users` + 插入 `voucher_uses` + 递增 `vouchers.used_count`
2. `ProcessInvitationCode` → 查询 `vouchers(type='referral')`，解析 `reward_config`，双边奖励写入 `voucher_uses`
3. 删除页面：`admin-redemption.html`、`redemption.html`、`admin-invitations.html`、`invitation.html`
4. 删除路由：`internal/routes/redemption.go`、`internal/routes/invitations.go`
5. 删除 handler：`admin_handlers.go` 中相关 handler、`pages.go` 中相关路由、`data.go` 中相关数据方法

### 7.2 Enterprise 用户扣费路径详细分析

设计意图：企业 credit 到 110% 硬阻断，不再扣员工个人余额。

实际代码有三条路径：

| 路径 | 文件/函数 | 是否扣个人 balance | 说明 |
|------|----------|-------------------|------|
| **调用前拦截** | `apikeys.go:750-773` | 不产生调用 | `creditUsed >= 110%` 直接返回 `ENTERPRISE_CREDIT_EXHAUSTED` |
| **单条结算（fallback）** | `webhook.go:720-803` `processRecord` | ❌ 不扣 | 注释明确 `Enterprise keys do NOT fall back to personal balance` |
| **批量结算（主路径）** | `webhook.go:1490-1531` `processBillingBatch` | ⚠️ **会扣** | 有 `Balance fallback`，credit 耗尽后继续扣 `totalBalance` |

**延迟分析**：
- `processStatsQueue` 每 **1 秒** 运行一次
- `processBillingQueue` 每 **2 秒** 运行一次
- 正常 Redis 通信时，usage 产生到扣费延迟约 **1~3 秒**
- Queue 是 Redis List，**无 TTL**，不是 5min/30min（5min 只是 `pricingRatio` 内存缓存的 TTL）

**窗口期**：
- 新请求：credit 到 110% 后**实时被拒**
- in-flight 请求：调用前检查通过、已发往上游的请求，仍会产生 usage 并在 1~3 秒内进入 billing
- **这些 in-flight usage 在 `processBillingBatch` 中可能被扣到个人 balance**（已知不一致，暂保留，见下文）

### 7.3 Batch Billing Balance Fallback（当前行为，暂不修改）

`processBillingBatch` 的 enterprise 分支目前存在个人 balance fallback。按当前讨论结论：**暂不修改代码**，但明确记录行为：

> 当 enterprise credit 耗尽时，`processBillingBatch` 会依次扣除 `gift_balance` 和 `recharge_balance`（合计为 `totalBalance`）作为兜底。这主要用于处理临界点附近的 in-flight 请求费用。若需严格实现"企业额度耗尽后一分钱都不扣个人"，需将 `processBillingBatch` 的 enterprise 分支改为与 `processRecord` 一致：credit 耗尽后 `remaining` 直接记为超额，不扣个人 balance。

### 7.4 Plan Voucher = 自动开通套餐（选定方案 B1）

产品决策：**Plan Voucher 兑换时不充余额，直接开通对应的 subscription。**

实现方式：
1. 创建 `plan_token` / `plan_code` voucher 时**强制选择 plan**（`plan_id` 必填）
2. 用户兑换时，根据 voucher 关联的 `plan_id` 查询 `subscription_plans`
3. 调用类似 `activateSubscription` 的逻辑，创建/续期 `user_subscriptions`

#### Quota 分配问题：$3 优惠券关联 $5 Lite 套餐

**推荐方案：按金额比例缩放 quota，保持完整 duration 和 rate limits**

计算：
- Lite: price = $5, quota = 5,000,000, duration_days = 30
- Voucher amount = $3
- ratio = 3 / 5 = 0.6

开通 subscription 时：
- `quota_total = 5,000,000 * 0.6 = 3,000,000`
- `duration_days = 30`（保持不变）
- `daily_limit = plan.daily_limit * 0.6`（向下取整）
- `rph_limit`, `tph_limit` 等 rate limits 使用 Lite 原始值

**理由**：
- 用户感知是"Lite 套餐的 60% 额度"
- 完整保留 Lite 的时间维度（30 天）和速率限制体验
- 仅减少可用额度，产品语义清晰

**边界情况**：
- 若 voucher amount >= plan price：直接开通**完整套餐**（ratio >= 1 时按 1 处理）
- 若用户已有该 plan 的 active subscription：按续期处理，`quota_total` 和 `duration_days` 均按比例累加

#### 替代方案对比

| 方案 | quota | duration | 适用场景 |
|------|-------|----------|---------|
| **按比例 quota（推荐）** | `plan.quota * ratio` | 完整 | 让用户以较低门槛体验某套餐 |
| **按比例 duration** | 完整 | `plan.duration * ratio` | 强调"买时间"而非"买额度" |
| **强制足额** | 必须 voucher ≥ plan price | 完整 | 最简单，无歧义，但运营不灵活 |
| **等值自定义** | 按单价换算 | 按单价换算 | 弱化了 plan 的品牌和等级属性 |

### 7.5 Referral 自动入账确认

当前行为（Plan B 修复后）：
- **被邀请人奖励**：直接进 `gift_balance`（`reward.go:150-155`）✅
- **邀请人奖励**：默认自动进 `gift_balance`（`reward.go:159-165`，`autoTransfer=true`）✅
- 仅当 `system_settings.invitation_auto_transfer_enabled` 显式设为 `false` 时才进入 `aff_quota`

~~但 `invitation.html` 页面上仍保留 legacy Transfer 区块~~  
**【已删除】** `invitation.html` 和 `admin-invitations.html` 页面已完全移除。

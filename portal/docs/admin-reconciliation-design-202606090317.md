# Admin 客户对账与发票审核系统设计

> **✅ IMPLEMENTED** — `/admin/reconciliation` 已上线，含 ledger / user reconciliation / enterprise reconciliation / top users & enterprises
> 文档编号：`admin-reconciliation-design-202606090317`  
> 创建时间：2026-06-09 03:17 UTC  
> 对应分支：`enterprise-cardification`

---

## 1. 背景与目标

### 1.1 现状痛点

当前系统存在以下与客户财务相关的数据表和操作，但**缺乏统一的 admin 视角**：

| 业务行为 | 数据表 | 当前 Admin 可见性 |
|----------|--------|------------------|
| 用户充值 | `orders` (`product_type='recharge'`) | ❌ 无统一列表 |
| _(旧表已废弃)_ | `recharge_orders` (legacy) | — |
| API 调用消费 | `usage_records` + `balance_transactions` | ❌ 无对账视图 |
| 优惠码兑换 | `voucher_uses` | ✅ admin/vouchers 可见 |
| 企业额度分配 | `enterprise_audit_log` | ❌ 仅企业侧可见 |
| 发票申请 | `invoice_requests` | ❌ 无审核接口/页面 |
| 退款/调账 | — | ❌ 无此功能 |

### 1.2 目标

在 admin 后台新建一个统一的 **「客户对账与发票审核」** 页面（`/admin/reconciliation`），实现：

1. **客户财务全景**：查看任意用户的充值、消费、余额变动、优惠码使用、订阅购买
2. **发票申请审核**：查看所有 `invoice_requests`，支持批准/拒绝/标记已开具
3. **退款与调账**：对异常订单或争议消费进行退款/调账操作（写入 `balance_transactions`）
4. **优惠码消费记录**：明确标记优惠码/赠金消费**不提供发票**
5. **企业对账**：查看企业客户的额度分配、成员消费汇总

---

## 2. 数据流全景调研

### 2.1 用户侧财务行为矩阵

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                         用户财务行为全景                                      │
├─────────────────┬────────────────────────┬──────────────────────────────────┤
│ 行为            │ 数据表                 │ 关键字段/说明                     │
├─────────────────┼────────────────────────┼──────────────────────────────────┤
│ 充值 (Stripe/   │ orders                 │ product_type='recharge',         │
│  Alipay/WeChat) │                        │ status='completed', amount>0     │
│                 ├────────────────────────┼──────────────────────────────────┤
│                 │ users.balance ↑        │ completeOrder() 中更新           │
├─────────────────┼────────────────────────┼──────────────────────────────────┤
│ 订阅购买        │ orders                 │ product_type='subscription'      │
│                 ├────────────────────────┼──────────────────────────────────┤
│                 │ subscription_purchases │ 购买记录                         │
│                 ├────────────────────────┼──────────────────────────────────┤
│                 │ user_subscriptions     │ 激活的订阅配额                   │
├─────────────────┼────────────────────────┼──────────────────────────────────┤
│ API 调用消费    │ usage_records          │ total_cost (upstream cost),      │
│                 │                        │ gift_deduction, balance_         │
│                 │                        │ deduction, credit_deduction      │
│                 ├────────────────────────┼──────────────────────────────────┤
│                 │ balance_transactions   │ transaction_type='usage',        │
│                 │                        │ amount<0, balance_type∈         │
│                 │                        │ {gift,balance,credit}            │
│                 ├────────────────────────┼──────────────────────────────────┤
│                 │ users.balance ↓        │ processBillingBatch/processRecord│
├─────────────────┼────────────────────────┼──────────────────────────────────┤
│ 优惠码兑换      │ voucher_uses           │ consumer_type='redeemer',        │
│                 │                        │ amount>0, balance_before/after   │
│                 ┼────────────────────────┼──────────────────────────────────┤
│                 │ users.gift_balance ↑   │ redeem() 中更新                  │
│                 │                        │ ⚠️ 不产生 balance_transactions   │
│                 │                        │ ⚠️ 当前代码同时更新 users.balance │
│                 │                        │    (重复记账 bug，需修复)          │
├─────────────────┼────────────────────────┼──────────────────────────────────┤
│ 签到奖励        │ checkin_records        │ reward (micro-USD)               │
│                 ├────────────────────────┼──────────────────────────────────┤
│                 │ users.gift_balance ↑   │ 直接增加余额                     │
│                 │                        │ ⚠️ 不产生 balance_transactions   │
│                 │                        │ ⚠️ 当前代码同时更新 users.balance │
│                 │                        │    (重复记账 bug，需修复)          │
├─────────────────┼────────────────────────┼──────────────────────────────────┤
│ 邀请奖励        │ voucher_uses           │ consumer_type='inviter'/'invitee'│
│                 │                        │ amount>0                         │
│                 ├────────────────────────┼──────────────────────────────────┤
│                 │ users.gift_balance ↑   │ ProcessInvitationCode()          │
│                 │                        │ ⚠️ 不产生 balance_transactions   │
│                 │                        │ ⚠️ 当前代码同时更新 users.balance │
│                 │                        │    (重复记账 bug，需修复)          │
├─────────────────┼────────────────────────┼──────────────────────────────────┤
│ 发票申请        │ invoice_requests       │ amount, status='pending',        │
│                 │                        │ delivery_method, profile_id      │
│                 ├────────────────────────┼──────────────────────────────────┤
│                 │ invoice_profiles       │ 抬头信息                         │
│                 ├────────────────────────┼──────────────────────────────────┤
│                 │ delivery_addresses     │ 邮寄地址（纸质发票）             │
└─────────────────┴────────────────────────┴──────────────────────────────────┘
```

### 2.2 余额扣除优先级链

```
general / subscription 用户:
  gift_balance → balance

enterprise 用户:
  gift_balance → enterprise_credit (110% buffer) → HARD STOP
  ⚠️ batch/single 路径统一：超过 110% 后不再扣个人 balance
```

### 2.3 企业侧财务行为矩阵

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                         企业财务行为全景                                      │
├─────────────────┬────────────────────────┬──────────────────────────────────┤
│ 行为            │ 数据表                 │ 关键字段/说明                     │
├─────────────────┼────────────────────────┼──────────────────────────────────┤
│ 额度充值        │ enterprises            │ credit_total ↑, quota_total ↑    │
│                 ├────────────────────────┼──────────────────────────────────┤
│                 │ enterprise_audit_log  │ change_type='credit_topup'       │
│                 │                        │ / 'quota_topup'                  │
├─────────────────┼────────────────────────┼──────────────────────────────────┤
│ 成员额度分配    │ enterprise_members     │ quota_limit                      │
│                 ├────────────────────────┼──────────────────────────────────┤
│                 │ users.quota_total ↑    │ topupMemberQuota()               │
│                 ├────────────────────────┼──────────────────────────────────┤
│                 │ enterprises.quota_used ↑│ 从企业池划拨                     │
├─────────────────┼────────────────────────┼──────────────────────────────────┤
│ 成员 API 消费   │ usage_records          │ user_id, enterprise_id(间接)     │
│                 ├────────────────────────┼──────────────────────────────────┤
│                 │ enterprises.credit_used↑│ processBillingBatch()            │
│                 ├────────────────────────┼──────────────────────────────────┤
│                 │ balance_transactions   │ balance_type='credit'            │
│                 │                        │ (balance_before/after=0)         │
├─────────────────┼────────────────────────┼──────────────────────────────────┤
│ 月度重置        │ enterprises            │ credit_used=0, quota_used=0      │
│                 ├────────────────────────┼──────────────────────────────────┤
│                 │ enterprise_audit_log  │ change_type='monthly_reset'      │
└─────────────────┴────────────────────────┴──────────────────────────────────┘
```

---

## 3. 关键发现：数据缺口 (Gaps)

### ~~Gap 1：优惠码/签到/邀请奖励 重复记账 + 缺失 ledger~~ ✅ 已修复

**原问题**：

1. **重复记账 Bug**：代码在发放赠金时同时增加 `gift_balance` 和 `balance`：
   ```sql
   UPDATE users SET gift_balance = gift_balance + $1, balance = balance + $1
   ```

2. **缺失 ledger**：没有 `balance_transactions` 记录。

**修复完成**：
- ✅ 已改为 **Plan B 余额模型**：`balance` = 真实资金，`gift_balance` = 赠金（互斥）
- ✅ 发放赠金时**只增加 `gift_balance`**，不再增加 `balance`
- ✅ 同步插入 `balance_transactions`：
  ```sql
  INSERT INTO balance_transactions
    (user_id, transaction_type, amount, balance_type, balance_before, balance_after, reference_id, description)
  VALUES
    ($1, 'voucher', $2, 'gift', $3, $4, $5, 'Voucher redemption: WELCOME068')
  ```

### ~~Gap 2：充值订单完成时 没有进入 balance_transactions~~ ✅ 已修复

`completeOrder()` 更新 `users.balance += amount`，同时记录 `balance_transactions`。

**修复完成**：在 `completeOrder()` 中同步插入：

```sql
INSERT INTO balance_transactions
  (user_id, transaction_type, amount, balance_type, balance_before, balance_after, reference_id, description)
VALUES
  ($1, 'recharge', $2, 'balance', $3, $4, $5, 'Recharge via Stripe')
```

> `balance_type='balance'` (not `'recharge'`) because `recharge_balance` field is deprecated.

### Gap 3：企业 credit 扣除的 balance_transactions 记录不完整

`balance_transactions` 中 `balance_type='credit'` 的记录的 `balance_before/after` 为 0，因为 credit 存储在 `enterprises` 表而非 `users` 表。

**修复建议**：企业 credit 变动应单独记录在 `enterprise_audit_log`（已有），但 `balance_transactions` 中的记录应补充 `enterprise_id` 并移除 `balance_before/after`（或填入企业 credit 余额）。

### Gap 4：发票申请没有消费校验

`createRequest()` 只检查 `amount > 0`，不验证用户是否有实际消费。

**修复建议**：在 `createRequest()` 中添加基于 `balance_transactions` 的校验：

```sql
-- 可开票金额 = 真实资金消费总额 - 已申请/已开票金额
WITH 
real_fund_consumption AS (
  SELECT COALESCE(SUM(ABS(amount)), 0) AS total
  FROM balance_transactions
  WHERE user_id = $1::uuid
    AND transaction_type = 'usage'
    AND balance_type = 'balance'
),
already_requested AS (
  SELECT COALESCE(SUM(amount), 0) AS total
  FROM invoice_requests
  WHERE user_id = $1::uuid
    AND status IN ('pending', 'processing', 'completed')
)
SELECT 
  (SELECT total FROM real_fund_consumption)
  - (SELECT total FROM already_requested) AS eligible_amount;
```

发票申请金额 ≤ `eligible_amount`，且 ≥ $10 USD（或等值货币）。

### Gap 5：没有退款/调账功能

当前系统**完全没有退款能力**。如果用户争议消费或订单异常，admin 无法通过系统操作退回余额。

**修复建议**：新增 `refund` / `adjustment` 类型的 `balance_transactions` + admin API。

---

## 4. Admin 对账与发票审核页面设计

### 4.1 页面定位

- **路由**：`/admin/reconciliation`
- **导航位置**：`layout.html` 侧边栏「财务管理」分组下（与 admin-vouchers 同级）
- **权限**：`super_admin` + `system_admin`

### 4.2 页面结构（Tab 式）

```
┌─────────────────────────────────────────────────────────────────────────────┐
│ 客户对账与发票审核                                                           │
├─────────────────────────────────────────────────────────────────────────────┤
│ [发票审核] [客户对账] [充值记录] [消费明细] [退款调账] [企业对账]           │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│ Tab 1: 发票审核 (Invoice Requests)                                          │
│ ─────────────────────────────────────────────────────────────────────────── │
│ 筛选: [全部] [待处理] [处理中] [已开具] [已拒绝]                            │
│                                                                             │
│ ┌────────┬──────────┬────────┬──────────┬──────────┬──────────┬──────────┐ │
│ │ 用户   │ 抬头     │ 金额   │ 配送方式 │ 状态     │ 申请时间 │ 操作     │ │
│ ├────────┼──────────┼────────┼──────────┼──────────┼──────────┼──────────┤ │
│ │ User A │ 公司A    │ $100   │ 电子     │ pending  │ 06-08    │ [审核]   │ │
│ │ User B │ 个人B    │ $50    │ 纸质     │ pending  │ 06-08    │ [审核]   │ │
│ └────────┴──────────┴────────┴──────────┴──────────┴──────────┴──────────┘ │
│                                                                             │
│ 点击 [审核] 打开 drawer：                                                   │
│   - 显示用户累计消费、已开发票金额、本次申请金额                             │
│   - 消费校验：如果申请金额 > 可开票金额，提示警告                            │
│   - 操作按钮：[批准并开具] [拒绝]                                           │
│   - 拒绝时填写原因                                                           │
│                                                                             │
├─────────────────────────────────────────────────────────────────────────────┤
│ Tab 2: 客户对账 (Customer Reconciliation)                                   │
│ ─────────────────────────────────────────────────────────────────────────── │
│ 搜索用户: [____________] [查询]                                              │
│                                                                             │
│ 用户: Business Test User (biz@example.com)                                   │
│ ┌─────────────────────────────────────────────────────────────────────────┐ │
│ │ 余额快照                    │ 消费统计              │ 充值统计          │ │
│ │ ─────────────────────────── │ ───────────────────── │ ───────────────── │ │
│ │ gift_balance:   $0.68       │ 本月消费: $12.50      │ 本月充值: $50.00  │ │
│ │ balance:        $49.32      │ 累计消费: $156.30     │ 累计充值: $200.00 │ │
│ │ **总余额: $50.00**          │ 优惠码消费: $20.00 ⚠️ │ 优惠码获赠: $5.00 │ │
│ │                             │   (不提供发票)        │                   │ │
│ └─────────────────────────────────────────────────────────────────────────┘ │
│                                                                             │
│ 财务流水 (Unified Ledger):                                                  │
│ ┌──────────┬──────────────┬─────────┬─────────────┬──────────┬────────────┐ │
│ │ 时间     │ 类型         │ 金额    │ 余额类型    │ 余额变动 │ 关联ID     │ │
│ ├──────────┼──────────────┼─────────┼─────────────┼──────────┼────────────┤ │
│ │ 06-09    │ usage        │ -$0.50  │ gift        │ $0.68→$0.18│ trace_123│ │
│ │ 06-08    │ recharge     │ +$50.00 │ balance     │ $0→$50   │ order_456  │ │
│ │ 06-08    │ voucher      │ +$0.68  │ gift        │ $0→$0.68 │ voucher_789│ │
│ │ 06-07    │ usage        │ -$2.00  │ balance     │ $2→$0    │ trace_abc  │ │
│ └──────────┴──────────────┴─────────┴─────────────┴──────────┴────────────┘ │
│                                                                             │
│ ⚠️ 说明: 优惠码消费(voucher)和签到奖励(checkin)产生的消费不提供发票。        │
│          可开票金额 = 累计充值 + 累计消费 - 优惠码消费                       │
│                                                                             │
├─────────────────────────────────────────────────────────────────────────────┤
│ Tab 3: 充值记录 (Recharge Records)                                          │
│ ─────────────────────────────────────────────────────────────────────────── │
│ 列表展示 orders + recharge_orders，支持按用户/时间/支付方式筛选             │
│                                                                             │
├─────────────────────────────────────────────────────────────────────────────┤
│ Tab 4: 消费明细 (Usage Details)                                             │
│ ─────────────────────────────────────────────────────────────────────────── │
│ 列表展示 usage_records + balance_transactions，支持按用户/时间/模型筛选     │
│                                                                             │
├─────────────────────────────────────────────────────────────────────────────┤
│ Tab 5: 退款调账 (Refunds & Adjustments)                                     │
│ ─────────────────────────────────────────────────────────────────────────── │
│ 搜索用户 → 显示余额 → [退款] [调增] [调减]                                  │
│ 操作后插入 balance_transactions (transaction_type='refund'/'adjustment')    │
│ 需要填写原因，审计日志记录                                                     │
│                                                                             │
├─────────────────────────────────────────────────────────────────────────────┤
│ Tab 6: 企业对账 (Enterprise Reconciliation)                                 │
│ ─────────────────────────────────────────────────────────────────────────── │
│ 搜索企业 → 显示企业额度/信用/成员消费汇总                                    │
│ 成员消费明细 + 额度分配历史                                                  │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

### 4.3 后端 API 设计

```
# 发票审核
GET    /api/admin/invoice-requests          # 列表 (status, user_id, page, limit)
POST   /api/admin/invoice-requests/:id/approve  # 批准 → status='processing'
POST   /api/admin/invoice-requests/:id/reject   # 拒绝 → status='rejected' + reason
POST   /api/admin/invoice-requests/:id/complete # 标记已开具 → status='completed' + invoice_number

# 客户对账
GET    /api/admin/reconciliation/user/:id   # 用户财务全景 (余额+统计+流水)
GET    /api/admin/reconciliation/ledger     # 统一流水 (user_id, type, date range)

# 充值记录
GET    /api/admin/reconciliation/recharges  # 充值订单列表

# 消费明细
GET    /api/admin/reconciliation/usage      # usage_records + balance_transactions

# 退款调账
POST   /api/admin/reconciliation/refund     # 退款
  Body: {user_id, amount, reason, reference_type, reference_id}
POST   /api/admin/reconciliation/adjustment # 调账
  Body: {user_id, amount, reason, adjustment_type}

# 企业对账
GET    /api/admin/reconciliation/enterprise/:id  # 企业财务全景
GET    /api/admin/reconciliation/enterprise/:id/members  # 成员消费汇总
```

### 4.4 统一流水 SQL（核心查询）

```sql
-- 单个用户的完整财务流水（时间倒序）
WITH unified_ledger AS (
  -- 1. 余额消费扣减 (balance_transactions)
  SELECT
    bt.id::text AS record_id,
    bt.user_id,
    bt.created_at,
    bt.transaction_type AS activity_type,
    bt.amount,
    bt.balance_type,
    bt.balance_before,
    bt.balance_after,
    bt.reference_id,
    bt.description,
    NULL::text AS status,
    NULL::text AS product_name
  FROM balance_transactions bt
  WHERE bt.user_id = $1::uuid

  UNION ALL

  -- 2. 充值订单 (orders)
  SELECT
    o.id::text AS record_id,
    o.user_id,
    o.created_at,
    'recharge' AS activity_type,
    o.amount AS amount,
    'recharge' AS balance_type,
    NULL::bigint AS balance_before,
    NULL::bigint AS balance_after,
    o.order_id AS reference_id,
    o.product_name AS description,
    o.status,
    o.product_name
  FROM orders o
  WHERE o.user_id = $1::uuid AND o.status = 'completed'
    AND o.deleted_at IS NULL

  UNION ALL

  -- 3. 优惠码兑换 (voucher_uses)
  SELECT
    vu.id::text AS record_id,
    vu.user_id,
    vu.created_at,
    'voucher_redeem' AS activity_type,
    vu.amount AS amount,
    'gift' AS balance_type,
    vu.balance_before,
    vu.balance_after,
    v.code AS reference_id,
    v.name AS description,
    NULL::text AS status,
    v.type AS product_name
  FROM voucher_uses vu
  JOIN vouchers v ON vu.voucher_id = v.id
  WHERE vu.user_id = $1::uuid

  UNION ALL

  -- 4. 签到奖励 (checkin_records)
  SELECT
    cr.id::text AS record_id,
    cr.user_id,
    cr.created_at::timestamp AS created_at,
    'checkin' AS activity_type,
    cr.reward AS amount,
    'gift' AS balance_type,
    NULL::bigint AS balance_before,
    NULL::bigint AS balance_after,
    NULL::text AS reference_id,
    'Daily check-in reward' AS description,
    NULL::text AS status,
    NULL::text AS product_name
  FROM checkin_records cr
  WHERE cr.user_id = $1::uuid

  UNION ALL

  -- 5. 发票申请 (invoice_requests)
  SELECT
    ir.id::text AS record_id,
    ir.user_id,
    ir.created_at,
    'invoice_request' AS activity_type,
    -ir.amount AS amount,  -- 负值表示"申请开票"（占用可开票额度）
    'invoice' AS balance_type,
    NULL::bigint AS balance_before,
    NULL::bigint AS balance_after,
    ir.order_id AS reference_id,
    ir.title AS description,
    ir.status,
    NULL::text AS product_name
  FROM invoice_requests ir
  WHERE ir.user_id = $1::uuid AND ir.deleted_at IS NULL
)
SELECT * FROM unified_ledger
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;
```

### 4.5 可开票金额计算

```sql
-- 可开票金额 = 真实资金消费总额 - 已申请/已开票金额
WITH
real_fund_consumption AS (
  SELECT COALESCE(SUM(ABS(amount)), 0) AS total
  FROM balance_transactions
  WHERE user_id = $1::uuid
    AND transaction_type = 'usage'
    AND balance_type = 'balance'
),
invoice_requested AS (
  SELECT COALESCE(SUM(amount), 0) AS total
  FROM invoice_requests
  WHERE user_id = $1::uuid
    AND status IN ('pending', 'processing', 'completed')
    AND deleted_at IS NULL
)
SELECT
  (SELECT total FROM real_fund_consumption) AS consumption_amount,
  (SELECT total FROM invoice_requested) AS requested_amount,
  (SELECT total FROM real_fund_consumption)
    - (SELECT total FROM invoice_requested) AS invoice_eligible_amount;
```

> **规则**：只有 `balance_type='balance'` 的消费（真实资金）可以开票。赠金消费（`balance_type='gift'`）和企业 credit 消费（`balance_type='credit'`）**不提供发票**。
>
> **最小开票金额**：$10 USD（或等值当地货币）。

---

## 5. 退款调账设计

### 5.1 退款 (Refund)

**场景**：用户充值后要求退款，或订单异常需退回。

**操作**：
1. Admin 在页面上选择用户、输入退款金额、填写原因
2. 系统创建 `balance_transactions`：
   ```sql
   INSERT INTO balance_transactions
     (user_id, transaction_type, amount, balance_type, balance_before, balance_after, reference_id, description)
   VALUES
     ($1, 'refund', -$2, 'recharge', $3, $4, $5, 'Admin refund: 用户争议')
   ```
3. 同步扣减 `users.balance`（`recharge_balance` 已废弃）
4. 如果关联订单，更新 `orders.status = 'refunded'`
5. 审计日志记录

### 5.2 调账 (Adjustment)

**场景**：系统错误导致多扣费，admin 手动补偿。

**操作**：
1. Admin 输入补偿金额、选择余额类型（gift/recharge/balance）、填写原因
2. 系统创建 `balance_transactions`：
   ```sql
   INSERT INTO balance_transactions
     (user_id, transaction_type, amount, balance_type, balance_before, balance_after, reference_id, description)
   VALUES
     ($1, 'adjustment', $2, 'gift', $3, $4, $5, 'Admin adjustment: 系统多扣补偿')
   ```
3. 同步增加 `users.gift_balance`（或对应余额类型）
4. 审计日志记录

---

## 6. 实施优先级

> 详见 `docs/billing-reconciliation-implementation-plan-202606091449.md`，本文档聚焦于 admin 页面/API 设计，底层数据修复在该文档中规划。

### Phase 1：底层数据修复 ✅ 已完成

| # | 任务 | 文件 | 状态 |
|---|------|------|------|
| 1 | 修复 `balance`/`gift_balance` 重复记账 bug | `internal/services/reward.go`, `vouchers.go`, `checkin.go` | ✅ 已完成 |
| 2 | 废弃 `recharge_balance` + `recharge_orders` | `internal/routes/webhook.go`, `billing.go`, `db/schema.go` | ✅ 已完成 |
| 3 | 企业 batch billing 去掉个人 balance 兜底 | `internal/routes/webhook.go` | ✅ 已完成 |
| 4 | 企业计费周期改自然月 | `internal/routes/enterprise.go` | ✅ 已完成（按自然月重置） |
| 5 | `balance_transactions` BIGINT 迁移 + 补录 | `internal/db/schema.go` + multiple handlers | ✅ 已完成 |

### Phase 2：发票审核 + 消费校验 ✅ 已完成

| # | 任务 | 文件 | 状态 |
|---|------|------|------|
| 6 | 发票申请消费校验 + $10 最小金额 | `internal/routes/invoices.go` | ✅ 已完成 |
| 7 | 新增 admin API：`GET /api/admin/invoice-requests` | `internal/routes/admin_reconciliation.go` | ✅ 已完成 |
| 8 | 新增 admin API：`POST /approve`、`/reject`、`/complete` | `internal/routes/admin_reconciliation.go` | ✅ 已完成 |
| 9 | 新增 admin 页面 Tab 1（发票审核） | `web/templates/admin-reconciliation.html` | ✅ 已完成 |

### Phase 3：客户对账 + 企业对账 ✅ 已完成

| # | 任务 | 文件 | 状态 |
|---|------|------|------|
| 10 | 新增 `GET /api/admin/reconciliation/user/:id` API | `internal/routes/admin_reconciliation.go` | ✅ 已完成 |
| 11 | 新增 `GET /api/admin/reconciliation/ledger` API | `internal/routes/admin_reconciliation.go` | ✅ 已完成 |
| 12 | 完善 Tab 2（客户对账）UI | `web/templates/admin-reconciliation.html` | ✅ 已完成 |
| 13 | 企业对账 API + UI | `internal/routes/admin_reconciliation.go` + template | ✅ 已完成 |

### Phase 4：退款调账 ⏳ 待开发

| # | 任务 | 文件 | 状态 |
|---|------|------|------|
| 14 | 退款 API + UI | `internal/routes/admin.go` + template | ⏳ 待开发 |
| 15 | 调账 API + UI | `internal/routes/admin.go` + template | ⏳ 待开发 |

---

## 7. 关键代码位置速查

| 功能 | 文件 | 函数/行号 |
|------|------|----------|
| 发票申请创建 | `internal/routes/invoices.go` | `createRequest()` ~345 |
| 发票申请列表 | `internal/routes/invoices.go` | `listRequests()` ~304 |
| 余额扣除 (batch) | `internal/routes/webhook.go` | `processBillingBatch()` ~1406 |
| 余额扣除 (single) | `internal/routes/webhook.go` | `processRecord()` ~720 |
| 充值订单完成 | `internal/routes/payment.go` | `completeOrder()` |
| 优惠码兑换 | `internal/routes/vouchers.go` | `redeem()` ~54 |
| Welcome Bonus | `internal/services/reward.go` | `ApplyWelcomeBonus()` ~30 |
| Invitation | `internal/services/reward.go` | `ProcessInvitationCode()` ~85 |
| 用户余额查询 | `internal/routes/billing.go` | `getBalance()` |
| 企业 Billing | `internal/routes/enterprise.go` | `getEnterpriseBillingDetails()` |
| 企业 Credit Log | `internal/routes/enterprise.go` | 相关 handler |
| usage_records 插入 | `internal/routes/webhook.go` | `processStats()` ~1257 |
| balance_transactions 插入 | `internal/routes/webhook.go` | ~964, 972, 980, 1642 |

---

## 8. 决策记录

| # | 决策 | 说明 |
|---|------|------|
| 1 | 优惠码消费**不提供发票** | 赠金属性，已在文档中明确标注 |
| 2 | 统一流水采用 **SQL UNION** 而非新表 | 避免数据迁移，通过 CTE/View 实时聚合 |
| 3 | 退款调账写入 `balance_transactions` | 利用现有 ledger 表，无需新表 |
| 4 | 发票审核状态机 | `pending` → `processing` (approve) → `completed` (issue) / `rejected` |
| 5 | 消费校验公式 | 可开票 = `balance_transactions` 中 `balance_type='balance'` 的累计消费 − 已申请 |
| 6 | 企业 credit 对账独立 Tab | 企业逻辑与普通用户差异大，单独展示更清晰 |
| 7 | `recharge_orders` 表废弃 | 统一使用 `orders` (`product_type='recharge'`) |
| 8 | `recharge_balance` 字段废弃 | 个人资金统一存入 `balance`，`gift_balance` 仅存赠金 |
| 9 | 余额模型 Plan B | `balance` 与 `gift_balance` 互斥，总余额 = `balance + gift_balance` (计算值) |
| 10 | 企业计费周期 | 自然月（calendar month），首月/末月按实际天数比例 |
| 11 | 企业超额消费 | 110% credit cap 后 **HARD STOP**，不扣个人 balance |
| 12 | 最小开票金额 | $10 USD（或等值当地货币） |
| 13 | `balance_transactions` 迁移 | `amount`/`balance_before`/`balance_after` → BIGINT (micro-USD) |
| 14 | `total_cost` 含义 | `usage_records.total_cost` = 上游成本，非用户实际支付价 |

---

## 9. 下一步行动

1. ✅ **底层数据修复**：`balance`/`gift_balance` 重复记账修复、`recharge_balance` 废弃、企业计费周期调整均已完成。
2. ✅ **Admin 对账页面**：发票审核、客户对账、企业对账三大 Tab 已上线。
3. ⏳ **退款调账功能**：后续单独开发，不在当前阶段实施。
4. ⏳ **充值记录 / 消费明细 Tab**：可在未来版本中追加到 admin/reconciliation 页面。

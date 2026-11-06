# Enterprise Billing: Prepaid/Postpaid 双池、月结账单与开票 — 设计

Status: ACTIVE
Date: 2026-11-01
Scope: ai-portal-go（Portal）

## 背景

- 企业客户分两类资金性质：**prepaid**（在线充值或公对公转账后 system admin 手动充入 `prepaid_balance`）和 **postpaid**（system admin 授信 `credit_total`，按 `credit_used` 消费，月初清零）。
- 现状缺口：
  1. 没有月结账单：月初 reset 只清计数器，`credit_used` 清零后上月消费（如 Aynbay 2026-10 的 $95.45）除成员快照 reason 字符串外无结构化留存。
  2. 企业可开票金额恒为 0：`enterpriseInvoiceEligibility` 只认 `credit_topup` 累计，授信企业（无 topup 记录）与消费口径都对不上。
  3. 无月初推送账单到 EA 的动作。
- 消费扣费路径（`webhook_usage.go` / `webhook_billing.go` / `webhook_preconsume.go`）是裸 UPDATE，不写消费流水——月结必须靠 reset 时抓计数器快照。

## 资金模型（双池）

| 池 | 字段 | 性质 | 清零 |
|---|---|---|---|
| postpaid（授信） | `credit_total` / `credit_used` | system admin 授信额度，先消费后结算 | 每月 1 日 reset |
| prepaid（预存） | `prepaid_balance` | 充值钱包（在线充值 / 公对公转账后 admin 充入） | 永不清零，余额结转 |

- 扣费顺序：先 prepaid 后 credit（已有，prepaid-first）。
- 展示：enterprise 卡片**合并显示总可用**（`prepaid_balance + credit_total - credit_used`），明细**分两行**（prepaid 一行、credit 一行）。
- `prepaid_topup` / `credit_topup` / `quota_topup` 走 admin Topup 接口时已有审计记录。

## 月结账单（P1）

新表 `enterprise_statements`：

| 列 | 说明 |
|---|---|
| id uuid pk | |
| enterprise_id uuid fk | |
| period varchar(7) | `'2026-10'`（账单月） |
| credit_used_micro bigint | 上月 postpaid 消费（reset 前 `credit_used`） |
| quota_used bigint | 上月 token 消费 |
| prepaid_topup_micro bigint | 上月 prepaid 充值合计（审计 `prepaid_topup` 当月） |
| prepaid_balance_end_micro bigint | 上月期末 prepaid 余额（reset 时刻值，供链式参考） |
| created_at | |

生成时机：`ResetMonthlyBilling` 的清零 UPDATE **之前**，同事务或紧前插入上月 statement，然后清零。幂等：同一 enterprise + period 唯一（partial unique index），重复跑 ON CONFLICT DO NOTHING。

通知：statement 生成后 `NotifyEnterpriseAdmins`（in-app + email），新通知类型 `enterprise_monthly_statement`，注册进 `preferenceTypesForRole`（enterprise_admin 组）。文案含期间与金额，链接 `/enterprise/billing`。

历史回填：Aynbay 2026-10（credit $95.45 / 95,448,830µ$、quota 125,209,910）与 Intel 2026-10 手工补录，使 EA 立即可申请开票。

## 开票模型（P2）

`invoice_requests` 扩展：`pool varchar(10)`（`'postpaid' | 'prepaid'`，默认 `'postpaid'` 兼容存量）、`period varchar(7)`（postpaid 开票对应账单月，可空）。

### 可开票金额（分池计算，合并展示）

- **postpaid 可开票** = Σ（未开票 statement 的 credit_used） − 该企业 postpaid 池 pending/processing/completed 开票申请。
  - statement 是否已开票由 `invoice_requests(enterprise_id, pool='postpaid', period)` 的 completed 集合反查，不重写 statement 行。
- **prepaid 可开票** = min( Σprepaid_topup − prepaid池已开票completed, Σprepaid_topup − prepaid_balance ) − prepaid池 pending/processing。
  - 口径："存进来的钱里已经花掉的部分，扣掉已经开过的票"。退款（prepaid 加回）自然抵消，不需要单独处理。
- 页面展示：总可开票 = 两池之和；明细两行分池列出（附口径说明 tooltip）。

### prepaid 的两种开票模式

- **模式 A：充值即开票**。admin Topup prepaid 时勾选 `issue_invoice` + 填写发票抬头/税号 → 生成 `invoice_requests`（pool='prepaid'，status 直接 `completed`，invoice_number 生成，issued_at=now）。该笔充值金额计入"prepaid 已开票"，不再进入事后可开票池。适用于客户转账时已要求发票的场景。
- **模式 B：事后按消费申请**。EA 在 enterprise/billing 发起申请，选择池（postpaid 选账单月 / prepaid 按消费余额），走现有 admin 审批流（pending → processing → completed / rejected）。

### 个人开票

`personalInvoiceEligibility` 不动（个人无企业池概念）。

## 页面改动

- **enterprise/billing**：交易历史加 `monthly_statement` 条目（从 statements 表渲染：期间、postpaid 消费、quota、prepaid 充值）；发票申请区分池；可开票金额分池两行。
- **enterprise（EA dashboard）/ admin enterprise 卡片**：合并可用 + 两行明细（prepaid used/total、credit used/total 已有基础上补齐 prepaid 行）。
- **admin 企业 Topup**：prepaid 充值块加"同步开票"复选框 + 抬头/税号字段（选中时必填）。

## 实施步骤

1. Migration 0023：`enterprise_statements` 表 + `invoice_requests.pool/period` 列；`internal/db/schema.go` 同步。
2. `ResetMonthlyBilling`：清零前写 statement + 通知（新 i18n key `notif.enterprise_monthly_statement.*` + preferenceTypesForRole 注册）。
3. `enterpriseInvoiceEligibility` 重写为双池公式；`getEligibility` 返回分池明细。
4. billing 页面前端：statement 交易行、分池可开票、申请开票选池。
5. admin Topup：prepaid 充值同步开票（模式 A）。
6. 回填 Aynbay / Intel 2026-10 statement。
7. 测试 + i18n + 部署。

## 不做 / 边界

- 不给消费路径加逐笔审计流水（量太大，月结快照足够）。
- 不做 statement 的 PDF 导出（后续需要再加）。
- 退款不改变开票口径（按"净消费"理解）。

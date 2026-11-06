# 上线前功能完整性审计报告与修复方案

**日期**：2026-07-13 06:20 UTC
**范围**：ai-portal-go 全站页面（user / system admin / super admin / enterprise admin）+ ai-proxy-go 消费侧
**方法**：9 个并行只读审计子任务，对每个页面交互元素做「模板 → 端点 → handler → 消费链路」四级追踪
**触发**：profile 备用邮箱"只落库不动作" bug（已修）后，担心同类问题残留

## 问题分类定义

- **A 断链**：前端调用的端点不存在，或端点存在但前端从未调用
- **B 空转**：handler 是 stub / 只 log / 返回 success 但不动作
- **C 死写**：写了 DB 但无任何读取消费方（备用邮箱式 bug）
- **D 未接线**：模板有按钮/表单但没有 handler
- **E 半成品**：单向实现或流程中途断裂

---

## P0：上线阻断（资金 / 账号安全）

### P0-1 用户/成员/企业"禁用"全部不生效 [C]

**状态：✅ 已修复（2026-07-13，commit 3c4e9b3，已 push+sync）**。实施与方案基本一致，偏差：登录门禁改为密码校验后分支判断（避免账号枚举 + 给出"账号已停用"文案）；`users.status` 可 NULL，统一用 `(status IS NULL OR status != 'suspended')`；企业门禁在 Go 侧判断 `e.status`（LEFT JOIN 不过滤，否则无法区分"无企业"与"企业被停"）；中间件文案用英文 "Account suspended"（CI i18n 审计禁 Go 硬编码中文），中文文案由登录入口 i18n 送达。新增 `internal/routes/suspension_gate_test.go` 6 个 DB 级用例全绿。

**证据**：
- admin/users Disable → `POST /api/admin/users/:id/toggle-status` 只更新 `users.status`（`internal/routes/admin_users.go:327`）
- 企业成员启停 → `POST /api/enterprise/members/:id/toggle` 同样只写 `users.status`（`enterprise_members.go:287-289`）
- 企业 suspend → 只写 `enterprises.status='suspended'`（`enterprise_common.go:886-888`）
- 消费侧全部不查：登录 `internal/routes/auth.go:276`、`internal/web/auth.go:88-91` 只过滤 `deleted_at`；JWT 中间件 `internal/middleware/auth.go:75` 只查 `deleted_at`；proxy key 校验 `apikeys_validate.go:114-145` 只查 `api_keys.status`
- 附加：admin 删除用户只软删 users 行（`admin_users.go:162-164`），`validateKey` 的 `JOIN users u` **不带 `u.deleted_at IS NULL`**（`apikeys_validate.go:115`）——已删用户的 key 仍有效

**方案**：让 `status` 成为真实门禁，而不是展示字段。

1. `apikeys_validate.go` 的 validateKey 查询：JOIN users 加 `AND u.deleted_at IS NULL AND u.status != 'suspended'`；LEFT JOIN enterprises 分支加 `AND (e.status IS NULL OR e.status = 'active')`，命中即拒绝（返回 key disabled 语义）。
2. JWT 中间件 `middleware/auth.go`：校验用户时补 `status` 检查，suspended 返回 401/403。
3. Web 登录 `routes/auth.go:276` + `web/auth.go:89`：WHERE 加 `AND status != 'suspended'`，报错文案"账号已停用"。
4. admin 删除用户：同事务软删其 `api_keys`（`status='disabled'`）并调 `invalidateValidateCache`。
5. 企业 suspend：同事务将其成员企业 key 置 disabled + 失效缓存（参考近期"组 disable 级联停 key"的实现模式）；恢复不自动恢复 key。
6. 测试：为 validateKey 增加 suspended 用户/企业/已删用户的拒绝用例；为 toggle 增加端到端用例。

**风险**：proxy 有 5 分钟 validate 缓存，禁用后最长 5 分钟生效——与现有 key 禁用行为一致，可接受；toggle 时必须调 `invalidateValidateCache` 才能即时生效，需确认两个 toggle 都调用了（企业成员 toggle 目前未调，见 P1）。

### P0-2 /super/payment 网关配置完全不通 [C+A+E]

**状态：✅ 已修复（2026-07-13，commit a48117c，已 push+sync，方案 A）**。实施要点：模板四表单改 `fetch PUT /api/admin/payment/gateways/:gateway` + `/test`，字段名对齐 `pkg/payment/gateway.go` JSON tag（注意 Alipay 用 `client_id` 不是 `app_id`）；`updateGateway` 改 upsert + 网关白名单，敏感字段 `crypto.Encrypt`（cfg.Auth.EncryptionKey）入库、掩码回传不覆盖原密文；支付链路 7 处在 `CreateGateway` 前统一 `DecryptGatewayConfig`（不可解密原样透传兼容明文）；`GetPaymentConfig` 改读 `payment_gateways` 掩码回显；四个旧 `payment_config` handler 与 `/super/payment/:gw` 路由删除（表 0 行无迁移负担）。新增 `pkg/payment/config_secrets.go` + `payment_gateway_test.go` 5 用例（含模拟模式 createPayment 端到端）全绿。

**实施中发现的额外事实**：①`payment_gateways` 表实际 schema **缺 `gateway` 列**（schema.go 建表只有 name/provider），即改动前整条支付链路 `WHERE gateway=...` 在真库必然报错——已通过 schema.go ALTER + 四行禁用种子修复（启动 InitSchema 自动应用）；②**新增 P2 项**：`createPayment`（payment.go:200）/`cancelOrder`（orders.go:442）的 `WHERE (id=$1::uuid OR order_id=$1::text)` 对 `ORD-xxx` 订单号会因 uuid 强转报错——经核查所有前端支付/取消入口均传 UUID（`order.id`/`order_uuid`），当前无触发路径，留作加固（改 `id::text=$1 OR order_id=$1`）；③Stripe 表单只有 secret_key/webhook_secret 两个密钥字段（hosted Checkout 不需要 publishable key，旧表单该字段是死字段），但 `CreateSession` 依赖的 `success_url`/`cancel_url` 表单未采集——已在 `createPayment` 注入默认值（`APP_BASE_URL/billing?payment=success|cancelled`，已配置则保留），否则 Stripe 建单必失败（commit 见 git log "Stripe success/cancel URL 默认值"）。

**证据**：
- 四个表单 handler 只写 `payment_config` 表（`internal/web/admin_handler_common.go:1168,1214,1256,1298`）
- 支付链路只读 `payment_gateways` 表：`internal/routes/payment.go:217`（createPayment）、`:350,396,442,487`（四家 webhook）、`:622`（回调）、`orders.go:255`（GatewayEnabled）
- `payment_config` 唯一读取方是 `internal/query/settings.go:59`，仅用于回显 /super/payment 页面本身
- Alipay 字段错位：模板提交 `alipay_client_id/alipay_merchant_private_key/alipay_public_key`（`payment.html:157,161,165`），handler 读 `alipay_app_id/alipay_private_key`（`admin_handler_common.go:1236-1237`）→ 保存必失败；且写入 key 与回显 key 也不一致（`query/settings.go:83`）
- WeChat 丢字段：模板有 `wechat_mch_serial_no/wechat_mch_private_key/wechat_apiv3_key`（`payment.html:185-193`），handler 只读 `wechat_app_id/wechat_mch_id/wechat_api_key`（`:1278-1280`），而 `pkg/payment/factory.go:63` 要求四项齐全
- 密钥明文存储：`payment_config.config_value` 明文 INSERT；`payment_gateways` JSONB 更新同样无加密（`payment.go:116-136`）。项目已有 `crypto.Encrypt`（SMTP 密码在用）

**方案**：二选一，推荐方案 A。

- **方案 A（推荐）**：废弃 `payment_config` 写入路径，/super/payment 表单改调已有的 `/api/admin/payment/gateways`（list/update/test，`payment.go:46-48`，这是唯一真正写 `payment_gateways` 的接口，但当前无任何 UI 调用）。步骤：
  1. 修正模板字段名与 `updateGateway` 的请求结构对齐（Alipay app_id/private_key/public_key；WeChat 四项齐全）
  2. 表单 JS 改 fetch `/api/admin/payment/gateways/:id`（PUT）+ `/test`
  3. 敏感字段（secret/private key）入库前 `crypto.Encrypt`（key 用 `cfg.Auth.EncryptionKey`），网关 factory 读取时 Decrypt；回显只给掩码
  4. 页面回显改读 `payment_gateways`（`GetPaymentConfig` 改数据源）
  5. 旧 `payment_config` 的四个 handler 删除或留作一次性迁移读取源（如有历史数据需迁移：payment_config → payment_gateways）
- **方案 B**：保留 payment_config，让支付链路改读它——改动面更大且支付链路是资金核心路径，不推荐。

**验证**：模拟模式 + 真实网关各跑一次充值端到端；webhook 签名校验通过。

### P0-3 降价永远不生效 / 删除的规则残留计费 [E]

**状态：⤵️ 降级为 P2（2026-07-13，经复核不改 proxy；2026-07-18 再次确认仍待修复）**。复核结论：计费权威在 portal——主链路 `handleUsageStatsBatch` → `processBillingBatch` 用 portal 自己的价格规则重算费用（`webhook_billing.go:92` `billingSvc.CalculateCostBatch`），全程不读 proxy 上报的 `record.Cost`；旧路径 `handleUsageBatch` 为 audit-only 直接丢弃（`webhook_usage.go:52-65`）。proxy 的 max 合并 bug 仅影响其本地 cost 统计展示值（可能偏高），不影响任何扣费。

**2026-07-18 代码复核**：`ai-proxy-go/pkg/billing/billing.go:285-350` 的 `SyncPricingFromPortal` 仍使用 `max(旧值, 新值)` 合并并复用 `existing.pricing` 对象；被 portal 删除/降价的规则字段仍会在 proxy 缓存中保持旧高值。需要按原方案改为**全量快照替换**（或整体替换该 key 的 pricing 条目）并处理 tombstone。该问题暂不阻断资金，但会影响 proxy 本地计费统计与上游对账展示，建议后续清理。

**证据**：`ai-proxy-go/pkg/billing/billing.go:319-330`：`SyncPricingFromPortal` 合并用 `max(缓存旧值, 新值)` 且复用已有缓存条目 `pd = existing.pricing`；缓存条目每次同步刷新 `at` 永不 TTL 过期（`:347`）。Portal 侧删除/停用规则后 `SyncViewForProxy` 只返回 active（`ai-portal-go/internal/services/pricing.go:819`），被删规则的高价字段因 max 永久保留。

**方案**（改 ai-proxy-go）：

1. 取消 max 合并语义：同步视图是**全量权威快照**，改为整体替换该 key 的 pricing 条目（`pricingCache[key] = 新条目`），不再逐字段 max。
2. 删除语义落地：同步返回的 key 集合与本地缓存求差，本地多出且本次同步为全量时删除该缓存条目（或改为：每条目记录 `ruleIDs`，视图不包含的 rule 字段清零）。确认同步接口是全量还是增量——若当前是增量，改为全量（数据量小）或加 tombstone 字段。
3. 验证：单测覆盖降价、删除规则、停用规则三个场景；本地起 proxy 手工同步验证。

### P0-4 企业成员点 key 启停是假成功 [B]

**状态：✅ 已修复（2026-07-13，commit 31d7b45，已 push+sync）**。偏差：`db.Exec` 包装层丢弃 CommandTag 拿不到 RowsAffected，改为 keyHash 预查询检查 `ErrNoRows` → 404（与 6 条 UPDATE 同 WHERE 条件，等价且顺带修了缓存未失效分支）；原 JS 本有 `res.success` 判断，本次补 `r.ok` 防御非 JSON 错误。方案第 3 条（未声明字段 PATCH 400）属 P2 未做。新增 `apikeys_update_test.go` 3 用例全绿；app.css 改动已 bump layout.html `?v=23`。

**证据**：`web/templates/apikeys.html:190` 对所有企业 key 渲染启停按钮（无 IsSelfCreated 守卫）；`updateKey` 所有 UPDATE 带 `WHERE user_id = 当前用户`（`apikeys_crud.go:548-599`），管理员分配的 key（user_id=管理员、assigned_user_id=成员）0 行受影响，handler 不检查 RowsAffected 仍返回 success（`:609`），keyHash 查询同样为空、缓存未失效（`:523-526,605-607`）。前端翻转 UI 并 toast "Key Disabled"（`apikeys.html:896-919`）。

**方案**：

1. 前端：非自建（管理员分配）的企业 key 不渲染启停按钮（模板已有 IsSelfCreated 数据，加守卫）。
2. 后端：`updateKey` 检查 `RowsAffected == 0` 时返回 404/403（"key not found or not editable"），不要假 success。
3. 顺带修 `updateKey` 静默丢字段的模式（见 P2 备注）：未声明字段的 PATCH 返回 400 提示。

### P0-5 企业端 Request Invoice 三重断链 [E]

**状态：✅ 已修复（2026-07-13，commit e39ac34，已 push+sync）**。按产品拍板口径实施：企业申请走 `createEnterpriseRequest` 分支（不传 profile_id/附件）；开票资料取 `enterprises.name` + `enterprise_applications.registration_number`（verify 时审核过的信息），不碰 profile/invoice_profiles；企业 eligible = `enterprise_audit_log`（change_type='credit_topup' AND field_name='credit_total'，即 topupEnterprisePool 实际写入的流水表——原方案说的 enterprise_pool_adjustments 表不存在）合计 − 已开票 − 待处理，与个人 `balance_type='balance'` 口径完全分开；金额单位统一为 USD 分（`amount_cents`，个人端前端原本传元 float，已同步收敛）；`invoice_requests` 加 `enterprise_id` 列（schema.go ALTER 自动迁移）；admin 列表展示企业徽章+企业名+税号。新增 `invoice_requests_enterprise_test.go` 全链路用例全绿。**EA 鉴权已收紧（commit 见后续）**：企业分支要求 `users.role='enterprise_admin'`，普通成员 403，企业端 billing 页按 `IsEnterpriseAdmin` 隐藏 Request Invoice 按钮。**遗留（待产品确认）**：存量 `invoice_requests.amount` 语义由 micro-USD 变为分，dev 库 0 行无需迁移，生产若有历史行需 `UPDATE invoice_requests SET amount=amount/10000`。

**证据**：
- `createRequest` 要求 `profile_id`（`binding:"required"`，`internal/routes/invoices.go:392`），企业端弹窗无抬头选择、JS 不传（`web/templates/enterprise/billing.html:292-297`）→ 必 400
- 可开票额按个人 `balance_transactions ... balance_type='balance'` 计算（`invoices.go:466-474`），企业记账写 `balance_type='credit'`（`webhook_billing.go:322-324`）→ 企业 eligible 恒 0
- 前端提示 "Amount in cents"，后端按美元做 `USDtoQuota`；前端传的 `currency` 后端结构体没有，直接忽略

**方案**：

1. 企业端弹窗加发票抬头选择（复用个人端 profile 列表端点，或允许企业级抬头），JS 传 `profile_id`。
2. 后端 `createRequest` 识别企业上下文：eligible 计算改按企业维度（`user_id IN (企业全员)` 且 `balance_type='credit'`），与个人口径分开两个函数。
3. 统一金额单位：前后端都按"分"传整数，或都按美元——选一种，在 API 结构体加 `currency` 并校验。
4. 测试：企业成员提交 → admin 审批 → 完成 全链路用例。

### P0-6 发票交付链缺失（用户永远拿不到发票） [E]

**状态：✅ 已修复（2026-07-13，commit e39ac34，按线下交付口径）**。产品拍板：发票由工作人员 email/纸件线下交付，不做 PDF/下载端点/自动邮件。已落地：`listRequests` 返回 `invoice_number` + `rejection_reason`；用户端发票列表与企业端 billing 页展示状态 + 发票号（completed）+ 驳回原因（rejected）；admin 列表同样可见。P2 表中"发票驳回原因死写"一项随此一并解决。

**证据**：admin `completeInvoiceRequest` 只 UPDATE status/invoice_number/issued_at（`admin_reconciliation.go:140-159`），无邮件、无 PDF、无下载端点；用户端 `listRequests` 不返回 invoice_number（`invoices.go:351-386`）；驳回原因 `rejection_reason` 写入后无任何 SELECT 读取（`admin_reconciliation.go:128`）。

**方案**（分两期）：

- **一期（上线必须）**：completeInvoiceRequest 增加邮件发送（复用 mail.Sender，新模板 `mail.invoice_issued`，正文含发票号）；用户端 `listRequests` 返回 `invoice_number` + `rejection_reason`；发票列表页展示这两列。
- **二期**：PDF 生成 + 下载端点（可先用邮件附件方式过渡，或 admin 上传 PDF → 用户下载）。

---

## P1：功能断裂 / 假成功

### P1-1 GDPR 三件套缺口 [E]

**证据**：
- 请求删除清 `backup_email = NULL`（`gdpr.go:211`），真实数据在 `second_email`（schema 两列并存）；同语句未清 `company`
- 删除账号匿名化只清 email/name/phone/password_hash（`profile.go:193-201`），残留 second_email/company/shipping_address/invoice_info
- 导出截断 `LIMIT 100`（usage，`gdpr.go:76`）/ `LIMIT 50`（orders，`:102`）；user 对象缺 phone/company/second_email/balance/偏好；未导出 delivery_addresses、invoice_profiles、audit_logs

**方案**：

1. `gdpr.go` 删除语句改清 `second_email`（并确认 `backup_email` 列是否还有任何读写方，没有则后续 migration 删除该列），补 `company`。
2. `deleteAccount` 匿名化补 second_email/company/shipping_address/invoice_info。
3. 导出：去掉 LIMIT 改分批流式（或全量——个人数据量级可控），user 对象补全字段，增加 addresses/invoice_profiles/audit_logs（近 1 年）三个 section。
4. 测试：构造含全字段用户，导出后断言字段齐全；删除后断言 second_email 为 NULL。

**状态：✅ 已修复（2026-07-13，commits 94f3f4e / ac8da3d / c5b1ca3 + proxy 6e9e868，已 push+sync+重建，线上端到端验证）**。按产品拍板升级为"延迟删除 + 分层导出"：① 删除（`/api/gdpr/delete` 与 `DELETE /api/profile/account` 统一）改为立即停用账号（key 软删、登录 403 专用文案）+ 置 `gdpr_erasure_requested_at`，PII 保留 `gdpr_retention_days`（新设置，默认 90 天）后由夜间 retention job 经共享函数 `db.AnonymizeUser` 匿名化（含 second_email/company 等全部漏列，事务执行）；② 自助导出改为近 90 天/每类 1000 条窗口但字段齐全（user 全字段、delivery_addresses、invoice_profiles、balance_transactions、audit_logs，替代原 LIMIT 100/50 截断）；③ 全量导出走 `gdpr_export_requests`：用户申请（邮箱可选，默认注册邮箱）→ admin 在新增 `/admin/compliance` 页面一键处理（生成 JSON 至 `uploads/gdpr-exports/`）→ 邮件通知（`SendGDPRExportReady`，TLS 已实测送达）→ 用户登录下载（鉴权到本人）；④ 合规页另含待删除账号列表（剩余天数、单用户导出、立即匿名化）与数据保留总览；⑤ proxy 侧 `RETENTION_ENABLED=true`、`RETENTION_MAX_AGE=2160h`（90 天）已上线（compose 加 `proxy-retention-data` 卷，日志确认 store 初始化），满足 Anthropic 类上游 30 天核查要求。与原方案偏差：导出未做流式（窗口+申请制替代）；全量导出邮件发下载通知而非附件（避免 PII 进邮箱）。

### P1-2 企业成员 Import CSV 100% 失败 [B]

**证据**：INSERT 不含 `token` 列（NOT NULL UNIQUE，`schema.go:726`）；`ON CONFLICT (enterprise_id, email) WHERE status IN (...)` 引用的部分唯一索引不存在（`enterprise_batch.go:144,183` vs `schema.go:722-761`）→ 规划期报错，每行计 failed；无测试文件。

**方案**：

1. INSERT 补 `token`（复用邀请 token 生成逻辑）、`expires_at`。
2. 补 schema 的部分唯一索引 migration，或改 ON CONFLICT 目标为现有索引。
3. 导入成功后复用 `SendEnterpriseInvitation` 发邮件（与 addEnterpriseMember 对齐）。
4. 新增 `enterprise_batch_test.go`：至少覆盖 1 成功 + 1 重复 + 1 格式错误。

**状态：✅ 已修复（2026-07-13，commit bbf6228，已 push+重建，线上索引已建）**。INSERT 补 `token`（`generateRandomCode` 32 位）+ `expires_at`（7 天）；schema init 新增部分唯一索引 `uq_enterprise_invitations_active(enterprise_id, email) WHERE status IN ('pending','email_confirmed')`（线上无重复行，创建成功，ON CONFLICT 不再规划期报错）；已存在用户分支对齐 `inviteMember`（`email_confirmed` 状态待 EA 审批，不发邮件），新用户导入后补发 `SendEnterpriseInvitation` 邀请邮件（事务提交后异步）；新增 `enterprise_batch_test.go` 4 例（新用户/重复跳过/缺 email 列 400/已存在用户）全过。

### P1-3 企业批量配额写错表 [C]

**证据**：`batchSetMemberQuota` 写 `users.quota_limit`（`enterprise_batch.go:284`），限流只读 `enterprise_members.quota_limit`（`apikeys_validate.go:101,187-195`）；单人编辑走 `PUT .../quota-limit` 写 `em.quota_limit`（生效）；列表显示也是 `em.quota_limit`（`query/enterprise.go:377`）。

**方案**：`batchSetMemberQuota` 改 UPDATE `enterprise_members.quota_limit`（WHERE enterprise_id + user_id IN (...)），事务 + 审计；`users.quota_limit` 若无其他消费方，标记废弃（后续 migration 清理）。

**状态：✅ 已修复（2026-07-13，commit 3f065d3，已 push+sync+重建）**。`batchSetMemberQuota` 与成员 CSV 导出均改 `enterprise_members.quota_limit`；全库确认 `users.quota_limit` 无任何限流/计费消费方（仅这两处读写），已标记废弃待后续 migration 清理；批量接口补负值校验与单人编辑对齐。

### P1-4 usage-logs 导出 404 + 今日数据错位 [A+E]

**证据**：`exportLogs()` 拼 `/api/requests/export`（`usage-logs.html:447-450`），真实注册 `/api/billing/requests/export`（`billing.go:43`）；"今日"卡片取 `daily[daily.length-1]` 但后端按日期降序返回（`billing.go:129-140`），取到的是最旧一天。

**方案**：前端路径改为 `/api/billing/requests/export`；"今日"改取 `daily[0]`。

**状态：✅ 已修复（2026-07-13，commit c457f68，已 push+重建 portal，容器 healthy）**。前端路径与"今日"取值按方案修正；后端 `exportRequestsCSV` 参数（start_date/end_date）与前端一致，无需改动。

### P1-5 签到功能无入口 + 两个端点 500 [A]

**证据**：全模板无 `/api/checkin` 调用（仅 admin-settings 有开关）；`getRecords`（`checkin.go:198`）和 `getAdminStats`（`:224,246`）查询不存在的列 `quota_earned`（schema 只有 `reward`，`schema.go:597-605`）。

**方案**：

1. 修 SQL：`quota_earned` → `reward`。
2. 产品决策：要么 dashboard 加签到入口（按钮 + 连续签到展示，调 `/api/checkin`、`/api/checkin/records`），要么下线该功能（删开关与端点）。建议上线前先做入口——后端 doCheckin 本身是完整的（gift_balance 入账有消费链）。

**状态：✅ 已修复（2026-07-13，commit 291ed51，已 push+sync+重建）**。产品拍板：功能全部删除。移除 `routes/checkin.go`(+测试)、路由注册与 bootstrap 接线、`checkin_records` 表（线上 0 行已 DROP）、三个 checkin_* 设置项（groups/defaults/seed/线上行已删）、retention 清理项、datax 实体、admin-settings 开关 UI、i18n 键及全部测试脚本引用；历史 `balance_transactions`（type='checkin'）在 reconciliation 页保留显示标签。

### P1-6 代金券 Referral/Welcome 设置保存不了 [E]

**证据**：`admin-vouchers.html` 展示 div 与输入框 id 完全重复（`:132-194`、`:70-109`），`getElementById` 返回展示 div → `.value` 为 undefined → Referral 写 "NaN" 进 5 个 invitation_* key；Welcome 校验失败永远弹错。

**方案**：输入框 id 加 `-input` 后缀（同步改 `saveSettings()`/`saveWelcome()` 的取值 id）；顺手加后端校验：`updateSetting` 对 invitation_*/welcome 数值项拒绝非数字（防止再被污染）；DB 里已存在的 "NaN" 值写一次性修正（或靠默认值兜底逻辑已存在，确认即可）。

**状态：✅ 已修复（2026-07-13，commit 686e24d，已 push+重建）**。7 个撞车输入框 id 加 `-input` 后缀，`editWelcome`/`saveWelcome`/`openSettingsEdit`/`saveSettings` 取值 id 同步修正（展示侧 id 不动，全页 `uniq -d` 校验无重复 id）；后端 `saveSettings` 加 5 键白名单 + `strconv.ParseInt` 数值校验（非数字 400，杜绝 "NaN" 再入库）；线上 `system_settings` 无 invitation_* 行，无污染数据需清理。

### P1-7 企业应用审批看不到附件 [E]

**证据**：下载端点 `GET /api/admin/enterprise-applications/:id/documents/:docId/download` 存在（`admin_common.go:103`），`renderReviewDocuments` 只渲染文件名纯文本（`admin-enterprise-applications.html:200-204`）。

**方案**：文件名渲染为链接/按钮调下载端点（带 auth header 的 fetch → blob 下载，因端点 RequireAdmin 需要 JWT）。

**状态：✅ 已修复（2026-07-13，commit 882e58a，已 push+sync+重建）**。比方案更简：auth 中间件有 `jwt` cookie 兜底，文件名直接渲染为 `<a target="_blank">` 链接即可，无需 fetch+blob。文件经 host `./uploads` bind mount 到容器 `/app/uploads`，admin 全程只面对 portal URL，无需访问 EC2 物理路径。

### P1-8 企业告警无确认入口 [B]

**证据**：`internal/routes/enterprise_alerts.go` 是空文件（仅 `package routes`）；dashboard 展示 Pending/Confirmed 徽章但无 ack 端点、无按钮。

**方案**：新增 `POST /api/enterprise/alerts/:id/ack`（校验成员权限，UPDATE `acknowledged_at`）；dashboard 告警卡片 Pending 项加"确认"按钮。

**状态：✅ 已修复（2026-07-13，commit 92d1502，已 push+sync+重建）**。`POST /api/enterprise/alerts/:id/ack` 落地（enterprise_admin + 本企业 + `acknowledged_at IS NULL` 幂等保护 + 审计日志），原空文件 `enterprise_alerts.go` 现为 handler；dashboard Alert History（该页本身仅 EA 可见）Pending 行加 Acknowledge 按钮。

### P1-9 AWS 模型页停用/忽略绕过副作用 [E]

**证据**：AWS 页 `pauseAWSModel`/`ignoreAWSModel` 走 PATCH `{status:'paused'|'ignored'}`（`admin-models-aws.html:484,493`），`updateServiceModel` 对非 active 状态只改 status 字段（`models_crud.go:206-252`），不调 `deactivatePricingRule`/`cleanupSingleModelFromGroups`/`PublishSyncEvent`；通用页 POST `/:id/pause`、`/:id/ignore` 有完整副作用（`:288-301,448-460`）。

**方案**：AWS 页按钮改调通用页的 POST `/:id/pause`、`/:id/ignore`（与通用页一致），删除 PATCH 旁路或在 `updateServiceModel` 对 paused/ignored 补齐同等副作用（二选一，推荐前者）。

**状态：✅ 已修复（2026-07-13，commit 4e12279）**。按推荐方案：AWS 页 pause/resume/ignore/restore 四个按钮改调通用页 POST 端点（`/:id/pause`、`/:id/resume`、`/:id/ignore`，restore 复用 resume），恢复价格规则停用/重激活、分组清理、key 同步与 `PublishSyncEvent` 全套副作用；PATCH `updateServiceModel` 保留供其他字段编辑。

### P1-10 Add Admin 表单 404 [D]

**证据**：模板提交 `POST /super/admins`（`super/admin-management.html:57`），`pages.go:173-180` 只注册了 `/admins/:id/promote`、`/demote`。

**方案**：新增 `POST /super/admins` handler（校验 super_admin、邮箱唯一、写 `users.role='system_admin'` 或发邀请邮件令其注册后自动赋予角色——推荐后者，与现有 promote 逻辑衔接）；或产品决策改为"先注册再 promote"流程并移除该表单。

**状态：✅ 已修复（2026-07-13，commit 2cecff6）**。落地"先注册再添加"流程：新增 `POST /super/admins` → `HandleAddAdmin`，按邮箱查已注册用户并提升为 `system_admin`；未注册邮箱提示"请先让对方注册"，已是管理员则拒绝。未做邀请邮件流（系统管理员无通用邀请机制，SA 场景下先注册再添加足够）。i18n 补 3 键（en+zh），i18n 门禁通过。

### P1-11 公告 dismiss 是假象 [C]

**证据**：`POST /api/announcements/:id/read` 写 `announcement_reads`，但唯一读取方 `getUnreadCount` 无前端调用；`listPublished`（`announcements.go:46-63`）不排除已读；dashboard 每次加载拉全量。

**方案**：`listPublished` 加 `AND NOT EXISTS (SELECT 1 FROM announcement_reads WHERE ...)`（对已登录用户）；dashboard 拉取时传认证信息。

**状态：✅ 已修复（2026-07-13，commit d0c9613，已 push+sync+重建）**。澄清：dismiss 本身写库成功，问题是 `listPublished` 不过滤已读导致下次加载重现。按 `dismiss_mode` 精细化处理而非一刀切：仅 `permanent` 模式过滤已读；`session` 保持"下一会话重现"语义（前端 sessionStorage 管会话内隐藏），`always` 保持不可 dismiss——否则两种模式会被静默改变。dashboard 拉取本就带 cookie 认证（端点 RequireAuth），无需改。

---

## P2：死配置 / 死代码（不阻断上线，建议上线后 1-2 周内处理）

| 项 | 证据 | 方案 |
|---|---|---|
| settings `routing_*` 四项 proxy 侧从不使用 | proxy `pkg/keypool/sync.go:217` 解码后死字段，proxy 只认自己 env（`ai-proxy-go/internal/config/config.go:60-61`） | 二选一：proxy 消费 sync 下发的 routing 配置（推荐，运维需要），或从 settings 页移除这四项 |
| 敏感词三项整体空转 | 唯一消费 `POST /api/sensitive/check` 无生产调用方；`check_sensitive_on_prompt_enabled` 连 handler 自己都不读 | 合规需求若在：proxy 请求链路接入 `/api/sensitive/check`（或 portal 侧中间件）；否则下线 |
| 模型 Hide 按钮死写且单向 | `hidden` 列无功能消费方；UI 只有 Hide 无 Unhide | 公开列表/proxy 同步过滤 hidden + 补 Unhide，或移除按钮 |
| Smart Routing 五开关不回显 | checkbox 硬编码 `checked`（`admin-models-general.html:245-253`），初始化不读已存值 | 页面初始化 fetch 当前值渲染 |
| upstream-ops 中文 tab 失效 | `getActiveTab()` 按渲染文案匹配英文（`admin-upstream-ops.html:357-365`） | tab 标识改 `data-tab` 属性 |
| 发票驳回原因死写 | `rejection_reason` 无 SELECT | ✅ 已随 P0-6 解决（e39ac34） |
| admin/users Role 下拉假选项 | 可选 system_admin/super_admin 但必然 403（`admin_users.go:104-107`） | 下拉只保留 user/enterprise_admin |
| 17 个 settings key 只写不读 | 见审计明细（quota_for_new_user、topup_link、retry_times、log_*、alipay_enabled 等） | UI 已隐藏；建议 PUT 接口对无消费方 key 拒绝写入并加注记，逐步接线或删除 |
| 死页面/死端点一批 | `/admin/groups/:id/edit` 整页无入口；用户详情页被抽屉取代；/checkout 独立收银台无入口且 HandleProcessPayment 不入账；`/api/admin/enterprise/:id/override` 等无调用方 | 逐一确认后删除（保留 git 历史可回溯） |
| 审计日志筛选仅当前页前端过滤 | `admin-audit-logs.html:155-187`，服务端支持参数但 UI 不发 | UI 改服务端筛选 |
| 企业 topupMemberQuota API 无 UI | `POST /api/enterprise/members/:userId/topup` 实现完整无调用方 | 成员列表加"充值"入口或删除 API |
| `updateEnterpriseGroup.concurrent_limit` 半拉字段 | 结构体声明但 UPDATE 不处理、模板无输入 | 补全或删字段 |
| 企业移除成员不失效 validate 缓存 | `removeMyEnterpriseMember` 未调 `invalidateValidateCache` | 补上（5 分钟窗口） |
| 删除账号/GDPR 删除不失效 key 缓存 | `profile.go:189`、`gdpr.go:145` | 同上 |
| validateKey 相关：`payment_simulation_mode` 无关，但 `GET /api/payment/status/:orderId` 查不存在的 `paid_at` 列 | `payment.go:313-337`，无前端调用 | 修 SQL 或删端点 |

### P2 批处理状态（2026-07-14，commit ai-portal-go `c23fe3a` / ai-proxy-go `150ff09`，已 push+sync+proxy 重建）

| 项 | 处理结果 |
|---|---|
| 1. routing_* 四项 | 已确认双端死配置（proxy 的 env 版 RoutingConfig 也无消费方），已从 portal settings/sync/seed、proxy config/sync 彻底移除；DB 清理 7 行死 settings |
| 2. 敏感词三项 | 已下线：删除 `POST /api/sensitive/check`、3 个 settings key 及 UI/测试引用 |
| 3. 模型 Hide | 补全 Unhide 按钮，hidden 模型从用户端模型列表过滤，admin 列表保留 |
| 4. Smart Routing 回显 | 页面初始化拉 `/api/admin/settings` 设置 5 个 checkbox 状态 |
| 5. upstream-ops tab | 改 `data-tab` 属性，中文界面不再失效 |
| 6. admin/users Role 下拉 | 编辑弹窗只保留 user/enterprise_admin |
| 7. 17 个死 settings key | 已在 `internal/routes/settings.go` 加 `deadSettings` 拒绝写入，17 key 清单见代码注释 |
| 8. 死页面/死端点 | 删除 `/admin/groups/:id/edit`、独立用户详情页、`/checkout` 独立收银台、`/api/admin/enterprise/:id/override` |
| 9. 审计日志筛选 | 改服务端筛选，翻页保留筛选条件 |
| 10. 企业成员充值 | 成员列表加 Top Up 入口，调现有 `POST /api/enterprise/members/:userId/topup` |
| 11. `updateEnterpriseGroup.concurrent_limit` | 补全 UPDATE + 分组编辑 UI 输入 |
| 12/13. 删除/移除失效缓存 | `removeMyEnterpriseMember`、`removeMember`、用户自助注销、GDPR 删除后均失效 validate 缓存 |
| 14. `GET /api/payment/status/:orderId` | 改查 `orders.completed_at` |
| 15. 企业 `quota_total` 软限制 | 设计思路：quota 仅提示，credit 才是硬阻断，不处理 |
| 16. 编辑用户清空 User Group | 允许清空；只影响新建 key 默认 group，已有 key 的 `ak.group_id` 不受影响 |
| 17. SA 邀请已存在用户通知 | `approveInvitation` 与 `approveInvitationAdmin` 在添加成员后发送 `SendEnterpriseAddedNotification` |
| 18. 企业发票 eligible 口径 | 个人/企业分离：普通成员可为自己的个人消费（general/subscription key）申请发票，不计入企业额度；EA 只处理企业发票 |
| 19. `createPayment` 币种 | 真实网关路径由 `cny` 改为 `usd` |
| 20. EOL confirm/discontinue 副作用 | 不处理，proxy 60s 轮询兜底 |

### 存疑点（已确认，不算 bug）

- 企业 `quota_total` 只展示+告警邮件，不像 `credit_total` 在 validateKey 硬阻断——**设计思路：quota 仅提示，credit 才是硬阻断，不处理**
- 编辑用户无法清空 User Group——**已允许清空；只影响新建 key 默认 group，已有 key 的 `ak.group_id` 不受影响**
- SA 邀请已存在用户不发邮件——**已在 `approveInvitation`/`approveInvitationAdmin` 添加成员后发送 `SendEnterpriseAddedNotification`**
- 企业发票 eligible 口径——**已按个人/企业分离修复：普通成员可为自己的个人消费申请发票，不计入企业额度；EA 只处理企业发票**
- `createPayment` 真实网关路径硬编码 `Currency: "cny"`——**已改为 `usd`**
- EOL confirm/discontinue 不停自动价格规则、不发同步事件——**不处理，proxy 60s 轮询兜底**

---

## 建议执行顺序

> **第一批已完成（2026-07-13）**：P0-1（3c4e9b3）、P0-4（31d7b45）、P0-2（a48117c）、P0-5/P0-6（e39ac34）均已修复 push+sync；P0-3 经复核降级为 P2（portal 是计费权威，proxy 侧不影响资金）。各项详情见上文状态标注。
>
> **审计后追加修复（2026-07-13，用户实测新报问题，均已 push+sync+重建上线）**：
> - 企业首 admin 审批死锁（393b2cc）：企业无 EA 时 SA 可直接批准 `email_confirmed` 邀请（bootstrap）；有 EA 时仍必须 EA 审批。线上实测 4c922b4a 卡住的邀请已疏通。
> - 企业邀请注册角色 bug + aff_quota 中间态拆除（d70750c）：非 autoJoin 邀请注册时不再预设 `users.role='enterprise_admin'`（曾导致被拒用户成为"无企业的 EA"，如 suzi@aynbay.com，已数据修正回 'user'）；inviter 奖励无条件直进 gift_balance，`POST /api/voucher/transfer-quota` 端点与 `invitation_auto_transfer(_enabled)` 设置删除（aff_quota 列保留兼容，全库无滞留余额）。
> - 其他：billing 页按网关启用状态门控支付图标（feaff83，playwright 截图验证）、Stripe success/cancel URL 默认值兜底（5fa107e）、发票申请收紧为仅 EA 可提交（0a6b70a）。
> - 企业邀请注册漏发 welcome 奖励 + provider-health 小样本阈值（2026-07-13）：`acceptInvitation` 新建用户未调 `ApplyWelcomeBonus`（与两条常规注册路径不一致，thinbee09 等 3 个普通用户漏发 $0.68，SA 账号为种子创建不算），已接线 `RewardService` 并在新用户创建后调用；存量补发待用户确认。provider-health 5 分钟桶判定加最小样本量 10（`minHealthSampleSize`）：桶内请求 <10 时新桶记 `unknown`、已有桶保持原状态，避免 2 请求 1 失败即 unhealthy。
>
> **P1 批（2026-07-13，均已 push+sync+重建上线）**：P1-1 GDPR 延迟删除+分层导出+合规 admin 页+proxy 90 天 payload retention（94f3f4e/ac8da3d/c5b1ca3 + proxy 6e9e868，线上端到端验证：申请→处理→邮件→下载全通）；P1-3 批量配额改 enterprise_members（3f065d3）；P1-5 签到功能全删（291ed51）；P1-7 审批附件下载链接（882e58a）；P1-8 告警 ack（92d1502）；P1-11 公告 permanent dismiss 过滤（d0c9613）。剩余 5 项已全部修复（2026-07-13）：P1-4 usage 导出 404+今日错位（c457f68）、P1-10 Add Admin 404（2cecff6）、P1-2 Import CSV 100% 失败（bbf6228）、P1-6 代金券 id 撞车（686e24d）、P1-9 AWS 停用副作用（4e12279），均已 push+重建上线。后续补充：GDPR 导出改走 storage.Provider 相对 key（c8effdc）；合规页加任意用户搜索导出、retention 文案勘误（e8cf2aa）；修复 admin 用户搜索因 NULL group_id 静默丢行（a73c673，`log_retention_days` 等设置项确认无消费方，已并入 P2 死 key 行）；admin/settings 页补 Compliance 分组（`gdpr_retention_days`，原遗漏模板 settingMeta 区块，f11fa9e）；企业成员侧栏不再显示"企业申请"入口（role='user' 且已加入企业时隐藏，后端本就有 409 拦截；cf11d4e），企业 key 类型误导文案"需管理员授权"改为"费用由企业账户结算"（成员本就可自建企业 key，无授权环节）；apikeys 复制链路修复（f6a433c）：`viewEnterpriseKey`（企业卡片 Copy）从未设置 `createdKeyValue` 导致弹窗"Copy Key"静默无效/复制旧值，`copyCreatedKey` 增加 DOM 文本回退，创建弹窗/个人卡片/企业卡片三条复制路径 playwright 全验 PASS；proxy Bedrock Runtime（Converse）→OpenAI 响应补齐 id/object/created/model（ai-proxy-go 79ed62f，model 回显客户端请求的逻辑模型名，线上 curl 验证通过）；api.woodgit.com nginx `proxy_pass` 尾斜杠吃掉 `/v1/` 前缀导致公网全量 404（去掉尾斜杠修复，公网 443 非流式已通）；Bedrock 流式空响应（ai-proxy-go 1508adb）：ConverseStream 真实帧的事件类型在 `:event-type` header、payload 为裸事件体（带 `"p"` 填充字段），原按包裹式 `{"contentBlockDelta":{...}}` 解析导致每帧零输出、客户端只收到 `[DONE]`；改按 header 解析+裸 payload 分发，附真实帧格式单测，线上内网+公网 443 流式均验证有 delta 输出；上游模型 ID 泄露修复（ai-proxy-go 033ae81）：chat（流式+非流式）与 embeddings 的 usage 记录、流式 chunk 回显、Responses API 流式 state 统一改用客户端逻辑模型名（此前 Bedrock ID 如 `us.anthropic.claude-opus-4-5-20251101-v1:0` 泄入 usage_records 与 API 响应）；存量 usage_records 41 行按 service_models 映射清洗为逻辑名（定价规则按 api_model_name 匹配，不受影响）；usage-by-key 消费全显 $0（442910c）：端点预转 USD float 而前端 Currency.format 期望 micro-USD quota，双重换算所致，改为返回原始 quota；admin 统计页巡查（4110750）：dashboard "Abnormal Providers" 原统计 disabled providers（66 个里 36 个未启用区域被报"异常"），改为按最近 15 分钟健康桶 worst-status（unhealthy/degraded）计数；provider_health_stats 的 `MAX(status)` 字母序聚合（degraded 被 healthy 覆盖不可见）在 dashboard breakdown、provider-health SSR+API（list/detail/history）、proxy 配置同步健康五处统一改为 severity CASE。注：provider-health 默认 15m 窗口在无流量时段统计为空属预期；小样本桶（如 2 请求 1 失败=50%）会把 provider 标 unhealthy，噪声敏感但逻辑一致。；reconciliation-ops detail 500（f847975+e46cbad）：一条 usage_records 的 upstream_key_id 为空串，`::uuid` cast 报错且 AND 不短路，改 `NULLIF(key_id,'')::uuid`；EA dashboard 本月用量显示原始 micro-USD（$52255→$0.052255，MonthlyUsageUSD），Requests/Tokens/Spend 图空白根因是 DailyUsagePoint json tag 小写键与 JS 读大写字段名不匹配（删 tag 与 EnterpriseUsageRecord 一致），Spend 图补 /1e6 换算；逻辑模型名改动导致对账 detail 的 FULL OUTER JOIN（provider+model+key）对 Bedrock 模型失配（up 侧 Bedrock ID vs billed 侧逻辑名），up CTE 加 service_models 映射归一（c226d42，验证 up/billed 匹配恢复）。

1. **第一批（上线前必须）**：P0-1（禁用车门）、P0-2（支付配置，资金命脉）、P0-3（降价生效，跨仓改 ai-proxy-go 需同步部署 proxy）、P0-4（假禁用）
2. **第二批（上线前完成）**：P0-5、P0-6 一期（发票链路）、P1-1（GDPR 合规）、P1-2/P1-3（企业管理高频功能）
3. **第三批（上线后 1 周）**：P1-4 ~ P1-11
4. **第四批（上线后 2 周）**：P2 清理 + 存疑点产品确认

## 每批的通用收尾动作

- 本地 fast-gate 10 项 + DB 回归（db/routes/services -p 1）
- 改动 ai-proxy-go 的批次：proxy 容器 rebuild 部署 + 本地 sync-to-proxy.sh
- 模板改动：playwright 截图验证相关页面；改 app.css 时 bump layout.html `?v=N`
- commit → push → sync-to-proxy.sh
- 每批完成后更新本文档对应项的状态标注

> **对账 detail billed 虚高修复（ad56c86，已 push+重建上线）**：data 查询 up CTE 的 `GROUP BY u.model`（raw 上游 ID，8a2ccc3 引入）把同一逻辑模型的 `anthropic.X`/`us.anthropic.X`/`global.anthropic.X` 拆成多行，每行各自 FULL OUTER JOIN 同一条 billed 行 → 页面出现"upstream=1 billed=7"假象；改为 GROUP BY 保持 (provider, 逻辑模型, key)，raw 变体用 `string_agg(DISTINCT ...)` 合并展示。线上验证：各行 up 与 billed 对齐；剩余小差异（opus 共 12 次）均为 2026-07-13 当天测试期间 proxy 多次重建/旧流式 bug 打断的在途请求（上游已记、portal 未结算），非系统性重复计费。

> **Bedrock 空消息 400 修复（ai-proxy-go 852c834，已 push+sync+重建上线）**：thinbee09 用 kimi code（OpenAI 格式客户端）调 claude-opus-4-6 报 400。定位为客户端历史消息含 `{"role":"assistant","content":""}`，aws.go ToProvider 无条件生成 `{Text:""}` 内容块，Bedrock Converse 拒绝空文本块（"The text field in the ContentBlock object is blank"）→ 上游 400 被 proxy 脱敏为 UPSTREAM_BAD_REQUEST。修复：ToProvider 丢弃空文本消息与空 system 块、assistant 消息无可内容（仅非 function 类型 tool_calls）时整条跳过；新增 TestAWSToProvider_SkipsEmptyMessages。线上验证：原 400 用例通过、流式+tool loop（kimi code 风格）通过。

> **上游错误可观测性 + maxTokens 超限修复（ai-proxy-go a47e517 + 82ccaf1，已 push+sync+重建上线）**：流式错误路径（stream_non_200）原不记录上游错误体，400 只能复现定位；现两条路径均记 status/provider/model/body_prefix（前 500 字节，仅服务端，客户端仍脱敏）。借此定位 thinbee09 kimi code 第二轮 400 根因：kimi code 请求 max_tokens 超模型上限（opus-4-6 Bedrock 上限 128000，客户端请求更大）。修复：DoRequestBytesWithContext 识别 400 报文中 "model limit of N"，将 inferenceConfig.maxTokens / max_tokens 钳位到 N 并重试一次（sendSigned 抽取复用），自适应所有模型无需维护模型表；新增 TestClampMaxTokensBody 4 例。线上验证：max_tokens=200000 非流式、256000 流式均成功，日志可见 clamp+retry 记录。

> **企业 key 计费三修（ai-portal-go 0b59030，已 push+sync+重建上线）**：thinbee09 名下同时有 general 与 enterprise 两类 key，企业消费全走了个人账（企业实收 $0、个人 gift 被误扣）。三处根因：① 批量结算 `webhook_billing.go` 的 key_type 判定是 `WHERE user_id LIMIT 1` 拿用户任意一把 key 的类型，同用户多类型 key 时判错 → 改为按请求 key hash 精确查 key_type；② 企业 key 原先扣个人 gift/balance → 改为只扣企业 credit（`webhook_usage.go` case enterprise、批量路径同步改 credit-only）；③ 预消费 `webhook_preconsume.go` 企业 key 不查个人余额 → 改查企业 credit 110% 上限（`handleEnterprisePreConsume`）。关键坑：两处 enterprises 查询的 `FOR UPDATE` 必须去掉，否则与 `checkEnterpriseLimits→sendEnterpriseAlert` 的 enterprise_alerts 插入（FK key-share，走连接池另一连接）死锁，已用 10 分钟测试挂死复现并修复。线上验证：企业 key（gift=0 balance=0）请求 200；Aynbay credit_used 0→84707 正常增长，thinbee09 个人 gift/balance 始终 0。新增测试 `EnterpriseCreditOnly_NoGiftNoBalance`、`MixedKeyTypes`。

> **Bedrock prompt caching + 会话粘性亲和（ai-proxy-go 140bc81，已 push+sync+重建上线）**：prompt caching 此前全 fleet 零命中——Bedrock 路径既无 cachePoint 注入也无 cache usage 解析。修复：`aws.go` ToProvider 在 system 与会话末尾注入 cachePoint（≥Bedrock 最小可缓存前缀 ~1024 token，4ch/token 启发式；小请求 Bedrock 静默不缓存不报错）；bedrockUsage 解析 cacheRead/WriteInputTokens，`promptTokensForBilling()=inputTokens+cacheRead`（OpenAI 语义：prompt_tokens 含 cache，cached 部分在 prompt_tokens_details 拆出），portal `pricing.go` 本就按 effectiveInput=Input-Cached + cache_hit 价计费，链路天然通；FromProvider/ExtractUsage/emitUsage 三处映射 cache 字段。亲和指纹从全 body sha256（历史每轮增长→永不命中、会话 50/50 拆到不同上行 key）改为会话种子哈希（system+首条非 system 消息），OpenAI/Anthropic 两形状归一化等价。线上验证：大 system（~40k 字符）两轮 cache_create 7003 → cached 7003，Bedrock prompt caching 生效（注意 Bedrock prompt cache 为账号级非 key 级，故跨 key 也能读到 cache）。

> **亲和 Get/Record key 一致性修复（ai-proxy-go 0cd2c61，已 push+sync+重建上线）**：140bc81 改了指纹但线上复验发现亲和仍未粘住——同会话请求在上行 key 间轮询（正是用户反馈的"同一 key 命中不同上行 key"）。根因：handler 在 `GetPreferredKeyID` 之后才把 `req.Model` 换成上游模型 ID、才 set `routing_track`，而 `RecordPreferredKeyID` 用改后的 model/track 重建亲和 key；Get（逻辑模型 + track=standard）与 Record（上游模型 + track=bedrock）算出的 key 不一致，Record 写的 key Get 永远查不到 → 每轮 miss → 退回轮询（实测 A、B、A）。修复：Get 把解析好的 key 存进 gin context（复用既有常量 `GinKeyAffinityCacheKey`），Record 直接复用同一 key，model/track 后续如何变更都不影响一致性；仅在 Get 未运行时回退重建。新增回归测试（Get 后变更 model+track，下一轮 Get 仍命中）。线上验证：同会话 4 轮请求全落同一上行 key 0665f97c；Redis 亲和 key 以逻辑模型名写入（修复前残留为上游模型名，1h TTL 自然过期）。

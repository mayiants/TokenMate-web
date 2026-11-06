# ai-portal-go / ai-proxy-go 全量源码评审报告

- **评审时间**：2026-07-19 ~ 2026-07-20（UTC）
- **评审范围**：两个仓库全部非测试 Go 源码（portal 158 文件 + proxy 62 文件，含子包），逐文件通读，无抽样
- **评审方式**：13 个模块分区并行评审，每区独立输出发现；本文档为汇总
- **约定**：严重度 P0=资损/安全/可用性事故，P1=正确性，P2=健壮性/可维护性，P3=建议项
- **前置说明**：评审基线已包含 2026-07-19 当天的修复（路由目录闸门、smartRoute 复合排序+亲和置顶、requestid 唯一化、deepseek reasoning 规范化、可服务模型三态、credit_deduction 污染修复、前端时间与导出统一），不再重复报告

## 统计总览

| 模块 | 评级 | P0 | P1 | P2 | P3 |
|------|------|----|----|----|----|
| portal webhook/sync 链路 | 需关注 | 1 | 3 | 5 | 3 |
| portal billing/支付 | **高风险** | 4 | 4 | 6 | 1 |
| portal auth/用户/apikeys | 需关注 | 0 | 5 | 7 | 3 |
| portal admin_* 管理面 | **高风险** | 2 | 6 | 6 | - |
| portal enterprise_* 企业域 | **高风险** | 1 | 6 | 7 | - |
| portal models/voucher/订阅 | 需关注 | 2 | 5 | 7 | - |
| portal services/db | 需关注 | 1 | 3 | 8 | 2 |
| portal web/query | **高风险** | 1 | 4 | 9 | 2 |
| portal 基础设施(pkg/middleware/cmd) | 需关注 | 0 | 2 | 9 | 4 |
| proxy handlers_chat/common/responses | **高风险** | 1 | 4 | 9 | - |
| proxy server 其余+middleware+auth+config | **高风险** | 1 | 5 | 5 | 4 |
| proxy keypool | 需关注 | 0 | 4 | 5 | 4 |
| proxy relay/billing/convert/cache/dto | 需关注 | 0 | 4 | 8 | 3 |
| **合计** | | **14** | **55** | **91** | **30+** |

## P0 汇总（14 项，按建议处理顺序）

| # | 位置 | 问题 | 类别 | 状态 |
|---|------|------|------|------|
| 1 | portal `payment.go:623-632` | `/billing/callback` 公开端点携 `status=success` 即入账，不与网关核实 —— 零成本充值 | 资损 | ✅ 已修复 (d1ca9f5) |
| 2 | portal `payment.go:319-326` + `pkg/payment/*` | micro-USD 原样传给 Stripe/PayPal/微信（按分）与支付宝（按元）—— 金额单位错配，多扣 100~10000 倍 | 资损 | ✅ 已修复 (d1ca9f5) |
| 3 | portal `alipay.go:117-121` | 路由恒传空签名、实现只判非空 —— 支付宝异步通知恒 401，付款不到账 | 可用性/资损 | ✅ 已修复 (d1ca9f5) |
| 4 | portal `orders.go:343-433` | 支付会话创建后可改订单金额，回调不核对实付 —— $10 付 $10000 的账 | 资损 | ✅ 已修复 (d1ca9f5) |
| 5 | portal `admin_users.go:245` | `createUser` 不限 role，普通管理员可直接创建 super_admin，绕过 updateUser 的双重闸门 | 越权 | ✅ 已修复 (eedac67) — role 白名单 + CanPerformSensitiveOps 校验 |
| 6 | portal `admin_users.go:297/326` | `promoteToSuperAdmin`/`demoteSuperAdmin` 无敏感操作校验、可自助提权、无名额检查、无审计 | 越权 | ✅ 已修复 (eedac67) — 敏感操作校验 + 禁自助 + MaxSuperAdmins 名额 + 保底1 SA + 结构化审计；web 层 HandlePromote/Demote 同步补禁自助+审计 |
| 7 | portal `enterprise_members.go:1172-1176,1281` | 邀请审批/拒绝不校验调用方企业归属 —— 跨租户审批并写入成员 | 跨租户越权 | ✅ 已修复 (eedac67) — approve 校验调用方企业归属；reject 的 UPDATE 绑定调用方企业；新增跨租户回归测试 |
| 8 | portal `query/enterprise.go:337` | 企业仪表盘趋势 SQL 过滤挂在无效 JOIN 上 —— 企业管理员看到全平台聚合，跨租户泄漏 | 数据泄漏 | ✅ 已修复 (eedac67) — 企业过滤移入 ur JOIN 子查询（与 usage.go:450 一致） |
| 9 | portal `vouchers_user.go:96-130` | 兑换无事务无行锁 —— 并发超兑/重复入账/无限复用 | 资损 | ✅ 已修复 — redeem/redeemPlanVoucher 全量 WithTx 包裹 + SELECT...FOR UPDATE 锁券行 + 条件 UPDATE 防超兑 |
| 10 | portal `subscriptions_subscriptions.go:404-481` | 取消订阅按套餐现价退款而非实付 —— voucher 激活（实付0）取消造钱；并发重复退款 | 资损 | ✅ 已修复 — WithTx 包裹取消+退款+订单；退款基准改查 subscription_purchases.actualPaid（无记录回退套餐价）；条件 UPDATE 防并发重复退款 |
| 11 | portal `db/schema.go:631-641` 等 | 每次启动无条件 DROP+ADD UNIQUE CONSTRAINT —— 分区大表全表重建索引阻塞写入，重启即事故 | 可用性 | ✅ 已修复 — 新增 ensureUniqueConstraint() 先查 pg_constraint 存在性，不存在才 ADD，不再 DROP；3 处（usage_records/provider_health_stats/upstream_key_usage）全部替换 |
| 12 | portal `sync.go:41-51` | SyncSecret 为空时 fail-open —— 未配置密钥环境下 `/key-pool/sync` 无鉴权返回上游密钥明文 | 安全 | ✅ 已修复 — requireSyncSecret 入口先校验 SyncSecret 非空，空值 fail-closed 并记错误日志 |
| 13 | proxy `handlers_chat.go:1317-1377` | Anthropic 原生透传流式路径完全不计费（无 preConsume/settle/usage 上报） | 资损 | ✅ 已修复 (6c9638a) — 补 preConsume(local+portal)、Anthropic SSE 事件解析(message_start/message_delta 提取 input/output/cache tokens)、settle(success/disconnect/4xx/5xx)、recordUsageAsyncFromContext、captureRetention、providerBreakers |
| 14 | proxy `handlers_audio.go:124` / `handlers_images.go:190` | audio（及疑似 images）无用户侧 usage 上报 —— 音频转写/翻译免单 | 资损 | ✅ 已修复 (6c9638a) — 新增 recordUsageFromContextByRequest（不因零 token 早退，按次计费专用），接入 audio transcriptions/translations + images handler；顺带修 images audit Billing nil 潜在 panic |

## 修复优先级建议

**第一梯队（上线阻断，立即修）**
1. 支付入账信任边界四连：#1 回调免验、#2 单位错配、#3 支付宝验签、#4 改价不核对（agent-1 模块）
2. 越权两簇：#5/#6 admin 角色提升、#7 企业跨租户审批
3. 计费闭环缺口：#13/#14 proxy 透传与 audio 路径
4. #11 启动期 DDL 炸弹、#12 sync 鉴权 fail-open、#8 跨租户泄漏、#9/#10 资金原子性

**第二梯队（首个迭代）** ✅ 已修复 (portal `ef029bb`、proxy `72a942c`)
- ✅ proxy：非流式 parse/transform 分支漏 settle、Responses API 无 portal 预扣、Claude cache token 双重扣减、pricing sync 价格只涨不跌、pre-consume 网络错误转实收、120s client/server 超时截断长流、auth 解析丢弃 8 个订阅限额字段
  - 注：非流式 parse/transform 漏 settle 与 Responses API 预扣已在 W0 批 `6c9638a` 修复（见 P0 #13 注）；cache 双扣/pricing max/pre-consume 网络错/SSE 超时/auth 8 字段在 W1 `72a942c` 修复
- ✅ portal：stats 队列丢记录、trace_id 去重失效致重试双扣、stats/billing 双倍计数、邀审批链"抢人"、企业组模型范围不校验、订阅续费事务、google 模型校验必败、可选认证跳过用户状态检查

**第三梯队（排期治理）**
- 系统性模式：吞错返 200/500、nil-rows defer panic、分页无上限/除零、CSV 公式注入、`go f(c.Request.Context())` 异步丢事件、`deleted_at IS NULL` 漏过滤、金额 float64 与 micro-USD int64 混用、软删除状态无白名单

---

# 各模块详细评审

## 1. Portal Webhook/Sync 链路（webhook_billing / webhook_common / webhook_health / webhook_preconsume / webhook_usage / sync，6 文件）—— 需关注

1. `[P0] sync.go:41-51` requireSyncSecret 在 `Portal.SyncSecret == ""` 时 fail-open：请求不带 secret 头时直接放行 —— 未配置密钥的环境下 `/key-pool/sync` 无鉴权返回**解密后的上游密钥明文**（sync.go:168-175）；而 webhook 侧中间件（webhook_common.go:93-97）空密钥是 fail-closed，两处策略相反 —— 修复：SyncSecret 为空一律 401，改用 `subtle.ConstantTimeCompare`。
2. `[P1] webhook_usage.go:1041-1072 + webhook_billing.go:397-416` processStatsBatch 已做 api_keys 统计 bulk UPDATE 与 subscription 计数器递增，processBillingBatch 的 step 8/9 又对同一批记录重复执行 —— `api_keys.total_requests/total_tokens` 双倍计数；订阅用户速率计数器双倍消耗 —— 修复：删除 billing 批的 step 8/9。 ✅ 已修复 (ef029bb) — 删除 webhook_billing 重复 step8（api_keys 批量更新）和 step9（订阅计数器自增）
3. `[P1] webhook_usage.go:537/734/1035` 去重子句 `ON CONFLICT (trace_id, created_at) DO NOTHING`，created_at 由语句内 `NOW()` 生成 —— Proxy 超时重发同一批 traceId 时去重完全不生效：重复落库 + 重复扣款 —— 修复：created_at 用记录自带时间戳，或对 trace_id 单列唯一约束。 ✅ 已修复 (ef029bb) — trace_id 去重 INSERT 用 record 自带 timestamp 而非 `NOW()`，重试不再碰撞
4. `[P1] webhook_common.go:324-330、webhook_usage.go:608-613` processStatsBatch 失败时已 RPop 的记录直接丢弃，无 requeue/死信（billing 队列有 RetryCount，stats 没有，行为不一致） —— 一次瞬时 DB 故障丢失最多 200 条/秒用量与扣款 —— 修复：失败整批 RPush 回（带重试计数）或写死信。 ✅ 已修复 (ef029bb) — `processUsageStatsQueue` 失败记录重入队列（retry 计数递增，>3 进死信队列），`processStatsBatch` 走同一重试+死信路径
5. `[P2] webhook_billing.go:156-158、webhook_usage.go:938-944` `pricingRatio = ratioMap[gid]` 未做存在性判断，map 缺 key 时零值 0.0，`cost * 0` 直接免单 —— 修复：`if r, ok := ratioMap[gid]; ok && r > 0` 否则 1.0。
6. `[P2] webhook_billing.go:298-301/382-395/406-416` 用户同时持有多种 key 类型时聚合口径错误：subscription 分支 totalTokens、企业 quota_used/告警、step 9 均按全部记录汇总（含 enterprise 记录），单条路径 processRecord 只在 enterprise 分支调 checkEnterpriseLimits —— 个人用量污染企业配额、企业记录消耗个人订阅 —— 修复：按 enterpriseIdx 分区后分别聚合。
7. `[P2] webhook_common.go:312-321 + webhook_usage.go:801-803 + webhook_billing.go:439-445` processStatsBatch 为 TraceID 空的记录生成 uuid 落库，但推入 billing 队列的是原始 JSON（traceId 仍为空） —— billing 批 `UPDATE ... WHERE trace_id=''` 不命中，扣款字段留 0，对账失真 —— 修复：用变异后的 record 重新序列化入队。
8. `[P2] webhook_preconsume.go:266-295/339-369` settle "charge" 先删 freeze 再扣款，chargeFromFreeze 失败时冻结金额永久丢失；getAndDeleteFreeze 用 GET+DEL 非原子，并发 settle 有双扣窗口 —— 修复：改 GETDEL（Redis≥6.2），charge 失败重建 freeze 或落死信。
9. `[P2] webhook_billing.go:44-53` billing 队列中 key 查不到的记录静默 `continue` 丢弃，无日志无死信 —— 用量已落库但永不扣款，隐形免单 —— 修复：Warn + 死信队列。
10. `[P2] sync.go:276-303 vs 410-429` config_version 两套实现：syncKeyPool 内联 md5 vs `/key-poll/version` 走 `db.ComputeKeyPoolConfigVersion`（注释还提到 PublishSyncEvent 第三处） —— 漂移导致 proxy 反复全量拉取或永不更新 —— 修复：收敛为一个函数。
11. `[P3] ✅ 已修复 sync.go:445 vs 506-512` models/sync 仅 `status='active'` 而 buildModelNameMap 按三态 —— 若 proxy 以 models/sync 判定可服务模型，eol 期模型提前消失（已按三态修正 buildModelNameMap，此处需确认 models/sync 消费方式）。
12. `[P3] ✅ 已修复 webhook_billing.go:358` 聚合 balance_transactions 全部用 `records[0].TraceID` 作 reference_id —— 批次流水共享他人 trace_id —— 修复：用独立批次 ID。
13. `[P3] ✅ 已修复 webhook_preconsume.go:460-465` chargeFromFreeze 个人路径扣款被余额截断后未收足部分静默（企业路径有 Warn） —— 修复：补 Warn。

**小结**：队列失败语义（#4/#3/#8 资损向）、批路径与单条路径口径漂移（#2/#6/#7）、sync 鉴权 fail-open（#1 条件触发型 P0）为三大风险面；上线前务必确认生产 PROXY_SYNC_SECRET 已配置并改 fail-closed。

## 2. Portal billing/orders/payment/invoices/quota + pkg/payment（12 文件，3851 行）—— 高风险

1. `[P0] payment.go:623-632` `handleBillingCallback` 无鉴权公开端点，`status=success` 携任意 `payment_id` 即 `completeOrder` 入账，不与网关核实 —— 自建 pending 订单后访问回调 URL 即零成本充值 —— success 分支必须经 `queryGatewayAndComplete` 查证或删除。
2. `[P0] payment.go:319-326 + stripe.go:41/paypal.go:45/wechat.go:56/alipay.go:39` `orders.amount` 为 micro-USD 原样传给各网关：Stripe/PayPal/微信按"分"、支付宝按"元"解释 —— 用户被多扣 100~10000 倍 —— 出账前统一换算为各网关最小货币单位并加锚定测试。
3. `[P0] alipay.go:117-121 + payment.go:461` 路由 `VerifySignature(body, "")` 恒传空签名，实现 `return signature != ""` —— 支付宝异步通知恒 401，付款永不到账 —— 从 HTTP 头取真实签名并用支付宝公钥验签。
4. `[P0] orders.go:343-433` `updateOrder` 允许支付会话创建后修改 pending 订单金额；回调与 `completeOrder` 均不校验实付金额（`WebhookResult.Amount` 被忽略） —— $10 下单改 $10000 付 $10 入账 —— 生成会话后锁金额，completeOrder 必须比对实付金额与币种。
5. `[P1] orders.go:442 与 payment.go:255` `(id = $1::uuid OR order_id = $1::text)`：非 UUID 的 order_id 使 `$1::uuid` 转换报错 —— 按 order_id 取消恒 500、创建支付恒 404 —— 改 `id::text = $1 OR order_id = $1`。 ✅ 已修复 (P1 ) — 
6. `[P1] billing.go:386 及 745` monthly 默认 `end_date` 用 `Format("200601021504")`（12 位）无法转 date —— 不传参时静默空数据/导出 500 —— 改 `"2006-01-02"`，查询出错返回 5xx。 ✅ 已修复 (P1 ) — Format 改为 
7. `[P1] invoices.go:57-68` `listProfiles` 缺 `deleted_at IS NULL`（软删抬头继续列出/可选默认）；`updateProfile`(:308) 同病 —— 补过滤。 ✅ 已修复 (P1 ) — listProfiles + updateProfile 补 
8. `[P1] paypal.go:43-79 + payment.go:670-711` PayPal 闭环断裂：`CreateSession` 不持久化 PayPal 订单 ID，`queryGatewayAndComplete` 拿内部 order_id 调 `GetOrder` 必败，capture 永不发生；验签仅判非空 —— 存储 PayPal order ID 映射并实现官方验签。 ✅ 已修复 (P1 +) — PayPal order ID 存入 payment_intent_id 列
9. `[P2] payment.go:770-775,790-802` 订阅路径先提交订单事务再事务外 `activateSubscription`（失败则"已付款无订阅"）；充值路径流水插入失败仅记日志仍提交 —— 激活纳入事务或可靠重试，流水失败应回滚。
10. `[P2] billing.go:871-880 与 orders.go:182-184` CSV 导出用户可控字段原样写入 —— 公式注入（`=cmd|...`） —— `= + - @` 前缀加 `'` 转义；createOrder 校验 payment_method 枚举。
11. `[P2] billing.go:83-88,171-182,432-443,580-591` limit/page_size 无上限（可全表拉取）；COUNT 查询错误被 `_ =` 吞掉致 total=0 与 records 不一致 —— clamp ≤200，COUNT 失败报错。
12. `[P2] wechat.go:62,171-175` `*resp.CodeUrl` 等直接解引用无 nil 检查 —— 异常报文 panic —— 逐字段判空。
13. `[P2] billing.go:356-378,402-406,490-492` DB 出错返回 200 空数据、`_ = rows.Scan(...)` 忽略扫描错误、COUNT 错误吞没 —— 故障被掩盖成"无数据" —— 记 error 日志并返回 5xx。
14. `[P2] payment.go:343-349` 非模拟路径 `UPDATE orders ... WHERE id = $2::uuid` 直接用 req.OrderID —— order_id 字符串查出时转换失败 500，会话已在网关创建，状态不一致 —— 用一致列更新。
15. `[P3] ✅ 已修复 config_secrets.go:53-55,22-24` encryptionKey 为空时密钥明文落库（仅注释无告警）；`IsMaskedValue` 以前缀 `"****"` 判定，合法密钥以 `****` 开头时无法更新 —— 启动告警并拒绝 enable；掩码判定结合"与现存掩码相等"。另 `getPaymentStatus`(:374) 仅支持 uuid 查单，双格式不一致。

**小结**：四个 P0 全在"入账信任边界"：回调免验、单位错配、验签恒败、改价不核对，任一上线即事故。测试仅覆盖结构体与构造路径，webhook 验签、金额换算、AES-GCM 解密均无测试。

## 3. Portal auth/profile/apikeys/devices/addresses/gdpr（9 文件，约3300行）—— 需关注

1. `[P1] apikeys_crud.go:132-133,290-301` createKey 直接采信用户传入 `rate_limit_rpm/tpm`，仅传 0 才回落默认 —— 用户可自建超限 key 绕过管理员/分组限速 —— 服务端 clamp 到组限额。 ✅ 已修复 (P1 ) — RPM/TPM clamp 到组限额
2. `[P1] apikeys_crud.go:562-568` updateKey 对 `req.Status` 无枚举白名单 —— 用户可复活被管理员禁用的 key —— 限定允许状态集合并禁止用户侧复活。 ✅ 已修复 (P1 +) — status 白名单 active/inactive 对齐 DB CHECK
3. `[P1] apikeys_crud.go:233-240,629-653` `group_priority` 全程不校验用户是否有权使用目标 group —— 可指向任意 group UUID 套取更低 pricing_ratio 或跨入 subscription/enterprise 组 —— 两处加归属校验，updateKey 补 key_type/group_type 兼容检查。 ✅ 已修复 (P1 ) — group_id 归属校验
4. `[P1] profile.go:153-188` changePassword 不做密码强度校验（auth.go:183 有）也不 bump `token_version`（auth.go:403 有） —— 改密后旧 JWT 继续有效 —— 复用强度校验并 `token_version+1`，或删除重复端点。 ✅ 已修复 (ef029bb) — changePassword 递增 `token_version` 使旧 JWT 失效
5. `[P1] auth.go:412-453` forgotPassword 无按邮箱限流（bruteForceProtection 只在 401/403/400 计数，此接口恒 200） —— 可对任意邮箱无限发重置码（邮件轰炸+SMTP 费用） —— 复用 sendCode 的 `email_rate:` INCR 限流。 ✅ 已修复 (P1 ) — 邮箱 Redis 限流 3/小时
6. `[P2] apikeys_validate.go:152-158,449-455` 未知 key 否定结果不缓存 + 接口无鉴权无限流 —— 未认证 DB DoS 放大面 —— 否定结果短 TTL 缓存 + IP 限流。
7. `[P2] apikeys_validate.go:449-455` validate 缓存只被 key 变更失效，余额/订阅/企业额度变化不失效 —— 欠费用户 TTL 内继续通行（超支）、充值用户 TTL 内仍被 402 —— 计费/充值路径加按用户缓存失效，或 balance 排除出缓存体。
8. `[P2] apikeys_crud.go:485-505` getKey 每次 GET 解密返回完整 API key —— 任何会话/XSS 可批量导出明文 key —— 默认只返回 prefix，明文仅在显式 reveal 动作（可审计）下返回。
9. `[P2] apikeys_usage.go:26-95` stats 在 `LIMIT 100` 返回集上内存累加却按"近 30 天"语义返回 —— 超 100 条系统性少计 —— 用独立聚合 SQL。
10. `[P2] apikeys_common.go:21 / apikeys_validate.go:56 / apikeys_crud.go:453,469 / apikeys_usage.go:40` 金额链路全部 float64 —— 违反 micro-USD int64 约定，大额精度丢失 —— 统一 int64。
11. `[P2] profile.go:190-234 / gdpr.go:79-141` 注销/删除不 bump token_version 不吊销 JWT —— `pending_deletion` 用户存量会话仍可调接口 —— 停用时 `token_version+1`。 ✅ 已修复 (ef029bb) — gdpr 删除账号递增 `token_version`
12. `[P2] apikeys_crud.go:555-653` updateKey 一次最多 5 条独立 UPDATE 无事务 —— 中途失败留中间态且审计只记一次 —— 单事务动态 UPDATE；addresses.go:159-229 同构（10 条）。
13. `[P3] ✅ 已修复 devices.go:95-116` recordDevice 先全量置 is_current=false 再无指纹去重 INSERT，无事务 —— 表无限膨胀、并发出现零/多 current —— 按 (user_id,指纹) UPSERT 包事务。
14. `[P3] ✅ 已修复 auth.go:279-285` 登录对不存在用户直接 401，无 dummy bcrypt —— 计时侧信道邮箱枚举 —— 对固定 hash 做一次比较。
15. `[P3] ✅ 已修复 apikeys_crud.go:674-690 / auth.go:190,211 / apikeys_usage.go:32-35` 吞错组：deleteKey 对不存在 key 静默成功（应 404）；register 吞邮箱查重与默认分组查询错误；getKeyUsage 失败返回 200 空数据。

**小结**：apikeys 子域限速/分组/status 三处用户可控输入缺服务端约束是资损与越权主入口；认证域限流缺口与 token_version 不一致次之；金额 float64 与 validate 缓存失效策略需与 proxy 侧对齐。

## 4. Portal admin_* 管理面（12 文件，7711 行）—— 高风险

1. `[P0] admin_users.go:245` `createUser` 对 role 无白名单/敏感操作校验 —— 任何 RequireAdmin 管理员可直接创建 super_admin/system_admin，完全绕过 updateUser 的 `CanPerformSensitiveOps`+`confirm_role` 双重闸门 —— 权限提升 —— 与 updateUser 一致限制并做敏感校验与人数上限。
2. `[P0] admin_users.go:297/326` `promoteToSuperAdmin`/`demoteSuperAdmin` 无敏感操作校验、无自身 ID 比对（可自助提权）、无 MaxSuperAdmins 名额检查、无审计 —— 越权提升且不可追溯。
3. `[P1] admin_keypool.go:834` `exportKeyPoolKeys` 查询 `WHERE 1=1` 未过滤 `deleted_at IS NULL`，软删 key 仍被解密导出明文；`getKeyPool`(:119) 与 `listUpstreamKeys`(:24) 同病 —— 已删除凭证明文外泄 —— 三处统一补过滤。 ✅ 已修复 (P1 batch `5131561`) — listUpstreamKeys/getKeyPool/exportKeyPoolKeys 三处查询补 `WHERE deleted_at IS NULL`
4. `[P1] admin_keypool.go:809` 导出密码允许 `c.Query("password")` 且路由注册 GET —— 密码进入 URL/access log/浏览器历史 —— 仅 POST body，移除 query 回退与 GET 路由。 ✅ 已修复 (P1 ) — 移除 c.Query 回退 + GET 路由
5. `[P1] admin_users.go:133` `updateUser` 直接 `UPDATE users SET balance`，不写 balance_transactions 流水、不校验 RowsAffected、字段错误仅记日志照返 success —— 余额变更无账可查，与对账汇总必然对不平 —— 事务内写流水+改余额，失败 500。 ✅ 已修复 (P1 ) — balance 变更包事务写 balance_transactions
6. `[P1] admin_usage.go:622（及 :675）` `date_to` 缺省 `Format("200601021504")`（14 位）与 date 列比较 PG 解析失败 —— 不传 date_to 时 summary/export 必 500 —— 改 `"2006-01-02"`。 ✅ 已修复 (P1 ) — Format 改为 
7. `[P1] admin_reconciliation.go:241-244（同型 :609-612、:634-637）` 查询出错仅记日志后 `defer rows.Close()`；pgx 出错返回 nil rows，退出时 nil 调 Close 直接 panic —— DB 抖动打崩进程 —— 出错即 return。 ✅ 已修复 (P1 ) — 出错即 return 再 defer
8. `[P1] admin_upstream_pricing.go:144` `updateUpstreamPricing` 无条件 `add("currency", req.Currency)`，请求不带时把既有值覆写为空串（其余字段均指针判空） —— 上游成本币种被静默清空 —— 改 `*string` 或空串跳过。 ✅ 已修复 (P1 ) — currency 条件 add
9. `[P2] admin_providers.go:23/53` `providers.api_key` 明文入库且 listProviders 原样回显 —— 与 upstream_keys 全链路 AES-256-GCM 约定不一致 —— 加密存储、列表脱敏。
10. `[P2] admin_providers.go:60 等十余处` `go r.db.PublishSyncEvent(c.Request.Context())`：goroutine 持有请求 ctx，handler 返回即 cancel，同步事件大概率丢失（对比 validateKeyAsync 正确用 `context.Background()`） —— Proxy 配置同步偶发不生效 —— 一律 `context.Background()`。
11. `[P2] admin_providers.go:82-119` `updateProvider` 拆 6 条独立 UPDATE，非事务、无存在性检查、错误吞掉照返 success —— 单条动态 UPDATE + RowsAffected + 失败 500。
12. `[P2] admin_reconciliation.go:114/136/158` 发票审批/驳回/完结有状态前置条件却不查 RowsAffected —— 状态机空转仍 success —— 0 行时 409/404。
13. `[P2] admin_users.go:225` `deleteUser` 审计 actor 传成被删用户 ID（同文件其他操作均传当前管理员） —— 审计归因错误。
14. `[P2] admin_keypool.go:1054` `validateKeyPoolKeysBatch` 请求内串行校验最多 100 key × 3 重试 × 15s —— 单请求可阻塞数十分钟 —— 异步任务或收敛 limit/超时。
15. `[P2] admin_usage.go:73/100/126-130` `getRateLimitUsage` 把 micro-USD 的 `SUM(total_cost)` 扫进 float64 原值返回（同包对账接口均用 QuotaToUSDFloat） —— 单位不一致，前端按 USD 渲染将放大 10⁶ 倍 —— 统一转换或明确字段名。

**小结**：用户/角色管理两条越权提升路径（P0×2）+ 余额直改无流水构成资损口径断裂最优先；keypool 软删过滤与导出密码走 URL 属凭证处理硬伤；"记日志照返 success"、PublishSyncEvent 上下文竞态、nil-rows panic 三类系统性模式建议横向整改。次要项：handleReconcileUpload 查重非原子、exportLimits map 只增不清、syncOpenRouterLive 无超时、getModelsStatus 漏 eol_pending、importPricingCSV 透传 err.Error()、价格字段无负值校验。

## 5. Portal enterprise_* + groups（12 文件）—— 高风险

1. `[P0] enterprise_members.go:1172-1176,1281` `approveInvitation`/`rejectInvitation` 全程用邀请记录自身的 enterprise_id，从未与调用方上下文比对 —— 任意企业管理员可审批/拒绝**其他企业**的邀请，审批还在事务内向目标企业写成员并覆盖 `users.enterprise_id` —— 跨租户越权写 —— 查询追加 `AND enterprise_id = $<caller>`。
2. `[P1] enterprise_members.go:890-898,651-674,1221-1242` `acceptInvitation`（autoJoin）与两级 approve 对已存在用户不校验当前企业归属，直接 `UPDATE users SET enterprise_id=...` —— 可把企业 A 的用户静默"抢走"到 B，A 的 members 行残留、key assignee 悬空；existing-user 接受路径不校验密码，仅凭邮件链接完成账号迁移 —— 加入前查 `enterprise_id IS NULL` 否则 409；existing-user 要求登录态或密码确认。 ✅ 已修复 (ef029bb) — `acceptInvitation`/`inviteMember` 校验目标用户未绑定其他企业，防跨租户"抢人"
3. `[P1] enterprise_groups.go:82-89,111-133,213-217` `createEnterpriseGroup` 读出平台组 models（platformModels）后从未使用，创建/更新企业组不校验 `req.Models ⊆ 平台组.models` —— RPM/TPM 有上限约束而模型范围没有，企业可建含任意模型的组并经 createEnterpriseKey 继承，绕过平台组供应边界 —— 做子集校验。 ✅ 已修复 (ef029bb) — create/update enterprise group 校验请求模型为平台模型子集
4. `[P1] groups.go:39-43,659-698` `getGroupModelsWithStatus` 挂公开路由（仅 RequireAuth），对任意 groupID 返回 product_id、input_price、output_price —— 与 enterprise_groups.go:320-322"绝不向企业用户暴露 product_id"直接冲突，任意登录用户可枚举全部组的上游模型标识与成本价 —— 公开版剥离敏感字段或仅管理员可见。 ✅ 已修复 (P1 ) — 剥离 product_id/价格字段
5. `[P1] enterprise_reconciliation.go:24-26,118,155,197,236` 默认 `to = Format("200601021504")` 参与 `$n::date` 转换 —— PG15 实测报错，不传 `to` 时 5 个端点全 500 —— 改 `"2006-01-02"`。 ✅ 已修复 (P1 ) — Format 改为 
6. `[P1] enterprise_members.go:150-163 vs enterprise_billing.go:253-257` `topupMemberQuota` 以 `quota_used += n` 记账并把配额转入 users.quota_total/remaining；`ResetMonthlyBilling` 每月清零企业 quota_used/credit_used，但无任何代码回收成员侧余额 —— 跨月后配额池"忘记"已发放量，可重复发放超发（资损向） —— 重置时同步回收/对账，或改独立 allocation 台账。 ✅ 已修复 (ef029bb) — `ResetMonthlyBilling` 同时清零成员 quota 余额，不止企业聚合
7. `[P1] enterprise_keys.go:22-26` `listEnterpriseKeys` 缺 `deleted_at IS NULL` —— 软删密钥仍出现在列表（成员可见）。 ✅ 已修复 (P1 ) — 补 
8. `[P2] enterprise_common.go:337-343,512; enterprise_billing.go:62-65; enterprise_reconciliation.go:60,146` 金额单位不一致：部分 JSON `cost` 为原始 micro-USD（float64），对账 CSV 与 usage_by_member 除以 1e6 输出 USD —— 两套单位字段名无区分 —— 统一 int64 micro-USD 并标注。
9. `[P2] enterprise_application.go:54-67` 拒绝冷却期把 `reviewed_at::text` 截断后用 UTC 布局解析：零小数秒时带时区后缀解析失败、冷却**静默跳过** —— 反滥用机制失效风险 —— 直接扫入 time.Time。
10. `[P2] enterprise_members.go:31-79,1134-1154; enterprise_common.go:993-1041` 成员移除/退出/企业删除均为 3~5 步顺序写库无事务 —— 失败即分裂状态 —— WithTx 包裹。
11. `[P2] enterprise_members.go:503-509,691-697,1256-1262,1514-1520,1593-1599; enterprise_common.go:1127-1132` 多处邮件 goroutine 内调用 `GetLocale(c)` 捕获请求级 `*gin.Context` —— gin 禁止跨 goroutine 使用原 Context（数据竞争） —— 进 goroutine 前取好值。
12. `[P2] enterprise_groups.go:209-233,424-425` `updateEnterpriseGroup` 各字段单独 `_ = Exec` 吞错、status 无白名单、不刷 updated_at；deleteEnterpriseGroup 吞错照返 success —— 检查错误、校验枚举、合并 UPDATE。
13. `[P2] enterprise_billing.go:181-210` `topupEnterprisePool` quota/credit 两段 UPDATE + credit_log INSERT 各自独立无事务 —— 部分生效、台账断裂 —— 单事务。
14. `[P2] enterprise_batch.go:214-217` 导入失败把 `err.Error()`（事务/约束内部信息）拼进响应 —— 内部细节外泄 —— 仅记日志返回通用消息。
15. `[P2] groups.go:321,344-350` 管理端 `updateGroup` 改 status 不做密钥级联禁用（企业端有级联+缓存失效），且吞 JSON 绑定错误 —— 对齐级联或明确文档差异。

**小结**：租户边界（邀请审批链跨企业越权写与"抢人"）为上线前必修簇；账目一致性（quota 预分配与月度重置断裂、金额单位混用、topup 无事务）次之；信息暴露面（公开路由泄露价格/模型标识、软删漏过滤）再次。

## 6. Portal models/vouchers/subscriptions/announcements/audit 等（24 文件）—— 需关注

1. `[P0] vouchers_user.go:96-130` 兑换全流程无事务无行锁：先查状态/次数 → 加 gift_balance → 再更新 used_count（基于过期读值）→ 插流水 —— 并发兑换同一码双双通过校验超兑/重复入账；余额已加 voucher 未核销时无限复用 —— 单事务 + `SELECT ... FOR UPDATE`，依赖 `voucher_uses(voucher_id,user_id)` 唯一约束兜底。
2. `[P0] subscriptions_subscriptions.go:404-481` 取消订阅退款基准错误 + 竞态：退款额 = 套餐**当前**价 × 剩余配额比，而非实付 —— voucher 激活（实付0）取消可把套餐价折算进真实 balance，凭空造钱；SELECT→UPDATE 非事务、RowsAffected 未检查，并发取消重复退款 —— 以订单实付为准，取消+退款同事务。
3. `[P1] models_sync.go:234-272,316,383-390` `fetchProviderModels` SSRF 与密钥泄露：管理员传入的 base_url 优先于 DB 配置，且未显式给 key 时解密 DB 密钥随请求发往该任意 URL（凭据外泄通道）；google 时密钥拼进 URL `?key=`，失败后含完整 URL 的 lastErr 既写日志又随 502 返回 —— base_url 限定已登记域名/白名单，google key 不入 URL，错误信息不回传含密钥 URL。 ✅ 已修复 (P1 batch `5131561`) — 自定义 base_url 时禁止解密 DB key；google key 改用 x-goog-api-key header；502 不再回传 lastErr.Error()
4. `[P1] models_sync.go:356-386` 重试 bug：`lastErr` 只在失败时赋值、成功不清零 —— 先败后成时循环 break 后仍返回 502，且合法 resp 未注册 defer Close（连接泄漏） —— 成功时 `lastErr = nil`。 ✅ 已修复 (P1 batch `5131561`) — 成功时 `lastErr = nil`；502 返回通用消息不回传 err.Error()
5. `[P1] subscriptions_renewal.go:55-86` 自动续费：扣款前事务外读 balance 判断，事务内 `GREATEST(balance - price, 0)` 兜底 —— 并发消费致余额不足时按 0 钳制，用户不足额获得全额续费；续期硬编码 `INTERVAL '30 days'` 无视 duration_days；NOT EXISTS 防重在事务外可重复扣款 —— `UPDATE ... WHERE balance>=$1` 校验行数，续期取套餐字段。 ✅ 已修复 (P1 ) — balance 校验入事务 + duration 从套餐读取
6. `[P1] models_common.go:121-151` `cleanupSingleModelFromGroups` 步骤顺序错误：Step1 先把模型从 user_groups.models 移除，Step4 notifyAffectedUsers 再按 models LIKE 查受影响用户 —— 永远查不到人，停服/EOL 通知邮件从未发出（三个入口均受影响） —— 先查收件人再清理。 ✅ 已修复 (P1 batch `5131561`) — notifyAffectedUsers 提到清理之前（Step 1 → Step 2-5），注释说明顺序原因
7. `[P1] settings.go:229-235 vs vouchers_admin.go:433-438 / vouchers_user.go:302-317` 邀请奖励两个管理面单位差 1e6：getSettingJSON 默认 `"5"/"2"`（美元），generate() 与 vouchers_admin.getSettings 按 micro-USD 读写 —— 管理员存 "10" 可能变成 10 micro-USD 或放大百万倍 —— 统一 micro-USD int64。 ✅ 已修复 (P1 ) — 默认值统一 micro-USD
8. `[P2] vouchers_user.go:177-258` `redeemPlanVoucher` 先核销 voucher 再创建订阅，订阅 INSERT 失败时券已消耗订阅未得；延期分支配额按比例折算但时长始终加完整 duration_days，口径不一致 —— 纳入事务。
9. `[P2] subscriptions_subscriptions.go:142-155` `updateUserSubscription` 查出 userID 却未使用，直接 `KEYS validate:*` 全量扫描删除**所有用户**校验缓存 —— 生产 Redis KEYS 阻塞 + 全局缓存雪崩；且管理员直改 quota_remaining 无审计 —— 按用户精准失效（SCAN），quota 调整写审计。
10. `[P2] audit.go:43-56,180-193` 角色上下文键名不一致：middleware.Auth 写 `c.Set("role")`，list/export 读 `c.GetString("user_role")` —— 恒空，super_admin 看已删除/历史日志特权静默失效 —— 统一键名。
11. `[P2] announcements.go:150-206`、`audit.go:33-142,260-316` 分页零校验：`limit=0` 整型除零 panic；负 page 负 OFFSET 500 —— 钳制 `1<=limit<=上限`、`page>=1`。
12. `[P2] audit.go:230-256` 审计导出 CSV 无转义：message 含用户可控内容 —— CSV 公式注入 + 逗号/换行破坏格式 —— 用 encoding/csv 并加 `'` 转义。
13. `[P2] dashboard.go:27,49-99` `/admin/dashboard` 仅 RequireAuth 无 RequireAdmin，且统计不按用户过滤 —— 普通登录用户可拿全平台经营数据 —— 按 userID 过滤或强制 admin。
14. `[P2] channel_health.go:161-166,180-184`、`dashboard.go:347-352` 查询错误被 `_` 吞掉后 `defer rows.Close()`：Query 出错 rows 为 nil，退出时 nil 解引用 panic —— 先判 err 再 defer。
15. `[P2] settings.go:254-281` `PUT /settings/:key` 无白名单/格式校验：可写入任意 key —— 包括把 smtp_password 以明文写入（绕过 updateSMTPSettings 加密路径，后续 Decrypt 失败 SMTP 瘫痪且明文落库），也绕过 deadSettings 拦截 —— 敏感 key 黑名单 + 格式校验。

**小结**：资金路径原子性（兑换/退款/续费三处跨请求窗口 + 退款基准错用现价）为当前最需封堵的资损面；fetchProviderModels 密钥外泄通道与重试 bug 次之；吞错后 nil-rows defer panic、分页除零 panic、CSV 注入为中低危；测试对 redeem 并发、取消退款、自动续费无 DB 级/并发用例。

## 7. Portal internal/services + internal/db（14 文件，schema.go 3270 行全量）—— 需关注

1. `[P0] db/schema.go:631-641（同型 1086-1095、1227-1236）` InitSchema 每次启动无条件 `DROP CONSTRAINT IF EXISTS` + `ADD CONSTRAINT ... UNIQUE`（usage_records / provider_health_stats / upstream_key_usage） —— ADD UNIQUE 全表扫描重建唯一索引并持锁阻塞写入；usage_records 是计费最热写入路径且为分区大表，每次重启/滚动发布阻塞写入数分钟到数小时，双实例并发启动相互冲突 —— 改为 pg_constraint 存在性检查（schema.go:329-341 已有先例），只在缺失时创建；或迁移到带 advisory lock 的一次性 migration。
2. `[P1] services/reward.go:181,194` ProcessInvitationCode 中 inviteeBalanceBefore/inviterBalanceBefore 在 gift_balance 已 UPDATE **之后**才 SELECT —— voucher_uses 的 balance_before 实为加钱后的值，审计流水金额虚高一倍，对账必然不符（ApplyWelcomeBonus:50 先读后写正确） —— 移到 UPDATE 之前或用 RETURNING。 ✅ 已修复 (P2 batch `a244ea9`) — 整个 ProcessInvitationCode 流程（含余额快照）已包入 WithTx 事务，快照读取在 UPDATE 之前
3. `[P1] services/pricing.go:281,296` 固定费缺陷：flat 模式 `Fixed: prices.FixedRequest` 不乘 RequestCount（聚合记录少收）；tiered 模式 `Fixed: 0` 直接丢弃 —— 按次计费模型固定费长期少收 —— flat 乘 RequestCount，tiered 计入或禁止配置。 ✅ 已修复 (P1 ) — flat 乘 RequestCount
4. `[P1] db/schema.go:636` usage_records 唯一约束 `UNIQUE (trace_id, created_at)`（分区表被迫包含分区键） —— "trace_id 唯一"在 DB 层不成立：同 trace_id 不同 created_at 产生重复记录、重复扣费 —— webhook 入库须保证重试沿用原始 created_at，考虑独立去重表或 trace_id 哈希锁。 ✅ 已修复 (P1 ) — UNIQUE(trace_id) 替代 (trace_id, created_at)
5. `[P2] services/pricing.go:400-423` MatchRule 缓存 key 含分钟，从不清理过期项，仅规则变更时整体清空 —— 每个活跃组合每分钟新增一条，内存单调上涨 —— 写入时淘汰过期项或改 TTL LRU。
6. `[P2] services/billing.go:141-154` GetPricingRatioBatch 查询失败完全静默（无日志无 metrics），缺失分组按 1.0；ratio=1.0 的组永不进缓存每次回表 —— DB 抖动期按错误倍率静默计费 —— 记 Warn；以成功集合为准写缓存含 1.0。
7. `[P2] services/billing.go:75-86` CalculateCost 计价失败返回 0 继续放行（fail-open） —— 规则缺失/解析错误期间所有用量按 0 计费，仅 Warn 无告警无标记 —— usage_records 标记 `pricing_error` 便于补收；全局 fallback 规则（seed.go:197）应不可删除。
8. `[P2] db/migrate.go:26-101` Migrate/InitSchema 未持 advisory lock（lock.go 有 TryAdvisoryLock 未用） —— 多实例首发并发 DDL 与 schema_migrations 插入，PK 冲突或 DDL 竞争启动失败 —— 入口统一 `pg_advisory_lock`。
9. `[P2] db/retention.go:57-62` RunRetentionCleanup 任一规则失败即 return —— 排前的表失败阻断后续全部清理及 runGDPRErasure，GDPR 定时擦除被静默跳过 —— 单条失败记录后 continue，GDPR 独立执行。
10. `[P2] db/retention.go:24-38` 保留期全部硬编码（audit_logs 2555 天等），seed.go:155-157 播种的 log_retention_days/audit_log_retention_days 从未被读取 —— 设置页修改保留期无任何效果 —— 清理时优先读 system_settings。
11. `[P2] db/gdpr.go:13-51` AnonymizeUser 宣称"permanently erases PII"，但 audit_logs（user_email/IP）、user_devices（IP/位置）、email_notifications、voucher_uses 仍保留可关联 PII；且 `DELETE FROM usage_records` 物理删除计费凭证 —— GDPR 擦除不彻底 + 财务审计链断裂 —— 明确各表擦除策略（匿名化而非保留），usage_records 改脱敏 user_id 而非整行删除。
12. `[P2] db/seed.go:235-238` SEED_TEST_ENTERPRISE=true 且未设 TEST_ENTERPRISE_PASSWORD 时用硬编码默认密码 `TestPass123!` 创建 enterprise_admin —— 误入生产即已知凭据高权限账号 —— 未显式提供时拒绝播种并 Error。
13. `[P2] db/db.go:348-361` `Auth.EncryptionKey == ""` 时静默把 key_encrypted 当明文使用并同步给 Proxy —— 配置缺失时上游密钥明文存储/同步，无任何告警 —— 生产模式 fail-fast，至少 Error 日志。
14. `[P3] ✅ 已修复 services/pricing.go:780-784,827-833` CSV 导入对 tiers/time_rules JSON 解析失败、float 解析失败均静默吞掉按 0 价/空 tiers 落库（行也不计 failed） —— 运营 typo 产出 0 价规则大面积少收 —— 计 failed 并返回行号原因。
15. `[P3] ✅ 已修复 db/db.go:67-69,89` BalanceUpdateMessage.Balance 用 float64（约定应 int64 micro-USD）；DSN 用 fmt.Sprintf 拼接未转义 user/password（含 `@/:#` 即解析失败） —— 消息体改 int64；DSN 用 `url.UserPassword`。

**小结**：schema.go 启动期 DDL 策略（#1）是最现实的可用性炸弹；计费正确性（#2/#3/#7）叠加构成持续资损面，建议统一"计费异常"可观测性；合规与保留策略（#10/#11）需对齐。安全面整体较好（全范围参数化，未见注入面）。

## 8. Portal internal/web + internal/query（30 文件）—— 高风险

1. `[P0] query/enterprise.go:337` 企业仪表盘 DailyUsage 趋势 SQL 把企业过滤放在第二个 LEFT JOIN（`api_keys k ... AND k.enterprise_id=$1`），而 k 的列从未被 SELECT，ur 的 JOIN 条件只有时间窗 —— 企业管理员看到的"本企业趋势"实为**全平台所有租户聚合**，跨租户泄漏且图表完全错误（对比 usage.go:450 正确写法） —— 过滤移入 ur 的 JOIN 条件。
2. `[P1] query/admin.go:285` GetAdminModelsData 主查询 `WHERE deleted_at IS NULL` + providerWhere（生成 `" WHERE provider LIKE ..."`）产生双 WHERE 语法错误；web 层固定传参，错误又被 `if err == nil` 吞掉 —— `/admin/models` 与 `/admin/models-aws` 列表恒为空且无报错 —— providerWhere 改 `" AND ..."`，主查询失败应返回 err。 ✅ 已修复 (P1 ) — providerWhere WHERE→AND，错误不再吞
3. `[P1] web/actions.go:56-59 + web/admin_handler_common.go:775` 创建 API key/企业 key 后明文经 `?message=...sk-wgit-...` 或 `?new_key=...` 放在 302 URL 中 —— 密钥进入浏览器历史/nginx 日志/Referer 头 —— 一次性展示改走 HTMX partial 或 Redis 一次性读取。 ✅ 已修复 (P1 ) — rawKey 移出 redirect URL
4. `[P1] web/admin_handler_keys.go:129-135` google 分支 `req, _ = http.NewRequest(...)` 忽略错误；base_url/apiKey 含非法转义时 req 为 nil，`client.Do(nil)` 在无 recover 的后台 goroutine 中 nil 解引用 panic —— 单个畸形 base_url 可使整个 portal 进程崩溃 —— 检查 err 并落库 invalid；goroutine 入口加 recover。 ✅ 已修复 (P1 ) — http.NewRequest 错误检查
5. `[P1] web/actions.go:314-328` HandleUpdateUserRole 仅要求 system_admin|super_admin，却允许把任意用户改为 super_admin —— system_admin 可自助加冕，绕过 /super/admins 层级设计；且无审计 —— 目标 super_admin 时要求操作者为 super_admin，补审计。 ✅ 已修复 (P1 ) — 自身比对 + super_admin 层级校验
6. `[P2] web/actions.go:308`、`admin_handler_keys.go:88,165,175` `go h.db.PublishSyncEvent(c.Request.Context())`：handler 返回 ctx 即取消，同步事件随机丢失 —— 用 `context.WithoutCancel` 或 `context.Background()`。
7. `[P2] web/handler.go:178-191,228-238` renderMu 全局互斥把所有页面渲染串行化；RenderPartial 先写共享 `h.currentLocale` 再释放后渲染 —— 并发请求语言串扰窗口 + 吞吐瓶颈 —— locale 改为模板执行期参数，消除共享可变状态与全局锁。
8. `[P2] query/usage.go:354,383-386`、`dashboard.go:95,167`、`apikeys.go:104` 时区基准三套混用：趋势用 `time.Now().UTC()` 比较 timestamptz（按会话时区转换）、"今天/本月"用 DB CURRENT_DATE、dashboard 用服务器本地时间 —— 非 UTC 部署或用户跨时区时口径错位 —— 统一以 DB 会话时区或显式用户时区。
9. `[P2] web/data.go:496-499` ExportAdminAuditLogs 的 csvEscape 只做引号转义，未中和 `= + - @` 前导 —— audit_logs.message/user_email 可被用户输入污染，管理员 Excel 打开触发公式注入 —— 前缀 `'`。
10. `[P2] query/usage.go:216-220` GetUsageRecords 的 EndDate 用 `created_at <= $N`：date-only 输入按 00:00:00 比较，**截止日全天被排除**；billing.go:133/319 用 `< $N::date + INTERVAL '1 day'`（含当天） —— 两处口径相反，用量日志必漏尾日 —— 统一 `< date+1`。
11. `[P2] web/auth.go:73` HandleLogin 无登录频率限制（HandleSendCode 有），可无限爆破密码；注册验证码（6 位数字、10 分钟有效）无尝试上限可在线爆破 —— 登录按 email+IP 限流锁定；验证码错误次数计数作废。
12. `[P2] query/` 系统性吞错：plans.go:58、apikeys.go:236、billing.go:63、monitor.go:259/472/619、admin.go:131/483 等主查询失败 `return 空数据, nil`；handler 层又普遍 `data, _ :=` —— DB 故障渲染成全零/空页面，无日志无告警 —— 至少 Error 日志，关键页面 500。
13. `[P2] query/apikeys.go:212,214`、`groups.go:153,190`、`enterprise.go:569` 门户侧"可用模型"列表与计数只取 `status='active'`，与可服务三态约定不符 —— eol_pending/eol 期模型在用户/企业端被隐藏，用户所见与 proxy 实际放行不符 —— 改 `IN ('active','eol_pending','eol')`。
14. `[P3] ✅ 已修复 query/dashboard.go:454-458` 管理驾驶舱对账差值用硬编码单价估算上游成本，与 service_models 真实定价脱节长期失真；dashboard.go:193 标签键 `time.Now().Format("2006-")+label` 跨年查不到上年 12 月 —— 复用真实定价；趋势键存完整日期。
15. `[P3] ✅ 已修复 web/pages.go:617`、`admin_handler_common.go:217,258,277` 审计取 `c.GetString("email")` 但中间件写 `user_email`（session.go:119） —— 设置项增删改、审计导出的审计记录邮箱恒为空 —— 改 `user_email`。另：创建企业 key 的 assignedTo 未校验本企业成员（编辑路径有）；重置密码最小 6 位与注册 8 位不一致。

**小结**：query 层 SQL 拼接缺陷（P0 跨租户泄漏 + P1 双 WHERE）出于零覆盖（仅 4 个编译冒烟用例）；敏感数据处理（明文 key 进 URL、goroutine 裸 panic、同步事件已取消 ctx）影响密钥池链路可靠性；口径一致性（时区三基准、日期边界互斥/包含相反、active-only 与三态不符）需统一。

## 9. Portal 基础设施（pkg 9 子包 + middleware + cmd/server，22 文件）—— 需关注

1. `[P1] pkg/validation/model_validator.go:389` google 分支 setHeaders 只设 Content-Type，buildTestURL(:364) 也未在 URL 拼 `?key=` —— Google 模型校验请求必然未认证，永远无法通过自动校验；叠加 background.go:132 每日 CleanupAllInvalidModels，存在被误判 invalid 后级联清理风险 —— google 分支补 `?key=`，同步修正固化了错误行为的 model_validator_test.go:278。 ✅ 已修复 (ef029bb) — google 分支测试 URL 用 `?key=api_key` 签名
2. `[P1] pkg/mail/mail.go:215` `sendWithTLS` 裸 `tls.Dial` 无任何超时/deadline（:192 的 smtp.SendMail 同样） —— SMTP 半开时发送 goroutine 永久阻塞，单 worker 邮件队列被卡死后验证码/重置邮件全部停摆且无监控 —— `net.Dialer{Timeout}` + `conn.SetDeadline`。 ✅ 已修复 (39420b3) — `tls.Dial` 换为 `tls.Dialer{NetDialer: &net.Dialer{Timeout:30s}}` 并 `conn.SetDeadline(2m)`；`smtp.SendMail` 路径替换为自建 `sendWithStartTLS`（`net.DialTimeout` + op deadline + STARTTLS），半开连接不再阻塞
3. `[P2] middleware/auth.go:153` `OptionalAuth` 校验签名通过后直接写 user_id/email/role，跳过 RequireAuth 的用户存在/封禁(:117)与 token 版本(:131)检查 —— 被删/被 suspend 用户旧 token 仍可携带 role；若特权路由组合 OptionalAuth()+RequireAdmin 即越权 —— 复用相同用户状态校验或约定+lint。 ✅ 已修复 (ef029bb) — `OptionalAuth` 现在校验用户存在性/封禁/`token_version`，不再信任 claims；`RequireAuth` 不再跳过 `TokenVersion=0` 的旧 token
4. `[P2] pkg/jwt/jwt.go:38` `Generate` 的 tokenVersion 可变缺省 0，auth.go:131 仅在 `claims.TokenVersion > 0` 时查库比对 —— 未传版本号的签发路径产出改密后不可吊销的 token，绕过是静默的 —— tokenVersion 必传，或对 ==0 的旧 token 按当前库值校验。 ✅ 已修复 (39420b3) — `Generate` 签名由变参 `tokenVersion ...int` 改为必传 `tokenVersion int`；web/login SQL 增查 `COALESCE(token_version,0)` 并显式传入；register 路径传 0（新用户）；所有测试调用方同步更新。配合 W1 的 Verify 侧修复（RequireAuth 不再跳过 `TokenVersion=0`），改密吊销链路完整闭合
5. `[P2] pkg/queue/queue.go:103` handler 返回错误仅打日志，job 已被 BRPop 弹出即永久丢失，无重试/DLQ —— SMTP 瞬时故障验证码邮件静默丢失 —— 失败 LPUSH 回（带重试上限）或死信。
6. `[P2] pkg/audit/audit.go:120` 缓冲满时 enqueue 直接丢弃审计条目（仅 Warn） —— 计费/密钥管理等合规敏感操作高并发下审计空洞，且无指标 —— 丢弃加计数器，关键类别同步写库兜底。
7. `[P2] middleware/cors.go:75` `sameOrigin` 直接信任可伪造的 `X-Forwarded-Proto/Host` 重建"同源" —— 前置代理不剥离这两个头时攻击者可伪造 Origin 绕过白名单 —— 仅在 gin trusted proxies 验证时采纳，或显式配置站点 origin。
8. `[P2] cmd/server/server.go:21` `http.Server` 未设 ReadHeaderTimeout/ReadTimeout/WriteTimeout/IdleTimeout —— 直连暴露易受 slowloris —— 按基线配置超时。
9. `[P2] cmd/server/background.go:57` 定时任务用 `context.WithoutCancel` 保批更新完整，但 server.go:44-51 关停 10s 后即 `database.Close()` 不等待后台任务 —— 任务仍在写库中途被断连（月度账单重置、自动续费） —— 关停先停 ticker、WaitGroup 等待在途任务（带独立超时）再关 DB。
10. `[P2] pkg/telemetry/telemetry.go:152` `tracingTransport` 将 `req.URL.String()`（含 query）整体写入 span 属性 —— Google `?key=`、webhook 签名参数等凭据落入 tracing 后端长期留存 —— 仅记录 Scheme+Host+Path 或 query 脱敏。
11. `[P2] pkg/mail/mail.go:273` `buildMessageWithContentType` 的 From/To 头原样拼接未做 CRLF 消毒（本文件已有 sanitizeSubject 未用于收发件人） —— 用户可控邮箱字段含 `\r\n` 即 SMTP 头注入伪造任意邮件 —— 统一消毒或拒绝控制字符。
12. `[P3] ✅ 已修复 cmd/server/router.go:64` `/api/health` 未认证且把 err.Error()（含内网地址实例信息）返回 —— 对外仅返回 status。
13. `[P3] ✅ 已修复 middleware/auth.go:41` `SanitizeInput` 在 io.ReadAll 出错（含超 10MB 截断）时不拦截反放行，后续 handler 读残缺 body；且重写后未同步 Content-Length —— 读体失败即 Abort(413/400)。
14. `[P3] ✅ 已修复 pkg/crypto/encryption.go:26` 无盐 SHA-256(passphrase) 作 KDF 无拉伸；ErrInvalidKey 定义后从未使用（文档称必须 32 字节但实现接受任意长度） —— 删除或启用校验，注释明确 KDF 语义。
15. `[P3] ✅ 已修复 cmd/server/router.go:166` `/api/service-models/public` 中 `_ = rows.Scan(...)` 忽略错误、循环后未查 rows.Err() —— 列漂移/连接中断时公开目录静默返回残缺数据 —— 检查并 500。

**小结**：google 凭据缺失（与后台清理级联）、邮件链路无超时无重试、OptionalAuth 与 token 版本两处"静默放行"为主要风险；CORS/HTTP 超时/SMTP 头消毒属标准基线缺口。

## 10. Proxy handlers_chat / handlers_common / handlers_responses（3 文件，5053 行全量）—— 高风险

1. `[P0] handlers_chat.go:1317-1377` Anthropic 原生透传流式路径完全不计费：无 preConsume、无 Settle，且从未调用 recordUsageAsyncFromContext（仅 recordUpstreamUsageAsync(nil) + 无 token 的 Audit） —— Claude 原生客户端默认走流式，该路径上游费用全部无法回收，直接资损 —— 参考 handleAnthropicStreamingResponse(1617-1910) 补齐 preConsume/settle 与流式 usage 解析（或按预估值保底结算）。 ✅ 已修复 (6c9638a) — 补 preConsume(local+portal)、Anthropic SSE 事件解析(message_start/message_delta 提取 input/output/cache tokens)、settle(success/disconnect/4xx/5xx)、recordUsageAsyncFromContext、captureRetention、providerBreakers.RecordSuccess/Failure
2. `[P1] handlers_chat.go:501-550` 非流式 ParseResponse 失败(501-521)与 FromProvider 转换失败(533-550)两分支直接 return，遗漏 settle —— 本地余额路径 preConsumeLocal 扣减永不退回，portal 预扣也无 Settle —— 上游返回非法 JSON 时用户被白扣预估输入费 —— 两分支补 settleLocal + portal Settle。 ✅ 已修复 (P1 proxy ) — 补 settleLocal
3. `[P1] handlers_common.go:32-40 + handlers_chat.go:1320 + handlers_responses.go:757` `sharedHandlerClient.Timeout = 120s` 被用于流式请求：http.Client.Timeout 覆盖整个响应体读取周期，Anthropic 透传流与 Responses 透传流（其 10 分钟 streamCtx 被覆盖失效）都在 120s 被硬切断；长流式响应必中途截断且已按"成功"结算 —— 流式改用无 Timeout client，仅保留 context 超时。 ✅ 已修复 (72a942c) — 新增 `sharedStreamingClient`（Timeout:0），2 处流式 `Do()`（handlers_chat.go:1368、handlers_responses.go:757）改用它，非流式保留 `sharedHandlerClient`
4. `[P1] handlers_responses.go:264-272` handleResponses 只做 preConsumeLocal 且丢弃 `used` 返回值，完全没有 portal PreConsume 回退（对比 handlers_chat.go:341-371）；本地缓存未启用时 settleLocal 直接 no-op —— Responses API 全端点无余额校验、无 portal 结算，余额不足可无限调用 —— 与 chat 路径对齐补 portal PreConsume/Settle。 ⚠️ 部分修复 — settleLocal 已在所有路径补齐（W0 `6c9638a`）；portal PreConsume 仍缺失，待对齐
5. `[P1] handlers_chat.go:1203-1235` handleClaudeMessages 中 `checkAllowedModels`(:1203) 在 `resolveModel`(:1235) **之前**执行，与 chat/responses 路径顺序相反 —— ① `wgit-maas-latest` 别名的 Claude 原生请求被 403 误杀；② resolveModel 解析出的目标模型不再受 allowed_models 约束，存在越权窗口 —— 移到 resolveModel 之后。 ✅ 已修复 (P1 proxy ) — checkAllowedModels 移到 resolveModel 之后
6. `[P1] handlers_chat.go:1279-1470、1473-1608` Anthropic 透传非流式与 Anthropic→OpenAI 非流式转换两条路径均无 preConsume/Settle，仅事后 recordUsageAsyncFromContext —— 余额为 0/负的用户在这些端点不受 INSUFFICIENT_BALANCE 拦截 —— 统一前置 preConsume。 ✅ 已修复 (6c9638a) — 随 #13 一并修复：非流式分支补 preConsume(local+portal)、transport/read error 与 4xx/5xx 补 settle、成功路径 settle(ActualUsage=usage)；recordUpstreamUsageAsync 传入解析后的 usage（原传 nil）
7. `[P2] handlers_chat.go:1330-1333、1401、1323、1384、1516` Anthropic 透传分支把原始上游错误体直接 c.Data 转发客户端，且错误消息拼接 err.Error()（含内部 URL/Provider 细节） —— 与主路径 471-474"never forward the raw upstream body"策略直接冲突 —— 改用 ClassifyUpstreamError/UpstreamUnreachableError。
8. `[P2] handlers_chat.go:756-774、1703-1730，handlers_responses.go:455-460` 流式错误分支 `io.ReadAll(resp.Body)` 无上限读取，且完整错误体传入 ReportKeyFailure 与 recordProviderHealthAsync（非流式路径已截断 500/200 字节） —— 超大错误体造成内存与上报管道膨胀 —— 统一 min(500,len) 截断并 LimitReader。
9. `[P2] handlers_chat.go:845-863、1771-1784` 流式后台读 goroutine 用容量 1 的 chunkCh：主循环退出且缓冲残留一个未消费 chunk 时，goroutine 在发送终止 error 处永久阻塞（AfterFunc 关 body 只能解除 Read 阻塞），goroutine 与 pool.GetBytes 缓冲一并泄漏 —— 读取侧循环 select streamCtx.Done，或退出前主动 drain。
10. `[P2] handlers_chat.go:378-381` 非流式在检查 statusCode **之前**调用 `RecordModelSuccess`：上游 4xx/5xx 既记 success 又记 ReportKeyFailure —— 模型健康统计被污染 —— 移到 statusCode<400 之后。
11. `[P2] handlers_chat.go:1074-1081` 流式 `c.Set("tokens_consumed", ...)` 被放进 `if upstreamKey.QuotaLimit > 0` 分支内（非流式 574-579 在分支外） —— 无配额上限的 key 走流式时 TPD 后置检查与 metrics 拿不到消耗量 —— 移出 if。
12. `[P2] handlers_chat.go:960-963（及 1013、1862、handlers_responses.go:553）` 流式 usage 聚合语义混杂：`PromptTokens =`（覆盖）而 `CompletionTokens +=`（累加） —— 上游若多 chunk 携带 usage，completion 重复累加或 prompt 覆盖丢失 —— 约定"取最后一个非空 usage"统一赋值。
13. `[P2] handlers_chat.go:37-50、606` `bodyBuf` 来自 pool 且 defer PutBuffer，`bodyBytes` 与该池化数组共享内存，随后传入 captureRetention 并 `c.Set("request_body", ...)` —— 若 retention/affinity 任一环在 handler 返回后异步引用即 use-after-return 数据竞争 —— 确认同步拷贝，否则传出前 copy。
14. `[P2] handlers_common.go:671-689` 候选构建兜底分支（candidates 为空时追加 high+normal+economy）使 general/economy 用户在 normal 档为空时实际可拿 high 档 key，与同函数 651-654 注释声明的档位隔离直接矛盾，且每轮 retry 都生效 —— 按用户 priority 限制兜底集合，或修正注释与文档。
15. `[P2] handlers_chat.go:1317-1377` 同 P0 路径连带缺口：Anthropic 透传流式分支无 captureRetention 调用（非流式 1469 有），trust-and-safety 留存缺失；上游非 200 时无 ReportKeyFailure/providerHealth 记录 —— 补留存与失败上报。 ✅ 连带已修复 (6c9638a) — 随 #13 一并补齐 captureRetention 与 providerBreakers.RecordSuccess/Failure

**小结**：计费闭环（Anthropic 透传流式 P0 + Responses API 与透传非流式 P1 + parse/transform 漏 settle）系统性缺口；流式基础设施（120s 截断、cap-1 通道泄漏、usage 聚合语义）次之；测试偏薄，建议为计费分支补回归用例。另注：handlers_responses.go:276 simulate 分支忽略 ResponsesToOpenAI 错误返回值，hubReq 为 nil 时 277 行解引用 panic（仅 simulate 模式）；responsesRouteCacheInstance 在本范围无任何 Set 调用，shouldUsePassthrough 注释所述"400 动态回退"逻辑不存在（注释与实现不符）。

## 11. Proxy server 其余 + middleware + auth + config（22 文件）—— 高风险

1. `[P0] handlers_audio.go:124 / handlers_images.go:190` audio 两个 handler（transcriptions/translations）与 images handler 全程只上报上游侧计量（recordUpstreamUsageAsync），从不调用用户侧 recordUsageAsyncFromContext（全仓调用点仅 chat×5、responses×3、embeddings×1）；audio 的 audit 条目连 Cost 字段都没有 —— 音频转写/翻译完全免单，图片仅剩 audit 里的 CalculateImageCost（portal 是否从 audit 结算需确认） —— 补用户侧 usage 上报（whisper 按时长、image 按成本结果写入管道），或显式标注结算链路。 ✅ 已修复 (6c9638a) — 新增 recordUsageFromContextByRequest（不因零 token 早退，按次计费专用），接入 audio transcriptions/translations + images handler；顺带修 images audit 中 Billing nil 潜在 panic
2. `[P1] internal/auth/auth.go:958-997` portal validate 响应解析结构体只含 4 个限额字段，`Claims` 中其余 8 个（SubConcurrentLimit/SubRp5hLimit/SubRpdLimit/SubRpwLimit/SubTpdLimit/SubTpwLimit/SubDailyLimit/SubMonthlyQuota）从未被赋值恒为 0；ratelimit.go 以 `> 0` 为生效条件 —— 订阅级并发、TPD/TPW、RP5H/RPD/RPW、日/月配额**全部静默失效**，纯订阅用户只剩 group 限额兜底 —— 配额逃逸 —— 补全解析结构体与 claims 拷贝。 ✅ 已修复 (72a942c) — 解析结构体与 Claims 赋值均补全 8 个字段（sub_concurrent/sub_rp5h/sub_rpd/sub_rpw/sub_tpd/sub_tpw/sub_daily/sub_monthly_quota）
3. `[P1] server.go:91` `WriteTimeout: 120s` 是每次请求的绝对写截止时间，注释声称 "streaming uses ResponseController if needed"，但全仓 grep 无任何 SetWriteDeadline/ResponseController 调用 —— 所有 SSE 流式 120s 墙钟后硬断，长推理流必现断流；ReadTimeout:10s 覆盖 body 读取，慢链路大体积音频上传会被杀 —— 流式端点用 `http.NewResponseController(c.Writer).SetWriteDeadline` 逐 chunk 续期，或 WriteTimeout 置 0 改应用层控制。 ✅ 已修复 (72a942c) — WriteTimeout 改为 0，流式时长由各请求 context（流式路径 10m）控制
4. `[P1] key_validation_handler.go:209` Bedrock 模型发现缓存键为 `region:filter`，不含任何凭证信息 —— 用有效密钥验证后 1 小时内，拿无效/低权限密钥验证同 region+filter 直接命中缓存返回 valid，根本不访问 AWS —— 密钥有效性校验被缓存旁路，portal 据此录入坏密钥 —— 缓存键加入 accessKey 哈希。 ✅ 已修复 (72a942c) — cacheKey 加入 `sha256(accessKey)[:16]`，不同凭证不再共享缓存条目
5. `[P1] usage_buffer.go:55-59 / upstream_usage_buffer.go:63-67 / provider_health_buffer.go:47-56` 三个名为 recordXxxAsync 的上报在 handlers_common.go:263/357/384 均为**同步调用**；缓冲区满时 `Add` 在请求 goroutine 内同步执行 Portal HTTP flush（client 超时 10s；usage 批量失败还逐条重试，最坏 100×10s 级阻塞，且 embeddings 在 `c.Data` 写回之前调用） —— Portal 抖动周期性传导为用户请求长尾/超时 —— drain 后交独立 worker 异步发送并加背压。 ⚠️ 部分修复 (P2 batch `896e8eb`) — 已加 fallbacklog 作为 DLQ；同步阻塞问题仍存，待改异步 worker
6. `[P1] config.go:529 + config.go:183` `admin.secret` 默认值硬编码 `"admin-secret"`，且 `Load()` 用 `_ = v.ReadInConfig()` 把"文件不存在"与"解析失败"一并吞掉 —— 生产配置损坏或漏配 ADMIN_SECRET 时静默回退公开默认密钥，`/admin/*`、`/api/internal/*` 的 HMAC 签名任何人可伪造 —— 取消默认值，Validate() 拒绝空/默认，解析失败报错。 ✅ 已修复 (72a942c) — 删除 `admin-secret` 默认值；`ReadInConfig` 解析错误不再吞掉（仅 ConfigFileNotFoundError 可接受）；`Validate()` 拒绝空值或 `admin-secret` 默认值（现环境已配 `ADMIN_SECRET`）
7. `[P2] handlers_embeddings.go:136-139` 上游响应 io.ReadAll 出错仅记日志继续执行：解析失败也只 warn，最终把截断/空 body 以 200 透传客户端（images 同场景正确返回 502） —— 读失败即 502。
8. `[P2] handlers_audio.go:54,174 / handlers_embeddings.go:116 / handlers_images.go:119` `httpReq, _ := http.NewRequest(...)` 统一吞错；portal 同步下来的 key 的 BaseURL 不经 validateBaseURL，畸形 URL 使 httpReq 为 nil、下一行 Header.Set 直接 panic（靠 gin.Recovery 兜成 500） —— 检查 err 返回 502。
9. `[P2] admin_handlers.go:324` `keyData["economyTiers"].([]string)` 类型断言永远不成立（JSON 数组解码为 `[]interface{}`） —— 批量导入密钥时 economyTiers 静默丢失且无报错 —— 遍历 []interface{} 逐项断言。
10. `[P2] metrics_middleware.go:24-25` `provider`/`model` 在 `c.Next()` **之前**从 context 读取，而 handler 在处理过程中才 c.Set —— 两值永远为空，IncrementProviderRequests/IncrementModelRequests 成为死代码 —— 移到 c.Next() 之后。
11. `[P2] handlers_audio.go:180-187` translations 的 transport-error 路径缺 recordUpstreamUsageAsync（transcriptions 路径 :63 有） —— 上游计量口径不一致。
12. `[P3] ✅ 已修复 internal/auth/auth.go:594,684` getBalance/PreConsumeLocal 内存路径用硬编码 balanceCacheTTL(10s) 常量而非配置的 `m.balanceCacheTTL()`，`LOCAL_BALANCE_CACHE_TTL` 对内存路径不生效 —— 统一走配置。
13. `[P3] ✅ 已修复 internal/auth/auth.go:128,557-559` 余额以 float64 承载（Claims.Balance/balanceEntry），floatToMicro 用 `int64(f*1e6)` 截断 —— 与 micro-USD int64 约定不符，存在向下舍入误差 —— portal 契约改 int64 micro 透传。
14. `[P3] ✅ 已修复 server.go:152,169` 公开 `/health` 回显 Redis host:port 及内部 sync stale 错误细节 —— 生产脱敏或要求鉴权。
15. `[P3] ✅ 已修复 usage_buffer.go:123,143 / upstream_usage_buffer.go:143` `failedCount++` 无原子/锁保护，sendBatch 可被 Add（满批）与 Flush（ticker）并发进入 —— 数据竞态（仅计数器，影响低） —— 改 atomic.Int64。

**小结**：计费/配额链路完整性（audio/images 计费上报、auth 丢弃 8 个限额字段）为直接资损面最优先；SSE 写超时为上线阻断级可用性；凭证处理（Bedrock 缓存不按凭证隔离、admin 默认密钥回退）第三。并发模型整体健康（buffer 锁粒度、nonce SETNX、balance Lua 均正确）。

## 12. Proxy pkg/keypool（7 文件）—— 需关注

1. `[P1] queue.go:154-161 + 190-197` `Dispatch` 先查 isStopped() 再 `q.jobs <- job`，与 `Stop()` 中 `close(q.jobs)` 之间存在检查-使用竞态 —— 停机瞬间在途请求 `send on closed channel` panic 打挂进程 —— 改"不 close 通道、done channel 通知 worker 退出"或 recover 保护。 ✅ 已修复 (e589ef1) — `Stop()` 改为 close 独立的 `done` channel 而非 `jobs` channel；worker 用 `select{case <-done; case job := <-jobs}` 退出，Dispatch 等待结果时也监听 `done` 兜底同步降级，彻底关闭 TOCTOU 窗口
2. `[P1] model_health.go:80-92` `IsModelAvailable` 在 RUnlock() 之后读 `entry.Disabled`、`entry.RecoverAt`，而 RecordFailure 在写锁下写同一 entry —— 真实数据竞态，热路径每请求触发，`go test -race` 必报 —— RLock 内拷贝字段后再判断，恢复翻转再走写锁。 ✅ 已修复 (P1 batch `b7287ed`) — RLock 内快照 disabled/recoverAt；恢复翻转在写锁内重新校验，消除热路径数据竞态
3. `[P1] sync.go:555-557` `updatePoolsFromSync` 合并时 `merged.KeyStatic = newKey.KeyStatic` 无条件用 portal 值覆盖 Enabled，本地 ReportKeyFailure 判死的 key（Enabled=false/Status=inactive）依赖 Enabled 拦截，而 IsKeyAvailable 不检查 Status —— 每次 portal 配置版本变化把本地判死的 key 重新拉回轮转池，401 反复冲击呈 flap 循环 —— 合并时本地 `Status=="inactive"` 且 portal 未显式变更应保留本地 Enabled=false（或 IsKeyAvailable 同时检查 Status）。 ✅ 已修复 (P1 batch `b7287ed`) — IsKeyAvailable 增加 Status=="inactive"|"purged" 检查；merge 保留本地判死态除非凭据轮换
4. `[P1] selection.go:264-272` `GetKeyByPriority` 过滤后无候选时回退为"provider 内任意可用 key"，绕过模型匹配、RPM/TPM 检查与优先级分层 —— 请求会被发到不支持该模型的 key 上必现上游报错；economy 用户也可能落到 high key —— 回退至少保留模型过滤，无候选返回 nil 交上层换 provider。 ✅ 已修复 (P1 proxy ) — 回退保留模型匹配
5. `[P2] manager.go:170-183 / selection.go:46-57` 前缀匹配逻辑与设计意图相反：`normS` 以 `normalized+"-"` 开头的（注释宣称支持的日期后缀变体）被显式 continue 跳过，而 `"gpt-4"` 对 `"gpt-4o"`（下一字符非 `-`）反而命中 —— 请求 gpt-4 可被路由到仅宣告 gpt-4o 的 key；与 ModelIndex.lookup 行为不一致，且注释描述的例子永远匹配不到 —— 前缀匹配要求连字符边界或统一走 index。
6. `[P2] sync.go:606-613` 同步合并末尾删除"本次同步未出现"的所有 provider 池，无任何兜底 —— portal 侧 bug/异常返回空 keys 且版本号变化时，全部上游池被清空，网关瞬间无 key 可路由 —— 护栏：`len(result.Keys)==0` 且本地池非空时拒绝应用并告警，或连续 N 次空快照才生效。
7. `[P2] model_health.go:221（Cleanup）` 全仓库仅测试调用，生产无周期触发 —— 每个 (key,model) 失败建常驻 entry，disabled 永不清除，map 无界增长 —— StartHealthCheck 周期挂 Cleanup，disabled 条目 RecoverAt 过久后回收。
8. `[P2] health.go:358-365` `isDailyQuotaExceeded` 用 `strings.Contains(msgLower, "5h")` 判定日配额耗尽 —— 任何含 "5h" 子串的错误消息都会把 key 冻结到本地次日零点（172 行还用服务器本地时区，未必对齐上游重置点） —— 收窄匹配词边界，明确 UTC。
9. `[P2] manager.go:331-332 + sync.go:619-632` `modelIndex` 每次同步全量重建并 atomic 存储，但 `ModelIndex.lookup` 全包无调用（grep 确认仅定义与 Store） —— 纯死代码且每 sync 白做全池遍历 —— 接线到 GetKeyByPriority/GetAllKeysForModel 或删除。
10. `[P3] ✅ 已修复 manager.go:525-528、sync.go:71-73/866-868` SetProxyInfo 与 syncBackoffDelay/StartSyncLoop 对 proxyID/proxyRegion/syncInterval 的写无锁，读方在 sync/pubsub goroutine —— 启动期赋值则良性，形式竞态 —— 启动前一次性设置或加锁/atomic。
11. `[P3] ✅ 已修复 queue.go:68-70 + 160-167` `size<0` 才归一化为 64，`size==0` 得无缓冲 channel，Dispatch 几乎必走 default 退化；ctx 取消后已入队 job 仍会被执行（仅靠前序 job.Ctx.Err() 检查缓解） —— `size<=0` 归一化；文档明确 job.Ctx 必须与 Dispatch ctx 相同。
12. `[P3] ✅ 已修复 persist.go:82-85` encryptionKey 为空时把明文 key 写入 Redis 且注释"Redis is already password-protected" —— NewManager 已 Fatal 拦截使此路径不可达，但明文回退是隐患（load 时空密钥会把密文 hex 当 API key 用） —— 删除明文回退，加/解密失败即报错。
13. `[P3] ✅ 已修复 sync.go:191 + 307-313` 同步端点直接拼接 m.portalURL，无 HTTPS 强制或告警，而响应体含全量上游真实 key；307-313 残留 provider=="moonshot" 硬编码调试日志 —— 启动校验 scheme 非 https 告警；删厂商定制调试日志。
14. `[P3] ✅ 已修复 health.go:21-24 vs 37-40` RPM/TPM 共享 windowEnd 但各自只重置自己的计数器 —— 只有请求计数推进窗口时 tpmUsed 可跨多窗口累积，TPM 误判溢出提前冷却 —— 窗口过期时同时清零。

**小结**：queue 停机 panic、model_health 锁外读、sync 合并覆盖本地禁用态为三个上线前必修 P1。密钥加密（AES-256-GCM+HKDF、随机 nonce、`json:"-"` 隔离）与目录闸门基线良好，金额/配额计数为 atomic int64。

## 13. Proxy pkg/relay + pkg/billing + pkg/convert + pkg/cache + dto（21 文件）—— 需关注

1. `[P1] pkg/billing/billing.go:319-330` SyncPricingFromPortal 合并区域价格时复用已有缓存条目做 `max()` —— Portal 降价永远不会生效：`pd = existing.pricing` 后 `max(pd.X, p.X)` 必然 ≥ 旧值，价格只涨不跌，用户被持续按历史最高价多收 —— 每次 sync 基于全新 PriceData{} 合并当批数据。 ✅ 已修复 (72a942c) — 用 `batchAgg` map 在当批内跨 region 取 max，然后 overwrite 缓存条目（不复用旧对象），降价可正常生效
2. `[P1] pkg/billing/billing.go:127 + pkg/convert/common.go:164-182` Claude 语义下 cache-read token 被双重扣减：Anthropic 的 `input_tokens` 本身就不含 cache_read（UsageFromClaude 直接 PromptTokens=InputTokens 且 CachedTokens=CacheRead，handlers_chat.go:1418-1426 透传同构），而 CalculateCostWithCache 再执行 `effectiveInput = inputTokens - cachedTokens` —— 缓存命中越多平台少收越多；AWS 适配器在 aws.go:651 显式把 cacheRead 加回 PromptTokens 做了口径补偿，Claude 路径没有 —— 计费函数感知 relay format，或各转换层统一为 OpenAI 语义。 ✅ 已修复 (72a942c) — 在 4 处转换/透传路径（`common.go` UsageFromClaude、`claude.go` convertClaudeUsage、`handlers_chat.go` 流式 message_start 与非流式透传）将 `cache_read` 加回 `PromptTokens`，对齐 OpenAI 语义（与 AWS adapter `promptTokensForBilling` 一致）
3. `[P1] pkg/billing/preconsume.go:197` `DetermineSettleAction` 中 `statusCode == 0` 一律 charge —— DNS 失败、连接拒绝等上游根本未收到请求的网络错误也把预估输入费转实收 —— 多收用户/客诉 —— 仅 errorType 确为 timeout 时才 charge，network_error 应 unfreeze。 ✅ 已修复 (72a942c) — 仅 `errorType==timeout` 才 charge；`statusCode==0` 的非 timeout 网络错误（DNS/连接拒绝）改为 unfreeze
4. `[P1] pkg/relay/channel/gemini/gemini.go:92` `DoRequestBytesWithContext` 用空字符串 model 调 `GetRequestURL("", stream)` → URL 为 `/models/:generateContent`（无模型名） —— 主流程 handlers 只调 DoRequestBytes*（handlers_chat.go:695/1513），且 Gemini 的 ToProvider 不像 AWS 那样捕获 model —— google provider 经此路径全量 404 —— 参照 AWSAdaptor 在 ToProvider 记录 model，或将 model 纳入签名。 ✅ 已修复 (e589ef1) — GeminiAdaptor 增加 `model` 字段，`ToProvider` 中 `a.model = req.Model` 捕获（对齐 AWSAdaptor 模式），`DoRequestBytesWithContext` 改用 `GetRequestURL(a.model, stream)`
5. `[P1] pkg/relay/base/adaptor.go:22-30` `sharedHTTPClient` 设置 `Timeout: 120s` 且被流式请求共用（AWS sendSigned 走 ConverseStream 也用它） —— http.Client.Timeout 覆盖整个 body 读取期，超过 120s 的 SSE 长流被硬切断 —— 流式请求用无总超时 client，仅靠 context 取消与 transport idle 超时。 ✅ 已修复 (72a942c) — `sharedHTTPClient` Timeout 改为 0，流式时长由请求 context 控制（AWS ConverseStream 同样受益）
6. `[P2] pkg/billing/preconsume.go:118-125` PreConsume 对非 402 的错误状态码（如 500）直接 decode body：JSON 错误体可正常 decode → 返回零值 `Success:false`（fail-closed 阻断全部请求），而网络错误却 fail-open 放行 —— 同一函数两种相反故障策略，且 decode 失败一律静默放行 —— 传输错误 fail-open、5xx 按配置、仅 402/明确 rejected 才拦截。
7. `[P2] pkg/relay/base/adaptor.go:178-182` ExtraHeaders 用 `req.Header.Add` 追加在 auth `Set` 之后 —— 若上游过滤器未剔除 Authorization/x-api-key，请求携带两个 Authorization 头，接收方取哪个行为未定义 —— 合并前显式跳过认证类头或用 Set+白名单。
8. `[P2] pkg/relay/channel/claude/claude.go:158-160,415-432` `mapReasoningEffortToThinking`：effort=high/xhigh → budget 20000，而客户端未设 max_tokens 时 ToProvider 默认仅 4096 —— Anthropic 要求 budget_tokens < max_tokens 且 ≥1024，直接 400 —— 按 max_tokens 比例钳制 budget。
9. `[P2] pkg/convert/claude.go:193` `fmt.Sprintf("%v", block.Input)` 作用于 `json.RawMessage`（[]byte） → 输出字节数组形式（`[123 34 ...]`），tool_use 参数彻底损坏 —— 当前仅测试引用，生产走 channel/claude，潜伏 bug —— 改 `string(block.Input)`。
10. `[P2] pkg/convert/anthropic.go:224-228 + pkg/relay/channel/aws/aws.go:460-463` tool_result 的 `block.Content`（可能是结构化 block 数组）原样透传为 OpenAI tool 消息 content，AWS 适配器仅接受 string，非 string 时生成空 Text block —— Bedrock 拒绝 blank text → Anthropic 格式客户端带结构化 tool_result 走 Bedrock 必 400 —— 非 string content 序列化为 JSON 字符串。
11. `[P2] pkg/convert/detector.go:29-31` `IsAnthropicFormat`：任何同时含 `"max_tokens"` 和 `"messages"` 的请求体（即绝大多数 OpenAI chat 请求）都判为 Anthropic —— 目前无生产调用方，但接入即大规模误路由 —— 用 Anthropic 特有字段判定或删除该分支。
12. `[P2] pkg/relay/providers.go:294-296` `RegisterProvider` 无锁写全局 DefaultProviderConfigs map，与 GetAdaptor/GetProviderConfig 并发读构成 data race —— 加 RWMutex 或限定仅 init 期调用并注释约束。
13. `[P2] pkg/billing/billing.go:512 vs preconsume.go:52` 金额单位不一致：同包内 `UsageRecord.Cost` 为 float64 美元，`SettleRequest.PreConsumed` 为 micro-USD int64 —— 违反平台约定，portal 侧任何换算疏漏即 10⁶ 倍资损 —— 统一 int64 micro-USD 并在字段/json tag 标注单位。
14. `[P3] ✅ 已修复 pkg/relay/channel/aws/aws.go:1352-1368` 流式 `emitUsage` 漏设 `PromptCacheHitTokens`（非流式 FromProvider:752 有） —— Bedrock 流式缓存命中统计/计费口径不一致 —— 补齐字段。
15. `[P3] ✅ 已修复 pkg/relay/channel/claude/claude.go:17,473-598 + aws.go:36,402` 流式状态（streamState）与目标模型（a.model）挂在 adaptor 实例可变字段上 —— 当前每请求新建 adaptor 故安全，但注释自称"per-connection state"名不副实，未来实例复用/跨重试共享即竞态+串话 —— 状态下沉到请求级 struct。

**小结**：计费口径三 P1（Claude cache 双重扣减少收、pricing sync 只涨不跌多收、网络错误转实收多收）都直接动钱，上线前必修并补格式感知计费单测；流式 120s 总超时为可用性硬伤；Gemini 主路径必 404 需确认是否已在线上灰度；潜伏型 bug（detector 误判、`%v` 序列化 RawMessage、tool_result 结构化内容）当前无生产调用方但接入即事故，建议同批清理。

---

# 系统性共性问题（跨模块横向治理项）

以下模式在 3 个以上模块重复出现，建议以统一手段治理而非逐点修补：

1. **吞错文化**：查询/写入失败记日志（或不记）后照返 `success: true` 或 200 空数据（admin_*、billing、enterprise、query 层普遍）。故障被渲染成"无数据"，运维不可见。建议：统一错误处理约定 + handler 级中间件收口。
2. **nil-rows defer panic**：`err` 未判即 `defer rows.Close()`，pgx 出错返回 nil rows，函数退出时 panic（admin_reconciliation、channel_health、dashboard 等至少 5 处）。一个 lint 规则可全灭。
3. **金额单位三套混用**：micro-USD int64（约定）/ float64 USD / 原始 micro 值按 float 返回，字段名不标注（apikeys、enterprise、admin_usage、proxy auth/billing）。10⁶ 倍事故温床。建议：全链路 int64 micro-USD + 字段名/tag 标注 + JSON 契约 lint。
4. **CSV 公式注入**：所有 CSV 导出（billing、orders、audit、web/data）对用户可控字段无 `= + - @` 前缀中和。统一 `csvutil.Escape`。
5. **异步 goroutine 持已取消 ctx**：`go f(c.Request.Context())`（PublishSyncEvent、邮件等十余处），handler 返回即 cancel，事件/邮件随机丢失。统一 `context.WithoutCancel` 或 `context.Background()`。
6. **`deleted_at IS NULL` 漏过滤**：keypool 导出/列表、企业 keys、invoices profiles 等多处软删数据泄漏到列表/导出/统计。
7. **分页无校验**：limit 无上限（全表拉取 DoS）、limit=0 除零 panic、负 page 负 OFFSET。统一分页参数 clamp 中间件。
8. **状态字段无白名单**：apikeys status、enterprise group status、user role、order payment_method 等用户/管理员可控枚举值无校验，且常与"记一次审计照返 success"伴生。
9. **写操作无事务的多步顺序写**：成员移除、企业删除、topup、updateKey/updateProvider/updateGroup 多字段拆条 UPDATE，中途失败即分裂状态。统一 WithTx。
10. **120s 超时截断长流（proxy 侧三处同构）**：proxy `sharedHandlerClient.Timeout=120s`、`server.go WriteTimeout=120s`、`pkg/relay/base sharedHTTPClient Timeout=120s` 都会硬断 SSE 长流。需要一套"流式专用 client + 写截止续期"方案统一替换。

# 行动清单（按周建议）

**W0（本周，上线阻断）**
- ~~支付四连 P0（回调免验/单位错配/支付宝验签/改价不核对）~~ ✅ 已修复 (d1ca9f5)
- ~~admin 角色越权两 P0 + web 层 role 提权 P1；企业跨租户审批 P0 + query 跨租户泄漏 P0~~ ✅ 已修复 (eedac67) — API 层 + web 表单层 + UI 模板选项按角色隐藏
- ~~schema.go 启动 DDL（改存在性检查）；sync.go 鉴权 fail-open 改 fail-closed；voucher 兑换/订阅取消资金事务化 + 退款基准按实付~~ ✅ 已修复 — ensureUniqueConstraint 不再 DROP；requireSyncSecret fail-closed；redeem/redeemPlanVoucher WithTx+FOR UPDATE；cancelSubscription WithTx+按实付退款
- ~~proxy 计费闭环：Anthropic 透传流式 P0、audio P0~~ ✅ 已修复 (6c9638a) — 透传流式补 preConsume/settle/usage 解析/captureRetention/失败上报；audio+images 补 recordUsageFromContextByRequest 按次计费。注：parse/transform 漏 settle(P1 #2)、Responses API 预扣(P1 #4) 属第二梯队，未在本批

**W1-W2** ✅ 已修复 (portal `ef029bb`、proxy `72a942c`)；双端均已编译运行 healthy
- ✅ proxy：Claude cache 双重扣减、pricing sync max() 只涨不跌、pre-consume 网络错误转实收、auth 8 限额字段补全、SSE 三处超时、Bedrock 验证缓存按凭证隔离、admin 默认密钥
  - Claude cache 双扣：Anthropic `input_tokens` 不含 `cache_read`，计费层 `effectiveInput = PromptTokens - CachedTokens` 导致缓存命中被双扣。修复在 4 处转换/透传路径（`common.go`、`claude.go`、`handlers_chat.go` 流式×2）将 `cache_read` 加回 `PromptTokens`，对齐 OpenAI 语义
  - pricing sync max()：原 `pd = existing.pricing` 复用旧对象做 max() 导致降价永不生效；改为 `batchAgg` map 当批内跨 region 取 max 后 overwrite 缓存条目
  - pre-consume 网络错误：原 `timeout || statusCode==0` 一律 charge；改为仅 `errorType==timeout` charge，其余网络错误（DNS/连接拒绝）unfreeze
  - auth 8 限额字段：Claims 解析与赋值补全 `sub_concurrent/sub_rp5h/sub_rpd/sub_rpw/sub_tpd/sub_tpw/sub_daily/sub_monthly_quota`
  - SSE 三处超时：新增 `sharedStreamingClient`（Timeout:0）供流式调用，`adaptor.go sharedHTTPClient` Timeout:0，`server.go WriteTimeout`:0；2 处流式 `Do()` 改用 `sharedStreamingClient`，非流式保留 `sharedHandlerClient`
  - Bedrock 验证缓存：`cacheKey` 加入 `sha256(accessKey)[:16]`，不同凭证不再共享缓存条目
  - admin 默认密钥：删除 `admin-secret` 默认值；`ReadInConfig` 解析错误不再吞掉；`Validate()` 拒绝空值或 `admin-secret` 默认值（现环境已配 `ADMIN_SECRET`）
- ✅ portal：stats 队列丢记录与双扣、双倍计数、邀请链"抢人"、企业组模型子集校验、quota 台账与月度重置对齐、google 校验、mail 超时、token_version 一致化（profile/gdpr/jwt/OptionalAuth）
  - webhook_billing：删除重复 step8（api_keys 批量更新）和 step9（订阅计数器自增），消除双扣
  - webhook_common：`processUsageStatsQueue` 失败记录重入队列（retry>3 进死信），不再丢弃
  - webhook_usage：trace_id 去重 INSERT 用 record timestamp 而非 `NOW()`，重试不再碰撞；`processStatsBatch` 走同一重试+死信路径
  - enterprise_members：`acceptInvitation`/`inviteMember` 校验目标用户未绑定其他企业，防跨租户"抢人"
  - enterprise_groups：create/update 校验请求模型为平台模型子集
  - enterprise_billing：`ResetMonthlyBilling` 同时清零成员 quota 余额，不止企业聚合
  - validation：google 模型测试 URL 用 `?key=api_key` 签名
  - profile/gdpr：改密与删号递增 `token_version` 使已发 token 失效；`OptionalAuth` 校验用户存在性/封禁/`token_version`，不再信任 claims

**W3+（治理项）**
- ~~十大共性模式横向整改（见上节）+ handler 级回归测试补齐（当前 admin/query 层 SQL 近乎零覆盖）~~ ✅ 已修复 (portal `a244ea9`、proxy `896e8eb`) — 吞错返 200 普遍补 Error 日志；nil-rows defer panic 统一先判 err；CSV 公式注入统一 csvSanitize；PublishSyncEvent 统一 context.Background()；金额单位补注释；分页统一 clamp；KEYS→per-user DEL
- ~~keypool 三 P1（停机 panic/锁外读/禁用态覆盖）+ 空快照护栏~~ ✅ 已修复 (proxy `b7287ed`) — queue TOCTOU、model_health 锁外读、sync 禁用态覆盖、空快照护栏
- ~~潜伏 bug 清理（detector、`%v` RawMessage、tool_result 结构化、Gemini model 传递）~~ ✅ 已修复 (proxy `896e8eb`) — detector 已删除、`%v`→`string()`、tool_result JSON 序列化

**W4（P2 全量修复）** ✅ 已修复 (portal `a244ea9`、proxy `896e8eb`)；双端编译通过、DB 集成测试 + race 测试全绿
- ✅ portal webhook/sync：pricingRatio 零值守卫、企业/个人聚合分区、traceID 变异后入队、原子 GETDEL freeze + 死信、孤儿 billing 记录死信、config_version 收敛
- ✅ portal billing/payment：activateSubscription 错误传播、CSV 公式注入消毒、分页 clamp、wechat nil 检查、DB 错误处理、order_id UUID cast 修复
- ✅ portal auth/apikeys：否定结果缓存 + IP 限流、余额变更失效缓存、getKey reveal-only、SQL 聚合统计、micro-USD 注释、updateKey 事务化
- ✅ portal admin_*：provider key 加密、PublishSyncEvent context.Background、updateProvider 单 UPDATE + RowsAffected、发票状态机 RowsAffected、deleteUser 审计 actor 修复、validateKeyPoolKeysBatch 超时、admin_usage 单位修复
- ✅ portal enterprise_*：金额单位注释、time.Time 直扫、成员移除事务化、GetLocale 前置、group status 白名单 + 单 UPDATE + 级联禁用、topup 事务化、错误消息脱敏
- ✅ portal models/vouchers/audit/settings：redeemPlanVoucher 事务、KEYS→per-user DEL + 审计、role context key 统一、分页 clamp、CSV escaping、dashboard RequireAdmin、nil-rows panic 修复、settings 白名单
- ✅ portal services/db：pricing cache 过期淘汰、billing ratio 缓存+日志、pricing_error 标记、advisory lock、retention continue-on-error、retention 读 settings、GDPR 匿名化、seed 密码拒绝、encryption key fail-fast
- ✅ portal web/query/middleware/cmd/pkg：PublishSyncEvent context.Background、renderMu locale、UTC 时区、csvEscape 公式注入、EndDate 边界修复、login 限流、query 错误日志、三态模型可见性、queue 重试/DLQ、audit dropped counter、CORS trusted proxy、server 超时、优雅关停
- ✅ proxy keypool：连字符边界前缀匹配、model_health Cleanup 周期触发、死代码 ModelIndex 删除
- ✅ proxy handlers：Anthropic 错误脱敏、LimitReader 截断、chunkCh context select、RecordModelSuccess 排序、tokens_consumed 移出 if、usage 聚合语义统一、bodyBuf 拷贝、tier 隔离兜底、embeddings 502、http.NewRequest 错误检查、economyTiers 断言、metrics 后置 c.Next、translations usage 记录
- ✅ proxy relay/billing/convert：preconsume 5xx fail-open、ExtraHeaders auth 跳过、claude budget 钳制、convert string() 修复、tool_result JSON 序列化、detector 删除、providers RWMutex、billing 单位文档

**W5（P1 全量修复）** ✅ 已修复 (portal `6ac09a6`~`35daf08`、proxy `016f5e2`)；双端编译通过、DB 集成测试 + race 测试全绿
- ✅ portal billing/payment：order_id cast 修复（`id::text = $1`）、monthly date 格式修正、invoices 软删过滤、PayPal order ID 持久化
- ✅ portal auth/apikeys：createKey RPM/TPM clamp 到组限额、updateKey status 白名单（active/inactive 对齐 DB CHECK）、group_id 归属校验、forgotPassword 邮箱限流
- ✅ portal admin_*：导出密码改 POST-only、updateUser balance 写流水事务、date_to 格式修正、nil-rows panic 修复（先 return 再 defer）、currency 条件 guard、double-WHERE 语法错修复
- ✅ portal enterprise_*：getGroupModelsWithStatus 剥离 product_id/价格、5 个端点 date 格式修正、listEnterpriseKeys 软删过滤
- ✅ portal models/subs/services：自动续费 balance 校验入事务 + duration 从套餐读取、邀请奖励统一 micro-USD、flat 模式 Fixed×RequestCount、usage_records UNIQUE(trace_id)
- ✅ portal web：rawKey 移出 redirect URL、http.NewRequest 错误检查、HandleUpdateUserRole 自身比对
- ✅ proxy handlers：ParseResponse/FromProvider 失败补 settleLocal、Claude 路径 checkAllowedModels 移到 resolveModel 之后、GetKeyByPriority 回退保留模型匹配
- ⚠️ 部分修复：handlers_responses.go portal PreConsume 仍缺、usage_buffer.go 同步阻塞仍存（待异步 worker）

**W6（P3 全量修复）** ✅ 已修复 (portal + proxy `f395fb4`→后续 commit)；双端编译通过、DB 集成测试 + race 测试全绿
- ✅ portal webhook/sync：models/sync 查询改为三态 `IN ('active','eol_pending','eol')`；webhook_billing 聚合 reference_id 改用独立批次 ID；chargeFromFreeze 个人路径补 Warn 日志
- ✅ portal billing/payment：encryptionKey 为空时告警；IsMaskedValue 掩码判定结合现存值
- ✅ portal auth/apikeys：recordDevice 改 UPSERT + 事务；登录 dummy bcrypt 防计时侧信道；deleteKey 404、register 错误传播、getKeyUsage 500
- ✅ portal admin/services/db：CSV 导入 JSON/float 解析失败计 failed 并返回行号；BalanceUpdateMessage.Balance 改 int64；DSN 用 url.UserPassword；dashboard 复用真实定价 + 趋势键存完整日期；审计 email→user_email
- ✅ portal web/middleware/cmd/pkg：/api/health 脱敏；SanitizeInput 读体失败 Abort 413 + Content-Length 同步；encryption ErrInvalidKey 启用 ≥8 字符校验 + KDF 注释；service-models/public rows.Scan 错误检查
- ✅ proxy auth：getBalance/PreConsumeLocal 统一走配置 TTL；Claims.Balance/balanceEntry 改 int64 micro-USD；floatToMicro 用 math.Round
- ✅ proxy server：/health 脱敏 Redis host:port 与 sync 错误细节
- ✅ proxy keypool：failedCount 改 atomic.Int64；SetProxyInfo proxyID/Region/syncInterval 加锁 atomic；queue size<=0 归一化 64；persist 删除明文回退；sync HTTPS 校验 + 删 moonshot 调试日志；health RPM/TPM 窗口同时清零
- ✅ proxy relay：aws streaming emitUsage 补 PromptCacheHitTokens；claude/aws adaptor streamState 注释修正为 per-request 语义

# 评审覆盖声明

本评审覆盖两个仓库当前 main（portal `dec345d`、proxy `ef20b4a`）全部非测试 Go 源码：portal 158 个文件（internal/routes 69、services 5、db 9、web 15、query 15、middleware 5、pkg 17、cmd 6、其他），proxy 62 个文件（internal/server 21、middleware 2、auth 1、config 1、pkg/keypool 7、relay 8、billing 2、convert 8、cache 1、dto 2、其他），均逐文件通读。测试文件仅作覆盖度评估，未逐行评审。每条发现均注明文件与行号，行号以评审基线为准；修复后请以最新代码复核。

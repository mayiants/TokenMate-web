# 平台全面 Review 记录与解决方案（灰度阶段）

**Status: ACTIVE**
**日期**: 2026-07-26
**范围**: ai-portal-go（控制面）+ ai-proxy-go（数据面）
**背景**: 项目处于灰度测试阶段。本次 review 覆盖四个方面：① enterprise 页面风格一致性；② 全系统致命/严重缺陷；③ 单元测试覆盖缺口；④ CI / E2E 配置过期与覆盖缺口。支付网关此前从未实际测试，单独详查。

---

## 目录

1. [Enterprise 页面风格一致性](#一enterprise-页面风格一致性)
2. [Portal 致命/严重缺陷](#二portal-致命严重缺陷)
3. [Proxy 致命/严重缺陷](#三proxy-致命严重缺陷)
4. [支付网关专项](#四支付网关专项)
5. [单元测试覆盖缺口](#五单元测试覆盖缺口)
6. [CI / E2E 配置问题](#六ci--e2e-配置问题)
7. [修复路线图](#七修复路线图)

---

## 一、Enterprise 页面风格一致性

整体骨架（layout.html、stats-bar、card、data-table、page-header）与老页面一致，问题集中在细节欠账。改造时以老页面约定为准。

### 1.1 日期选择器

- 全站约定：flatpickr，由 `web/static/app-common.js:445-511` 的 `initFlatpickr()` 自动初始化 `.fp-date` / `.fp-datetime` / `.fp-range`（含中文 locale、暗色主题）。老页面全部遵守。
- **D-1 不一致**：`web/templates/enterprise/audit.html:119,123` 使用原生 `<input type="date">`（全模板中唯二的原生 date 输入），无中文本地化、无暗色主题适配。
- 正确范例：`enterprise/usage.html:102`（`.fp-range` + hidden inputs）。
- **方案**：audit.html 改为 `.fp-range`（配 hidden from/to）或两个 `.fp-date`。

### 1.2 页签组件三套并存

| 方案 | 位置 | 使用方 |
|---|---|---|
| `.tabs`+`.tab-btn`+`.tab-pane` | `app.css:628` | 老页面（usage-logs.html:6）、enterprise/dashboard.html:184 |
| `.tab-nav`+`.tab-pane` | `app.css:659` | enterprise/billing.html:123 |
| 页内私有 `.usage-tab` | enterprise/usage.html:64（页内 `<style>`） | 仅 usage.html |

**方案**：统一为 `.tabs`+`.tab-btn`，删除 usage.html 页内私有样式，billing.html 迁移。

### 1.3 分页缺失（本组最优先）

- **T-2**：`enterprise/users.html:74`、`audit.html:151`、`alerts.html:69`、`keys.html:79` 均服务端全量渲染，仅靠前端 JS 过滤。成员/审计量大的企业会遇到页面膨胀。
- 现成方案：`layout.html:17` 全局加载的 `table-pagination.js`（`initTablePagination`，见 admin-groups.html:639），或服务端 `.pagination`（admin-users.html:115）。
- **方案**：四个列表页接入 `table-pagination.js`，数据量大后再升级服务端分页。

### 1.4 表格空态四种并存

主流写法是 `empty-state-sm`+图标（全站 20 个文件）。不一致：`keys.html:140`/`groups.html:117` 用大号 `empty-state`；`alerts.html:107`/`member-detail.html:81`/`key-detail.html:113` 无图标；`users.html:139` 裸 `<p>`。
**方案**：统一 `empty-state-sm`+图标。

### 1.5 图表

- 库无混用（全 Chart.js），无需处理。
- **C-1 颜色硬编码**：`enterprise/usage.html:416`（`'#8b5cf6','#3b82f6','#f59e0b'`）、`:481-482`（`rgba(34,197,94,0.12)`）。老页面及 enterprise/dashboard 走 `chartColor('--color-*')` 主题变量（`app-common.js:400,424`），暗色主题下硬编码色不跟随。
- **C-2 画布容器三种写法**：老 `chart-canvas`（app.css:3646，240px）；`dashboard.html:405-408` 页内自定义 `.chart-canvas-wrap`（300px）；`usage.html:79` 内联 `style="height:200px"`。
- **方案**：颜色统一 `chartColor()`/`chartPalette()`；画布统一 `chart-canvas`（如需不同高度在 app.css 加修饰 class，不内联）。

### 1.6 图标

- 全站统一内联 lucide 风格 SVG，无图标字体，无需处理。
- **I-1 stats-bar 图标尺寸**：老页面一律 24px（dashboard.html:52、admin.html:50）；enterprise 中 `audit.html:23`、`users.html:14`、`keys.html:15`、`groups.html:12`、`alerts.html:16`、`member-detail.html:15`、`key-detail.html:15` 用了 20px（容器 `.stat-icon` 固定 48px，忽大忽小）。
- **方案**：统一 24px。

### 1.7 样式 class 欠账

- **E-1 页内 `<style>` 重复/覆盖全局**：`enterprise/dashboard.html:392-404` 页内重定义 `.credit-gauge`（加卡片外壳）和 `.chart-card`（与全局 app.css:3622 完全重复）。导致 credit-gauge 在 dashboard 有外壳、在 billing.html:58 没有。
- **E-2 死 class**：`enterprise/audit.html:135,142,144` 的 `btn-outline-secondary` 在 app.css 和 tailwind 中均无定义。
- **E-3 平台级历史欠账**：`form-input-sm`/`form-select-sm` 在 13 个模板（新老都有）使用但 CSS 从未定义；`filter-label`（audit.html:118-130）靠 `app.css:3761` 后代选择器兜住。
- **E-4 内联尺寸**：`enterprise/usage.html:95,102,201` 用 `style="height:38px;..."` 手调控件尺寸。
- **方案**：在 app.css 正式定义 `form-input-sm`/`form-select-sm`/`btn-outline-secondary`（或替换为已有 class）；删除 dashboard.html 页内重复 `<style>`；删除 usage.html 内联尺寸。

### 1.8 金额精度与 i18n

- **E-5 金额精度混用**：`%.6f` / `%.2f` / `toFixed(2/4/6)` / `formatCurrency` 全站混用（老页面同样存在）。**方案**：定规范——展示统一 `%.2f`，对账/单价场景统一 `%.6f`，逐页收敛。
- **L-1 i18n 漏网**：
  - `enterprise/keys.html:109-110`：`{{.MonthRequests}} requests/month` 硬编码英文；
  - `enterprise/groups.html` JS 多处硬编码英文（:260,327,268,301,335,368,372,379,407,423,466）；
  - `enterprise/groups.html:51`：搜索框 placeholder 错用页面标题 key `{{t "Group Management"}}`；
  - `enterprise/groups.html:341-343`：`allModels` 为空时显示 `{{t "0 = Unlimited"}}`（复制粘贴错误）；
  - `enterprise/dashboard.html:19,22`：状态横幅同一句文案既当标题又当副标题。
- **方案**：补齐 en/zh i18n key，修正两处错误文案，改后跑 `python3 scripts/i18n_full_audit.py --strict`。

---

## 二、Portal 致命/严重缺陷

路径相对 `ai-portal-go/`。

### 严重 S2-1：密码重置 token / 验证码明文写日志（账户接管风险）

- `internal/web/auth.go:335-338`：SMTP 未配置时，1 小时有效的重置 token 以 Info 级写日志 `zap.String("token", resetToken)`。
- `internal/profile.go:316`：换邮箱验证码同样写日志（Debug 级）。
- 对比 `internal/routes/auth.go:151-153,476-478` 已刻意不记录 code——这两处是遗漏。
- **方案**：两处一律不记录 token/code 原文；如需排障记录 hash 前缀。

### 严重 S2-2：多有效订阅时扣费 UPDATE 命中全部订阅——重复扣量/负额度

- 扣减语句按 `user_id + status='active'` 更新**全部**有效订阅，SELECT 却只取 `ORDER BY created_at DESC LIMIT 1` 一条：
  - `internal/routes/webhook_billing.go:334-339`（批量扣配额）、`:300-303`（daily reset）
  - `internal/routes/webhook_usage.go:368-373`、`:355-358`、`internal/routes/webhook_usage.go:1183-1200`（incrementSubscriptionCounters）
- 两个 plan 可同时 active（`vouchers_user.go:288-311`），触发时两个订阅同时扣量 → `quota_remaining` 可为负、消耗翻倍。
- 相关：`apikeys_validate.go:148-150` 的 LEFT JOIN 无 LIMIT 1，多订阅时取到不确定行。
- **方案**：扣减 UPDATE 加 `AND id = <选定订阅id>`（与 SELECT 同一条订阅）；validate 查询加 `LIMIT 1` 并明确优先级。

### 严重 S2-3：计费丢失路径（漏扣 = 资损）

- `internal/routes/webhook_usage.go:164-178`：handleUsageStatsBatch 在 Redis 入队失败时 fallback 只做 processStatsBatch（写 usage_records），**从不扣款**也不入 billing 队列，响应仍 `ok:true`。
- `internal/routes/webhook_common.go:340-350`：processUsageStatsQueue 向 billing 队列 RPUSH 失败时只加 metric，不重试不进死信。
- **方案**：fallback 路径同步完成扣款（或写死信队列 + 对账任务兜底）；RPUSH 失败入死信队列并由后台任务重放。

### 严重 S2-4：计费队列无 trace 级幂等——可重复扣款

- `webhook_common.go:340-345` 与 `:698-703` 都会推同一 `webhook:billing:queue`；`processBillingBatch`（webhook_billing.go:93-479）按队列内容直接扣余额。仅 usage_records 插入有 `ON CONFLICT (trace_id, created_at) DO NOTHING`，**扣款动作无幂等**。
- **方案**：扣款前以 trace_id 检查/占位（Redis SETNX 或 DB 唯一约束表），已处理则跳过。

### 严重 S2-5：管理员角色降级不即时生效

- `internal/middleware/auth.go:153` 角色取自 JWT claims；`requireRole`（:227-248）只读 context。`RequireAuth` 对封禁/删除做 DB 校验（:73-95）但对角色变化不查 DB；`token_version` 仅在改密码时递增（auth.go:420）。被降级的 system_admin 在 token 过期前（默认 7 天）保留全部管理权限。
- 对比企业侧 `requireEnterpriseAdmin`（enterprise_common.go:183-203）查 DB，两侧标准不一致。
- **方案**：`requireRole` 对 admin 级角色增加 DB 校验（可带短 TTL 缓存），或角色变更时递增 token_version。

### 严重 S2-6：6 位验证码无按码失败次数上限

- `internal/routes/auth.go:502-505`（resetPassword）、`:181-185`（register）：错误码仅 400，无"连续失败 N 次作废"。唯一防线 bruteForceProtection 按 IP 计数且 Redis 故障 fail-open（:68-73）。验证码空间 10⁶、TTL 10 分钟，代理池轮换 IP 可爆破。
- **方案**：验证码记录按码失败计数，≥5 次作废该码。

### 严重 S2-7：Redis 未配置时认证端点 nil 指针 panic

- `internal/routes/auth.go:118,181,442,502` 直接 `r.db.Redis.Incr/Get` 无 nil 检查（同文件 bruteForceProtection:57 把 Redis nil 当合法场景）。nil 时 panic 被 Recovery 兜成 500，注册/重置不可用。
- **方案**：入口统一 nil 检查，fail-closed 返回 503 并告警。

### 一般项

- `sync.go:52`：sync 端点共享密钥字符串直比（该端点返回解密后的上游 key 明文），改 `subtle.ConstantTimeCompare`。
- `webhook_preconsume.go:125-157`：pre-consume 检查与冻结非原子，并发可超额冻结（最终扣款有 GREATEST 兜底）。
- `auth.go:42`：`/api/auth/send-code` 无按 IP 限流，可邮件轰炸。
- `pkg/jwt/jwt.go:73-79`：无 iss/aud 的 legacy token 仍接受；`pkg/crypto/encryption.go:29-31` 密钥最短 8 字符（production 已被 bootstrap 强制 ≥32 兜底）。
- `webhook_usage.go:1097-1104`：批量 INSERT 丢失 `pricing_error` 列（单条路径有），计价失败记录在批量路径无法被对账识别。
- `auth.go:520`：resetPassword 未排除软删用户；`webhook_billing.go:384`：batchRefID 秒级时间戳+条数可撞车。
- `apikeys_usage.go:16-20`：getKeyUsage 仅按 user_id 判所有权，admin 分配的企业 key 归属判断错位。

### 已验证无问题（不要在修复中破坏）

- SQL 注入：所有动态 SQL 均为固定片段 + `$n` 占位符。
- Webhook 签名：HMAC-SHA256 + 300s 时间窗 + `hmac.Equal`，无 secret 时 fail-closed。
- 支付 completeOrder 幂等（条件更新 + 事务）、入账金额取 DB 值。
- 日志不记录 Authorization；上游 key 只存前后几位预览。

---

## 三、Proxy 致命/严重缺陷

路径相对 `ai-proxy-go/`。

### 致命 F3-1：本地余额缓存一簇 bug（启用 LocalBalanceCacheEnabled 即触发）

1. **误拒 402**：`internal/auth/auth.go:619` setBalance TTL 即 balanceCacheTTL（默认 10s）；Lua 缺键返回 {0,0}（:652-659）被当作"扣款成功且余额归零"，并把内存余额污染为 0（:670-676）；下一请求命中污染条目直接 402（:311-323）。付费用户被约 50% 占空比误拒。
2. **凭空捏造余额**：`SettleLocal`（:708-713）对过期键 INCRBY，Redis 缺键时新建值=delta 的"余额"键。
3. **双重扣费**：扣后余额恰为 0 时与缺键 {0,0} 无法区分，`internal/server/handlers_common.go:1318-1321` 误判"本地未处理"转而走 Portal PreConsume 再冻结一次，本地扣款永不被结算。
- **方案**：Lua 缺键返回哨兵值（如 {-1,-1}）与"扣后为 0"区分；setBalance 用独立较长 TTL；SettleLocal 对缺键走 Portal 对账而非 INCRBY 新建；handlers_common 按哨兵值判断而非 remaining==0。

### 致命 F3-2：已禁用 key 可用窗口不受控

- key 验证缓存只有 TTL 淘汰（auth.go:32 默认 5min），无主动失效通道。
- `auth.go:36` gracefulTTL=30min + `:806-811`：Portal 不健康时过期缓存再延 30 分钟；`:910-911` 任何网络抖动即 markPortalUnhealthy。被禁用 key 最长可继续用 30 分钟。
- **方案**：Portal 推送 key 撤销（复用现有 balance pub/sub 通道或新增 webhook）；gracefulTTL 降权（如 5-10min）并加告警。

### 严重 S3-1：/v1/responses 端点无 Portal pre-consume 兜底

- `internal/server/handlers_responses.go:265-272`：`used` 标志被 `_` 丢弃，全文件无 `Billing.PreConsume` 调用（对比 handlers_chat.go:346-376）。本地缓存未启用/未命中时上游调用前无任何冻结/预扣。
- **方案**：补齐与 chat 路径一致的 `if !used { PreConsume }` 兜底。

### 严重 S3-2：上游无 usage 的成功请求完全免单

- 非流式 `handlers_chat.go:608-611`：usage==nil → 空 Usage，totalTokens<=0 直接 return（handlers_common.go:364-367），settle 走 Success → Portal unfreeze。预扣退还 + 无实际扣费。
- 流式同理：上游不带 usage chunk 时 totalUsage=0（StreamOptionsInjector 是可选插件）。
- **方案**：success 且 usage 为 0 时按预扣估值 charge（而非 unfreeze），并加 metric 监控该路径比例。

### 严重 S3-3：流式中断免单 + 审计丢失

- `handlers_chat.go:946-965`：streamCtx.Done()（客户端断开/超时）直接 return，不 recordUsageAsyncFromContext、不写 audit、仅按 input 预扣结算。已流出的 output token 免单。恶意客户端可系统性"流一半断开"白嫖。
- **方案**：中断路径按已收到的 usage chunk（或按已流字节估算）结算并记录。

### 严重 S3-4：所有 Settle fire-and-forget

- `go s.handlerDeps.Billing.Settle(...)` 11+ 处（handlers_chat.go:419,488,536,...）；`pkg/billing/preconsume.go:182-193` 失败仅返回 {Success:false}，无重试无日志；`cmd/server/main.go:300` cancel 后立即退出杀死在途 settle。
- 后果：unfreeze 丢失 → 用户余额永久冻结；charge 丢失 → 平台承担上游成本。
- **方案**：settle 失败写 fallback 日志/重试队列（参照 usage_buffer.go:142-148 的 fallback log）；shutdown 时等待在途 settle（带超时）。

### 严重 S3-5：非超时网络错误一律按 timeout 扣费

- `handlers_chat.go:386-431,779-795`：任何 dispatchUpstream error（DNS/连接拒绝/TLS——上游根本没收到请求）都传 ErrorType:"timeout"。`pkg/billing/preconsume.go:213-217` 设计的"statusCode==0 且非 timeout → unfreeze"成为死代码。用户为未到达上游的请求付费。
- **方案**：区分错误类型（net.Error timeout vs 其他连接错误），后者传非 timeout 类型走 unfreeze。

### 严重 S3-6：并发限制计数器 panic 泄漏 + 多实例限流失效

- `internal/ratelimit/ratelimit.go:589-591` 释放依赖 c.Next() 返回后执行，**无 defer**；handler panic 时 +1 永不释放，清理循环（:1026-1038）只删 <=0 计数。累积 N 次 panic 后用户被永久 CONCURRENT_LIMIT_EXCEEDED。
- `ratelimit.go:107-119,1099-1147`：判定全走进程内 localCounter，Redis 只写不读。多实例部署限额放大 N 倍；重启清零。
- **方案**：释放改 defer；声明当前限流为"单实例语义"（文档+配置注释），或改为读 Redis 计数。

### 一般项

- G1：非流式上游调用 `sharedHTTPClient` Timeout:0（pkg/relay/base/adaptor.go:27-35），客户端不断开则 goroutine 无限阻塞；images/audio/embeddings 的 `http.NewRequest` 未挂 ctx。
- G2：无 Redis 时 nonce 防重放 TOCTOU（auth.go:487-502）。
- G3：BudgetController 数据竞争（pkg/routing/budget_controller.go:138-160,198-208）。
- G4：keypool 错误分类过宽（health.go:355-361，含 "quota"/"rate limit" 子串即冷却 60s）。
- G5：keypool 兜底放松 RPM 限制（selection.go:217-228）无日志/指标。
- G6：PreConsume 对 Portal 故障 fail-open（preconsume.go:113-130，有意设计，建议配告警）。
- S3-7 关机/重发路径：main.go:300 不等待最终 flush；audit Logger 在 Shutdown 前 Close；usage_buffer 批次失败后逐条重发依赖 Portal traceId 幂等（见 S2-4）；audit 批次重排队无上限可 OOM（internal/audit/audit.go:226-235）。

---

## 四、支付网关专项

路径相对 `ai-portal-go/`。核心资金安全设计（幂等入账、入账金额取 DB 值、success 回调向渠道核实）合格；问题集中在四渠道回调路径与金额换算，且**四个真实渠道的回调路径零测试**。

### 渠道 × 检查项矩阵

| 检查项 | Stripe | PayPal | Alipay | WeChat |
|---|---|---|---|---|
| 回调验签 | ✅ SDK ConstructEvent | ❌ 假验签（F4-1） | ✅ RSA-SHA256 fail-closed | ✅ RSA，但不校验时间戳（S4-5） |
| 幂等 | ✅ | ✅ | ✅ | ✅（completeOrder 条件更新） |
| 金额以服务端为准 | ✅ | ⚠️ 校验被跳过（S4-4） | ❌ 截断（S4-1） | ⚠️ CNY 浮点（S4-2） |
| 测试 | ⚠️ 无 webhook 测试 | ❌ 零测试 | ⚠️ 无验签测试 | ⚠️ 仅验签正反例 |

### 致命 F4-1：PayPal webhook 验签形同虚设

- `pkg/payment/paypal.go:98-103`：VerifySignature 仅 `return signature != ""`。任何带非空 `paypal-transmission-sig` 头的请求都能通过路由层验签（payment.go:537-541）。目前 HandleWebhook 恒返回 error 未直接入账，但属于"意外安全"。
- **方案**：实现 PayPal `verify-webhook-signature` API 调用（需 webhook_id + transmission-id/time/cert-url/auth-algo 头），或显式拒绝 webhook 路径直到实现。

### 严重 S4-1：Alipay 金额全链路整数截断——非整数订单"付了钱不入账"

- 创建：`alipay.go:46` 用 `MicroUSDToMajorUnit`（gateway.go:100 整除），$6.50 只收 6。
- 回调：`alipay.go:107-109` Sscanf 后 int64 截断，"10.50"→10。
- 结果：completeOrder 严格相等校验（payment.go:834）必 mismatch → 用户已扣款但拒绝入账，Alipay 反复重推反复失败。
- **方案**：换算改保留小数（字符串直传或分单位）；回调解析保留两位小数比较。

### 严重 S4-2：CNY 浮点 round-trip 误判拒单

- `gateway.go:85-90` CNYFenToMicroUSD 浮点连除后 int64 截断；payment.go:834 严格不等。某些金额回换算差 1 micro-USD → 合法支付被随机拒单。
- **方案**：金额校验加容差（±1 分对应 micro 值）。

### 严重 S4-3：匿名 GET 取消任意待支付订单

- 路由 `payment.go:69` `GET /billing/callback` 无鉴权；cancel 分支（:677-688）只凭 query order_id 即 UPDATE cancelled。订单号 `ORD-`+uuid 前 8 位 hex 仅 32 bit 熵，且出现在邮件/URL。GET 改状态对 CSRF/预取友好。
- **方案**：cancel 需登录且校验订单属主；或改 POST + CSRF。

### 严重 S4-4：PayPal 金额校验跳过 + 潜在 panic

- `paypal.go:105-143` QueryOrder 从不设 result.Amount，payment.go:822 `paidAmount > 0` 条件使校验整体跳过。
- `paypal.go:113` `order.PurchaseUnits[0].CustomID` 无长度检查，空 PurchaseUnits 时索引越界 panic（:128 有检查，不一致）。
- **方案**：QueryOrder 填 Amount；:113 补长度检查。

### 严重 S4-5：微信回调不校验时间戳

- `wechat.go:139-161` 只验 RSA 签名，不检查 Wechatpay-Timestamp 新鲜度（官方要求 5 分钟窗口）。completeOrder 幂等使当前影响有限，属实现不完整。

### 严重 S4-6：四渠道全部无退款路径

- Stripe 只认 checkout.session.completed（stripe.go:66），charge.refunded 忽略；微信只认 TRANSACTION.SUCCESS；订单状态机无 refunded（payment_test.go:151 标为非法）。退款/拒付后余额和订阅不回收。
- **方案**：状态机加 refunded；各渠道处理退款事件，回收余额/订阅。

### 一般项

- M4-1：completeOrder 不校验币种（USD 订单付同数值低值币种可少付）。
- M4-2：`getPaymentStatus` 只接受 UUID（payment.go:390），与 `ORD-xxx` 文本 order_id 不一致，当前为死代码。
- M4-3：`updateGateway` 无审计日志（支付密钥变更不留痕）。
- M4-4：Alipay 验签用 `c.Request.URL.Path`（payment.go:484-491），NotifyURL 带 query 或反代改写路径时全部拒签。
- M4-5：模拟模式是全局 DB 开关（payment.go:288-291），误开即免费充值，建议加启动告警/环境限制。
- M4-6：`ENCRYPTION_KEY` 缺失时密钥明文落库仅 Warn（payment.go:143-146），生产应 fail-fast。

---

## 五、单元测试覆盖缺口

路径相对 `ai-portal-go/`。DB 测试须 `-p 1`（internal/db 有破坏性 schema 测试）。

### 待补清单（按优先级）

| # | 包/函数 | 风险 | 类型 |
|---|---|---|---|
| 1 | routes/webhook_preconsume.go 全链路（667 行，零测试）：冻结-结算金额守恒、重复 requestID 幂等、结算>冻结溢出、GETDEL 原子性、企业 110% 硬停边界 | 预扣费资损/冻结泄漏 | DB+Redis，-p 1 |
| 2 | routes/payment.go completeOrder 金额校验 + 4 个 webhook handler：单位换算、少付/多付拒绝、重复 webhook 幂等、汇率缺失 | 少付入账/重复入账 | DB，-p 1 |
| 3 | pkg/payment paypal.VerifySignature（钉住当前语义并关联 F4-1 修复） | 认证绕过隐患 | 纯单测 |
| 4 | pkg/payment stripe/alipay 验签真实向量 + wechat decryptAESGCM：合法通过、篡改拒绝、错误密钥 fail-closed（仿 payment_test.go:114-143 临时 RSA 方案） | 伪造回调 | 纯单测 |
| 5 | services/pricing.go 算法：calculateTieredComponentCost(:314) 阶梯边界、resolveTimeBasedPrices(:372) 跨时区、matchScore(:500) tie-break；**断言绝对金额**——现有 TestPricingService_CalculateCostBatch 是自引用测试（batch 比 single，公式错也通过） | 计费错误 | 纯单测 + DB |
| 6 | routes/admin_pricing.go / admin_upstream_pricing.go 写接口（630 行零测试） | 错误定价上线 | DB，-p 1 |
| 7 | middleware SanitizeInput(:33，应 413 而非静默截断) / userSuspended(:86) / ValidateUserExists(:73) | 安全回归 | 纯单测 + DB |
| 8 | routes/system_settings_cache.go TTL 收敛/非法值回退/-race 并发 | 全局限流失效 | DB，-p 1，-race |
| 9 | services/quota.go 换算边界与 float64→int64 舍入语义（充值链路系统性向下抹零） | 金额抹零 | 纯单测 |
| 10 | enterprise_application* 上传白名单 isAllowedContentType / computeFileHash / 审批流 | 上传绕过 | 纯单测 + DB |
| 11 | pricing MatchRule 缓存（:413-450）失效/-race 并发 | 脏价/内存增长 | -race |
| 12 | datax import/export、admin_reconciliation 等报表 SQL 分页/日期边界 | 数据正确性 | DB，-p 1 |

### 结构性问题

- 全仓库 `sync.WaitGroup`/`t.Parallel` 出现 0 次——**无任何并发测试**。webhook_billing 日额度重置竞态、preconsume 并发冻结等需 `-race` 覆盖。
- 覆盖较好的参照标准：pkg/jwt、pkg/crypto、internal/middleware（ratelimit 含并发测试）、services/exchange.go（fallback 链）。
- 本机（Portal EC2）无 Postgres/Redis/Node，DB 测试只能跑 GitHub CI regression；纯单测可本地验证。

---

## 六、CI / E2E 配置问题

### 严重 S6-1：test-portal-quick.sh 是假门禁

- `ai-portal-go/tests/test-portal-quick.sh:155`：全文无 `exit $FAIL`，CI 步骤（ci-portal.yml:176-177）只要登录成功就永远绿。
- 被掩盖的过期端点：
  - `:115` 测 `/api/redemption/my-records` —— 实际是 `/api/voucher/records`（vouchers_common.go:45）；
  - `:129` 测 `/api/admin/enterprise/list` —— 实际是 `/api/admin/enterprise`（enterprise_common.go:82-85）。
- **方案**：末尾加 `[ "$FAIL" -eq 0 ] || exit 1`；修正两个端点（修完会立即暴露现存红状态）。

### 严重 S6-2：13 个 Playwright spec 全部不在 CI 运行

- ci-portal.yml regression 只跑 3 个 scan-*.js；`ui-smoke.spec.js`、`ui-all-pages.spec.js`（admin 23 页 + enterprise 8 页）、`ui-auth-flow.spec.js` 等 13 个 spec 无 CI 入口。
- CI 依赖安装（:148）缺 `@playwright/test`（spec 运行时依赖），即使加 `npx playwright test` 也会直接失败。
- **方案**：regression 增加 `npm ci && npx playwright test e2e/ui-smoke.spec.js e2e/ui-all-pages.spec.js --workers=1`。

### 严重 S6-3：支付/充值全流程无 e2e

- pkg/payment 四渠道、POST /billing/recharge 在 e2e 与 shell 测试中仅出现为 /super/payment 页面加载，无下单/回调测试（与第五节 #2 合并解决）。

### 一般项

- G6-1：`e2e/scan-console-errors.js:37-54` / `scan-a11y-axe.js:38-53` 页面清单漏 13 个现有页面（/admin/pricing、/admin/rate-limits、/admin/compliance、/admin/reconciliation-ops、/admin/upstream-pricing、/admin/usage-explorer、/admin/enterprise-applications、/enterprise/alerts、/enterprise/settings、/my-group、/billing-details、/upstream-ops、/subscriptions/my）。建议从单一来源（路由表或 ui-all-pages.spec.js）生成。
- G6-2：`ai-portal-go/tests/e2e/e2e_test.go:22-26` 是无 build tag 的死代码，TestMain 中 panic；`tests/loadtest/`、`tests/business/` 无 CI 入口。
- G6-3：ci-portal.yml Node 20 已 EOL（2026-04），建议升 22（workspace 已有 node-v22 工具链）。
- G6-4：CI `npm install playwright ...` 装 latest 与 package.json 锁定的 @playwright/test ^1.60 脱钩，浏览器缓存 key 不随版本变。
- G6-5：两个 workflow 的 govulncheck 用 @latest，建议 pin 版本。
- G6-6：proxy 的 tests/test-integration.sh、tests/integration/task*.sh、tests/e2e-accessapi-verification.sh 无 CI 入口，只能手动跑。

### 已核对无问题

- Action 版本全部主流最新（checkout@v4、setup-go@v5、golangci-lint-action@v7 + v2.5.0）。
- Go 版本一致：go.mod 1.25.5/toolchain 1.25.12，CI 1.25.12，Dockerfile golang:1.25-alpine。
- AGENTS.md 列出的 fast-gate 检查项 Portal CI 全覆盖；Proxy CI 覆盖全部命令。

---

## 七、修复路线图

> **进度（2026-07-26）**：批次 1、批次 2 已全部完成并提交。
> - 1.1 proxy `434f9cd` 余额缓存：缺键哨兵值（-2）+ `handled` 返回值、SettleLocal 条件 INCRBY；新增 3 个回归测试（含 Redis 路径，经本机临时 Redis 验证）。
> - 1.2 proxy `2a2a997` 免单路径：success 无 usage 改按预扣估值扣费（新 errorType `usage_missing`）、流式中断按已产出 token 计费+审计；新增 3 个测试。
> - 1.3 portal `3b69dee` 计费：订阅扣量 UPDATE 按订阅 id 过滤（批量/单条/计数器三处 + validate LATERAL 取最新订阅）、billing 队列 trace 级幂等（SETNX 24h，重试释放标记）、入队失败同步直扣 + 死信；新增多订阅 DB 测试与幂等 miniredis 测试（本机 Postgres 测试库验证，`go test -p 1` 全绿）。
> - 1.4 portal `f382e65` 支付：PayPal 验签 fail-closed、Alipay 金额两位小数 + 回调按百分之一单位携带、CNY 容差 1 分、取消订单需登录属主（OptionalAuth）；新增 5 个纯单测。
> - 2.1 portal `e9322dc` 重置 token/验证码不再落日志（3 处）。
> - 2.4 proxy `33dc1e5` ratelimit 并发计数改 defer 释放（修 panic 泄漏导致永久锁死）；新增 panic 回归测试。
> - 2.6 proxy `6fd1a7d` 网络错误分类：仅真超时才计费，其余 transport_error 退款（15 处 settle 点）；settleLocal 同步对齐。
> - 2.5 proxy `dcfed9b` /v1/responses 补 portal pre-consume 兜底：新增 preConsumeState + settlePreConsume 统一双侧结算（23 处 settle 点）。
> - 2.3 portal `02b994a` 验证码按码失败 5 次作废、send-code 按 IP 限流、Redis nil 防护。
> - 2.2 portal `b2f3c27` 角色降级即时生效：RequireAuth/OptionalAuth/web cookie 三处角色改读 DB。
> - 2.7 portal `e37af64` PayPal PurchaseUnits 越界 panic 修复；Stripe charge.refunded 退款回收余额（幂等），订阅订单仅标记人工处理。**遗留**：微信/支付宝退款通知、PayPal 真验签、订阅退款自动回收未实现。
>
> 部署状态：批次 1 已部署两侧（2026-07-26）；批次 2 尚未部署。
>
> **延后实现（已标记，待排期）**：
> - Anthropic（/v1/messages）流式中断的部分 token 计费与审计——1.2 只覆盖了 chat completions 路径；修法参照 `handlers_chat.go` 中断分支（按已累积 usage 结算 + recordUsage + audit）。涉及 `handlers_chat.go:2385` 附近与 anthropic passthrough 流式路径。
> - 微信/支付宝退款通知处理、PayPal verify-webhook-signature 真验签、订阅退款自动回收。
>
> **进度（2026-07-26，批次 3）**：门禁修复完成并提交。
> - 3.1 portal `fe29e50`：`test-portal-quick.sh` 失败时 exit 1（此前是永不失败的假门禁）；修正 2 个过期端点（已对本机 portal 验证：新端点 401 存在、旧端点 404）。
> - 3.2 portal `9a7ec96`：CI regression 接入 `ui-smoke` + `ui-all-pages` Playwright spec；发现 package.json/lock 被 .gitignore 忽略，改为在 workflow 中 pin 版本（`@playwright/test@1.60.0` 等），浏览器缓存 key 改为 workflow 文件 hash。
> - 3.3 portal `2d29180`：console/a11y 扫描页面清单补齐 13 个页面。
> - 3.4 portal `2b863ef`：tests/e2e Go 死套件加 `//go:build e2e`；Node 20→22；govulncheck pin v1.6.0（含 proxy `1c3c29f`）。
> - 注意：CI 首次运行新门禁（quick.sh exit code、Playwright spec、新增扫描页面）可能暴露现存红状态，属预期。
>
> **进度（2026-07-26，批次 4）**：enterprise 前端统一 + 测试补齐完成并提交。
> - 前端 `66eb211`：users/audit/alerts 分页（table-pagination.js）、audit 日期改 flatpickr、app.css 补定义 form-input-sm/form-select-sm/btn-outline-secondary、页签统一 .tabs/.tab-btn（删 .tab-nav 与 usage 私有样式）、stats 图标统一 24px（7 页 27 处）、图表颜色走 chartColor 主题变量 + 画布统一 chart-canvas、空态统一 empty-state-sm+图标（6 页）、i18n 修硬编码英文/错误文案 + 新增 9 个 key、dashboard 页内重复 style 清理、app.css v32。验证：tailwind rebuild 无 diff、i18n --strict 通过、模板 smoke 通过。
> - 测试 `b0ef543`：pricing 阶梯/分时/打分绝对值测试（修"自引用测试"缺口）、quota 截断语义、Alipay 真实 RSA 验签向量、WeChat AES-GCM、SanitizeInput、completeOrder 金额校验 DB 测试（少付拒单/精确入账幂等/CNY 容差）。
> - 遗留：keys.html 卡片布局未分页；金额精度规范（E-5）未统一；#1 preconsume、#6 admin_pricing、#8 settings cache、#10 enterprise 申请、#11 pricing 缓存并发、#12 datax/对账测试未补（见第五节表格）。

按"资损 → 安全 → 门禁 → 体验 → 测试"排序，分四批。

### 批次 1：资损簇（灰度前必须）

| 项 | 位置 | 内容 |
|---|---|---|
| 1.1 | proxy internal/auth/auth.go | F3-1 余额缓存：Lua 缺键哨兵值、setBalance TTL、SettleLocal 缺键处理、handlers_common 判断条件 |
| 1.2 | proxy handlers_chat.go | S3-2 usage 缺失免单（按预扣估值 charge）、S3-3 流式中断结算 |
| 1.3 | portal webhook_billing/usage | S2-2 多订阅扣量（UPDATE 加订阅 id 过滤）、S2-3 计费丢失死信、S2-4 扣款幂等 |
| 1.4 | portal payment | F4-1 PayPal 验签、S4-1 Alipay 截断、S4-2 金额容差、S4-3 匿名取消 |

### 批次 2：安全簇

| 项 | 位置 | 内容 |
|---|---|---|
| 2.1 | portal web/auth.go:335、profile.go:316 | 重置 token/验证码不落日志 |
| 2.2 | portal middleware/auth.go | 角色降级 DB 校验或 token_version |
| 2.3 | portal routes/auth.go | 验证码按码失败上限、Redis nil 检查、send-code 按 IP 限流 |
| 2.4 | proxy ratelimit.go | 并发计数 defer 释放 |
| 2.5 | proxy handlers_responses.go | 补 PreConsume 兜底 |
| 2.6 | proxy handlers_chat.go:386 | 网络错误分类（非 timeout → unfreeze） |
| 2.7 | portal payment.go / paypal.go | PayPal panic、退款状态机（S4-6） |

### 批次 3：门禁修复（会暴露现存红状态）

| 项 | 位置 | 内容 |
|---|---|---|
| 3.1 | tests/test-portal-quick.sh | exit code + 2 个过期端点 |
| 3.2 | ci-portal.yml | 接入 ui-smoke + ui-all-pages spec（npm ci 装 @playwright/test） |
| 3.3 | e2e/scan-*.js | 页面清单同步/单一来源化 |
| 3.4 | ci-portal.yml / ci-proxy.yml | Node 22、pin govulncheck、tests/e2e Go 死套件加 build tag |

### 批次 4：Enterprise 风格统一 + 测试补齐

- 风格：T-2 分页 → D-1 日期 → E-2/E-3 死 class → T-1 页签 → I-1 图标 → C-1/C-2 图表 → T-3 空态 → L-1 i18n → E-1/E-4/E-5 样式欠账。改完跑 `bash scripts/rebuild-tailwind.sh`（无 diff）+ i18n audit（--strict）。
- 测试：按第五节表格 #3→#4→#5→#9 先纯单测（本地可验证），#1→#2→#6→#7→#8 DB 测试随 CI regression 验证。

### 验证基线

- Portal fast-gate：`go build`、`go vet ./...`、`gofmt -l .`、单测、`check_schema_vs_code.sh`、`i18n_full_audit.py --strict`、`rebuild-tailwind.sh` 无 diff。
- Proxy：`make build && make test`、`go vet ./... && test -z "$(gofmt -l .)"`。
- 修复计费/支付相关后，在 CI regression（Postgres 15 + Redis 7）跑 DB 测试 `-p 1` 确认。

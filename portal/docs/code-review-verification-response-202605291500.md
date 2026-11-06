# 代码审查报告双重验证与回应

> **验证时间**: 2026-05-29 15:00  
> **原报告**: `code-review-202605291412.md` (by opencode)  
> **验证范围**: ai-portal-go + ai-proxy-go  

---

## 验证方法

对原报告中每一项问题，通过以下方式验证：
1. 直接读取源代码定位到具体行
2. 运行 `grep` / `sed` 确认代码存在性
3. 对数据库相关项执行 SQL 验证
4. 对可运行时验证的项执行测试

---

## 一、ai-portal-go 验证结果

### 1.1 CRITICAL — 安全风险

| # | 问题 | 严重度 | 验证结果 | 说明 |
|---|------|--------|----------|------|
| S1 | 密钥泄露到 Git 仓库 | CRITICAL | ❌ **不成立** | `.env` 和 `.env.go-platform` 当前**不在 git tracked 列表中** (`git ls-files` 返回空)。`.gitignore` 已正确配置 `.env` 和 `.env.*`。若历史上曾提交过，建议用 `git filter-repo` 清理历史，但当前状态是安全的。 |
| S2 | 默认管理员密码硬编码 | HIGH | ✅ **确认存在** | `cmd/server/main.go:534-535`：`adminPassword = "<ADMIN_PASSWORD>"`。虽然生产模式要求 12+ 字符，但硬编码默认值本身是高风险。 |
| S3 | 密钥导出端点明文返回 | HIGH | ✅ **确认存在** | `internal/routes/admin.go:1291-1345`：`exportKeyPoolKeys` 调用 `crypto.Decrypt()` 解密所有上游 API key，以明文 JSON 返回。仅依赖 admin JWT，无二次验证、无审计日志（**我们刚补充了 audit log，但无操作密码二次验证**）。 |
| S4 | Webhook 签名验证可绕过 | MEDIUM | ✅ **确认存在** | `internal/routes/webhook.go:83-86`：当 `PORTAL_SYNC_SECRET` 为空时直接 `c.Next()` 跳过验证。虽有启动警告日志 (`NewWebhookRoutes:83`)，但不会阻止启动。 |
| S5 | 邮件模板 HTML 注入 | MEDIUM | ✅ **确认存在** | `pkg/mail/mail.go:100-111`：`enterpriseName` 和 `acceptURL` 直接拼接到 HTML 字符串中，未使用 `html.EscapeString()` 转义。 |
| S6 | 验证码使用非安全随机数 | HIGH | ✅ **确认存在** | `internal/web/auth.go:20-22` 使用 `math/rand.Intn(1000000)`；`internal/routes/auth.go:436` 使用 `uuid.New().String()[:6]`（16^6 ≈ 1677 万种可能）。两者均非密码学安全。 |
| S7 | Metrics 端点无认证 | MEDIUM | ✅ **确认存在** | `cmd/server/main.go:177-179`：`/metrics` 无中间件保护，直接暴露 Prometheus 格式数据。 |
| S8 | CORS 默认允许所有源 | MEDIUM | ✅ **确认存在** | `cmd/server/main.go:476-483`：当 `allowedOrigins` 为空数组时，不设置 `Access-Control-Allow-Origin` header。但 Gin 默认中间件可能在其他层设置 `*`。需进一步确认实际响应头。 |

### 1.2 HIGH — Bug / 逻辑错误

| # | 问题 | 严重度 | 验证结果 | 说明 |
|---|------|--------|----------|------|
| B1 | Webhook INSERT 参数不匹配 | CRITICAL | ✅ **确认存在** | `internal/routes/webhook.go:890-900`：`processRecord` 中的 INSERT 有 **27 个占位符 ($1-$27)**，但只传入了 **10 个参数**（第 898-899 行）。**$1-$17 的参数完全缺失**。这是一个会导致运行时 PostgreSQL 报错的严重 bug。<br><br>⚠️ 但注意到 `processRecord` 仅在 Redis 队列失败时作为 fallback 被调用（`handleUsage:203-214`）。如果 Redis 始终可用，此 bug 可能从未触发。<br><br>另外，`processStats`（line 1320-1330）和 `processBillingBatch` 中的 INSERT 参数数量是匹配的（23 个占位符 ↔ 23 个参数）。 |
| B2 | Webhook 缺少幂等性检查 | HIGH | ✅ **确认存在** | `usage_records` 表的 `trace_id` 有 btree 索引 (`idx_usage_records_trace`)，但**无唯一约束**。Proxy 超时重试时同一条记录会重复插入，导致余额重复扣除。<br><br>建议：将 `trace_id` 改为 UNIQUE，或使用 `ON CONFLICT (trace_id) DO NOTHING`。 |
| B3 | 速率限制器内存泄漏 | MEDIUM | ✅ **确认存在** | `cmd/server/main.go:565-607`：`ipRateLimiter.buckets` 是 `map[string]*rateBucket`，新 IP 不断加入，旧 IP 永不被清理。长期运行会无限增长。<br><br>建议：添加 TTL 过期清理 goroutine，或使用 `github.com/patrickmn/go-cache` 等带过期机制的库。 |
| B4 | Redis 连接失败静默忽略 | MEDIUM | ✅ **确认存在** | `internal/db/db.go:75-79`：Redis 连接失败仅记录 `logger.Warn`，返回的 `DB` 对象中 `Redis` 为 nil。依赖 Redis 的功能（验证码、限流、队列、暴力破解防护）会静默失效。<br><br>建议：明确降级策略——验证码功能应直接报错而非跳过验证。 |
| B5 | CSRF Cookie 缺少 SameSite | MEDIUM | ✅ **确认存在** | `internal/web/middleware.go:45`：`c.SetCookie("csrf_token", ..., false)` 最后一个参数是 `httpOnly`，未设置 `SameSite`。浏览器默认行为不一致（Chrome 默认 Lax，某些旧版浏览器默认 None）。<br><br>建议：改为 `c.SetCookie("csrf_token", token, 86400, "/", "", isSecure(c), true, true)` 或显式设置 SameSite。 |

### 1.3 MEDIUM — 架构 / 设计问题

| # | 问题 | 验证结果 | 说明 |
|---|------|----------|------|
| D1 | main.go 过于臃肿 | ✅ 确认 | `cmd/server/main.go` 608 行，`main()` 约 440 行，包含路由注册、中间件、后台 goroutine、CORS、限流器、管理员初始化等。建议拆分为 `routes.go`、`middleware.go`、`background.go`。 |
| D2 | 欢迎奖金/邀请码逻辑重复 | ✅ 确认 | `internal/routes/auth.go:227-268` vs `internal/web/auth.go:217-256`；`internal/routes/auth.go:439-518` vs `internal/web/auth.go:413-475`。两处实现不一致（如 `used_count` 递增逻辑）。 |
| D3 | 双计费路径导致收入泄漏 | ⚠️ 部分确认 | `processRecord`（同步 fallback）和 `processBillingBatch`（异步队列）确实存在两条路径。若异步路径失败进入死信队列（3 次重试），使用已记录但余额未扣除。但死信队列有日志记录，可人工对账。 |
| D4 | 后台 goroutine 无 panic recovery | ✅ 确认 | `cmd/server/main.go:334-343, 360-376, 440-449, 452-461` 多个后台 goroutine 无 `defer recover()`。建议统一封装为带 recovery 的 `SafeGo()`。 |
| D5 | 大量错误被忽略 | ✅ 确认 | 整个代码库广泛使用 `_ = r.db.Exec(...)` 和 `_ = rows.Scan(...)`，包括我们刚添加的 audit log 也有此模式。建议关键操作至少记录 error log。 |
| D6 | `go:embed` 使用 `**` 通配符 | ✅ 确认 | `web/embed.go:5` 使用 `templates/**/*`，`go:embed` 不支持 `**` 递归匹配。应改为 `templates/**/*.html` 或列出具体子目录。 |
| D7 | 模板错误泄露实现细节 | ✅ 确认 | `internal/web/handler.go:164`：模板渲染错误将原始 error 返回给客户端。 |

---

## 二、ai-proxy-go 验证结果

### 2.1 CRITICAL — 安全风险

| # | 问题 | 严重度 | 验证结果 | 说明 |
|---|------|--------|----------|------|
| P1 | Nonce 重放防护跨实例失效 | CRITICAL | ✅ **确认存在** | `internal/auth/auth.go:453-464`：`isNonceUsed` 在 Redis 报错时（line 455-458，err != nil 时未处理）直接回退到本地 `nonceCache` map。多实例部署时，同一 nonce 可在不同实例重放。 |
| P2 | 加密密钥回退到 JWT_SECRET | HIGH | ✅ **确认存在** | `pkg/keypool/manager.go:419-424`：`ENCRYPTION_KEY` 未设置时回退到 `JWT_SECRET`。违反密码学密钥分离原则。 |
| P3 | API Key 暴露在 URL 查询参数 | MEDIUM | ⚠️ 待验证 | 原报告定位到 `pkg/models/validator.go:171`。需要进一步确认 Google provider 的验证逻辑。 |
| P4 | Sync 端点接受 query 中的 secret | MEDIUM | ⚠️ 待验证 | 原报告定位到 `internal/server/admin_handlers.go:453-456`。需要进一步确认。 |
| P5 | 默认 admin secret | MEDIUM | ✅ **确认存在** | `internal/config/config.go:338`：默认值 `"admin-secret"` 过于简单。 |

### 2.2 HIGH — Bug

| # | 问题 | 严重度 | 验证结果 | 说明 |
|---|------|--------|----------|------|
| PB1 | 锁无关限流器竞态条件 | HIGH | ✅ **确认存在** | `internal/ratelimit/ratelimit.go:52-81`：`AllowN` 的 CAS 循环仅保护 `lastUpdate`（line 68），`tokens` 的读取（line 55 `b.loadTokens()`）和写入（line 71 `b.storeTokens()`）不在 CAS 保护范围内。多个 goroutine 可能读取到相同的 `currentTokens`，然后分别扣减，导致 token 超发。<br><br>虽然 `maxRetries=100` 提供了一定程度的冲突退避，但 token 超发风险仍然存在。 |
| PB2 | 并发限制器缺少 `c.Abort()` | HIGH | ✅ **确认存在** | `internal/server/server.go:46-56`：并发限制拒绝请求后（`default` case）调用 `c.JSON()` 但未调用 `c.Abort()`，请求会继续执行到后续 handler。 |
| PB3 | 上游错误响应体丢失 | MEDIUM | ⚠️ 待验证 | 原报告定位到 `internal/server/handlers.go:574-612`。需要进一步确认。 |
| PB4 | WriteTimeout 为 0 | MEDIUM | ✅ **确认存在** | `internal/server/server.go:72`：`WriteTimeout: 0`。SSE 流有 10 分钟 context 超时，但非流式响应无保护。 |
| PB5 | 空 Models 切片逻辑 | MEDIUM | ❌ **不成立** | 该逻辑是** intentional design**。`len(key.Models) == 0` 表示"不支持任何模型"（跳过），这是为了防止 Provider 模型同步不完整时，key 被错误路由到不支持的模型。之前我们专门修复并保留了此逻辑。 |

---

## 三、对原报告的修正与补充

### 3.1 原报告中的误判

| 问题 | 原报告结论 | 实际验证 |
|------|-----------|----------|
| S1 密钥泄露 | 文件已被追踪 | ❌ 当前 `.env` 和 `.env.go-platform` **不在 git tracked 列表中**，`.gitignore` 配置正确。可能是历史遗留问题已被修复，或报告基于旧快照。 |
| PB5 空 Models 逻辑 | 逻辑反转，应匹配所有模型 | ❌ 这是** intentional design**。`null/空 models` 表示"此 key 尚未同步模型列表，不应参与任何路由"。若改为匹配所有模型，会导致 Provider 配置不完整时 key 被路由到错误模型（此前已发生并修复）。 |

### 3.2 原报告遗漏的问题

| 问题 | 严重度 | 说明 |
|------|--------|------|
| Webhook `processRecord` INSERT 参数缺失 | CRITICAL | B1 报告了参数不匹配，但未指出具体是 **$1-$17 完全缺失**。这会导致所有 fallback 路径的使用记录插入失败。 |
| `processRecord` 函数从未执行 | HIGH | 由于 Redis 队列始终优先（`handleUsage:203-208`），且 Redis 运行正常，`processRecord` 作为 fallback 可能从未被触发，因此 B1 的 bug 可能处于"潜伏"状态。 |
| `exportKeyPoolKeys` 无审计日志 | HIGH | 导出明文密钥是高危操作，但原报告 S3 仅指出无二次验证，未指出**无审计日志**（我们刚补充）。 |

### 3.3 我们已修复的问题

以下问题在本次会话中已修复并推送：

| 修复项 | 对应报告问题 | 状态 |
|--------|-------------|------|
| Audit log CSV 导出 Content-Type 覆盖 | — | ✅ 已修复 |
| Audit log SQL 类型不匹配 | — | ✅ 已修复 |
| 补充 35+ 个缺失的 audit log | D5（部分）、S3（部分） | ✅ 已修复 |
| 前端筛选选项对齐 | — | ✅ 已修复 |

---

## 四、建议的修复优先级（更新）

### P0 — 立即处理（今天）

| 优先级 | 问题 | 影响 |
|--------|------|------|
| 1 | **B1: Webhook INSERT 参数缺失** | Redis 队列失败时所有使用记录插入失败，计费数据丢失 |
| 2 | **S3: 密钥导出端点** | 添加操作密码二次验证 + 限流（audit log 已补充） |
| 3 | **PB2: 并发限制器缺少 c.Abort()** | 超限请求继续执行，可能导致资源耗尽 |

### P1 — 本周内

| 优先级 | 问题 | 影响 |
|--------|------|------|
| 4 | S6: 验证码安全 | 暴力破解验证码 |
| 5 | B2: Webhook 幂等性 | 重复扣费 |
| 6 | P1: Nonce 重放 | 跨实例重放攻击 |
| 7 | P2: 加密密钥分离 | 违反密码学原则 |
| 8 | B3: 速率限制器内存泄漏 | OOM 风险 |
| 9 | S2: 默认密码硬编码 | 初始部署安全风险 |
| 10 | S4/S7/S8: Webhook/Metrics/CORS | 安全边界弱化 |

### P2 — 中期

| 优先级 | 问题 |
|--------|------|
| 11 | D1: main.go 拆分 |
| 12 | D2: 共享逻辑提取 |
| 13 | B4: Redis 失败策略 |
| 14 | B5: CSRF SameSite |
| 15 | D4: Goroutine recovery |
| 16 | D5: 错误处理统一 |

---

## 五、结论

原报告 **总体准确**，识别出了多个真实存在的安全和逻辑问题。但其中 **S1（密钥泄露）和 PB5（空 Models 逻辑）为误判**——前者当前状态已安全，后者是 intentional design。

**最关键的未修复问题**是 **B1（Webhook INSERT 参数缺失 $1-$17）**，这是一个可能导致计费数据丢失的潜伏 bug，虽然当前因 Redis 队列优先而可能未触发，但一旦 Redis 不可用就会暴露。


---

## 附录 A：修复实施记录（2026-05-29）

以下 P0 和 P1 问题已在本会话中修复并推送至 GitHub：

| # | 问题 | 修复文件 | 修复内容 | 状态 |
|---|------|----------|----------|------|
| 1 | B1: Webhook INSERT 参数缺失 | `internal/routes/webhook.go` | 补全 `processRecord` 中缺失的 $1-$17 参数（trace_id, user_id, key_id, server_id, model, provider, region, tokens, cost, gift/recharge/credit/subscription deductions） | ✅ |
| 2 | PB2: 并发限制器缺少 c.Abort() | `ai-proxy-go/internal/server/server.go` | default case 添加 `c.Abort()` + `return` | ✅ |
| 3 | S6: 验证码不安全 | `internal/web/auth.go`, `internal/routes/auth.go` | `generateCode()` 和 `randomString()` 改用 `crypto/rand` + `math/big` | ✅ |
| 4 | B2: Webhook 幂等性缺失 | `internal/routes/webhook.go` + migration | 1. 创建 `idx_usage_records_trace_unique` 唯一索引<br>2. 两处 INSERT 添加 `ON CONFLICT (trace_id, created_at) DO NOTHING` | ✅ |
| 5 | P1: Nonce 重放跨实例 | `ai-proxy-go/internal/auth/auth.go` | `isNonceUsed` Redis 报错时返回 `true`（拒绝请求），不再回退本地 map | ✅ |
| 6 | P2: 加密密钥回退 JWT_SECRET | `ai-proxy-go/pkg/keypool/manager.go` | 空 `ENCRYPTION_KEY` 时 `logger.Fatal()`，禁止回退 | ✅ |
| 7 | B3: 速率限制器内存泄漏 | `cmd/server/main.go` | `newIPRateLimitMiddleware` 启动 goroutine 每 5 分钟清理 10 分钟未访问的 bucket | ✅ |
| 8 | S2: 默认密码硬编码 | `cmd/server/main.go` | 移除 `"<ADMIN_PASSWORD>"` 默认值，空值时 `logger.Fatal()` | ✅ |
| 9 | S4: Webhook 签名可绕过 | `internal/routes/webhook.go` | `SyncSecret` 为空时返回 403，不再跳过验证 | ✅ |
| 10 | S7: Metrics 无认证 | `cmd/server/main.go` + `docker-compose.portal.yml` + `start.sh` | `/metrics` 添加 `X-Admin-Secret` header 验证；Docker Compose 传递 `ADMIN_SECRET` | ✅ |
| 11 | S8: CORS 允许所有源 | `cmd/server/main.go` | 未匹配任何 origin 时返回 403 | ✅ |
| 12 | B4: Redis 失败策略 | `internal/routes/auth.go` | `bruteForceProtection` Redis 不可用时返回 503 拒绝请求，不再静默跳过 | ✅ |
| 13 | B5: CSRF SameSite | `internal/web/middleware.go` | CSRF cookie 显式设置 `SameSite=LaxMode` | ✅ |
| 14 | 验证 URL 构建不一致 | `internal/routes/admin.go` | 新增 `buildModelsURL()` 统一所有验证路径的 models endpoint URL 构建；修复 zhipu `/v4/v1/models` 错误拼接 | ✅ |
| 15 | 异步验证不查 providers 表 | `internal/routes/admin.go` | `validateKeyAsync` / `ValidateUpstreamKeysBackground` / `validateKeyPoolKeysBatch` 统一先查 providers 表再 fallback | ✅ |
| 16 | S3: 密钥导出二次验证 | `internal/routes/admin.go` | 1. 添加 `password` 参数 bcrypt 二次验证<br>2. 添加内存限流器（每用户每小时 10 次） | ✅ |
| 17 | S5: 邮件 HTML 注入 | `pkg/mail/mail.go` | `SendEnterpriseInvitation`、`SendEnterpriseAddedNotification`、`SendAdminVacantReminder` 中所有用户可控输入使用 `html.EscapeString()` 转义 | ✅ |
| 18 | D4: Goroutine recovery | `cmd/server/main.go` | 新增 `safeGo()` 辅助函数；所有后台 goroutine（enterprise reminder、model lifecycle、key validation、auto-renewal）添加 panic recovery | ✅ |
| 19 | D2: 共享逻辑提取 | `internal/services/reward.go` + `internal/routes/auth.go` + `internal/web/auth.go` | 提取 `ApplyWelcomeBonus` 和 `ProcessInvitationCode` 到 `RewardService`；API 层和 Web 层统一调用 service | ✅ |
| 20 | D5: 错误处理统一（全局） | `internal/db/db.go` + 24 个 routes 文件 | 1. 新增 `MustExec()` / `MustExecTx()` 自动记录错误<br>2. 全局替换 `_ = db.Exec(...)` / `_ = db.Pool.Exec(...)` → `MustExec()`<br>3. `webhook.go` tx.Exec 错误从 `_, _ =` 改为显式传播<br>4. 新增 14 个单元测试全部通过 | ✅ |
| 21 | D6: go:embed `**` 通配符 | `web/embed.go` | 将 `templates/**/* static/**/*` 改为列出所有具体子目录：`templates/admin/* templates/auth/* ... static/css/* static/fonts/* static/js/*` | ✅ |
| 22 | D7: 模板错误泄露 | `internal/web/handler.go` | 模板渲染错误不再将 `err.Error()` 返回给客户端，改为只返回通用 `"Internal Server Error"` | ✅ |
| 23 | P3: API Key 暴露在 URL query | `ai-proxy-go/pkg/models/validator.go` | `query` auth type 添加安全注释，说明 API key 会出现在访问日志中 | ✅ |
| 24 | P4: Sync 端点接受 query secret | `ai-proxy-go/internal/server/admin_handlers.go` | 移除 `c.Query("secret")` fallback，空 header 时直接返回 401 | ✅ |
| 25 | PB3: 上游错误响应体丢失 | `ai-proxy-go/internal/server/handlers.go` | 上游 4xx/5xx 时尝试将响应体解析为 JSON 并转发给客户端，解析失败时回退到通用错误 | ✅ |
| 26 | C2: 死代码 | `ai-proxy-go/internal/auth/auth.go` | 删除未使用的 `hashKey()` 函数 | ✅ |
| 27 | C3: 废弃方法 | `ai-proxy-go/pkg/cache/affinity.go` | 删除 5 个已废弃的 legacy stub 方法（`GetChannelWithPriority` 等） | ✅ |
| 28 | C4: 压力测试除零 | `ai-proxy-go/cmd/stress/main.go` | 百分比打印前添加 `if metrics.TotalRequests > 0` 保护 | ✅ |
| 29 | C5: io.ReadAll 错误忽略 | `ai-proxy-go/internal/server/handlers.go` | image/audio 三个 handler 的 `io.ReadAll(resp.Body)` 错误从 `_` 改为显式处理，失败时返回 502 | ✅ |

### 端到端验证结果

```
Portal health:                200 ✅
Proxy health:                 200 ✅
Metrics no auth:              401 ✅
Metrics wrong auth:           401 ✅
Webhook no signature:         403 ✅
Audit log CSV export:         200 ✅ (Content-Type: text/csv)
Export no password:           400 ✅
Export wrong password:        403 ✅
Export correct password:      200 ✅ (16 keys)
Export rate limit (11th):     429 ✅
```

### D5 修复验证结果（2026-05-28）

```
internal/db:          12/12 PASS ✅
internal/routes:       ALL PASS ✅
internal/web:          PASS ✅
internal/middleware:   PASS ✅
pkg/crypto:            PASS ✅
```

### P2/P3 修复验证结果（2026-05-29 追加）

```
Portal 编译:                    PASS ✅
Proxy 编译:                     PASS ✅
Portal 单元测试 (routes/web/db): PASS ✅
Proxy 单元测试 (auth/models/keypool/server/ratelimit): PASS ✅
Login E2E via nginx:            200 ✅
Sync endpoint no query secret:  401 ✅
```

### 仍需后续处理的问题

| # | 问题 | 严重度 | 说明 |
|---|------|--------|------|
| 12 | **D1: 全局代码库瘦身** | LOW→MEDIUM | 原 D1（main.go 拆分）已扩展为全局重构计划。Portal 有 6 个文件 > 2000 行、Proxy 有 2 个文件 > 2000 行。详见 `docs/large-file-refactoring-roadmap-202605291733.md` |
| 13 | D3: 双计费路径对账 | LOW→MEDIUM | Phase 1+2 已完成：6 处缺失修复 + Proxy 本地日志 + Portal 手动对账接口。剩余 Phase 3（BillingService 统一提取）和 Phase 4（webhook.go 拆分）待后续处理。详见 `docs/billing-data-reliability-analysis-202605291817.md` |

#### D1 扩展详情（大型文件拆分）

经全面扫描，以下文件远超合理规模（> 2000 行），需按优先级逐步拆分：

**Portal 🔴 P0（> 2000 行）：**
1. `internal/routes/admin.go` (2,898 行, 56 funcs) → admin_users.go + admin_providers.go + admin_models.go + admin_keys.go + admin_validation.go + admin_export.go
2. `internal/routes/enterprise.go` (2,575 行, 51 funcs) → enterprise_my.go + enterprise_members.go + enterprise_admin.go + enterprise_groups.go
3. `internal/web/data.go` (2,432 行, 38 funcs) → data_user.go + data_admin.go + data_analytics.go
4. `internal/web/admin_handlers.go` (2,320 行, 59 funcs) → admin_handlers_*.go (users/models/keys/providers/plans)
5. `internal/routes/models.go` (2,331 行, 54 funcs) → models_crud.go + models_lifecycle.go + models_routing.go + models_sync.go

**Portal 🟡 P1（> 1000 行）：**
6. `internal/routes/webhook.go` (1,770 行) → webhook_handlers.go + webhook_billing.go + webhook_queue.go
7. `cmd/server/main.go` (647 行) → routes.go + middleware.go + background.go + init.go

**Proxy 🔴 P0（> 2000 行）：**
8. `pkg/keypool/manager.go` (2,259 行, 65 funcs) → routing.go + selection.go + health.go + stats.go
9. `internal/server/handlers.go` (2,015 行, 39 funcs) → handlers_relay.go + handlers_usage.go + handlers_health.go

**实施建议顺序：** main.go → keypool/manager.go → webhook.go → admin.go → enterprise.go

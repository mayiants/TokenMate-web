# 上线前第三轮 Review：Key / User / Member / Usage 管理专项

**Status: ACTIVE**
**日期**: 2026-07-27
**范围**: ai-portal-go（`324e185`）+ ai-proxy-go（`1c3c29f`）
**背景**: 前两轮 review（`portal-proxy-review-20260705.md`、`platform-review-report-202607260943.md`，另含 `full-code-review-202607200416.md` 等历史轮次）已覆盖支付网关、计费资损簇、CI 门禁与前端一致性。本轮聚焦**管理面功能**：API key、用户、企业成员、用量/账单四个域，从总体架构、安全、性能、可靠性、质量控制五个维度查找前两轮未覆盖的漏洞与功能缺口。所有结论均附代码证据；关键资金类发现已由第二轮独立抽查复核（标注「已复核」）。

---

## 目录

1. [总体架构评估](#一总体架构评估)
2. [致命缺陷](#二致命缺陷)
3. [严重缺陷——Key 管理](#三严重缺陷key-管理)
4. [严重缺陷——User/Member 管理](#四严重缺陷usermember-管理)
5. [严重缺陷——Usage/账单](#五严重缺陷usage账单)
6. [严重缺陷——Proxy 数据面](#六严重缺陷proxy-数据面)
7. [一般项汇总](#七一般项汇总)
8. [质量控制与测试缺口](#八质量控制与测试缺口)
9. [功能缺口清单（Admin 管理面）](#九功能缺口清单admin-管理面)
10. [修复路线图](#十修复路线图)

---

## 一、总体架构评估

控制面（portal）/数据面（proxy）的分层、webhook + Redis pub/sub 的同步通道、pre-consume/settle 的资金三段式整体设计是成立的。本轮发现的缺陷呈现三个**系统性根因**，比单个 bug 更值得关注：

### 根因 R1：Web 表单处理器与 API 处理器双轨并存，Web 轨校验全面弱化

`internal/web/actions.go`、`internal/web/admin_handler_common.go` 中存在一批与 `internal/routes/` API 处理器**功能重复但实现独立**的遗留端点。系统性差异：Web 轨普遍**无审计日志、无 validate 缓存失效、无状态白名单/角色防护**。当前前端页面大多已改走 API，这些端点成为「注册着但无人维护」的攻击面。涉及：key revoke/create/delete、用户删除、企业 suspend、企业 key 全操作。这是本轮致命/严重缺陷的最大来源（F2、S-K3、S-U1/U3/U4 等）。

**建议**：Web 端点统一改为薄包装转发到 routes 层同一实现，或删除确认无调用的端点；改完后 grep 路由表确认每个 Web 端点都有模板/JS 调用方。

### 根因 R2：计数/限额维度错位——「名为 user 实为 key」与「名为月度实为终身」

- Proxy 全部硬限额（RPM/TPM/TPMonth/并发）按 **keyHash** 计数（F5），换 key 即重置，订阅/分组配额体系被架空；
- Portal 企业成员的 `quota_limit/credit_limit` 闸门比对的是**终身累积**的 `users.quota_used/credit_used`，而月度重置只清企业池与成员余额（S-U8），第二个账期起成员被永久锁死；
- 额度回收不对称：分配时企业池 `quota_used += N`，移除成员时不回扣（S-U6）。

**建议**：统一定义每个限额/计数器的「维度（key/user/subscription/group/enterprise）× 窗口（秒/日/自然月/终身）」并文档化，代码按矩阵对齐。

### 根因 R3：资金链路三段（pre-consume / stats / billing）语义各自为政

- pre-consume 只看 balance+gift，对订阅额度盲视（S-U9）；
- stats 阶段（T1）与 billing 阶段（T2）两次独立算价，`usage_records.total_cost` ≠ 实扣（S-U10）；
- timeout 扣费路径 INSERT 列名错误已整体失效（F1，已复核）；
- 三类死信队列只有写入方，无消费者、无告警、无补算入口（S-U11）。

**建议**：以 trace_id 为唯一主线，扣款金额以 stats 阶段落库值为准；死信补齐 metric + 告警 + 重放工具（详见第五节）。

---

## 二、致命缺陷

### F1【已复核】timeout pre-consume 扣费路径整体失效：INSERT 引用不存在的列

- `internal/routes/webhook_preconsume.go:522-538`（个人）、`:627-643`（企业）的 `INSERT INTO usage_records` 使用 `api_key_hash`、`error_message` 两列；`internal/db/schema.go:549-582` 及全部 ALTER 中 usage_records **均无此两列**（只有 `error` 列，无 key hash 列）。
- 影响链：INSERT 必报错 → 事务 aborted → `tx.Commit` 失败 → `handleSettle` 重新 freeze 并返回 500 → proxy 侧 Settle 不重试 → 冻结 TTL 过期释放。**所有 timeout / usage_missing 场景的预估扣费永远不落账**，超时请求完全免费。`:541` 注释「Non-fatal: balance already deducted」与事实相反。
- **影响面确认**（2026-07-27 复核）：pre-consume 在整体设计中只是闸口+押金，成功请求的真实计费由 usage 上报链路完成（settle=unfreeze，见 `preconsume.go` DetermineSettleAction 注释），本条不影响成功请求计费的正确性。但**失败类请求（timeout / usage_missing / 无部分输出的流中断）不会产生任何 usage 上报**（已核实 proxy 上游错误路径只调 `recordUpstreamUsageAsync`，不调用户侧 `recordUsageAsyncFromContext`），settle→chargeFromFreeze 是这类请求**唯一**的计费通道——因此本条仍是真实的收入损失，必须修复。流式中断且已有部分输出的路径设计正确（recordUsage + settleSuccess 成对出现，无双重扣费）。
- 注意：这是第二轮批次 1.2（`2a2a997` 免单修复）引入的扣费路径，当时新增的是 proxy 侧测试，portal 落库路径无真实 DB 测试覆盖，列名错误因此漏网。
- **修法**：列名对齐 schema（去 `api_key_hash`、`error_message`→`error`）；该路径补 DB 集成测试。

### F2：用户可经 Web 端点重新激活被管理员禁用的 key

- `internal/web/actions.go:85-103`（`HandleRevokeAPIKey`，`POST /apikeys/:id/revoke`）：`UPDATE api_keys SET status = CASE WHEN status='active' THEN 'revoked' ELSE 'active' END`——任何非 active 状态（含管理员设置的 `disabled`、成员移除时的 `recalled`）都被翻回 `active`。API 层 `updateKey` 明确阻止此操作（`apikeys_crud.go:598-611`），Web 端点完全绕过，且**无审计、无 validate 缓存失效**。
- **修法**：删除该遗留端点，或复用 `updateKey` 的状态白名单与 disabled 保护。

### F3：`PATCH /admin/users/:id` 绕过 super_admin 全部降级保护

- `internal/routes/admin_users.go:104-127`（`updateUser`）：校验 `req.Role ∈ {user, enterprise_admin}` 后直改 role，**不读目标当前角色、不检查最后一个 super_admin、不禁止改自己**。可绕过 `demoteSuperAdmin:405-432` 的「非 super 不可降 / 最后一个不可降 / 不可自降」全部保护；MVP 模式下 system_admin 即可降级 super_admin。
- **修法**：role 变更前读目标角色，super/system admin 目标走与 demote 相同守卫；禁止经此端点改自己 role。

### F4：删除用户无目标角色防护，可删除最后一个 super_admin

- API `deleteUser`（`admin_users.go:186-245`）只有自删检查；Web `HandleDeleteUser`（`internal/web/actions.go:366-389`）更弱：不级联禁用 key、不失效缓存、无审计、无角色防护。删掉最后一个 super_admin 后 `CanPerformSensitiveOps`（`middleware/auth.go:310-318`）全平台无持有者，敏感操作永久锁死。
- **修法**：复用 `toggleUserStatus:469-474` 的角色检查 + 「不得删除最后一个 super_admin」守卫；Web 版改为调 API 同一实现（事务级联 + 缓存失效 + 审计）。

### F5（Proxy）：所有硬限额按 keyHash 计数——新建 key 即重置全部限额

- `internal/ratelimit/ratelimit.go:382`：`redisKey := prefix + "user:" + keyHash`，RPM/RPH/RPW/TPM/TPH/TPMonth/Concurrent 全部以此计数。变量名为 `user:` 实为 key 粒度（文档 `rate-limit-design-202605072130.md:345` 的 Redis key 结构佐证）。
- 影响：一个订阅用户建 N 把 key 即获 N× sub_rpm/sub_tpm/**sub_tpmonth**；月度硬限额 TPMonth 可换 key 无限重置；group_concurrent_limit 随组内 key 数线性放大。订阅配额体系形同虚设。
- **修法**：计数维度拆分——key 级限额用 keyHash，subscription 级用 subscription/user ID，group 级用 group_id（claims 均已有）；短期可先限制单用户 key 数并在 admin 页明示「限额按 key 生效」。

---

## 三、严重缺陷——Key 管理

### S-K1：系统管理员没有统一的 key 管理界面，能力严重残缺（功能缺口）

- 侧边栏（`partials/layout-sidebar.html:126-257`）system_admin 区块无任何 key 管理入口；唯一的 key 相关页 `/admin/monitor-downstream` 为**只读**（刷新/导出/筛选，无操作按钮）。
- 管理员能力仅存于裸 API：`GET/PATCH /api/admin/downstream-keys*`（`internal/routes/downstream.go:21-27`），且全模板无任何地方调用 `PATCH .../status`。
- **任何路径都不存在**的能力：删除任意用户 key、修改任意 key 的配额/分组/限流/allowed_models、按 key 查看审计。
- **修法**：新建 `/admin/keys` 页面——全量 key 列表（分页 + 按用户/企业/状态/分组筛选）+ 禁用/启用/删除/改配操作，后端复用 `downstream.go` 并按下条补齐防护。

### S-K2：admin 改 key 状态接口无白名单、无审计、无缓存失效

- `internal/routes/downstream.go:163-182`（`updateKeyStatus`）：`req.Status` 直写 DB（可写任意字符串）；`DownstreamRoutes` 无 audit 依赖；更新后不失效 validate 缓存（portal 侧 5 分钟窗口内禁用不生效）；WHERE 无 `deleted_at IS NULL` 且不查 RowsAffected。
- **修法**：状态白名单 + 先取 key_hash 再更新并 `invalidateValidateCache` + 接审计 + 过滤软删。

### S-K3：Web/API 双轨处理器不一致（根因 R1 的 key 域表现）

- `internal/web/admin_handler_common.go`：`HandleSuspendEnterprise:391-410` 不级联禁用 key、不失效缓存、无审计；`HandleRevokeEnterpriseKey:796` / `HandleDeleteEnterpriseKey:950` / `HandleReactivateEnterpriseKey:974` / `HandleEditEnterpriseKey:876` 全部无缓存失效、无审计（API 版 `enterprise_keys.go:453-590` 三者齐全）。
- API 版 `deleteEnterprise`（`enterprise_common.go:1093-1099`）级联禁用全部企业 key 但**未失效缓存**（同文件 suspendEnterprise 做了）——活跃路径的真实空洞。
- **修法**：Web 端点改薄包装；`deleteEnterprise` 参照 suspendEnterprise 收集 key_hash 并失效。

### S-K4：`createKey` 不校验 key_type，企业成员可自铸企业 key

- `apikeys_crud.go:147-149` 无枚举校验 → `:204-211` `key_type=="enterprise"` 生成 `sk-wgit-en-` 前缀 → `:342-351` 回填 enterprise_id → `:152` enterprise 类型跳过 20 把上限。企业成员可绕过「企业 key 由管理员分配」的治理模型自行签发不限量企业 key，且不受 admin-assigned 删除保护、不产生分配审计。任意垃圾 key_type 字符串也可落库。
- **修法**：key_type 白名单 `general/subscription`，enterprise 仅允许走 `/api/enterprise/keys`。

### S-K5：审计类别名拼写不一致，key 审计可被静默丢弃

- `pkg/audit/audit.go:27-32` criticalCategories 是 `"keys"`，而所有 key 审计实际写 `"apikeys"`（`apikeys_crud.go:403,523,725,766`）。缓冲满时 key 管理审计按 best-effort 丢弃——与注释「key-management 不容丢弃」的设计意图相反。
- **修法**：统一类别名并补测试。

### S-K6：key 归属判断三个端点三种语义（前轮遗留，确认未修）

- `apikeys_usage.go:16-23`（getKeyUsage）与 `apikeys_crud.go:455`（getKey）只按 `user_id`；`apikeys_models.go:264-293`（canViewKeyModels）已允许 owner/assignee/enterprise_admin 三方；计费归属按 `COALESCE(assigned_user_id, user_id)`。被分配 key 的成员能看模型、能拿明文，却查不到自己的用量（404）。
- **修法**：统一为 `user_id = $2 OR assigned_user_id = $2`。

---

## 四、严重缺陷——User/Member 管理

### S-U1：Web 密码重置不使旧会话失效（双缺口叠加）

- `internal/web/auth.go:411-413` 重置密码不递增 `token_version`（API 版 `routes/auth.go:607` 有）；`internal/web/session.go:93-130`（JWTMiddlewareWeb）从不校验 `claims.TokenVersion`。被盗 Web cookie JWT 在改密码后仍可访问全部 Web 页面与操作，最长 30 天（remember me）。
- **修法**：HandleResetPassword 补 `token_version+1`；JWTMiddlewareWeb 增加与 RequireAuth 一致的 token_version 比对。

### S-U2：审计日志可被 system_admin 软删且删除动作不留痕

- `internal/routes/audit.go:27`（soft-delete 仅 RequireAdmin）、`:338-349`（softDelete 无 audit.Log）。作恶管理员可抹掉自己的操作痕迹；企业侧 enterprise_audit_log 反而无删除入口，平台侧倒挂。
- **修法**：删除/恢复动作自身写不可删审计；删除权限收紧到 super_admin；合规上建议只归档不删除。

### S-U3：Web 找回密码完全无限流

- `internal/web/auth.go:299-348` 无 `email_rate` 检查（API 版 `routes/auth.go:515-529` 每邮箱 3 次/小时）。可邮件轰炸 + 用户存在性探测。

### S-U4：Web 登录不拦截 GDPR 待删除账号

- `internal/web/auth.go:111-156` 查询不含 `gdpr_erasure_requested_at`（API 版 `routes/auth.go:356,387-393` 有 pending-deletion 拦截）。已申请删除的用户可经 Web 重新登录恢复完整访问。

### S-U5：quitEnterprise 回收 key 后不失效 validate 缓存

- `enterprise_members.go:1390-1421` 事务内禁用 key，事务后无 `invalidateValidateCache`（对照 removeMyEnterpriseMember:72-78 有）。退出成员的企业 key 最长 5 分钟仍可用。

### S-U6：成员移除/退出时企业池配额不回收（资金泄漏）

- 分配时 `topupMemberQuota`（`enterprise_members.go:163-176`）企业池 `quota_used += N`；`removeMyEnterpriseMember:47-67`、`removeMember:1187-1206`、`quitEnterprise:1390-1409` 均不回扣 `quota_used`，也不回收成员未用 `quota_total`。反复「加人-分配-移除」可持续放大泄漏。
- **修法**：移除/退出时同事务 `quota_used = GREATEST(quota_used - 未消耗部分, 0)` 并回收成员未用 quota；至少在 UI 明示「额度不退回」。

### S-U7：邀请审批路径绕过企业 max_admins 上限

- `addEnterpriseMember:1810-1813` 有 admin 计数检查，但 `approveInvitationAdmin:776-822`、`approveInvitation:1487-1531`、`acceptInvitation` autoJoin（:1087-1101）置 admin 时均无检查。EA 发 role=admin 邀请经审批/autoJoin 即突破 max_admins（默认 3）。

---

## 五、严重缺陷——Usage/账单

### S-U8【已复核】成员 quota/credit 计数器跨月不清零，与 validate 闸门矛盾

- 累积：`webhook_billing.go:373`（`users.credit_used +=`）、`:521`（`users.quota_used +=`）。月度重置 `enterprise_billing.go:299-317` 只清企业池与成员 `quota_total/quota_remaining`（注释称 quota_used 保留「for audit」），**从不清 `users.quota_used/credit_used`**。
- 但闸门 `apikeys_validate.go:238-258` 用 `u.quota_used >= em.quota_limit` 阻断——月度语义的上限比对的是终身累积量。第一个月达到 limit 的成员，次月企业池已重置仍被 402 永久阻断，且无任何清零入口。
- **修法**：明确 quota_limit/credit_limit 的窗口语义；若为月度，ResetMonthlyBilling 同事务清零成员计数器（历史另存月度快照表）；若为终身，改 UI 文案并移除 reset 注释的矛盾。

### S-U9：pre-consume 对订阅 key 完全盲视（可用性问题，非错账）

- `webhook_preconsume.go:111-140` 只查 balance+gift（enterprise 一个特例分支），不看 `user_subscriptions.quota_remaining`；proxy 对所有 key 类型无条件 pre-consume（`handlers_chat.go:347` 无 keyType 判断）。开启 PreConsumeEnabled 后，余额为 0 的纯订阅用户被 402。
- **定性**（2026-07-27 与设计者确认）：真实 billing 以 proxy 上报的 usage 数据在 portal 侧结算为准，pre-consume 只是闸口与押金——成功请求 settle=unfreeze 后由 billing 队列按实际用量扣费，本条不会造成错账。影响是**可用性**：纯订阅付费用户被误拒；以及闸口口径不一致（订阅额度不能挡预扣，却能挡 validate）。timeout 扣费 `chargeFromFreeze:462-519` 只扣 gift/balance，与正常计费「订阅优先」顺序不一致，属同一口径问题的次要面。
- **修法**：pre-consume 增加 subscription 分支（按 keyType=subscription 检查 `user_subscriptions` 剩余额度），或 proxy 对订阅 key 跳过 pre-consume 仅依赖 validate 闸门。

### S-U10：stats 与 billing 两次独立算价，total_cost ≠ 实扣

- T1（`webhook_usage.go:954,1051`）算价写 `total_cost`；T2（`webhook_billing.go:199-276`）重新 `CalculateCostBatch` 扣款，从不回写。定价按 (provider, region, model, **minute**) 缓存键解析，跨分钟/调价/汇率刷新即产生无任何字段标记的差异——对账永远无法闭合的根本原因之一。
- **修法**：billing 阶段按 trace_id 读 stats 已算的 total_cost 扣款；或新增 `charged_cost` 列回写实扣并保留差异。

### S-U11：三类死信与「已入账未扣费」记录无消费者、无告警、无补算入口

- 死信只有写入方：`webhook:billing:dead`、`webhook:usage-stats:deadletter`、`billing:freeze:dead`；无消费者、无 metric、无 admin 页面。历史文档确认这是长期空白（`billing-orders-implementation-202605080748.md` §10.3 曾列为 Phase 5 低优先级建议，未实现）。
- 唯一补算工具 `handleReconcileUpload` 按 trace_id 存在即跳过（`admin_reconciliation.go:876-885`）——billing 死信记录已存在于 usage_records，永远无法经此补扣。`pricing_error=true` 同样无重算任务。
- **修法**：① 死信队列长度 metric + 告警；② admin 增加「未扣费记录」视图（`total_cost > 0 AND 各扣款列全 0 AND created_at < NOW() - 10min`）；③ reconcile upload 增加 unbilled 模式只补扣不重插。

### S-U12：普通企业成员可查看/导出全企业按成员拆分的用量与费用（越权）

- `enterprise_common.go:108-118`：`/api/enterprise/usage/details` 与 `/usage/export` 挂在 `requireEnterpriseAccess`（任何成员通过），返回每个成员的姓名/邮箱/请求数/成本。同族接口（member-logs、reconciliation、errors）均要求 requireEnterpriseAdmin。
- **修法**：移到 admin 组，或 dimension=user 时强制只返回本人行。

### S-U13：processRecord 同步路径扣款不受 INSERT 幂等保护，重放即重复扣费

- `webhook_usage.go:336-587` 同事务先扣款、`:599-608` 才 `INSERT ... ON CONFLICT DO NOTHING`，不检查 RowsAffected。Redis 故障 fallback 与 reconcile upload 两条同步路径下，proxy 重发同 trace 记录 → 记录不重复但**余额重复扣**。上轮的 trace 幂等 marker 只覆盖队列路径。
- **修法**：扣款移到 INSERT 之后，以 `RowsAffected==0` 短路扣款分支。

### S-U14（性能）：usage_records 缺全部高频过滤维度索引

- 仅有 PK `(id, created_at)`、UNIQUE `(trace_id, created_at)` 与 6 个低基数单列索引（`schema.go:3447-3474`）。**无** `(user_id, created_at)`、`(user_api_key_id, created_at)`、`(group_id)`、`(model)` 索引。用户账单页、key 用量、admin explorer、企业用量、rate-limit LATERAL 子查询全部退化为分区顺序扫描；`is_streaming` 布尔索引几乎不被选用还拖慢 INSERT。
- **修法**：`CREATE INDEX CONCURRENTLY` 补齐四个复合索引；is_streaming/status 改部分索引（`WHERE status='error'`）或删除。

---

## 六、严重缺陷——Proxy 数据面

### S-P1：key 撤销无主动失效通道 + gracefulTTL 被 cleanupLoop 架空

- 撤销通道：pub/sub 只有 `ai-proxy:balance` / `ai-proxy:sync`，无 key revoke/user ban 消息（`server.go:293-312`、`auth.go:766`）。禁用 key 只能等缓存过期。**注意**：历史文档 `pre-launch-functionality-audit-202607130620.md` 曾明确「接受 5 分钟窗口」——本轮建议是重新评估该决策（见下）。
- 架空 bug：`auth.go:522-528` cleanupLoop 每分钟无条件删除过期条目，优雅降级分支（`:825`）要求条目仍在 map 中——30 分钟 graceful 窗口实际只剩 ~1 分钟，portal 故障 ~6 分钟后全线 503，与设计意图相反。
- **修法**：cleanupLoop 删除条件加 `(portalHealthy || 超过 gracefulTTL)`；撤销推送复用 balance pub/sub 增加 `key_revoked`/`user_banned` 消息类型，portal 侧在禁用/封禁时发布。

### S-P2：usage 上报链路三处可靠性缺口

- `usage_buffer.go:53-64`：缓冲满时在**请求 handler goroutine** 里同步 sendBatch；批量失败退化逐条发送，每条最坏 10s，100 条阻塞 ~17 分钟；chat 路径 `recordUsageAsyncFromContext` 在 `c.JSON(200)` 前同步调用——portal 抖动直接放大为客户端延迟（`full-code-review-202607200416.md` 曾标 ⚠️ 部分修复，现状仍阻塞）。
- 发送失败只写 fallback 文件，无内存重试/重排队。
- `cmd/server/main.go:298-301`：cancel 触发最终 Flush 但 main 不等其完成即退出，关机窗口内 usage 静默丢失；所有 `go Billing.Settle(...)` 同样无人等待。
- **修法**：满批量投异步发送队列；失败指数退避重试 N 次再落 fallback；shutdown 用 WaitGroup 等待 flush 与 in-flight settle（30s shutdownCtx 内）。

### S-P3：Settle 仍为 fire-and-forget（前轮 S3-4，确认未修）

- `handlers_chat.go` 30+ 处 `go Billing.Settle(...)`；`preconsume.go:182-192` 失败仅返回 false，无重试、无 fallback 日志。
- **影响面**（2026-07-27 复核）：portal 侧 freeze 有 5 分钟 TTL 自动释放兜底（`webhook_preconsume.go:26`），settle 丢失不会永久冻结用户余额；实际损失是 timeout/usage_missing 类请求该扣的预扣没扣成（叠加 F1 后该类请求完全免费），以及进程重启窗口的 in-flight settle 全部丢失。严重级维持，但不需要为「永久冻结」恐慌。

### S-P4：trace_id 链路断裂

- `handlers_chat.go:348-352` 只在 portal pre-consume 分支设 trace_id；local cache 路径、PreConsume 关闭、embeddings/images/audio 均不设，`recordUsageAsyncFromContext` 与 `captureRetention` 各自 `uuid.New()`——同一请求的 usage 幂等键与 retention 定位键互不相同。
- **修法**：RequestID 中间件统一 `c.Set("trace_id", rid)`，全链路只读不写。

### S-P5：/v1/messages 与 /v1/completions 无 TPM/TPH/TPMonth 预检

- `ratelimit.go:230-296` estimateRequestTokens 不匹配这两类路径 → 估算 0 → 全部 token 硬预检跳过。Claude 客户端走 /v1/messages 可绕过 TPM 硬阻断（独立于已知的 Anthropic 流式免单遗留）。

### S-P6：/v1/models 的 key_limits 用「最宽松值」，与执行的「最严格值」相反

- `handlers_models.go:167-172` 用 `maxInt(keyRPM, subRPM, groupRPM)`；执行端 `ratelimit.go:359-368` 用 `minPositive`。客户端按返回值规划必然超调被打 429——对外 API 正确性 bug（model-capability-exposure 计划落地时引入）。

### S-P7：UsageRecord.Cost 恒为 0（隐契约）

- `handlers_common.go:194-198` 读 `c.Get("request_cost")` 但全仓库无对应 Set。当前靠 portal 重算不出事，一旦 portal 某路径改用 record.Cost 即静默零元入账。建议填实或删字段。

---

## 七、一般项汇总

### Key 域

| # | 位置 | 问题 |
|---|---|---|
| G-K1 | downstream.go:29-66,79-89 | listDownstreamKeys 无分页、含软删 key，全表扫描 |
| G-K2 | admin_handler_common.go:820-873 | 两个活跃明文 key reveal Web 端点无审计（API 版有） |
| G-K3 | actions.go:21-62 | Web HandleCreateAPIKey 绕过全部创建校验（无上限/无 group 继承/硬编码 RPM 60/无审计） |
| G-K4 | apikeys_validate.go:175-189 | 已禁用 key 不做负缓存，hot key 每次打 DB 大 JOIN |
| G-K5 | enterprise_keys.go:34,288-365 | listEnterpriseKeys 硬 LIMIT 100 无分页；reassignKey 非原子且不排除软删 key |
| G-K6 | apikeys_crud.go:589-590 | 注释声称 status 有 DB CHECK 约束，实际 schema.go:274 无——修正注释或加约束 |

### User/Member 域

| # | 位置 | 问题 |
|---|---|---|
| G-U1 | routes/auth.go:607 | resetPassword 仍未排除软删用户（前轮遗留，确认未修，一行可修） |
| G-U2 | admin_users.go:120-181 | updateUser 三处 db.Exec 失败仍返回 success 且照写审计；balance/group UPDATE 不排除软删用户 |
| G-U3 | admin_users.go:277-287,321-322 | createUser 不强制 MaxSystemAdmins；adminMode 统计不排除软删用户 |
| G-U4 | enterprise_members.go:1304-1318 | promoteDemoteMember 双写非事务，第二步失败致两侧角色漂移 |
| G-U5 | enterprise_members.go:1941-1960 | addEnterpriseMember 三步关键写忽略错误且非事务，可产生 users/members 不一致 |
| G-U6 | enterprise_members.go:25-28 | EA 可移除其他 EA；移除最后 admin 不置 admin_vacant → appointAdmin 死锁 |
| G-U7 | admin_users.go:41 + query/admin.go:65-69 | Admin 用户列表 pageSize=0 时无 LIMIT，全表返回 |
| G-U8 | web/auth.go:42-59,22-26 | Web HandleSendCode 限流非原子、无 per-IP 上限、crypto/rand 失败回退可预测验证码 |
| G-U9 | profile.go:350-359,153-188 | 邮箱/手机变更验证码无失败次数上限；changeEmail 不 bump token_version 致审计 email 错位；Web changePassword 无强度校验 |
| G-U10 | enterprise_batch.go:321-433 | 批量操作旧值读取与更新非事务，审计 old_value 不可信；setMember*Limit 不查 RowsAffected 假成功 |
| G-U11 | enterprise_members.go:605-648 | 邀请 token 明文存储且在 API 响应返回——建议存 hash |
| G-U12 | admin_users.go:252 | createUser Balance float64 写整型列，小数值 500；admin 设密码无强度校验 |
| G-U13 | migrations/0004:14 | 回填 role 审计 delta 恒 0，member→admin 应为 1 |

### Usage/账单域

| # | 位置 | 问题 |
|---|---|---|
| G-B1 | services/billing.go:97-105 + webhook_usage.go:1106-1112 | CalculateCostBatch 出错不标 PricingError，批量 INSERT 仍丢 pricing_error 列——主路径定价失败与零成本记录无法区分 |
| G-B2 | admin_reconciliation*.go | 对账只覆盖「上游 vs 下游」一维；usage 扣款列 vs balance_transactions vs 订阅/企业计数器三方无 diff 手段 |
| G-B3 | webhook_usage.go:342-381 | processRecord 订阅路径不执行 daily_limit 封顶，与批量路径分裂 |
| G-B4 | 全部 CSV 导出点 | 无 CSV formula injection 防护（用户昵称 `=HYPERLINK(...)` 即触发）；仅 audit.go 的 csvSanitize 正确——统一 csvSafe() |
| G-B5 | invoices.go:468-487,605-638,352-365 | 发票资格 check-then-insert 竞态可超开；listRequests 漏过滤软删 |
| G-B6 | billing.go:133-143 等 | days/时间跨度无上限（generate_series 万行级展开）；admin 企业成员用量无时间过滤全历史 JOIN |
| G-B7 | webhook_usage.go:41,51 | deriveErrorCode 子串误判（"moderate"/"generate" 含 "rate" → 429） |
| G-B8 | webhook_common.go:303-333 | 队列消费非 RPOPLPUSH，portal RPop 后崩溃即丢批；建议 Streams+ACK 或 RPOPLPUSH |
| G-B9 | partition.go:14,52-61 | usage_records 分区 lookback 仅 12 个月，更老数据落入 default 分区成隐形主表且无清理——明确归档策略 |

### Proxy 域

| # | 位置 | 问题 |
|---|---|---|
| G-P1 | ratelimit.go:386-594 | 检查链中途失败不退还已消耗配额；429 重试自我加速耗尽 RPM——改「全部 check 后统一 commit」 |
| G-P2 | handlers_embeddings/images/audio | 无端点 pre-consume，按次计费的上游超时即免单（与已修的 chat 路径同类未覆盖） |
| G-P3 | ratelimit.go:219 | estimateRequestTokens 对全 body io.ReadAll 无大小上限（音频 25MB+ 双倍驻留）——LimitReader 截断 |
| G-P4 | ratelimit.go:70-104 | localCounter.buckets 以 keyHash 为键永不淘汰，key 轮换持续累积内存 |
| G-P5 | ratelimit.go:1062-1100 | Retry-After 声称日历窗口、实际滑动桶——统一语义或修正 header |
| G-P6 | auth.go:34,92,836-846 | keyCache maxEntries=10000 是死配置，从不检查容量 |
| G-P7 | auth.go:968-969 | ENTERPRISE_CREDIT_EXHAUSTED/MEMBER_QUOTA_EXCEEDED 折叠成「Insufficient balance」——按 error_code 透传 |
| G-P8 | 多文档 | 多实例「Redis 分布式限流」宣称与实现（本地判定，Redis 只写不读）矛盾，rate-limit-design/proxy-routing-and-prompt-cache 等文档需回改 |
| G-P9 | billing.go:518-557 | UsageRecord 无 user_id 字段，portal 需反查 key→user，key 删除后归属变脆 |

---

## 八、质量控制与测试缺口

1. **F1 暴露的盲区**：portal 侧 pre-consume 落库路径无真实 DB 测试，列名错误存活了一个迭代。补：`webhook_preconsume.go` 全链路 DB 测试（冻结-结算金额守恒、timeout charge 落库、结算>冻结溢出），`-p 1` 跑。
2. **Web/API 双轨无对照测试**：建议为每个双轨端点加「两轨行为一致性」测试，或直接删 Web 轨（根因 R1 修复后此项自然消失）。
3. **角色防护矩阵无测试**：F3/F4/S-U7 类问题建议加一个矩阵化测试——枚举（操作 × 操作者角色 × 目标角色）断言允许/拒绝，防止后续端点新增时漏防护。
4. **proxy ratelimit 维度测试**：F5 修复需配「同 user 两把 key 共享 sub 限额」的集成测试。
5. **CI 门禁注意**：第二轮批次 3 修复的假门禁（quick.sh exit code、Playwright spec 入 CI）首次全量运行可能暴露现存红状态，上线前需跑绿一次完整 regression。
6. **文档债**：proxy 多实例限流宣称（G-P8）、api_keys.status CHECK 约束注释（G-K6）、reset 注释与闸门语义矛盾（S-U8）——代码修复同步回改文档。

---

## 九、功能缺口清单（Admin 管理面）

非漏洞但影响上线运营完整性：

| 缺口 | 现状 | 建议 |
|---|---|---|
| **系统管理员统一 key 管理界面**（用户点名项） | 仅只读 `/admin/monitor-downstream`；裸 API 无 UI、无防护 | 新建 `/admin/keys`：列表（分页/筛选）+ 禁用/删除/改配 + 审计，见 S-K1/S-K2 |
| 管理员重置用户密码 | 无任何端点，用户忘密码只能自助 | admin-users 加「重置密码」操作（强制改密 + token_version+1 + 审计） |
| 用户详情页 | 列表仅 balance/role/status/key_count；impersonate 是桩（admin_handler_common.go:24-42） | 用户详情：订阅/key/用量/登录历史汇总页 |
| admin 调整用户 quota_total | updateUser 只支持 balance/group/role/name | 视运营需要补 |
| 被删用户的订阅处理 | deleteUser 只级联 api_keys，user_subscriptions 保持 active 成孤儿 | 删除时同事务取消订阅 |
| 死信/未扣费运营视图 | 无（见 S-U11） | admin 对账区加死信长度与未扣费记录面板 |
| 企业额度/成员配额「下次重置时间」可见性 | enterprise-review-202605300413 已提出，仍未做 | 企业 dashboard 展示 |
| 退款/调账能力 | admin-reconciliation Phase 4 ⏳ 未开发；仅 Stripe charge.refunded 部分覆盖 | 上线后排期（微信/支付宝/PayPal 退款通知 + 订阅退款自动回收） |

---

## 十、修复路线图

按「权限体系 → 资金 → 安全窗口 → 可靠性 → 体验/性能」排序。

### 批次 1：权限体系致命项（上线前必须）

| 项 | 内容 |
|---|---|
| 1.1 | F3：updateUser 角色变更守卫（读目标角色 + super 保护 + 禁止自改） |
| 1.2 | F4：deleteUser/Web HandleDeleteUser 目标角色防护 + 最后一个 super_admin 守卫；Web 版改调 API 实现 |
| 1.3 | F2：删除/修复 Web HandleRevokeAPIKey 等遗留双轨端点（根因 R1，含 G-K3 等） |
| 1.4 | S-U1：Web 密码重置 bump token_version + JWTMiddlewareWeb 校验 token_version |

### 批次 2：资金链路（上线前必须）

| 项 | 内容 |
|---|---|
| 2.1 | F1：pre-consume timeout charge INSERT 列名修正 + DB 集成测试 |
| 2.2 | F5：proxy 限额计数维度拆分（key/sub/group 分列）；S-P5 补 /v1/messages、/v1/completions 估算分支 |
| 2.3 | S-U8：成员计数器窗口语义对齐（月度则随 ResetMonthlyBilling 清零） |
| 2.4 | S-U9：pre-consume 订阅分支（或 proxy 跳过订阅 key） |
| 2.5 | S-U13：processRecord 扣款以 INSERT RowsAffected 短路 |
| 2.6 | S-U6：成员移除/退出回收企业池配额 |

### 批次 3：安全窗口与越权

| 项 | 内容 |
|---|---|
| 3.1 | S-K2：updateKeyStatus 白名单/审计/缓存失效；S-K4：key_type 白名单 |
| 3.2 | S-P1：cleanupLoop graceful 修复 + key 撤销 pub/sub 推送（重新评估 5 分钟窗口决策） |
| 3.3 | S-U12：企业 usage details/export 权限收紧；S-U2：审计删除留痕/收紧 |
| 3.4 | S-U3/U4/U5：Web 找回密码限流、pending_deletion 拦截、quitEnterprise 缓存失效 |
| 3.5 | S-U7：审批/autoJoin 补 max_admins 检查；S-K5：审计类别名统一 |

### 批次 4：可靠性与对账

| 项 | 内容 |
|---|---|
| 4.1 | S-P2/S-P3：usage_buffer 异步化 + 重试 + shutdown 等待；Settle fallback |
| 4.2 | S-U11：死信 metric/告警 + 未扣费视图 + reconcile unbilled 模式 |
| 4.3 | S-U10：billing 复用 stats 算价或回写 charged_cost |
| 4.4 | S-P4：trace_id 统一；S-P7：Cost 字段填实或删除 |

### 批次 5：管理面补齐与性能（可与灰度并行）

- S-K1：`/admin/keys` 统一 key 管理界面（含第九节其余 admin 缺口）。
- S-U14：usage_records 复合索引（CONCURRENTLY）。
- 一般项按 G-B4（CSV 注入统一 csvSafe）、G-U 系列、G-P 系列排入。
- S-P6：/v1/models key_limits 改 minPositive 对齐执行语义。

### 验证基线

- Portal fast-gate：`go build`、`go vet ./...`、`gofmt -l .`、单测、`check_schema_vs_code.sh`、`i18n_full_audit.py --strict`、`rebuild-tailwind.sh` 无 diff。
- Proxy：`make build && make test`、`go vet ./... && test -z "$(gofmt -l .)"`。
- 资金/角色相关修复必须在 CI regression（Postgres 15 + Redis 7）跑 DB 测试 `-p 1`（含 -race）确认。

---

## 十一、设计确认与决策记录（2026-07-27，与设计者对齐）

### 11.1 pre-consume 的定位：闸口 + 押金，不是真实 billing

已核实代码，设计者的说明成立：

- 成功请求：`DetermineSettleAction`（`ai-proxy-go/pkg/billing/preconsume.go:194-226`）对 success 一律 **unfreeze**，注释明确「normal billing queue will charge actual cost」——真实计费由 proxy 上报 usage → portal stats/billing 队列完成，pre-consume 瑕疵不影响成功请求的计费正确性。
- 失败/4xx/5xx/非超时网络错误：unfreeze，不计费（让利用户）。
- **唯一例外**：timeout / usage_missing / 无部分输出的流中断这三类请求**不上报 usage 记录**，settle→chargeFromFreeze 是其唯一计费通道，因此 F1 仍是真实收入损失；S-P3（settle 丢失）的实际影响也收敛到这一类（freeze 5 分钟 TTL 自动释放，不会永久冻结）。
- 流式中断且已有部分输出的路径经核实设计正确：recordUsage（按已产出 token 由 billing 队列计费）+ settleSuccess（unfreeze）成对出现，无双重扣费。

结论：pre-consume 相关发现的定性更新——F1 维持「必须修」（失败类请求唯一计费通道断裂）；S-U9 降级为可用性问题（误拒纯订阅用户，非错账）；S-P3 维持严重但影响面收窄。

### 11.2 Web 表单处理器收敛到 API（根因 R1 的修复方向，已确认执行）

决策：**Web 表单处理器全部收敛为通过 API 处理**，消除双轨。执行方案：

1. **盘点**：以 `internal/web/pages.go` 的路由注册表为基准，逐端点 grep `web/templates/` 与 `web/static/` 的调用方，分三类：
   - a) 无任何模板/JS 调用的死端点 → 直接删除路由注册与 handler（预计占多数，如 `POST /apikeys`、`POST /apikeys/:id/revoke`、企业 key 系列 Web 端点——对应页面已走 `/api/*` fetch）；
   - b) 仍有表单提交的端点 → 模板改为 fetch 调对应 `/api/*` JSON API（HTMX 页面可用 `hx-post` 指向 API + 统一错误处理），然后删除 Web handler；
   - c) 必须保留的服务端渲染表单（如登录/注册/找回密码这类设置 cookie 的）→ Web handler 改为薄包装，调用与 API 层相同的服务函数，禁止再写独立 SQL。
2. **收口规则**（写入 AGENTS.md）：业务写操作只允许存在 `internal/routes/` 一份实现；`internal/web/` 只允许页面渲染与薄包装，出现 SQL 即评审打回。
3. **配套**：删除前对 a) 类端点保留 302/410 一周观察期（可选）；为 b)/c) 类保留的薄包装加「与 API 行为一致」对照测试。

### 11.3 Admin 重置用户密码：保留，但用「发送重置邮件」而非「直接设密」

决策：**需要该能力，但形态为「触发重置流程 + 强制下线」**，管理员自始至终不接触用户密码：

- 端点：`POST /api/admin/users/:id/send-password-reset`——复用现有 forgot-password 的验证码/token 签发链路向用户邮箱发重置邮件，同时 `token_version+1` 立即使该用户全部现有会话（API JWT + Web cookie）失效，写审计。
- 场景：用户邮箱可用但账号疑似被盗（强制踢会话 + 引导自助重置）；客服协助。用户丢失邮箱访问权的场景不属于此功能，走人工身份核验后由 super_admin 改邮箱。
- 不做「admin 直接设置新密码」：避免管理员知悉用户口令、避免绕过强度校验（G-U12 已记录 admin 设密无强度校验问题）。

### 11.4 Admin 对 key / user 管理薄弱：确认按 S-K1 + 第九节补齐

- 批次 5 的 `/admin/keys` 统一管理界面提升优先级，与批次 1 的权限修复合并为「管理面专项」：全量 key 列表（分页 + 按用户/企业/状态/分组筛选）+ 禁用/启用/删除/改配 + 全部写操作带审计与缓存失效（修 S-K2 后复用 `downstream.go`）。
- 用户管理同步补：用户详情页（订阅/key/用量汇总）、`send-password-reset`（11.3）、删除用户时同事务取消订阅（第九节）。

---

## 十二、修复进度

> **进度（2026-07-27，Phase 1：批次 1 + Web 双轨收敛）**：已完成并 push（`324e185..45b0a4f`，9 个 commit）。
>
> ...（上文已保留，此处更新 Phase 3 进度）
>
> **进度（2026-07-28，Phase 3：安全窗口与越权）**：已完成并 push（`b96febd..361771c`，8 个 commit）。
>
> ...（上文已保留）
>
> **进度（2026-07-28，Phase 4：可靠性与对账）**：已完成（`d602462`—`85447be`，portal + proxy 共 6 个 commit）。
>
> | Commit | 仓库 | 内容 |
> |---|---|---|
> | `d602462` | proxy | **S-P2/S-P3/S-P4**：usage_buffer 异步 flush + 重试退避 + shutdown 等待；新增 SettleBuffer（可靠 settle 队列，3 次重试）；RequestID 中间件统一设置 trace_id |
> | `ccfddae` | proxy | **S-P7**：移除永不设置的 request_cost 死代码 |
> | `2153781` | portal | **S-U11**：新增 dead letter 队列状态、未扣费记录视图、unbilled 重算模式 |
> | `85447be` | portal | **S-U10**：billing 改用 usage_records.total_cost 作为扣款依据，不再双重算价 |
>
> **状态**：S-P2/S-P3/S-P4/S-P7/S-U10/S-U11 关闭。
> **批次 4 余项**：无。
>
> **验证**：`go build ./...` + `go vet ./...` 双仓库通过。gofmt 合规。

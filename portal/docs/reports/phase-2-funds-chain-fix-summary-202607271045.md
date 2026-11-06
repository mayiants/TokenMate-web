**Status: ACTIVE**
**日期**: 2026-07-27
**范围**: ai-portal-go（HEAD `d6f08f1..`）+ ai-proxy-go（`1c3c29f..`）
**背景**: 第三轮 review `docs/reports/pre-launch-review-key-user-member-usage-202607270342.md` §10 批次二（资金链路）的落地实现。批次一（权限致命项 + Web 双轨收敛）已于 2026-07-27 完成（见 §12 进度）。

---

## 完成清单

### 2.2 F5（Proxy）— 限额计数维度拆分

**根因**: `ratelimit.go` 把所有硬限额 (RPM/RPH/RP5H/RPW/TPM/TPH/TPMonth/Concurrent) 全部按 `prefix+"user:"+keyHash` 计数。变量名虽为 `user:`，实际是 key 粒度 —— 建 N 把 key 即获 N× 的 sub RPM/TPMonth/group RPM/Concurrent。订阅配额体系形同虚设（详见报告 §二·F5）。

**修法**（`ai-proxy-go/internal/ratelimit/ratelimit.go`）：
- 拆分三个 redis key 维度：
  - `prefix+"key:"+keyHash` —— key 级（默认 RPM/TPM 限的回退）；
  - `prefix+"sub:"+userID` —— 订阅计划级（sub_* 全部）；
  - `prefix+"group:"+groupID` —— 组级（group_* 全部）。
- 每种限额独立计数和独立 hard-precheck：任何维度拒绝即整请求 429，日志带 `dimension` 字段标注被哪一档截断；被前序 reject 的 key/sub 仍会计入自己的桶（反映"该 key 尝试发起过一次请求"的真实计数）。
- Concurrent 同时可能涉及两档（subscription + group）。两档都先 acquire，任一档拒绝则释放已得，确保不丢槽位（`defer releaseAllConcurrent()` 处理 panic 路径）。
- Post-recon 按相同维度回收（hard 用 `extra`、soft 用 `tokens` 全量，原行为不变）。
- 删除 `c.Set("effective_*)` 9 项（不再被消费），保留 `GetEffectiveLimits`/`RateLimitInfo` 静态辅助供测试用。

**回归测试**（`internal/ratelimit/ratelimit_test.go`）：
- `TestMiddleware_F5_DimensionSplit` —— 用户拥有两个 key 共享 sub RPM，第二个不同 key 应被 429；
- `TestMiddleware_F5_GroupSharedAcrossUsers` —— 同 group 的两个用户共享 group RPM；
- `TestMiddleware_SP5_MessagesAndCompletionsEstimate` —— /v1/messages + /v1/completions 产生 ≥1 token 估算；
- 更新 `TestMiddleware_ConcurrentSlotReleasedOnPanic` —— 断言 `rl:test:group:panic-group` 而非旧 `rl:test:user:panic-key`。

### 2.2 S-P5（Proxy）— /v1/messages 与 /v1/completions token 估算

**根因**: `estimateRequestTokens` 仅识别 `/chat/completions` 与 `/responses`，Claude `/v1/messages` 与遗留 `/v1/completions` 返回 0 → TPM/TPH/TPMonth 全部被跳过，Claude 客户端能绕过 token 硬阻断。

**修法**: 在 `estimateRequestTokens` 增加两路 `case`：
- `/messages` —— 反序列化为 `convert.AnthropicRequest`，调 `AnthropicToOpenAI` 转 OpenAI hub 格式走 `EstimateMessages`，叠加 `MaxTokens`；
- `/completions` —— 解析 legacy `prompt`（string / []string）+ `max_tokens`，用 `CountTextToken` 估算。

### 2.3 S-U8（Portal）— 成员计数器跨月清零

**根因**: `ResetMonthlyBilling` 只清企业池（`enterprises.quota_used`/`credit_used`）和成员 `quota_total`/`quota_remaining`，但 `users.quota_used`/`credit_used` 不清。`/api/apikeys/validate:238-258` 用 `users.quota_used >= em.quota_limit` 比终身累积——成员一旦触限，次月仍被永久 402。

**修法**（`ai-portal-go/internal/routes/enterprise_billing.go`）：
- `ResetMonthlyBilling` 重写：
  1. 先按企业批量查 `users.quota_used`/`credit_used`，写入 `enterprise_audit_log`（`category='billing', change_type='monthly_member_reset_snapshot'`, 保留 `old_value`/`reason`）作为月度快照；
  2. 抽出 `resetOneEnterpriseBilling(ctx, tx, id, nextReset)` 单事务完成：清企业池 + 成员 `quota_total=0/remaining=0/quota_used=0/credit_used=0` + 写 `monthly_reset` 审计行。
- 所有成员计数器跨月清零；旧审计以快照形式保留，闸门口径与 Reset 路径一致。

### 2.4 S-U9（Portal + Proxy）— pre-consume 订阅 key 分支

**根因**: `handlePreConsume` 只看 `balance+gift`。订阅 key 在开启 PreConsumeEnabled 时若个人余额为 0 即被 402，但订阅用户实际预算由 `user_subscriptions.quota_remaining` 托管，`validate` 已放行。这造成"通过 validate、卡在 pre-consume"的可用性故障（详见 §11.1 决策）。

**修法**（双侧）：
- **Portal**（`internal/routes/webhook_preconsume.go`）：key 类型查询后增加 `keyType == "subscription"` 分支 → `handleSubscriptionPreConsume`：若用户持有 active 订阅且 `quota_remaining > 0` 返回 `amount=0`（**不冻结 USD**），否则 402 rejected。订阅 key 真实计费仍走 usage/billing 队列（按 tokens 扣 quota）；timeout/usage_missing 类的 settle 因 `preConsumed == 0` 自动 no-op。
- **Proxy**（`internal/server/handlers_chat.go` + `handlers_responses.go`）：5 处 `if PreConsumeEnabled && Billing != nil && inputTokens > 0` 增加 `c.GetString("key_type") != "subscription"` 守卫（与 `preConsumeLocal` 已有的"跳过 subscription"逻辑对齐，避免回退 portal 后再次冻结）。

### 2.5 S-U13（Portal）— processRecord INSERT 幂等短路

**根因**: `processRecord` 在事务中先做全部扣款（`UPDATE users`/`user_subscriptions`/`balance_transactions`），最后才 `INSERT INTO usage_records ... ON CONFLICT (trace_id, created_at) DO NOTHING`，不检查 `RowsAffected`。fallback 直扣路径 + reconcile upload 路径下，proxy 重发同一 trace 记录 → 记录去重成功，但**余额被重复扣**。

**修法**（`internal/routes/webhook_usage.go::processRecord`）：
- INSERT 上提到扣款之前，所有 deduction 列先以 0 占位写入。`ON CONFLICT (trace_id, created_at) DO NOTHING` 成为幂等闸门。
- `cmdTag.RowsAffected() == 0` → `tx.Commit()` 直接 `return nil`（记录已存在＝僵死重复触发，绝不能再扣）。
- 扣款路径完成后再 `UPDATE usage_records` 回填 `gift_deduction`/`credit_deduction`/`subscription_deduction` 三个实际值（audit）。
- 其余逻辑（key lookup、cost 计算、企业 credit/110% 溢出、subscription daily_reset、`incrementSubscriptionCounters`、cache 失效）保持不变。

### 2.6 S-U6（Portal）— 成员移除/退出加回企业池 quota_used

**根因**: `topupMemberQuota` 分配时 `enterprises.quota_used += N` 且 `users.quota_total += N`/`quota_remaining += N`；`removeMyEnterpriseMember`/`removeMember`/`quitEnterprise` 均不回扣 `quota_used`，也不回收未消费的 `quota_total`。反复"加人-分配-移除" 可持续放大池泄漏直至全池自封。

**修法**（`internal/routes/enterprise_members.go`）：
- 抽出 `reclaimMemberEnterpriseQuota(ctx, tx, userID, enterpriseID)`，在三个移除/退出的事务内同事务调用：
  - 读 `users.quota_total`/`quota_used`，`unused := max(quota_total - quota_used, 0)`；
  - `enterprises.quota_used = GREATEST(quota_used - unused, 0)`；
  - `users.quota_total = 0, quota_remaining = 0, quota_used = 0, credit_used = 0`（已与 ResetMonthlyBilling 清零口径一致）。
- 已消费部分继续保留在池中（pre-allocated 时已扣池对应），避免对历史审计记录造成撕扯。

---

## 验证

Portal EC2（本机）有 Postgres+Redis，可跑全套 DB 测试：

| 检查 | 结果 |
|---|---|
| `go build ./...` (portal/proxy) | ✅ |
| `go vet ./...` (portal/proxy) | ✅ |
| `gofmt -l .` (portal/proxy) | ✅ clean |
| Portal fast-gate: `go test ./internal/middleware/... ./internal/web/... ./pkg/... ./tests/performance/...` | ✅ |
| Portal DB-depend: `REDIS_ADDR=127.0.0.1:6380 go test -p 1 ./internal/db/... ./internal/routes/... ./internal/services/...` | ✅（含 `internal/db` 重建库的破坏性测试） |
| `scripts/check_schema_vs_code.sh` | ✅（schema columns 987 / code refs 483，无 drift） |
| Proxy `go test ./... -count=1` | ✅ 33 个包全绿，含 `TestMiddleware_F5_DimensionSplit`/`F5_GroupSharedAcrossUsers`/`SP5_*`/`ConcurrentSlotReleasedOnPanic`(更新后) |
| 未跑：`-race`（本机无 gcc/cgo）、Tailwind 重建（无 frontend 改动）、`i18n_full_audit.py`（无 key 改动）、Playwright（无 UI 改动） | 由 GitHub CI 完成 |

---

## 遗留（范围之外，下一批）

- 角色防护矩阵化测试（待第三批与批次一尾声合并实现）。
- F5 维度下"默认 RPM/TPM 仍按 keyHash 计数"，对无 sub 限额的纯 key 用户保留 per-key 重置出口——按报告"短期可先在 admin 页明示限额按 key 生效"待管理面补齐 (S-K1/9)。
- proxy timeout/usage_missing 类请求对订阅 key 的 token 计费缺口（pre-consume 不冻结后，订阅 timeout 实际不上报 usage → 永远免费；与本批同步解决需新增"订阅 timeout feed usage record"代码，未在 S-U9 范围内）。
- S-U8 历史快照写入 `enterprise_audit_log` 大企业量大时事务倍数膨胀——若审计日志写入批量化瓶颈再优化。
- 余额项验收（`balance_transactions`、`balance_after`）在 processRecord INSERT 提前路径下的 correctness 由现有 webhook_billing_test 覆盖，超出现有覆盖范围的新测试用例见上文测试列表。

---

## 部署提示

- **Portal**：需要 docker rebuild（含 `internal/routes/webhook_usage.go`、`webhook_preconsume.go`、`enterprise_billing.go`、`enterprise_members.go` 改动）。
- **Proxy**：`internal/ratelimit/ratelimit.go` 变更 + `handlers_chat.go`/`handlers_responses.go` 守卫——sync 后 remote docker rebuild。
- auth 缓存 TTL 5 分钟，重建后需重启 proxy 容器或等待过期（不影响批次二，但若同时有人批次一改动同部署则建议）。
- Redis 数据结构无 schema 变更，部署无需 flush。
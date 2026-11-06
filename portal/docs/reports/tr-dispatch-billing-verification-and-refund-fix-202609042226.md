# TokenRouter Dispatch 计费验证与退款链路修复报告

**Status**: ACTIVE
**Date**: 2026-09-04
**Related**: [tokenrouter-ui-pricing-analysis-202608120210.md](tokenrouter-ui-pricing-analysis-202608120210.md), `downstream-pricing-summary-by-vendor-202608290308.md`

## 背景

平台计费原则为"不成功不扣费"，但怀疑上游 TokenRouter（TR）对媒体任务是
**创建（dispatch）即计费、无论成败**。本报告记录验证（D）结论与据此实施的
 A（失败遥测）/ B（dispatch 计费 + 失败退款）/ C（按 provider 配置）修复，
以及 TR 媒体定价倒挂（P0）的修复。

## D 验证结论（实锤）

- TR 账号 thinbee09@qq.com（个人 profile，user_id 5130）2026-07-15 ~ 09-04
  共 4535 条用量记录。
- **媒体 47 笔（video 24 + image 23）全部 quota > 0**，视频记录均带 task_id，
  即在任务创建时记账；`other` 字段无 status 区分成败 —— **dispatch 即计费成立**。
- TR 平台 API（浏览器 in-page fetch 可行，curl 重放会因 token 轮换被拒）：
  `GET https://tokenrouter-backend-api.tokenrouter.com/backend-api/api/log/self/?p=N&page_size=100&type=0&start_timestamp=...&end_timestamp=...`
  认证头：`Authorization: Bearer <localStorage eyJ token>` + `tokenrouter-user: 5130`。
  全量数据：`/tmp/tokenrouter-scrape/tr_usage_all.json`。
- 对账：本地 portal usage_records 39 笔 $10.215 vs TR 47 笔 $10.97，
  4 笔未对齐 ≈ $0.106（seedream-lite 2 笔、seedream-pro 1 笔、gemini-2.5-flash-image 1 笔）。
  当前泄漏金额可忽略，风险在放量。
- TR 各媒体模型真实单价（`other.model_price`）：

| 模型 | TR 成本 | 修复前我们收费 | 问题 |
|---|---|---|---|
| MiniMax-H3 | $0.45/任务 | per-second 0.09~0.13（短视频低至 ~$0.31） | 倒挂 |
| kling-v3 | $0.56/任务 | per-second 0.084~0.42（std 5s $0.42） | 倒挂 |
| kling-v2-6 | $0.35/任务 | per-block | 倒挂 |
| MiniMax-Hailuo-2.3 | $0.28/任务 | per-block 0.28~0.56 | 部分倒挂 |
| seedream-5.0-lite | $0.035/张 | $0.035/张 | 零毛利 |
| seedream-5.0-pro | $0.045 / $0.09 | 同左 | 零毛利 |

## 修复内容

### C：proxy 按 provider 配置异步计费模式（ai-proxy-go）

- `internal/config/config.go`：`BillingConfig` 新增
  `async_charge_on_dispatch_providers`（`billing.async_charge_on_dispatch_providers`）。
- `internal/server/media_async.go`：`mediaTaskBilling` 增加 `ChargeOnDispatch`；
  `billAsyncMediaTask(c, taskID, terminalStatus, latencyMs)` 双语义 —
  charge-on-dispatch 任务 success 直接返回、failed 调 `refundAsyncMediaTask`
  （负 cost、Status="refund"）；其余 provider 保持 deferred bill-on-terminal-success。
- `handlers_images.go` / `handlers_video.go` / `handlers_audio.go`：dispatch 与
  poll 两处接线。
- **关键设计**：退款记录使用**全新 trace_id**（不复用 dispatch trace_id）。
  portal 计费队列按 trace_id 去重（`markBillingRecords`，24h TTL），复用会被
  当作已计费重复记录丢弃，退款永远无法入账。

### B：portal 负额入账（退款）支持（ai-portal-go）

- `internal/routes/webhook_usage.go` `processRecord`（直接路径）：
  `status=refund` 记录先按原计费元数据重算价格再取负（portal 从不信任
  proxy 的 cost 估算，取负保证与 dispatch 扣费完全对齐）；负 cost 走新增
  `applyBalanceRefund`（`users.balance` 贷记 + `balance_transactions`
  type='refund' 审计行）。企业 key 暂不自动退款（记 Warn，负 usage 行仍落库
  供对账）。幂等由 `INSERT ... ON CONFLICT (trace_id, created_at) DO NOTHING`
  保证，重放不会重复退款。
- `internal/routes/webhook_billing.go` `processBillingBatch`（热路径）：
  退款记录从计费分区剥离（不进 enterprise/subscription/general 扣费），
  单独结算 —— 个人 key 贷记 balance，企业 key 释放
  `enterprises.credit_used`（`GREATEST(...,0)` 不为负）；
  `usage_records.balance_deduction` 记负值镜像贷记。
- 新增 `internal/routes/webhook_refund_test.go` 三个 DB 测试：
  批路径 net-zero、直接路径贷记、重放幂等，全部通过。

### A：失败请求遥测持久化（ai-portal-go）

- proxy 审计批（`POST /webhook/usage/batch`）此前直接丢弃，现落库新表
  `proxy_request_audits`（`internal/db/schema.go` + `migrations/0011_proxy_request_audits.sql`，
  单事务多行 INSERT，超 5000 条丢弃并 ACK，DB 故障不影响 200 响应）。

### P0：TR 媒体定价（DB 直接修复，2026-09-04 生效，09-05 按用户要求修订）

TR 媒体为 **flat per-task**（dispatch 即扣，与时长无关），原 per-second 规则在
短视频上倒挂（如 MiniMax-H3 768P 5s 仅收 $0.45×0.09×5≈成本线以下）。修订方案
（保留原 media 变价结构，仅抬 `fixed_request` 保底）：

| 模型 | 结构 | fixed_request（保底=TR flat 成本） |
|---|---|---|
| MiniMax-H3 | per-second 0.09~0.13（恢复原值） | **0.45** |
| kling-v3 | per-second 0.084~0.42（恢复 8 档） | **0.56** |
| kling-v2-6 | per-block 0.21~1.68（恢复 12 档） | **0.35** |
| minimax-hailuo-2.3 | per-block 0.28~0.56（恢复 3 档） | **0.28** |
| seedream-5.0-lite | per-image 0.035（恢复） | 0.01 |
| seedream-5.0-pro | per-image 0.003/0.045/0.09（恢复） | 0.01 |
| seedream-4.5 | per-image 0.04（未动 variant） | 0.01 |

保底效果（最差情形 = 1 秒/最低档）：每单 ≥ TR 成本，per-second/per-block 部分
为按时长的毛利。例如 H3 768P 5s = 0.09×5+0.45 = $0.90（成本 $0.45，2.0x）；
kling-v3 std 5s = 0.084×5+0.56 = $0.98（成本 $0.56）。

`kling-v3-omni`、`kling-3.0-turbo`、`happyhorse-1.0-i2v/t2v` 无 TR 用量数据
（TR 成本未知），fixed_request 暂保持 0.005 未动 —— 需要先在 TR 平台确认
model_price 后再补保底。图片模型上游按张计费（无 flat 成本），fixed 0.01
提供约 1.3x 综合毛利。注意：portal pricing 缓存 TTL 1 分钟，规则改动最迟
1 分钟后自动生效。

## 部署清单

- [x] proxy 两端 `config.yaml` 增加 `billing.async_charge_on_dispatch_providers: [tokenrouter]`
      （本机 ai-proxy-go + Proxy EC2 172.31.43.214）
- [x] portal 生产库定价规则 UPDATE
- [ ] 重建并重启 proxy 容器（两端 config 生效）
- [ ] 重建 portal 容器（webhook 退款 + 审计表生效；新表由 InitSchema/migration 自动创建）
- [ ] 验证：发一笔 TR 视频任务 → 确认 dispatch 即扣费；人为失败后确认退款入账

## 遗留风险

- 退款贷记一律进 `balance`（不区分当初从 gift 还是 balance 扣）——原扣费 gift
  优先，退款进 balance，对用户总可用金额无影响，仅 gift/balance 分项略有偏移。
- TR 文本模型仍是 per-token 计量，与 TR 上游按 token 计费一致，无倒挂问题。
- portal 秒级时间戳 + `effective_from` 窗口的边界：backdated 记录（时间戳早于
  规则生效时间）会 miss 规则落到通配规则按 0 计费 —— 存量行为，未改动。

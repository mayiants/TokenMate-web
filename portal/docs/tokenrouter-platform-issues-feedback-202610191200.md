# TokenRouter 平台问题反馈与建议

> 状态：ACTIVE（2026-10-19 v2——**重大修正**：v1 曾判断"钱包按重复日志行 3 倍扣费、多扣 $140"，经受控 burst 实验与钱包快照对账后**修正**为"日志视图瞬时 3 倍写入（后自动去重），钱包 1:1 按日志金额扣款，当日实扣与我们网关计费差额约 +$15 待核查"；v1 结论仅保留日志幂等缺陷部分）
> 整理时间：2026-10-19（基于 2026-10-19 全天 Management API 实测数据 + 受控 burst 实验，账号 thinbee09 企业组，7 把上行 Key 交叉验证）
> 反馈人：mayiants（WOODGIT AI 平台，API 网关客户）
> 联系时可引用本文中的 request_id、Key 名与复现方式。数据获取方式：`GET /api/management/hour/usages`（JSON）与 `/api/management/export/hour/usages`（CSV）。

---

## 〇、先回答"扣款从什么时间开始统计"（口径说明）

`GET /api/management/self/wallet` 的 `toppedUpSpent` / `voucherSpent` 是**开卡至今累计消费**，不是当日。2026-10-19 快照：`toppedUpSpent $229.32 + voucherSpent $25.37 = $254.69`（topUpBalance 余 $0.68，voucherEfficientAmount 余 $37.50）。

按我方充值记录可拆解为：今日充 $150（赠 25% voucher $37.5，与 voucherEfficientAmount 吻合）+ 之前余额 ~$30 → 今日 topup 实扣 **$179.32**（$(30+150)-0.68$）；toppedUpSpent 中另 ~$50 为今日之前（8–10 月测试期）消费；voucherSpent $25.37 为**上一期**赠送 voucher 的消费（与本期 $37.5 无关）。充值赠送的扣款顺序：**先扣 topup，耗尽后扣 voucher**（实测：topup 耗尽后，5 次测试调用从 voucher 扣 $0.000098）。

## 一、P0：burst 期间用量日志瞬时 3 倍写入（视图/导出均中招，后自动去重）

**现象**：2026-10-19 10:00–11:00 UTC 流量突增期间（模型 `anthropic/claude-opus-5`，Key `wg-up-22` / `wg-up-14`），Management API 返回的用量日志中，**同一 request_id 被原样写入 3 次**（所有字段完全相同：request_id、prompt_tokens、completion_tokens、cost、created_at）。

实测数据（2026-10-19，UTC；11:30 抓取 vs 12:05 复抓同一窗口）：

| 小时 | 11:30 抓取行数 | 11:30 唯一 request_id | 出现分布 | 11:30 金额 | 12:05 复抓行数 | 12:05 金额 |
|---|---|---|---|---|---|---|
| 09 | 15 | 15 | 全 1 次 | $0.80 | 15 | $0.80 |
| **10** | **300** | **100** | **100 个 request_id 恰好各 3 次** | **$218.92** | **100（重复行消失）** | **$72.97** |
| 11 | 41 | 41 | 全 1 次 | $34.74 | 41 | $34.74 |

即日志管道在高并发窗口存在**重复写入 + 随后自动清理**的行为（疑似消费端 at-least-once 重复投递，清理端按 request_id 收敛）。**该窗口内客户从 dashboard 看到的消费金额是虚高的**（$218.92 vs 实际 $72.97，3 倍）。

**钱包侧受控实验**（排除"钱包按 3 倍扣款"）：
1. 低率 5 次调用：日志 $0.000098，钱包 voucher 精确扣 $0.000098（1:1）；
2. 20 并发 burst（12:00:18，15 笔成功）：日志无重复（15 行/15 唯一/$0.000304），钱包精确扣 $0.000304（1:1），**未复现 3 倍写入**（触发阈值可能高于 20 并发或依赖其他条件）。

**结论**：钱包扣款 = 日志行金额 1:1，无独立乘数；但当日 topup 实扣 $179.32 比我们网关对同一流量的计费 $163.7 **多 $15.6（+9.5%）**——该差额无法用现行日志解释（当日去重后日志仅 $114，比实扣低），需要贵方提供**当日 billing 流水（按分钟/按 request_id）**核对来源（疑似 retry/reconnect 重计或最低消费规则）。

**复现方式**：用单把 Key 对 `anthropic/claude-opus-5` 以 ≥30 RPM 持续压测 10 分钟，分别在 10 分钟内与 1 小时后 `GET /api/management/hour/usages?start_timestamp=...&end_timestamp=...` 对比同一 request_id 出现次数。

**建议**：
1. 消费/落库链路加 request_id 幂等去重（唯一索引 + UPSERT），避免 dashboard 虚高引发客户误解；
2. 提供按 request_id 查单笔扣费与 billing 流水接口，便于对账；
3. 协助核查 2026-10-19 我方账号 topup 实扣 $179.32 与日志 $114（去重后）之间 $65 的差异构成（另我方网关侧计费 $163.7 可作为第三方参照）。

---

## 二、P1：burst 期间日志行丢失（与 P0 同窗口）

**现象**：同一小时（10:00 UTC），我们网关侧有据可查的成功请求为 **215 笔**（每笔独立 trace_id、完整 token 数），但 TR 日志中仅有 **100 个唯一 request_id**。即约 115 笔请求在 TR 侧既未出现在日志里，其扣费也无法核对（可能被并入重复行，也可能丢失）。

对比：09、11 两个非 burst 小时，双方笔数与逐笔 token 数**完全一致**（p50/p90/max 均相同），说明正常流量下对账是精确的，问题仅出现在高并发窗口。

**建议**：排查 burst 窗口日志管道的缓冲/批量写入是否有丢行；提供按 request_id 查询单笔的接口便于客户核对。

---

## 三、P2：上行 Key 配额透支后仍可继续调用

**现象**：多把上行 Key（`wg-up-04/08/12/14/18/22/30`）的 `remain_quota` 为**负数**（如 wg-up-22：used $138.22 / remain -$138.22），即未设配额上限（或上限为 0）时 Key 可无限透支。请确认这是预期行为（不限量 Key）还是 bug；若账号余额耗尽，这些 Key 会以什么策略被停用？

**附带提醒**：我们账号 topUpBalance 仅剩 **$0.68**（voucher 余 $37.50）。若 voucher 用尽且未充值，所有企业 Key 将被切断，影响双方业务连续性。

---

## 四、对我们网关的改进项（已自行修复，留档）

| 问题 | 根因 | 处理 |
|---|---|---|
| Claude SDK（`/v1/messages`）流式请求全部记录为"非流式" | 代理该入口未写入 is_stream 上下文标记 | 已修复（代理侧） |
| 流式 usage 提取丢 cache tokens / reasoning tokens | Anthropic→OpenAI 流式路径未拷贝 `prompt_tokens_details` | 已修复（代理侧） |
| 状态页越权：无 Key 用户可见全平台 model×group 聚合数据 | 空授权列表被当作"无限制" | 已修复（portal 侧） |

## 五、对 TR 的产品建议

1. **Management API 很好**（`/hour/usages` JSON + CSV export，含 cache tokens 与逐笔 request_id），正是网关对账所需；建议增加"按 request_id 查单笔"与"官方对账差异申诉"接口。
2. 文档中 `remain_quota` 为负的含义、`unlimited_quota` 与账号余额的关系建议补充说明。

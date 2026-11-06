# TR 媒体模型实测计费验证 + 异步计费链路热修报告

**Status**: ACTIVE
**Date**: 2026-09-05
**Related**: [tr-dispatch-billing-verification-and-refund-fix-202609042226.md](tr-dispatch-billing-verification-and-refund-fix-202609042226.md)

## 背景

上一报告（09-04）已确认 TR 对媒体任务"dispatch 即计费"，并实施了
charge-on-dispatch + 失败退款的 proxy/portal 链路。但 4 个媒体模型
（kling-v3-omni、kling-3.0-turbo、happyhorse-1.0-i2v/t2v）无 TR 用量数据，
fixed_request 保底未核实。本报告记录对这 4 个模型的真实调用测试、
双方计费对比，以及测试中发现的**两个线上 bug** 的修复。

## 实测结果（测试 key sk-wgit-7ebb...，2026-09-05 04:20~04:34 UTC）

### happyhorse-1.0-t2v —— 成功，TR $1.00/单

- 任务 `task_MuZyGLRZiTPpzWlLvfdaC0c5G89JlMO5`（5s, 720p）成功。
- TR 日志（type=2 消费）：`quota=500000, model_price=1, realCost=1`
  → **派发时扣 $1.00，与定价 API 的 model_price 完全一致**。
- portal 侧：轮询到终态后计费 **$0.705**（0.14/秒×5 + fixed 0.005）
  → 单笔毛利 -$0.295（倒挂）。

### happyhorse-1.0-i2v —— 任务失败，TR 照样扣 $3.60 且不退

- 任务 `task_unZkVqtukRpWs1rbVxeHIJ9GrHEdKXgb` 派发成功（TR 扣 $3.60，
  type=2, realCost=3.6），约 100 秒后任务终态 FAILURE
  （`InvalidParameter: Field required: input.media` —— TR 上游 dashscope
  要求图片在 `input.media` 字段，OpenAI 风格 `image` 字段未被映射）。
- TR 在任务失败后生成 type=6 记录（带 fail reason），但**余额无任何返还**
  （派发后余额 23539255 quota，40+ 分钟后仍不变）。
  → **实锤：TR 对失败媒体任务照扣全款，验证了我们"上游一旦调用即扣费"
  的担忧。**
- portal 侧：该失败任务**完全没有计费记录**（usage_records 无行、
  balance_transactions 无动账）→ 用户未扣费，TR 扣我们 $3.60，
  **单笔净亏 $3.60**。

### kling-v3-omni —— TR 不接受该模型的 video-generations 请求

- 多次尝试均 400：`fail_to_fetch_task, code 1201, model is not supported`。
- TR 定价页虽列出（$6.30/单，quota_type=1），但实际不可用。
  → 无上游成本风险；portal 侧该模型也永远不会成功，**建议从对外模型列表
  下线或标注 beta**，定价保底不动。

### kling-3.0-turbo —— TR 按 token 计费（quota_type=0），上游持续 5xx

- 定价 API：`quota_type=0, model_ratio=1.05, completion_ratio=1`，
  即按 token 计费的文本型计费模型；视频任务 token 上报为 0。
- 派发持续 502/5xx（上游错误），无法完成调用。
  → 保留 media 定价观察；如长期不可用建议下线。

## 测试中发现的两个线上 bug（已修复）

### Bug 1：charge-on-dispatch 运行时未生效（配置载体错误）—— 高危

**现象**：TR 已在 `config.yaml` 的
`billing.async_charge_on_dispatch_providers: [tokenrouter]` 中配置，
但线上所有视频任务仍走 deferred 计费（t2v 在轮询成功后才计费，
而非 dispatch 时），`mediaTaskBilling.ChargeOnDispatch` 实际为 false。

**根因**：生产容器**不挂载 config.yaml**（docker-compose.proxy.yml 无
volume，/app 下无 yaml），viper 只读到 defaults + 环境变量；而
`async_charge_on_dispatch_providers` 没有任何环境变量覆盖入口
→ 运行时恒为空列表。**09-04 上线的 B/C 修复因此从未真正生效。**

**修复**（ai-proxy-go，commit 随后）：
- `internal/config/config.go`：`overrideFromEnv` 新增
  `ASYNC_CHARGE_ON_DISPATCH_PROVIDERS`（逗号分隔）。
- `docker-compose.proxy.yml`：新增
  `ASYNC_CHARGE_ON_DISPATCH_PROVIDERS: "${ASYNC_CHARGE_ON_DISPATCH_PROVIDERS:-tokenrouter}"`
  （proxy 机的 compose 文件不被 rsync 同步，已单独同步修改）。
- `config.yaml` 加注释说明该文件生产不生效、以 compose env 为准。

### Bug 2：TR 的 `"FAILURE"` 终态不被识别 → 失败任务永不结算

**现象**：TR 轮询体状态为 `"FAILURE"`（如 i2v 失败任务），而
`classifyTaskStatus` 只匹配 `failed/fail/error/...`，不含 `failure`
→ 不识别为终态 → charge-on-dispatch 任务的**失败退款永远不会触发**，
deferred 任务失败后也永远无记录（静默丢失）。

**修复**：`internal/server/media_async.go` `classifyTaskStatus` 增加
`"failure"`；测试 `handlers_media_test.go` 增加 TokenRouter FAILURE case；
`internal/config/config_test.go` 增加 env 覆盖测试。

## 定价修正（portal `model_pricing_rules`，保留 per-second 变价结构）

| 模型 | fixed_request 修复前 | 修复后 | 依据 |
|---|---|---|---|
| happyhorse-1.0-t2v | 0.005 | **1.00** | TR realCost=1.00（实测，720p 也是 flat $1.00） |
| happyhorse-1.0-i2v | 0.005 | **3.60** | TR realCost=3.60（实测，含失败任务照扣） |
| kling-v3-omni | 0.005 | 不动 | TR 实际不可用（1201），无成本风险 |
| kling-3.0-turbo | 0.005 | 不动 | TR token 计费型，上游 5xx，观察 |

注意：TR 的 `video_generation_pricing` 字段（如 t2v 720p 0.14）是**展示用
分辨率价目，并非 default 组实收**——实收以 model_price flat 为准
（本次 t2v 720p 实收 $1.00 而非 $0.14×5 已证明）。昨天按 video_generation_pricing
抬 fixed 的 MiniMax-H3（0.45）/kling-v3（0.56）与 8 月实收一致，无需改动；
当前 TR 列表价（1.95/1.68）为 svip 价或已调价，default 组仍以用量日志实收为准。

## 修复后端到端验证（2026-09-05 05:07~05:12 UTC，全部通过）

| 场景 | 结果 |
|---|---|
| t2v 派发 | **dispatch 即扣 $1.70**（0.14×5s + fixed 1.00），usage_records + balance_transactions 各一行 ✓ |
| t2v 轮询到 SUCCESS | **无重复扣费**（幂等 SETNX 生效）✓，毛利 +$0.70 |
| i2v 派发 | dispatch 即扣 $4.30（0.24×5s + fixed 3.60）✓ |
| i2v 轮询到 FAILURE（同一 input.media 上游错误） | 识别终态 → 按当时设计自动退款 +$4.30 入账 ✓（**该退款行为当日已被"对齐 TR"决策取代，见下文**） |
| 容器 env | `ASYNC_CHARGE_ON_DISPATCH_PROVIDERS=tokenrouter` 已注入 ✓ |

## 失败任务政策（2026-09-05 已定：对齐 TR）

**决策**：charge-on-dispatch provider 的任务级 FAILURE **不退款**，仅
提交阶段错误不扣费。已实施：proxy `billAsyncMediaTask` 对
`tb.ChargeOnDispatch` 在任何终态直接返回（退款函数
`refundAsyncMediaTask` 已删除），相关单测改为断言"失败不产生退款记录"。

理由：TR 对失败任务照扣上游成本（实测 i2v 失败净扣 $3.60），我们若给
用户退款则每笔失败任务净亏上游成本。dispatch 计费只发生在任务创建成功
时，因此用户不会被收取上游明确拒绝的请求。

备选方案存档：（2）失败全额退款——用户体验最好但媒体任务失败率直接
打成负毛利；（3）部分退款——两头兼顾但定价解释成本高。

## happyhorse-1.0-i2v 已下线（2026-09-05）

`service_models.status='paused'`（tokenrouter 行）。该模型经 TR 100%
失败（TR 不映射 OpenAI `image` → dashscope `input.media`）；在"失败不退款"
新政策下若保持上线等于向用户收费但永远给不出结果。**重新上线前必须：
proxy 增加 image → input.media 字段映射转换 + 实测任务成功。**

## 附带发现：happyhorse-1.0-i2v 经 TR 实际不可用

两次 i2v 派发均到达 TR 上游后在 dashscope 侧失败
（`Field required: input.media`）——TR 不把 OpenAI 风格的 `image` 字段
映射到 dashscope 的 `input.media`。即 **经 TR 转发时该模型 100% 失败**
（用户被退款，portal 每次净亏 $3.60 上游成本）。建议：从用户可见模型列表
下线该模型，或后续在 proxy 增加 image → input.media 的字段映射转换。

## 测试花费

- TR 侧：t2v $1.00 + i2v $3.60 = **$4.60**（i2v 为失败任务，TR 不退）。
- portal 侧：t2v 向测试 key 收 $0.705（旧定价）；i2v 未收费。
- TR 账号余额 $47.08（原 $50.68）。

## 附：关键证据位置

- TR 日志 API（in-page fetch）：`api/log/self/?type=0`，type=2 消费 /
  type=6 任务失败记录；账号 thinbee09@qq.com / user_id 5130。
- 本次 TR 原始记录：`/tmp/tokenrouter-scrape/`（tr_models_apis.json 定价、
  tr_balance_check.js 余额与日志核对脚本）。

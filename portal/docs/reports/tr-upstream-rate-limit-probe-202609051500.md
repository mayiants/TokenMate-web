# TokenRouter 上行 Key 限流实测报告

**Status**: ACTIVE
**Date**: 2026-09-05
**Related**: [tr-media-live-billing-test-and-async-billing-hotfix-202609051300.md](tr-media-live-billing-test-and-async-billing-hotfix-202609051300.md)

## 背景

平台大部分模型走 TokenRouter（TR）两条上行 key（`sk-Dk28...2GUU`、
`sk-eYYl...iLJt`）。用户接入放量后是否会撞 TR 的 RPM 上限未知，本报告
用真实请求实测两把 key 的限流行为。

## 测试方法

- 直接请求 `https://api.tokenrouter.com/v1/chat/completions`（解密上行 key，
  绕过自有 proxy 的 60 RPM 限制，避免混淆）。
- 压测脚本：`/tmp/tr_rpm_test.py`（并发梯度 + 固定速率持续 60s）、
  `/tmp/tr_limit_detail.py`（429 详情 + 双 key 对比）、
  `/tmp/tr_flash_clean.py`（干净态复测）。请求均为
  `max_tokens=1` + 极短 prompt，成本可忽略。
- 每个阶段之间冷却 65~70s，让滑动窗口归零。

## 结论（实锤）

| 模型 | 限流（每 key） | 证据 |
|---|---|---|
| z-ai/glm-5.3-free（免费） | **8 请求/分钟** | 429 错误信息原文：`Maximum 8 requests within 1 minutes, Including the number of failed attempts`；key1/key2 均复现 |
| deepseek/deepseek-v4-flash-0731 | **15 请求/分钟** | 429 错误信息原文：`Maximum 15 requests within 1 minutes`；干净态 20 并发 burst 过 15 挂 5；60/120 RPM 持续压测均只放行 ~17/min |
| deepseek/deepseek-v4-pro-0813 | ≥12 请求/分钟（未触顶） | 12 并发 burst 全过；推断与 flash 同级 15/min |

关键事实：

1. **限流维度 = (key, 模型)**，按 1 分钟窗口计数，**失败尝试也计入配额**
   （429 错误信息明确说明）。超出即 429，响应无 `x-ratelimit-*` 头，
   无法从 header 预知余量。
2. **与并发无关**：16 并发 burst 全挂只是因为 1 分钟内请求数 >15；
   12 并发在窗口干净时全过。没有独立的并发连接数限制。
3. **两把 key 配额独立**：同模型在两把 key 上各有独立窗口
   （key2 flash 12/12 通过、glm-free 6/12 被 8/min 卡住）。
4. 即单热点模型的实际上游天花板 ≈ **15 RPM × key 数**（当前 2 把 =
   ~30 RPM）；免费模型 8 RPM × 2 = 16 RPM。

## 费用核对（TR 侧）

- 余额：压测前 $47.07851 → 压测后 $42.47239，差 **$4.606**。
- 构成：t2v $1.00 + i2v $3.60（视频测试）+ 限流压测 **$0.0058**。
- 压测明细（TR 日志）：flash 89 笔 × 19 quota（84 prompt + 1 completion
  token/笔）、pro 12 笔 × 57 quota、glm-free 0 收费。注意 TR 对 "ping"
  也计 84 prompt tokens（其模板/tokenizer 计数偏大），我们的成本核算
  按 TR 计费口径不受影响（用量回传以 TR 上报为准）。

## 运营建议

1. **keypool 聚合 rpm_limit 保护不了热点模型，反而可能误杀多模型流量**：
   两把 TR key 实际配了 `rpm_limit=300`（聚合、跨全部模型），而 TR 真实
   限额是 (key, 模型) 15/min——单模型打满 100 RPM 时 300 的聚合检查照样
   放行，全靠 TR 429 + circuit breaker 兜底；反过来 100 个模型各 3 请求/
   分的正常流量就会撞到 300 聚合顶被误杀。**建议把 rpm_limit 调回 0**
   （纯靠 TR 429 + circuit breaker），或提到 1000+ 只当安全网；keypool
   目前不支持 per-(key,模型) 限速，要精确建模 TR 语义需加 model 维度
   计数（见下）。
2. **扩容路径**：TR 账号可建更多 API token，每把 token 有独立配额。
   预期单模型 >25 RPM 前，应在 TR 控制台加 key 并加入 portal
   `upstream_keys`（模型列表照抄现有两把）。
3. **热点模型预期管理**：deepseek-v4-pro/flash 类模型全平台合计
   ~30 RPM 上限；glm-free 类免费模型 ~16 RPM。控制台应对外说明或在
   定价页标注免费模型限速。
4. 用户 key 的默认 60 RPM（proxy `RATE_LIMIT_DEFAULT_RPM`）高于上游
   单模型容量，单用户不会直接把上游打爆（60 > 30 时才会），但多用户
   叠加必然撞 TR 429 → 用户侧收到 429。这是上游容量问题，非平台 bug。

4. **keypool 增强（如需要精确节流）**：keypool 的 RPM 计数是 key 级聚合，
   要按 TR 的 per-(key,模型) 语义节流，需在 upstream usage key 中加模型
   维度（`provider:keyID:model`），让热点模型在 keypool 层均匀限速而不是
   打到 TR 吃 429——注意 TR 429 **也消耗配额**（失败尝试计入），无脑
   重试会烧掉真实容量。

## 证据位置

- 压测输出：`~/.kimi-code/.../tasks/bash-p4s4d6kj`、`bash-foa0e397`、
  `bash-d3r52ukl`、`bash-njsgopvu` 的 output.log。
- TR 日志核对脚本：`workspace/tokenrouter/tr_log_ratelimit.js`
  （余额 + 按时间过滤的逐笔 token/quota）。

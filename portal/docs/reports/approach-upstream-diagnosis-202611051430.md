# approach 上游 glm-5.3 断连定性 — 直连绕过测试（2026-11-05 21:30 CST）

Status: ACTIVE
问题：approach-only 压测中 glm-5.3 三轮逐轮劣化（run#1 全过 → run#3 首请求 hang 125s
后整组 503），需判定是我方 proxy 问题还是上游问题。
方法：从 proxy EC2（与生产同网络路径）**绕过 proxy 直连上游**，复刻 run#3 负载；
TR 作对照组。

## 结论：上游问题，proxy 无责；TR 完全健康

| 测试项 | approach :8443（portal 登记地址） | approach :443（真实后端） | TR 直连（对照） |
|---|---|---|---|
| glm small | 401 Invalid token | 200 (2.5s) | — |
| glm small stream | 401 | 200 (8.5s) | — |
| glm medium 8k | 超时/EOF | **504 Gateway Timeout (~63s)** | — |
| glm 40k stream | SSL EOF | **504 (~81s)** | — |
| glm burst12 | 401 | 12/12 200 (avg 5.8s) | — |
| glm 16k 吞吐 | RemoteDisconnected | **499 (~71s)** | — |
| k3 small / burst | 401 | 200 / 11-12 | — |
| deepseek 长输出 ×16 | — | — | **16/16 全 200**（含 10487 tok 大单，11-66s） |

两个独立上游缺陷：

1. **portal 登记的 `:8443` 是坏的网关 front**（nginx+envoy 后的 One API 实例）：
   小请求 401 "Invalid token"，大请求 hang/断连。生产 proxy 实际从未走它——
   tcpdump 证实生产流量全部到 `:443`（运行态 provider 配置为早期残留的正确值）。
   已修：`providers.base_url` → `https://www.tokenservices.store/v1`，与线实一致。
2. **真实后端 `:443` 的网关对 ≥8k 上下文有 ~60s 墙**：medium 8k / 40k / 16k长输出
   全部 504/499。小请求与并发（burst12）完全正常。这就是三轮压测 glm 大上下文
   表现逐轮恶化、以及 run#3 首请求 hang 后熔断跳闸的上游侧解释——
   **kimi-k3 同 key 同通道交互类三轮全稳**，进一步证明是我方 proxy 无别。

## 推论与建议

- glm-5.3 经 approach 的能力上限 = **交互类/小上下文**（<8k，60s 内能完成）；
  大上下文/长输出必须走 TR（当前 TR 优先，正确）。40k 类请求在 approach 上
  结构性不可行（对方网关 60s 墙，非 key/配额问题）。
- run#3 后 approach key 曾短时 401（13:22 直连全 401）但经平台 200——当时的
  悖论由 :8443/:443 两层解释：直连测试打到了坏的 8443 front，proxy 走 443。
  key 本身未失效。
- 建议找 approach 供应商确认/调大其网关超时（当前 ~60s）；加第二把 approach
  key 不能突破该墙。
- 计费口径提醒：approach 上游 504/断连时可能已计费（TR 在 11-04 就有计费但
  我方未完成的对账差）——对账时需留意 approach 侧 quota 消耗。

## 复现命令（备查）

```bash
# 直连 443（真实后端）
curl -X POST https://www.tokenservices.store/v1/chat/completions \
  -H "Authorization: Bearer <approach-upstream-key>" ...
# 直连 8443（坏 front，401 对照）
curl -X POST https://www.tokenservices.store:8443/v1/chat/completions ...
# TR 对照（management key 在 workspace/tokenrouter/mgmt_key.txt）
```

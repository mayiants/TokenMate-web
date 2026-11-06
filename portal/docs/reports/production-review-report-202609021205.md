# 上线后生产 Review 报告（安全 / 性能 / 计费正确性）

> **Status:** ACTIVE
> 生成时间（UTC）：2026-09-02 12:05
> 范围：`ai-portal-go` + `ai-proxy-go` 近 1 个月变更（2026-08-02 ~ 2026-09-02，Portal 139 commits / Proxy 14 commits）、两台 EC2 运行状态、安全性、性能、计费正确性
> 背景：项目已上线，小范围测试阶段（约 100–2000 req/天）
> 方法：commit diff 人工审查 + 4 路并行代码 review + 生产库/容器实况核查（只读）

---

## 1. 总体结论

- **无 P0 级紧急线上事故**：计费主链路（webhook 批量插入、pre-consume/settle、退款幂等）经核查设计良好，本月无回归。
- **2 个必须立即处理的高危项（凭据泄露）**：AccessAPI 真实 key 进了 Portal git 历史；GitHub PAT 明文躺在 HANDOVER.md。两者均须**先轮换、再清理**。
- **4 个计费正确性中危项**集中在 Proxy 新媒体端点（图片/视频/音频）：异步任务先扣费无退款路径、媒体端点无余额预扣、`n`/`duration` 客户端可控无上限、音频异步任务无轮询路由。当前小流量下损失有限，但会随测试范围扩大直接变成资损。
- **性能在当前规模下余量充足**，两处预防性修复成本低（`usage_records(upstream_key_id, created_at)` 缺索引、媒体响应体无界 `ReadAll`）。
- CI 双门禁（fast-gate + regression、govulncheck、golangci-lint）运转正常，是供应链安全的主要保障。

---

## 2. 基础设施状态（两台 EC2，2026-09-02 实测）

### Portal EC2（172.31.45.213）

| 组件 | 状态 |
|---|---|
| `ai-portal-go` | Up 3 hours (**healthy**)，镜像基于 `main@f9d6be3`（2026-09-02 重建） |
| `ai-portal-postgres` | Up 8 days (healthy)，绑定 `127.0.0.1:5432` |
| `ai-portal-redis` | Up 8 days (healthy)，绑定 `127.0.0.1:6379` |
| `/api/health` | `{"status":"ok"}`，pg + redis up |
| 内存 | 3.7Gi 总 / 可用 2.4Gi，**无 swap**，容器合计约 125MB |
| 磁盘 | 38G，用 41% |

### Proxy EC2（172.31.43.214）

| 组件 | 状态 |
|---|---|
| `ai-proxy-go` | Up 2 hours (**healthy**，用户刚重建），`:8080` |
| `ai-proxy-redis` | Up 2 hours (healthy)，绑定 `127.0.0.1:6380` |
| `/health` | `healthy`，key_pool / model_registry last_sync 2026-09-02T11:51:34Z |
| 内存 | 1.9Gi 总 / 可用 1.4Gi，swap 4Gi（本日新增，此前因无 swap 构建 OOM 两次） |
| 磁盘 | 38G，用 46%；retention volume **804MB**（5,605+ 文件） |

**观察：**
- Portal EC2 无 swap，且所有 compose 容器**无 memory limit**；当前余量很大，但一个失控容器即可拖垮整机，建议补 limit + 1–2G swap。
- Proxy 上曾误跑 Portal 构建（已澄清分工）；本日已加 4G swap + `/usr/local/bin/go` 软链，构建不再 OOM。
- Retention 数据只进不出（`DiskStore.Cleanup()` 已实现但无定时调用，HANDOVER 已记录），按当前增速短期无虞，建议流量增长前接 cron。

---

## 3. 近 1 个月变更概览

### ai-portal-go（139 commits，约 100 个为纯 CSS/模板类替换）

| 主题 | 代表 commit | 评价 |
|---|---|---|
| Media billing（图片/视频/音频定价） | `1e2bd8b` `f43ad0d` `268b793` `13787aa` | 功能完整；变体匹配与预扣设计有 2 个中危问题（§5.1） |
| 定价/模型状态联动 | `13fe1bd` `5915525` `56b967b` `59e17ba` | pause/resume 级联方向正确，但 resume 会"复活"管理员手动停用的规则（§5.1-MED） |
| 企业重分配 + 分组企业可见性 | `04eef01` `4244b43` | 实现质量高：参数化 SQL、事务后失效缓存、双端审计；仅 2 个 LOW |
| 支付退款链路 | `bd5603b` `eb93c58` | PayPal 验签 fail-closed、退款幂等、`FOR UPDATE` 锁余额；但退款计算吞错误（§5.1-MED） |
| 密钥可见性收敛 | `4ee84cf` `0d1661b` `1a21cd7` `b4b93a0` | 修复了企业密钥暴露全平台模型的真实越权问题，验证通过 |
| upstream_ops 健康查询 | `6a0c679` `876db76` | LATERAL/GROUP BY 修复正确；性能问题见 §6（索引） |
| 可访问性/键盘导航 | `6c624ab` `324d07e` 等 | 模板/JS 层，无风险 |
| e2e 账号生命周期 | `714078b` `597f846` `f9d6be3` | CI 内 `if: always()` 开关固定账号；本日已补动态账号清理 + `disable-all` |
| 其他 | `f2d3d03` Go 1.25.13；`548ad4a` 审计 level 约束修复 | 均为真实 bug 修复 |

### ai-proxy-go（14 commits）

| 主题 | 代表 commit | 评价 |
|---|---|---|
| 异步图片/视频生成 | `615f842` `6bbef54` | 打通了 TokenRouter 异步链路；但有计费/校验 4 个中危问题（§5.2） |
| 图片按上游返回尺寸计费 | `b55ec25` `a09b592` `fb5c3c8` | 方向正确（按实际变体结算） |
| 音频端点 | `3f56c0c` | speech/generations 可用；**异步 audio 任务客户端无法取回**（§5.2-HIGH 倾向） |
| Retention analytics 引擎 | `9aba41c` `71a5f91` `80f45b3` | 离线分析设计合理（报告只含聚合+user_id 前 8 位）；刷新端点同步阻塞 + 无互斥（§6-MED） |
| `/v1/models` 去重 | `ac99269` | 正确 |
| 部分 token 计费（流中断） | `aacb65b` | 无重复扣费，核查通过 |
| 工程清理 | `5cb0e30` | Go 1.25.13 + lint 全开 + 死代码清理，纯收益 |

---

## 4. 安全发现

### HIGH（需立即处理）

**S-H1. AccessAPI 真实 key 已进入 git 历史 — ai-portal-go**
`tests/business/probe_accessapi_models.py:12` 硬编码一把 `sk-` 格式的 AccessAPI key（base `https://accessapi.ai/v1`），由 `8b2bab1` 提交且仍在 HEAD。这不是测试 fixture，是第三方付费网关的可用凭据。
**处置：**
1. 立即在 AccessAPI 控制台吊销/轮换该 key；
2. 用 env var 替换硬编码（脚本保留占位符）；
3. git 历史是否清洗可视风险决定（key 已轮换则历史残留风险降低，但仓库若曾公开/push 过即视为泄露）。

**S-H2. GitHub PAT 明文存于 HANDOVER.md — 已知问题，确认仍存在**
`/opt/ai-platform/HANDOVER.md`（git push 段）含有效 PAT，任何能读本机的进程/用户均可取，且对 `mayiants/*` 有 push 权限。项目文档早已标记"应轮换"但未执行。
**处置：** 立刻在 GitHub 撤销该 token → 从 HANDOVER.md 删除 → 改用 `gh auth login` 或 SSH deploy key 的流程说明。此 token 已长期暴露，视为已泄露。

**S-H3. `start.sh` 默认向生产 env 静默注入 E2E 种子开关 + 公开弱口令**
`start.sh:478` 默认 `SEED_E2E_ACCOUNTS=true`；`ensure_e2e_env_vars`（`start.sh:504-533`）会在每次 `check_config`（含生产 `portal` 模式）时把 `SEED_E2E_ACCOUNTS=true` 及硬编码密码（`E2E_AdminPass123!`，同见于 `.env.template:160`）追加进 `.env.go-platform`；`docker-compose.portal.yml:89` 会覆盖 compose 的 `false` 默认值。
**缓解事实：** 当前生产 e2e 账号为 `suspended` 而非删除，`initE2EAccounts`（`bootstrap.go:340-348`）对存在账号跳过，故不会复活。但一旦有人硬删账号或重建库，将以仓库公开口令创建 `super_admin`。
**处置：** `ensure_e2e_env_vars` 改为默认 `false`（对齐 compose），或仅在 `simulate`/`mvp` 模式注入；生产侧显式写 `SEED_E2E_ACCOUNTS=false`。

### MED

**S-M1. `ai-proxy-go/config.yaml` 被 git 追踪（尽管 .gitignore 已列）**
`git ls-files` 确认其被追踪（`4a8e3ca` 首次提交即入库），文件内含 `portal.sync_secret`、`auth.jwt_secret` 及 Redis/DB 口令等**非占位值**。AGENTS.md 声称它"gitignored"的安全网实际无效——本次审查确认当前值像开发级，但只要哪天写入生产值就会直接进历史。
**处置：** `git rm --cached config.yaml` + 提供 `config.sample.yaml`；若任何值曾用于生产则轮换。

**S-M2. 审计数据无独立备份（有前车之鉴）**
HANDOVER 记录过 638 条 `balance_transactions` 审计记录被误删且无本地备份。现有 `/opt/ai-platform/backups/` 为每晚 pg_dump，只能按快照恢复。
**处置：** 确认 RDS PITR/snapshot 保留策略覆盖审计表；中期考虑审计日志写 once-only 外部存储。

### LOW

- **S-L1** `internal/routes/sync.go:52` 用 `!=` 比较 sync secret（webhook 路径已是 `hmac.Equal`）；建议统一 `subtle.ConstantTimeCompare`。
- **S-L2** Proxy `/api/webhook/*` 接受 `X-Admin-Secret` 作为 sync secret 回退且无时间戳/nonce 防重放（`/admin/*` 已有 HMAC 强校验）；爆炸半径略增，建议收敛。
- **S-L3** 依赖整体新鲜（gin v1.12.0、jwt/v5 v5.2.2、x/crypto v0.52.0），双仓库 CI 均跑 govulncheck，保持 CI 绿即可。
- **S-L4** CI 硬编码 ephemeral 口令（`ci-test-password`）为一次性 runner 设计，可接受。

**已核查无问题：** Portal webhook 全部 HMAC-SHA256 验签 + ±300s 时间窗 + 1MB body 上限 + secret 未配置时 fail-closed；admin 路由组有 IP 限流 + RequireAdmin 兜底（fail-closed）；`/metrics` 常量时间比较；Proxy admin 端点强制 HMAC 签名（明文回退已被测试拒绝）；`dev_mode=false` 时拒绝非 HTTPS Portal URL；Postgres/Redis 均绑 localhost；CORS 双端均为白名单 + 403；**`/admin/retention/:trace_id` 无路径穿越**（Gin 参数不含 `/`，文件名仅精确 basename 匹配）；retention 头脱敏覆盖 `authorization/x-api-key/x-admin-secret/cookie`；env 模板无真实值； HANDOVER 记载的三起历史事故（审计 level 约束、企业密钥模板、reconciliation NULL）均已在代码中确认修复。

---

## 5. 计费正确性发现（钱相关，优先级高于一般功能 bug）

### 5.1 Portal 侧

**B-M1. 订阅退款计算吞错误 → 用户余额静默损失** — `internal/routes/payment.go`（`calculateSubscriptionRefund`，约 :620）
任何 DB 错误返回 `(0, "", nil)`，随后 `processRefundCredits` 继续取消订阅并标记已退款，但**未把未用额度退回余额**。瞬时 DB 错误 = 永久性资损。
**修复：** 返回 error 并中止事务（订单状态翻转随回滚）。

**B-M2. `discontinueModel` 吞 UPDATE 错误，handler 恒 200** — `internal/routes/models_eol.go:152`
模型 discontinue 失败也返回成功；自动 EOL 任务 best-effort 可接受，但 admin 手动操作应暴露失败。修复：函数返回 error，handler 映射 500。

**B-M3. resume 会"复活"管理员手动停用的定价规则/路由** — `models_crud.go:1695` `reactivatePricingRule`、`models_eol.go:185` `enableModelProviderRouting`
`13fe1bd`/`5915525` 去掉了 `note='auto-created on model confirm'` 守卫后，pause→resume 会把管理员手动 inactive 的规则静默恢复。修复：用标记区分"因 pause 停用"与"管理员停用"，或 resume 时提示。

**B-M4. 媒体预扣低估：worstCasePrices 按最贵变体定固定费，但实际按时长/区块数乘算** — `internal/services/pricing.go:1054` vs `:336`
长视频实际成本可远超预扣额 → 结算后负余额风险。建议按 `duration`/`blocks` 维度做预扣，或设预扣上限 + 超额即时结算。
另：`matchVariant`（`pricing.go:~355`）用双向 `strings.Contains`，模糊标签（`1024x1024` vs `1024`）可能选错变体档位。建议严格化匹配。

### 5.2 Proxy 侧（本月新增媒体端点集中出问题）

**B-H1（倾向 HIGH）. 异步任务**派发即计费、失败无退款；audio 异步结果无路由可取**** — `handlers_images.go:259` `handlers_video.go:244` `handlers_audio.go:643`
上游返回任务信封时 Portal 立即出账；任务后续失败不退款。更严重：`handleAudioGenerations` 可能返回 async `task_id`，但 **`GET /v1/audio/generations/:task_id` 路由不存在**（`server.go:261-264`）——客户被扣费却无法取结果。
**修复：** 补 audio 轮询路由；或改为任务成功首 poll 时才计费。

**B-M2. 所有媒体端点无余额预扣/门控** — 与 chat 链路（`preConsumeLocal`+settle）不同，图片/视频/音频只在上游成功后上报。零余额 key 也能发起媒体生成，平台垫资。
**修复：** 媒体 handler 接同一套 pre-consume/settle，或至少做余额检查。

**B-M3. 客户端可控的 `n`（image_count）与 `duration` 直接进计费、无上限** — `handlers_images.go:109` 仅 clamp `n<1`；Portal `pricing.go:321,336` 原样使用
`n:100000` 会原样转发并计费 100000×变体（若上游忽略 n）。**修复：** Proxy 侧 cap `n`（如 ≤10）并对 `duration` 设上限；尽量按上游返回时长计费。

**B-M4. `task_id` 未校验即拼进上游 URL** — `handlers_images.go:333` `handlers_video.go:318`
Gin 会解码 `%2F`→`/`、`%3F`→`?`，持有有效 key 的攻击者可向上游请求注入路径/查询串，且请求携带**上游供应商凭据**，配合可控 `model` 参数可跨 provider 边界。修复：严格 regex 校验（`^[\w.-]{1,128}$`）+ `url.PathEscape`。

**B-M5. analytics 刷新无互斥** — `scheduler.go:104`（详见 §6-MED3；同时属正确性：同秒两次 run 报告文件名冲突互相覆盖）

**LOW：** 视频上游 body 重写丢未知字段（`6bbef54`，建议 map 级补丁）；Proxy 侧媒体 `Cost` 计算与实际不一致（Portal 重算兜底，建议直接置 0）；10MB `LimitReader` 截断不报错（应 413）。

**已核查无问题：** 流中断部分计费无重复扣费/重复退款；async 轮询 GET 只读不计费；媒体端点无 SSRF（上游 URL 来自 keypool 配置）；usage 上报头脱敏。

---

## 6. 性能评估

### 结论先行
当前 100–2000 req/天规模下**余量充足、无急性问题**；计费/用量管线（批量 INSERT、批量定价、pipeline Redis、异步 flush + 退避重试、本地令牌桶限流 = 请求路径 0 次 Redis RT）设计良好。以下为**随流量增长会劣化**的项，按性价比排序。

### MED

**P-M1. `usage_records.upstream_key_id` 无索引 + upstream-ops 页每 key 一次 LATERAL 扫描**
`internal/query/upstream_ops.go:389-399`；生产库 `pg_indexes` 确认无该索引；CSV 导出单页最多 5000 key。当前 12 key / 5.5k 行无感；增长到 100k 行/月 × 90 天窗口 × 50 key/页 ≈ 1500 万行扫描/次。
**修复：** `CREATE INDEX ON usage_records (upstream_key_id, created_at)`；更优：复用 `upstream_key_usage` 预聚合表出 `error_dist`。

**P-M2. `service_models` OR-join 无索引且每请求执行**
`upstream_ops.go:239-240, 289-301`，由 `/status` 和 admin 页每次请求触发；`api_model_name` 无索引，OR 条件使 `UNIQUE(provider, product_id)` 失效，每次 seq-scan 211 行。
**修复：** 加 `service_models(api_model_name) WHERE status='active'` 索引；更优：进程内缓存 model→type 映射（仅 admin 操作失效）。

**P-M3. analytics 刷新在 HTTP handler 内同步全量扫 90 天 gzip，且无运行互斥**
`handler_analytics.go:108` → `scheduler.go:100-128`；约 9k–180k 文件串行解压，admin 请求被挂住；与 6h 定时任务可重叠，同秒两次运行报告文件名撞车互相覆盖。
**修复：** `sync.Mutex/TryLock`（运行中返回 202/409）；改触发异步执行；文件名加纳秒。

**P-M4. 媒体/音频上游响应体无界 `io.ReadAll`**
`handlers_images.go:155,355` `handlers_video.go:161,340` `handlers_audio.go` 多处；视频载荷可达 100MB+，叠加 300s media client 与 2GB 内存的 Proxy 主机，少量并发大生成即可 OOM/抖 swap。
**修复：** `io.LimitReader`（如 video 512MiB / image+audio 64MiB），超限干净失败。

**P-M5. 容器无内存限额；Portal 主机无 swap**
两个 compose 均无 `deploy.resources.limits`。修复：portal 512m / postgres 1g / redis 256m / proxy 1g + `pids_limit`；Portal 主机加 1–2G swap。

### LOW

- `/api/keys/validate` 缓存击穿无 singleflight（有 IP 限流+1min 负缓存兜底，可加）；
- admin 用户列表 `key_count` 相关子查询无 `api_keys(user_id)` 索引；
- 限流计数器本地优先、异步同步、chan 满静默丢（单实例 OK，多实例/重启会欠 enforcement——**文档化为扩展约束**）；
- chat settle 路径约 15 处绕过 `SettleBuffer` 直接 `go Settle()`（背压时 goroutine 无界，当前不可见）；
- 每页加载都拉取 `/api/announcements`（可加 30–60s 缓存）。

### 容量余量速查（代码实测值）

| 资源 | 现值 | 说明 |
|---|---|---|
| Portal 主机 | 3.83GB / 无 swap，容器用 ~125MB | 余量大，但无护栏 |
| Portal pgx 池 | 默认 20 连接（PG max_connections=100） | 余量充足 |
| Usage buffer | batch 100 / 3s flush / chan 10 | ~333 rec/s 持续能力，现用量 <0.1% |
| Settle buffer | chan 100 / 单 worker / 退避 0.5s→10s | 吞吐下限 ≈ 1/portal-RT |
| Rate-limit 同步 | chan 10k / 100ms | ~100k 计数/s，满则静默丢 |
| Retention 分析 | 6h 间隔 × 90 天窗口 | 随 req/day 线性增长，首个需设界的增长点 |
| Media client | 300s 超时 + 无界 body read | 见 P-M4 |

---

## 7. 建议处置优先级

| 优先级 | 事项 | 动作 |
|---|---|---|
| **P0（本周内）** | S-H1 AccessAPI key、S-H2 GitHub PAT | 轮换/撤销 → 代码/文档清理 |
| **P0** | S-M1 proxy `config.yaml` 出库 + S-H3 `start.sh` E2E 默认改 false | `git rm --cached` + 脚本一行改动 |
| **P1（2 周内）** | B-H1 异步计费退款/audio 路由、B-M2 媒体余额门控、B-M3 `n`/`duration` 上限、B-M4 `task_id` 校验 | Proxy 侧 4 个补丁，均为局部改动 |
| **P1** | B-M1 退款吞错误、B-M4(5.1) 预扣上限 | Portal 侧，直接防资损 |
| **P2（1 月内）** | P-M1/P-M2 两个索引、P-M3 分析互斥、P-M4 LimitReader | 低成本预防性修复 |
| **P2** | B-M2(5.1) discontinue 错误透传、B-M3(5.1) resume 守卫、S-M2 审计备份确认 | 质量加固 |
| **P3** | 全部 LOW 项 + 容器 memory limit + Portal swap | 随下次迭代顺带 |

---

## 8. 附录：已核查且未发现问题的重要区域

- Portal 全部运行态 SQL 参数化；字符串拼 SQL 仅存在于运维导入脚本（无用户输入）。
- Webhook 签名、admin RBAC、fail-closed 语义、缓存失效在事务提交后执行（顺序正确）。
- 退款/取消幂等（条件翻转 + `FOR UPDATE`）。
- Proxy 限流/用量/结算缓冲区：批量、重试、死信、关闸等待均正确。
- 流中断部分计费、async 轮询只读、`/v1/models` 去重、报告文件权限（0640/0750）。
- e2e 固定账号 CI 内 `if: always()` 开关；生产账号当前 `suspended`。
- 历史事故三件套（审计 level 约束、企业密钥模板渲染、reconciliation NULL 扫描）均已修复并有回归覆盖。

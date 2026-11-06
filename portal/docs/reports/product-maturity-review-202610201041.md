# 产品化成熟度 Review 报告（运营 / 安全 / 功能）

> **Status:** REMEDIATED (P1 全部修复并上线；P2 代码全部修复，仅 retention/fallback cleanup cron 延后到 S3；P1-5 备份异地待 S3)
> **Date (UTC):** 2026-10-20 10:41（修复更新 12:35）
> **范围:** `ai-portal-go` + `ai-proxy-go`，重点审计 **2026-09-02 之后新增/变更的代码**（约 120 portal + 30 proxy commits），叠加两台 EC2 运行态与运维现状。
> **背景:** 平台已进入**限量商用试运行**。
> **方法:** 读取既有 review 基线（`production-review-report-202609021205.md`、`pre-launch-audit-report-202608142320.md`、`operations-risk-checklist-202608142350.md`）避免重复；对新代码做四路独立并行审计（authz / billing / secrets+proxy / 运维可靠性）；对两个最关键发现（P1-1 预付费退款丢失、P1-2 吊销不传播）做了亲自代码复核。

---

## 0. 修复状态（2026-10-20 更新）

| 项 | 状态 | 说明 |
|---|---|---|
| P1-1 预付费退款丢失 | ✅ 已修 + 测试 + 上线 | `webhook_billing.go` 批量路径改 LIFO；`TestEnterpriseRefundLIFO_BatchPath` |
| P1-2 吊销不传播 | ✅ 已修 + 测试 + 上线 | Portal `PublishKeyRevoked/PublishUserBanned` 挂在 invalidate 节点；Proxy HTTPS webhook 扩展 |
| P1-3 管理令牌生命周期 | ✅ 已修 + 测试 + 上线 | `authenticate()` owner 重校验 + suspend 时 `RevokeManagementTokensForUser` |
| P1-4 PAT 泄露 | ✅ 已从 HANDOVER 移除；用户已在 GitHub 吊销 | — |
| P1-5 备份异地 | ⏸ 待 S3（用户暂缓） | S3 就绪后启用；届时 retention 数据单独存储 |
| P2 usage fallback 回放 | ✅ 已修 + 测试 | `fallbacklog.Replay` + `UsageBuffer.replayLoop`（2min） |
| P2 chat 结算 SettleBuffer | ✅ 已修 | 30 处 `go Billing.Settle` → `s.Settle`（缓冲、重试、关机排空） |
| P2 mgmtRemoveMember | ✅ 已修 | 最后 admin 守卫 + 完整 detach/配额回收/缓存失效 |
| P2 sync secret 常量时间 | ✅ 已修 | `subtle.ConstantTimeCompare`；防重放延后（见下） |
| P2 倍率 CTE 环检测 | ✅ 已修 + 测试 | path 数组环检测（两处 CTE）；写入侧确认无 cycle 路径 |
| P2 监控告警 | ✅ 已加 | `/opt/ai-platform/monitor.sh` + cron（health/disk/container） |
| P2 retention/fallback cleanup cron | ⏸ 延后到 S3 | 届时 retention 迁移到 S3 单独存储，再接清理 |
| **运维** Cloudflare 灰云后 portal 登录慢 | ✅ 已定位 + nginx 侧修复 | 见 §9 |

**接受未改（有依据）:**
- sync/webhook **防重放**（nonce/timestamp）：payload 幂等、VPC 内网、需协调发送端改动，风险>收益，暂不加。
- 倍率 CTE **写入侧 guard**：确认全仓无任何 UPDATE 改 `parent_group_id`（仅 INSERT 指向顶层平台组），结构上不可能成环；CTE 环检测已是防御。

---

## 1. 总体结论

- **核心账务链路工程质量高**：幂等（trace_id 去重）、worst-case 预冻、倍率单次应用、退款幂等等防御设计良好；9-02 review 的历史 P0/P1 多已修复（`config.yaml` 出库、AccessAPI key 已删、`start.sh` E2E 默认 `false`、每日 DB 备份已跑、容器内存限额已加、media SSRF/`task_id` 校验/余额门控/异步退款均已修）。
- **新功能与运维侧引入/遗留若干真实缺陷**，其中 **4 个 P1 会直接导致资损或越权**。
- **系统性薄弱点（主题）:** 平台的“创建/扣费”路径写得很全，但 **“撤销 / 回滚 / 访问回收”路径常漏一半**——P1-1（退款不对称）、P1-2（吊销不传播）、P1-3（令牌不回收）、以及 P2 的 `mgmtRemoveMember`（移除不彻底）属同一类问题。建议集中补齐。

---

## 2. 基础设施与运维现状（2026-10-20 实测）

| 项 | 现状 | 评价 |
|---|---|---|
| Portal EC2 (172.31.45.213) | `ai-portal-go` healthy、Postgres/Redis 容器 healthy（Up 9 天）；容器均有 mem_limit（portal 1g / pg 1g / redis 256m）；swap 4G | 容器护栏已补齐 ✅ |
| Proxy EC2 (172.31.43.214) | `ai-proxy-go` healthy | — |
| 磁盘 | `/dev/root` 38G，**已用 80%**；`/var/lib/docker` 仅 540M，backups 7.7M，workspace 1.8G | 磁盘压力来自 OS/root，非我方产物；但 retention cleanup 未接入会持续增长 |
| DB 备份 | 每日 03:00 `pg_dump` cron 正常（现有 8 份）；**但 `aws s3 cp` 注释掉、`-mtime +7` 本地保留、与 DB 同盘** | **唯一不可恢复风险**（见 P1-5，用户暂缓因 S3 未开通） |
| DB 架构 | 单 Docker Postgres，无 RDS/Multi-AZ | 试商用可接受，规模化前迁移 |
| 监控告警 | 仅备份 cron；无 /health 轮询、无 CloudWatch 告警 | 见 P2 |

---

## 3. 发现清单（按严重度）

### 🔴 P1 — 必须尽快处理

#### P1-1 [资损·已复核] 企业预付费钱包退款在主批量路径丢失真金
- **位置:** `internal/routes/webhook_billing.go`（`processBillingBatch` 企业退款分支，约 :625-634）。
- **问题:** 批量路径（线上主路径）退款只做 `UPDATE enterprises SET credit_used = GREATEST(credit_used - $1, 0)`，**从不恢复 `prepaid_balance`**；而逐条路径 `webhook_usage.go:505-527` 正确做了 LIFO 拆分（先还月度 allowance，余额回 prepaid）。
- **复核证据:** 两个文件的 SQL 已逐行比对确认不一致。
- **后果:** charge-on-dispatch 媒体任务（TokenRouter 视频等）从预付费扣款 → 上游失败 → 退款走批量路径 → 预付费被 `GREATEST(…,0)` 下限吞掉，**永久丢失**。Intel 等企业有预付费余额，每笔失败的按派发计费任务都漏钱。
- **修复:** 把 `webhook_usage.go` 的 LIFO 拆分逻辑原样搬入 `processBillingBatch`；补批量路径 prepaid 退款测试。
- **性能影响评估:** 见 §4（纯 Portal 内 DB 事务路径，**不涉及 portal↔proxy 通信**）。

#### P1-2 [越权·已复核] Key / 用户吊销不向 proxy 传播
- **位置:** Portal `internal/db/db.go`（只发布 `RedisSyncChannel`/`RedisBalanceChannel`，无吊销频道）；proxy `internal/auth/auth.go:652/668`（`PublishKeyRevoked`/`PublishUserBanned` 已实现）+ `:844/850`（消费端 `key_revoked`/`user_banned` 已实现）。
- **复核证据:** Portal 中 `key_revoked`/`user_banned` 仅为审计 `ChangeType` 字面量（`enterprise_keys.go:486`、`management_tokens_consumers.go:182`），全仓无 Portal 端吊销 Redis 发布；proxy 发布/消费端齐全但从未被 Portal 触发。
- **后果:** proxy 校验缓存 TTL 5 分钟、降级态 `gracefulTTL` 30 分钟。禁用/删除 key、封禁用户、企业欠费后，被吊销的 key 仍可继续消费最长 5~30 分钟。
- **修复:** Portal 在 key 禁用/删除、用户 suspend、企业 suspend 时向共享吊销频道 `RedisPublish`（proxy 消费端已就绪，只差发布端）。

#### P1-3 [越权] 管理令牌在 owner 离职 / 降级 / 改派后仍然有效
- **位置:** `internal/routes/management_tokens.go:247`（`authenticate()` 只校验 `status=active` + 未过期，不校验 owner 现状）；无任何生命周期事件吊销令牌。
- **后果:** 企业 admin 创建 `manage:keys`+`manage:members` 令牌后被降级/移出/封禁/改派，UI 会话失效，但令牌仍能无限期增删改原企业的 key 与成员（默认 TTL 365 天）。
- **修复:** `authenticate()` join users+enterprises 校验 owner 角色/成员关系/状态；并在 demote/remove/quit/reassign/suspend 事件中显式 `UPDATE management_tokens SET status='revoked'`。

#### P1-4 [凭据泄露·已确认] GitHub PAT 明文在 HANDOVER.md
- **位置:** `/opt/ai-platform/HANDOVER.md`（“常用命令”段内嵌对 `mayiants/*` 有 push 权限的有效 PAT；9-02 review 已标 S-H2 至今未处理）。
- **后果:** 任何能读该主机或备份者可污染 CI、向生产镜像注入代码（供应链攻击）。
- **修复:** 从 HANDOVER.md 删除该行并以占位符替代；**提示用户必须在 GitHub 吊销该 token（视为已泄露）** 并改用 `gh auth`/SSH deploy key。

#### P1-5 [运维·durability] 备份只在本机、无异地 — **用户暂缓（S3 未开通）**
- `backup-db.sh` 的 `aws s3 cp` 注释掉、`-mtime +7` 本地保留、与 Postgres data 同盘。单实例/EBS 损坏即数据库与全部备份一起丢，RPO 实际为 ∞。
- **处置:** 用户尚未开通 S3，本轮暂不动。开通后启用 S3 上传（或迁 RDS PITR）。**这是唯一不可恢复的风险，S3 就绪后应最优先处理。**

### 🟡 P2 — 规模化前必须处理

- **retention + fallback 日志 `Cleanup()` 从未接入**（`disk_store.go:174`、`writer.go:71` 仅测试调用），磁盘 80%。磁盘写满会打断 Portal 主机 Postgres WAL/写入 → 控制面全挂。接 cron 即可。
- **usage fallback NDJSON 只写不回放**（`usage_buffer.go:166`）：Portal 不可达超 3 次重试后写 `usage-fallback-*.ndjson`，但全仓无回放代码 → Portal 抖动窗口 = 永久少计费，且每 100 条才打一次日志。
- **chat 结算仍 fire-and-forget**：`handlers_chat.go` 约 15 处用裸 `go Billing.Settle(context.Background(),…)` 而非 `SettleBuffer`，崩溃/关机即丢，对应 Portal 5 分钟冻结到期后无人回收 = 静默漏收。
- **`mgmtRemoveMember`** 缺“最后一个 admin”保护；且不清 `users.enterprise_id`/不回收配额/不失效个人 key 校验缓存（被“移除”的成员仍能继续烧企业额度）。应复用 `removeMyEnterpriseMember` 完整逻辑。
- **`sync.go:52` 用 `!=` 比较 sync secret**（非常量时间；该端点会 `crypto.Decrypt` 返回明文上游供应商密钥），proxy `/api/webhook/*` + `/api/internal/*` 无 timestamp/nonce 防重放。改 `subtle.ConstantTimeCompare` + 时间戳。
- **倍率递归 CTE 无环检测**（`billing.go:156`、`apikeys_validate.go:147`）：组链 >10 层漏乘（少收）；若 `parent_group_id` 成环则乘 10 次（超收）。当前创建流程构不成环，属防御缺口，应加写入时环检测 + 兜底。
- **无监控告警**：除备份 cron 外无人轮询 /health、无 CloudWatch、失败全静默。

### 🟢 P3 — 质量 / 加固

- `/v1/moderations`（新增）无余额预扣门控；上游请求体用了逻辑模型名而非 `upstreamModelName`（可能发错模型）。
- 媒体按**客户端请求的图片数** `n` 计费而非上游实返数量，上游忽略 `n` 时最多超收 10 倍（已有 n≤10 兜底）。
- 余额检查在 Redis+Portal 双挂时 **fail-open**（试商用可接受，规模化前加消费上限）。
- 限流为单进程内存态、异步同步、channel 满静默丢、重启清零（单实例 OK，水平扩展时变 N 倍松弛）。
- 管理令牌 scope 白名单实际可授予 `manage:*`，但注释/迁移头只写 `read:*`（误导 blast radius 判断）。
- `proxy_request_audits.model` 存了 vendor 前缀上游模型名（该表无生产端点读取，未对用户暴露，属 DB 卫生）。

---

## 4. P1-1 性能与服务间通信影响评估（应用户要求）

**结论先行:** P1-1 的修复是**纯 Portal 内部的 DB 事务路径改动，对 Portal 性能影响可忽略，对 proxy 性能与 portal↔proxy 通信压力无任何影响（零增量请求）。**

### 4.1 改动所在的调用路径
- `processBillingBatch` 位于 Portal 的 **webhook 计费消费侧**，由 proxy 经 `POST /webhook/usage`（批量 usage 上报）触发，Portal 在**本地同一数据库事务**内完成扣费/退款。
- proxy→portal 的通信量由 proxy 的 usage buffer（batch 100 / 3s flush）决定，**与 Portal 内部退款 SQL 的写法无关**。P1-1 不新增任何 proxy→portal 或 portal→proxy 的调用。

### 4.2 对 Portal 的增量开销
修复把企业退款分支从：
```sql
UPDATE enterprises SET credit_used = GREATEST(credit_used - $1, 0) ...   -- 1 UPDATE
```
改为 LIFO 拆分（与 `webhook_usage.go` 逐条路径一致）：
```sql
SELECT COALESCE(credit_used,0) FROM enterprises WHERE id=$1   -- 1 SELECT（主键点查，纳秒级）
UPDATE enterprises SET credit_used = credit_used - $toAllowance,
                       prepaid_balance = prepaid_balance + $toPrepaid ...  -- 1 UPDATE
```
- 增量 = **每个批次退款分支多 1 次主键 SELECT**（`enterprises.id` 主键索引，命中单行）。
- 该分支**仅在批次中存在企业退款（`entRefundTotal > 0`）时执行**——即仅 charge-on-dispatch 媒体任务失败才触发，属低频路径（非每请求）。
- 仍在原有 `tx`（单事务）内，不增加事务数、不增加锁范围（`enterprises` 行锁本就被同事务持有）。

### 4.3 两服务间通信压力
- **零变化。** 不改 webhook 协议、不改 usage buffer 批量参数、不新增回调/同步/pub-sub。
- 对比：P1-2（吊销传播）才会新增 portal→proxy 的 Redis pub/sub 消息，但其量级极小（仅在 key 禁用/删除、用户/企业 suspend 时各发一条，属管理操作频率，远低于计费流量），且走的是已有的 Redis 连接与 proxy 既有消费端，无新增连接/轮询。

### 4.4 评估结论
| 维度 | P1-1 影响 |
|---|---|
| Portal CPU/延迟 | 可忽略（退款低频分支 +1 主键 SELECT） |
| Portal DB 负载 | 可忽略（无新表/索引/扫描；单事务内） |
| portal→proxy 通信 | **无** |
| proxy→portal 通信 | **无** |
| proxy 性能 | **无** |

因此 P1-1 可安全修复，无需为性能/通信压力做额外节流或批处理设计。

---

## 5. 经核查确认安全 / 已修的要点（节选）

- 管理令牌/自助企业 key/企业数据读取**无跨租户越权**（查询按服务端解析 enterprise_id 作用域，`:id` 额外带 `AND enterprise_id=$ent`）；令牌仅存 SHA-256，一次性返回。
- 正常计费路径倍率**只乘一次**（`2a776bc` 的 `if !proxySentRatio` 守卫正确），validate 与 webhook 倍率口径一致；tiered_context 每档带 fixed_request、worst-case 预冻正确；trace_id 去重、退款幂等、流中断部分计费均正确。
- media SSRF 已修（`task_id` 正则 `^[A-Za-z0-9_-]{1,64}$` 拼 URL 前校验 + 测试覆盖）；异步退款/预扣门控/`n`、`duration` 上限均已修；`PROXY_CALLBACK_URLS` 推导无 SSRF；retention header 脱敏覆盖 authorization/x-api-key/x-admin-secret/cookie 且 `trace_id` 无路径穿越；普通用户 vendor 掩码确实生效（仅 admin 看到真实上游 provider + client_ip）。
- 认证中间件每请求从 DB 重读 role + token_version，拒绝 deleted/suspended；企业自管理不信任 JWT、super admin 不旁路。
- Portal webhook 全程 HMAC + 300s 窗口 + fail-closed，无 X-Admin-Secret 回退。

---

## 6. 建议处置顺序

| 优先级 | 事项 | 本轮动作 |
|---|---|---|
| **立即** | P1-4 吊销 HANDOVER PAT | 代码/文档清理 + 提示用户 GitHub 吊销 |
| **本轮修复** | P1-1 预付费退款批量路径 | 代码修复 + 测试（性能/通信评估见 §4） |
| **本轮修复** | P1-2 吊销传播 | Portal 新增 Redis 发布 + 测试 |
| **本轮修复** | P1-3 管理令牌生命周期吊销 | authenticate 现状校验 + 事件吊销 + 测试 |
| **S3 就绪后** | P1-5 备份异地 | 用户暂缓 |
| **规模化前** | 全部 P2 | retention cleanup cron、usage fallback 回放、chat SettleBuffer、mgmtRemoveMember、sync 常量时间、CTE 环检测、监控告警 |
| **迭代顺带** | 全部 P3 | moderations 门控、媒体按实返计费等 |

---

*本报告由四路并行独立代码审计 + 两项关键发现的亲自复核生成。P1-1/P1-2/P1-3/P1-4 的修复在本会话后续提交；P1-5 按用户指示暂缓（S3 未开通）。*

---

## 9. 运维诊断：Cloudflare 灰云后 Portal 登录慢（2026-10-20）

**现象:** 几天前把 `api.woodgit.com` 从橙云切灰云（仅 DNS）后,用户 web 登录 portal 明显变慢。

**诊断（本机实测）:** 源站本身很快——portal 应用 TTFB 2-8ms、登录 API ~82ms（bcrypt）、整页 HTTPS ~44ms（本机）。源站在 **ap-southeast-1（新加坡）**。慢的根因是:灰云后所有用户直连新加坡源站,**失去了 Cloudflare 全球边缘**（就近 PoP 的 TLS 终结 + 暖连接复用）;中国用户每次都要跨境完成 TCP+TLS 握手(~250-300ms RTT × 多个往返)。并且**源站 nginx 有 3 处配置放大了这个延迟**:

1. **静态 JS/CSS 完全未压缩**（`gzip_types` 被注释掉,只压 HTML）——登录页拉取 ~550KB 未压缩 JS/CSS。
2. **无 TLS 会话复用**（`ssl_session_cache`/`ssl_session_tickets` 缺失）——每个新连接都付完整 TLS 握手。
3. **静态资源 `max-age=3600`**——浏览器每小时重新下载全部资源。

**已修（nginx 侧,无需重建应用）:** `/etc/nginx/sites-enabled/api.woodgit.com`（备份 `.bak-20261020`）:
- `gzip on` 覆盖 JS/CSS/JSON/SVG/字体 → chart.js 208→71KB、app.css 101→21KB、htmx 51→17KB、alpine 45→16KB(**~550KB → ~160KB,-70%**)。
- `ssl_session_cache shared:SSL:20m` + `ssl_session_tickets on` + `ssl_protocols TLSv1.2 TLSv1.3` → 重复连接免握手。
- `/static/` `Cache-Control: public, max-age=31536000, immutable`（应用已用 `?v=N` 做 cache-busting,安全）。
- 保留 `/v1/` 600s 读超时（长图像/视频生成,灰云直连无 CF 100s 限制）。
- `nginx -t` 通过并 reload;实测 gzip + immutable 生效。
- **坑:** `sites-enabled/api.woodgit.com` 是**普通文件而非指向 sites-available 的软链**,必须直接改 sites-enabled。

**最大的剩余改进（建议,需用户决策）:** **把 web 登录域重新切回橙云**。当初切灰云只是为了绕开 Cloudflare 对长媒体生成的 ~100s 边缘超时。更好的组合是:
- **web/portal 域走橙云**（拿回全球边缘 + Brotli + DDoS/WAF,中国用户就近握手,登录快）;
- **`/v1/` 长媒体生成单独用一个灰云子域**（或 Cloudflare 的 Enterprise/规则放大 origin 超时),直连源站避开 100s 限制。

这样既解决登录慢,又不牺牲长任务。nginx 侧优化对两种模式都有益,已先行落地。

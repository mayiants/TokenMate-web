# WOODGIT 试商用一个月：商用就绪度 Review 报告

Status: ACTIVE
Date: 2026-11-06
Scope: ai-portal-go（控制面）+ ai-proxy-go（数据面）+ 生产数据（Portal PostgreSQL、Proxy EC2 retention 卷）
Related: `product-maturity-review-202610201041.md`、`ops-reliability-audit-report-202610201003.md`、`platform-security-enhancements-design-202611042000.md`

---

## 0. 方法与证据边界

**做了什么**
- 只读查询 Portal 生产库（`ai_proxy_portal`，67 MB）的用户/Key/用量/账务/审计/保留相关表。
- 只读检查 Proxy EC2：容器状态、磁盘、retention 卷的目录结构和体积。
- 阅读关键代码：webhook 验签、Key 校验、加密、审计、retention、GDPR、备份与监控脚本。
- 对比 10-20 两份历史 review 的遗留项，确认哪些仍未闭环。

**没做 / 无法验证**
- 没有访问 AWS（本机无凭据），因此 **Security Group / Cloudflare 源站锁定情况未验证**。Portal 容器绑定 `0.0.0.0:3001`，是否只允许 Cloudflare/内网访问需要人工确认。
- 没有读取 retention 文件内容，只看了体积和目录。
- 没有做渗透测试和压测，没有重跑 CI（只读了配置和测试文件数量）。
- 竞品部分基于公开资料与我已有的知识，**未联网逐项核对最新版本**，落地前请复核。
- 用户提到测试/调试期间 agent 直接操作 DB、有硬删除。本报告里的"历史数据"只能代表**幸存的数据**，不是完整历史。

---

## 1. 一个月的数据画像

### 1.1 规模（均来自生产库查询）

| 指标 | 数值 | 备注 |
|---|---|---|
| 用户 | 45 | user 34 / enterprise_admin 6 / super_admin 2；停用 5 |
| 企业 | 8 | **4 家为真实企业名称（其中仅 Aynbay、Intel 有用量记录；Danchen、Nanocell 用量为 0）**，另 4 家是 E2E 测试企业（`E2E-Corp-*`、`E2E-Enterprise-*`、`E2E Enterprise`） |
| API Key | 174 | 其中 155 状态 active；43 个名字含 test/e2e/load/approach 等测试痕迹 |
| 近 14 天有调用的用户 | 14 | |
| usage_records | 21,941 行 | 2026-07-11 起；累计 cost ≈ **$951** |
| 月度 cost | 7月 $176 / 8月 $9.6 / 9月 $16.5 / 10月 **$535** / 11月(6天) $214 | 10 月起真实客户上量 |
| proxy_request_audits | 44,612 行 | 2026-09-05 起 |
| audit_logs | 1,122 行 | 2026-08-12 起 |
| enterprise_audit_log | 286 行 | |
| balance_transactions | 6,995 行 | usage 6,962 / voucher 18 / admin_adjustment 8 / recharge 3 … |
| 订单 | 16 | 完成 6（stripe 1、alipay 2、未记录渠道 3），pending 5，cancelled 5 |
| 上游 Key | 81 | tokenrouter 32、alibaba 12（**0 个校验通过**）、laozhang 10、openrouter 11 … |
| service_models | 623 | active 214 / discontinued 291 / paused 73 / ignored 45 |

### 1.2 延迟（近 14 天 success 请求）
p50 ≈ 1.3 s，p95 ≈ 18.8 s，p99 ≈ 75 s。长尾主要来自大上下文/长输出的 Opus/GLM 请求，需要结合 SLA 承诺定义口径（见 §3.6）。

### 1.3 从数据里读到的几个事实
1. **真实商业体量还很小**：2 家企业有实际用量（另有 2 家已开户未使用）、十几个活跃用户。试商用的主要价值是验证链路，不是验证容量。
2. **数据被测试污染**：生产库里有 E2E 企业、load-test 用户、大量 test key。后续做对账/审计报表时需要先清洗口径。
3. **数据完整性已经受损**：8,301 条 usage 记录（$100.44，7/11–11/2）引用的 4 个 Key 在 `api_keys` 中已不存在（被硬删除）。`usage_records` 没有任何外键。这些记录还在，但**无法再追溯到 Key 名称/归属人**（`api_key_name`、`key_prefix` 字段仅部分冗余）。
4. **只有成功请求被记录**：`usage_records.status` 只有 `success`（21,940）和 1 条 `refund`；`proxy_request_audits.status` 为 success 44,611 / error 1。**失败请求（429/5xx/超时/上游错误）几乎不落库**，意味着无法从 DB 计算成功率、无法出 SLA 报告、无法解释客户的"我请求失败了"。

---

## 2. 总体结论

| 维度 | 评级 | 一句话 |
|---|---|---|
| 核心计费链路 | 良好 | 预消费、幂等、退款、LIFO 扣减等设计成熟，历史 P1 已修复 |
| 功能完整性（对企业客户） | 中上 | 企业/成员/配额/预付费/信用额度/发票/对账页面都有；缺 SSO/MFA/IP 白名单/客户侧告警 |
| **审计与数据留存** | **不达商用标准** | 证据链断裂、IP 缺失、失败不记录、payload 明文无清理、审计可被超管软删 |
| 安全 | 中 | 基础面扎实（HMAC、bcrypt、AES-GCM、限流）；企业级身份与凭据治理缺失 |
| 可靠性/运维 | 试用级 | 单机单 AZ、备份无异地、告警未配置、retention 无清理 |
| 可维护性 | 中 | 测试和 CI 齐全，但文件巨大、文档泛滥、开发方式（agent 直连 DB）风险高 |

**结论**：作为"小范围、可信客户的试商用"可以继续；作为"面向企业客户收费并承诺审计/合规/数据隐私"的产品，**在 §4 的 P0/P1 项处理完之前不建议扩大客户范围或在合同里写审计/留存/SLA 条款**。最需要优先解决的不是功能，而是"证据链 + 数据留存治理 + 运维底线"。

---

## 3. 分维度 Review

### 3.1 商用价值
- **定位清晰**：面向企业的 token 聚合分发 + 权限/配额 + 对账审计 + 数据留存。这是 OpenRouter 个人/开发者市场的空档。
- **已有差异化能力**：企业账户体系（成员、两级审批、配额回收）、预付费钱包 + 信用额度、对账运营后台、多币种展示、双轨路由（AWS Bedrock / OpenRouter / TokenRouter / approach 等）、缓存计费（cache read/create 已在 11-5 打通）。
- **商用风险**：差异化的几个点恰好是"审计 / 留存 / 隐私"，也正是目前**质量最弱**的部分（§3.3）。如果把这些作为卖点而实现不扎实，风险大于收益。
- **上游依赖**：291/623 个模型处于 discontinued，alibaba 上游 Key 12 个全部校验失败，说明上游可用性波动大。需要把"模型目录与上游健康"当成持续运营工作，而不是一次性配置。

### 3.2 功能完整性（对照企业客户期望）

| 能力 | 现状 | 备注 |
|---|---|---|
| 企业/成员/组/Key 管理 | ✅ | `enterprise_members.go` 2.6k 行，功能多但偏大 |
| 配额/信用/预付费 | ✅ | `quota_total/credit_total/prepaid_balance`，Intel 预付余额 $4,111 已在使用 |
| 对账后台 / 发票申请 | ✅ | `admin_reconciliation.go`、`invoice_requests` |
| 用量导出 CSV | ✅ | |
| Key 过期、RPM/TPM、模型白名单 | ✅ | |
| Key IP 白名单 | ❌ | 11-04 有设计草案，未实现 |
| SSO（SAML/OIDC）/ SCIM | ❌ | 代码里无 |
| MFA/TOTP | ❌ | 仅有邮件验证码；生产库 45 个用户中 `email_verified=true` 仅 1 个 |
| 客户侧用量/余额告警（webhook/邮件） | ❌ | 未发现 low_balance / threshold 类实现 |
| 失败请求可见性（客户可查自己的失败日志） | ❌ | 失败请求不入库（§1.3-4） |
| 客户可配置的数据留存策略（零留存/保留期/导出/删除） | ❌ | retention 为全局开关（§3.3） |
| 合规文档（DPA、子处理方清单、SOC2/ISO 路线） | ❌ | docs 中无 |

### 3.3 审计 / 数据留存 / 隐私（重点）

#### 3.3.1 证据链断裂：三套账用三个不同的 ID（P0）
- `usage_records.trace_id` 与 `proxy_request_audits.trace_id` **44,612 条里 0 条能对上**（SQL 全量验证）。
- retention 文件名使用的是 usage 那一侧的 trace_id（如 `f56e920e-…` 对应 usage 最新一条），而 audit 表是另一个 UUID。
- 结果：**无法从一条账单追到审计记录，也无法从审计追到原始请求/响应**。对账争议时只能靠时间戳 + 模型 + token 数模糊匹配。
- 两边的条数在近几日是吻合的（例如 11-05 audit 273 / usage 270），说明数据是同一批请求，只是 ID 没统一。

#### 3.3.2 审计内容缺失关键字段（P0）
- `proxy_request_audits.client_ip`：**44,612 / 44,612 为空**。
- `audit_logs.ip_address`：1,122 条里只有 **1 条**有值。原因：`audit.Logger.Log(...)` 的调用签名不带 IP/UA，只有 `gdpr.go`、`profile.go` 的 7 处手写 INSERT 带 IP。
- `usage_records.client_ip` 为空的仅 503 条（2.3%），这一路是好的，可以作为参考实现。
- 失败请求不记录（§1.3-4）。
- 企业审计 `enterprise_audit_log`：222/286 条 `action` 为空（用 `change_type` 字段代替），字段语义不统一，导出时会出现大量空 action。

#### 3.3.3 审计日志可被修改/软删（P1）
- `internal/routes/audit.go` 提供 super admin 对 `audit_logs` 的**软删除/恢复**接口，同表同库，无哈希链、无外部 WORM 副本。该操作本身会写一条 `audit_soft_deleted`，这是好的，但**该记录同样在同一张可被修改的表里**。
- 超管有 `ADMIN_SECRET`/DB 权限，且开发过程中 agent 直连 DB 的事实说明，**审计表在当前模型下不具备"防篡改"属性**。企业客户做合规审计时，这是常见的否决项。

#### 3.3.4 Proxy 的 payload retention（P0/P1）
这是**全文最大的风险点**，现状：

| 项 | 现状 |
|---|---|
| 开关 | 生产 `docker-compose.proxy.yml` 默认 `RETENTION_ENABLED=true`，所有租户的所有请求都留 |
| 内容 | 请求体完整落盘；**流式请求不保存响应体**（代码注释："response body is not accumulated in minimal version"）。而 agent 类流量绝大多数是流式 → 留存的是"只有问题没有答案"的残缺证据 |
| 存储 | 本机 Docker 卷，**明文 gzip JSON**，无加密；体积 **2.8 GB / 48,733 个文件**，与系统盘（Proxy 机 38G，已用 79%）同盘 |
| 保留期 | 配置 `RETENTION_MAX_AGE=2160h`（90 天） |
| **清理** | `DiskStore.Cleanup()` **全仓没有调用点**。实测 2026/07 目录仍有 **799 MB**（7/11 起，已超过 90 天上限）；2026/10 有 1,386 MB。保留期是"写在配置里但从未执行"的承诺 |
| 检索 | `Get(trace_id)` 通过 `filepath.Walk` 全目录扫描，O(N) |
| 租户隔离 | 文件按 `年/月/日/时/trace_id` 存放，**没有按租户/企业分目录或索引**。无法按企业导出、无法按企业删除、无法证明某企业的数据已被清除 |
| 访问控制 | 通过 `admin` 路由 + `ADMIN_SECRET`（共享静态密钥）即可读取任一租户的完整 prompt，**访问不记审计** |
| GDPR/删除 | Portal 的 `AnonymizeUser` 只处理 Portal DB，**不触及 Proxy 上的 payload 文件** |
| 备份 | 无。Proxy 机只有这一份 |
| 告警 | 无。磁盘 85% 才会触发 monitor 里的告警项，但告警 webhook 未配置（见 §3.5） |

这套实现的自述是"minimal / 让运维先验证采集管道，再决定要不要上 S3"。它作为**验证原型**合理，但已经被当成**生产数据**在跑：里面是客户的真实 prompt。这既是安全风险（明文 + 共享密钥可读），也是合规风险（承诺 90 天却没执行删除、无法按企业删除）。

#### 3.3.5 Portal 侧 retention（总体正常，有缺口）
- `RunRetentionCleanup` 已通过 `cmd/server/background.go` 注册为带分布式锁的每日任务（我最初怀疑未接线，复核后确认已接线）。
- 但规则表里**没有** `usage_records`、`proxy_request_audits`、`balance_transactions`、`enterprise_audit_log`。其中 `proxy_request_audits` 目前 44k 行/16 MB，无清理、无分区，会无限增长。
- `users.data_retention_days` 全部是默认 365，但代码中没有找到按用户/企业该值驱动的清理（仅读取、展示）。**这是一个"有字段、没生效"的功能**，不要在对外材料里宣称。
- GDPR：导出/延迟匿名化流程完整（`gdpr.go`、`runGDPRErasure`），生产库有 3 个用户触发过导出/删除。

#### 3.3.6 数据被硬删除后的审计影响
- `audit_logs` 中有 50 条 `key_deleted`，而 `api_keys` 里有 110 行带 `deleted_at`（软删），另有 4 个 Key 被硬删。说明业务路径是软删，**硬删来自直接操作 DB**。
- `api_keys` 中有 **91 行 `status='active'` 但 `deleted_at` 非空**：状态与软删标记不一致。需要确认所有读取路径（validate、列表、统计）都按 `deleted_at IS NULL` 过滤，否则可能出现"已删除的 Key 仍可用/仍被计入"的边缘情况。

### 3.4 安全

**做得好的（已核实）**
- Proxy→Portal webhook：HMAC-SHA256（`timestamp.body`）+ 5 分钟时间窗 + `hmac.Equal`；未配置密钥时拒绝（fail-closed）。
- `/api/keys/validate` 虽无需登录，但有按 IP 限流，并缓存正/负结果。
- 密码 bcrypt；登录限流（Redis 计数）；JWT `token_version` 可吊销；会话 Cookie `HttpOnly`；存在 CSRF token。
- 上游 Key 与用户 Key 均 AES-256-GCM 加密存储；API Key 校验使用 SHA-256 哈希。
- Postgres/Redis 仅绑定 127.0.0.1。
- Key/用户吊销已能向 Proxy 传播（10-20 修复）。
- 计费幂等有 Redis 去重 + DB 唯一索引。

**需要改进的**
1. **没有企业级身份能力**：无 SSO/SAML/OIDC、无 SCIM、无 MFA，无 Key IP 白名单。企业安全问卷（SIG/CAIQ）几乎必问。
2. **加密密钥治理**：`pkg/crypto` 自述"SHA-256 单轮无盐派生，非正式 KDF"；`ENCRYPTION_KEY` 在两台机器上相同、无版本号、无轮换机制。`key_revealed` 审计有 41 次，说明"可再次查看完整 Key"这一设计在被使用——明文 Key 可被解密还原，对客户而言是风险点（行业惯例是创建时只显示一次）。
3. **Webhook 防重放**：目前只有时间窗，无 nonce，10-20 评审选择"接受不改"。在 VPC 内网+幂等的前提下可接受，但应在文档里明确为已知风险。**计费幂等的第二层是 Redis 24h TTL 的 SetNX，Redis 出错时 fail-open**；DB 层的唯一约束是 `(trace_id, created_at)`，对同一 trace_id 不同时间戳的重放没有保护。建议把"是否已结算"落到 DB（例如 `UNIQUE(trace_id)` 的结算表）。
4. **凭据落盘**：`workspace/github/ghp.txt`（GitHub PAT，权限 664）、`workspace/openrouter/mgmt_key.txt`、`upstream_keys.md`、两把 SSH 私钥都以明文放在工作目录。HANDOVER.md 里的 PAT 已移除（`AGENTS.md` 里关于 HANDOVER 仍含 PAT 的描述已过时，应更新），但 `ghp.txt` 仍在。建议立即轮换 + 改用 SSM Parameter Store / Secrets Manager。
5. **共享静态管理密钥**：Proxy `/admin/*`（含读取 retention payload）只靠一个共享 `ADMIN_SECRET`，没有个人身份、没有操作审计。
6. **网络暴露**：Portal `0.0.0.0:3001`；**需要确认 SG 仅放行 Cloudflare 段/内网**（本次未验证）。
7. **邮箱验证**：45 个用户中仅 1 个 `email_verified`。如果注册流程本应强制验证，则存在绕过；如果是邀请制，则需要明确这一点。
8. **SSRF**：上游 `base_url`、media 下载等未发现统一的内网地址拦截（10-20 报告称 media SSRF 已修，本次未复核全部出站点）。仅管理员可配，风险中低。

### 3.5 可靠性与运维

| 项 | 现状 | 风险 |
|---|---|---|
| 部署形态 | Portal、Postgres(容器)、Redis 在同一台 EC2；Proxy 另一台 | 单点；Portal 挂掉后 Proxy 只能靠 5 分钟缓存 + 30 分钟降级缓存 |
| 备份 | 每日 03:00 `pg_dump`，保留 7 天，**仅在本机同一块盘** | 机器/磁盘故障 = 全丢；RPO 最长 24h |
| 备份范围 | 仅 Portal DB。`uploads/`（企业申请材料）、Proxy retention 卷、Redis 均未备份 | 企业资质文件、留存 payload 无副本 |
| 恢复演练 | 无记录 | 备份是否可用未知 |
| 监控 | `monitor.sh` 每 5 分钟检查 health/磁盘/容器 | **`ALERT_WEBHOOK_URL` 未配置**，目前只写日志，等于没有告警 |
| 磁盘 | Portal 81%，Proxy 79%（各 38 GB） | retention 无清理，Proxy 剩余约 8 GB；Postgres/Docker 与其共盘 |
| 失败可观测 | 失败请求不入库（§1.3） | 无法做 SLA 统计与事故复盘 |
| 部署 | 手工 rsync + docker compose build，无灰度/回滚脚本 | 变更风险靠人 |

说明：10-20 的两份报告已指出备份异地、告警、retention 清理三项，当时因"S3 未开通"暂缓。**一个月过去，这三项仍然全部开放，并且 retention 数据量增长了数倍**，建议不再等 S3，先做临时方案（见 §4）。

### 3.6 计费与对账（需要你确认的疑点）

已验证：
- 累计 `usage_records.total_cost` ≈ **$951.17**。
- 其中 `balance_deduction` 合计 $128.01；`gift/recharge/credit/subscription` 四个扣减列合计 ≈ 331.63（按 micro-USD 理解）。
- **有 4,177 条记录 total_cost>0 但所有扣减列均为 0，合计约 $480**，集中在 10 月（3,586 条 / $427），涉及用户 `xuecheng.li`（$329.9）、`perrin.peng`（$139.8）等；模型为 `claude-opus-5`（openrouter/tokenrouter）和 Bedrock 的 haiku/sonnet/opus。
- 这些用户属于企业账户，企业层面另有 `quota_used`、`credit_used`、`prepaid_balance`（例如 Aynbay `credit_used`=64.18 与 credit 扣减列合计吻合），**很可能是在企业层面结算而非用户层面**。

**未能验证**：这 $480 是否已经在企业层面被正确扣减。仅凭 SQL 无法闭合，因为记录层没有"本条记录由哪个账本（用户余额 / 企业额度 / 企业预付 / 信用）承担"的显式字段。

建议：
- 用现有 `admin_reconciliation` 跑一次 `usage_records` ↔ 企业 `quota_used/credit_used/prepaid_balance` 的月度核对，并保存结果。
- 在 `usage_records` 增加 `settled_ledger`（枚举）与 `settled_ledger_id`，让每条记录自解释。
- `gift_deduction` 等列声明为 `numeric(18,10)` 但存的是 micro-USD 整数值，**列定义与语义不一致**，容易在报表里误用 10^6 倍，建议统一为 bigint micro-USD。

### 3.7 可维护性

| 项 | 数据 | 评价 |
|---|---|---|
| Portal 代码 | 约 10.8 万行 Go（含测试），非测试 7.5 万行；167 个测试文件 | 测试基础好 |
| Proxy 代码 | 非测试 3.6 万行；86 个测试文件 | 同上 |
| 巨型文件 | `schema.go` 3,938；`handlers_chat.go` 3,403；`enterprise_members.go` 2,594；`models_crud.go` 1,823 | 单文件过大，评审和并行修改困难 |
| 迁移 | `schema.go`（幂等基线）+ 32 个 SQL 迁移 | 双轨可行，但 schema 与 migrations 需要持续对齐，已有 drift 检查脚本 |
| CI | 两个仓库均有 fast-gate（build/vet/lint/govulncheck/测试）+ Portal 回归（含 Playwright） | 良好 |
| 文档 | Portal `docs/` 187 个文件，`reports/` 53 个；HANDOVER.md 2,539 行 | **文档泛滥**，同主题多版本；新人（或新 agent）难以确认哪份有效 |
| 工程习惯 | agent 直连生产 DB 做测试/调试；`internal/web` 与 `internal/routes` 曾有重复业务逻辑（已有收敛规范） | **最大的可维护性风险是操作方式而非代码** |
| 配置漂移 | 两台机器各自 `.env`，靠脚本核对 2 个共享密钥 | 可接受，但缺少配置清单版本化 |

---

## 4. 重点问题与整改方案

优先级定义：P0 = 不处理则不应扩大商用；P1 = 1 个月内；P2 = 季度内。

### P0-1 统一 trace_id，打通 账单 ↔ 审计 ↔ 留存 证据链
- **方案**：Proxy 在请求入口生成唯一 `trace_id`（可复用 `X-Request-ID`），同一个值写入：usage webhook、audit 记录、retention 文件名/元数据、响应头 `X-Trace-Id`（返回给客户，便于客户报障）。
- **验证**：上线后跑 SQL：`usage_records` 与 `proxy_request_audits` 按 trace_id 关联率应 ≥ 99.9%。
- **工作量**：小到中（Proxy 侧改一处 ID 生成 + 3 个落点）。

### P0-2 补全审计字段：IP / UA / 失败请求
- Proxy audit 填 `client_ip`（`c.ClientIP()`，注意 Cloudflare 后需配置可信代理与 `CF-Connecting-IP`）。
- Portal `audit.Logger.Log` 增加 `ctx` 中取 IP/UA 的统一实现（中间件把 IP/UA 放入 `context`），避免每处手写 INSERT。
- **记录失败请求**：在 usage/audit 中以 `status=error|rate_limited|upstream_error` + `error_code` 落库，但不计费（`total_cost=0`）。这是 SLA 与对客户解释的基础。
- 统一 `enterprise_audit_log.action` 与 `change_type`，导出时只暴露一个。

### P0-3 Proxy retention 治理（立即止血 + 中期重做）
**立即（本周）**
1. 在 Proxy 启动时注册 `Cleanup()` 定时任务（每小时），并**先手工清理 90 天前数据**（7 月 799 MB）。同时给 retention 卷加容量上限（超过阈值停止写入并告警，而不是写满系统盘）。
2. 把 retention 迁出系统盘（独立 EBS 卷或挂载点）。
3. 访问 `/admin/retention/objects/*` 记审计（谁、何时、读了哪个 trace、哪个企业）。
4. 明确是否对所有租户默认开启 retention。**建议改为：默认关闭，按企业显式开启**（见下）。

**中期（1–2 个月）**
- 分层模型：
  - 元数据层（trace、token、模型、IP、状态）：永久/按合同保留，进 Portal DB 或对象存储 Parquet。
  - Payload 层（prompt/response）：**按企业配置**（零留存 / 7 / 30 / 90 天），按企业前缀存储。
- 存储改为 S3（SSE-KMS，按企业前缀 + 对象 tag 做 lifecycle 到期删除）；本地磁盘仅做短时缓冲。
- 流式响应：在 SSE 转发过程中累积并在结束时写入（设上限并标记截断），否则留存证据残缺。
- Portal 增加企业级"数据留存设置"页面：保留期、导出、一键删除（需异步任务，Proxy 提供按企业删除接口，并产出删除证明）。
- GDPR 匿名化联动 Proxy：`AnonymizeUser`/企业删除触发 Proxy 的按用户/企业删除。
- 在对外文档里写清楚：留存的是什么、保存在哪、保存多久、谁能访问。

### P0-4 运维底线：告警、异地备份、恢复演练
- **告警**：今天就配置 `ALERT_WEBHOOK_URL`（飞书/钉钉/Slack 任一），并增加三项检查：Proxy retention 目录体积/磁盘、备份文件是否当日生成、usage webhook 积压（Redis 队列长度/fallback 日志大小）。
- **异地备份（不等 S3 全面方案）**：最小方案是 `pg_dump` 后 `aws s3 cp` 到专用桶（SSE-KMS、版本控制、30 天生命周期、限制删除权限）；`uploads/` 同步；Proxy 的 retention 元数据同步。桶开通本身是一个小动作，建议不要再作为阻塞项。
- **恢复演练**：每月一次，把最近备份恢复到临时容器并跑一组校验 SQL（行数、最新 usage 时间、余额总和），记录结果。
- **监控 RPO/RTO 目标**：先写下来（例如 RPO 24h→1h，RTO 4h），再决定是否上 RDS/Aurora（已有 `aurora-postgresql-migration-guide.md`、`migration-assessment-202610301250.md`）。

### P1-1 保护账务与审计数据不被"直接操作 DB"破坏
这是本次最重要的流程性建议，直接对应你说的"agent 直接操作 DB、很多被硬删除"。
- **DB 角色分离**：应用使用受限角色；生产库的人工/agent 访问使用只读角色；写权限走审批后的临时角色。
- **对关键表禁止硬删除**：对 `usage_records`、`balance_transactions`、`audit_logs`、`orders`、`api_keys` 撤销 `DELETE`（或加触发器抛错）；软删除字段统一。
- **补外键或影子表**：`usage_records.user_api_key_id` 缺 FK 的原因是分区表，可改为在 Key 删除时写入"墓碑"表（`api_key_tombstones`：id、name、prefix、owner、deleted_at、deleted_by），保证历史账单可解释。
- **启用 `pgaudit`**（或至少 `log_statement=ddl,mod` 到独立日志）并把日志外送，这样即使应用层审计被改，也有 DB 层的第二证据。
- **测试数据隔离**：新建 staging 环境（独立 DB/Redis/Proxy），E2E/压测只在 staging 跑；生产库里的 `E2E-*` 企业、`loadtest-*` 用户需要标记为 `is_test` 并从对账/报表里排除，而不是硬删。
- **审计防篡改**：每日把前一天的 `audit_logs`/`usage_records` 摘要（行数 + 哈希链）写到 S3 对象（开启 Object Lock），可以低成本得到"事后可证明未被修改"的能力。可再加 `prev_hash` 字段做链式。
- **审计软删权限**：超管软删审计需要二人审批或直接移除该功能；至少把"软删/恢复"同步到外部存储。

### P1-2 计费对账闭环
- 记录层加 `settled_ledger` / `settled_ledger_id`（§3.6）。
- 跑一次 10 月全量对账，处理 $480 无扣减记录的结论（若是企业层结算则文档化；若有漏扣则补扣并写 `admin_adjustment`）。
- 结算幂等落 DB（独立结算表，`UNIQUE(trace_id)`），Redis 去重仅作加速。
- 统一 `*_deduction` 列类型。

### P1-3 企业身份与访问控制
按企业客户需求排序：
1. Key IP 白名单（已有设计草案，工作量最小）。
2. TOTP MFA（管理员强制；企业可选强制）。
3. OIDC SSO（先支持 Google Workspace / Azure AD / 飞书），再考虑 SAML 与 SCIM。
4. API Key 创建后只显示一次；移除/限制"再次查看完整 Key"（现有 41 次 `key_revealed`）。
5. 管理员操作二次确认与操作审计。
6. 加密密钥：引入 `key_version`，用 HKDF/随机 salt，支持轮换；长期迁移到 KMS 信封加密。

### P1-4 凭据与网络
- 立即轮换 `workspace/github/ghp.txt` 中的 PAT 与 `openrouter/mgmt_key.txt`、`upstream_keys.md` 涉及的 Key，把凭据迁入 Secrets Manager，把文件移出工作目录。
- 更新 `AGENTS.md`（"HANDOVER.md 含 PAT"已过时）。
- 确认 SG：Portal 3001 仅 Cloudflare 回源 IP / 内网；Proxy 8080 仅 Cloudflare/内网；SSH 限制来源。
- 对 Cloudflare 回源使用 Authenticated Origin Pulls 或回源密钥头。

### P2
- 拆分巨型文件（`handlers_chat.go` 按协议拆；`enterprise_members.go` 按子域拆）。
- 文档治理：`docs/` 建 `INDEX.md`，把过期文档标注 `OUTDATED/HISTORICAL`（现有规范已要求，但需执行）。
- 部署：加蓝绿或至少"上一版本镜像一键回滚"脚本。
- Proxy/Portal 多实例与 Redis 外部化（限流计数目前是单实例本地计数，10-20 报告已指出）。
- 上游健康与模型目录：为 discontinued/paused 做定期清理和客户公告（已有 announcement 机制）。
- `users.data_retention_days` 要么落地要么移除。

### 建议执行顺序（4 周）
| 周 | 内容 |
|---|---|
| 第 1 周 | 配置告警 webhook；Proxy `Cleanup()` 接线 + 清理 7 月数据；retention 迁盘；轮换 PAT/上游 Key；确认 SG；S3 备份最小方案上线 |
| 第 2 周 | trace_id 统一；audit 补 IP/UA；失败请求落库；DB 角色分离 + 撤销关键表 DELETE；staging 环境 |
| 第 3 周 | 对账闭环（`settled_ledger`、10 月复核）；墓碑表；pgaudit；每日摘要上传（Object Lock）；恢复演练 |
| 第 4 周 | retention 分层方案评审与实现启动（企业级配置、S3、流式响应）；Key IP 白名单；MFA 设计 |

---

## 5. 与竞品对比及建议

> 说明：以下基于公开资料与我已有的知识整理，**未逐项联网核对最新版本**。用于定位判断，写进销售材料前请复核。

### 5.1 对比对象
- **OpenRouter**：面向开发者/团队的聚合与分发，信用点预付，按 token 加价（平台费）；支持 BYOK、Provider 路由与回退、按请求的数据策略（如 `data_collection: deny`、ZDR 路由）、Activity 日志与用量导出、组织与成员、Guardrails（预算/模型/供应商限制）、Prompt caching。企业档提供 SSO 等。
- **LiteLLM（自建 / Enterprise）**：自托管代理，虚拟 Key、团队/用户预算、速率限制、成本追踪、审计日志、SSO（企业版）、日志回调（S3/Langfuse 等）。
- **Portkey / Helicone / Kong AI Gateway / Cloudflare AI Gateway**：偏观测与治理（日志、缓存、护栏、预算、PII 脱敏、SSO/RBAC）。
- **云厂商原生（Bedrock、Azure OpenAI + APIM）**：IAM/私网/合规认证齐全，但只覆盖自家模型。
- **国内同类（New API/One API 系）**：令牌分发 + 额度 + 渠道管理，企业能力、审计较弱。

### 5.2 能力对照（定性）

| 能力 | WOODGIT 现状 | OpenRouter | LiteLLM/Portkey 类 | 评价 |
|---|---|---|---|---|
| 多上游聚合/回退 | ✅（含 Bedrock、国内渠道） | ✅ 强 | ✅ | 持平；模型目录运营是持续成本 |
| 企业组织/成员/配额/信用/预付 | ✅ 较完整 | 基础组织 + 信用 | 团队预算 | **优势** |
| 对账/发票/多币种（含人民币、支付宝） | ✅ | 信用卡/加密货币为主 | 弱 | **优势**（面向国内企业） |
| 缓存计费、推理 token、分档计价 | ✅ | ✅ | 部分 | 持平 |
| 用量导出/Activity | ✅ CSV | ✅ | ✅ | 持平，但缺失败请求 |
| **按请求/按组织的数据策略（零留存、不用于训练）** | ❌ 全局开关 | ✅ 有 | 视部署 | **差距大** |
| **审计日志完整性、防篡改、SIEM 导出** | ❌ | 基础 | 企业版有 | **差距大** |
| 失败请求日志与成功率统计 | ❌ | ✅ | ✅ | **差距大** |
| SSO/SCIM/MFA | ❌ | 企业档 | 企业版 | 差距 |
| Key IP 白名单/细粒度权限 | ❌ | 部分 | ✅ | 差距 |
| 预算告警/Guardrails（模型/供应商/内容） | 部分（配额） | ✅ | ✅ | 差距 |
| 合规认证（SOC2 等）、DPA | ❌ | 有 | 视部署 | 差距（需路线图） |
| 私有化/专属部署 | 有基础（自建） | ❌ | ✅ 自托管 | **可作为差异化** |

### 5.3 建议的产品策略
1. **定位成"企业的 token 财务与治理层"，而不是"又一个转发"**。转发能力各家趋同；能让企业采购/财务/合规认可的是：**可对账、可审计、可控的数据留存**。因此 §4 的 P0 项就是产品本身，而不是附属工程。
2. **把"数据留存与隐私"做成企业级可选项**（对标 OpenRouter 的 ZDR 思路但落到企业维度）：
   - 三档：零留存（只留元数据）/ 标准（30 天 payload）/ 合规（90–365 天，加密 + 导出）。
   - 每个企业可配置，且在合同/DPA 中写明；Portal 展示当前策略与"最近一次到期删除"证明。
   - 对"是否被上游用于训练/上游是否留存"建立**模型×上游**的数据策略标签，让企业可选"仅限零留存上游"。这是 OpenRouter 已验证有效的卖点。
3. **对账作为核心卖点深化**：月度对账单（按企业/部门/项目/Key/模型），可导出并带摘要哈希；与上游账单（Bedrock/OpenRouter/TokenRouter）自动对比差异，向客户展示"我们的成本与你的用量一致"。
4. **成本归集维度**：支持 project / cost-center / tag（`project_name` 字段已有雏形），让客户财务按部门分摊。
5. **企业治理**：模型白名单 + 预算上限 + 超限策略（阻断/降级/仅通知）+ 余额/预算阈值告警（邮件/webhook）。
6. **差异化的部署选项**：提供"专属 Proxy（企业自己的 VPC 或独立实例）+ 共享控制面"，payload 留在客户侧，控制面只收元数据。对大企业客户是强卖点，且与你们现有的"控制面/数据面分离"架构天然匹配。
7. **合规路线**：先出 DPA 模板、子处理方清单、数据流图、安全白皮书；再做 SOC2 Type I 的差距评估。短期内最现实的是"安全问卷可答、隐私条款可签"。
8. **SLA 的口径先定义再承诺**：当前 p95≈19s、p99≈75s，主要受长输出影响。建议按"首 token 延迟 TTFT"与"成功率"定义 SLA，而不是端到端延迟；并先具备失败请求统计能力。
9. **定价**：OpenRouter 的平台费模式在企业场景不一定适用。可考虑"平台年费 + 用量加价/阶梯折扣 + 专属部署附加费"，具体价格请结合成本与客户谈判。

---

## 6. 风险登记表（摘要）

| # | 风险 | 级别 | 证据 | 建议 |
|---|---|---|---|---|
| 1 | 账单/审计/留存无法按 trace_id 关联 | P0 | 44,612 条 audit 与 usage 0 匹配 | §4 P0-1 |
| 2 | 审计缺 IP、缺失败请求 | P0 | audit client_ip 100% 空；audit_logs IP 1/1122；失败 0 | §4 P0-2 |
| 3 | Retention 明文、无清理、无租户隔离、流式无响应 | P0 | 2.8 GB / 48.7k 文件；7 月 799 MB 超期；`Cleanup()` 无调用 | §4 P0-3 |
| 4 | 告警未配置、备份无异地、无恢复演练 | P0 | 无 `ALERT_WEBHOOK_URL`；备份仅本机 | §4 P0-4 |
| 5 | 生产数据可被直连 DB 硬删/修改，审计可被超管软删 | P1 | 8,301 条 usage 引用已删 Key；`audit.go` 软删接口 | §4 P1-1 |
| 6 | 约 $480 usage 无记录级扣减归属 | P1（待确认） | 4,177 条无扣减列 | §4 P1-2 |
| 7 | 缺 SSO/MFA/IP 白名单；Key 可再次查看；加密密钥无轮换 | P1 | 代码检索无实现；41 次 `key_revealed` | §4 P1-3 |
| 8 | 明文凭据落盘 | P1 | `workspace/github/ghp.txt` 等 | §4 P1-4 |
| 9 | 测试数据与生产混合 | P1 | 4/8 企业为 E2E 测试；43 个测试痕迹 Key | staging + `is_test` |
| 10 | 巨型文件、文档泛滥 | P2 | 单文件 3.9k 行；187 个 docs | §4 P2 |

---

## 7. 附：本次使用的主要查询（可复现）

```sql
-- 规模
select count(*) from users; select count(*) from enterprises; select count(*) from api_keys;
-- 月度用量
select to_char(created_at,'YYYY-MM'),count(*),round(sum(total_cost)/1e6,2) from usage_records group by 1 order by 1;
-- 状态分布（仅成功被记录）
select status,count(*) from usage_records group by 1;
select status,count(*) from proxy_request_audits group by 1;
-- trace_id 关联率
select count(*) from proxy_request_audits p where not exists (select 1 from usage_records u where u.trace_id=p.trace_id);
-- 客户端 IP 缺失
select count(*) filter (where client_ip is null or client_ip=''), count(*) from proxy_request_audits;
select count(*), count(ip_address) from audit_logs;
-- 引用已删除 Key 的用量
select count(*), round(sum(total_cost)/1e6,2) from usage_records u
 where user_api_key_id is not null and not exists (select 1 from api_keys k where k.id=u.user_api_key_id);
-- 无扣减归属的记录
select count(*), round(sum(total_cost)/1e6,2) from usage_records
 where total_cost>0 and balance_deduction=0 and credit_deduction=0 and subscription_deduction=0
   and gift_deduction=0 and recharge_deduction=0;
```

Proxy retention：在 Proxy EC2 上以 `sudo` 对 `/var/lib/docker/volumes/ai-proxy-go_proxy-retention-data/_data` 做目录体积统计（7 月 799 MB、10 月 1,386 MB、合计 2.8 GB、48,733 文件）；`grep -rn "Cleanup()" ai-proxy-go` 仅有定义无调用。

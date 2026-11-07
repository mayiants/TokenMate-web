# 合规就绪评估:SOC 2 与 DPA / GDPR

Status: DRAFT
Date: 2026-11-06
Scope: ai-portal-go(控制面)+ ai-proxy-go(数据面)+ 两台 AWS EC2(Portal 与 Proxy,区域 ap-southeast-1)
Method: 代码与生产库只读核查 + 本次 session 的实测。**没有 AWS 控制台/CLI 权限**,凡涉及 AWS 配置的条目均标注"未核实"。
Not legal advice: DPA 与对外承诺类文本需法务审阅。

## 0. 结论

| 目标 | 现状 | 判断 |
|---|---|---|
| DPA(与企业客户签署) | 技术底座已具备(按企业留存、导出/删除、审计、加密存储);文本和子处理方清单此前缺失 | **可在 1–2 周内对外提供**(本次已出草案,见 §5、`legal/`) |
| SOC 2 Type I(时点) | 技术控制约 60% 已具备;**流程性证据几乎为零**(政策、变更审批、访问评审、供应商评估、事件演练) | 8–12 周可达到审计条件 |
| SOC 2 Type II(区间,3–12 个月) | 需先有 Type I 的控制并持续留证 | Type I 后再观察 3–6 个月 |

最影响通过的三项,都是**运维/流程而非代码**:
1. 备份只在同一台机器、同一块盘、7 天、未加密、无恢复演练(`backup-db.sh`),且含 PII。
2. 管理员账号未强制 MFA(2 个 super_admin、6 个企业管理员,TOTP 启用数 0;`mfa_required_roles` 为空)。
3. 没有成文的安全政策、事件响应、变更与访问评审流程,CI 之外没有证据留存。

## 1. 角色定位(DPA 的前提)

- **客户内容**(prompt/response、企业成员发出的请求):平台是**处理者(processor)**,客户是控制者。
- **账号与计费数据**(邮箱、姓名、发票抬头、支付记录):平台是**控制者**。
- **上游模型供应商**:当客户内容经平台转发时,各供应商是平台的**子处理者(sub-processor)**。数据库 `providers` 中有 AWS Bedrock(多区域)、OpenAI、Anthropic、Google、Microsoft、DeepSeek、Moonshot、Zhipu、MiniMax、ByteDance、Alibaba、Tencent、Baidu、Huawei、Mistral、Cohere、Groq、Perplexity、xAI、Meta、Openrouter、Tokenrouter、Approaching(approach)、Laozhang、Zimao、Xiaomi 等。**完整清单与逐家数据流向见 `legal/subprocessors-202611062215.md`**,这是 DPA 最需要的附件,也是目前最大的对外披露缺口。

## 2. SOC 2 信任服务标准映射

范围建议:**Security(必选)+ Availability + Confidentiality**。Privacy 与 Processing Integrity 暂不纳入,以缩短首次审计周期。

图例:✅ 已具备 / 🟡 部分 / ❌ 缺失 / ❓ 未核实

### CC1–CC2 控制环境与沟通
| 控制 | 现状 |
|---|---|
| 成文的信息安全政策、行为准则、角色职责 | ❌ |
| 董事会/管理层监督与风险评估记录 | ❌ |
| 对客户的安全与隐私披露(信任中心/安全白皮书) | ❌(本文档可作为内部底稿) |

### CC3 风险评估
| 控制 | 现状 |
|---|---|
| 年度风险评估与风险登记册 | ❌;已有 `commercial-readiness-review-report-202611061240.md` 可作为首版输入 |
| 欺诈风险(计费、额度)评估 | 🟡 账单与对账有审计、哈希链,见 CC7 |

### CC4 监控
| 控制 | 现状 |
|---|---|
| 持续监控 | 🟡 `monitor.sh` 每 5 分钟:健康、磁盘、备份新鲜度、Proxy 磁盘、usage 对账(本次新增)。**告警 webhook 未配置**,告警目前只写日志 |
| 内部审计/控制自测 | ❌ |

### CC5–CC6 逻辑与物理访问(重点)
| 控制 | 现状 |
|---|---|
| 唯一身份、最小权限(RBAC) | ✅ super_admin / system_admin / enterprise_admin / user |
| 强认证:MFA | 🟡 **已实现**(TOTP、恢复码、锁定、企业级 `require_mfa`、`mfa_required_roles`),但**默认不强制**,管理员 0/8 启用 |
| SSO | ✅ 每企业 OIDC(PKCE、SSRF 防护、JIT、`enforce_sso`),**默认关闭,尚未对真实 IdP 验证** |
| 登录防暴力破解 | ✅ 同邮箱+IP 5 分钟内 5 次失败锁 15 分钟(`internal/web/auth.go`) |
| 会话管理 | 🟡 JWT 默认 7 天(`JWT_EXPIRES`);对管理员偏长 |
| 密码存储 | ✅ bcrypt;最小长度有校验;复杂度/泄露库检查未确认 |
| API Key 管理 | ✅ 哈希存储;上游 Key AES 加密(`ENCRYPTION_KEY`);**无密钥轮换/版本机制** |
| 访问评审(定期复核谁有什么权限) | ❌ 无流程、无报表 |
| 入职/离职访问开通与回收 | ❌ 无流程 |
| 服务器访问(SSH、sudo、堡垒) | ❓ 共用一把 `portal-sg.pem` 跨机访问;无个人账号审计;未核实 |
| 网络隔离、安全组 | ❓ 3001 仅应对 Proxy 开放,**待用户确认**;Proxy→Portal 为 VPC 内 `http`(Proxy 每次同步会 warn) |
| 静态加密(EBS/备份) | ❓ EBS 加密未核实;**备份文件未加密**(`gzip` 明文 SQL) |
| 传输加密 | ✅ 外部 HTTPS + HSTS;🟡 VPC 内 Portal↔Proxy 为 http;Postgres `ssl=off`(容器内网) |
| 机密管理 | 🟡 `.env` 文件(每机一份,不入库);无专用密钥库,无轮换流程;`workspace/` 中存在 SSH 私钥与 `.env` 副本 |

### CC7 系统运营与事件管理
| 控制 | 现状 |
|---|---|
| 审计日志 | ✅ 本 session 新增:哈希链 + 更新/删除/截断拦截 + 每日 HMAC 锚点 + 校验 API/CLI |
| 审计日志**可读性与追溯** | ✅ trace_id 贯通 Proxy 审计 / usage / 留存(本次已实测 100% 关联);失败请求有审计 |
| 异常检测与告警 | 🟡 见 CC4 |
| 事件响应计划、分级、复盘 | ❌ 没有成文计划;有 `docs/reports/*postmortem*` 的事后记录习惯,可复用 |
| 漏洞管理 | 🟡 CI 有 `govulncheck` 与 golangci-lint;**无 Dependabot、无镜像扫描、无 SBOM、无定期渗透测试** |

### CC8 变更管理
| 控制 | 现状 |
|---|---|
| 版本控制、CI 门禁 | ✅ GitHub + 门禁(build/vet/lint/单测/schema/i18n);Portal 另有回归 + Playwright |
| **变更审批/同行评审** | ❌ 直接推 `main`,无分支保护与 PR 评审(审计员会重点看) |
| 部署可追溯与回滚 | ✅ `deploy-portal-step.sh`(镜像 tag `rollback-pre-*`)、迁移只增不减;🟡 Proxy 靠 rsync + 现场构建,无镜像版本留存 |
| 测试与生产分离 | 🟡 DB 测试已强制 `*test*` 库;生产无独立预发环境 |

### CC9 风险缓解 / 供应商管理
| 控制 | 现状 |
|---|---|
| 供应商清单与评估 | ❌ 无;见 `legal/subprocessors`(本次建立首版) |
| 业务连续性 | 见 A1 |

### A1 可用性
| 控制 | 现状 |
|---|---|
| 容量与监控 | 🟡 磁盘/健康监控有;容量规划无 |
| **备份与恢复** | ❌ 同机同盘 7 天,**从未演练恢复**,无 RPO/RTO 声明 |
| 灾备/多可用区 | ❌ 单机单实例(Portal、Proxy 各一台) |
| SLA 与状态页 | ❌ |

### C1 机密性
| 控制 | 现状 |
|---|---|
| 数据分类 | ❌ 未成文;实际分为:客户内容(留存文件)、账号 PII、计费数据、上游密钥 |
| 留存与销毁 | ✅ **本 session 完成**:按企业留存(off/metadata/standard + 天数)、Proxy 以**当前**策略为准清理、平台默认统一 90 天、删除申请 90 天后匿名化;🟡 无"销毁证明"导出 |
| 租户隔离 | ✅ 留存目录按企业隔离(`ent/<id>/`)、路径校验;🟡 数据库为共享表 + 应用层隔离 |

## 3. DPA / GDPR 技术对照

| 要求 | 现状 |
|---|---|
| 目的限制与指令处理 | ✅ 平台只按客户请求转发;🟡 需在 DPA 中写明"不用于训练" ——**须与各上游供应商的条款一致后才能承诺**(见 subprocessors 中"训练政策"一列,待逐家核实) |
| 数据主体权利:访问/导出 | ✅ `/gdpr/export`、全量导出请求流程 |
| 数据主体权利:删除 | ✅ 删除申请 → 立即停用 → `gdpr_retention_days`(90)后匿名化;管理员可立即匿名化 |
| 留存期限可配置 | ✅ 按企业 1–365 天 + 平台默认 90 |
| 安全措施(Art.32) | 🟡 见 §2;**备份、MFA、密钥轮换是三项短板** |
| 子处理者披露与变更通知 | ❌→🟡 本次建立清单;**变更通知机制**(提前 30 天通知)未实现 |
| 数据泄露通知(72h) | ❌ 无流程;审计哈希链为取证提供基础 |
| 跨境传输 | ❌ 平台在新加坡区,上游供应商分布美/中/欧;需 SCC 或等效机制,并在 DPA 中披露**路由是否可限定区域**(平台目前按模型路由,未提供"仅限某区域"选项) |
| 记录处理活动(Art.30) | ❌ 未建;可由本文档 §1 与 subprocessors 派生 |
| DPIA | ❌ 按需 |

## 4. 差距清单与优先级

P0 = 阻塞签 DPA / 阻塞 Type I;P1 = Type I 前完成;P2 = Type II 期间持续。

| # | 差距 | 级别 | 做法 | 谁来做 | 本次状态 |
|---|---|---|---|---|---|
| 1 | 备份:异地、加密、可恢复、演练 | P0 | 夜间 `pg_dump` → 客户端加密(age/KMS)→ S3(版本化 + 生命周期 + 跨区复制);季度恢复演练并留记录;声明 RPO 24h / RTO 4h | 需 AWS 权限 | ❌ 待用户 |
| 2 | 管理员强制 MFA | P0 | `mfa_required_roles=super_admin,system_admin`;先让 2 个 super_admin 完成绑定再生效,否则会被锁在门外 | 用户确认时间点 | ❌ 待用户 |
| 3 | 子处理者清单 + DPA 文本 | P0 | 见 `legal/` | 法务审阅 | ✅ 草案已出 |
| 4 | 成文政策集 | P0/P1 | 信息安全、访问控制、变更管理、事件响应、备份与恢复、数据分类与留存、供应商管理、可接受使用(模板见 Vanta/Drata 或自写) | 管理层签发 | ❌ |
| 5 | 变更管理证据 | P1 | GitHub 分支保护:`main` 必须 PR + 1 人评审 + 状态检查通过;提交与评审即为证据 | 用户(仓库设置) | ❌ |
| 6 | 依赖与镜像漏洞 | P1 | Dependabot(gomod/actions/docker)、镜像扫描(trivy)、SBOM(syft)进 CI | 我 | ✅ Dependabot 配置已加;trivy/SBOM 待做 |
| 7 | 密钥轮换 | P1 | `ENCRYPTION_KEY` 版本化(`kid` 前缀 + 双读单写),`PROXY_SYNC_SECRET`/`JWT_SECRET` 轮换手册 | 我 | 设计待做 |
| 8 | 管理员会话缩短与设备管理 | P1 | 管理员 JWT ≤ 8h + 刷新;空闲超时 | 我 | 待做 |
| 9 | **预付/信用额度的"直接编辑"无台账** | P1 | admin 企业编辑表单可直接改 `prepaid_balance`/`credit_total`,且**无审计行**——这正是 Intel 余额当初无台账的原因。改为:表单只读,变动走 `topup` 或带必填原因的 `adjustment`,并写入哈希链 | 我 | **本次发现**,待你确认后做 |
| 10 | 访问评审与离职回收 | P1 | 每季度导出"谁有什么角色/Key/服务器权限"并签字;SSH 改个人账号 + 会话审计(SSM Session Manager) | 流程 + AWS | ❌ |
| 11 | 事件响应计划 + 桌面演练 | P1 | 分级、联系人、通知客户 72h 模板;演练一次并留记录 | 管理层 | ❌ |
| 12 | 告警 webhook | P1 | 提供 URL 即可,脚本已就绪 | 用户 | ⏸ |
| 13 | 3001 安全组与 VPC 内传输 | P1 | 确认 SG 仅放行 Proxy;长期可改为内网 TLS(私有 CA)或 mTLS | 用户 + 我 | ⏸ |
| 14 | EBS 加密、IMDSv2 强制、CloudTrail/GuardDuty、S3 公共访问阻断 | P1 | AWS 配置核查并留截图/导出 | 需 AWS 权限 | ❓ |
| 15 | 渗透测试(第三方)| P1/P2 | Type I 之前或之中做一次,出报告 | 采购 | ❌ |
| 16 | SSO 对真实 IdP 验证 | P2 | 与首个要求 SSO 的客户联调 | 客户配合 | ❌ |
| 17 | 区域限定路由("仅限 EU/US 供应商")| P2 | 路由层增加企业级 `allowed_regions`,作为 DPA 的可售点 | 我 | 设计待做 |
| 18 | 子处理者变更通知机制 | P2 | 邮件订阅 + `subprocessors` 页面版本化 | 我 | 待做 |

## 5. 本次已交付

- 本文档。
- `docs/legal/dpa-template-202611062215.md`:DPA 草案(中英对照要点 + 附件 A 处理说明、附件 B 安全措施、附件 C 子处理者)。**需法务审阅**,其中 `[ ]` 括号处是商务/法律决定项。
- `docs/legal/subprocessors-202611062215.md`:子处理者清单首版(以 `providers` 表为准,逐家核实项标记为 TODO)。
- 两个仓库的 `.github/dependabot.yml`。
- 此前已落地的技术控制(与本评估直接相关):审计哈希链与锚点、失败请求审计、trace_id 贯通、按企业留存且以当前策略为准、统一 90 天默认、TOTP MFA、OIDC SSO、usage/审计对账告警。

## 6. 建议的 12 周路线

| 周 | 事项 |
|---|---|
| 1 | 你确认 §4 的 #1、#2、#5、#12、#13;法务审阅 DPA;我做 #9(余额直改台账)、#6(trivy/SBOM)、#7 设计 |
| 2–3 | 备份上线并做首次恢复演练;管理员 MFA 生效;分支保护;政策集定稿 |
| 4–6 | 密钥轮换、管理员会话、事件响应演练、访问评审首轮;选型合规自动化平台(Vanta/Drata/Secureframe)用于证据采集 |
| 7–8 | 渗透测试 + 整改;AWS 配置核查;审计机构选型与范围确认 |
| 9–12 | Type I 审计;同时开始 Type II 观察期 |

## 7. 风险与边界

- 我无法替代审计机构或法务;本文档用于**对准备工作排序**。
- "不用于模型训练"之类对客户的承诺,必须先逐家核实上游供应商条款,否则不要写进 DPA。
- AWS 侧控制全部"未核实"。拿到只读权限(`SecurityAudit` 策略即可)后,我可以把 §2 的 ❓ 项逐一核实并导出证据。

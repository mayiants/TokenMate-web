# 数据备份与跨版本迁移方案

> **Status:** Phase 1 COMPLETE, Phase 2-3 partially implemented  
> 生成时间：2026-07-09 18:43 UTC  
> 适用分支：`main`  
> 背景：项目即将上线，预期上线初期需要频繁 debug / fix / 重新编译 / 改 schema / 改 seed。需要一套既能回滚、又能把数据带进新版本的机制。  
> 实施状态：Phase 1（migrations 框架 + schema_migrations 表）✅ 已上线（8 个 migration 文件）；Phase 2（export/import CLI）未做（改用 pg_dump cron 替代）；Phase 3（pg_dump 自动快照）✅ 已通过 cron job 实现（每日 3:00 UTC 本地备份，保留 7 天）。

---

## 1. 目标与原则

- **可回滚**：任意一次 hotfix 改坏数据，能在分钟级恢复到发布前一刻。
- **可演进**：schema 加 / 删 / 改列、拆表、回填都能以可重复、可审查的方式落到生产。
- **可移植**：能把业务数据从旧版本导出，导入到 schema 已经变化的新版本。
- **可审查**：所有变更都进 git，拒绝线上"手动 ALTER / 手动 UPDATE"。

核心思路：**把"灾难恢复""schema 演进""业务数据迁移"三件事分开**，不要用同一个工具（例如单纯 `pg_dump`）硬扛所有场景。

---

## 2. 现状盘点

### 2.1 Schema 管理方式

- `internal/db/schema.go` 的 `DB.InitSchema()` 在每次启动幂等执行，全部是 `CREATE TABLE IF NOT EXISTS` / `ADD COLUMN IF NOT EXISTS` —— **只增不减**。
- `internal/db/migrate.go` 是一个**已写好但未启用**的版本化迁移 runner：
  - 维护 `schema_migrations(version PK, applied_at)` 表。
  - 从目录加载 `*.sql`，按 `version` 排序，逐个事务执行，记录已应用版本。
- `migrations/` 目录目前是**空占位**（`migrations/README.md` 写明：Pre-Production 用 `InitSchema`，量产后切到版本化迁移）。
- CI 用 `scripts/check_schema_vs_code.sh` 静态校验 Go 源码里的 SQL 引用与 `schema.go` 一致。

### 2.2 Seed 数据

`internal/db/seed.go` 写入：providers、subscription_plans、user_groups、system_settings、默认 group、welcome voucher、model_pricing_rules fallback 等。

- 全部是 `ON CONFLICT (id) DO NOTHING` 或 `WHERE NOT EXISTS` —— **幂等，但只补不更新**。
- 后果：新增 seed 会补，但**修改 / 删除已存在的 seed 行不会被同步**（例如改套餐价格不会回写到已上线环境）。

### 2.3 导出 / 导入能力

| 类型 | 端点 | 性质 |
|---|---|---|
| 观测数据 CSV | `GET /api/billing/{realtime,monthly,requests,recharge-records}/export`、`/api/orders/export`、`/api/admin/upstream-usage/export`、`/api/admin/provider-health/export`、`/api/admin/audit-logs/export`、`/api/enterprise/usage/export`、`/api/enterprise/audit/export` | 只读导出，**不可导入** |
| Keypool | `POST /api/admin/keypool/export`、`POST /api/admin/keypool/import` | 唯一成对的导出 + 导入 |
| 个人数据 JSON | `GET /api/gdpr/export` | **单用户**，不是全量 |

详见 `docs/import-export-spec-202606080634.md`。

### 2.4 核心表（30+）

users, providers, subscription_plans, enterprises, user_groups, api_keys, upstream_keys, service_models, model_pricing_rules, user_subscriptions, enterprise_members, audit_logs, proxy_heartbeats, proxy_instances, usage_records（**按月 RANGE 分区**）, orders, invoices, checkin_records, system_settings, user_devices, verification_codes, enterprise_invitations, payment_gateways, payment_config, subscription_purchases, balance_transactions, exchange_rates, announcements, announcement_reads, vouchers, voucher_uses, provider_health_stats, provider_health_stats_model, model_group_health_stats, upstream_key_usage, model_provider_routing, enterprise_audit_log, enterprise_member_role_log …

---

## 3. 痛点对应的空白

1. **没有"改 / 删列"的迁移机制**：现在只能加列，不能安全地重命名、改类型、删列、拆表、回填。
2. **没有"全量业务数据 → 新版本"的导入通道**：GDPR 是单用户级、CSV 是观测数据，主数据（users / providers / plans / vouchers / system_settings / models / routing）无法导出再导入。
3. **seed 改了不会回写**：`DO NOTHING` 让已上线环境的 seed 永远停在第一次写入的版本。
4. **`usage_records` 是分区表**：备份 / 导入必须处理分区与 default 分区。

---

## 4. 方案：四层防护

### L0 — 灾难恢复：`pg_dump` 必须保留

> "不想简单 pg_dump，因为 schema 会变" —— 这个担心成立，但 `pg_dump` 不该是"导入新版本"的工具，而该是**可回滚到任意时刻的最后保险**。一旦某次 fix 把数据改坏，唯一能 100% 还原的就是物理备份。

- 频率：每天 1 份 + 每次发布前 1 份。
- 格式：`pg_dump -Fc`（custom 格式，支持并行 restore、按表 restore）。
- 保留：最近 N 份（建议 14 天 + 每次发布的快照长期保留），文件名带 commit / 版本。
- 还原：始终 restore 到**新库名**验证后再切换，不要原地 restore。
- 注意：还原后仍需执行目标版本的 `migrate`，因为 snapshot 是旧 schema。

### L1 — 跨版本 Schema：启用版本化 migrations（最关键）

`migrate.go` 已就绪，缺的是启动开关与 baseline。上线前一次性做完：

1. 从干净库跑 `InitSchema()`，`pg_dump --schema-only` 生成 `migrations/0001_baseline.sql`，冻结为起点。
2. `cmd/server/bootstrap.go` 把无条件的 `InitSchema()` 换成 `Migrate(migrationsDir)`；`InitSchema()` 仅保留给全新空库 / 测试使用。
3. **立规矩**：此后所有 schema 改动（加 / 删 / 改列、回填）都写成 `migrations/NNNN_xxx.up.sql`，**禁止**再改 `schema.go` 来线上加列。`schema.go` 只作为新库初始化与静态校验的真相源。
4. 每个 migration 把**数据回填**也写进去（如拆表时的 `INSERT ... SELECT`），这样"导入新版本"就是 `migrate up`，天然带数据。
5. 静态校验 `scripts/check_schema_vs_code.sh` 保留，作为"代码与 schema 未漂移"的兜底。

### L2 — 业务数据可移植：领域级导出 / 导入 CLI

做一个按实体的导出 / 导入工具（Go 子命令，复用现有 `internal/db`）：

```
portal data export --entities users,enterprises,api_keys,subscriptions,
                     vouchers,providers,models,routing,system_settings
                     --since 2026-07-01 --out backup-202607091843/

portal data import --in backup-202607091843/ --mode upsert --dry-run
portal data import --in backup-202607091843/ --mode upsert
```

设计要点：

- 每个实体一个文件，头部在 `manifest.json` 记录 `portal_version` / `schema_version` / `exported_at` / 各实体行数 / 校验和。
- 导入时按 `schema_version` 做**字段映射**：缺列给默认值、多列忽略、改名列做 alias。
- 按外键依赖顺序写：`providers → user_groups → enterprises → users → enterprise_members → api_keys → subscription_plans → user_subscriptions → vouchers → voucher_uses → orders → balance_transactions → system_settings → service_models → model_pricing_rules → model_provider_routing`。
- 导入默认 `ON CONFLICT DO UPDATE`（upsert），可选 `DO NOTHING`；先 `--dry-run` 打印将影响行数再执行。
- **不**导出 `usage_records` / `audit_logs` / `provider_health_stats*` 这类大观测表（用 L0 `pg_dump` 管，或按时间窗单独归档 CSV）。
- 这套工具同时就是上线初期"把 staging 数据搬进新 hotfix 环境"的通道。

### L3 — Seed 可演进

- 把 `seed.go` 中需要随版本更新的项（如 plans 价格、system_settings 默认值）从 `DO NOTHING` 改成 `ON CONFLICT DO UPDATE`。
- 或迁到一张 `seed_config(key, value, version, managed_by)` 表：运营在 admin 改的标记 `managed_by='admin'`，seed 只更新 `managed_by='seed'` 的行，避免覆盖线上手改的配置。

---

## 5. L2 导出数据格式（可读、可单独编辑）

**结论：可以单独查看和编辑。** L2 导出采用**文本 JSON（每实体一个文件）+ 一个 `manifest.json`**，目录打包为 `tar.gz`。

### 5.1 目录结构

```
backup-202607091843/
├── manifest.json
├── providers.json
├── user_groups.json
├── subscription_plans.json
├── enterprises.json
├── users.json
├── enterprise_members.json
├── api_keys.json
├── vouchers.json
├── voucher_uses.json
├── orders.json
├── balance_transactions.json
├── system_settings.json
├── service_models.json
├── model_pricing_rules.json
└── model_provider_routing.json
```

### 5.2 `manifest.json`

```json
{
  "portal_version": "2026.07.09",
  "schema_version": "0042",
  "exported_at": "2026-07-09T18:43:00Z",
  "exported_by": "admin@example.com",
  "entities": {
    "users":        { "rows": 1234, "sha256": "..." },
    "api_keys":     { "rows": 890,  "sha256": "..." }
  },
  "notes": "pre-hotfix snapshot"
}
```

### 5.3 实体文件（例如 `users.json`）

JSON 数组，每个元素是一行的 `列名 → 值` 映射：

```json
[
  {
    "id": "a9a02ffc-8492-44fb-90d2-921a12d469d8",
    "name": "Alice",
    "email": "alice@example.com",
    "role": "user",
    "balance": 1500000,
    "status": "active",
    "created_at": "2026-06-01T10:00:00Z"
  }
]
```

### 5.4 为什么选 JSON 而不是 CSV

| 维度 | JSON | CSV |
|---|---|---|
| 嵌套字段（`model_pricing_rules.prices` JSONB、数组列） | 原生支持 | 需要二次编码，易错 |
| 字段映射（改 / 加 / 删列） | 按列名匹配，天然 | 依赖列顺序，脆弱 |
| 可读 / 可编辑 | ✅ 任意编辑器、`jq` | ✅ 但嵌套字段难处理 |
| git diff 友好 | ✅ | ✅ |
| 单文件大小 | 中 | 小 |

> 大型观测数据（`usage_records`、`audit_logs`）继续用 CSV + 分区归档，不走 L2。

### 5.5 编辑时的红线

可以安全编辑：name、description、status、quota、balance、pricing、sort_order 等业务字段。

**不要手改**，改了就导入失败 / 数据损坏：

- 主键 / 外键 UUID（`id`、`user_id`、`enterprise_id`…）：改了会破坏引用。
- 加密 / 哈希字段：`api_keys.key_hash`、`upstream_keys` 的密文、`users.password_hash`。
- 时间戳：除非你知道自己在做什么。
- `schema_version`：改了会触发错误的字段映射。

---

## 6. 上线前 / 上线初期操作流

### 上线前一周

1. 冻结 baseline、启用 migrations（L1）。
2. 写好 `portal data export/import` 最小可用版（L2，先覆盖 users / api_keys / enterprises / subscriptions / vouchers / system_settings）。
3. 搭好 `pg_dump -Fc` 定时 + 发布前快照（L0）。
4. 在 staging 完整演练：`pg_dump 旧库 → 起新版本 binary → migrate up → data import → 冒烟测试`。

### 每个 hotfix 迭代

```
1. pg_dump -Fc snapshot_pre_hotfix           # 先保险
2. 写 migrations/NNNN_fix.sql（含数据回填）    # schema/数据变更进脚本
3. staging 跑 migration + 回归
4. 生产：停写或只读 → migrate up → 起新 binary → 验证 → 恢复写
5. 出问题：pg_restore snapshot_pre_hotfix 到新库 → 切回旧 binary
```

---

## 7. 实施阶段

| 阶段 | 内容 | 验收 |
|---|---|---|
| Phase 1 (L1) | 生成 `migrations/0001_baseline.sql`；`bootstrap.go` 增加 migration 启动路径；保留 `InitSchema()` 给空库 / 测试 | 新 binary 在已 baseline 的库上 `migrate` 幂等；CI 静态校验通过 |
| Phase 2 (L2) | `portal data export/import` CLI，覆盖核心主数据，JSON + manifest，dry-run + upsert | staging 全量导出 → 导入到新库 → 行数一致 + 关键外键完整 |
| Phase 3 (L0/L3) | `start.sh` / CI 接入 `pg_dump -Fc` 发布前快照；seed 可更新策略 | 发布前自动产快照；seed 更新能回写到已存在行 |

---

## 8. 风险与未决

- baseline 生成依赖一个干净库跑 `InitSchema()`；需要临时 Postgres（Docker 起一个，或在现有实例里建临时 DB）。
- 大观测表（`usage_records` 分区表）导出策略待定：优先靠 L0，必要时再加分区 CSV 归档。
- L2 字段映射表需要随版本维护；建议放在 `internal/datax/mappings.go`，每个改列 migration 同步加一条映射。

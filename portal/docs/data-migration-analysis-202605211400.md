# 数据迁移与持久化分析

> **ACTIVE** — 2026-05-21
> 分析 Portal 项目的数据架构、迁移机制、数据丢失风险及解决方案。

## 1. 数据分层总览

```
┌─────────────────────────────────────────────────┐
│                  运行时数据                         │
│  (用户产生，除备份外无法恢复)                       │
│                                                   │
│  orders / invoices / billing_records              │
│  usage_logs / audit_logs                          │
│  upstream_keys (加密)                             │
│  users (除 admin 外的注册用户)                     │
│  manually_added_providers                         │
│  user_groups (除 default-general)                 │
│  redemption_codes / redemption_records            │
│  checkin_records / invitations / devices          │
│  subscription_purchases                           │
│  enterprises / addresses / gdpr_requests          │
│  proxy_heartbeats / channel_health                │
│  sensitive_words                                  │
└─────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────┐
│                  种子数据                          │
│  (迁移文件 INSERT，幂等重建)                       │
│                                                   │
│  20 家 provider 预设                              │
│  1 个 default-general 用户组                       │
│  4 个订阅计划 (Lite/Standard/Pro/Max)             │
│  3 个支付网关配置模板 (stripe/alipay/wechat)       │
│  system_settings 默认值                           │
│  1 个 admin 用户 (initDefaultAdmin)              │
└─────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────┐
│                  静态结构                          │
│  (迁移文件 CREATE TABLE/ALTER TABLE，幂等重建)     │
│                                                   │
│  全部 ~40 张表的 schema                            │
│  索引、约束、外键                                  │
└─────────────────────────────────────────────────┘
```

## 2. 迁移文件清单及其作用

| 文件 | 内容类型 | 具体数据 |
|---|---|---|
| `001_core_tables.sql` | Schema | users, api_keys, providers 等核心表 |
| `002_usage_and_records.sql` | Schema | usage_logs, audit_logs |
| `003_billing.sql` | Schema | orders, invoices, billing |
| `004_subscriptions.sql` | Schema | subscription_plans, purchases |
| `005_redemption.sql` | Schema | redemption_codes, records |
| `006_enterprise_groups.sql` | Schema | enterprises, groups |
| `007_system_settings.sql` | Schema + Seed | system_settings 表 + 默认值 |
| `008_v3_schema.sql` | Schema + Seed | 阶段3表 + system_settings |
| `20260516a_seed_default_providers.sql` | **Seed** | 20 家 provider |
| `202605061500_user_group_phase1.sql` | Schema + **Seed** | user_groups 表 + default-general |
| `202605071900_competitive_subscription_plans.sql` | **Seed** | 4 个订阅计划 |
| `202605071200_phase3_enterprise_payment.sql` | Schema + **Seed** | payment_gateways + 3 个模板 |
| 其余 20+ 个文件 | Schema | ALTER TABLE、CREATE INDEX 等 |

## 3. 数据恢复能力矩阵

| 数据分类 | 丢失原因 | 可恢复性 | 恢复方式 |
|---|---|---|---|
| 表结构 | clean-all / 删库 | ✅ 自动 | 迁移文件幂等执行 |
| 20 家 provider | clean-all / 删库 | ✅ 自动 | 种子迁移 INSERT |
| 默认用户组 | clean-all / 删库 | ✅ 自动 | 种子迁移 INSERT |
| 订阅计划模板 | clean-all / 删库 | ✅ 自动 | 种子迁移 INSERT |
| 支付网关模板 | clean-all / 删库 | ✅ 自动 | 种子迁移 INSERT |
| admin 用户 | clean-all / 删库 | ✅ 自动 | initDefaultAdmin() |
| service_models | clean-all / 删库 | ✅ 自动 | Proxy 同步 |
| upstream_keys | clean-all / ENCRYPTION_KEY 变更 | ❌ 不可恢复 | 需手动重新填入 |
| 注册用户 | clean-all / 删库 | ❌ 不可恢复 | 需重新注册 |
| 用户组（自定义） | clean-all / 删库 | ❌ 不可恢复 | 需手动重建 |
| 手动添加的 provider | clean-all / 删库 | ❌ 不可恢复 | 需手动添加 |
| 订单 / 发票 | clean-all / 删库 | ❌ 不可恢复 | 完全丢失 |
| 充值记录 / 余额变更 | clean-all / 删库 | ❌ 不可恢复 | 完全丢失 |
| usage_logs | clean-all / 删库 | ❌ 不可恢复 | 完全丢失 |
| audit_logs | clean-all / 删库 | ❌ 不可恢复 | 完全丢失 |
| 红包码 / 签到记录 | clean-all / 删库 | ❌ 不可恢复 | 需手动重建 |
| 订阅购买记录 | clean-all / 删库 | ❌ 不可恢复 | 完全丢失 |

## 4. 核心风险场景

### 风险 1：数据库容器被误删或数据卷损坏

```
docker compose down -v    # 或 docker volume rm
```

**后果**：所有运行时数据永久丢失。

**触发概率**：在调试、重置或迁移过程中容易误操作。

### 风险 2：ENCRYPTION_KEY 变更导致 upstream_keys 不可解密

```
ENCRYPTION_KEY 用于 AES-256-GCM 加密 upstream_keys.key
```

如果：
- clean-all 后重新部署（新随机密钥）
- 手动修改 `.env.go-platform` 中的 ENCRYPTION_KEY

**后果**：数据库中所有 upstream_key 变成乱码，无法使用。只能删掉重新填入。

### 风险 3：分布式部署时配置文件不同步

Portal 和 Proxy 的 `JWT_SECRET`、`PROXY_SYNC_SECRET` 不一致时：
- Proxy sync 请求被 Portal 拒绝（401）
- Proxy 无法验证用户 JWT token
- 实际上系统不可用

### 风险 4：数据库迁移版本管理

迁移文件按文件名排序执行，记录在 `schema_migrations` 表。
如果：
- 迁移文件被重命名
- 手动修改已应用的迁移
- 跳过某个迁移文件

**后果**：迁移顺序错乱，表结构不一致。

## 5. 解决方案

### 5.1 数据库定时备份

```bash
# 基础备份命令
docker exec ai-portal-postgres pg_dump -U postgres -d ai_proxy_portal \
  --format=custom \
  -f /backups/portal-$(date +%Y%m%d-%H%M%S).dump

# 仅备份数据（不含 schema），便于迁移后恢复
docker exec ai-portal-postgres pg_dump -U postgres -d ai_proxy_portal \
  --data-only --exclude-table=schema_migrations \
  -f /backups/portal-data-$(date +%Y%m%d-%H%M%S).sql
```

**建议**：
- 每日凌晨全量备份，保留 30 天
- 备份文件存到独立的数据卷或外部存储（非 Docker volume）
- 部署前手动备份一次

### 5.2 关键数据的导出流程

对于 `upstream_keys` 这类加密存储的数据，导出时需要注意：

```bash
# 导出 providers（元数据，可直接恢复）
docker exec ai-portal-postgres pg_dump -U postgres -d ai_proxy_portal \
  --data-only --table=providers --inserts \
  -f /backup/providers.sql

# 导出 upstream_keys（含加密字段，ENCRYPTION_KEY 不变时可恢复）
docker exec ai-portal-postgres pg_dump -U postgres -d ai_proxy_portal \
  --data-only --table=upstream_keys --inserts \
  -f /backup/upstream_keys.sql

# 导出 users（不含 password_hash？谨慎处理）
docker exec ai-portal-postgres pg_dump -U postgres -d ai_proxy_portal \
  --data-only --table=users --inserts \
  -f /backup/users.sql
```

**恢复时需满足**：
- `providers`：可直接恢复
- `upstream_keys`：必须使用**相同的** ENCRYPTION_KEY
- `users`：password_hash 是 bcrypt，可跨环境恢复

### 5.3 部署清单（clean-all 后恢复步骤）

```
□ 1. start.sh mvp → 启动所有服务
□ 2. 确认 20 家 provider 已自动创建
□ 3. 等待 service_models 自动同步完成
□ 4. 手动添加上一环境的 provider（accessapi, xunfei, siliconflow 等）
□ 5. 手动创建用户组（test, test-1 等）
□ 6. 手动填入 upstream API Key（需准备原始 key 值）
□ 7. 确认 Proxy 能正常 sync（查看 portal 日志）
□ 8. 重新配置红包码、签到奖励等
□ 9. 通知用户重新注册（用户数据丢失时）
```

### 5.4 ENCRYPTION_KEY 管理

- **不要频繁轮换**：ENCRYPTION_KEY 轮换需要重新加密所有 upstream_keys
- **安全存储**：部署时记录到密码管理器，或使用 Vault 等密钥管理服务
- **灾备**：将 `.env.go-platform` 作为备份文件保存（包含所有密钥）

### 5.5 长期建议

| 建议 | 优先级 | 说明 |
|---|---|---|
| 添加 `pg_dump` 定时备份脚本 | 🔴 高 | 每日自动备份到外部存储 |
| 部署前备份 `.env.go-platform` | 🔴 高 | 包含所有密钥配置 |
| 将备份脚本纳入 start.sh | 🟡 中 | `start.sh backup` / `start.sh restore` |
| 添加数据迁移/导出命令 | 🟡 中 | 支持跨环境迁移 |
| 数据库主从/高可用 | 🟢 低 | 生产环境必要，当前阶段可不做 |

## 6. 总结

Portal 的数据安全性取决于两个因素：

1. **数据库持久化** — Docker volume `postgres-data` 是唯一的存储载体，一旦丢失无法挽回
2. **ENCRYPTION_KEY 一致性** — 它不仅用于加密，还决定了 upstream_keys 的可恢复性

**最低要求的保护措施**：每天凌晨 `pg_dump` 到宿主机目录 + 保存 `.env.go-platform` 副本。
这样即使数据库完全丢失，也能恢复到最近一天的完整状态。

# Aurora PostgreSQL 迁移指南

> **目标**：将 Portal 当前由 `start.sh` 拉起的 Docker PostgreSQL（`postgres:15-alpine`）切换到 AWS Aurora PostgreSQL（兼容版），同一套 `start.sh` 同时支持「本地 Docker」与「外部 Aurora」两种模式，由 `DB_HOST` 一键切换。
>
> **范围**：仅改 `start.sh` / `scripts/backup-db.sh` / 配置项；**Go 代码无需改动**（驱动 `pgx/v5`，DSN 已支持 `sslmode`/`sslrootcert`，启动幂等 `InitSchema` 建表）。

---

## 1. 架构与兼容性

| 项目 | 本地 Docker | 目标 Aurora | 说明 |
|------|------------|-------------|------|
| 版本 | PostgreSQL 15.x | Aurora PostgreSQL 15.x | 兼容，pg_dump/pg_restore 直通 |
| 驱动 | `pgx/v5/pgxpool` | 同 | 仅改连接端点 |
| Schema | `internal/db/schema.go` `InitSchema()` 幂等建表 | 同 | 空库首次启动自动建表，迁移推荐 data-only |
| 连接方 | 仅 **Portal** | 仅 **Portal** | `ai-proxy-go` 不连库（只走 Redis + Portal HTTP），无需改 |

代码侧已确认：无 `CREATE EXTENSION` / `ALTER SYSTEM` / `COPY FROM PROGRAM` / 文件系统访问；使用 `pg_advisory_lock`（Aurora 支持）。

---

## 2. 切换机制（关键）

由 `.env.go-platform` 中的 `DB_HOST` 推导（`start.sh` 内 `is_external_db()`）：

| `DB_HOST` | 模式 | 行为 |
|-----------|------|------|
| 空 / `postgres` / `127.0.0.1` / `localhost` | **本地** | 拉起 `ai-portal-postgres` 容器，Portal `depends_on` postgres |
| 其它（如 `*.rds.amazonaws.com`） | **外部** | 不拉 PG 容器、不建 `postgres-data` 卷，Portal 直连端点 |

`.env.go-platform` 数据库相关键（首次运行从 `.env` 继承，缺省本地语义）：

```bash
DB_HOST=postgres        # postgres=本地；填 Aurora writer endpoint 即外部模式
DB_PORT=5432
DB_NAME=ai_proxy_portal
DB_USER=postgres
DB_PASSWORD=...         # 本地：容器密码；外部：Aurora master 密码
DB_SSLMODE=disable      # 本地 disable；Aurora 必须 require（建议 verify-full + DB_SSL_ROOT_CERT）
DB_SSL_ROOT_CERT=       # verify-full 时填 RDS CA 证书包路径
```

> 真实配置入口是 **`.env.go-platform` + `start.sh` 生成器**（不是 `ai-portal-go/.env` 或 `ai-proxy-go/.env`）。`docker-compose.portal.yml` 由 `start.sh` 每次启动重新生成，**不要手改该文件**（会被覆盖）。

---

## 3. 前置（AWS 侧）

- Aurora PostgreSQL **15.x**，放 **与 EC2 同一 VPC**（本环境在 `ap-southeast-2`）。
- DB subnet group 跨 ≥2 AZ；安全组入站放行「EC2 安全组 → TCP 5432」；不开公网。
- 初始数据库 `ai_proxy_portal`、主用户 `postgres`（与本地一致，减少变更）。
- 记录 **writer endpoint**：`<cluster>.cluster-xxxxxxxx.ap-southeast-2.rds.amazonaws.com`。
- 建议：删除保护、备份保留 7–35 天、Performance Insights、`max_connections` ≥ `DB_POOL_MAX`（默认 20）。

---

## 4. 本地模式（现状，无需改动）

```bash
cd /home/ubuntu/workspace/ai-portal-go
./start.sh status          # ai-portal-postgres / ai-portal-go / redis 均 healthy
./start.sh snapshot        # 留底 -> backups/snapshot-keep-<UTC>.dump
```

---

## 5. 迁移到 Aurora（data-only 路径，推荐）

> 思路：schema 由 Portal 在 Aurora 上 `InitSchema` 自动建好，只把**数据**从 Docker 灌过去。比全量 dump/restore 更干净。

```bash
cd /home/ubuntu/workspace/ai-portal-go

# 0) 留底（本地仍在跑）
./start.sh snapshot

# 1) 从本地 Docker 导出【仅数据】（custom 格式）
docker exec ai-portal-postgres pg_dump -U postgres -d ai_proxy_portal \
  --data-only --no-owner --no-acl -Fc > /tmp/portal_data.dump

AURORA=<cluster-writer>.ap-southeast-2.rds.amazonaws.com
AURORA_PW='<aurora-master-password>'

# 2) 连通性 + 建库（用一次性客户端容器，宿主机无需装 psql）
docker run --rm -e PGPASSWORD="$AURORA_PW" postgres:15-alpine \
  psql "host=$AURORA port=5432 user=postgres dbname=postgres sslmode=require" \
  -c "SELECT version();"
# 若创建集群时已指定初始库 ai_proxy_portal 可跳过下面这句
docker run --rm -e PGPASSWORD="$AURORA_PW" postgres:15-alpine \
  psql "host=$AURORA port=5432 user=postgres dbname=postgres sslmode=require" \
  -c "CREATE DATABASE ai_proxy_portal;"

# 3) 切到外部模式并启动一次：让 InitSchema/Migrate 在空 Aurora 上建 schema
#    编辑 .env.go-platform：
#      DB_HOST=<AURORA>  DB_PASSWORD=<AURORA_PW>  DB_SSLMODE=require
./start.sh rebuild        # Portal 在 Aurora 空库上建表；此时业务表为空

# 4) 停写（避免迁移期间产生新数据），把数据灌进 Aurora
./start.sh stop-portal
docker run --rm -i --network host -e PGPASSWORD="$AURORA_PW" postgres:15-alpine \
  pg_restore "host=$AURORA port=5432 user=postgres dbname=ai_proxy_portal sslmode=require" \
  --no-owner --no-acl < /tmp/portal_data.dump

# 5) 起来 + 校验
./start.sh portal
docker run --rm -e PGPASSWORD="$AURORA_PW" postgres:15-alpine \
  psql "host=$AURORA port=5432 user=postgres dbname=ai_proxy_portal sslmode=require" \
  -c "SELECT count(*) AS tables FROM pg_tables WHERE schemaname='public';"
```

业务冒烟清单：admin 登录、用户/企业页、API Key 列表、兑换码、Proxy 从 Portal 同步、经 Proxy 调模型、计费/用量更新。

> 备选：全量 `pg_dump`/`pg_restore`（schema+data）也可，导出加 `--no-owner --no-acl`，并确认扩展在 Aurora 可用；近零停机需 DMS/逻辑复制，本次不展开。

---

## 6. 备份 / 恢复（双模式，自动识别 DB_HOST）

`start.sh` 的 `snapshot` / `restore` 与 `scripts/backup-db.sh` 均已支持本地/外部双模式：外部模式用**一次性客户端容器**（`postgres:15-alpine`）执行 `pg_dump/pg_restore`，宿主机无需安装 psql。

```bash
# 手动留底（本地或外部，自动判断）
./start.sh snapshot                                    # -> backups/snapshot-keep-<UTC>.dump

# 恢复到【新库名】，绝不覆盖当前库
./start.sh restore backups/snapshot-keep-<UTC>.dump portal_check
# 验证无误后切换：.env.go-platform 改 DB_NAME=portal_check 再 ./start.sh rebuild

# cron 备份（双模式；外部模式需先 export DB_HOST/DB_PASSWORD/...）
./scripts/backup-db.sh 7
# 外部示例：
DB_HOST=$AURORA DB_PASSWORD="$AURORA_PW" DB_SSLMODE=require ./scripts/backup-db.sh 7
```

相关环境变量：`SNAPSHOT_ON_RESTART`（rebuild 前自动快照，默认 1）、`SNAPSHOT_RETAIN_DAYS`（默认 14）、`SNAPSHOT_DIR`（默认 `<项目>/backups`）。

---

## 7. 回滚

本地 Docker 卷 `ai-portal-go_postgres-data` 在切换后仍保留：

```bash
# .env.go-platform 改回本地
DB_HOST=postgres
./start.sh rebuild        # Portal 重连本地 ai-portal-postgres
```

> 注意：切到 Aurora 之后产生的新写数据**不在**本地卷中；如需回滚并保留这些数据，先从 Aurora 反向 dump 再回灌本地。

---

## 8. 常见问题

- **连不上 Aurora**：确认同 VPC；安全组放行「EC2 安全组→5432」；`publicly accessible=No` 时走内网；NACL 放行 5432。`start.sh` 外部模式启动前会做 TCP 连通性检查并给出明确报错。
- **导入报权限/属主错误**：导出/恢复均已加 `--no-owner --no-acl`。
- **改 `.env` 不生效**：Portal 实际读 `docker-compose.portal.yml` 注入的环境（来自 `.env.go-platform`）；改完需 `./start.sh rebuild` 重新生成 compose。
- **`ENCRYPTION_KEY` / `JWT_SECRET` 不要动**：与库迁移无关；`ENCRYPTION_KEY` 变会让已存 API Key 无法解密，`JWT_SECRET` 变会让所有用户掉线。
- **`docker-compose.portal.yml` 被还原**：属正常，该文件由 `start.sh` 生成；改 `.env.go-platform` 后重建即可。

---

## 9. 成本参考（ap-southeast-2，月）

| 方案 | 实例 | 存储 | 估算 |
|------|------|------|------|
| Docker PG（EC2 本地） | $0 | EBS ~$0.10/GB | ~$5–10 |
| Aurora db.t3.medium | ~$65 | ~$0.12/GB | ~$70 |
| Aurora db.r6g.large | ~$180 | ~$0.12/GB | ~$190 |
| Aurora Serverless v2（0.5–8 ACU） | 按 ACU | ~$0.12/GB | 低流量更省 |

长期用可买 1/3 年预留实例省 40–60%。

---

> **文档版本**：2026-07-10
> **适用**：ai-portal-go PostgreSQL 15.x → Aurora PostgreSQL 15.x（`start.sh` 本地/外部双模式）

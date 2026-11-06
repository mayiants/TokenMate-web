# AI Platform Go 部署指南

> **Date:** 2026-05-30 06:40 | **Status:** ACTIVE | **Version:** v1.0.0

---

## 目录

1. [架构概述](#1-架构概述)
2. [组件说明](#2-组件说明)
3. [密钥管理（核心）](#3-密钥管理核心)
4. [部署阶段](#4-部署阶段)
   - Stage 1: 单机部署
   - Stage 2: 分离数据库
   - Stage 3: 区域代理
   - Stage 4: 多区域 Portal
5. [配置文件详解](#5-配置文件详解)
6. [启动方式](#6-启动方式)
7. [运维操作](#7-运维操作)
8. [故障排查](#8-故障排查)

---

## 1. 架构概述

```
                            ┌─────────────────────────┐
                            │   Nginx (SSL, 80/443)   │
                            └───────────┬─────────────┘
                                        │
                    ┌───────────────────┼───────────────────┐
                    │                   │                   │
              ┌─────▼─────┐      ┌─────▼─────┐      ┌─────▼─────┐
              │ Portal    │      │ Proxy     │      │ Proxy     │
              │ :3001     │◄────►│ :8080     │      │ :8080     │
              │           │ sync │ (per-node │      │ (per-node │
              │ PG+Redis  │ 30s  │ key pool) │      │ key pool) │
              └───────────┘      └───────────┘      └───────────┘
```

**核心关系：**
- Portal 是唯一数据源（PostgreSQL + Redis）。所有配置、用户、计费数据在 Portal 维护。
- 每个 Proxy 独立从 Portal 同步 key pool（默认 30s 间隔），本地缓存。
- Proxy 之间不直接通信。Portal 也不主动推送数据到 Proxy。
- Proxy 缓存 API key 验证结果 5 分钟，减少 Portal 查询压力。
- 所有实例共享同一组核心密钥（见 §3）。

---

## 2. 组件说明

| 组件 | 职责 | 端口 | 必需 |
|------|------|------|:----:|
| **Portal** (Go) | 管理后台、用户认证、计费、上游 key 管理、Webhook 接收 | 3001 | ✅ |
| **Proxy** (Go) | API 请求转发、模型路由、限流、密钥池管理 | 8080 | ✅ |
| **PostgreSQL** | Portal 业务数据库（用户、key、计费、日志） | 5432 | ✅ |
| **Redis** | Portal 队列、会话缓存、限流计数器 | 6379 | Portal 必需 |
| **Nginx** | SSL 终止、反向代理、静态文件服务 | 80/443 | 生产必需 |

**Proxy 与 Redis 的关系：**
- Proxy 的 Redis 配置是**可选的**。`REDIS_HOST` 为空时，Proxy 自动回退到**内存模式**（代码内部处理）。
- 内存模式在单机部署下性能足够（单机限流 80-100K RPM）。
- 只有 Stage 3（多台 Proxy）且需要**跨实例共享限流状态**时，才需要为 Proxy 集群单独部署 Redis。

---

## 3. 密钥管理（核心）

### 3.1 必须全局一致的密钥

以下 4 个变量 **必须在 Portal 和所有 Proxy 节点上完全一致**。任何一个不一致都会导致服务不可用：

| 变量 | 用途 | 不一致的后果 |
|------|------|-------------|
| `JWT_SECRET` | Proxy 验证 Portal 签发的 JWT token | API key 验证全部失败，返回 401 |
| `ENCRYPTION_KEY` | AES-256-GCM 加密/解密上游 API key | 上游 key 解密失败，所有请求 503 |
| `PROXY_SYNC_SECRET` | Portal Webhook 签名 + Proxy 同步认证 | Webhook/Sync 请求被 Portal 拒绝（403） |
| `ADMIN_PASSWORD` | 初始管理员账号密码（仅首次启动创建） | 首次启动无法创建管理员 |

> **生成建议**：所有节点共用同一份 `.env` 或 `.env.template`，避免手动输入出错。使用 `openssl rand -hex 32` 生成。

### 3.2 各节点独立的密钥

以下变量**不需要**全局一致，每台机器可以独立设置：

| 变量 | 用途 | 说明 |
|------|------|------|
| `ADMIN_SECRET` | Proxy 管理接口（`/admin/*`）认证 | 每台 Proxy 可独立设置，默认复用 `PROXY_SYNC_SECRET` |
| `DB_PASSWORD` | PostgreSQL 连接密码 | Portal 连自己的 DB；Proxy 不直接连 DB |
| `REDIS_PASSWORD` | Redis 连接密码 | Stage 1 共享 Portal 的 Redis；Stage 3 各 Proxy 用自己的 Redis |

### 3.3 生成密钥

```bash
# 全局一致的密钥（64 字符十六进制）
openssl rand -hex 32   # JWT_SECRET
openssl rand -hex 32   # ENCRYPTION_KEY
openssl rand -hex 32   # PROXY_SYNC_SECRET

# 各节点独立的密钥
openssl rand -hex 32   # ADMIN_SECRET（可选，可复用 PROXY_SYNC_SECRET）
openssl rand -hex 32   # DB_PASSWORD
openssl rand -hex 32   # REDIS_PASSWORD
```

---

## 4. 部署阶段

### Stage 1: 单机部署（10-100 用户）

**所有组件运行在同一台机器上。**

```
Server (Ubuntu 22.04, 2 vCPU, 4GB RAM)
├── Nginx    (:80/443 → :3001, :8080)
├── Portal   (:3001)
├── Proxy    (:8080)
├── PostgreSQL (container, :5432)
└── Redis    (container, :6379)
```

**1. 准备环境**

```bash
# 克隆代码（两个仓库需要在同级目录）
git clone https://github.com/your-org/ai-portal-go.git
git clone https://github.com/your-org/ai-proxy-go.git

cd ai-portal-go
```

**2. 配置 `.env`**

```bash
cp .env.template .env
nano .env
```

```bash
# === 数据库 ===
DB_PASSWORD=<64-char-hex>

# === 全局一致密钥（⚠️ 所有节点必须相同）===
JWT_SECRET=<64-char-hex>
ENCRYPTION_KEY=<64-char-hex>
PROXY_SYNC_SECRET=<64-char-hex>

# === Admin ===
ADMIN_EMAIL=admin@example.com
ADMIN_PASSWORD=<12-chars-min>
ADMIN_NAME=Admin
ADMIN_MODE=production

# === 网络 ===
PORTAL_URL=http://127.0.0.1:3001
APP_BASE_URL=https://your-domain.com
ALLOWED_ORIGINS=https://your-domain.com

# === SMTP（可选）===
SMTP_HOST=smtp.example.com
SMTP_PORT=465
SMTP_SECURE=true
SMTP_USER=noreply@example.com
SMTP_PASSWORD=<password>
SMTP_FROM="AI Platform <noreply@example.com>"
```

**3. 启动**

```bash
chmod +x start.sh
./start.sh mvp
```

`start.sh` 会自动：
- 生成 `.env.go-platform`（从 `.env` 读取值）
- 创建 `docker-compose.portal.yml`（Portal + PG + Redis）
- 创建 `docker-compose.proxy.yml`（Proxy，连接 Portal 的 Redis）
- 构建并启动所有容器

**4. 配置 Nginx**

```nginx
server {
    listen 443 ssl http2;
    server_name your-domain.com;
    ssl_certificate /etc/letsencrypt/live/your-domain.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/your-domain.com/privkey.pem;

    # Portal 管理后台
    location / {
        proxy_pass http://127.0.0.1:3001;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }

    # Proxy API 转发
    location /v1/ {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_read_timeout 120s;
        proxy_buffering off;
    }
}
```

**Stage 1 的 Redis 配置：**
- Portal 的 Redis 绑定 `127.0.0.1:6379`
- Proxy 通过 Docker 网络连接到 Portal 的 Redis
- 两者共享同一个 Redis 实例，因为都在同一台机器上

---

### Stage 2: 分离数据库（100-500 用户）

**将 PostgreSQL 移到外部 RDS，Portal + Proxy + Redis 仍在同一台 EC2。**

```
┌─ EC2 (t3.medium) ───────────────┐     ┌─ AWS RDS ──────────────────┐
│ Nginx, Portal(:3001),           │     │ PostgreSQL 15              │
│ Proxy(:8080), Redis             │────►│ db.t3.medium, 20GB GP3     │
└─────────────────────────────────┘     └────────────────────────────┘
```

**变更：**
- 从 `docker-compose.portal.yml` 中移除 `postgres` service
- 更新 Portal 环境变量：`DB_HOST=<rds-endpoint>`
- Proxy 配置不变（仍共享 Portal 的 Redis）

```yaml
# docker-compose.portal.yml (diff)
portal:
  environment:
    - DB_HOST=ai-portal-db.xxxx.rds.amazonaws.com
    - DB_PORT=5432
    - DB_NAME=ai_proxy_portal
    - DB_USER=postgres
    - DB_PASSWORD=${DB_PASSWORD}
```

**迁移数据：**

```bash
# 从旧容器导出
docker exec ai-portal-postgres pg_dump -U postgres ai_proxy_portal > /tmp/dump.sql

# 导入 RDS
psql -h <rds-endpoint> -U postgres -d ai_proxy_portal -f /tmp/dump.sql
```

---

### Stage 3: 区域代理（500-2000 用户）

**在延迟敏感区域（如中国、美西）部署额外的 Proxy-only 服务器。**

```
                    ┌──────────────────────┐
                    │ Portal (Singapore)    │
                    │ :3001 + PostgreSQL    │
                    │ Redis                 │
                    └──────┬───────────────┘
                           │ key-pool/sync (REST, 30s)
              ┌────────────┼────────────────────┐
              │            │                    │
        ┌─────▼─────┐ ┌───▼────────┐  ┌────────▼──────┐
        │Proxy SG   │ │Proxy CN    │  │Proxy US       │
        │(same EC2) │ │(Beijing)   │  │(Oregon)       │
        └───────────┘ └────────────┘  └───────────────┘
```

**Portal 服务器**（不变）：
```bash
./start.sh portal
```

**Proxy-only 服务器**（重复部署到每个区域）：

#### 方式 A：从源码构建（需要 Go 环境）

```bash
# 1. 仅复制 ai-proxy-go 代码
git clone https://github.com/your-org/ai-proxy-go.git
cd ai-proxy-go

# 2. 配置 .env
cat > .env << EOF
PORTAL_URL=https://portal.your-domain.com:3001
JWT_SECRET=<same-as-portal>
ENCRYPTION_KEY=<same-as-portal>
PROXY_SYNC_SECRET=<same-as-portal>
ADMIN_SECRET=<can-be-different>
RATE_LIMIT_ENABLED=true
RATE_LIMIT_DEFAULT_RPM=60
EOF

# 3. 启动
docker compose -f docker-compose.proxy.yml up -d --build
```

#### 方式 B：从 Portal 拷贝 Docker 镜像 + .env（推荐，无需 Go 环境）

```bash
# === 在 Portal 服务器上 ===
# 构建 Proxy 镜像
cd ai-proxy-go && docker build -t ai-proxy-go:latest .
docker save ai-proxy-go:latest | gzip > /tmp/proxy.tar.gz

# 拷贝镜像和配置到 Proxy 服务器
scp /tmp/proxy.tar.gz proxy-server:/opt/ai-proxy/
scp .env.go-platform proxy-server:/opt/ai-proxy/

# === 在 Proxy 服务器上 ===
cd /opt/ai-proxy

# 加载镜像
gunzip -c proxy.tar.gz | docker load

# 修改配置：仅需改 PORTAL_URL 和添加 SERVER_ID
sed -i 's|PORTAL_URL=.*|PORTAL_URL=https://portal.your-domain.com:3001|' .env.go-platform
echo "SERVER_ID=proxy-beijing-01" >> .env.go-platform
echo "SERVER_REGION=cn" >> .env.go-platform
echo "REDIS_HOST=" >> .env.go-platform  # 留空 = 内存模式

# 启动
docker run -d --name ai-proxy-go \
  --env-file .env.go-platform \
  -p 8080:8080 \
  --restart unless-stopped \
  ai-proxy-go:latest
```

> **方式 B 要点**：
> - Proxy 镜像约 51MB，无需源码和 Go 编译环境
> - 直接从 Portal 的 `.env.go-platform` 拷贝，Proxy 会忽略不需要的变量（如 `DB_PASSWORD`、`SMTP_*`）
> - 必须修改 `PORTAL_URL` 指向远端 Portal 地址
> - 必须添加 `SERVER_ID` 用于 Portal 识别不同 Proxy 实例（不设则自动生成随机 ID）
> - `REDIS_HOST=` 留空启用内存模式，单机限流足够（80-100K RPM）

**Stage 3 的 Redis 策略：**

| 方案 | 配置 | 适用场景 |
|------|------|---------|
| **A. 不配 Redis（推荐）** | `REDIS_HOST=` 留空 | 单台 Proxy 或不需要跨实例限流 |
| **B. Proxy 集群 Redis** | 每台 Proxy 配独立 Redis | 多台 Proxy 需要共享限流/Nonce 状态 |

> ⚠️ **不要**让跨区域 Proxy 连接 Portal 的 Redis。Portal 的 Redis 只接受本地连接，且跨区域 Redis 延迟过高。

**跨区域 Proxy 的延迟影响：**
- 北京 Proxy → 新加坡 Portal：~100ms RTT
- 但 API key 验证结果缓存 5 分钟，只有首次请求有延迟惩罚
- key pool 同步每 30s 一次，异步进行，不影响请求路径

---

### Stage 4: 多区域 Portal（2000+ 用户）

**双 Portal + PostgreSQL streaming replication。**

```
┌─ Portal CN (Beijing) ───────┐    streaming    ┌─ Portal SG (Singapore) ────┐
│ :3001, Redis, PostgreSQL    │◄───────────────►│ :3001, Redis, PostgreSQL   │
│ (writer — admin UI here)    │   replication   │ (reader — faster for SG)   │
└──────────┬──────────────────┘                 └──────────┬─────────────────┘
           │                                               │
    Proxy CN-1, CN-2                              Proxy SG, Proxy US
```

**writer（中国）配置：**
```bash
ADMIN_MODE=production
# 正常全功能
```

**reader（新加坡）配置：**
```bash
ADMIN_MODE=production
# 建议：管理写操作重定向到 writer Portal
# 通过 Nginx 或应用层实现
```

> Stage 4 需要应用层支持读写分离。当前版本建议手动配置 PostgreSQL streaming replication，Portal 代码本身不区分读写角色。

---

## 5. 配置文件详解

### 5.1 Portal `.env` 变量

| 变量 | 必填 | 默认值 | 说明 |
|------|:----:|--------|------|
| `DB_PASSWORD` | ✅ | — | PostgreSQL 密码，≥16 字符 |
| `REDIS_PASSWORD` | ✅ | — | Redis 密码 |
| `JWT_SECRET` | ✅ | — | JWT 签名密钥，≥32 字符，全局一致 |
| `ENCRYPTION_KEY` | ✅ | — | AES-256-GCM 密钥，≥32 字符，全局一致 |
| `PROXY_SYNC_SECRET` | ✅ | — | 同步签名密钥，≥32 字符，全局一致 |
| `ADMIN_SECRET` | ✅ | — | 管理接口密钥，≥32 字符 |
| `ADMIN_EMAIL` | ✅ | `admin@example.com` | 初始管理员邮箱 |
| `ADMIN_PASSWORD` | ✅ | — | 初始管理员密码，生产 ≥12 字符 |
| `ADMIN_MODE` | ✅ | `production` | `development` / `production` |
| `PORTAL_URL` | ✅ | `http://127.0.0.1:3001` | Proxy 连接地址 |
| `APP_BASE_URL` | ✅ | — | 外部访问域名，用于 cookie domain |
| `ALLOWED_ORIGINS` | — | 空 | CORS 来源，空=允许所有 |
| `SMTP_HOST` | — | 空 | SMTP 服务器 |
| `SMTP_PORT` | — | `465` | SMTP 端口 |
| `SMTP_SECURE` | — | `true` | 是否使用 TLS |
| `SMTP_USER` | — | 空 | SMTP 用户名 |
| `SMTP_PASSWORD` | — | 空 | SMTP 密码 |
| `SMTP_FROM` | — | 空 | 发件人地址 |

### 5.2 Proxy `.env` 变量

| 变量 | 必填 | 默认值 | 说明 |
|------|:----:|--------|------|
| `PORTAL_URL` | ✅ | — | Portal 地址 |
| `JWT_SECRET` | ✅ | — | 必须与 Portal 相同 |
| `ENCRYPTION_KEY` | ✅ | — | 必须与 Portal 相同 |
| `PROXY_SYNC_SECRET` | ✅ | — | 必须与 Portal 相同 |
| `ADMIN_SECRET` | ✅ | — | 可独立设置 |
| `SERVER_ID` | — | 自动生成 `proxy-{uuid}` | 实例标识，用于 Portal 识别不同 Proxy |
| `SERVER_REGION` | — | `default` | 区域标识，用于跨区域路由优先级 |
| `REDIS_HOST` | — | `127.0.0.1` | 空=内存模式 |
| `REDIS_PORT` | — | `6379` | Redis 端口 |
| `REDIS_PASSWORD` | — | 空 | Redis 密码 |
| `RATE_LIMIT_ENABLED` | — | `true` | 是否启用限流 |
| `RATE_LIMIT_DEFAULT_RPM` | — | `60` | 默认每分钟请求数 |
| `SIMULATE_ENABLED` | — | `false` | 模拟模式（不调用真实 API） |
| `SIMULATE_LATENCY_MS` | — | `100` | 模拟延迟 |

---

## 6. 启动方式

### 6.1 `start.sh` 命令

```bash
./start.sh mvp              # 完整启动 Portal + Proxy（生产）
./start.sh simulate         # 模拟模式（不消耗真实 API key）
./start.sh portal           # 仅 Portal（分布式部署）
./start.sh proxy            # 仅 Proxy（连接本地或远端 Portal）
./start.sh stop             # 停止所有服务
./start.sh status           # 查看状态和健康检查
./start.sh logs             # 查看日志
./start.sh build            # 构建所有服务
./start.sh clean            # 清理 docker-compose 文件（保留 .env.go-platform）
./start.sh clean-all        # 完全重置（删除数据卷）
./start.sh rebuild          # 重新构建并启动
```

### 6.2 分布式 Proxy 启动

```bash
# 在 Proxy-only 服务器上
cd ai-proxy-go

# 方式 1: 使用 CLI 参数
./start.sh proxy \
  --portal-url=https://portal.your-domain.com:3001 \
  --proxy-secret=<PROXY_SYNC_SECRET> \
  --jwt-secret=<JWT_SECRET> \
  --redis-password=<REDIS_PASSWORD>

# 方式 2: 使用 .env 文件
cp .env.template .env
# 编辑 .env 填入值
./start.sh proxy

# 方式 3: 纯 Docker Compose
docker compose -f docker-compose.proxy.yml up -d --build
```

---

## 7. 运维操作

### 7.1 查看日志

```bash
# Portal 日志
cd ai-portal-go
docker compose -f docker-compose.portal.yml logs -f portal

# Proxy 日志
cd ai-proxy-go
docker compose -f docker-compose.proxy.yml logs -f proxy

# 所有日志
./start.sh logs
```

### 7.2 备份数据

```bash
# PostgreSQL 备份
docker exec ai-portal-postgres pg_dump -U postgres ai_proxy_portal > backup_$(date +%Y%m%d).sql

# Redis 备份
docker exec ai-portal-redis redis-cli -a "${REDIS_PASSWORD}" BGSAVE
```

### 7.3 更新部署

```bash
# 拉取代码
git pull

# 重建并启动（零停机需要蓝绿部署，单机有 ~30s 中断）
./start.sh rebuild
```

### 7.4 健康检查

```bash
# Portal
curl http://localhost:3001/api/health
# → {"status":"ok",...}

# Proxy
curl http://localhost:8080/health
# → {"status":"ok","healthy":true,...}

# 检查已同步的上游 key
curl http://localhost:8080/health | jq '.providers'
```

---

## 8. 故障排查

### Portal 无法启动

```bash
# 检查数据库连接
docker exec ai-portal-postgres psql -U postgres -c "SELECT 1"

# 检查 Redis 连接
docker exec ai-portal-redis redis-cli -a "${REDIS_PASSWORD}" ping

# 查看 Portal 日志
docker logs ai-portal-go --tail 100
```

### Proxy 无法同步 key

```bash
# 检查 Portal 可访问性
curl http://<portal-url>:3001/api/health

# 检查 sync secret 是否一致
echo "Portal: $(grep PROXY_SYNC_SECRET ai-portal-go/.env)"
echo "Proxy:  $(grep PROXY_SYNC_SECRET ai-proxy-go/.env)"

# 手动测试同步
curl -H "x-proxy-secret: <PROXY_SYNC_SECRET>" \
  http://<portal-url>:3001/api/admin/key-pool/sync
```

### CORS 错误

```bash
# 检查 ALLOWED_ORIGINS
grep ALLOWED_ORIGINS ai-portal-go/.env

# 空值 = 允许所有（适用于反向代理场景）
# 如需限制，设置为实际域名：ALLOWED_ORIGINS=https://your-domain.com
```

### API key 验证失败（401）

```bash
# 检查 JWT_SECRET 是否一致
grep JWT_SECRET ai-portal-go/.env
grep JWT_SECRET ai-proxy-go/.env

# 检查 Portal 是否能正确签发 token
curl -X POST http://localhost:3001/api/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email":"admin@example.com","password":"<ADMIN_PASSWORD>"}'
```

---

## 部署阶段对比

| 因素 | Stage 1 | Stage 2 | Stage 3 | Stage 4 |
|------|---------|---------|---------|---------|
| Portal 实例 | 1 | 1 | 1 | 2 (writer+reader) |
| Proxy 实例 | 1 (同机) | 1 (同机) | 3-5 (区域) | 5+ (区域) |
| DB | 容器 | RDS | RDS | RDS + replica |
| Portal Redis | 容器 | 容器 | 容器 | 每 Portal 一个 |
| Proxy Redis | 共享 Portal | 共享 Portal | 可选（独立） | 可选（独立） |
| 零停机部署 | ❌ ~30s | ❌ ~30s | ✅ per-proxy | ✅ per-proxy |
| 最大用户 | 10-100 | 100-500 | 500-2000 | 2000+ |

---

## 首次部署检查清单

- [ ] 代码已推送到 GitHub：`ai-portal-go` + `ai-proxy-go`
- [ ] 已打 tag `v1.0.0`（或最新版本）
- [ ] 生成 4 个全局密钥：`JWT_SECRET`, `ENCRYPTION_KEY`, `PROXY_SYNC_SECRET`, `ADMIN_PASSWORD`
- [ ] `ADMIN_MODE=production`
- [ ] `ALLOWED_ORIGINS` 设为实际域名（或留空用于反向代理）
- [ ] 配置 SSL（Let's Encrypt certbot）
- [ ] 本地测试通过：`./start.sh mvp`
- [ ] 构建镜像：`docker build`
- [ ] 验证健康检查：`curl /api/health` 和 `curl /health`
- [ ] 创建测试 API key + 测试代理调用
- [ ] 配置每日 DB 备份到 S3

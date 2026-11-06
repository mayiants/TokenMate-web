# AI Platform Go - 部署指南

> **状态**: ACTIVE  
> **创建时间**: 2026-05-03  
> **版本**: 1.0

## 1. 概述

本文档描述 AI Platform Go (Portal + Proxy) 的部署方案，包括：
- 启动脚本 `start.sh` 使用说明
- 单机部署方案
- 分布式部署方案
- PostgreSQL 使用说明
- Redis 使用建议

## 2. 目录结构

```
/home/ubuntu/workspace/
├── ai-portal-go/           # Go Portal 代码
│   ├── start.sh            # 启动脚本
│   ├── cmd/server/main.go  # Portal 入口
│   ├── internal/           # 内部包
│   ├── pkg/                # 公共包
│   └── docs/               # 文档
│
└── ai-proxy-go/            # Go Proxy 代码
    ├── cmd/server/main.go  # Proxy 入口
    ├── internal/           # 内部包
    ├── pkg/                # 公共包
    └── docs/               # 文档
```

## 3. 启动脚本 start.sh

### 3.1 命令列表

| 命令 | 说明 |
|------|------|
| `./start.sh` | 交互式选择模式 |
| `./start.sh mvp` | 启动完整平台 (Portal + Proxy) |
| `./start.sh simulate` | 启动完整平台 (模拟模式) |
| `./start.sh portal` | **仅启动 Portal** |
| `./start.sh proxy` | **仅启动 Proxy** |
| `./start.sh stop` | 停止所有服务 |
| `./start.sh stop-portal` | 停止 Portal |
| `./start.sh stop-proxy` | 停止 Proxy |
| `./start.sh status` | 查看状态 |
| `./start.sh logs` | 查看日志 |
| `./start.sh build` | 构建所有服务 |
| `./start.sh build-portal` | 仅构建 Portal |
| `./start.sh build-proxy` | 仅构建 Proxy |
| `./start.sh clean` | 清理配置 |
| `./start.sh clean-all` | 完全重置 |
| `./start.sh rebuild` | 重新构建并启动 |
| `./start.sh help` | 显示帮助 |

### 3.2 选项参数

| 参数 | 说明 | 示例 |
|------|------|------|
| `--portal-url=URL` | Portal URL (用于 Proxy 连接) | `--portal-url=http://10.0.0.1:3001` |
| `--proxy-secret=SECRET` | Proxy 同步密钥 | `--proxy-secret=abc123` |

### 3.3 示例

```bash
# 完整部署 (模拟模式)
./start.sh simulate

# 完整部署 (生产模式)
./start.sh mvp

# 仅启动 Portal
./start.sh portal

# 启动 Proxy 连接远端 Portal
./start.sh proxy --portal-url=http://10.0.0.1:3001

# 查看状态
./start.sh status

# 停止所有服务
./start.sh stop
```

## 4. 单机部署方案

### 4.1 架构图

```
┌─────────────────────────────────────────────────────────────┐
│                    单机部署 (./start.sh mvp)                 │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  ┌──────────────┐    ┌──────────────┐    ┌──────────────┐  │
│  │  PostgreSQL   │    │    Redis     │    │    Portal    │  │
│  │   (5432)      │    │   (6379)     │    │   (3001)     │  │
│  └──────────────┘    └──────────────┘    └──────────────┘  │
│         │                   │                   │          │
│         └───────────────────┼───────────────────┘          │
│                             │                              │
│                       ┌──────────────┐                     │
│                       │    Proxy     │                     │
│                       │   (8080)     │                     │
│                       └──────────────┘                     │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

### 4.2 配置文件

配置文件位置: `/home/ubuntu/workspace/ai-portal-go/.env.go-platform`

```bash
# 数据库配置
DB_PASSWORD=your-db-password

# Redis 配置
REDIS_PASSWORD=your-redis-password

# JWT 安全配置
JWT_SECRET=your-jwt-secret

# 默认管理员配置
ADMIN_EMAIL=admin@example.com
ADMIN_PASSWORD=<ADMIN_PASSWORD>
ADMIN_NAME=Admin

# Proxy 同步密钥
PROXY_SYNC_SECRET=your-sync-secret

# Portal URL
PORTAL_URL=http://localhost:3001
```

### 4.3 启动步骤

```bash
# 1. 进入 Portal 目录
cd /home/ubuntu/workspace/ai-portal-go

# 2. 首次运行 (会自动创建配置文件)
./start.sh mvp

# 3. 查看状态
./start.sh status

# 4. 访问服务
# Portal: http://localhost:3001
# Proxy:  http://localhost:8080
```

### 4.4 停止和重启

```bash
# 停止所有服务
./start.sh stop

# 重新构建并启动
./start.sh rebuild

# 完全重置 (删除所有数据)
./start.sh clean-all
```

## 5. 分布式部署方案

### 5.1 架构图

```
┌─────────────────────────────────────────────────────────────┐
│                     服务器 A (Portal)                        │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  ┌──────────────┐    ┌──────────────┐    ┌──────────────┐  │
│  │  PostgreSQL   │    │    Redis     │    │    Portal    │  │
│  │   (5432)      │    │   (6379)     │    │   (3001)     │  │
│  └──────────────┘    └──────────────┘    └──────────────┘  │
│                                                             │
│  开放端口: 3001 (Portal API)                                │
│  开放端口: 5432 (PostgreSQL, 仅内部)                        │
│  开放端口: 6379 (Redis, 仅内部)                             │
│                                                             │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│                     服务器 B (Proxy)                         │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  ┌──────────────────────────────────────────────────────┐  │
│  │                     Proxy (8080)                      │  │
│  │  - 本地内存限流 (无 Redis)                            │  │
│  │  - 本地内存 Key Pool                                  │  │
│  │  - 本地内存 Affinity Cache                            │  │
│  │  - 连接 Portal 同步 Key Pool                          │  │
│  └──────────────────────────────────────────────────────┘  │
│                                                             │
│  开放端口: 8080 (Proxy API)                                 │
│                                                             │
│  环境变量:                                                   │
│  AI_PROXY_PORTAL_URL=http://10.0.0.1:3001                  │
│  AI_PROXY_PORTAL_SYNC_SECRET=your-sync-secret              │
│  AI_PROXY_REDIS_HOST=  (留空，不使用 Redis)                 │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

### 5.2 Portal 服务器部署

```bash
# 1. 在服务器 A 上
cd /home/ubuntu/workspace/ai-portal-go

# 2. 仅启动 Portal
./start.sh portal

# 3. 查看状态
./start.sh status

# 4. 记录服务器 A 的 IP 地址 (例如 10.0.0.1)
```

### 5.3 Proxy 服务器部署

```bash
# 1. 在服务器 B 上
cd /home/ubuntu/workspace/ai-portal-go

# 2. 启动 Proxy 连接远端 Portal
./start.sh proxy --portal-url=http://10.0.0.1:3001 --proxy-secret=your-sync-secret

# 3. 查看状态
./start.sh status
```

### 5.4 分布式部署配置

#### ⚠️ 密钥一致性要求（关键）

以下 5 个变量 **必须在 Portal 和所有 Proxy 节点上完全一致**。任何一个不一致都会导致服务不可用：

| 变量 | 用途 | 不一致的后果 |
|------|------|-------------|
| `JWT_SECRET` | Proxy 验证 Portal 返回的 JWT | API key 验证全部失败，返回 401 |
| `REDIS_PASSWORD` | Proxy 连接 Portal 的 Redis | Redis 连接失败，限流/缓存/队列全崩 |
| `PROXY_SYNC_SECRET` | Portal Webhook 签名验证 | Webhook 请求被 Portal 拒绝（403） |
| `ADMIN_SECRET` | Proxy 管理接口认证 | `/admin/*` 接口无法调用 |
| `ENCRYPTION_KEY` | 解密 Portal 同步的上游 API key | 上游 key 解密失败，所有请求 503 |

**生成建议**：所有节点共用同一份 `.env` 或 `.env.template`，避免手动输入出错。

**Portal 服务器 (.env.go-platform)**:
```bash
DB_PASSWORD=your-db-password
REDIS_PASSWORD=your-redis-password
JWT_SECRET=your-jwt-secret
ENCRYPTION_KEY=your-encryption-key
ADMIN_EMAIL=admin@example.com
ADMIN_PASSWORD=<ADMIN_PASSWORD>
PROXY_SYNC_SECRET=your-sync-secret
ADMIN_SECRET=your-admin-secret
```

**Proxy 服务器 (环境变量)**:
```bash
# 设置环境变量
export PORTAL_URL=http://10.0.0.1:3001
export PORTAL_SYNC_SECRET=your-sync-secret
export JWT_SECRET=your-jwt-secret
export REDIS_PASSWORD=your-redis-password
export ENCRYPTION_KEY=your-encryption-key
export ADMIN_SECRET=your-admin-secret

# 或使用 .env.template → cp .env.template .env → 填入值 → source .env
```

## 6. PostgreSQL 使用说明

### 6.1 数据库配置

| 配置项 | 默认值 | 说明 |
|--------|--------|------|
| `POSTGRES_DB` | `ai_proxy_portal` | 数据库名 |
| `POSTGRES_USER` | `postgres` | 用户名 |
| `POSTGRES_PASSWORD` | 见配置文件 | 密码 |
| 端口 | `5432` | 监听端口 |

### 6.2 数据库表结构

**主要表**:
- `users` - 用户表
- `api_keys` - API 密钥表
- `user_subscriptions` - 用户订阅表
- `usage_records` - 使用记录表
- `upstream_keys` - 上游密钥表
- `audit_logs` - 审计日志表

### 6.3 数据库操作

```bash
# 连接数据库
docker exec -it ai-portal-postgres psql -U postgres -d ai_proxy_portal

# 查看表
\dt

# 查看用户
SELECT id, email, role, balance FROM users;

# 查看 API 密钥
SELECT id, key_prefix, status, access_mode FROM api_keys;

# 查看上游密钥
SELECT id, key_preview, provider, enabled FROM upstream_keys;
```

### 6.4 数据备份

```bash
# 备份数据库
docker exec ai-portal-postgres pg_dump -U postgres ai_proxy_portal > backup.sql

# 恢复数据库
docker exec -i ai-portal-postgres psql -U postgres ai_proxy_portal < backup.sql
```

## 7. Redis 使用建议

### 7.1 Redis 使用场景

| 模块 | Redis 用途 | 是否必须 |
|------|-----------|---------|
| **Rate Limit** | 分布式限流同步 | ❌ 可选 |
| **Key Pool** | 持久化 key 状态 | ❌ 可选 |
| **Affinity Cache** | 缓存亲和性数据 | ❌ 可选 |
| **Session** | 用户会话存储 | ❌ 可选 |

### 7.2 单机部署 (无 Redis)

**适用场景**:
- RPM < 80-100K
- 单机部署
- 开发/测试环境

**优点**:
- 部署简单，无需额外组件
- 内存访问速度快
- 无网络延迟

**缺点**:
- 不支持分布式限流
- 重启后数据丢失
- 不支持多实例共享状态

**配置**:
```bash
# 不设置 Redis 或设置为空
AI_PROXY_REDIS_HOST=
```

### 7.3 单机部署 (有 Redis)

**适用场景**:
- RPM > 80-100K
- 需要持久化限流状态
- 需要多实例共享状态

**优点**:
- 支持分布式限流
- 数据持久化
- 支持多实例共享

**缺点**:
- 部署复杂，需要额外组件
- 有网络延迟

**配置**:
```bash
# 设置 Redis
AI_PROXY_REDIS_HOST=localhost
AI_PROXY_REDIS_PORT=6379
AI_PROXY_REDIS_PASSWORD=your-password
```

### 7.4 分布式部署 (共享 Redis)

**适用场景**:
- 多个 Proxy 实例
- 需要全局限流
- 需要共享 Key Pool 状态

**架构**:
```
┌─────────────────────────────────────────────────────────────┐
│                    共享 Redis 架构                           │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  ┌──────────────┐    ┌──────────────┐    ┌──────────────┐  │
│  │   Portal     │    │    Redis     │    │   Proxy 1    │  │
│  │   (3001)     │◄──►│   (6379)     │◄──►│   (8080)     │  │
│  └──────────────┘    └──────────────┘    └──────────────┘  │
│                             ▲                               │
│                             │                               │
│                       ┌──────────────┐                     │
│                       │   Proxy 2    │                     │
│                       │   (8081)     │                     │
│                       └──────────────┘                     │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

**配置**:
```bash
# 所有 Proxy 实例使用相同 Redis
AI_PROXY_REDIS_HOST=redis-server-ip
AI_PROXY_REDIS_PORT=6379
AI_PROXY_REDIS_PASSWORD=your-password
```

### 7.5 性能对比

| 方案 | RPM 能力 | 延迟 | 持久化 | 分布式 |
|------|---------|------|--------|--------|
| 无 Redis (本地内存) | 80-100K | < 1μs | ❌ | ❌ |
| 有 Redis (单机) | > 100K | ~1ms | ✅ | ❌ |
| 有 Redis (分布式) | > 100K | ~1-5ms | ✅ | ✅ |

### 7.6 建议

| 场景 | 建议 |
|------|------|
| 开发/测试 | 无 Redis |
| 单机生产 (RPM < 80K) | 无 Redis |
| 单机生产 (RPM > 80K) | 有 Redis |
| 分布式部署 | 共享 Redis |
| 高可用部署 | Redis Cluster |

## 8. 常见问题

### 8.1 端口冲突

```bash
# 检查端口占用
lsof -i :3001
lsof -i :8080
lsof -i :5432
lsof -i :6379

# 停止占用端口的进程
kill -9 <PID>
```

### 8.2 Docker 容器问题

```bash
# 查看容器状态
docker ps -a

# 查看容器日志
docker logs ai-portal-portal
docker logs ai-proxy-go

# 重启容器
docker restart ai-portal-portal
docker restart ai-proxy-go

# 删除容器
docker rm -f ai-portal-portal
docker rm -f ai-proxy-go
```

### 8.3 数据库连接问题

```bash
# 检查 PostgreSQL 是否运行
docker exec ai-portal-postgres pg_isready -U postgres

# 检查连接
docker exec -it ai-portal-postgres psql -U postgres -d ai_proxy_portal

# 查看日志
docker logs ai-portal-postgres
```

### 8.4 Redis 连接问题

```bash
# 检查 Redis 是否运行
docker exec ai-portal-redis redis-cli ping

# 检查连接 (带密码)
docker exec ai-portal-redis redis-cli -a your-password ping

# 查看日志
docker logs ai-portal-redis
```

## 9. 监控和日志

### 9.1 查看日志

```bash
# 查看所有日志
./start.sh logs

# 查看 Portal 日志
./start.sh logs portal

# 查看 Proxy 日志
./start.sh logs proxy
```

### 9.2 健康检查

```bash
# 检查 Portal
curl http://localhost:3001/health

# 检查 Proxy
curl http://localhost:8080/health

# 检查 Proxy 状态
curl http://localhost:8080/api/status
```

### 9.3 性能监控

```bash
# 查看容器资源使用
docker stats

# 查看系统资源
top
htop
```

## 10. 安全建议

### 10.1 密码安全

- 使用强密码
- 定期更换密码
- 不要将密码提交到代码仓库

### 10.2 网络安全

- 仅开放必要端口
- 使用防火墙限制访问
- 使用 HTTPS (生产环境)

### 10.3 数据安全

- 定期备份数据库
- 加密敏感数据
- 启用审计日志

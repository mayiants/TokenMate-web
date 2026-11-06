# 密码与密钥一致性问题分析

> **ACTIVE** — 2026-05-21
> 分析 Portal 和 Proxy 之间密码/密钥的一致性问题，给出修复方案。

## 1. 问题概述

Portal 和 Proxy 共享多组密钥（JWT_SECRET、PROXY_SYNC_SECRET、REDIS_PASSWORD 等），
但当前的部署脚本存在以下问题：

- **硬编码密钥**：`ai-proxy-go/docker-compose.proxy.yml` 内嵌明文密钥
- **生成方式不一致**：`create_portal_compose()` 用 `<< 'EOF'` + `--env-file`（变量运行时解析），
  `create_proxy_compose()` 用 `<< EOF`（变量在生成时展开）
- **缺少 CLI 参数**：分布式部署时 Proxy 端无法传入 `JWT_SECRET` 和 `REDIS_PASSWORD`
- **Secret 传递机制不明确**：`--proxy-secret` 的原理没有文档

## 2. 各组密钥的作用域

| 密钥 | Portal 需要 | Proxy 需要 | 是否两端须一致 |
|---|---|---|---|
| `DB_PASSWORD` | ✅ PostgreSQL | ❌ | — |
| `REDIS_PASSWORD` | ✅ 启动 Redis | ✅ 连接 Portal 的 Redis | ✅ |
| `JWT_SECRET` | ✅ 签发 token | ✅ 验证 token | ✅ |
| `PROXY_SYNC_SECRET` | ✅ 验证 sync 请求 | ✅ 签名 sync 请求 | ✅ |
| `ADMIN_SECRET` | ❌ | ✅ Proxy 管理接口认证 | —（但常复用 SYNC_SECRET） |
| `ENCRYPTION_KEY` | ✅ 加密上游 Key | ❌ | — |
| `ADMIN_PASSWORD` | ✅ 创建管理员 | ❌ | — |

## 3. `--proxy-secret` 是什么？怎么传递的？

`--proxy-secret` 的值就是要同步给 Proxy 的 `PORTAL_SYNC_SECRET`。

### 验证流程

```
Proxy                              Portal
  │                                  │
  │  GET /api/admin/key-pool/sync    │
  │  Header: x-proxy-secret=<值>     │
  │ ──────────────────────────────→  │
  │                                  │ 校验 Header == config.Portal.SyncSecret
  │                                  │ (即 PROXY_SYNC_SECRET 环境变量)
  │  ←────────────────────────────── │
```

### 传递方式

**`--proxy-secret` 不是从 Portal 自动传递的。它是一个预共享密钥（Pre-Shared Secret），
两端通过配置文件或 CLI 参数各自设置：**

- Portal 端：读取 `.env.go-platform` 中的 `PROXY_SYNC_SECRET`
- Proxy 端：通过 `start.sh proxy --proxy-secret=xxx` 传入
- 或者两端都用同一个 `.env.go-platform` 文件

没有自动分发机制——部署者需要手动保证两端值一致。

## 4. 当前脚本的问题

### 4.1 `ai-proxy-go/docker-compose.proxy.yml` 硬编码

这个文件是静态的，里面有明文的密钥值。虽然 `start.sh proxy` 会通过 `create_proxy_compose()`
覆盖它，但如果有人直接运行 `docker compose -f ai-proxy-go/docker-compose.proxy.yml up`，
就会使用硬编码的值。

### 4.2 `create_proxy_compose()` 使用 shell 展开

当前 `create_proxy_compose()` 使用 `<< EOF` heredoc，shell 在生成文件时就把变量值写死了：

```bash
cat > "${PROXY_DIR}/docker-compose.proxy.yml" << EOF
...
      JWT_SECRET: ${jwt_secret}
...
EOF
```

而 `create_portal_compose()` 使用 `<< 'EOF'`，保留 `${VAR}` 引用，运行时通过 `--env-file` 注入：

```bash
cat > "${PORTAL_DIR}/docker-compose.portal.yml" << 'EOF'
...
      JWT_SECRET: ${JWT_SECRET}
...
EOF
```

### 4.3 分布式部署缺少 CLI 参数

Proxy 端命令 `start.sh proxy` 只支持 `--portal-url` 和 `--proxy-secret` 两个参数。
`JWT_SECRET` 和 `REDIS_PASSWORD` 只能通过 `.env.go-platform` 文件传递——在分布式场景中，
如果 Proxy 服务器没有这个文件，这两个值就会为空。

## 5. 修复方案

### 5.1 删除静态的 `ai-proxy-go/docker-compose.proxy.yml`

该文件应移出版本控制（或加入 `.gitignore`），因为 `start.sh proxy` 每次都会动态生成它。

### 5.2 统一 `create_proxy_compose()` 的生成方式

改为与 Portal 一致：使用 `<< 'EOF'`，保留 `${VAR}` 引用，运行 `docker compose` 时通过
`--env-file .env.go-platform` 注入变量。

需要在生成 compose 前将函数参数（portal_url, proxy_secret 等）export 为环境变量，
以便 docker compose 解析。

### 5.3 添加两个 CLI 参数

给 `start.sh proxy` 添加：

- `--jwt-secret=<JWT_SECRET>` — 预共享的 JWT 密钥，用于验证用户 token
- `--redis-password=<REDIS_PASSWORD>` — Portal 端 Redis 的密码

### 5.4 部署流程建议

**单机部署（Portal + Proxy 同机）：**

```bash
./start.sh simulate    # 或 ./start.sh mvp
```
start.sh 会自动读取 `.env.go-platform`，两个 compose 共享同一份配置。

**分布式部署（两台独立服务器）：**

Portal 服务器：
```bash
./start.sh portal
```

Proxy 服务器：
```bash
# 需要四个参数：portal-url（必选）, proxy-secret, jwt-secret, redis-password
./start.sh proxy \
  --portal-url=http://portal-server:3001 \
  --proxy-secret=<与 Portal 的 PROXY_SYNC_SECRET 一致> \
  --jwt-secret=<与 Portal 的 JWT_SECRET 一致> \
  --redis-password=<与 Portal 的 REDIS_PASSWORD 一致>
```

或者更简单：将 Portal 的 `.env.go-platform` 复制到 Proxy 服务器，然后：

```bash
./start.sh proxy --portal-url=http://portal-server:3001
```
此时 `load_config()` 会加载 `.env.go-platform`，获取所有密钥。

---

## 6. 已实施的修复（2026-05-21）

### 6.1 `check_config()` 密钥随机化

**修改内容**：`start.sh` 的 `check_config()` 函数。

**修改前**：首次生成 `.env.go-platform` 时使用硬编码的固定密钥：
```bash
DB_PASSWORD=<DB_PASSWORD_HASH>
JWT_SECRET=<JWT_SECRET>
ENCRYPTION_KEY=<ENCRYPTION_KEY>   # 与 PROXY_SYNC_SECRET 相同！
```

**修改后**：全部由 `openssl rand -hex 32` 随机生成：
```bash
DB_PASSWORD=$(openssl rand -hex 32)
REDIS_PASSWORD=$(openssl rand -hex 32)
JWT_SECRET=$(openssl rand -hex 32)
ENCRYPTION_KEY=$(openssl rand -hex 32)
PROXY_SYNC_SECRET=$(openssl rand -hex 32)
```
`ADMIN_PASSWORD` 保留为 `<ADMIN_PASSWORD>`（管理员部署后自行修改）。
`ADMIN_SECRET` 复用 `PROXY_SYNC_SECRET`。

### 6.2 `create_proxy_compose()` 改为引用式生成

**修改内容**：`start.sh` 的 `create_proxy_compose()` 和 `start_proxy()`。

**修改前**：使用 `<< EOF`（shell 展开），值在生成时写死到 YAML 文件。

**修改后**：使用 `<< 'EOF'`（字面量），保留 `${VAR}` 引用，运行时通过 `--env-file` 注入。

生成 compose 前将函数参数 export 为环境变量：
```bash
export PORTAL_URL="${portal_url}"
export PORTAL_SYNC_SECRET="${proxy_secret}"
export ADMIN_SECRET="${ADMIN_SECRET:-${PROXY_SYNC_SECRET:-}}"
# JWT_SECRET：CLI 参数 > 环境变量 > 空
if [[ -n "${JWT_SECRET_ARG}" ]]; then
    export JWT_SECRET="${JWT_SECRET_ARG}"
fi
# REDIS_PASSWORD：CLI 参数 > 环境变量 > 空
if [[ -n "${REDIS_PASSWORD_ARG}" ]]; then
    export REDIS_PASSWORD="${REDIS_PASSWORD_ARG}"
fi
```

`start_proxy()` 运行时传递 `--env-file`：
```bash
${DOCKER_COMPOSE_CMD} -f docker-compose.proxy.yml ${env_flag} up -d --build
```

### 6.3 新增两个 CLI 参数

| 参数 | 用途 |
|---|---|
| `--jwt-secret=<JWT_SECRET>` | 分布式部署时传入 JWT 密钥 |
| `--redis-password=<REDIS_PASSWORD>` | 分布式部署时传入 Redis 密码 |

### 6.4 全项目默认密码统一

所有测试脚本、代码 fallback、文档中写死的 `<ADMIN_PASSWORD>` 统一改为 `<ADMIN_PASSWORD>`，涉及 15 个文件约 25 处。

### 6.5 遗留问题

- `ai-proxy-go/docker-compose.proxy.yml` 仍有静态硬编码副本——`start.sh proxy` 虽会覆盖，但直接
  `docker compose up` 会有风险。建议移出 git 或加 `.gitignore`。
- `upstream_keys` 使用 `ENCRYPTION_KEY` 加密存储，clean-all 后新密钥无法解密旧数据，
  需重新填入 API Key。

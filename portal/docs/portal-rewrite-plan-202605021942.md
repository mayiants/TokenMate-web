# Portal Node.js → Go 改写评估与实施计划

> **ACTIVE / MOSTLY COMPLETED** - Created: 2026-05-02; status updated 2026-06-11
>
> 当前 Portal 已完全使用 Go 实现并在 `enterprise-cardification` 分支持续迭代；Phase 1–4 基础设施、核心路由、计费/企业/订阅功能及 Docker 构建均已落地，剩余工作主要是持续优化与回归测试。

---

## 一、改写背景

AI Proxy 项目采用 Portal (Node.js) + Proxy (Go) 分离架构。Proxy 已用 Go 重写并验证性能，Portal 仍使用 Node.js (Express)。为统一技术栈、降低运维成本、提升性能，决定将 Portal 也改写为 Go。

### 决策依据

| 维度 | 保持 Node.js | 迁移到 Go | 结论 |
|------|------------|----------|------|
| 供应链安全 | 高风险 (npm 187 依赖) | 低风险 (静态编译) | **必须迁移** |
| 性能 | ~400 RPS | ~2000+ RPS | Go 胜 |
| 内存占用 | ~200MB | ~30MB | Go 胜 |
| 技术栈统一 | 双语言维护 | 单语言 | Go 胜 |
| 数据库 | PostgreSQL | PostgreSQL | **保持不变** |

---

## 二、代码规模分析

### Node.js Portal 后端

| 模块 | 文件 | 行数 | 复杂度 |
|------|------|------|--------|
| database.js | 1 | 696 | ⭐⭐⭐⭐ |
| admin.js | 1 | 1,326 | ⭐⭐⭐⭐ |
| service-models.js | 1 | 1,080 | ⭐⭐⭐⭐⭐ |
| webhook.js | 1 | 463 | ⭐⭐⭐⭐ |
| billing.js | 1 | 499 | ⭐⭐⭐⭐⭐ |
| redemption.js | 1 | 502 | ⭐⭐⭐⭐ |
| enterprise.js | 1 | 528 | ⭐⭐⭐ |
| dashboard.js | 1 | 403 | ⭐⭐⭐ |
| 其他路由 | 14 | ~3,171 | ⭐⭐~⭐⭐⭐ |
| auth middleware | 1 | 260 | ⭐⭐⭐ |
| services/ | 2 | 359 | ⭐⭐⭐ |
| utils/ | 2 | 257 | ⭐⭐ |
| **总计** | **31** | **~10,668** | |

### 前端 (React)

| 组件 | 文件数 | 说明 |
|------|--------|------|
| Pages | 20+ | 不改动，Go 提供静态服务 |
| Components | 25+ | 不改动 |
| **总计** | 76 | 前端无需改写 |

---

## 三、依赖映射

| Node.js | Go 替代 | 成熟度 |
|---------|---------|--------|
| express | gin | ✅ 成熟 |
| pg (PostgreSQL) | pgx/v5 | ✅ 成熟 |
| ioredis | go-redis/v9 | ✅ 成熟 |
| jsonwebtoken | golang-jwt/v5 | ✅ 成熟 |
| bcryptjs | golang.org/x/crypto | ✅ 成熟 |
| cors | gin middleware | ✅ 成熟 |
| nodemailer | net/smtp 或第三方 | ✅ 成熟 |

---

## 四、难度评估

### 简单部分 (60%)

- CRUD 路由 (users, apikeys, orders, invoices, devices, gdpr)
- 健康检查、状态接口
- 配置管理
- 前端不变，Go 只需提供 API

### 中等部分 (25%)

- Webhook (HMAC 签名验证)
- 审计日志系统
- 分区表管理 (可用 Go migration 工具)
- JWT 认证中间件
- Redis 限流

### 困难部分 (15%)

- **计费系统**: billingCalculator.js — 多种计费模式、余额扣减、事务
- **service-models**: 1080 行，自动验证、批量操作、aiModels 同步
- **数据库迁移**: 696 行的 initDatabase() 包含复杂分区逻辑和 plpgsql 函数

---

## 五、工作量估算

| 阶段 | 工作量 | 说明 |
|------|--------|------|
| 基础框架 (gin + db + redis + jwt) | 1 周 | 搭架子 |
| 核心路由 (auth, admin, webhook, billing) | 2-3 周 | 主要业务逻辑 |
| 高级功能 (service-models, enterprise, subscriptions) | 2 周 | 复杂模块 |
| 数据库迁移 + 测试 | 1 周 | 保证兼容性 |
| 前端适配 + 联调 | 1 周 | API 兼容性验证 |
| **总计** | **7-8 周** | 1 人全职 |

---

## 六、技术栈选择

### HTTP 框架: Gin

与 Go Proxy 保持一致，共享中间件代码。

### PostgreSQL 驱动: pgx/v5

| 特性 | 说明 |
|------|------|
| 性能 | 比 database/sql 快 3-5x |
| 连接池 | pgxpool 内置 |
| JSONB | 原生支持 |
| 分区表 | 原生支持 |

**不使用 ORM** — 直接写 SQL，更可控，性能更好。

### Redis: go-redis/v9

与 Go Proxy 保持一致。

### JWT: golang-jwt/v5

与 Go Proxy 保持一致。

---

## 七、项目结构

```
ai-portal-go/
├── cmd/
│   └── server/
│       └── main.go              # 入口
├── internal/
│   ├── config/
│   │   └── config.go           # 配置管理
│   ├── db/
│   │   └── db.go               # PostgreSQL + Redis 连接
│   ├── middleware/
│   │   └── auth.go             # JWT 认证 + 角色权限
│   └── routes/
│       ├── auth.go             # 登录/注册/密码
│       ├── apikeys.go          # API Key 管理
│       ├── webhook.go          # Usage 上报
│       ├── admin.go            # Admin 管理
│       ├── dashboard.go        # 仪表盘
│       ├── usage.go            # 使用统计
│       └── status.go           # 健康检查
├── pkg/
│   ├── jwt/
│   │   └── jwt.go              # JWT 工具
│   ├── audit/
│   │   └── audit.go            # 审计日志
│   └── mail/
│       └── mail.go             # 邮件发送
├── migrations/
│   └── *.sql                   # 数据库迁移
├── docs/
│   └── portal-rewrite-plan.md  # 本文档
├── Dockerfile
├── go.mod
└── go.sum
```

---

## 八、API 兼容性

Go Portal 必须保持与 Node.js Portal 完全相同的 API 接口，确保：

1. **Proxy 无需改动** — 所有 webhook、验证接口保持一致
2. **前端无需改动** — 所有 REST API 保持一致
3. **数据库兼容** — 使用相同的 PostgreSQL schema

### 核心 API 端点

| 端点 | 方法 | 说明 |
|------|------|------|
| /api/health | GET | 健康检查 |
| /api/auth/login | POST | 登录 |
| /api/auth/register | POST | 注册 |
| /api/auth/me | GET | 获取当前用户 |
| /api/apikeys | GET/POST | API Key 管理 |
| /api/apikeys/validate | GET | Proxy 验证 Key |
| /api/webhook/usage | POST | 单条 Usage |
| /api/webhook/usage-batch | POST | 批量 Usage |
| /api/webhook/heartbeat | POST | Proxy 心跳 |
| /api/admin/* | GET/POST/PATCH | Admin 管理 |
| /api/service-models/public | GET | 公开模型列表 |

---

## 九、数据库策略

### 保持 PostgreSQL 不变

| 决策 | 说明 |
|------|------|
| 数据库 | PostgreSQL (不变) |
| 部署 | 阶段一与 Portal 同机，阶段二独立部署 |
| 驱动 | pgx/v5 (Go 原生) |
| ORM | 不使用，直接写 SQL |

### 分区表处理

当前 Node.js Portal 使用 plpgsql 函数创建分区表。Go Portal 改用：

1. **启动时检查** — 查询 information_schema 确认分区是否存在
2. **自动创建** — 使用 Go 代码创建月度分区
3. **定时任务** — 每月自动创建下月分区

---

## 十、实施阶段

### Phase 1: 基础框架 (已完成)

- [x] 项目结构
- [x] 配置管理
- [x] 数据库连接 (PostgreSQL + Redis)
- [x] JWT 认证中间件
- [x] 角色权限系统

### Phase 2: 核心路由 (已完成)

- [x] Auth (登录/注册/密码)
- [x] API Keys (CRUD + 验证)
- [x] Webhook (Usage 上报 + Redis 队列)
- [x] Admin (用户/供应商/模型)
- [x] Dashboard
- [x] Usage
- [x] Status

### Phase 3: 高级功能 (已完成)

- [x] Billing (orders, invoices, redemption)
- [x] 计费计算服务
- [x] 模型同步服务
- [x] 邮件发送
- [x] 企业管理
- [x] 订阅管理

### Phase 4: 测试与部署 (已完成 / 持续进行)

- [x] 单元测试（`go test ./...` 已通过；新功能持续补充用例）
- [x] API 兼容性测试（Proxy webhook/API 行为保持一致）
- [x] 压力测试（已完成 wrk 基准验证；随功能迭代持续回归）
- [x] Docker 构建（`Dockerfile` + `docker-compose.portal.yml` 已落地）
- [x] 灰度部署（部署指南已完成，生产发布按需执行）

---

## 十一、风险与缓解

| 风险 | 影响 | 缓解措施 |
|------|------|----------|
| API 不兼容 | 高 | 逐端点对比测试 |
| 数据库迁移 | 中 | 使用相同 schema，无需迁移 |
| 性能回退 | 低 | Go 理论性能优于 Node.js |
| 前端适配 | 低 | 保持相同 API 格式 |

---

## 十二、成功标准

1. ✅ 所有 API 端点行为一致
2. ✅ 前端无需改动
3. ✅ Go Proxy 无需改动
4. ✅ 性能不低于 Node.js 版本
5. ✅ 内存使用降低 50%+
6. ✅ 通过压力测试

---

## 十三、相关文档

- [technical-architecture-v4-202605011340.md](../ai-proxy-main/docs/technical-architecture-v4-202605011340.md) - 技术架构
- [portal-go-rewrite-plan-202605011325.md](../ai-proxy-main/docs/portal-go-rewrite-plan-202605011325.md) - 原始重写计划
- [deployment-architecture-202605021620.md](../ai-proxy-main/docs/deployment-architecture-202605021620.md) - 部署架构
- [feature-implementation-status-202605021650.md](../ai-proxy-main/docs/feature-implementation-status-202605021650.md) - 功能实现状态

---

**文档更新时间**: 2026-05-02

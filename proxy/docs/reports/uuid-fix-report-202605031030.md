# Go Portal + Proxy UUID 类型修复报告

> **HISTORICAL** — May 2026 UUID fix report.

---

## 一、问题背景

在测试 Go Portal + Proxy 时发现以下关键问题：

1. **管理员登录失败**: `admin@example.com` 无法登录，返回 "Invalid email or password"
2. **Proxy API Key 验证失败**: 返回 "Invalid API key"
3. **类型不匹配**: 数据库使用 `UUID` 类型存储 ID，但 Go 代码使用 `int` 类型

---

## 二、根本原因分析

### 2.1 数据库 Schema

```sql
-- users 表
id UUID PRIMARY KEY DEFAULT gen_random_uuid()

-- api_keys 表
id UUID PRIMARY KEY DEFAULT gen_random_uuid()
user_id UUID REFERENCES users(id)
```

### 2.2 Go 代码类型定义

```go
// 原始定义 (错误)
type Claims struct {
    UserID   int    `json:"user_id"`
    APIKeyID int    `json:"api_key_id"`
    ...
}

// JWT Manager
func (m *Manager) Generate(userID int, email, role string) (string, error)

// Audit Logger
func (l *Logger) Log(ctx context.Context, userID int, ...)
```

### 2.3 问题链路

```
1. Portal 登录流程:
   - SQL: SELECT id::text, ... FROM users WHERE email = ?
   - Scan: var userID int (类型不匹配，Scan 失败)
   - 结果: 返回 "Invalid email or password"

2. Proxy 验证流程:
   - Portal 返回: {"user_id": "uuid-string", "api_key_id": "uuid-string"}
   - Proxy 解析: var result struct { UserID int `json:"user_id"` }
   - 结果: JSON 解析失败，返回 "Invalid API key"
```

---

## 三、修复内容

### 3.1 Portal 修复

| 文件 | 修改内容 |
|------|----------|
| `pkg/jwt/jwt.go` | `UserID` 从 `int` 改为 `string` |
| `pkg/audit/audit.go` | `userID` 参数从 `int` 改为 `string` |
| `internal/routes/auth.go` | 所有 `userID` 变量改为 `string`，SQL 添加 `::text` 和 `::uuid` 转换 |
| `internal/routes/apikeys.go` | 所有 `GetInt` 改为 `GetString`，SQL 添加类型转换 |
| `internal/routes/dashboard.go` | `GetInt` 改为 `GetString` |
| `internal/routes/usage.go` | `GetInt` 改为 `GetString` |
| `internal/routes/admin.go` | 添加 `RequireAuth()` 中间件 |
| `start.sh` | `load_config()` 使用 `set -a; source; set +a` 替代 `export $(grep | xargs)` |

### 3.2 Proxy 修复

| 文件 | 修改内容 |
|------|----------|
| `internal/auth/auth.go` | `Claims.UserID/APIKeyID` 从 `int` 改为 `string`，验证响应解析字段改为 `string` |
| `internal/ratelimit/ratelimit.go` | `GetInt` 改为 `GetString`，`zap.Int` 改为 `zap.String` |
| `internal/server/handlers.go` | 模拟模式 0ms 延迟不再强制设为 100ms |
| `internal/ratelimit/ratelimit.go` | RPM 预设值扩展: `{60, 120, 300, 1000, 5000, 10000, 50000, 100000}` |

---

## 四、关键代码变更

### 4.1 JWT Claims (Portal)

```go
// 修改前
type Claims struct {
    UserID     int    `json:"user_id"`
    ...
}

// 修改后
type Claims struct {
    UserID     string `json:"user_id"`
    ...
}
```

### 4.2 登录路由 (Portal)

```go
// 修改前
var userID int
err := r.db.QueryRow(ctx,
    `SELECT id, name, email, password_hash, role FROM users WHERE email = $1`,
    email,
).Scan(&userID, ...)

// 修改后
var userID string
err := r.db.QueryRow(ctx,
    `SELECT id::text, name, email, password_hash, role FROM users WHERE email = $1`,
    email,
).Scan(&userID, ...)
```

### 4.3 Proxy 验证响应解析

```go
// 修改前
var result struct {
    UserID   int `json:"user_id"`
    APIKeyID int `json:"api_key_id"`
    ...
}

// 修改后
var result struct {
    UserID   string `json:"user_id"`
    APIKeyID string `json:"api_key_id"`
    ...
}
```

### 4.4 Admin 路由中间件

```go
// 修改前
func (r *AdminRoutes) Register(rg *gin.RouterGroup) {
    rg.GET("/users", r.auth.RequireAdmin(), r.listUsers)
    ...
}

// 修改后
func (r *AdminRoutes) Register(rg *gin.RouterGroup) {
    authGroup := rg.Use(r.auth.RequireAuth())
    authGroup.GET("/users", r.auth.RequireAdmin(), r.listUsers)
    ...
}
```

---

## 五、测试结果

### 5.1 登录测试

```bash
curl -X POST http://127.0.0.1:3001/api/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email":"admin@example.com","password":"<ADMIN_PASSWORD>"}'

# 返回
{
  "token": "eyJhbGciOiJIUzI1NiIs...",
  "user": {
    "id": "805d1643-ad99-47c1-aa5a-1120d4066494",
    "email": "admin@example.com",
    "role": "super_admin"
  }
}
```

### 5.2 API Key 验证测试

```bash
curl "http://127.0.0.1:3001/api/apikeys/validate" \
  -H "Authorization: Bearer sk-xxx"

# 返回
{
  "valid": true,
  "user_id": "805d1643-ad99-47c1-aa5a-1120d4066494",
  "api_key_id": "33e93c3d-19df-425f-a63a-0410b712fac6",
  "role": "super_admin",
  "rate_limit_rpm": 100000
}
```

### 5.3 Proxy 模拟模式测试

```bash
curl -X POST http://127.0.0.1:8080/v1/chat/completions \
  -H "Authorization: Bearer sk-xxx" \
  -d '{"model":"gpt-4","messages":[{"role":"user","content":"Hello"}]}'

# 返回
{
  "id": "chatcmpl-sim",
  "model": "gpt-4",
  "choices": [{
    "message": {"role": "assistant", "content": "Simulated response for model gpt-4"}
  }],
  "usage": {"prompt_tokens": 10, "completion_tokens": 20, "total_tokens": 30}
}
```

---

## 六、压力测试结果

### 6.1 测试配置

- 模拟模式: `SIMULATE_ENABLED=true`, `SIMULATE_LATENCY_MS=0`
- 并发数: 1000
- 总请求: 100,000

### 6.2 结果

```
Summary:
  Total:        14.4 secs
  Requests/sec: 6,936
  Status code distribution:
    [200] 100000 responses
```

---

## 七、遗留问题

1. **Rate Limit 本地计数器瓶颈**: 高并发下本地计数器成为瓶颈，需要 Redis 分布式限流
2. **Portal Sync 端点缺失**: Proxy 同步 Key Pool 和 Models 的端点返回 404
3. **前端 dist 构建流程**: 需要多步骤构建 (Node.js 构建 dist → 复制到 Go Portal)

---

## 八、相关文档

- [portal-rewrite-plan-202605021942.md](../../ai-portal-go/docs/portal-rewrite-plan-202605021942.md)
- [proxy-architecture-design-202605022300.md](../design/proxy-architecture-design-202605022300.md)
- [deployment-guide-202605030900.md](../../ai-portal-go/docs/deployment-guide-202605030900.md)

---

**文档更新时间**: 2026-05-03
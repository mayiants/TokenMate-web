# Provider 管理优化方案

> **DRAFT** - 待确认后实施

Created: 2026-05-08 10:33

## 1. 当前实现

### 1.1 数据来源

Provider 数据有两个来源：

| 来源 | 位置 | 触发时机 | 行为 |
|------|------|----------|------|
| Migration SQL | `migrations/009_seed_providers.sql` | 数据库迁移时 | 已删除 |
| initProviders() | `cmd/server/main.go` | 每次服务启动 | `ON CONFLICT DO NOTHING` |

### 1.2 initProviders() 当前代码

```go
func initProviders(database *db.DB, logger *zap.Logger) {
    providers := []struct {
        ID      string
        Name    string
        Type    string
        BaseURL string
    }{
        // International (12)
        {"openai", "OpenAI", "international", "https://api.openai.com/v1"},
        {"anthropic", "Anthropic", "international", "https://api.anthropic.com/v1"},
        // ... 共 21 个
    }

    for _, p := range providers {
        _, _ = database.Pool.Exec(ctx,
            `INSERT INTO providers (id, name, type, base_url) VALUES ($1, $2, $3, $4)
             ON CONFLICT (id) DO NOTHING`,
            p.ID, p.Name, p.Type, p.BaseURL,
        )
    }
}
```

### 1.3 Admin 管理接口

Admin 可以通过 dashboard 添加 provider：
- `POST /admin/providers` - 添加新 provider
- `POST /admin/providers/edit` - 编辑 provider
- `POST /admin/providers/:id/toggle` - 启用/禁用

---

## 2. 当前问题

### 2.1 代码与数据耦合

- Provider 配置硬编码在 Go 代码中
- 修改 provider 名称/URL 需要重新编译
- 不同部署环境可能需要不同的 provider 列表

### 2.2 更新不便

```
当前流程：
1. 修改 main.go 中的 initProviders()
2. 重新编译
3. 重新部署
4. 重启服务
```

### 2.3 冗余代码

- initProviders() 每次启动都执行，但大部分情况是 `DO NOTHING`
- 21 个 provider 定义占用代码空间

---

## 3. 优化方案

### 方案 A：完全删除 initProviders()（推荐）

**思路：** Provider 完全由 Admin 通过 Dashboard 管理

**优点：**
- 代码更简洁
- 配置完全在数据库中
- 修改 provider 不需要重新编译
- 支持不同部署环境使用不同 provider 列表

**缺点：**
- 首次启动无 provider，需 Admin 手动添加
- 新部署需要额外配置步骤

**实施：**
```go
// 删除 initProviders() 函数
// 删除 main.go 中的调用：initProviders(database, logger)
```

**配套措施：**
1. 提供默认 provider 列表文档
2. Admin Dashboard 提供"批量导入"功能（可选）
3. 首次登录引导页面提示添加 provider

---

### 方案 B：保留 initProviders() 但改为可配置

**思路：** 从配置文件读取 provider 列表，而不是硬编码

**优点：**
- 首次启动有默认 provider
- 修改 provider 不需要重新编译
- 支持不同环境使用不同配置

**缺点：**
- 仍需维护配置文件
- 配置文件与数据库数据可能不一致

**实施：**
```yaml
# providers.yaml
providers:
  - id: openai
    name: OpenAI
    type: international
    base_url: https://api.openai.com/v1
  - id: anthropic
    name: Anthropic
    type: international
    base_url: https://api.anthropic.com/v1
  # ...
```

```go
func initProviders(database *db.DB, logger *zap.Logger, configPath string) {
    // 从 YAML 文件读取 provider 列表
    // 插入数据库（ON CONFLICT DO NOTHING）
}
```

---

### 方案 C：保留 initProviders() 但添加版本控制

**思路：** 使用版本号控制 provider 列表，只在版本变化时更新

**优点：**
- 保留开箱即用体验
- 可以批量更新 provider
- 避免每次启动都执行

**缺点：**
- 实现复杂度增加
- 版本管理需要额外维护

**实施：**
```go
const PROVIDERS_VERSION = "20260508"

func initProviders(database *db.DB, logger *zap.Logger) {
    // 检查当前版本
    var currentVersion string
    db.QueryRow("SELECT value FROM system_settings WHERE key = 'providers_version'").Scan(&currentVersion)
    
    if currentVersion == PROVIDERS_VERSION {
        return // 版本一致，跳过
    }
    
    // 执行 provider 初始化
    // 更新版本号
    db.Exec("UPDATE system_settings SET value = $1 WHERE key = 'providers_version'", PROVIDERS_VERSION)
}
```

---

## 4. 方案对比

| 维度 | 方案 A (删除) | 方案 B (配置文件) | 方案 C (版本控制) |
|------|--------------|------------------|------------------|
| 代码复杂度 | 低 | 中 | 高 |
| 首次启动体验 | 需手动添加 | 自动初始化 | 自动初始化 |
| 修改灵活性 | 高（Dashboard） | 中（配置文件） | 低（需改代码） |
| 维护成本 | 低 | 中 | 高 |
| 部署一致性 | 依赖 Admin | 依赖配置文件 | 代码保证 |

---

## 5. 推荐方案

**推荐方案 A：完全删除 initProviders()**

理由：
1. **简化代码** - 减少约 50 行 Go 代码
2. **职责清晰** - Provider 管理完全由 Admin 负责
3. **灵活部署** - 不同环境可以有不同 provider 列表
4. **现代实践** - 配置与代码分离

**配套建议：**
1. 在 Admin Dashboard 添加"快速添加常用 Provider"按钮
2. 提供默认 provider 列表供参考
3. 首次登录时显示引导页面

---

## 6. 实施步骤

如果确认采用方案 A：

### Step 1: 删除 initProviders()

```go
// cmd/server/main.go
// 删除 initProviders() 函数定义
// 删除 main() 中的调用：initProviders(database, logger)
```

### Step 2: 更新文档

在 `docs/` 目录添加默认 provider 列表文档

### Step 3: Admin Dashboard 优化（可选）

- 添加"批量导入 Provider"功能
- 添加"常用 Provider"快速添加按钮

### Step 4: 测试

- 验证首次启动无 provider
- 验证 Admin 可以正常添加 provider
- 验证 provider 在重启后保留

---

## 7. 默认 Provider 列表参考

如果采用方案 A，以下是建议的默认 provider 列表，Admin 可按需添加：

### International (12)

| ID | Name | Base URL |
|----|------|----------|
| openai | OpenAI | https://api.openai.com/v1 |
| anthropic | Anthropic | https://api.anthropic.com/v1 |
| google | Google | https://generativelanguage.googleapis.com/v1beta |
| microsoft | Microsoft | https://api.openai.azure.com/v1 |
| mistral | Mistral | https://api.mistral.ai/v1 |
| groq | Groq | https://api.groq.com/openai/v1 |
| cohere | Cohere | https://api.cohere.ai/v1 |
| perplexity | Perplexity | https://api.perplexity.ai |
| replicate | Replicate | https://api.replicate.com/v1 |
| meta | Meta | https://api.llama.meta.com/v1 |
| amazon | Amazon | https://bedrock-runtime.us-east-1.amazonaws.com/v1 |
| xai | xAI | https://api.x.ai/v1 |

### Domestic (8)

| ID | Name | Base URL |
|----|------|----------|
| deepseek | DeepSeek | https://api.deepseek.com/v1 |
| zhipu | Zhipu | https://open.bigmodel.cn/api/paas/v4 |
| moonshot | Moonshot | https://api.moonshot.cn/v1 |
| minimax | MiniMax | https://api.minimax.chat/v1 |
| baidu | Baidu | https://aip.baidubce.com/rpc/2.0/ai_custom/v1 |
| alibaba | Alibaba | https://dashscope.aliyuncs.com/compatible-mode/v1 |
| tencent | Tencent | https://api.hunyuan.cloud.tencent.com/v1 |
| bytedance | ByteDance | https://ark.cn-beijing.volces.com/api/v3 |

### Other (1)

| ID | Name | Base URL |
|----|------|----------|
| other | Other | - |

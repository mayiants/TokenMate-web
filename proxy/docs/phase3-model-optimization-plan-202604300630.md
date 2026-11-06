> **HISTORICAL** — Superseded by Stage 5/6 implementation.

# 阶段 3: 模型数据优化方案

## 问题分析

### 1. 模型名称不一致
- 显示名: `Kimi K2.5` (Products 页面)
- OpenRouter: `kimi-k2-5` → `moonshot/kimi-k2-5`
- Moonshot 官方 API: `kimi-k2.5`

### 2. 价格结构细化
现代 LLM 支持缓存，价格需要细化：

| 价格类型 | 说明 | Claude 示例 |
|----------|------|-------------|
| input_cache_hit | 缓存命中 | $0.30/1M |
| input_cache_miss | 缓存未命中 | $3.00/1M |
| output | 输出价格 | $15.00/1M |

### 3. 第三方平台映射
OpenRouter 等平台有自己的 baseUrl 和 modelName，需要映射关系。

---

## 解决方案

### 数据结构（复用 service_models）

**不需要新建表**，扩展 `service_models`：

```sql
ALTER TABLE service_models ADD COLUMN IF NOT EXISTS:
  -- 细化价格
  input_price_cache_hit DECIMAL(10, 4),
  input_price_cache_miss DECIMAL(10, 4),  -- 原 input_price
  
  -- 验证与映射
  validation_status VARCHAR(20) DEFAULT 'pending',
  validation_result JSONB,
  validated_at TIMESTAMP,
  suggested_api_name VARCHAR(100),
  provider_aliases JSONB DEFAULT '{}';
```

### 字段说明

| 字段 | 用途 | 示例 |
|------|------|------|
| `product_id` | 显示标识（来自 aiModels） | `kimi-k2-5` |
| `api_model_name` | API 调用名 | `kimi-k2.5` |
| `aimodels_data->name` | Products 页面显示名 | `Kimi K2.5` |
| `provider_aliases` | 第三方平台映射 | `{"openrouter": "moonshot/kimi-k2-5"}` |
| `input_price_cache_hit` | 缓存命中价格 | `0.30` |
| `input_price_cache_miss` | 缓存未命中价格 | `0.60` |
| `output_price` | 输出价格 | `2.50` |

---

## 自动验证机制

### 工作流程

```
aiModels 同步 → 自动验证 → 生成建议 → Admin 确认
```

### 验证服务

```go
type ModelValidator struct {
    providers map[string]ProviderConfig
    client    *http.Client
}

type ValidationResult struct {
    Provider            string
    ProductID           string
    SuggestedAPIName    string    // 验证通过的 API 名称
    Available           bool
    LatencyMs           int
    ErrorType           string
    OpenRouterName      string    // 自动检测的 OpenRouter 映射
    CheckedAt           time.Time
}
```

### API 名称候选生成

```go
func generateAPINameCandidates(productID string) []string {
    // kimi-k2-5 → ["kimi-k2-5", "kimi-k2.5", "kimi-k2-5-latest", ...]
    candidates := []string{productID}
    
    // 替换 - 为 .
    if strings.Contains(productID, "-") {
        candidates = append(candidates, strings.ReplaceAll(productID, "-", "."))
    }
    
    return candidates
}
```

---

## API 端点

### 验证相关

```
POST /api/admin/models/batch-validate    # 批量验证
POST /api/admin/models/:id/validate      # 单个验证
POST /api/admin/models/:id/auto-confirm  # 一键确认
```

---

## Admin 工作量

| 场景 | 之前 | 之后 |
|------|------|------|
| 新模型确认 | 手动填写所有字段 | 一键确认 |
| API 名称错误 | 手动试错 | 自动检测 |
| 价格确认 | 手动查询官网 | 参考价格 + 可修改 |
| 批量处理 | 逐个处理 | 批量验证 + 批量确认 |

---

## 实施优先级

| 优先级 | 功能 | 工作量 | 价值 |
|--------|------|--------|------|
| **P0** | 数据库扩展（细化价格 + provider_aliases） | 低 | 高 |
| **P1** | 自动验证服务 | 中 | 高 |
| **P2** | Admin 界面增强 | 中 | 中 |
| **P3** | 官方价格同步 | 高 | 中 |

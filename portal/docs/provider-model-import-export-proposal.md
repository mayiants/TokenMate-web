# Provider & Model 导入导出方案

> **提案时间**: 2026-05-29
> **关联功能**: 上行 Key 已支持导入导出，Providers 和 Models 暂缺

---

## 一、现状分析

### 1.1 已有能力：上行 Key 导入导出

| 功能 | 端点 | 格式 | 说明 |
|------|------|------|------|
| **导入** | `POST /admin/keypool/import` | JSON | 支持单 provider / 多 provider 混合批量导入，AES-256-GCM 加密存储，UPSERT 去重 |
| **导出** | `GET /admin/keypool/export` | JSON | 支持按 provider 过滤，返回明文 key（已解密） |

### 1.2 缺失能力

| 对象 | 导入 | 导出 | 场景 |
|------|------|------|------|
| **Providers** | ❌ | ❌ | 新环境初始化、跨环境迁移 provider 配置 |
| **Service Models** | ❌ | ❌ | 批量上架模型、跨环境同步模型列表、定价数据备份 |

---

## 二、方案设计

### 2.1 设计原则

1. **与 KeyPool 保持一致**: 复用已有的加密、审计日志、格式风格
2. **最小权限**: 导出敏感数据（如 API base_url）需二次确认
3. **幂等导入**: 重复导入同一 provider/model 不报错、不重复创建
4. **格式兼容**: JSON 为主，未来可扩展 CSV

---

### 2.2 Provider 导入导出

#### 导出

```
GET /admin/providers/export
```

**响应格式**:
```json
{
  "providers": [
    {
      "id": "zhipu",
      "name": "Zhipu",
      "type": "domestic",
      "base_url": "https://open.bigmodel.cn/api/paas/v4",
      "sync_source": "manual",
      "enabled": true,
      "sort_order": 13
    }
  ],
  "exported_at": "2026-05-29T15:30:00Z",
  "count": 22
}
```

#### 导入

```
POST /admin/providers/import
```

**请求格式**:
```json
{
  "providers": [
    {
      "id": "zhipu",
      "name": "Zhipu",
      "type": "domestic",
      "base_url": "https://open.bigmodel.cn/api/paas/v4",
      "enabled": true,
      "sort_order": 13
    }
  ],
  "mode": "upsert"
}
```

**导入策略** (`mode`):
| 模式 | 行为 |
|------|------|
| `upsert` (默认) | 存在则更新（name, type, base_url, enabled, sort_order），不存在则插入 |
| `skip` | 存在则跳过，不存在则插入 |
| `replace` | 先清空再全量导入（危险，需二次确认） |

**响应**:
```json
{
  "imported": 1,
  "updated": 0,
  "skipped": 0,
  "failed": 0,
  "errors": []
}
```

**约束**:
- `id` 必填，长度 ≤ 50，仅允许 `[a-z0-9_-]`
- `type` 必须为 `international` 或 `domestic`
- 系统内置 provider（openai, anthropic 等 20 个）允许更新 `base_url`/`enabled`/`sort_order`，不允许删除或修改 `id`

---

### 2.3 Service Model 导入导出

#### 导出

```
GET /admin/service-models/export?provider=alibaba&status=active
```

**Query 参数**:
| 参数 | 说明 |
|------|------|
| `provider` | 按 provider 过滤，不传则导出全部 |
| `status` | 按状态过滤：`active`, `paused`, `eol_pending`, `eol`, `pending` |

**响应格式**:
```json
{
  "models": [
    {
      "provider": "zhipu",
      "product_id": "glm-5",
      "api_model_name": "glm-5",
      "status": "active",
      "input_price_cache_hit": 0.0005,
      "input_price_cache_miss": 0.0010,
      "output_price": 0.0020,
      "reasoning_price": 0.0015,
      "show_in_store": true,
      "sort_order": 1,
      "billing_type": "token",
      "aimodels_data": {
        "name": "GLM-5",
        "contextWindow": 128000,
        "features": ["function_calling", "vision"]
      }
    }
  ],
  "exported_at": "2026-05-29T15:30:00Z",
  "count": 150
}
```

#### 导入

```
POST /admin/service-models/import
```

**请求格式**:
```json
{
  "models": [
    {
      "provider": "zhipu",
      "product_id": "glm-5",
      "api_model_name": "glm-5",
      "status": "active",
      "input_price_cache_hit": 0.0005,
      "input_price_cache_miss": 0.0010,
      "output_price": 0.0020,
      "reasoning_price": 0.0015,
      "show_in_store": true,
      "sort_order": 1,
      "billing_type": "token"
    }
  ],
  "mode": "upsert",
  "auto_sync_to_keys": true
}
```

**导入策略**:
| 模式 | 行为 |
|------|------|
| `upsert` (默认) | 按 `(provider, product_id)` 唯一键，存在则更新定价/状态等，不存在则插入 |
| `skip` | 存在则跳过 |
| `pricing_only` | 只更新定价字段，不修改状态/其他属性 |

**约束**:
- `provider` + `product_id` 联合唯一
- 导入前自动校验 provider 是否存在于 `providers` 表
- `status` 必须为合法值：`pending`, `active`, `paused`, `ignored`, `eol_pending`, `eol`, `discontinued`
- `auto_sync_to_keys`: 导入完成后自动调用 `syncServiceModelsToKeys()` 将 models 同步到 upstream_keys

**响应**:
```json
{
  "imported": 10,
  "updated": 5,
  "skipped": 0,
  "failed": 0,
  "synced_keys": 15,
  "errors": []
}
```

---

## 三、前端集成建议

### 3.1 Providers 页面 (`admin-providers.html`)

在现有 provider 管理表格上方添加工具栏：

```html
<div class="flex gap-2 mb-4">
  <a class="btn btn-secondary" href="/admin/providers/export" download="providers.json">
    导出 Providers
  </a>
  <button class="btn btn-secondary" onclick="showImportProviderModal()">
    导入 Providers
  </button>
</div>
```

导入弹窗：
- JSON 文件上传（拖拽/选择）
- 预览前 5 条记录
- 选择 mode: upsert / skip
- 显示导入结果统计

### 3.2 Models 页面 (`admin-models-general.html`)

在模型列表上方添加：

```html
<div class="flex gap-2 mb-4">
  <a class="btn btn-secondary" href="/admin/service-models/export?provider={{.CurrentProvider}}" download="models.json">
    导出 Models
  </a>
  <button class="btn btn-secondary" onclick="showImportModelModal()">
    导入 Models
  </button>
</div>
```

导入弹窗：
- JSON 文件上传
- 按 provider 分组预览
- 选择 mode: upsert / skip / pricing_only
- 勾选 "auto_sync_to_keys"
- 显示定价变更对比（高亮新增/修改/删除的模型）

---

## 四、API 实现建议

### 4.1 代码位置

复用现有 `AdminRoutes`，在 `internal/routes/admin.go` 中添加：

```go
// Provider Import/Export
func (r *AdminRoutes) importProviders(c *gin.Context)
func (r *AdminRoutes) exportProviders(c *gin.Context)

// Service Model Import/Export
func (r *AdminRoutes) importServiceModels(c *gin.Context)
func (r *AdminRoutes) exportServiceModels(c *gin.Context)
```

### 4.2 路由注册

```go
rg.POST("/providers/import", r.auth.RequireAdmin(), r.importProviders)
rg.GET("/providers/export", r.auth.RequireAdmin(), r.exportProviders)
rg.POST("/service-models/import", r.auth.RequireAdmin(), r.importServiceModels)
rg.GET("/service-models/export", r.auth.RequireAdmin(), r.exportServiceModels)
```

### 4.3 审计日志

所有导入导出操作记录 audit log：

```go
r.audit.Log(ctx, userID, email, "admin", "providers_exported", "info", fmt.Sprintf("Exported %d providers", count))
r.audit.Log(ctx, userID, email, "admin", "providers_imported", "info", fmt.Sprintf("Imported %d, updated %d", imported, updated))
r.audit.Log(ctx, userID, email, "admin", "models_exported", "info", fmt.Sprintf("Exported %d models", count))
r.audit.Log(ctx, userID, email, "admin", "models_imported", "info", fmt.Sprintf("Imported %d, updated %d", imported, updated))
```

### 4.4 事务处理

Provider 导入使用单条 UPSERT（`ON CONFLICT DO UPDATE`），每条独立事务，单条失败不影响其他。

Model 导入同样使用单条 UPSERT，但可在最后统一调用 `syncServiceModelsToKeys()` 同步到 keys。

---

## 五、安全考虑

| 风险 | 缓解措施 |
|------|----------|
| 导入恶意 provider ID | 限制 `id` 格式 `[a-z0-9_-]{1,50}` |
| 覆盖系统内置 provider | `replace` 模式禁止删除系统内置 provider |
| 导入超大文件 | 限制文件大小 ≤ 1MB，限制 records ≤ 1000 |
| 敏感数据泄露（base_url） | 导出包含 base_url，需 admin JWT + 已存在的 audit log |
| 定价数据错误导入 | 导入前预览，高亮变更项；支持 `pricing_only` 模式 |

---

## 六、优先级建议

| 优先级 | 功能 | 工作量 | 价值 |
|--------|------|--------|------|
| **P0** | Provider 导出 | 低 | 新环境初始化必备 |
| **P0** | Model 导出（按 provider） | 低 | 定价数据备份、跨环境同步 |
| **P1** | Model 导入（upsert） | 中 | 批量上架、定价批量更新 |
| **P1** | Provider 导入（upsert） | 中 | 跨环境迁移 |
| **P2** | CSV 格式支持 | 低 | 运营人员友好 |
| **P2** | 导入预览/ diff | 中 | 减少误操作 |

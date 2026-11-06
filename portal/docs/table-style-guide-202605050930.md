# 表格列名与样式规范

## 统一命名

| 列名 | 中文 | 英文 | 说明 |
|------|------|------|------|
| ID | ID | ID | 供应商唯一标识符，使用 `code` 样式显示 |
| 名称 | 名称 | Name | 供应商显示名称 |
| 类型 | 类型 | Type | international/domestic，使用 `badge badge-info` 样式 |
| Base URL | Base URL | Base URL | API基础地址，保持英文，使用 `code font-mono text-xs` 样式 |
| 状态 | 状态 | Status | 启用/禁用，使用 `badge badge-success/error` 样式 |
| 操作 | 操作 | Actions | 功能按钮区域 |
| 密钥数 | 密钥数 | Keys | 该供应商下的API密钥数量 |
| 模型数 | 模型数 | Models | 该供应商下的模型数量 |

---

## 字体规范

### 字体族定义

```css
:root {
  --font-sans: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif;
  --font-mono: "Menlo", "Monaco", "Courier New", "Ubuntu Mono", monospace;
}
```

### 字体选择原则

| 要求 | 说明 |
|------|------|
| 无专利 | 使用系统自带字体，无需授权 |
| 无衬线 | Sans-serif 字体，现代简洁 |
| 无需下载 | 使用系统预装字体，零网络请求 |

### 各元素字体规范

| 元素类型 | 字体族 | 字号 | 字重 | 示例 |
|----------|--------|------|------|------|
| **表格标题 (th)** | `var(--font-sans)` | 13px (0.8125rem) | 600 (Semi-bold) | ID、名称、类型 |
| **表格内容 - 中文** | `var(--font-sans)` | 14px (0.875rem) | 400 (Normal) | 启用、禁用 |
| **表格内容 - 英文** | `var(--font-sans)` | 14px (0.875rem) | 400 (Normal) | international |
| **表格内容 - 数字** | `var(--font-sans)` | 14px (0.875rem) | 400 (Normal) | 5, 100 |
| **表格内容 - 代码** | `var(--font-mono)` | 12px (0.75rem) | 400 (Normal) | openai, https://api.openai.com |
| **Badge 标签** | `var(--font-sans)` | 12px (0.75rem) | 500 (Medium) | international, 启用 |
| **按钮文字** | `var(--font-sans)` | 14px (0.875rem) | 500 (Medium) | 编辑、删除 |

### CSS 样式代码

```css
/* 表格标题 */
.data-table th {
  font-family: var(--font-sans);
  font-size: 0.8125rem;    /* 13px */
  font-weight: 600;
  color: var(--text-muted);
  text-transform: uppercase;
  letter-spacing: 0.025em;
}

/* 表格内容 */
.data-table td {
  font-family: var(--font-sans);
  font-size: 0.875rem;     /* 14px */
  font-weight: 400;
}

/* 代码/ID 内容 */
.font-mono {
  font-family: var(--font-mono);
  font-size: 0.75rem;      /* 12px */
}

/* Badge 标签 */
.badge {
  font-family: var(--font-sans);
  font-size: 0.75rem;      /* 12px */
  font-weight: 500;
}

/* 按钮 */
.btn {
  font-family: var(--font-sans);
  font-size: 0.9375rem;    /* 15px */
  font-weight: 500;
}
.btn-sm {
  font-size: 0.875rem;     /* 14px */
}
```

---

## 图标规范

### 图标来源

**Feather Icons** - 开源、简洁、一致的图标库

- **官网**: https://feathericons.com
- **GitHub**: https://github.com/feathericons/feather-icons
- **许可证**: MIT License (无专利问题)
- **风格**: 简洁线性风格，24x24 网格，2px 描边

### 图标特点

| 特点 | 说明 |
|------|------|
| 风格 | 线性 (Outline/Stroke) |
| 描边宽度 | 2px |
| 默认尺寸 | 24x24 |
| 颜色 | `currentColor` (继承父元素颜色) |
| 无需下载 | 直接内联 SVG，零网络请求 |

### 常用图标

| 图标名 | 用途 | SVG 代码 |
|--------|------|----------|
| edit-2 | 编辑 | `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/><path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/></svg>` |
| trash-2 | 删除 | `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M3 6h18M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/></svg>` |
| power | 启用/禁用 | `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M18.36 6.64a9 9 0 1 1-12.73 0M12 2v10"/></svg>` |
| plus | 添加 | `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></svg>` |
| search | 搜索 | `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/></svg>` |
| x | 关闭 | `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>` |

### 图标使用规范

```html
<!-- 标准用法：继承颜色，14px 尺寸 -->
<button class="btn btn-sm btn-ghost" title="编辑">
  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
    <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/>
    <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/>
  </svg>
</button>
```

**注意**:
- `width="14" height="14"` - 在按钮中使用 14px
- `viewBox="0 0 24 24"` - 保持 24x24 原始比例
- `fill="none"` - 不填充，仅描边
- `stroke="currentColor"` - 继承父元素文字颜色
- `stroke-width="2"` - 2px 描边宽度

---

## 样式规范

### ID 列
```html
<code class="font-mono text-xs">{{.ID}}</code>
```

### 名称列
```html
<span class="font-medium">{{.Name}}</span>
```

### 类型列
```html
<span class="badge badge-info">{{.Type}}</span>
```

### Base URL 列
```html
<code class="font-mono text-xs">{{if .BaseURL}}{{.BaseURL}}{{else}}默认{{end}}</code>
```

### 状态列
```html
<span class="badge badge-{{if .Enabled}}success{{else}}error{{end}}">
  {{if .Enabled}}启用{{else}}禁用{{end}}
</span>
```

### 数量列 (密钥数/模型数)
```html
<!-- 有数据时绿色，无数据时红色 -->
<span class="badge badge-{{if gt .Count 0}}success{{else}}error{{end}}">{{.Count}}</span>
```

---

## 操作按钮规范

### 按钮顺序 (从左到右)
1. **编辑** - 铅笔图标按钮，使用 `btn btn-sm btn-ghost`
2. **启用/禁用** - 切换按钮，使用 `btn btn-sm btn-secondary`
3. **删除** - 删除按钮，使用 `btn btn-sm btn-danger` (仅super admin)

### 编辑按钮
```html
<button class="btn btn-sm btn-ghost" onclick="editProvider('{{.ID}}')" title="编辑">
  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
    <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/>
    <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/>
  </svg>
</button>
```

### 启用/禁用按钮
```html
<form method="POST" action="/path/{{.ID}}/toggle" class="inline">
  <input type="hidden" name="csrf_token" value="{{$.CSRFToken}}">
  <button type="submit" class="btn btn-sm btn-secondary">{{if .Enabled}}禁用{{else}}启用{{end}}</button>
</form>
```

### 删除按钮
```html
<form method="POST" action="/path/{{.ID}}/delete" class="inline" onsubmit="return confirm('确定要删除吗？')">
  <input type="hidden" name="csrf_token" value="{{$.CSRFToken}}">
  <button type="submit" class="btn btn-sm btn-danger">删除</button>
</form>
```

---

## 权限对照

| 页面 | 路径 | 权限 | 可用操作 |
|------|------|------|----------|
| System Admin | `/admin/providers` | 系统管理员 | 编辑、启用/禁用 |
| Super Admin | `/super/providers` | 超级管理员 | 编辑、启用/禁用、删除 |

---

## 表格结构示例

### admin/providers (系统管理员)
| ID | 名称 | 类型 | Base URL | 密钥数 | 模型数 | 状态 | 操作 |
|----|------|------|----------|--------|--------|------|------|

### super/providers (超级管理员)
| ID | 名称 | 类型 | Base URL | 状态 | 操作 |
|----|------|------|----------|------|------|

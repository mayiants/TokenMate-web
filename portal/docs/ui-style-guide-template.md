# UI Style Guide Template

> **ACTIVE** — Current UI style guide for the project

**Project:** AI Portal Go  
**Version:** 3.0  
**Last Updated:** 2026-05-08

---

## 1. Design System Overview

### 1.1 Typography

| Element | Font Family | Size | Weight |
|---------|-------------|------|--------|
| Body | Inter, system-ui | 0.9375rem (15px) | 400 |
| Heading H1 | Inter | 1.5rem | 700 |
| Heading H2 | Inter | 1.125rem | 600 |
| Heading H3 | Inter | 1rem | 600 |
| Labels | Inter | 0.875rem | 500 |
| Small text | Inter | 0.75rem | 400 |
| Monospace (keys, codes) | Menlo, Monaco | 0.875rem | 400 |

### 1.2 Color Palette

| Color | Light Mode | Dark Mode | Usage |
|-------|------------|-----------|-------|
| Background | `#f8fafc` | `#0f172a` | Page background |
| Card Background | `#ffffff` | `#1e293b` | Cards, modals |
| Sidebar Background | `#ffffff` | `#1e293b` | Navigation sidebar |
| Text Primary | `#1e293b` | `#f1f5f9` | Main text |
| Text Muted | `#64748b` | `#94a3b8` | Secondary text |
| Primary (Blue) | `#3b82f6` | `#60a5fa` | Buttons, links, accents |
| Primary Hover | `#2563eb` | `#3b82f6` | Hover state |
| Success (Green) | `#16a34a` | `#4ade80` | Active, confirmed |
| Warning (Orange) | `#f59e0b` | `#fbbf24` | Pending, paused |
| Error (Red) | `#ef4444` | `#f87171` | Errors, discontinued |
| Border | `#e2e8f0` | `#334155` | Borders, dividers |

### 1.3 Spacing Scale

| Token | Value | Usage |
|-------|-------|-------|
| `--radius` | 8px | Buttons, inputs |
| `--radius-lg` | 12px | Cards, modals |
| `--sidebar-width` | 240px | Sidebar width |
| `--header-height` | 56px | Top bar height |
| Card padding | 20px | Card content |
| Section gap | 24px | Between sections |
| Form group gap | 16px | Between form fields |

---

## 2. Component Standards

### 2.1 Buttons

**Primary Button:**
```html
<button class="btn btn-primary">Action</button>
```
- Background: `var(--primary)`
- Text: white
- Padding: `10px 20px`
- Border-radius: `8px`
- Font-weight: 500

**Secondary Button:**
```html
<button class="btn btn-secondary">Cancel</button>
```
- Background: `var(--bg-hover)`
- Border: `1px solid var(--border)`
- Text: `var(--text)`

**Button Sizes:**
| Size | Class | Padding |
|------|-------|---------|
| Small | `.btn-sm` | `6px 12px` |
| Default | `.btn` | `10px 20px` |
| Large | `.btn-lg` | `12px 24px` |

**Button Variants:**
| Variant | Class | Color |
|---------|-------|-------|
| Primary | `.btn-primary` | Blue |
| Success | `.btn-success` | Green |
| Warning | `.btn-warning` | Orange |
| Danger | `.btn-danger` | Red |
| Ghost | `.btn-ghost` | Transparent |

### 2.2 Badges/Status Indicators

```html
<span class="badge badge-success">Active</span>
<span class="badge badge-warning">Pending</span>
<span class="badge badge-error">Error</span>
<span class="badge badge-info">Info</span>
```

**Status Color Mapping:**
| Status | Badge Class | Color |
|--------|-------------|-------|
| Active/Confirmed | `badge-success` | Green |
| Pending/Paused | `badge-warning` | Orange |
| Error/Discontinued | `badge-error` | Red |
| Info/Default | `badge-info` | Blue |
| Muted/Hidden | `badge-muted` | Gray |

### 2.3 Form Elements

**Input Field:**
```html
<div class="form-group">
  <label class="form-label">Label</label>
  <input type="text" class="form-input" placeholder="Enter value">
</div>
```

**Select Dropdown:**
```html
<select class="form-select">
  <option value="">Select...</option>
  <option value="1">Option 1</option>
</select>
```

**Textarea:**
```html
<textarea class="form-textarea" rows="4"></textarea>
```

**Focus State:**
- Border color: `var(--border-focus)` (#3b82f6)
- Box shadow: `0 0 0 3px rgba(59,130,246,0.15)`

### 2.4 Tables

```html
<div class="data-table-wrapper">
  <table class="data-table">
    <thead>
      <tr>
        <th>Column 1</th>
        <th>Column 2</th>
        <th style="text-align:right">Actions</th>
      </tr>
    </thead>
    <tbody>
      <tr>
        <td>Data 1</td>
        <td>Data 2</td>
        <td style="text-align:right">
          <button class="btn btn-sm btn-primary">Edit</button>
        </td>
      </tr>
    </tbody>
  </table>
</div>
```

**Table Standards:**
- Header font-size: `0.8125rem` (13px)
- Header text-transform: uppercase
- Header color: `var(--text-muted)`
- Cell padding: `12px 16px`
- Hover row background: `var(--bg-hover)`
- Actions column: right-aligned

### 2.5 Cards

```html
<div class="card">
  <div class="card-header">
    <h2 class="card-title">Title</h2>
    <button class="btn btn-sm btn-primary">Action</button>
  </div>
  <div class="card-body">
    Content...
  </div>
</div>
```

### 2.6 Modals

```html
<div class="modal-overlay" id="modal-id">
  <div class="modal">
    <div class="modal-header">
      <h3 class="modal-title">Modal Title</h3>
      <button class="modal-close" onclick="closeModal()">×</button>
    </div>
    <div class="modal-body">
      Content...
    </div>
    <div class="modal-footer">
      <button class="btn btn-secondary">Cancel</button>
      <button class="btn btn-primary">Confirm</button>
    </div>
  </div>
</div>
```

**Modal Standards:**
- Max-width: `480px`
- Max-height: `90vh`
- Footer: right-aligned buttons
- Confirm modal z-index: 300

### 2.7 Stat Cards

```html
<div class="stats-grid">
  <div class="stat-card">
    <div class="stat-label">Label</div>
    <div class="stat-value">Value</div>
  </div>
</div>
```

**Stats Grid:**
- Grid: `repeat(auto-fit, minmax(240px, 1fr))`
- Gap: `16px`
- Value font-size: `1.5rem`
- Value font-weight: 700

---

## 3. Layout Standards

### 3.1 Page Layout

```
+------------------+------------------------+
|    Sidebar       |      Main Content      |
|    (240px)       |                        |
|                  |  +------------------+  |
|  - Logo          |  |    Topbar (56px) |  |
|  - Navigation    |  +------------------+  |
|  - Sections      |                        |
|  - Logout        |  |    Page Content   |  |
|                  |  |    (padding 24px) |  |
|                  |  |                    |  |
+------------------+------------------------+
```

### 3.2 Sidebar Navigation

```html
<div class="sidebar">
  <div class="sidebar-header">Logo/Title</div>
  <nav class="sidebar-nav">
    <a href="/dashboard" class="active">Dashboard</a>
    <div class="sidebar-section">
      <div class="sidebar-section-title">Section Title</div>
      <a href="/subpage" class="sidebar-subitem">Sub Page</a>
    </div>
  </nav>
  <div class="sidebar-logout">
    <button class="sidebar-item">Logout</button>
  </div>
</div>
```

### 3.3 Responsive Breakpoints

| Breakpoint | Width | Behavior |
|------------|-------|----------|
| Desktop | > 1024px | Full sidebar visible |
| Tablet/Mobile | ≤ 1024px | Sidebar hidden, toggle button |

---

## 4. Icons & Emojis

### 4.1 SVG Icons (Recommended)

Use inline SVG icons with consistent sizing:

```html
<svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
  <!-- Icon paths -->
</svg>
```

**Common Icons:**
| Action | SVG Path |
|--------|----------|
| Key/Lock | `<rect x="3" y="11" width="18" height="11" rx="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/>` |
| Money | `<path d="M12 2v20M17 5H9.5a3.5 3.5 0 0 0 0 7h5a3.5 3.5 0 0 1 0 7H6"/>` |
| Document | `<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/>` |
| User | `<path d="M20 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2"/><circle cx="12" cy="7" r="4"/>` |
| Settings | `<circle cx="12" cy="12" r="3"/><path d="M12 1v2M12 21v2M4.22 4.22l1.42 1.42M18.36 18.36l1.42 1.42M1 12h2M21 12h2M4.22 19.78l1.42-1.42M18.36 5.64l1.42-1.42"/>` |

### 4.2 Emoji Usage (Avoid)

**Do NOT use emojis in production UI.** Use SVG icons instead for:
- Consistent rendering across platforms
- Theme compatibility (light/dark)
- Accessibility
- Professional appearance

---

## 5. Accessibility Standards

### 5.1 Required Attributes

| Element | Required |
|---------|----------|
| Images | `alt` attribute |
| Buttons | Accessible text (not just icon) |
| Forms | `<label>` associated with input |
| Links | Descriptive text |
| HTML root | `lang` attribute |

### 5.2 Color Contrast

- Text on background: minimum 4.5:1 ratio
- Large text: minimum 3:1 ratio
- Interactive elements: visible focus state

---

## 6. Animation & Transitions

| Element | Transition | Duration |
|---------|------------|----------|
| Button hover | background-color | 0.15s |
| Sidebar toggle | transform | 0.3s |
| Modal open | opacity | 0.2s |
| Toast notification | transform, opacity | 0.3s |

---

## 7. Data Display Standards

### 7.1 Currency Format

```html
<!-- Chinese Yuan -->
{{formatCurrency .Amount "CNY"}} → ¥1,234.56

<!-- US Dollar -->
{{formatCurrency .Amount "USD"}} → $1,234.56
```

### 7.2 Token Count Format

```html
{{formatTokens .Tokens}} → 1.2M, 500K, 123
```

### 7.3 Date/Time Format

```html
<!-- Short date -->
2026-05-08

<!-- Full datetime -->
2026-05-08 14:30:00
```

### 7.4 Key Display

```html
<div class="key-display">
  <code class="font-mono">sk-xxxx-xxxx-xxxx</code>
  <button class="btn btn-sm btn-ghost">Copy</button>
</div>
```

---

## 8. Toast Notifications

```html
<div class="toast-container">
  <div class="toast toast-success toast-show">
    Success message
  </div>
</div>
```

| Type | Class | Border Color |
|------|-------|--------------|
| Success | `.toast-success` | Green |
| Error | `.toast-error` | Red |
| Warning | `.toast-warning` | Orange |

---

## 9. Common Page Patterns

### 9.1 List Page with Filters

```html
<div class="card">
  <div class="card-header">
    <h1 class="card-title">Items</h1>
    <div class="flex gap-2">
      <select class="form-select" style="width: 120px">...</select>
      <button class="btn btn-primary">Add New</button>
    </div>
  </div>
  <div class="data-table-wrapper">
    <table class="data-table">...</table>
  </div>
</div>
```

### 9.2 Detail/Edit Page

```html
<div class="card">
  <div class="card-header">
    <h1 class="card-title">Edit Item</h1>
  </div>
  <form>
    <div class="form-group">...</div>
    <div class="flex gap-2 mt-4">
      <button class="btn btn-secondary">Cancel</button>
      <button class="btn btn-primary">Save</button>
    </div>
  </form>
</div>
```

---

## 10. Do Not Use

| Avoid | Reason | Alternative |
|-------|--------|-------------|
| Emojis in UI | Platform inconsistency | SVG icons |
| Inline styles | Maintenance difficulty | CSS classes |
| Hardcoded colors | Theme support | CSS variables |
| Tables without wrapper | Horizontal scroll issues | `.data-table-wrapper` |
| Modal without overlay | UX issues | `.modal-overlay` |
| Buttons without text | Accessibility | Icon + text or aria-label |

---

## Checklist for New Pages

- [ ] Uses CSS variables for colors
- [ ] Consistent font sizes (Inter)
- [ ] Proper button variants
- [ ] Badge colors match status
- [ ] Table has wrapper for scroll
- [ ] Actions column right-aligned
- [ ] Form labels associated with inputs
- [ ] Focus states visible
- [ ] Responsive layout tested
- [ ] Dark mode compatible
- [ ] No emojis in production
- [ ] SVG icons consistent size
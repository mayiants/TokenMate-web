# Admin & Enterprise Admin UI Improvement Plan

> Date: 2026-05-25
> Scope: System/Superadmin UI + Enterprise Admin UI

## Current Pain Points

### System Admin
1. **Sidebar**: Flat text list, no icons, hard to scan at a glance
2. **Admin Overview**: Only static stat cards, no trend indicators (↑/↓), no visual hierarchy
3. **Enterprise Table**: 5 action buttons per row = visual clutter; status is plain text without color
4. **No breadcrumbs**: Users lose context when navigating deep pages
5. **Tables**: No sticky filter bar, no batch operations

### Enterprise Admin
1. **Dashboard**: 4 bare stat cards, no quota progress visualization, no credit gauge
2. **No activity feed**: Enterprise admin cannot see recent events (invites, key revokes, member joins)
3. **Billing**: Raw numbers only, no usage trend bars, no visual comparison
4. **Users table**: No bulk actions, avatar-only name display is plain
5. **Keys table**: No usage frequency indicator, no "last used" info

## Improvement Plan

### Phase 1: Foundation (CSS + Layout)

| Change | File | Detail |
|--------|------|--------|
| Sidebar icons + grouping | `layout.html` | Add Lucide-style SVG icons to every nav item; group into collapsible sections |
| Breadcrumb component | `app.css` + templates | Add `.breadcrumb` trail below header on admin/enterprise pages |
| New CSS components | `app.css` | `.progress-bar`, `.trend-up/.trend-down`, `.activity-feed`, `.credit-gauge`, `.dropdown-menu` |

### Phase 2: System Admin Pages

| Change | File | Detail |
|--------|------|--------|
| Admin overview | `admin.html` | Add trend arrows to stat cards; add "system health" mini-cards (active enterprises, alert count) |
| Enterprise table | `admin-enterprise.html` | Status → colored badges; actions → dropdown menu; add batch suspend/activate |
| Users table | `admin-users.html` | Add role-colored badges; add enterprise column |

### Phase 3: Enterprise Admin Pages

| Change | File | Detail |
|--------|------|--------|
| Dashboard | `enterprise/dashboard.html` | Add quota progress ring + credit gauge; add "recent activity" feed |
| Billing | `enterprise/billing.html` | Add CSS bar-chart for usage-by-user; add quota usage progress bar |
| Users | `enterprise/users.html` | Add bulk-select checkboxes + batch actions toolbar; improve avatar block |
| Keys | `enterprise/keys.html` | Add "last used" column + usage frequency dot indicator |

## Design Principles
1. **No new JS dependencies** — use existing Alpine.js + vanilla JS
2. **CSS-only visualizations** — progress bars, gauges, bar charts via CSS
3. **Backward compatible** — all new classes are additive
4. **Mobile responsive** — stats-grid wraps, tables scroll horizontally

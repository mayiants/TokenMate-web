# Admin Rate Limits 页面改造方案

> 日期：2026-07-18
> 关联页面：`/admin/rate-limits`

---

## 1. 现状与问题

当前 `/admin/rate-limits` 页面存在以下局限：

1. **视角单一**：只展示 `user_subscriptions` 的订阅级计数器（RPM/RPH/RPD/TPM/TPD 等），对 **Group**、**API Key**、**Upstream Key** 的限额一无所知。
2. **只读**：只能看，不能改，也无法直接跳转到可编辑页面。
3. **窗口固定**：仅显示最近 1 小时流量，无法评估 24h/7d 趋势。
4. **缺少利用率排序**：无法快速定位“快被打满”的订阅、Group 或上游渠道。
5. **缺少容量总览**：看不到“下游总需求 vs 上游总容量”是否匹配。

---

## 2. 改造目标

将 `/admin/rate-limits` 升级为 **“Rate Limits & Capacity” 统一容量看板**：

- 同时覆盖四级限速对象：User Subscription / Group / API Key / Upstream Key
- 每级都展示：配置上限、实际用量、利用率、告警色
- 支持按利用率排序 / 筛选
- 支持 1h / 24h / 7d 用量窗口
- 提供编辑入口（跳转到已有管理页面）
- 增加聚合容量视图：上游总容量 vs 下游总需求

---

## 3. 页面结构

```
┌─────────────────────────────────────────────────────────────┐
│  标题：Rate Limits & Capacity                                │
│  Tab：Users | Groups | API Keys | Upstream Keys | Capacity   │
├─────────────────────────────────────────────────────────────┤
│  [时间窗口 1h/24h/7d] [搜索] [利用率阈值] [刷新]             │
├─────────────────────────────────────────────────────────────┤
│  表格：Name | 限额维度 | 配置上限 | 实际用量 | 利用率 | 操作 │
└─────────────────────────────────────────────────────────────┘
```

### 3.1 Tab 设计

| Tab | 数据源 | 展示字段 | 编辑跳转 |
|-----|--------|----------|----------|
| **Users** | `user_subscriptions` + `usage_records` | 各 subscription 的 rpm/rph/rpd/tpm/tpd/周/并发等 | `/admin/subscriptions` |
| **Groups** | `user_groups` + `usage_records` | rpm/rph/rp5h/rpw/rpmonth/tpm/tph/tpd/tpw/tpmonth/concurrent | `/admin/groups` |
| **API Keys** | `api_keys` + `usage_records` | rate_limit_rpm / rate_limit_tpm | `/apikeys` 或用户详情 |
| **Upstream Keys** | `upstream_keys` + `usage_records` | rpm_limit / tpm_limit / quota_limit | `/admin/keypool` |
| **Capacity** | 聚合上游 vs 下游 | 总上游 RPM/TPM/Quota vs 下游峰值需求 | — |

### 3.2 利用率可视化

每行限额用进度条展示：

- `< 60%`：绿色
- `60% - 80%`：黄色
- `> 80%`：红色
- 达到 100% 时显示 “LIMITED” badge

对多维限额（如 Group 同时有 RPM、TPM、并发），每行可折叠展示所有维度，默认只展示利用率最高的维度。

---

## 4. 数据模型与 SQL 思路

### 4.1 User Subscription 用量

沿用当前逻辑，从 `user_subscriptions` 读取限额字段，从 `usage_records` 按窗口聚合实际用量。

```sql
SELECT u.id, u.name, u.email,
       us.rpm_limit, us.rph_limit, us.rpd_limit, us.tpm_limit, us.tpd_limit,
       COALESCE(SUM(ur.request_count) FILTER (WHERE ur.created_at > NOW() - INTERVAL '1 hour'), 0) as req_1h,
       COALESCE(SUM(ur.input_tokens + ur.output_tokens) FILTER (WHERE ur.created_at > NOW() - INTERVAL '1 hour'), 0) as tok_1h
FROM users u
LEFT JOIN LATERAL (
    SELECT * FROM user_subscriptions
    WHERE user_id = u.id AND status = 'active' AND expires_at > NOW()
    ORDER BY created_at DESC LIMIT 1
) us ON true
LEFT JOIN usage_records ur ON ur.user_id = u.id AND ur.created_at > NOW() - INTERVAL '7 days'
WHERE u.deleted_at IS NULL
GROUP BY u.id, u.name, u.email, us.*
```

### 4.2 Group 用量

```sql
SELECT g.id, g.name,
       g.rpm, g.rph, g.rp5h, g.rpw, g.rpmonth,
       g.tpm, g.tph, g.tpd, g.tpw, g.tpmonth,
       g.concurrent_limit,
       COALESCE(SUM(ur.request_count), 0) as requests,
       COALESCE(SUM(ur.input_tokens + ur.output_tokens), 0) as tokens
FROM user_groups g
LEFT JOIN usage_records ur ON ur.group_id = g.id AND ur.created_at > NOW() - INTERVAL '7 days'
WHERE g.deleted_at IS NULL
GROUP BY g.id, g.name, g.rpm, g.rph, g.rp5h, g.rpw, g.rpmonth,
         g.tpm, g.tph, g.tpd, g.tpw, g.tpmonth, g.concurrent_limit
```

### 4.3 API Key 用量

```sql
SELECT k.id, k.name, k.key_prefix,
       k.rate_limit_rpm, k.rate_limit_tpm,
       COALESCE(SUM(ur.request_count), 0) as requests,
       COALESCE(SUM(ur.input_tokens + ur.output_tokens), 0) as tokens
FROM api_keys k
LEFT JOIN usage_records ur ON ur.user_api_key_id = k.id AND ur.created_at > NOW() - INTERVAL '7 days'
WHERE k.deleted_at IS NULL
GROUP BY k.id, k.name, k.key_prefix, k.rate_limit_rpm, k.rate_limit_tpm
```

### 4.4 Upstream Key 用量

上游 key 的用量需要从 `usage_records` 按 `upstream_key_id` 聚合（该字段已存在）。

```sql
SELECT k.id, k.provider, k.key_preview,
       k.rpm_limit, k.tpm_limit, k.quota_limit, k.quota_used,
       COALESCE(SUM(ur.request_count), 0) as requests,
       COALESCE(SUM(ur.input_tokens + ur.output_tokens), 0) as tokens
FROM upstream_keys k
LEFT JOIN usage_records ur ON ur.upstream_key_id = k.id AND ur.created_at > NOW() - INTERVAL '7 days'
WHERE k.deleted_at IS NULL AND k.enabled = true
GROUP BY k.id, k.provider, k.key_preview, k.rpm_limit, k.tpm_limit, k.quota_limit, k.quota_used
```

### 4.5 Capacity 总览

```sql
-- 下游峰值需求（按 1 小时滑动窗口）
SELECT MAX(req_1h) as peak_rpm, MAX(tok_1h) as peak_tpm
FROM (
    SELECT date_trunc('hour', created_at) as bucket,
           SUM(request_count) as req_1h,
           SUM(input_tokens + output_tokens) as tok_1h
    FROM usage_records
    WHERE created_at > NOW() - INTERVAL '7 days'
    GROUP BY bucket
) t;

-- 上游总容量
SELECT SUM(rpm_limit) as total_rpm, SUM(tpm_limit) as total_tpm, SUM(quota_limit - quota_used) as remaining_quota
FROM upstream_keys
WHERE deleted_at IS NULL AND enabled = true;
```

---

## 5. 后端 API 建议

现有 `/api/admin/rate-limits/usage` 返回用户订阅数据。建议扩展为：

- `GET /api/admin/rate-limits/subscriptions?window=1h|24h|7d`
- `GET /api/admin/rate-limits/groups?window=1h|24h|7d`
- `GET /api/admin/rate-limits/apikeys?window=1h|24h|7d`
- `GET /api/admin/rate-limits/upstream-keys?window=1h|24h|7d`
- `GET /api/admin/rate-limits/capacity?window=1h|24h|7d`

返回统一结构：

```json
{
  "window": "1h",
  "items": [
    {
      "id": "...",
      "name": "...",
      "limits": { "rpm": 120, "tpm": 100000 },
      "usage": { "rpm": 45, "tpm": 12000 },
      "utilization": { "rpm": 0.375, "tpm": 0.12 },
      "status": "healthy"
    }
  ]
}
```

---

## 6. 前端改动

1. **Tab 切换**：替换当前单一表格为 5 个 Tab。
2. **利用率进度条**：复用现有 `limit-cell` / `progress-bar` 样式。
3. **时间窗口下拉**：1h / 24h / 7d，默认 1h。
4. **Near Limit 筛选**：只显示利用率 > 80% 的项。
5. **操作列**：
   - Users → “View Subscription”
   - Groups → “Edit Group”
   - API Keys → “View Key”
   - Upstream Keys → “Edit Key”
6. **Capacity Tab**：用两个大数字卡片 + 一个折线图展示上游容量 vs 下游峰值趋势。

---

## 7. 实施阶段

| 阶段 | 内容 | 风险 | 预估工作量 | 状态 |
|------|------|------|-----------|------|
| **P0** | 保留现有 Users Tab；新增 Groups 和 Upstream Keys Tab；统一返回利用率；状态卡片显示总用量/超限统计 | 低：只读查询 | 1-2 天 | ✅ 已完成（`admin-rate-limits.html` 四 Tab + 后端 `/api/admin/rate-limits/{usage,groups,upstream-keys}` + 1h/24h/7d 窗口 + Near Limit 筛选） |
| **P1** | 新增 API Keys Tab；展示具体限速绝对值；Groups 增加 Monthly 列；状态卡片增加 Limited/Exceeded 统计；"Near Limit" 改为 "At Risk"（≥80%） | 低 | 1-2 天 | ✅ 已完成 |
| **P2** | 新增 Capacity 总览 Tab；后端聚合上游 vs 下游峰值/平均需求；RPM/TPM/Quota 利用率进度条 | 中：窗口聚合 SQL 需要优化 | 2-3 天 | ✅ 已完成（`GET /api/admin/rate-limits/capacity?window=1h\|24h\|7d` + 前端 Capacity Tab） |
| **P3** | 当前页签 CSV 导出（Users/Groups/API Keys/Upstream Keys） | 低 | 1 天 | ✅ 已完成（`GET /api/admin/rate-limits/export?dimension=...&window=...` + 前端 Export CSV 按钮） |

---

## 8. 需要确认的问题（2026-07-18 状态）

1. `usage_records.upstream_key_id` 当前覆盖率如何？旧记录是否为空？若大量为空，Upstream Keys Tab 利用率会偏低。  
   **当前处理**：新记录已带 `upstream_key_id`；旧记录为空时对应 key 的利用率会显示为 0，属于数据缺口而非实现问题。未做历史回填。

2. 是否需要在前端展示“有效限速”？例如某 user 实际受 group RPM 限制，而 subscription RPM 更高，这种情况下当前显示 subscription 会误导。  
   **当前处理**：Users Tab 仍展示 subscription 级限额；Groups Tab 已可查看 group 级限额。"有效限速" 需要额外逻辑取 subscription / group / api_key 三者的最小值，暂未实现，可作为后续优化。

3. Capacity 视图是否需要按 provider 拆分？还是只看全局总容量？  
   **当前处理**：Capacity Tab 目前为**全局总览**（上游全部 enabled key 汇总 vs 下游总需求）。按 provider 拆分对定位单供应商容量瓶颈更有价值，可作为 P2+ 增强项。

4. API Key 的 rate_limit 是否在实际网关层生效？若只是字段未启用，则展示利用率意义不大。  
   **当前处理**：`api_keys.rate_limit_rpm` / `rate_limit_tpm` 已在 `apikeys_validate.go` 的 `validateKey` 流程中读取并返回给 proxy（`RateLimitRPM` / `RateLimitTPM`），proxy 侧是否严格按此限流需另行确认。

---

## 9. 总结

改造后的 `/admin/rate-limits` 将从“subscription 只读表”升级为 **全链路容量监控看板**，帮助管理员：

- 快速发现哪一层_limits_先被打满
- 判断上游容量是否充足
- 一键跳转到对应管理页进行调整

建议从 **P0（Groups + Upstream Keys Tab）** 开始实施，改动最小、价值最明显。

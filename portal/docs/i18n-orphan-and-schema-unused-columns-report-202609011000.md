# i18n 孤儿键 & Schema 未引用列 清理清单

> **Status:** ACTIVE
> 生成时间（UTC）：2026-09-01T10:00Z
> 生成方式：`python3 scripts/i18n_full_audit.py` + `bash scripts/check_schema_vs_code.sh`（均为增强后的版本）
> 适用仓库：`ai-portal-go`（Portal 控制面）

## 背景

在完善 i18n / schema 校验脚本时发现两类"疑似冗余"：

1. **i18n 孤儿键** —— `web/i18n/en.json` / `zh.json` 中存在、但代码里检测不到引用的 key。
2. **Schema 未引用列** —— `internal/db/schema.go` 定义、但代码 SQL 里检测不到引用的列。

脚本已在 commit `aad6ac1` 中增强：
- i18n 孤儿检测现在能识别**动态构造键**（模板 `{{t (printf "prefix.%s" ...)}}`、Go `T("prefix."+var)` / `key := "prefix."+var`）与**字符串拼接碎片键**（全量源码字面量兜底），孤儿从 463 降到 373，消除了 90 个误报。
- schema 检查新增**非阻断**的反向报告（Phase 5），列出未被引用的列。

**本清单是清理线索，不是删除命令。** 两个脚本的孤儿/未引用统计都不计入 CI 门禁（`--strict` 只对 missing keys / key parity / hardcoded 中文 / 缺列缺表 失败）。

## 清理注意事项（务必先读）

- **i18n 键**：删除前确认没有新的动态构造路径。带 `error.` / `msg.` 前缀的键尤其危险——后端可能以本脚本尚未覆盖的方式（多层封装、间接返回）引用它们。建议删一批后跑一次 `i18n_full_audit.py --strict` 且在 UI 冒烟测试中确认无 key 直出。删 en.json 的同时必须删 zh.json（保持 key parity）。
- **Schema 列**：这是 best-effort 静态推断，**别名 / `SELECT *` / `RETURNING` / CODE_DIRS 之外的路径都可能隐藏真实使用**。删除列属于**高风险不可逆**操作，必须：(1) 人工逐列核对；(2) 通过 migration + 同步更新 `schema.go`；(3) 先在测试库验证。**不建议**在没有明确收益时批量删列。

---

### A. i18n 孤儿键（en.json / zh.json 中存在但代码未引用）

共 **373** 个。已排除动态前缀键（`audit.category.`/`alert_type.`/`settings.`/`role.`）与字符串拼接碎片键（脚本已能识别，不在此列）。

> 注意：删除前请再确认——个别键可能通过尚未被脚本识别的方式引用。带命名空间前缀（`error.`/`msg.` 等）的键尤其要核对后端是否有新的动态构造路径。多行键中的换行以 `\n` 表示。

> **A.1 溯源统计**（263 个无前缀键）：244 个曾在代码中出现、后被移除（有"最后引用提交"记录，删除相对安全）；**19 个从未在代码文件出现**（标 `(never)`，疑似遗留 JSON 键）；**51 个存在当前近似键**（疑似改名/大小写变体，如 `Add Failed`→`Add failed`，属高置信度重复，宜优先清理）。


#### A.1 无命名空间的界面文案碎片（263）— 多为历史遗留，清理优先级最高

下表标注每个孤儿键的**溯源**信息，辅助判断能否安全删除：

- **最后引用提交**：`git log -S` 找到的最后一次在代码文件（web/internal/pkg/cmd，排除 i18n JSON）中出现该文案的提交。通常即“移除该用法”的那次提交——说明该键已从代码中退役。
- **最后出现文件**：该提交 diff 涉及的代码文件（best-effort，取路径末两段）。
- **当前近似键**：现有代码里仍在使用、与该键高度相似（≥0.85）的键。若有值，多为**改名/大小写变体**（如 `Add Failed`→`Add failed`）——删除前应确认调用方已迁移到新键；这类是**大小写/措辞重复**的强删除候选。
- **(never)**：git 历史中从未在代码文件里出现，可能是从未接线的遗留 JSON 键，或仅经动态方式引用；删除前建议再全局搜一次。

| # | Key | 最后引用提交 | 最后出现文件 | 当前近似键（疑似改名） |
|---|-----|--------------|--------------|------------------------|
| 1 | ` (already exists)` | `cc8c1bd` 2026-07-27 | js/page-admin-models.js | — |
| 2 | ` entries, page ` | `055da1b` 2026-06-20 | templates/admin-upstream-usage.html | — |
| 3 | ` format error, expected: provider,key` | `cc8c1bd` 2026-07-27 | js/page-admin-keypool.js | — |
| 4 | ` models to the service model table?` | `cc8c1bd` 2026-07-27 | js/page-admin-models.js | — |
| 5 | ` models, skipped ` | `cc8c1bd` 2026-07-27 | js/page-admin-models.js | — |
| 6 | ` models?` | `cc8c1bd` 2026-07-27 | js/page-admin-models.js | `models` |
| 7 | ` upstream keys, total ` | `cc8c1bd` 2026-07-27 | js/page-admin-models.js | — |
| 8 | `% of Total` | `f6dae60` 2026-06-24 | enterprise/billing.html | — |
| 9 | `1 Day` | `51341f7` 2026-07-17 | enterprise/dashboard.html | — |
| 10 | `14 days` | `ddcc213` 2026-06-22 | templates/admin.html | — |
| 11 | `15 Days` | `51341f7` 2026-07-17 | templates/dashboard.html | — |
| 12 | `1h Requests` | `3037ae8` 2026-07-18 | templates/admin-rate-limits.html | — |
| 13 | `1h Tokens` | `3037ae8` 2026-07-18 | templates/admin-rate-limits.html | — |
| 14 | `24/7 monitoring` | `2d9b26e` 2026-08-13 | templates/landing.html | — |
| 15 | `30 Days` | `51341f7` 2026-07-17 | templates/dashboard.html | `30 days` |
| 16 | `7 Days` | `51341f7` 2026-07-17 | templates/dashboard.html | — |
| 17 | `7-Day Requests` | `6c49f78` 2026-07-17 | templates/usage-logs.html | `Daily Requests` |
| 18 | `7-Day Usage Trend` | `9ab5f2a` 2026-07-17 | templates/usage-logs.html | — |
| 19 | `? Requests to use this key while disabled will be denied.` | `c3a41e5` 2026-07-27 | enterprise/keys.html | — |
| 20 | `? Their enterprise keys will be recycled upon removal.` | `c3a41e5` 2026-07-27 | enterprise/users.html | — |
| 21 | `? This operation is irreversible.` | `c3a41e5` 2026-07-27 | enterprise/keys.html | `This action is irreversible!` |
| 22 | `AWS format: access_key_id:secret_access_key (encrypted at rest)` | `cc8c1bd` 2026-07-27 | js/page-admin-keypool.js | `AWS format: access_key_id:secret_access_key (will be encrypted at rest)` |
| 23 | `Add Failed` | `cc8c1bd` 2026-07-27 | js/page-admin-keypool.js | `Add failed` |
| 24 | `Add Result` | `cc8c1bd` 2026-07-27 | js/page-admin-models.js | — |
| 25 | `Add to Service Models` | `cc8c1bd` 2026-07-27 | js/page-admin-models.js | — |
| 26 | `Added successfully! Added ` | `cc8c1bd` 2026-07-27 | js/page-admin-models.js | — |
| 27 | `Aggregated from 5-minute buckets over the selected period` | `b4c1651` 2026-06-20 | templates/status.html | — |
| 28 | `All leading AI models` | `fe4d631` 2026-08-13 | templates/landing.html | — |
| 29 | `All suppliers` | `641a9af` 2026-06-20 | templates/admin-monitor-upstream.html | — |
| 30 | `Amount in cents` | `e39ac34` 2026-07-13 | enterprise/billing.html | — |
| 31 | `Application Status` | `155d961` 2026-06-24 | templates/enterprise-apply.html | — |
| 32 | `Apply for Enterprise Account` | `155d961` 2026-06-24 | templates/enterprise-apply.html | — |
| 33 | `Approval Failed` | `cc8c1bd` 2026-07-27 | js/page-admin-enterprise.js | `Approval failed` |
| 34 | `Are you sure you want to add ` | `cc8c1bd` 2026-07-27 | js/page-admin-models.js | `Are you sure you want to ` |
| 35 | `Are you sure you want to delete key` | `c3a41e5` 2026-07-27 | enterprise/keys.html | `Are you sure you want to delete this key?` |
| 36 | `Are you sure you want to delete provider "{name}"? This cannot be undone.` | `0acaf78` 2026-06-18 | js/page-admin-providers.js | — |
| 37 | `Are you sure you want to delete this model? This is irreversible and all data will be removed.` | `cc8c1bd` 2026-07-27 | js/page-admin-models.js | — |
| 38 | `Are you sure you want to delete this rule?` | `cc8c1bd` 2026-07-27 | js/page-admin-models.js | `Are you sure you want to delete this key?` |
| 39 | `Are you sure you want to delist this model? This is irreversible and the model will stop serving entirely.` | `cc8c1bd` 2026-07-27 | js/page-admin-models.js | — |
| 40 | `Are you sure you want to disable key` | `c3a41e5` 2026-07-27 | enterprise/keys.html | — |
| 41 | `Are you sure you want to disable this enterprise?\n\nAll members will lose access.` | `cc8c1bd` 2026-07-27 | js/page-admin-enterprise.js | `Are you sure you want to disable this enterprise?\n\nAll members will be unable to use the service.` |
| 42 | `Are you sure you want to enable enterprise "{name}"?` | `0acaf78` 2026-06-18 | js/page-admin-enterprise.js | `Are you sure you want to enable this enterprise?` |
| 43 | `Are you sure you want to ignore this model? It can be restored later.` | `cc8c1bd` 2026-07-27 | js/page-admin-models.js | — |
| 44 | `Are you sure you want to pause this model? It will become unavailable.` | `cc8c1bd` 2026-07-27 | js/page-admin-models.js | — |
| 45 | `Are you sure you want to remove member` | `c3a41e5` 2026-07-27 | enterprise/users.html | `Are you sure you want to remove this member?` |
| 46 | `Are you sure you want to restore this model? It will return to "pending" status.` | `cc8c1bd` 2026-07-27 | js/page-admin-models.js | — |
| 47 | `Are you sure you want to validate all keys?` | `cc8c1bd` 2026-07-27 | js/page-admin-keypool.js | `Are you sure you want to verify all keys?` |
| 48 | `Assigned To` | `ff38add` 2026-07-25 | enterprise/key-detail.html | — |
| 49 | `Balance (cents)` | `c23fe3a` 2026-07-14 | partials/user-detail.html | — |
| 50 | `Banner Color` | `b5dd04d` 2026-08-13 | templates/admin-announcements.html | — |
| 51 | `Batch Quota` | `ef914c4` 2026-07-25 | enterprise/users.html | — |
| 52 | `Batch remove feature coming soon` | `8c5c66a` 2026-06-25 | enterprise/users.html | — |
| 53 | `Batch validating keys...` | `cc8c1bd` 2026-07-27 | js/page-admin-keypool.js | — |
| 54 | `Board resolution, Power of Attorney, or appointment letter` | **(never)** | — | — |
| 55 | `Business Registration Certificate` | **(never)** | — | — |
| 56 | `Business license, Certificate of Incorporation, or equivalent` | **(never)** | — | `Business license, Certificate of Incorporation` |
| 57 | `By User` | `f6dae60` 2026-06-24 | enterprise/usage.html | — |
| 58 | `Cache Read ($/1M)` | `7f5f474` 2026-08-12 | templates/admin-upstream-pricing.html | — |
| 59 | `Cache Write ($/1M)` | `7f5f474` 2026-08-12 | templates/admin-upstream-pricing.html | — |
| 60 | `Can only be changed to user or enterprise_admin` | `c23fe3a` 2026-07-14 | partials/user-detail.html | — |
| 61 | `Cancel EOL Notice` | `cc8c1bd` 2026-07-27 | js/page-admin-models.js | — |
| 62 | `Cancel EOL notice? The model will return to normal service.` | `cc8c1bd` 2026-07-27 | js/page-admin-models.js | — |
| 63 | `Cancel failed: ` | `cc8c1bd` 2026-07-27 | js/page-admin-models.js | `Cancel failed:` |
| 64 | `Cannot find provider data` | `cc8c1bd` 2026-07-27 | js/page-admin-providers.js | — |
| 65 | `Confirm EOL? After confirmation, model enters EOL: no new purchases, continues serving existing users (max 1 month), hidden from store.` | `cc8c1bd` 2026-07-27 | js/page-admin-models.js | — |
| 66 | `Confirm failed: ` | `cc8c1bd` 2026-07-27 | js/page-admin-models-aws.js | `Confirm failed:` |
| 67 | `Cost Forecast` | `f71fa52` 2026-07-26 | enterprise/dashboard.html | — |
| 68 | `Cost Trend` | `f6dae60` 2026-06-24 | enterprise/billing.html | — |
| 69 | `Create Failed` | `cc8c1bd` 2026-07-27 | js/page-admin-enterprise.js | `Create failed` |
| 70 | `Credit Change Log` | `e0ab050` 2026-07-25 | enterprise/audit.html | — |
| 71 | `Credit Total (USD)` | `0a92f4f` 2026-06-25 | enterprise/settings.html | — |
| 72 | `Delete Document` | `bdd1cf0` 2026-07-01 | templates/enterprise-apply.html | — |
| 73 | `Delete Failed` | `cc8c1bd` 2026-07-27 | js/page-admin-providers.js | `Delete failed` |
| 74 | `Delete Model` | `cc8c1bd` 2026-07-27 | js/page-admin-models.js | `Delete model` |
| 75 | `Delete Routing Rule` | `cc8c1bd` 2026-07-27 | js/page-admin-models.js | `Delete routing rule` |
| 76 | `Delete all personal data and deactivate account. Irreversible.` | `c5b1ca3` 2026-07-13 | templates/profile.html | — |
| 77 | `Delete all usage records` | `c5b1ca3` 2026-07-13 | templates/profile.html | — |
| 78 | `Delist Model` | `cc8c1bd` 2026-07-27 | js/page-admin-models.js | — |
| 79 | `Delist failed: ` | `cc8c1bd` 2026-07-27 | js/page-admin-models.js | — |
| 80 | `Download all your personal data (accounts, API keys, usage records, orders).` | `c5b1ca3` 2026-07-13 | templates/profile.html | — |
| 81 | `Downstream key usage table` | `bb765b5` 2026-07-17 | templates/admin-monitor-downstream.html | `Downstream usage table` |
| 82 | `Edit Failed` | `cc8c1bd` 2026-07-27 | js/page-admin-keypool.js | `Edit failed` |
| 83 | `Email Confirmed` | `cc8c1bd` 2026-07-27 | js/page-admin-enterprise.js | — |
| 84 | `Email cannot be modified` | `c23fe3a` 2026-07-14 | partials/user-detail.html | — |
| 85 | `Email or name` | `f89a7b4` 2026-07-26 | enterprise/audit.html | — |
| 86 | `Enable failed: ` | `cc8c1bd` 2026-07-27 | js/page-admin-models-aws.js | — |
| 87 | `Enter password to export keys:` | `3b9ff48` 2026-07-19 | templates/admin-keypool.html | — |
| 88 | `Enter top-up amount (USD):` | `13357ca` 2026-07-25 | enterprise/users.html | — |
| 89 | `Enterprise Change Requests` | `aa63b25` 2026-06-25 | web/pages.go | — |
| 90 | `Enterprise Pricing Ratio` | `90abdb3` 2026-06-25 | enterprise/billing.html | — |
| 91 | `Est Cost` | `036cba0` 2026-07-17 | templates/admin-reconciliation-ops.html | — |
| 92 | `Exceeds ` | `cc8c1bd` 2026-07-27 | js/page-admin-keypool.js | — |
| 93 | `Explore platform usage by user, enterprise, model, key or endpoint` | `3e93813` 2026-07-23 | templates/admin-usage-explorer.html | — |
| 94 | `Export Records` | `f6dae60` 2026-06-24 | enterprise/audit.html | — |
| 95 | `Fetch failed: ` | `c5bea7b` 2026-06-28 | js/page-admin-models.js | `Fetch failed` |
| 96 | `Field to Change` | `0a92f4f` 2026-06-25 | enterprise/settings.html | — |
| 97 | `File exceeds maximum size` | **(never)** | — | — |
| 98 | `Final Verification` | `f917184` 2026-06-20 | templates/admin-monitor-upstream.html | — |
| 99 | `Fixed ($/req)` | `7f5f474` 2026-08-12 | templates/admin-upstream-pricing.html | — |
| 100 | `Flexible billing` | `2d9b26e` 2026-08-13 | templates/landing.html | — |
| 101 | `Format Error` | `cc8c1bd` 2026-07-27 | js/page-admin-keypool.js | `Format error` |
| 102 | `Global nodes` | `2d9b26e` 2026-08-13 | templates/landing.html | — |
| 103 | `Ignore Model` | `cc8c1bd` 2026-07-27 | js/page-admin-models.js | `Ignore model` |
| 104 | `Ignore failed: ` | `cc8c1bd` 2026-07-27 | js/page-admin-models-aws.js | `Ignore failed:` |
| 105 | `Input ($/1M)` | `7f5f474` 2026-08-12 | templates/admin-upstream-pricing.html | — |
| 106 | `Input (Cache Hit)` | `eb54b46` 2026-07-02 | templates/admin-models-general.html | `Input Cache Hit` |
| 107 | `Input (Cache Miss)` | `eb54b46` 2026-07-02 | templates/admin-models-general.html | — |
| 108 | `Input Cache Hit ($/1M)` | `7f5f474` 2026-08-12 | templates/admin-upstream-pricing.html | — |
| 109 | `Input-Cache Hit ($/1M)` | `eb54b46` 2026-07-02 | templates/admin-models-general.html | — |
| 110 | `Input-Cache Miss ($/1M)` | `eb54b46` 2026-07-02 | templates/admin-models-general.html | — |
| 111 | `Input-cache miss ($/1M) *` | `eb54b46` 2026-07-02 | templates/admin-models-general.html | — |
| 112 | `Invitation Failed` | `cc8c1bd` 2026-07-27 | js/page-admin-enterprise.js | `Invitation failed` |
| 113 | `Invoice title / company name` | `6fa1301` 2026-06-25 | enterprise/billing.html | — |
| 114 | `Joined At` | `ff38add` 2026-07-25 | enterprise/member-detail.html | — |
| 115 | `Key Settings` | `71e7a55` 2026-07-11 | templates/apikeys.html | — |
| 116 | `Key settings` | `71e7a55` 2026-07-11 | templates/apikeys.html | — |
| 117 | `Key validation result: ` | `cc8c1bd` 2026-07-27 | js/page-admin-keypool.js | — |
| 118 | `Key validation successful! Status: ` | `cc8c1bd` 2026-07-27 | js/page-admin-keypool.js | — |
| 119 | `Last 7 Days` | `51341f7` 2026-07-17 | templates/dashboard.html | — |
| 120 | `Last Bucket` | `b489202` 2026-06-21 | templates/admin-upstream-ops.html | — |
| 121 | `Latest registered user` | `4610eb7` 2026-06-21 | templates/admin.html | — |
| 122 | `Live rate limit usage per user (subscription counters + recent 1h traffic)` | `3037ae8` 2026-07-18 | templates/admin-rate-limits.html | — |
| 123 | `Max Reward` | `291ed51` 2026-07-13 | templates/admin-settings.html | — |
| 124 | `Member Consumption` | `f6dae60` 2026-06-24 | enterprise/reconciliation.html | — |
| 125 | `Member Limit Changes` | `e0ab050` 2026-07-25 | enterprise/audit.html | — |
| 126 | `Min Reward` | `291ed51` 2026-07-13 | templates/admin-settings.html | — |
| 127 | `Model Breakdown (24h)` | `e7fdc54` 2026-06-21 | templates/dashboard.html | — |
| 128 | `Model Usage Distribution` | `476c32c` 2026-06-20 | templates/usage-logs.html | `Model Distribution` |
| 129 | `Model added successfully, now active` | `cc8c1bd` 2026-07-27 | js/page-admin-models.js | — |
| 130 | `Model name and Provider are required` | `cc8c1bd` 2026-07-27 | js/page-admin-models.js | `Model and Provider required` |
| 131 | `Monitor upstream API key status and health` | `f917184` 2026-06-20 | templates/admin-monitor-upstream.html | — |
| 132 | `Near Limit` | `6166690` 2026-07-18 | templates/admin-rate-limits.html | — |
| 133 | `Network error: ` | `cc8c1bd` 2026-07-27 | js/page-admin-models-aws.js | `Network error` |
| 134 | `New Account` | **(never)** | — | — |
| 135 | `New value is required` | `0a92f4f` 2026-06-25 | enterprise/settings.html | — |
| 136 | `No Data` | `7162921` 2026-06-28 | templates/admin-groups.html | `No data` |
| 137 | `No Models Added` | `cc8c1bd` 2026-07-27 | js/page-admin-models.js | — |
| 138 | `No Usage` | **(never)** | — | — |
| 139 | `No admins` | `7162921` 2026-06-28 | super/admin-management.html | — |
| 140 | `No applications` | `7162921` 2026-06-28 | templates/invoices.html | — |
| 141 | `No audit logs` | `7162921` 2026-06-28 | templates/admin-audit-logs.html | — |
| 142 | `No business groups available yet` | `7162921` 2026-06-28 | templates/my-group.html | — |
| 143 | `No configuration` | `7162921` 2026-06-28 | templates/admin-settings.html | — |
| 144 | `No data yet` | `7162921` 2026-06-28 | templates/billing-details.html | — |
| 145 | `No enterprise custom groups` | `7162921` 2026-06-28 | templates/admin-groups.html | — |
| 146 | `No enterprise-assigned keys yet` | `7162921` 2026-06-28 | templates/apikeys.html | — |
| 147 | `No group pricing available.` | **(never)** | — | — |
| 148 | `No groups yet.` | `7162921` 2026-06-28 | enterprise/groups.html | — |
| 149 | `No historical data` | `7162921` 2026-06-28 | templates/admin-provider-health.html | — |
| 150 | `No history data` | **(never)** | — | — |
| 151 | `No invitation codes` | `7162921` 2026-06-28 | templates/voucher.html | — |
| 152 | `No invoices` | `ca515ab` 2026-06-25 | enterprise/billing.html | — |
| 153 | `No matching logs` | `7162921` 2026-06-28 | templates/admin-audit-logs.html | — |
| 154 | `No members` | `cc8c1bd` 2026-07-27 | js/page-admin-enterprise.js | — |
| 155 | `No packages available yet` | `7162921` 2026-06-28 | templates/subscriptions.html | — |
| 156 | `No personal groups available yet` | `7162921` 2026-06-28 | templates/my-group.html | — |
| 157 | `No personal key yet` | `7162921` 2026-06-28 | templates/apikeys.html | — |
| 158 | `No quota/credit change record yet` | `7162921` 2026-06-28 | enterprise/audit.html | — |
| 159 | `No record of role changes yet` | `7162921` 2026-06-28 | enterprise/audit.html | — |
| 160 | `No records` | `7162921` 2026-06-28 | templates/admin-reconciliation.html | — |
| 161 | `No routing rules` | `cc8c1bd` 2026-07-27 | js/page-admin-models.js | — |
| 162 | `No subscription plan yet` | `7162921` 2026-06-28 | templates/admin-subscriptions.html | — |
| 163 | `No subscription record yet` | `7162921` 2026-06-28 | templates/my-subscriptions.html | — |
| 164 | `No supplier yet` | `7162921` 2026-06-28 | templates/admin-providers.html | — |
| 165 | `No system user groups` | `7162921` 2026-06-28 | templates/admin-groups.html | — |
| 166 | `No upstream keys` | `cc8c1bd` 2026-07-27 | js/page-admin-models-aws.js | — |
| 167 | `No upstream keys available` | `cc8c1bd` 2026-07-27 | js/page-admin-models-aws.js | — |
| 168 | `No usage data` | `f6dae60` 2026-06-24 | enterprise/billing.html | — |
| 169 | `Not Verified` | `f917184` 2026-06-20 | templates/admin-monitor-upstream.html | — |
| 170 | `One API Key for leading AI models. Standard OpenAI format, pay-as-you-go, enterprise-grade security.` | `fe4d631` 2026-08-13 | templates/landing.html | — |
| 171 | `One Key, All models` | `7ea4d1c` 2026-08-13 | templates/landing.html | — |
| 172 | `Operation Failed` | `cc8c1bd` 2026-07-27 | js/page-admin-providers.js | `Operation failed` |
| 173 | `Operation Successful` | `cc8c1bd` 2026-07-27 | js/page-admin-providers.js | — |
| 174 | `Output ($/1M)` | `7f5f474` 2026-08-12 | templates/admin-upstream-pricing.html | — |
| 175 | `Output ($/1M) *` | `eb54b46` 2026-07-02 | templates/admin-models-general.html | — |
| 176 | `Passport, National ID, or Driver's License` | **(never)** | — | `Passport, National ID, Driver's license` |
| 177 | `Pause Model` | `cc8c1bd` 2026-07-27 | js/page-admin-models.js | `Pause model` |
| 178 | `Pause failed: ` | `cc8c1bd` 2026-07-27 | js/page-admin-models-aws.js | `Pause failed:` |
| 179 | `Permanently deactivate account` | `c5b1ca3` 2026-07-13 | templates/profile.html | — |
| 180 | `Please fill in at least one top-up amount` | `cc8c1bd` 2026-07-27 | js/page-admin-enterprise.js | — |
| 181 | `Please retry` | `cc8c1bd` 2026-07-27 | js/page-admin-models.js | — |
| 182 | `Please select a Provider` | `cc8c1bd` 2026-07-27 | js/page-admin-models.js | `Please select a provider` |
| 183 | `Please select models to add` | `cc8c1bd` 2026-07-27 | js/page-admin-models.js | — |
| 184 | `Please upload business license, authorization letter, or other documents required for enterprise verification.` | `bdd1cf0` 2026-07-01 | templates/enterprise-apply.html | — |
| 185 | `Pool Top-ups` | `e0ab050` 2026-07-25 | enterprise/audit.html | — |
| 186 | `Pricing Breakdown` | **(never)** | — | — |
| 187 | `Product ID (product_id)` | `bf7bd80` 2026-07-05 | templates/admin-models-general.html | — |
| 188 | `Project Name` | `81f558f` 2026-07-16 | templates/usage-logs.html | — |
| 189 | `Projected Cost` | `f71fa52` 2026-07-26 | enterprise/dashboard.html | — |
| 190 | `Projected Cost (Local)` | `f71fa52` 2026-07-26 | enterprise/dashboard.html | — |
| 191 | `Projected full-month cost based on current daily average` | `f71fa52` 2026-07-26 | enterprise/dashboard.html | — |
| 192 | `Proof of Business Address` | **(never)** | — | — |
| 193 | `Quota / Credit Change Log` | `e0ab050` 2026-07-25 | enterprise/audit.html | — |
| 194 | `Quota Overview` | `81f558f` 2026-07-16 | enterprise/dashboard.html | — |
| 195 | `Quota cap` | `8f1f9f1` 2026-07-25 | enterprise/users.html | — |
| 196 | `RPMonth Limit` | **(never)** | — | — |
| 197 | `Rate Limit Usage` | `3037ae8` 2026-07-18 | templates/admin-rate-limits.html | — |
| 198 | `Reasoning ($/1M)` | `7f5f474` 2026-08-12 | templates/admin-upstream-pricing.html | — |
| 199 | `Recent audit logs` | `4610eb7` 2026-06-21 | templates/admin.html | — |
| 200 | `Recently Used Keys` | `ce2cd39` 2026-06-21 | templates/dashboard.html | — |
| 201 | `Records of enterprise member role changes and quota/credit changes.` | `f6dae60` 2026-06-24 | enterprise/audit.html | — |
| 202 | `Registered At` | `4610eb7` 2026-06-21 | templates/admin.html | — |
| 203 | `Reject Failed` | `cc8c1bd` 2026-07-27 | js/page-admin-enterprise.js | `Reject failed` |
| 204 | `Remove Failed` | `cc8c1bd` 2026-07-27 | js/page-admin-enterprise.js | `Remove failed` |
| 205 | `Remove Member` | `c3a41e5` 2026-07-27 | enterprise/users.html | `Remove member` |
| 206 | `Repeat Applicant` | **(never)** | — | — |
| 207 | `Req Diff` | `1c4432c` 2026-07-18 | templates/admin-reconciliation-ops.html | — |
| 208 | `Request Failed` | `cc8c1bd` 2026-07-27 | js/page-admin-enterprise.js | `Request failed` |
| 209 | `Request Usage` | `f6dae60` 2026-06-24 | enterprise/billing.html | — |
| 210 | `Request Volume` | `ddcc213` 2026-06-22 | templates/admin.html | — |
| 211 | `Request package remaining` | `a336165` 2026-07-17 | templates/dashboard.html | — |
| 212 | `Requester` | `aa63b25` 2026-06-25 | templates/admin-enterprise-change-requests.html | `Request` |
| 213 | `Requests / Month` | `3b8ead7` 2026-07-12 | enterprise/keys.html | — |
| 214 | `Requests by User` | `81f558f` 2026-07-16 | enterprise/dashboard.html | — |
| 215 | `Restore Model` | `cc8c1bd` 2026-07-27 | js/page-admin-models.js | `Restore model` |
| 216 | `Restore failed: ` | `cc8c1bd` 2026-07-27 | js/page-admin-models.js | `Restore failed:` |
| 217 | `Resume failed: ` | `cc8c1bd` 2026-07-27 | js/page-admin-models-aws.js | `Resume failed:` |
| 218 | `Role Change Log` | `f89a7b4` 2026-07-26 | enterprise/audit.html | — |
| 219 | `Save Failed` | `cc8c1bd` 2026-07-27 | js/page-admin-enterprise.js | `Save failed` |
| 220 | `Save failed: ` | `cc8c1bd` 2026-07-27 | js/page-admin-models-aws.js | `Save failed:` |
| 221 | `Search by name or email` | `3037ae8` 2026-07-18 | templates/admin-rate-limits.html | — |
| 222 | `Search mailbox` | `8eec38e` 2026-07-19 | enterprise/users.html | — |
| 223 | `Search member...` | `f6dae60` 2026-06-24 | enterprise/reconciliation.html | — |
| 224 | `Select File` | `bdd1cf0` 2026-07-01 | templates/enterprise-apply.html | — |
| 225 | `Send reset link` | `1af62bd` 2026-07-27 | templates/forgot-password.html | — |
| 226 | `Set failed: ` | `cc8c1bd` 2026-07-27 | js/page-admin-models.js | `Set failed:` |
| 227 | `Set quota limit for` | `ef914c4` 2026-07-25 | enterprise/users.html | — |
| 228 | `Sort Order` | `3ac1695` 2026-07-21 | templates/admin-models-aws.html | — |
| 229 | `Status Counts` | **(never)** | — | — |
| 230 | `Submit your enterprise information for review. Once approved, you will become the enterprise administrator.` | `155d961` 2026-06-24 | templates/enterprise-apply.html | — |
| 231 | `Sync completed. Updated keys: ` | `cc8c1bd` 2026-07-27 | js/page-admin-models.js | `Sync completed. Updated keys:` |
| 232 | `Sync models from OpenRouter API? This will update the local cached model list.` | `cc8c1bd` 2026-07-27 | js/page-admin-models.js | — |
| 233 | `Sync successful! Updated ` | `cc8c1bd` 2026-07-27 | js/page-admin-models.js | — |
| 234 | `TPMonth Limit` | **(never)** | — | — |
| 235 | `Tax Registration Certificate` | **(never)** | — | — |
| 236 | `The model name recognized by the Provider (such as MiniMax-M2.7, claude-opus-4-6-20251101)` | `bf7bd80` 2026-07-05 | templates/admin-models-general.html | — |
| 237 | `The unified model name used when the user requests it, used for route matching (lowercase + number + hyphen, such as claude-opus-4-6)` | `bf7bd80` 2026-07-05 | templates/admin-models-general.html | — |
| 238 | `This Month's Usage` | `a336165` 2026-07-17 | templates/dashboard.html | — |
| 239 | `This action cannot be undone. Emails will be anonymized and API keys will be deactivated.` | `c5b1ca3` 2026-07-13 | templates/profile.html | — |
| 240 | `This is the lowest price across all providers. Enterprise and premium group pricing may differ. See your group pricing on the Subscription & Group page.` | `de602b8` 2026-08-13 | templates/models-gallery.html | — |
| 241 | `Token Usage` | `f6dae60` 2026-06-24 | enterprise/billing.html | — |
| 242 | `Token Usage (7d)` | `e0ad216` 2026-06-21 | templates/dashboard.html | — |
| 243 | `Tokens (1h)` | `3037ae8` 2026-07-18 | templates/admin-rate-limits.html | — |
| 244 | `Tokens / Month` | `3b8ead7` 2026-07-12 | enterprise/keys.html | — |
| 245 | `Tokens by User` | `81f558f` 2026-07-16 | enterprise/dashboard.html | — |
| 246 | `Top up $7, get $3 free` | `c17bddc` 2026-08-13 | templates/layout.html | — |
| 247 | `Top up now` | `c17bddc` 2026-08-13 | templates/layout.html | — |
| 248 | `Top-up Failed` | `cc8c1bd` 2026-07-27 | js/page-admin-enterprise.js | — |
| 249 | `Top-up Successful` | `cc8c1bd` 2026-07-27 | js/page-admin-enterprise.js | — |
| 250 | `Total Cost (micro-USD)` | `90abdb3` 2026-06-25 | enterprise/usage.html | — |
| 251 | `Upload document for this category` | **(never)** | — | — |
| 252 | `Upstream Key Usage` | `055da1b` 2026-06-20 | templates/admin-upstream-usage.html | — |
| 253 | `Upstream Key aggregate usage on a daily basis for reconciliation with Provider` | `055da1b` 2026-06-20 | templates/admin-upstream-usage.html | — |
| 254 | `Usage Distribution` | `81f558f` 2026-07-16 | templates/usage-logs.html | — |
| 255 | `Usage by Member` | `929bf7b` 2026-06-25 | enterprise/usage.html | — |
| 256 | `Usage by User` | `f6dae60` 2026-06-24 | enterprise/billing.html | — |
| 257 | `Used So Far` | `f71fa52` 2026-07-26 | enterprise/dashboard.html | — |
| 258 | `Utility bill, bank statement header, or government letter (< 3 months)` | **(never)** | — | — |
| 259 | `Validating key...` | `cc8c1bd` 2026-07-27 | js/page-admin-keypool.js | — |
| 260 | `Validation complete! Valid: ` | `cc8c1bd` 2026-07-27 | js/page-admin-keypool.js | — |
| 261 | `Voided check, bank statement, or bank confirmation letter` | **(never)** | — | — |
| 262 | `record_count` | `055da1b` 2026-06-20 | templates/admin-upstream-usage.html | — |
| 263 | `⚠️ Are you sure you want to permanently delete enterprise "{name}"?\n\nThis action will:\n- Remove all member enterprise associations\n- Delete enterprise quota and settings\n- This action is irreversible` | `0acaf78` 2026-06-18 | js/page-admin-enterprise.js | `Are you sure you want to permanently delete this enterprise?\n\nThis will:\n- Remove all member enterprise associations\n- Delete enterprise quota and settings\n- This action cannot be undone.` |


#### A.2 `Fetching.*`（1）

- `Fetching...`

#### A.2 `admin.*`（1）

- `admin.upstream_usage`

#### A.2 `billing.*`（1）

- `billing.recharge`

#### A.2 `common.*`（3）

- `common.by`
- `common.dimension_user`
- `common.loading`

#### A.2 `enterprise.*`（1）

- `enterprise.reconciliation`

#### A.2 `error.*`（59）

- `error.activate_failed`
- `error.add_failed_with_reason`
- `error.alipay_appid_required`
- `error.already_admin`
- `error.already_super_admin`
- `error.approve_failed`
- `error.cannot_change_own_role`
- `error.cannot_delete_group_with_members_or_keys`
- `error.cannot_demote_last_super_admin`
- `error.cannot_remove_last_admin`
- `error.create_failed_with_reason`
- `error.create_order_failed`
- `error.create_retry`
- `error.current_password_wrong`
- `error.demote_failed`
- `error.encrypt_failed_with_reason`
- `error.encryption_key_required`
- `error.enter_valid_amount`
- `error.enterprise_max_admins`
- `error.enterprise_max_members`
- `error.enterprise_name_required`
- `error.group_not_in_enterprise`
- `error.impersonate_needs_jwt`
- `error.invalid_amount`
- `error.invalid_reset_link`
- `error.invalid_role`
- `error.invitation_quota_insufficient`
- `error.key_create_failed`
- `error.key_name_required`
- `error.member_update_failed`
- `error.model_not_found`
- `error.not_super_admin`
- `error.password_min_length_6`
- `error.password_process_failed`
- `error.password_update_failed`
- `error.payment_failed`
- `error.payment_method_required`
- `error.paypal_config_required`
- `error.promote_failed`
- `error.provider_not_found`
- `error.remove_failed`
- `error.required_fields`
- `error.reset_link_expired`
- `error.revoke_failed`
- `error.role_must_be_member_admin`
- `error.send_failed_retry`
- `error.setting_key_required`
- `error.stripe_config_required`
- `error.suspend_failed`
- `error.too_many_requests`
- `error.transfer_failed`
- `error.update_failed_with_reason`
- `error.update_group_failed`
- `error.user_added_to_enterprise`
- `error.user_delete_failed`
- `error.user_in_other_enterprise`
- `error.user_must_register_first`
- `error.user_not_in_enterprise`
- `error.wechat_config_required`

#### A.2 `info.*`（2）

- `info.key_created`
- `info.order_created`

#### A.2 `mail.*`（1）

- `mail.button.reset_password`

#### A.2 `msg.*`（38）

- `msg.admin_added`
- `msg.alipay_config_updated`
- `msg.deleted`
- `msg.enterprise_activated`
- `msg.enterprise_created`
- `msg.enterprise_deleted`
- `msg.enterprise_suspended`
- `msg.key_activated`
- `msg.key_added_validating`
- `msg.key_created`
- `msg.key_deleted`
- `msg.key_revoked`
- `msg.key_status_updated`
- `msg.model_approved`
- `msg.model_created`
- `msg.model_deleted`
- `msg.model_status_updated`
- `msg.password_changed`
- `msg.password_reset_link_sent_if_email_exists`
- `msg.payment_processing`
- `msg.paypal_config_updated`
- `msg.priority_updated`
- `msg.provider_added`
- `msg.provider_status_updated`
- `msg.provider_updated`
- `msg.routing_rule_deleted`
- `msg.setting_created`
- `msg.status_updated`
- `msg.stripe_config_updated`
- `msg.super_admin_demoted_to_system_admin`
- `msg.updated`
- `msg.user_added`
- `msg.user_deleted`
- `msg.user_not_registered_invitation_sent`
- `msg.user_promoted_to_super_admin`
- `msg.user_removed`
- `msg.user_role_updated`
- `msg.wechat_config_updated`

#### A.2 `orders.*`（2）

- `orders.amount`
- `orders.status_pending`

#### A.2 `status.*`（1）

- `status.pending_short`


### B. Schema 中未被代码引用的列（informational，best-effort）

共 **252** 列（已跳过 id/created_at/updated_at/deleted_at 等通用列）。

> 强烈提示：这是**尽力而为**的静态推断。别名、`SELECT *`、`RETURNING`、以及 CODE_DIRS 之外的代码路径都可能隐藏真实使用。**删除任何列前必须人工确认**，并优先用 migration + schema.go 同步变更。


#### announcements（12）

- `announcements.banner_color`
- `announcements.content`
- `announcements.cta_link`
- `announcements.cta_text`
- `announcements.dismiss_mode`
- `announcements.display_mode`
- `announcements.end_at`
- `announcements.icon_emoji`
- `announcements.pinned`
- `announcements.show_popup`
- `announcements.start_at`
- `announcements.target_audience`

#### api_keys（4）

- `api_keys.key_type`
- `api_keys.model_binding`
- `api_keys.model_priority`
- `api_keys.subscription_id`

#### archival_jobs（7）

- `archival_jobs.job_type`
- `archival_jobs.period_end`
- `archival_jobs.period_start`
- `archival_jobs.records_archived`
- `archival_jobs.records_deleted`
- `archival_jobs.started_at`
- `archival_jobs.storage_location`

#### audit_logs（1）

- `audit_logs.log_id`

#### channel_health_history（1）

- `channel_health_history.tested_at`

#### email_notifications（3）

- `email_notifications.body`
- `email_notifications.sent_at`
- `email_notifications.subject`

#### enterprise_alerts（3）

- `enterprise_alerts.acknowledged_at`
- `enterprise_alerts.acknowledged_by`
- `enterprise_alerts.notified_at`

#### enterprise_application_documents（8）

- `enterprise_application_documents.content_type`
- `enterprise_application_documents.document_type`
- `enterprise_application_documents.file_hash`
- `enterprise_application_documents.file_size`
- `enterprise_application_documents.original_name`
- `enterprise_application_documents.storage_provider`
- `enterprise_application_documents.uploaded_at`
- `enterprise_application_documents.uploaded_by`

#### enterprise_applications（1）

- `enterprise_applications.documents`

#### enterprise_change_requests（2）

- `enterprise_change_requests.field`
- `enterprise_change_requests.requested_by`

#### enterprise_members（1）

- `enterprise_members.remove_reason`

#### enterprises（3）

- `enterprises.last_payment_amount`
- `enterprises.last_payment_confirmed_at`
- `enterprises.last_payment_confirmed_by`

#### gdpr_export_requests（1）

- `gdpr_export_requests.requested_at`

#### model_group_health_stats（14）

- `model_group_health_stats.auth_error_count`
- `model_group_health_stats.availability`
- `model_group_health_stats.avg_latency_ms`
- `model_group_health_stats.bucket`
- `model_group_health_stats.content_mismatch_count`
- `model_group_health_stats.error_4xx_count`
- `model_group_health_stats.error_5xx_count`
- `model_group_health_stats.invalid_request_count`
- `model_group_health_stats.model`
- `model_group_health_stats.network_error_count`
- `model_group_health_stats.rate_limit_count`
- `model_group_health_stats.response_timeout_count`
- `model_group_health_stats.slow_latency_ms`
- `model_group_health_stats.timeout_count`

#### model_pricing（3）

- `model_pricing.cache_read_price`
- `model_pricing.confirmed_by`
- `model_pricing.model_id`

#### model_pricing_rules（7）

- `model_pricing_rules.block_seconds`
- `model_pricing_rules.effective_from`
- `model_pricing_rules.effective_to`
- `model_pricing_rules.note`
- `model_pricing_rules.tiers`
- `model_pricing_rules.time_rules`
- `model_pricing_rules.timezone`

#### model_products（2）

- `model_products.model_id`
- `model_products.release_date`

#### orders（1）

- `orders.model_binding`

#### payment_config（2）

- `payment_config.config_key`
- `payment_config.config_value`

#### pricing_history（6）

- `pricing_history.changed_at`
- `pricing_history.input_price_new`
- `pricing_history.input_price_old`
- `pricing_history.model_id`
- `pricing_history.output_price_new`
- `pricing_history.output_price_old`

#### pricing_sources（2）

- `pricing_sources.cache_read_price`
- `pricing_sources.model_id`

#### provider_health_stats（13）

- `provider_health_stats.auth_error_count`
- `provider_health_stats.availability`
- `provider_health_stats.avg_latency_ms`
- `provider_health_stats.bucket`
- `provider_health_stats.content_mismatch_count`
- `provider_health_stats.error_4xx_count`
- `provider_health_stats.error_5xx_count`
- `provider_health_stats.invalid_request_count`
- `provider_health_stats.network_error_count`
- `provider_health_stats.rate_limit_count`
- `provider_health_stats.response_timeout_count`
- `provider_health_stats.slow_latency_ms`
- `provider_health_stats.timeout_count`

#### provider_health_stats_model（14）

- `provider_health_stats_model.auth_error_count`
- `provider_health_stats_model.availability`
- `provider_health_stats_model.avg_latency_ms`
- `provider_health_stats_model.bucket`
- `provider_health_stats_model.content_mismatch_count`
- `provider_health_stats_model.error_4xx_count`
- `provider_health_stats_model.error_5xx_count`
- `provider_health_stats_model.invalid_request_count`
- `provider_health_stats_model.model`
- `provider_health_stats_model.network_error_count`
- `provider_health_stats_model.rate_limit_count`
- `provider_health_stats_model.response_timeout_count`
- `provider_health_stats_model.slow_latency_ms`
- `provider_health_stats_model.timeout_count`

#### schema_migrations（1）

- `schema_migrations.applied_at`

#### sensitive_check_logs（3）

- `sensitive_check_logs.checked_at`
- `sensitive_check_logs.content_preview`
- `sensitive_check_logs.matched_words`

#### service_model_approvals（4）

- `service_model_approvals.model_id`
- `service_model_approvals.notes`
- `service_model_approvals.submitted_at`
- `service_model_approvals.submitted_by`

#### service_models（15）

- `service_models.avg_latency_ms`
- `service_models.cache_read_price`
- `service_models.cache_write_price`
- `service_models.confirmed_by`
- `service_models.discontinued_notes`
- `service_models.discontinued_reason`
- `service_models.eol_confirmed_at`
- `service_models.eol_confirmed_by`
- `service_models.is_hot`
- `service_models.paused_notes`
- `service_models.protected`
- `service_models.success_rate`
- `service_models.suggested_api_name`
- `service_models.validated_at`
- `service_models.validation_result`

#### single_model_purchases（5）

- `single_model_purchases.amount_paid`
- `single_model_purchases.model_id`
- `single_model_purchases.token_quota`
- `single_model_purchases.token_remaining`
- `single_model_purchases.token_used`

#### subscription_plans（8）

- `subscription_plans.model_permissions`
- `subscription_plans.rp5h_limit`
- `subscription_plans.rpd_limit`
- `subscription_plans.rph_limit`
- `subscription_plans.rpw_limit`
- `subscription_plans.tpd_limit`
- `subscription_plans.tph_limit`
- `subscription_plans.tpw_limit`

#### super_admin_downgrade_requests（4）

- `super_admin_downgrade_requests.approved_at`
- `super_admin_downgrade_requests.approved_by`
- `super_admin_downgrade_requests.frozen_at`
- `super_admin_downgrade_requests.requester_id`

#### upstream_key_usage（6）

- `upstream_key_usage.avg_latency_ms`
- `upstream_key_usage.cost`
- `upstream_key_usage.date`
- `upstream_key_usage.model`
- `upstream_key_usage.token_count`
- `upstream_key_usage.upstream_key_id`

#### upstream_keys（3）

- `upstream_keys.allowed_regions`
- `upstream_keys.last_synced_at`
- `upstream_keys.notes`

#### usage_records（35）

- `usage_records.actual_group_id`
- `usage_records.api_key_name`
- `usage_records.audio_input_tokens`
- `usage_records.audio_output_tokens`
- `usage_records.balance_deduction`
- `usage_records.cache_cost`
- `usage_records.cache_hit`
- `usage_records.cached_input_tokens`
- `usage_records.client_geo`
- `usage_records.client_ip`
- `usage_records.credit_deduction`
- `usage_records.effective_input_tokens`
- `usage_records.endpoint`
- `usage_records.error`
- `usage_records.error_code`
- `usage_records.gift_deduction`
- `usage_records.input_cost`
- `usage_records.is_fallback`
- `usage_records.is_streaming`
- `usage_records.model`
- `usage_records.original_track`
- `usage_records.output_cost`
- `usage_records.pricing_error`
- `usage_records.pricing_ratio_at_request`
- `usage_records.project_id`
- `usage_records.project_name`
- `usage_records.reasoning_cost`
- `usage_records.reasoning_intensity`
- `usage_records.reasoning_tokens`
- `usage_records.recharge_deduction`
- `usage_records.subscription_deduction`
- `usage_records.total_cost`
- `usage_records.ttft_ms`
- `usage_records.upstream_key_id`
- `usage_records.user_api_key_id`

#### usage_summary_daily（3）

- `usage_summary_daily.day`
- `usage_summary_daily.model`
- `usage_summary_daily.total_cost`

#### usage_summary_hourly（3）

- `usage_summary_hourly.hour`
- `usage_summary_hourly.model`
- `usage_summary_hourly.total_cost`

#### user_devices（1）

- `user_devices.device_id`

#### user_groups（1）

- `user_groups.model_permissions`

#### user_subscriptions（30）

- `user_subscriptions.auto_renew`
- `user_subscriptions.plan_name`
- `user_subscriptions.request_remaining`
- `user_subscriptions.request_total`
- `user_subscriptions.request_used`
- `user_subscriptions.rp5h_limit`
- `user_subscriptions.rp5h_reset_at`
- `user_subscriptions.rp5h_used`
- `user_subscriptions.rpd_limit`
- `user_subscriptions.rph_limit`
- `user_subscriptions.rph_reset_at`
- `user_subscriptions.rph_used`
- `user_subscriptions.rpm_reset_at`
- `user_subscriptions.rpm_used`
- `user_subscriptions.rpw_limit`
- `user_subscriptions.rpw_reset_at`
- `user_subscriptions.rpw_used`
- `user_subscriptions.started_at`
- `user_subscriptions.tpd_limit`
- `user_subscriptions.tph_limit`
- `user_subscriptions.tph_reset_at`
- `user_subscriptions.tph_used`
- `user_subscriptions.tpm_reset_at`
- `user_subscriptions.tpm_used`
- `user_subscriptions.tpmonth_limit`
- `user_subscriptions.tpmonth_reset_at`
- `user_subscriptions.tpmonth_used`
- `user_subscriptions.tpw_limit`
- `user_subscriptions.tpw_reset_at`
- `user_subscriptions.tpw_used`

#### users（15）

- `users.backup_email`
- `users.data_retention_days`
- `users.email_verified`
- `users.enterprise_config`
- `users.invoice_info`
- `users.locale`
- `users.locked_until`
- `users.login_attempts`
- `users.privacy_region`
- `users.remember_me_expires_at`
- `users.remember_me_token`
- `users.shipping_address`
- `users.theme`
- `users.timezone`
- `users.user_type`

#### verification_codes（1）

- `verification_codes.purpose`

#### voucher_uses（1）

- `voucher_uses.reward_granted`

#### vouchers（2）

- `vouchers.apply_trigger`
- `vouchers.auto_apply_on_register`

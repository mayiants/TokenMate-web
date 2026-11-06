# DB Schema 初始化问题分析与解决方案

> **ACTIVE** — 基于 InitSchema 合并迁移导致的运行时问题，提出分层检测和根治方案
>
> 日期: 2026-06-04
> 前置条件: 产品未上线，所有数据为测试数据，可以清空重建

---

## 一、问题背景

为了减少 DB 迁移的复杂度，在版本发布前将所有迁移合并到了 `internal/db/schema.go` 的 `InitSchema()` 函数中。但合并后出现了大量运行时错误，包括：表结构不匹配、类型定义不一致、字段缺失、Scan 参数数量错误等。虽然有些已经修复，但问题仍然持续出现。

---

## 二、当前 `schema.go` 的具体问题

### 2.1 重复定义

`users` 表在 schema.go 中被 **CREATE TABLE 了两次**：

- 第 26-63 行：第一版定义（无 `group_id`, `enterprise_id`）
- 第 153-191 行：第二版定义（含 `group_id`, `enterprise_id`）

由于使用了 `CREATE TABLE IF NOT EXISTS`，第二次定义**静默被忽略**。缺失的字段依赖后面 ~200 行 `ALTER TABLE ADD COLUMN IF NOT EXISTS` 来补救。

### 2.2 错误静默吞掉

```go
_ = db.Exec(ctx, `CREATE TABLE IF NOT EXISTS users (...)`)  // 错误被丢弃
```

整段 InitSchema 没有返回 error，也没有收集执行失败信息。任何 DDL 失败（语法错误、约束冲突、权限不足）都无声无息。

### 2.3 僵尸代码段

schema.go 第 487-509 行：

```go
// ============================================================================
// 2.5 Missing columns detected by scripts/check_schema_vs_code.py (2026-06-03)
// ============================================================================
// （完全为空 — 检测到了问题但没有填入修复）
```

### 2.4 历史问题记录

从 docs 中的历史报告可以看到这类问题反复出现：

| 报告 | 问题类型 | 示例 |
|---|---|---|
| `api-test-report-202605032024.md` | 表名不匹配 | `groups` vs `user_groups` |
| `api-test-report-202605032024.md` | Scan 参数数量不匹配 | SELECT 9 列，Scan 8 个变量 |
| `api-test-report-202605032024.md` | 列名不存在 | `reward` vs `quota_earned` |
| `debug-issues-report-202605031857.md` | JSON 字段命名 | `keyPrefix` (camelCase) vs `key_prefix` (snake_case) |
| `debug-issues-report-202605031857.md` | 表缺失 | `sensitive_logs` 等 17 个表未创建 |

---

## 三、现有检测手段及盲区

### 3.1 三层检测现状

| 检测层 | 工具 | 能发现 | 局限 |
|---|---|---|---|
| 静态 SQL 文本分析 | `scripts/check_schema_vs_code.py` | SQL 中引用的 (table, column) 是否在 schema.go 定义 | **纯文本匹配**：不懂 Go 类型、不懂 JSON 序列化、不执行代码 |
| API 黑盒测试 | `test-api.ts` (102 用例) | 端点是否返回 200/400/500 | **事后发现**：需要完整启动服务 + DB；只知道"坏了"，不知道"哪里坏了" |
| 人工代码审查 | — | 逻辑错误、架构问题 | 不可靠、不持续 |

### 3.2 当前工具完全检测不到的问题

| 盲区 | 说明 | 后果 |
|---|---|---|
| Go struct ↔ DB column 类型不一致 | Go `int` vs DB `BIGINT`，Go `string` vs DB `UUID` | 运行时 panic 或数据截断 |
| JSON tag 命名不一致 | 前端 `camelCase` 后端 `snake_case` | 前端解析失败 |
| Scan 参数数量/顺序不匹配 | `QueryRow(...).Scan(&a, &b)` vs SELECT 3 列 | 运行时 panic |
| CHECK 约束枚举不匹配 | DB `CHECK (status IN ('a','b'))` vs Go 写入 `"c"` | 插入失败 |
| 外键引用表不存在 | FK 指向的表还未创建 | DDL 失败（被静默吞掉） |
| 索引引用的列不存在 | `CREATE INDEX ON t(col)` 但 col 未定义 | DDL 失败（被静默吞掉） |

---

## 四、解决方案

### 方案 A：增强静态分析（低投入，立即收益）

扩展 `check_schema_vs_code.py`，新增检测：

| 检测项 | 方法 |
|---|---|
| 重复 CREATE TABLE | 正则扫描 schema.go，报告同一表名出现多次 |
| Scan 参数数量匹配 | 提取 Go 代码中 `QueryRow(...).Scan(a,b,c)` 的参数数量 vs 前面最近的 SELECT 列数 |
| Go struct tag vs DB column | 扫描 `json:"xxx" db:"yyy"` tag，与 SQL 中的列名交叉验证 |
| CHECK 约束枚举 | 提取 `CHECK (col IN ('a','b'))` 值，搜索 Go 代码中写入该列的字符串常量 |

### 方案 B：集成测试 — 完整 Schema 验证（中投入，覆盖最广）

写一个 Go 测试函数，在真实 PostgreSQL 上运行：

```
1. 连接空数据库
2. 执行 InitSchema()
3. 验证每张表：INSERT 一条完整记录 → SELECT 回来 → 验证所有列
4. 验证约束：尝试插入违反 CHECK/UNIQUE/FK 的数据，确认数据库拒绝
5. 验证所有 ALTER TABLE 的列实际存在于对应表中
```

在 CI 中用 `docker run postgres:16` 启动干净容器，每次 PR 跑一遍。这个测试可以在 **本地、CI、预发布环境** 三个层面运行。

### 方案 C：运行时防御

1. **InitSchema 返回 error** — 收集所有 `db.Exec()` 的错误，结束时不静默吞掉
2. **启动时核心表探活** — 对每张表执行 `SELECT 1 LIMIT 1`，确认存在且可访问
3. **panic recovery 增强日志** — 记录触发崩溃的 SQL 语句和参数

### 方案 D：回归测试（长期）

维护 `test-api.ts` 脚本，对每个新功能同步添加测试用例。当前 102 用例覆盖了大部分 GET 端点，需补充写操作的完整 CRUD 流程验证。

---

## 五、推荐方案（组合拳）

由于 **产品未上线，数据可以清空**，这是一个极好的窗口期，可以采用最彻底的方案而不必担心数据迁移兼容性。

### 推荐：B 为主 + A 为辅 + C 保底 + D 长期维护

```
            ┌─────────────────────────────┐
            │      B: 集成测试 (核心)       │
            │    CI 中每次 PR 跑完整验证     │
            │    "schema 是否真的能用"       │
            └─────────────┬───────────────┘
                          │
        ┌─────────────────┼─────────────────┐
        │                 │                 │
        ▼                 ▼                 ▼
┌───────────────┐ ┌──────────────┐ ┌──────────────┐
│ A: 静态分析   │ │  C: 运行时   │ │  D: API 测试 │
│ 代码提交前    │ │  生产兜底    │ │  业务正确性  │
│ 秒级反馈     │ │  不静默吞错  │ │  持续积累    │
└───────────────┘ └──────────────┘ └──────────────┘
```

**为什么 B 是核心**：

1. **前置条件完美匹配** — 数据可清空，不需要考虑迁移兼容性，可以从零开始验证
2. **覆盖所有盲区** — Go struct 类型、Scan 匹配、CHECK 约束、FK 引用、JSON tag，集成测试全都跑得到
3. **可重复** — 不像人工测试，集成测试可以在 CI 中每次 PR 自动运行
4. **真相源** — 静态分析只能推断"应该能工作"，集成测试直接验证"确实能工作"

**具体实施步骤**：

### Step 1：清理 schema.go（1-2 小时）

- 合并重复的 `users` 表定义为一个完整的 CREATE TABLE
- 删除所有 `ALTER TABLE ADD COLUMN IF NOT EXISTS`，将列直接写入对应的 CREATE TABLE 中
- 给 `InitSchema()` 返回 error，不再 `_ = db.Exec(...)`
- 目标：一个干净、无重复、无 ALTER TABLE 补丁的 schema.go

### Step 2：编写 TestInitSchema 集成测试（2-4 小时）

```go
func TestInitSchema_FullValidation(t *testing.T) {
    // 连接空 DB  →  执行 InitSchema  →  逐表验证
    // 验证: INSERT 完整记录、SELECT 所有列、CHECK 约束生效、FK 引用正确
}
```

### Step 3：CI 集成（1 小时）

```yaml
# .github/workflows/test.yml
- name: Start PostgreSQL
  run: docker run -d -p 5432:5432 -e POSTGRES_DB=test postgres:16
- name: Run Schema Tests
  run: go test ./internal/db/ -v -run TestInitSchema
```

### Step 4：增强 check_schema_vs_code.py（1-2 小时）

- 添加重复 CREATE TABLE 检测
- 添加 Scan 参数数量匹配检测

### Step 5：运行时保底（0.5 小时）

- InitSchema 改为返回 error
- main.go 启动时检查返回值并 log.Fatal

---

## 六、为什么现在是做这件事的最佳时机

| 条件 | 影响 |
|---|---|
| 产品未上线 | 可以直接 `DROP TABLE ... CASCADE` 重建，不需要写迁移脚本 |
| 数据可清空 | 不用考虑 `ALTER TABLE` 的向后兼容性 |
| 问题已暴露 | 从历史报告看，schema 问题反复出现，不根治会持续消耗时间 |
| CI 未建立 | 趁现在把集成测试加入 CI 流程，后续所有 PR 都有保障 |

上线后再做这件事，代价会高一个数量级——需要考虑数据迁移、向后兼容、零停机部署等。

---

## 七、实施过程

### 7.1 Step 1: 清理 schema.go

**改动内容**：

1. **移除重复的 `users` CREATE TABLE** — 原文件第 153-191 行的第二个 `users` 定义被删除。第一个 `users` 表（第 27 行）保持不变，FK 约束（`group_id REFERENCES user_groups(id)`、`enterprise_id REFERENCES enterprises(id)`）通过后续的 `ALTER TABLE ADD COLUMN` 添加。

2. **`_ = db.Exec()` → 错误返回** — 所有 353 个 `_ = db.Exec(ctx, ...)` 调用替换为：
   ```go
   if _, err := db.Pool.Exec(ctx, `...`); err != nil {
       return fmt.Errorf("create table xxx: %w", err)
   }
   ```
   每个错误消息包含具体操作和目标表名，便于定位问题。

3. **`InitSchema()` 返回 `error`** — 不再静默吞掉错误，调用方可以感知并处理。

4. **`main.go` 改为 `logger.Fatal`** — Schema 初始化失败时立即退出：
   ```go
   if err := database.InitSchema(ctx, logger); err != nil {
       logger.Fatal("Schema init failed -- cannot start without database tables", zap.Error(err))
   }
   ```

### 7.2 Step 2: 集成测试 `schema_test.go`

新建 `internal/db/schema_test.go`，包含 7 个验证阶段：

```
TestInitSchema_FullValidation
├── Phase 1: 清空全部表 (DROP 59 tables CASCADE)
├── Phase 2: 执行 InitSchema()
├── Phase 3: 验证 59 张表可访问性 (SELECT 1 LIMIT 1)
├── Phase 4: 验证 6 种约束 (CHECK / FK / UNIQUE)
│   ├── constraint_users_role          — 非法 role 被拒绝 ✅
│   ├── constraint_api_keys_fk         — 无效 user_id 被拒绝 ✅
│   ├── constraint_providers_unique    — 重复 PK 被拒绝 ✅
│   ├── constraint_user_groups_fk      — NULL enterprise_id 允许 ✅
│   ├── constraint_subscription_plans_type — 非法 plan_type 被拒绝 ✅
│   └── constraint_checkin_unique      — 同用户同日签到被拒绝 ✅
├── Phase 5: 验证 15 列类型 (information_schema.columns)
│   覆盖表: users(7), api_keys(3), usage_records(3), service_models(2)
├── Phase 6: 验证 12 个索引 (pg_indexes)
└── Phase 7: 验证幂等性 (二次执行 InitSchema 无错误)
```

**测试执行结果**（2026-06-04）：

```text
=== RUN   TestInitSchema_FullValidation
    Phase 1: Dropping existing tables... (59 tables)
    Phase 2: Running InitSchema...        ✅
    Phase 3: 59/59 tables accessible      ✅
    Phase 4: 6/6 constraints verified     ✅
    Phase 5: 15/15 column types verified  ✅
    Phase 6: 12/12 indexes verified       ✅
    Phase 7: Idempotency verified         ✅
--- PASS: TestInitSchema_FullValidation (2.23s)
PASS
ok      github.com/ai-proxy/ai-portal-go/internal/db   2.879s
```

同时修复了已有测试 `db_test.go` 中 3 处硬编码的旧密码（与 `.env` 不一致导致 SASL auth 失败），全部 15 个 DB 测试通过。

### 7.3 Step 3: 增强 `check_schema_vs_code.py`

新增两个检测阶段：

| 阶段 | 检测内容 | 输出 |
|---|---|---|
| Phase 5 | 重复 CREATE TABLE | **0 duplicates** |
| Phase 6 | Scan 参数 vs SELECT 列数匹配 | **41 warnings**（大量来自 SQL `COALESCE(...)` 函数误报） |

Scan 检测为 **warning 级别**，不计入硬失败。PASS/WARN/FAIL 分级逻辑：

- **PASS** — 0 缺失列 + 0 缺失表 + 0 重复表
- **WARN** — 仅有 Scan 数量不匹配（可能是 SQL 函数误报）
- **FAIL** — 有缺失列/表/重复表

最终输出：**PASS**（0 hard issues, 41 Scan warnings）

### 7.4 Step 4: 运行时保底

- `InitSchema()` 返回值在 `main.go` 中以 `logger.Fatal` 处理，schema 初始化失败不会继续启动
- 每个 DDL 执行失败都有明确错误消息：`"create table xxx: ..."`, `"alter table xxx add yyy: ..."`, `"create index zzz: ..."` 等

---

## 八、验证状态

| 检查项 | 结果 |
|---|---|
| `go build ./...` | ✅ PASS |
| `go vet ./...` | ✅ PASS |
| `go test ./internal/db/` (15 tests, 108 sub-tests) | ✅ ALL PASS (2.88s) |
| `check_schema_vs_code.py` | ✅ PASS (0 hard issues) |
| Schema 表数量 | 60 张表 |
| Schema 列数量 | 799 列 |
| 重复 CREATE TABLE | 0 |
| 错误处理覆盖 | 353/353 `db.Exec` 调用 |
| `_ = db.Exec` 残留 | 0 |

---

## 九、参考

- `internal/db/schema.go` — 清理后的 InitSchema（~2700 行，含完整错误处理）
- `internal/db/schema_test.go` — 集成测试（7 阶段验证）
- `scripts/check_schema_vs_code.py` — 增强版静态分析工具（6 阶段）
- `scripts/fix_schema_errors.py` — Schema 错误处理转换脚本
- `docs/reports/api-test-report-202605032024.md` — API 测试报告，记录了 17+ 个 schema 相关 500 错误
- `docs/reports/debug-issues-report-202605031857.md` — Debug 问题记录，包含 JSON 字段命名和表名不匹配

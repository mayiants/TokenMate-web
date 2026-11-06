# 生产 Review  remediation 收尾报告（第 2 轮）

> **Status:** ACTIVE
> 生成时间（UTC）：2026-09-04 09:45
> 前置文档：`docs/reports/production-review-report-202609021205.md`（第 1 轮，全部发现清单）
> 范围：核对第 1 轮 P0–P2 每项的当前状态，完成剩余修复

---

## 1. 结论

第 1 轮报告的全部 P0/P1 项与绝大部分 P2 项已关闭。本轮补齐最后 2 个代码项
（Portal B-M3 resume 守卫、B-M2 discontinue 错误透传）。剩余 2 项非代码项
需要用户决策（见 §3）。

## 2. 逐项状态核对

### 安全

| 项 | 状态 | 说明 |
|---|---|---|
| S-H1 AccessAPI key 入库 | ✅ 已修（`0fd6eb0`） | 脚本已删除；key 吊销由用户在 AccessAPI 控制台执行 |
| S-H2 GitHub PAT 明文 | ⏳ **用户负责** | 用户明确"稍后替换"，本轮不处理 |
| S-H3 start.sh E2E 默认注入 | ✅ 已修（`0fd6eb0`） | 默认 `SEED_E2E_ACCOUNTS=false` |
| S-M1 proxy config.yaml 出库 | ✅ 已修（`8bdadaf`） | 已 `git rm --cached` 并加入 exclude |
| S-M2 审计数据备份 | ⏳ **待用户确认** | 每晚 pg_dump 覆盖全库含审计表，但只能按快照恢复；如需 PITR 级恢复需确认 RDS/snapshot 策略 |

### 计费正确性

| 项 | 状态 | 说明 |
|---|---|---|
| B-H1 异步计费/退款、audio 路由 | ✅ 大部分已修（`8bdadaf`+`da59716`） | 改为终态成功首 poll 计费（SETNX 幂等），失败永不计费无需退款；Kling 大写 SUCCESS 分类 bug 已修。**audio 异步结果仍无轮询路由**——用户决策"audio 缺乏测试条件，暂不修改"，保留为已知限制 |
| B-M1 订阅退款吞错误 | ✅ 已修（`0fd6eb0`） | 返回 error 并中止 |
| B-M2 discontinue 恒 200 | ✅ **本轮修复**（`c1e210a`） | 函数返回 error，handler 映射 500；自动 EOL 任务记录 warn 继续 |
| B-M3 resume 复活手动停用规则 | ✅ **本轮修复**（`c1e210a`） | pause 把级联停用的 pricing_rule/routing 行 ID 记入 `service_models.paused_reason`（pauseCascadeMarker），resume 只精确恢复这些行；无 marker 的旧 pause 回退按名恢复并打 warn 日志 |
| B-M4 媒体预扣低估 | ✅ 已修（`8bdadaf`） | 预扣 = 最贵变体单价 × image_count/duration（按时长维度），不再只冻结 1 单位；`matchVariant` 已严格化（精确/阈值匹配 + 时长交叉校验） |
| B-M2(5.2) 媒体余额门控 | ✅ 已修（`8bdadaf`） | 402 BALANCE_EXHAUSTED 实测通过 |
| B-M3(5.2) n/duration 上限 | ✅ 已修（`8bdadaf`） | n≤10、duration≤120，400 拒绝，实测通过 |
| B-M4(5.2) task_id 注入 | ✅ 已修（`8bdadaf`） | `^[A-Za-z0-9_-]{1,64}$` 校验 |

### 性能

| 项 | 状态 | 说明 |
|---|---|---|
| P-M1 usage_records 索引 | ✅ 已修（`0fd6eb0` migration 0010） | `(upstream_key_id, created_at)` |
| P-M2 service_models 索引 | ✅ 已修（`0fd6eb0`） | `(product_id)`、`(api_model_name)` |
| P-M3 analytics 刷新互斥 | ✅ 已修（`8bdadaf`） | TryLock + 409，实测验证过 |
| P-M4 媒体响应 LimitReader | ✅ 已修（`8bdadaf`） | 512MiB 上限，超限 502 |
| P-M5 容器 memory limit | ✅ 部分（`0fd6eb0`） | compose 模板已加 postgres 1g / redis 256m / portal 1g；**Portal 主机无 swap 仍未加**（P3，余量大，可延后） |

## 3. 剩余开放项（需用户决策）

1. **S-H2 GitHub PAT**：用户在 GitHub 撤销后从 HANDOVER.md 删除（用户明确自己处理）。
2. **S-M2 审计备份**：确认 nightly pg_dump（`/opt/ai-platform/backups/`）是否满足合规要求；如需 PITR 需另配 WAL 归档/快照。
3. **audio 异步轮询路由**（B-H1 残余）：~~不修改~~ → **已实现**（proxy `e09d91c`，2026-09-04）：新增 `GET /v1/audio/generations/:task_id` 路由，镜像 image/video 轮询处理器；dispatch 侧修复为先识别 async envelope、经 `storeMediaTaskBilling` 延迟计费（此前会在派发时立即扣费且 poll 无路由）。内部调用测试：httptest 假上游跑通 dispatch→poll 全链路，终态成功计费恰好一次；无真实 audio 异步上游场景，mock 级验证。
4. **P3 LOW 项**：可随下次迭代顺带。
5. **Portal 主机 swap**：复查后发现 **已有 4G swap**（`/swapfile`，fstab 持久化，`free -h` 确认 used 217MB），09-02 报告"无 swap"已过时，无需处理。
6. **S-H2/S-M2 最终决策**（2026-09-04 用户确认）：现有措施足够（PAT 自行轮换、nightly pg_dump 可接受），不追加投入。

## 4. 本轮变更验证

- `go build` / `go vet` / `gofmt` 干净
- fast-gate 单测 12 包全过；`internal/routes` DB 测试（`-p 1`）全过（43s）
- `scripts/check_schema_vs_code.sh` 通过（无 schema 变更）
- Portal 容器重建后 `/api/health` ok

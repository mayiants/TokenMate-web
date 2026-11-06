# 任务：P3-A2 —— 系统管理后台第二批：分组与计划 + 模型与定价 + 上游管理（woodgit-platform/web）

规范与准入同 P3-A1 任务书（system admin 专属 `/console/admin/*`，Drawer CRUD、dangerConfirm、Toast、双 i18n、build 验证）。导航在「平台管理」组追加本批三项。

## A4 分组与计划 `/console/admin/groups`
- 用户组：表格（名称/类型/限额 RPM·TPM/并发/模型数/成员数/状态/操作）+ 创建/编辑 Drawer（名称/类型 Select/限额/并发/模型多选「来源 /api/models 列表」）+ 删除 dangerConfirm
- 订阅计划页签：计划列表卡片或表格（名称/价格/周期/额度/模型权限数/订阅数/上下架 Switch）+ 编辑 Drawer；读 admin subscription/plan 源码端点
- 后端：admin-groups.go / admin-subscriptions.go / rate-limits 相关

## A5 模型与定价 `/console/admin/models`（合并老 admin-models-general/aws/pricing 三页）
- 页签：①通用模型（表格：供应商/模型名/类型/上下文/上下架/操作「定价」）②AWS 模型（区域/型号/价格字段——admin-models-aws 源码）③定价规则（pricing 规则表格：模型/计费模式/输入输出价/缓存价/优先级/操作「编辑定价」Drawer）
- 批量操作：导入/同步（有端点则接，如 import/export 或 sync）

## A6 上游管理 `/console/admin/upstream`（合并 providers/keypool/monitor 三页）
- 页签：①上游渠道（providers 卡片/表格：名称/协议/状态药丸/优先级/操作「编辑」Drawer/「探测」按钮调 test 端点）②密钥池（keypool 表格：上游/池名/权重/健康度/今日用量/操作；轮换/禁用 Drawer 或按钮）③监控（monitor-downstream/monitor-upstream 数据：健康时间线或状态表+异常列表）
- 后端：admin-providers.go / admin-keypool.go / monitor 相关

## 流程
- 每页 commit：`feat(web): P3-A2-...`，WorkBuddy Agent
- 完成 build 零错误 + check:i18n；输出 API 清单+遗留

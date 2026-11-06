# Enterprise 成员管理两级审批方案

> 创建日期: 2026-07-12
> 状态: 已实现（commit b796ec0）
> 关联文档: [enterprise-self-registration-design-202606231945.md](enterprise-self-registration-design-202606231945.md)（企业创建与认证）、[enterprise-creation-authorization-202606151600.md](enterprise-creation-authorization-202606151600.md)

## 背景

平台对企业客户采用**后付费**模式：企业成员产生的 API 调用成本由企业统一结算。这意味着「谁能加入企业」直接关联资金风险——一个未经审核的成员可以立刻消耗企业额度。

旧流程存在两个权限漏洞：

1. **Enterprise Admin（EA）自邀自批**：EA 邀请任意邮箱（包括企业域名外的邮箱），被邀请人注册后只需 EA 一级审批即可入企。EA 实质上可以绕过 System Admin（SA）把任何人加入企业。
2. **SA 邀请直接入企**：SA 在后台邀请一个已注册用户时，该用户被直接写入 `enterprise_members`，完全不经过 EA。

## 设计原则

- **域名白名单内自治**：企业配置了 `allowed_domains` 且邮箱域名匹配时，视为企业可自证身份，EA 一级审批即可。
- **域名外/无白名单双级把关**：域名不匹配（或企业未配置白名单）时，必须 EA → SA 两级审批，SA 终审通过才入企。
- **EA 邀请 = EA 隐式批准**：EA 发起的邀请本身即代表 EA 审批，被邀请人注册后直接等待 SA 终审，不再要求 EA 二次操作。
- **SA 不预批任何一级**：SA 发起的邀请仍需 EA 先审批（无论域名是否匹配），避免 SA 单方面塞人进企业。
- **拒绝任一级可直接完成**：EA 和 SA 都可以直接拒绝，无需对方参与。

## 状态机

`enterprise_invitations.status` 在原 `pending` / `email_confirmed` / `approved` / `rejected` / `cancelled` / `accepted` 基础上新增 **`ea_approved`**，无数据库 schema 变更。

```
pending（已邀请，未注册）
   │ 被邀请人点击邮件链接注册/登录
   ▼
  ┌─ 域名匹配 且 邀请人是 EA ──→ accepted（自动入企，流程结束）
  │
  ├─ 邀请人是 EA（域名不匹配）──→ ea_approved（EA 隐式批准，待 SA 终审）
  │
  └─ 其他（SA 邀请 / 无邀请人）──→ email_confirmed（待 EA 审批）
                                      │ EA 审批
                                      ├─ 域名匹配 ──→ approved（入企）
                                      └─ 域名不匹配 ──→ ea_approved（待 SA 终审）
                                                            │ SA 终审
                                                            ▼
                                                          approved（入企）
```

### 审批规则矩阵

| 场景 | 需要的审批 | 说明 |
|------|-----------|------|
| 域名匹配 + EA 邀请 | EA（隐式） | 注册即自动入企 |
| 域名匹配 + SA 邀请 | EA | SA 邀请不预批，EA 审批后入企 |
| 域名不匹配 + EA 邀请 | EA（隐式）→ SA | 注册后置 `ea_approved`，SA 终审入企 |
| 域名不匹配 + SA 邀请 | EA → SA | 两级显式审批 |
| EA 直接添加已注册用户（域名匹配） | 无（旧行为不变） | 直接入企 |
| EA 直接添加已注册用户（域名不匹配） | SA | 创建 `ea_approved` 邀请，SA 终审入企 |
| SA 邀请已注册用户 | EA | 改为创建 `email_confirmed` 邀请（旧行为是直接入企，已修复） |

## 实现方式

### 后端（`internal/routes/enterprise_members.go`）

- **`acceptInvitation`**（注册/接受邀请）：查询邀请时 LEFT JOIN 邀请人用户表得到 `inviterRole`，计算 `autoJoin = isDomainMatched && inviterIsEA`；非 autoJoin 时按邀请人身份置 `ea_approved` 或 `email_confirmed`。
- **`approveInvitation`**（EA 审批端点 `PUT /api/enterprise/invitations/:invId/approve`）：审批前检查企业 `allowed_domains`；域名不匹配只把邀请推进到 `ea_approved`，返回 `pending_sa_approval: true`。
- **`approveInvitationAdmin`**（SA 终审端点 `POST /api/admin/enterprises/:id/invitations/:invId/approve`）：只接受 `ea_approved` 状态的邀请，否则返回 400（`error.invitation_needs_ea_approval`）；终审在事务内完成成员插入（`ON CONFLICT ... DO UPDATE` 恢复已移除成员）+ 用户 `enterprise_id`/role 更新 + 重复邀请清理。
- **`addEnterpriseMember`**（EA 添加已存在用户）：域名匹配维持旧行为直接入企；不匹配改为创建 `ea_approved` 邀请等待 SA 终审。
- **`inviteMember`**（SA 邀请）：已存在用户不再直接入企，改为创建 `email_confirmed` 邀请；审计日志记录。
- 全文件 12 处 `status IN ('pending', 'email_confirmed')`  widening 为含 `ea_approved`（成员上限计数、重复邀请检查、取消、拒绝、清理等），保证中间态邀请计入名额且可被取消/拒绝。

### 前端

- **EA 成员页**（`web/templates/enterprise/users.html`）：邀请列表三态展示——`pending` 显示「等待注册」+ 复制邀请链接、`email_confirmed` 显示「等待您的审批」+ Approve 按钮（此前 EA 页面没有任何审批入口，本次新增）、`ea_approved` 显示「等待系统管理员审批」。添加用户的成功提示区分「直接加入」与「等待 SA 终审」。
- **SA 企业页**（`web/templates/admin-enterprise.html`）：Members 弹窗邀请列表同样三态展示，Approve 按钮仅对 `ea_approved` 显示，Reject 始终可用。

### 测试

`internal/routes/enterprise_members_approval_test.go` 覆盖 5 个用例：EA 邀请域名外用户注册后置 `ea_approved`、SA 邀请需 EA 先批（SA 直接终审返回 400）、域名匹配 EA 审批直接入企、EA 终审域名外邀请、`ea_approved` 计入成员上限。

## 已知边界与后续工作

- **域名验证尚未做强制拥有权校验**：当前 `allowed_domains` 由 SA 在 Edit 抽屉中填写，没有要求企业在 DNS 中设置 A/CNAME/TXT 记录来证明域名控制权。这是后续加固方向——域名白名单是审批豁免的唯一依据，一旦可被随意填写，两级审批就会被绕过。在实现 DNS 验证之前，SA 填写白名单时应人工核实。
- `inviteMember` 中预留了 `allowedDomains` 查询（`_ = allowedDomains`），为未来基于域名的分支逻辑保留。
- SA 邀请已存在用户不再发送「已加入企业」通知邮件（语义上改为「等待审批」）。

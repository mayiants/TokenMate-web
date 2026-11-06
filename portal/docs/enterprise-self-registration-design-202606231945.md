# Enterprise 自助注册与认证增强方案

> 创建日期: 2026-06-23  
> 状态: COMPLETE — 已实施并上线（enterprise-apply 页面、enterprise_applications 表、审批流程均已实现）

## 背景

当前系统中 Enterprise Admin 角色完全由 System Admin 手动指定，普通用户无法自助申请创建企业。这限制了企业客户的自助接入效率。

## 核心设计原则

1. **创建者 ≠ Admin** — 提交申请不自动获得管理权限，必须通过资质审核证明"有权代表公司"
2. **不靠名称去重** — 企业名称相似性判定是工商/法律层面的事，平台不做自动判断
3. **不依赖唯一编号** — 不同国家的企业标识体系差异大（UEN/统一信用代码/EIN等），不作为必填字段
4. **人工审核为主** — 现阶段企业客户数量可控，人工审核是最可靠、成本最低的方案
5. **未认证企业自然受限** — quota/credit = 0 意味着无法调用 API，无需额外冻结逻辑

## 流程设计

### 阶段 1: 用户提交企业注册申请

任何已注册用户均可提交：

- 企业名称
- 国家/地区
- 联系信息（联系人、邮箱、电话）
- 企业注册文件（PDF/图片）— 营业执照或等效文件
- 授权代表证明 — 证明申请人有权代表公司（如任命书、公司章程节选）
- 企业唯一编号（选填）— 如有 UEN、统一社会信用代码等

提交后状态变为 `pending_review`。

### 阶段 2: System Admin 审核

System Admin 在管理后台：

1. 查看待审核申请列表
2. 下载/查看上传的资质文件
3. 核实文件真实性和申请人身份
4. 操作：
   - **通过** → 自动创建企业 + 申请人成为 enterprise_admin + 标记 `verified_at`
   - **拒绝** → 填写拒绝原因，通知申请人
   - **要求补充** → 标记为 `needs_more_info`，申请人可重新上传

### 阶段 3: 认证后运营

审核通过后：
- System Admin 按需分配 quota/credit
- Enterprise Admin 可邀请成员、管理分组、配置密钥
- 企业正常运营

### 超时机制

- 提交后 7 个自然日未补全材料的申请 → 自动关闭（可重新申请）
- Admin 空缺不影响企业实体存在（但没有 quota = 无法使用）

## 未认证企业的权限

| 操作 | 未认证 | 已认证 |
|------|--------|--------|
| API 调用 | ❌（quota=0） | ✅ |
| 邀请成员 | ✅ | ✅ |
| 管理分组/密钥 | ✅ | ✅ |
| 获得 quota/credit | ❌ | ✅（System Admin 分配） |
| 开具发票 | ❌ | ✅ |

核心思路：未认证企业可以搭建组织架构（准备工作），但无法产生任何有成本的操作。System Admin 不会给未认证企业分配资源。

## 关于"多人申请同一企业"的处理

### 问题

- 基层员工/技术人员可能先于真正的管理者提交申请
- 平台无法自动判断谁才是合法代表

### 方案

**由 System Admin 在审核环节判断**：

- 审核时核实"授权代表证明"文件
- 无法提供证明的申请 → 拒绝，建议联系公司管理层申请
- 同一公司多份申请 → 人工识别后只批准有合法授权的那份

不做技术层面的自动去重或抢注机制。

## 关于企业名称相似性

**平台不做相似度判断。**

原因：
- "adc co,ltd" 和 "adc co.Limited" 可能是同一家也可能不是（不同国家）
- NLP 相似度匹配会产生误判
- 这是法律层面的问题，不应由技术解决

真正的去重由 System Admin 审核时人眼判断。如果发现重复，拒绝后一份并告知原因。

## 关于不同国家的企业标识

| 地区 | 唯一编号 | 平台处理 |
|------|---------|---------|
| 新加坡 | UEN | 选填，有助于审核 |
| 中国 | 统一社会信用代码 | 选填，有助于审核 |
| 美国 | EIN | 选填，无公开查询渠道 |
| 东南亚小国 | 无标准编号 | 不要求 |

所有编号均为**选填**，仅辅助人工审核，不作为系统去重依据。平台不自动验证编号真伪——由 System Admin 结合上传文件综合判断。

## 数据模型变更

### 新增表: `enterprise_applications`

```sql
CREATE TABLE enterprise_applications (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    applicant_user_id UUID NOT NULL REFERENCES users(id),
    enterprise_name VARCHAR(255) NOT NULL,
    country VARCHAR(100) NOT NULL,
    contact_name VARCHAR(255),
    contact_email VARCHAR(255),
    contact_phone VARCHAR(100),
    registration_number VARCHAR(255),  -- UEN/统一信用代码等，选填
    documents JSONB DEFAULT '[]',       -- [{filename, storage_path, uploaded_at}]
    status VARCHAR(20) DEFAULT 'pending_review',  -- pending_review / needs_more_info / approved / rejected / expired
    rejection_reason TEXT,
    reviewed_by UUID REFERENCES users(id),
    reviewed_at TIMESTAMP,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);
```

### enterprises 表变更

```sql
ALTER TABLE enterprises ADD COLUMN IF NOT EXISTS verified_at TIMESTAMP;
ALTER TABLE enterprises ADD COLUMN IF NOT EXISTS application_id UUID REFERENCES enterprise_applications(id);
```

`verified_at` 为 NULL 表示未认证。

## API 端点

### 用户侧

- `POST /api/enterprise-application` — 提交申请（含文件上传）
- `GET /api/enterprise-application` — 查看自己的申请状态
- `PUT /api/enterprise-application/:id` — 补充材料（状态为 needs_more_info 时）

### System Admin 侧

- `GET /api/admin/enterprise-applications` — 待审核列表
- `GET /api/admin/enterprise-applications/:id` — 申请详情
- `POST /api/admin/enterprise-applications/:id/approve` — 通过
- `POST /api/admin/enterprise-applications/:id/reject` — 拒绝
- `POST /api/admin/enterprise-applications/:id/request-info` — 要求补充材料

## 实现优先级

1. **P0**: enterprise_applications 表 + 提交申请 API + Admin 审核列表/操作
2. **P1**: 文件上传存储（对接 S3 或本地）+ 超时自动关闭定时任务
3. **P2**: 邮件通知（申请状态变更时通知申请人）
4. **P3**: 申请人前端页面（提交表单 + 状态追踪）

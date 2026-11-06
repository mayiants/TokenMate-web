# 优惠码体系完整指南

> **ACTIVE** - 最后更新：2026年5月

## 一、优惠码体系总览

WOODGIT 平台的优惠码体系包含 5 种类型，覆盖从注册到付费的全生命周期：

| 类型 | 中文名 | 触发时机 | 发放方 | 奖励对象 | 使用次数 | 余额去向 |
|------|--------|----------|--------|----------|----------|----------|
| welcome | 欢迎码 | 注册时自动 | 系统预设 | 新用户 | 999999（全员） | gift_balance |
| invitation | 邀请码 | 注册时填写 | 用户生成 | 邀请人+被邀请人 | 可配置（上限100） | gift_balance / aff_quota |
| balance | 余额兑换码 | 手动兑换 | 管理员发放 | 使用者本人 | 可配置（上限10000） | gift_balance |
| promo | 促销码 | 手动兑换 | 管理员发放 | 使用者本人 | 可配置（上限10000） | gift_balance |
| plan | 套餐码 | 手动兑换 | 管理员发放 | 使用者本人 | 可配置（上限10000） | 订阅计划 |

**核心设计原则：**
- 所有免费奖励（welcome、invitation、balance、promo）均进入 `gift_balance`，消费优先级最高
- 用户付费充值进入 `recharge_balance`，消费优先级低于免费奖励
- `balance` 是总余额（gift_balance + recharge_balance），仅作为记账字段

## 二、欢迎码（welcome）

### 定义

欢迎码是新用户注册时自动发放的一次性奖励，属于注册触发型优惠。与邀请码类似，都是在注册环节自动生效，不需要用户手动操作。

### 特性

- **触发时机**：用户完成注册后自动应用
- **发放方**：系统预设（管理员在"邀请码管理"页面配置）
- **使用次数**：max_uses 设为 999999（实际为全员可领取）
- **不可手动兑换**：欢迎码在 `redeemCode()` 中被显式拦截，防止用户手动输入兑换
- **金额**：默认 10 元（管理员可调整）
- **余额去向**：gift_balance + balance（消费优先级最高）

### 管理位置

欢迎码管理已移至 **邀请码管理页面**（`/admin/invitations`），与邀请码配置放在一起，因为两者都是注册时触发的一次性优惠。

管理员可以：
- 查看当前欢迎码（兑换码、金额、状态、过期时间）
- 编辑欢迎码金额和状态
- 设置过期时间

**API 端点：**
- `GET /api/admin/invitations/welcome-code` — 获取当前欢迎码
- `POST /api/admin/invitations/welcome-code` — 创建或更新欢迎码

### 实现细节

欢迎码的发放逻辑在 `auth.go` 的 `applyWelcomeBonus()` 函数中：

```go
// 查找活跃的欢迎码
SELECT id, amount FROM redemption_codes
WHERE type = 'welcome' AND status = 'active'
AND (expires_at IS NULL OR expires_at > NOW())

// 添加到 gift_balance + balance
UPDATE users SET gift_balance = COALESCE(gift_balance, 0) + $1,
                balance = balance + $1 WHERE id = $2::uuid

// 记录兑换记录
INSERT INTO redemption_records (user_id, code_id, code, amount, ...)
```

## 三、邀请码（invitation）

### 定义

邀请码是用户推荐机制，用户生成邀请码分享给朋友，朋友注册时双方获得奖励。

### 特性

- **触发时机**：注册时填写邀请码
- **发放方**：用户自行生成
- **奖励对象**：邀请人 + 被邀请人（双奖励）
- **格式**：`INV-XXXXXXXX`
- **余额去向**：自动转入 gift_balance（默认）或 aff_quota（手动模式）

### 邀请码规则

| 配置项 | 默认值 | 硬上限 | 说明 |
|--------|--------|--------|------|
| 每人最大邀请码数 | 10 | 100 | 每个用户最多可生成的活跃邀请码数量 |
| 每个码最大使用次数 | 10 | 100 | 每个邀请码最多可被使用的次数 |
| 有效期 | 365天 | 365天 | 邀请码从生成到失效的天数 |
| 邀请人奖励 | ¥5 | - | 成功邀请一人后邀请人获得的金额 |
| 被邀请人奖励 | ¥2 | - | 使用邀请码注册后获得的金额 |
| 奖励自动转入 | 是 | - | 邀请奖励自动转入可用余额 |

硬上限在 `generate()` 函数中强制执行，无论管理员配置值如何，超过上限时自动取上限值。

### 邀请奖励机制

**自动转入模式（默认）：**
- 邀请人奖励自动转入 `gift_balance`（赠送余额）
- 赠送余额在消费时优先级最高，最先被扣费
- 无需手动操作，奖励立即可用

**手动转入模式：**
- 邀请人奖励存入 `aff_quota`（邀请额度）
- 需要用户手动点击"奖励转余额"才能使用
- 转入后进入 `gift_balance`，消费优先级最高

### 分页与筛选

**管理员端：**
- 邀请码列表：支持按状态筛选（全部/活跃/禁用/已过期），每页20条
- 邀请记录：每页20条，支持翻页
- API：`GET /api/admin/invitations?page=1&limit=20&status=active`

**用户端：**
- 邀请码列表：一次性加载（用户最多100个码）
- 邀请记录：每页20条，支持翻页
- API：`GET /api/invitation/records?page=1&limit=20`

## 四、余额兑换码（balance）

### 定义

余额兑换码是管理员发放的一次性优惠码，用户手动输入后获得余额奖励。适用于活动赠送、合作推广等场景。

### 特性

- **触发时机**：用户手动兑换
- **发放方**：管理员创建
- **奖励对象**：使用者本人
- **格式**：`WG-XXXX-XXXX-XXXX`
- **余额去向**：gift_balance + balance（消费优先级最高）
- **可配置项**：金额、最大使用次数、备注、过期时间

### 管理员创建

管理员可在"兑换码管理"页面创建余额兑换码：

1. 单个创建：设置名称、类型（余额兑换）、金额、最大使用次数、备注、过期时间
2. 批量生成：设置名称前缀、类型、金额、数量（最多100个）、最大使用次数、备注、过期时间

### 使用流程

1. 登录后进入"兑换码管理"页面
2. 在输入框中输入兑换码
3. 点击"兑换"按钮
4. 兑换成功后金额自动加入 `gift_balance`

### 规则

- 每个兑换码有使用次数上限（max_uses），达到后自动失效
- 兑换码有有效期，过期无法使用
- 每个用户只能使用同一个兑换码一次
- 兑换码奖励进入 `gift_balance`，消费优先级最高
- 兑换码不可转让、不可兑换现金、不可退款
- 严禁买卖兑换码，违者账户将被冻结

## 五、促销码（promo）

### 定义

促销码是管理员发放的限时优惠码，与余额兑换码类似，但通常使用次数更高（如50次、100次），适用于限时促销活动。

### 特性

- **触发时机**：用户手动兑换
- **发放方**：管理员创建
- **奖励对象**：使用者本人
- **格式**：`WG-XXXX-XXXX-XXXX`
- **余额去向**：gift_balance + balance（消费优先级最高）
- **与 balance 的区别**：促销码通常 max_uses 较高，适合多人共享使用

### 使用场景

- 限时促销活动（如双十一、新年活动）
- 合作伙伴推广（给合作伙伴一个促销码，其用户都可兑换）
- 新用户引流（在社交媒体发布促销码，吸引新用户注册）

### 管理员创建

与余额兑换码创建流程相同，只需将类型选择为"促销优惠"。

### 规则

与余额兑换码规则相同，额外建议：
- 促销码建议设置过期时间，避免长期有效导致滥用
- 促销码建议设置合理的 max_uses（如50-100次），避免无限使用

## 六、套餐码（plan）

### 定义

套餐码是管理员发放的兑换码，兑换后获得订阅计划（Token Plan 或 Code Plan），而非余额。

### 特性

- **触发时机**：用户手动兑换
- **发放方**：管理员创建
- **奖励对象**：使用者本人
- **类型细分**：token_plan（Token 计划）和 code_plan（Code 计划）
- **余额去向**：不直接增加余额，而是授予订阅计划

### 管理员创建

管理员创建套餐码时：
1. 类型选择"计划兑换"
2. 选择具体的订阅计划（从下拉列表中选择）
3. 系统自动将兑换码类型设置为对应的 token_plan 或 code_plan

### 当前实现状态

套餐码目前为半实现状态：
- DB 已有 `plan_id` 字段，创建时可绑定计划
- `redeemCode()` 目前仍将 amount 加入余额，尚未实现"兑换后授予订阅计划"的逻辑
- 后续需要完善：兑换时检查 plan_id，创建/延长用户的订阅而非增加余额

## 七、余额消费优先级

WOODGIT 平台的余额消费顺序：

```
订阅计划 → gift_balance → recharge_balance → balance
```

详细说明：

1. **订阅计划** — 如果用户有活跃订阅，API 调用首先消耗订阅额度
2. **赠送余额（gift_balance）** — 欢迎奖励、签到奖励、兑换码、邀请奖励等免费获得的额度
3. **充值余额（recharge_balance）** — 用户主动充值的部分
4. **普通余额（balance）** — 总余额（= gift_balance + recharge_balance），仅作为记账字段

**核心原则：免费获得的额度最先被消费，用户自己充值的部分最后消费，确保用户的付费余额不会被优先消耗。**

### gift_balance 的一致性保障

所有免费奖励必须同时更新 `gift_balance` 和 `balance`：

```sql
UPDATE users SET gift_balance = COALESCE(gift_balance, 0) + $1,
                balance = balance + $1 WHERE id = $2::uuid
```

如果只更新 `balance` 而不更新 `gift_balance`，则 `gift_balance = 0`，消费优先级链会跳过它，导致用户的充值余额被优先消耗——这与设计意图相反。

**已修复的 bug：**
- `redemption.go` 的 `redeemCode()` 之前只更新 `balance`，现已修复
- `auth.go` 的 `applyWelcomeBonus()` 之前只更新 `balance`，现已修复
- `checkin.go` 的签到奖励之前只更新 `balance`，现已修复

## 八、优惠码类型对比

| 特性 | 欢迎码 | 邀请码 | 余额兑换码 | 促销码 | 套餐码 |
|------|--------|--------|------------|--------|--------|
| 触发时机 | 注册自动 | 注册填写 | 手动兑换 | 手动兑换 | 手动兑换 |
| 发放方 | 系统预设 | 用户生成 | 管理员 | 管理员 | 管理员 |
| 奖励对象 | 新用户 | 邀请人+被邀请人 | 使用者 | 使用者 | 使用者 |
| 奖励内容 | 余额 | 余额 | 余额 | 余额 | 订阅计划 |
| 使用次数 | 全员 | 可配置 | 可配置 | 可配置（通常较高） | 可配置 |
| 余额去向 | gift_balance | gift_balance/aff_quota | gift_balance | gift_balance | 订阅计划 |
| 管理页面 | 邀请码管理 | 邀请码管理 | 兑换码管理 | 兑换码管理 | 兑换码管理 |
| 可手动兑换 | 否 | 否 | 是 | 是 | 是 |

## 九、管理员配置

### 邀请码管理页面（/admin/invitations）

**邀请码配置：**
- `invitation_max_codes_per_user` — 每人最大邀请码数
- `invitation_default_max_uses` — 每个码最大使用次数
- `invitation_default_expire_days` — 有效期天数
- `invitation_inviter_reward` — 邀请人奖励金额
- `invitation_invitee_reward` — 被邀请人奖励金额
- `invitation_auto_transfer_enabled` — 奖励自动转入开关

**欢迎码管理：**
- 查看当前欢迎码（兑换码、金额、状态、过期时间）
- 编辑欢迎码金额和状态
- 创建或更新欢迎码

### 兑换码管理页面（/admin/redemption）

**兑换码列表：**
- 支持按类型筛选（余额兑换/促销优惠/Token Plan/Code Plan）
- 支持按状态筛选（活跃/已禁用/已过期/已用完）
- 分页显示（每页20条）

**创建兑换码：**
- 名称、类型（余额兑换/促销优惠/计划兑换）
- 金额（余额/促销类型）或选择计划（计划类型）
- 最大使用次数（默认1，上限10000）
- 备注（可选）
- 过期时间（可选）

**批量生成：**
- 名称前缀、类型、金额、数量（最多100个）
- 最大使用次数、备注、过期时间

**操作：**
- 禁用/启用兑换码

### DB Schema

```sql
-- redemption_codes 表
CREATE TABLE redemption_codes (
  id UUID PRIMARY KEY,
  code VARCHAR(20) UNIQUE,
  name VARCHAR(100),
  type VARCHAR(20) CHECK (type IN ('welcome', 'balance', 'promo', 'token_plan', 'code_plan')),
  amount INTEGER,          -- 金额（分）
  plan_id UUID,            -- 关联的订阅计划ID（plan类型）
  max_uses INTEGER,        -- 最大使用次数
  used_count INTEGER,      -- 已使用次数
  description TEXT,        -- 备注
  reward_type VARCHAR(20) DEFAULT 'gift_balance' CHECK (reward_type IN ('gift_balance', 'recharge_balance')),
  status VARCHAR(20) DEFAULT 'active',
  expires_at TIMESTAMP,
  created_by UUID,
  created_at TIMESTAMP DEFAULT NOW(),
  updated_at TIMESTAMP DEFAULT NOW()
);

-- invitation_codes 表
CREATE TABLE invitation_codes (
  id BIGSERIAL PRIMARY KEY,
  code VARCHAR(20) UNIQUE,
  inviter_id UUID REFERENCES users(id),
  reward_type VARCHAR(20),
  reward_amount FLOAT,      -- 邀请人奖励（元）
  invitee_reward_amount FLOAT, -- 被邀请人奖励（元）
  max_uses INTEGER,
  use_count INTEGER,
  expires_at TIMESTAMP,
  status VARCHAR(20) DEFAULT 'active',
  created_at TIMESTAMP DEFAULT NOW()
);
```

### API 端点汇总

| 端点 | 方法 | 说明 |
|------|------|------|
| `/api/redemption/redeem` | POST | 用户兑换码 |
| `/api/redemption/my-records` | GET | 用户兑换记录 |
| `/api/redemption/admin/codes` | GET | 管理员兑换码列表（分页+筛选） |
| `/api/redemption/admin/codes` | POST | 管理员创建兑换码 |
| `/api/redemption/admin/codes/:id` | PATCH | 管理员更新兑换码 |
| `/api/redemption/admin/codes/:id` | DELETE | 管理员删除兑换码 |
| `/api/redemption/admin/records` | GET | 管理员兑换记录 |
| `/api/redemption/admin/stats` | GET | 兑换码统计 |
| `/api/admin/invitations/welcome-code` | GET | 获取欢迎码 |
| `/api/admin/invitations/welcome-code` | POST | 创建/更新欢迎码 |
| `/api/admin/invitations` | GET | 管理员邀请码列表 |
| `/api/admin/invitations/records` | GET | 管理员邀请记录 |
| `/api/invitation/my-codes` | GET | 用户邀请码列表 |
| `/api/invitation/generate` | POST | 用户生成邀请码 |
| `/api/invitation/records` | GET | 用户邀请记录 |
| `/api/invitation/transfer-quota` | POST | 用户转移邀请额度 |

## 十、法律条款

### 兑换码条款

- 兑换码为赠送性质，不支持退款
- 兑换码不属于付费消费，不支持开具发票
- 兑换码不可转让、不可兑换现金
- 严禁买卖兑换码，违者账户将被冻结
- 兑换码有有效期，过期无法使用

### 邀请码条款

- 邀请奖励为赠送性质，不支持退款
- 邀请奖励不属于付费消费，不支持开具发票
- 邀请额度（aff_quota）永不过期，可随时转入余额使用
- 严禁通过虚假注册等方式骗取邀请奖励，违者账户将被冻结
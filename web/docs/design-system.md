# woodgit-web 设计规范（Design System）

> 适用范围：公开页（Landing/ModelsGallery）、认证页（Login/Register/Forgot）、用户控制台（/console/*）、企业管理台（/console/enterprise/*）。
> 基线：Semi UI 组件 + CSS 变量（`src/styles/global.css`）+ 本规范。CI 门禁见 `e2e/`（i18n 对齐 / console 零错误 / axe serious+critical 零 / Lighthouse a11y≥0.9）。

## 1. 排版

| 层级 | 规格 | 用途 |
|---|---|---|
| Display | 56–64px / 600–700 / `letter-spacing:-0.02em` / 行高 1.15 | Landing Hero 大标题 |
| 页面标题 | 20px / 600 | 控制台页头（`wg-page-title`） |
| 卡片标题 | 15–16px / 600 | Semi Card title |
| 正文 | 14px / 400 / 行高 1.6 | 表单、表格 |
| 辅助 | 12–13px / 400 / `--wg-text-2` | 说明、hint |
| 数字 | `tabular-nums` 等宽 | 价格、额度、KPI |

字体栈：`--wg-font-display` / `--wg-font-body` / `--wg-font-mono`（global.css 定义）。CJK 走系统栈（PingFang/HarmonyOS/YaHei），不引 CJK 网络字体。

## 2. 颜色与边框

- 主色 `#0086ff`，hover `#1890ff` 级，浅底 `--wg-primary-light:#e8f3ff`
- 语义色：成功 `#22c55e` / 警告 `#f59e0b` / 错误 `#ef4444`（删除、危险操作）/ 信息 `#0086ff`
- 状态药丸：Enabled 绿 / Disabled 红 / 管理员蓝 / pending 橙
- **边框统一**：卡片/分隔 `1px solid var(--wg-border)`，`--wg-border: rgba(28,31,35,0.08)`；不靠阴影分层
- 背景：light `#f7f8fa` 页面 / 纯白卡片；dark `#1b1d21` 页面 / `#232529` 卡片

## 3. 按钮位置原则（硬性）

| 类型 | 对齐 | 颜色 |
|---|---|---|
| 条件控件：搜索框、下拉筛选、时间选择、查询/重置按钮 | **左对齐**（与筛选输入同组，从左排起） | 查询=主蓝 solid；重置=neutral |
| CRUD：创建/新建、导出、刷新 | **右对齐**（筛选卡右侧留白，或表格卡 header 右上 `headerExtraContent`） | 创建=主蓝 solid（醒目） |
| 危险操作：删除、吊销、停用 | 行内或卡内右侧 | **红色**，二次确认（Popconfirm/Modal） |
| 次要操作：编辑、转交、查看 | 行内文字链 | neutral/borderless |

- 表格卡结构（三段式）：①筛选卡（条件全左）②表格卡（工具条：左侧可放批量操作，右侧创建主按钮；表格；底部分页）③详情/编辑一律右侧 Drawer
- Drawer/Modal：600px 表单 / 560 详情 / 480 弹窗；`maskClosable={false}` 时必须 `closeOnEsc`；条件挂载防遮罩泄漏；底部 sticky 操作条

## 4. 公开营销页例外

Landing/ModelsGallery 无 CRUD：条件控件（搜索）居左优先，视图切换/类型筛选可居右；CTA（Get API Key）用主蓝实心药丸。

## 5. 图标

只用 `@douyinfe/semi-icons`（禁 emoji 图标，文案内 emoji 可）。统计图标圆底浅色 tint（`wg-tint-blue/green/amber/violet/primary`）。Provider 图标用 `src/assets/providers/*.svg`，未命中回退中性灰首字母。

### 3.1 浮层三组件分工（勿混用）

| 组件 | 用途 | 示例 |
|---|---|---|
| **Drawer** | 一切 CRUD 表单/详情 | 创建 Key、成员管理、分组编辑 |
| **Modal.confirm（居中，danger 红钮）** | 仅危险动作二次确认 | 删除/吊销/停用/删除分组——走 `dangerConfirm()` helper（src/components/dangerConfirm.tsx） |
| **Toast（右上角弹出）** | 轻量操作反馈 | 复制成功、保存成功、导出完成 |

禁：浏览器原生 confirm/alert/prompt；行内 Popconfirm 已废弃（危险确认统一居中弹窗）。

## 6. i18n

- 所有用户可见文案 `t('key')`，支持 `{var}` 插值；zh/en 键集合必须一致（`npm run check:i18n` 门禁）
- en 值不得含中文字符（门禁检查）

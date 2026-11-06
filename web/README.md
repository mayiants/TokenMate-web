# woodgit-web

WOODGIT 前端重建工程：**React 18 + Semi UI + Vite + TypeScript**，对齐 TokenRouter 的产品形态与视觉风格。替代 ai-portal-go 现有 Go 模板 + HTMX 前端（渐进接管全部页面）。

## 技术栈与硬性约定

- **组件库**：Semi UI（`@douyinfe/semi-ui` + `@douyinfe/semi-icons`）——TokenRouter 同源组件体系
- **构建**：Vite 5 + TypeScript（strict，`npm run build` 先 tsc 检查再打包）
- **路由**：react-router-dom v6
- **图表**：ECharts 5（本地化）
- **字体**：Inter Variable（@fontsource-variable/inter，OFL 许可，本地打包）
- **Provider 品牌图标**：`src/assets/providers/*.svg`（手绘，CC0/自有）
- **所有依赖必须本地化**：一律 npm 安装本地打包，**禁止任何 CDN / 外链资源**
- **i18n**：`src/i18n/locales/{zh,en}.json` 双份同步，用户可见文案必须走 `t()`

## 开发

```bash
npm install          # registry 已指向 npmmirror
npm run dev          # http://127.0.0.1:5173，/api 代理到本机 portal :3001
npm run build        # 产物在 dist/
```

后端 API 即 ai-portal-go 现有 `/api/*`（开发期经 Vite proxy；生产由 Gin 托管 dist + SPA fallback，集成属后续任务）。

## 目录

```
src/
├── api/        # fetch 封装 + /api/models/public 等接口类型
├── assets/     # provider SVG 图标（Vite 打包）
├── i18n/       # locale 上下文 + zh/en 字典
├── layouts/    # PublicLayout（顶部导航壳）
├── pages/      # Landing / ModelsGallery / NotFound（P1 骨架，持续填充）
└── styles/     # 全局样式、品牌渐变、走马灯动画
```

## 阶段

- **P1（当前）**：公开首页 + 模型广场，达到 TokenRouter 公开页体验
- P2：登录/注册 + 用户控制台（Dashboard / API Keys / 账单）
- P3：管理后台全部页面（最大块）
- P4：接入 ai-portal-go（Gin 托管 dist、路由回落、鉴权对接、语义化迁移）

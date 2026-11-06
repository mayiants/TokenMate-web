# woodgit-platform

WOODGIT 平台统一工程（2026-11-07 组建）：**portal + proxy + web + agent 单仓**。老的 `ai-portal-go` / `ai-proxy-go` / `ai-gateway-go` / `adaii/woodgit-web` 自此冻结（只读归档，不再演进）。

## 结构

```
portal/   Go 控制面（gin + postgres + redis；含 webui/ = 嵌入的 React SPA）
proxy/    Go 数据面（/v1 路由、上游池、计费回传）
web/      React 18 + Semi UI 前端（Vite；独立 npm 工程）
agent/    PC Agent 本体（Go 单二进制，独立 module，构建产物分发用）
deploy/
  publish-web.sh   构建 SPA → rsync 到 portal/webui/dist（portal 重编译即嵌入）
docs/     （设计规范等在 web/docs/）
```

## 常用工作流

```bash
# 1) 发布前端（改了 web/ 之后必须跑，否则 portal 里仍是旧 SPA）
./deploy/publish-web.sh

# 2) 构建/运行控制面（沿用 portal 的 start.sh；compose 模板在 portal/ 下）
cd portal && ./start.sh rebuild        # 自动快照 DB

# 3) 质量门禁（在 web/ 下；需要 dev/preview 服务时可参照 web/README）
cd web && npm run check:ui             # i18n + console + axe + responsive

# 4) Agent 交叉编译（在 agent/ 下）
cd agent && GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o woodgit-agent.exe ./cmd/agent
```

## 关键设计

- **SPA 嵌入**：`portal/webui` 用 `go:embed all:dist` 把前端打包进二进制；路由默认 SPA 接管全部非 `/api` 路径（NoRoute 回落 index.html）。`LEGACY_WEB_UI=true` 可回退旧 SSR 页面（代码保留在 `internal/web`，未删除）。
- **迁移**：portal 迁移已排到 0039（agent_enrollment/adapters 在 0038/0039）；加载器对版本号重名硬失败，新增迁移顺延。
- **CORS**：`ALLOWED_ORIGINS` 经 start.sh 透传；同源自部署（nginx 443 → portal 单点）后此问题自然消失。
- **旧库冻结**：历史与热修复参照仍在 `/opt/workspace/ai-portal-go`、`/opt/workspace/ai-proxy-go`（原样未动）。

## 与上游同步（portal/proxy 源自 gh 最新）

单仓后不再以子仓库跟踪上游。需要同步时：

```bash
git clone --depth 50 https://github.com/mayiants/ai-portal-go /tmp/up-portal
diff -r --exclude=.git /tmp/up-portal portal   # 评估差异后手工合入
```

注意上游 2026-11-07 已含 retention/SSO/MFA 等工作（迁移至 0037）。

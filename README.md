# TokenMate-web

TokenMate 的 Web UI（React SPA）源码仓库。平台本体（portal/proxy/agent）
已迁至 [TokenMate](https://github.com/mayiants/TokenMate)；本仓库只维护前端。

- `web/` — React SPA 源码（构建产物经 `deploy/publish-web.sh` 发布，由 portal 内嵌托管）
- `deploy/` — 发布脚本

## 构建

```bash
cd web && npm ci && npm run build   # 产物在 web/dist
```

# 网络接入与页面加载优化、边缘加速与保护方案

**Status: ACTIVE（2026-10-31 更新：CF 已配置并验证；安全组已审计，整改项见 §6）**
**Date: 2026-10-31（周六低负荷期实施）**
**适用：api.woodgit.com（Portal Web + Proxy API 共用域名，Cloudflare 橙云）**

## 1. 背景与现状测量

用户分布以中国为主，源站在 AWS SG（新加坡）。域名 api.woodgit.com 走 Cloudflare 橙云（代理）。周六低负荷时段用户仍感觉页面慢，实测诊断结论：**慢不在服务器（portal 本地 TTFB 1-4ms），慢在网络路径与资源加载策略**。

关键测量数据（2026-10-31）：

| 指标 | 测量值 | 评价 |
|---|---|---|
| portal 本地 TTFB（/login） | 1-4ms | 极快，应用无瓶颈 |
| 经 CF 到 origin TTFB（同机房 hairpin） | 86-274ms | CF 处理开销 |
| /login HTML | 14.6KB | 小 |
| 全页 JS/CSS（gzip 后） | ~161KB | 中 |
| cf-cache-status（/static/*） | **BYPASS** | 🔴 橙云但零边缘缓存 |
| cf-cache-status（HTML） | DYNAMIC | 正常 |
| 静态资源浏览器缓存 | immutable 1年 + ?v=N | ✅ 已有 |
| SSL session cache | shared:SSL 20m / 1d / tickets on | ✅ 已有 |
| gzip | level 5，css/js/json/svg | ✅ 已有 |
| 外部动态字体 | 无（Inter 本地 @font-face） | ✅ |
| 图片总量 | 12KB | ✅ |

核心矛盾：**橙云但没有配任何 CF 缓存规则**，所有资源（含静态）全部回源；CF 免费版对中国流量常绕美国西岸边缘 → SG 源站，单资源 +300-600ms。

## 2. 已实施的本地优化（2026-10-31）

### 2.1 nginx（/etc/nginx/sites-enabled/api.woodgit.com）

1. **upstream keepalive**：portal_backend（127.0.0.1:3001, keepalive 32）、proxy_backend（172.31.43.214:8080, keepalive 16），全部 location 加 `proxy_http_version 1.1` + `Connection ""`。消除每请求新建 TCP。
2. **`location /i18n/` 缓存**：`Cache-Control: public, max-age=3600`。背景：app-layout.js 每次页面加载同步 XHR 拉 172KB en.json（阻塞渲染），加浏览器缓存后重复访问零成本。代价：i18n 更新最长 1h 生效，可接受（i18n 随版本发布）。
3. **清理 sites-enabled 中的 3 份 .bak 备份配置**：nginx 会加载 sites-enabled 下所有文件，旧配置与正式配置 server_name 冲突（启动告警 + 顺序敏感的配置抢占风险），已移至 sites-available。

### 2.2 页面渲染阻塞（layout.html）

6 个同步脚本改 `defer`：chart.min.js（208KB）、sortable、table-pagination、flatpickr×2、app-common。head 同步 JS 从 ~430KB 降到 ~75KB（raw）。

改前安全验证（重要，防止 JS 时序破坏）：
- 9 个使用 Chart 的模板全部在命名渲染函数或 DOMContentLoaded 内调用 `new Chart`（deferred 脚本在 DOMContentLoaded 前执行，时序安全）
- flatpickr / Sortable 调用点均有 `typeof x === 'undefined'` 守卫
- app-layout.js 保持同步（加载 i18n 全局，耦合面大，不动）

已验证：模板冒烟测试通过、SSE 流式正常、容器 healthy、线上 HTML defer 属性确认。

## 3. Cloudflare 配置建议（按优先级）

### P0 — Cache Rule（免费，收益最大）✅ 已配置并验证

| 设置 | 值 |
|---|---|
| 匹配 | `api.woodgit.com/static/*` |
| Cache eligibility | **Cache Everything** |
| Edge TTL | 1 month |
| Browser TTL | Respect origin（已有 immutable 1年 + ?v=N 版本控制） |

**验证结果（2026-10-31）**：带版本参数 URL `cf-cache-status: HIT` ✅（如 app-common.js?v=15、app.css?v=56）。
已知无害现象：裸 URL（不带 ?v= 参数）偶发 BYPASS，是 CF 对缓存资格判定结果的短暂缓存所致，页面全用带版本 URL，不受影响，无需处理。

**注意**：
- 只配 `/static/*`，**绝不配 `/v1/`**（API 响应有 Set-Cookie，CF 默认 DYNAMIC 不会缓存，安全；Cache Everything 若误配会缓存私密数据，重大事故）
- `/i18n/` 可加第二条规则 Edge TTL 1h（origin 已给 max-age=3600，不配也行，CF 尊重 origin cache-control 的前提是 Edge TTL 设置或 Cache Everything）
- origin 对 /static/ 响应带 csrf_token Set-Cookie（Gin 中间件全局设置），CF 缓存后该 header 不再下发，反而更干净；如想彻底干净可在 portal 侧对 /static 跳过 CSRF 中间件（低优先）

### P1 — 免费开关 ✅ 已配置（用户操作，2026-10-31）

Tiered Cache / Early Hints / Brotli / HTTP/3 均已在 CF 面板开启（Brotli 为 CF 默认启用）。Argo Smart Routing 未付费，暂缓——若 P0/P1 生效后仍觉慢再考虑（$5/月 + $0.10/GB）。

### P2 — Argo Smart Routing（$5/月 + $0.10/GB）

CF 边缘 → 源站的智能选路，实时避开拥塞链路。对"中国用户 → CF 边缘 → SG 源站"这段质量不稳的路径改善最直接。当前流量规模月成本约 $5-10。**如果 P0/P1 配完后仍觉得慢，这是下一步**。

### 安全组审计结果（2026-10-31，launch-wizard-1，已实测）

**发现漏洞**：443 对 0.0.0.0/0 开放，从 proxy EC2（非 CF 来源）用 origin 公网 IP + Host 头直连返回 200——**橙云保护可被完全绕过**。3001 外部已拒绝（000 ✅）。

逐条评估：

| 规则 | 现状 | 评估 | 处置 |
|---|---|---|---|
| 443 ← 0.0.0.0/0 | 🔴 全网开放 | 绕过面：直连 origin 免 CF 防护 | **替换为 CF IP 段（下表）** |
| 80 ← 0.0.0.0/0 | 🟠 全网开放 | 仅做跳转，同为绕过面 | **替换为 CF IP 段** |
| 22 ← 0.0.0.0/0 | 🟠 SSH 全网开放 | 密钥登录暂安全，但全网扫描爆破面广 | 建议限制常用 IP/32 |
| 3001 ← 172.31.43.214/32 | ✅ | proxy → portal 内部直连 | 保持 |
| 8080 ← 172.31.45.213/32 | ⚪ 遗留 | 旧主机（非当前 proxy），无用 | 建议删除 |

**443/80 源替换（CF 官方 IP 段 v4，控制台每行一条粘贴；入站 30 条 < 60 上限）**：

```
173.245.48.0/20
103.21.244.0/22
103.22.200.0/22
103.31.4.0/22
141.101.64.0/18
108.162.192.0/18
190.93.240.0/20
188.114.96.0/20
197.234.240.0/22
198.41.128.0/17
162.158.0.0/15
104.16.0.0/13
104.24.0.0/14
172.64.0.0/13
131.0.72.0/22
```

来源：https://www.cloudflare.com/ips-v4 （会变更，建议每季度复核）。本机无公网 IPv6，无需配 v6 段。证书为 Cloudflare Origin CA（2041 到期），无 ACME 入站校验需求，80 收严无影响。

改完验证闭环：`api.woodgit.com` 正常 200；从 proxy EC2 直连 origin 公网 IP 应超时/拒绝；proxy→portal:3001 内部直连不受影响。

### 不建议

- **Rocket Loader**：会改写 JS 加载时序，破坏 Alpine.js/HTMX 初始化。
- **Auto Minify**：已废弃，且资源本身已压缩。
- **Polish/Mirage**：图片总共 12KB，无收益。
- **橙云改灰云**：已实测灰云更慢（失去边缘优化），且仅规避 >100s 限制；保持橙云。
- **AOP/China Network**：Enterprise 专属，费用高，当前阶段不需要。

## 4. 已知的橙云约束与应对

**/v1/ 长请求 100s 限制**：CF 所有付费档位对单请求 origin 响应有 100s 上限（Enterprise 可提到 600s）。影响：图片/视频生成、超长流式。当前应对：
- chat SSE 流持续有字节流动，不受 100s 限制
- 单次生成类请求（image/video）若上游 >100s 会被 CF 掐 504。若此问题重现，方案：**单独开一个灰云域名**（如 `direct.woodgit.com`）专供 API 客户端（WorkBuddy 等）使用，web 页面保持橙云享受边缘加速。一套 nginx server 块 + 灰云 DNS 即可，成本为零。
- 不建议为此上 Enterprise。

## 5. 后续可选优化（暂不做，记录备查）

1. **chart.min.js 按页面加载**：现 deferred 全站加载，208KB。可在 layout 加 `{{block "charts"}}`，9 个图表页覆写引入。省 ~200KB/非图表页。风险低但需逐个模板改动，收益一般（deferred 已不阻塞）。
2. **app-layout.js 的同步 XHR 改 async + ready 队列**：彻底消除 head 最后一个大阻塞。需保证 i18n 就绪前 t() 调用排队，改动面中等。
3. **Inter 字体子集化**：app.css 内嵌全套 Inter woff2，按常用字符子集化可省几十 KB。当前字体本地加载已不慢，收益小。
4. **静态资源迁移 R2 + Workers**：CF 边缘缓存已能解决，无需引入 R2 复杂度。
5. **portal 侧 /static 跳过 CSRF cookie**：配合 §3 P0 注记。
6. **源站升配 / 多源站**：当前负载极低，无必要。

## 6. 待办清单

| # | 事项 | 优先级 | 状态 |
|---|---|---|---|
| 1 | nginx keepalive + /i18n/ 缓存 + .bak 清理 | P0 | ✅ 2026-10-31 完成 |
| 2 | layout.html 六脚本 defer + portal 重编译 | P0 | ✅ 2026-10-31 完成并验证 |
| 3 | CF Cache Rule `/static/*` Cache Everything | P0 | ✅ 已配置，HIT 验证通过 |
| 4 | CF Tiered Cache / Early Hints / Brotli / HTTP3 | P1 | ✅ 已配置 |
| 5 | **AWS SG：443/80 源 0.0.0.0/0 → CF IP 段（§3 表格）** | **P0（安全）** | ⬜ **待用户控制台操作**（绕过漏洞已实测确认） |
| 6 | AWS SG：22 限制常用 IP；删除遗留 8080 规则 | P2 | ⬜ 待用户操作 |
| 7 | WAF 自定义规则 + Rate Limiting + Bot Fight | P2 | ⬜ 待用户（CF 面板） |
| 8 | Argo Smart Routing（若仍慢） | P2 | ⬜ 暂缓（未付费） |
| 9 | image/video 客户端专属灰云域名（若 >100s 问题重现） | 按需 | ⬜ 预案就绪，我执行 |

## 7. 验证命令速查

```bash
# 静态缓存是否命中（配完 Cache Rule 后）
curl -sI https://api.woodgit.com/static/css/app.css | grep cf-cache-status   # 期望 HIT

# HTML 不缓存（应保持 DYNAMIC）
curl -sI https://api.woodgit.com/login | grep cf-cache-status

# i18n 缓存头
curl -sI https://api.woodgit.com/i18n/en.json | grep cache-control           # 期望 max-age=3600

# 首字节与总耗时
curl -s -o /dev/null -w "ttfb=%{time_starttransfer}s total=%{time_total}s\n" https://api.woodgit.com/login

# nginx 配置正确性
sudo nginx -t && sudo systemctl reload nginx

# origin 是否仅放行 CF（从一台非 CF 的机器执行，期望超时/拒绝）
curl -m 5 https://<ORIGIN_IP>/login -k -H "Host: api.woodgit.com"
```

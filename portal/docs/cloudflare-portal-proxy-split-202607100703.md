# Cloudflare 分流：Portal / Proxy 同 Region 拆 EC2

> 生成时间：2026-07-10 07:03 UTC | 更新时间：2026-07-11 14:15 UTC
> 适用：Portal / Proxy 已在新加坡拆成两台 EC2 并跑通；当前 `api.woodgit.com` 通过 **Portal EC2** 做 TLS 网关，Cloudflare path 分流作为可选的更干净拓扑。
> 关联文档：`singapore-migration-stage3-split-proxy-202607110942.md`、`deployment-guide-202605300640.md`、`aws-deployment-guide-202605250930.md`、`aurora-postgresql-migration-guide.md`。

---

## 1. 目标与约束

**目标**：Portal 与 Proxy 从同机 Docker 拆到两台独立 EC2，但对最终用户仍是**同一个域名、同一套前缀**，调用方无感。

**硬约束（来自需求讨论）**：

1. DNS 由 **Cloudflare** 处理。
2. 两台 EC2 **同 region / 同 VPC**，对外同一 endpoint；**暂不跨区域**（portal 新加坡 + proxy 悉尼这类留作后续演进）。
3. `https://api.woodgit.com/v1` 可改，但要让用户不别扭：**web 与 API 保持同一前缀**；不考虑换 TLD（如 `api.woodgit.ai`）。

**当前实际部署**：

| 主机 | 角色 | 内网 IP | 公网 EIP | 安全组 |
|---|---|---|---|---|
| Portal EC2 | `ai-portal-go` + PG + Redis | `172.31.45.213` | `47.130.132.254` | 与 Proxy 共用同一 SG |
| Proxy EC2 | `ai-proxy-go` + proxy Redis | `172.31.43.214` | `54.169.55.55` | 与 Portal 共用同一 SG |

因为两台机器在同一安全组，AWS 默认的自引用规则已经允许它们在内网互相访问 3001/8080，无需额外 SG 规则。

**两种可行拓扑**：

1. **当前快速跑通方案**：`api.woodgit.com` → Portal EIP，Portal Nginx 做网关，`/v1/` 转发到 Proxy 内网 IP，其余本地处理。
2. **Cloudflare path 分流方案**：Cloudflare Origin Rules 按 path 分流，默认 `/*` → Portal origin，仅 `/v1/*` → Proxy origin。

> **Cloudflare Free tier 约束**：Origin Rules 在 Free 计划下**只能覆盖目标端口**，**不能覆盖源站 hostname/DNS 记录**（见 [Cloudflare 文档](https://developers.cloudflare.com/rules/origin-rules/)）。因此 Free 用户无法在 Cloudflare 边缘按 path 把请求发到不同源站主机。要在 Free tier 实现 path 分流，只能用 Cloudflare Workers（每日 10 万请求限制），或保持「单台 EC2 做 Nginx 网关」的当前方案。

---

## 2. 核心认知：DNS 不能按 path 分流

DNS 只把域名映射到 IP/CNAME，看不到 `/v1`。当前单机能 `/` → Portal、 `/v1/` → Proxy，是**本机 Nginx 在 443 入口按 `location` 转发**实现的。拆机后这一层从「本机 Nginx」上移到「Cloudflare 边缘」，转发目标从本机端口变成两台 EC2 的源站地址。

---

## 3. 拓扑

### 3.1 推荐：Cloudflare 按 path 分流

```
                         Cloudflare (api.woodgit.com, 橙云代理)
                         DNS + 按 path 选源站 (Origin Rules / Worker)
                ┌─────────────────────────┴─────────────────────────┐
                │ 默认 /*                  │ /v1/*                     │
                ▼                          ▼                          │
   portal-origin.woodgit.com      proxy-origin.woodgit.com            │
   (灰云 / DNS only)               (灰云 / DNS only)                   │
   47.130.132.254                 54.169.55.55                        │
                │                          │                          │
        ┌───────▼────────┐         ┌───────▼────────┐       同 region / 同 VPC
        │  EC2: Portal   │         │  EC2: Proxy    │
        │  Nginx → :3001  │◄────────│  Nginx → :8080  │
        │  PG(本地/Aurora)│  内网同步 │  内存模式 Redis │
        └────────────────┘  key pool └────────────────┘
        SG: Cloudflare IP→443       SG: Cloudflare IP→443
```

### 3.2 当前：Portal EC2 做 TLS 网关

```
                         Cloudflare (api.woodgit.com, 橙云代理)
                                   │
                                   ▼
                        portal-origin.woodgit.com
                        47.130.132.254
                        ┌──────────────────┐
                        │   EC2: Portal    │  Nginx 443
                        │   /   → :3001    │  /v1/ → 172.31.43.214:8080
                        └──────────────────┘
                                   │
              ┌────────────────────┘
              ▼
   172.31.43.214:8080  EC2: Proxy (ai-proxy-go + Redis)
```

- Portal 与 Proxy 之间走 **VPC 内网**同步（key pool / 心跳 / key 验证），不绕 Cloudflare。
- DB 留在 portal EC2 本地，或按 `aurora-postgresql-migration-guide.md` 外部化到同 region Aurora——与本文方案**正交、互不影响**。

---

## 4. 网关放在哪台 EC2？

既然 Cloudflare Free tier 无法在边缘按 path 分流，就必须由其中一台 EC2 的 Nginx 做网关。当前是 **Portal 做网关**。

| 方案 | 当前 | 替代 |
|---|---|---|
| 网关位置 | Portal EC2 (`47.130.132.254`) | Proxy EC2 (`54.169.55.55`) |
| Cloudflare `api.woodgit.com` A 记录 | Portal EIP | Proxy EIP |
| Nginx 分流逻辑 | `/v1/` 转发 Proxy，其余本地 | `/v1/` 本地，其余转发 Portal |
| 公网入口机的规格 | t3.medium (4 GB) | t3.small (2 GB) |

**当前选择 Portal 做网关的理由**：

1. **Portal 配置更高**（t3.medium vs t3.small），TLS 终止、并发连接、静态资源服务的余量更大。
2. Proxy 作为纯 API 处理机，负载更单一，2 GB 足够跑 `ai-proxy-go` + Redis。
3. 网关会收到所有公网请求（web + API 流量），放在高配机器更合理。
4. 无论网关放哪边，它都是单点；既然单点不可避免，让性能更好的机器承担更合适。

切换成本：只是改 Cloudflare A 记录 + 两台 Nginx 配置对调，5 分钟可完成。

如果你同意，我可以现在把 `api.woodgit.com` 指向 Portal EIP，并调整 Nginx 配置。

---

## 5. Cloudflare 配置

### 5.1 回源子域（仅 Cloudflare 回源用，不对外公布）

| DNS 记录 | 类型 | 目标 | 代理状态 | 是否必须 |
|---|---|---|---|---|
| `api.woodgit.com` | A/CNAME | Cloudflare | **橙云（代理）** | ✅ 必须 |
| `portal-origin.woodgit.com` | A | `47.130.132.254` (Portal EIP) | 灰云（DNS only） | 仅 path 分流时需要 |
| `proxy-origin.woodgit.com`  | A | `54.169.55.55` (Proxy EIP)  | 灰云（DNS only） | 仅 path 分流时需要 |

> 这两个 `-origin` 子域只给 Cloudflare 回源用，用户永远访问 `api.woodgit.com`。
>
> **如果采用 Nginx 网关方案**（当前实际方案），`api.woodgit.com` 直接指向网关 EC2 的 EIP 即可，**不需要创建** `portal-origin` 和 `proxy-origin` 子域。Nginx 的分流不依赖 DNS 子域，而是靠 `location` 匹配 path。
>
> **只有切换到 Cloudflare path 分流时**，才需要这两个灰云子域作为 Origin Rule 的目标。
>
> 当前 `api.woodgit.com` 默认源站指向 Portal EIP；切换到 path 分流后，默认源站应改为 `portal-origin.woodgit.com`。

### 5.2 按 path 选源站

**首选 Origin Rules**（Rules → Origin Rules，无代码、不计 Worker 请求量）：

- Rule 1：`http.request.uri.path matches "^/v1/"` → **Override origin hostname** = `proxy-origin.woodgit.com`
- 默认（不匹配）→ `portal-origin.woodgit.com`

**plan 没有 Origin Rules 时用 Worker 兜底**：

```js
export default {
  async fetch(req) {
    const u = new URL(req.url);
    u.hostname = u.pathname.startsWith('/v1/')
      ? 'proxy-origin.woodgit.com'
      : 'portal-origin.woodgit.com';
    return fetch(new Request(u, req));
  }
};
```

> Worker 有请求数上限（Free 10 万/天起），API 大流量注意计费；Origin Rules 无此限制。

### 5.3 SSL / TLS

- 加密模式：**Full (strict)**。
- 源站证书：两台 EC2 装 **Cloudflare Origin CA 证书**（15 年，仅 Cloudflare 回源信任）或 Let's Encrypt。

### 5.4 缓存规则

- `/v1/*`：**Bypass cache**（API 动态请求，绝不缓存；默认即不缓存，确认无 Cache Everything 误伤）。
- 静态资源（web 的 JS/CSS）：可缓存，加速 web 加载。

### 5.5 源站安全组

当前两台 EC2 已合并到**同一个安全组**，因此内网 3001/8080 互通由 AWS 默认自引用规则自动允许，无需额外规则。

公网入站只需：

| 端口 | 来源 | 说明 |
|---|---|---|
| 22 | 你的 IP / `0.0.0.0/0` | SSH |
| 80, 443 | Cloudflare 官方 IP 段 | Cloudflare 回源 |

> 建议把 443 来源锁到 [Cloudflare IP 段](https://www.cloudflare.com/ips/)，防止绕过 Cloudflare 直接打源站。

如果你以后把两台机器拆回不同 SG，再按旧方案添加 Proxy → Portal 3001、Portal → Proxy 8080 的规则。

---

## 6. 两台 EC2 配置

### 6.1 方案 A：Portal EC2 做网关（当前，Cloudflare Free tier 友好）

Cloudflare 上只配一条 A 记录：`api.woodgit.com` → `47.130.132.254`。

**Portal EC2**

```bash
cd /opt/ai-platform/ai-portal-go
./start.sh portal --app-base-url=https://api.woodgit.com
```

Portal 侧 Nginx（网关模式）：

```nginx
server {
    listen 443 ssl http2;
    server_name api.woodgit.com;

    ssl_certificate     /etc/ssl/certs/woodgit.com.pem;
    ssl_certificate_key /etc/ssl/private/woodgit.com.key;

    # API 转给 Proxy
    location /v1/ {
        proxy_pass http://172.31.43.214:8080/;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 120s;
        proxy_buffering off;
    }

    # 其他全部本地处理
    location / {
        proxy_pass http://127.0.0.1:3001;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

**Proxy EC2**

```bash
cd /opt/ai-platform/ai-proxy-go
docker compose -f docker-compose.proxy.yml up -d --build
```

Proxy 侧 Nginx（只处理 `/v1/`，不处理 `/`）：

```nginx
server {
    listen 443 ssl http2;
    server_name api.woodgit.com;

    ssl_certificate     /etc/ssl/certs/woodgit.com.pem;
    ssl_certificate_key /etc/ssl/private/woodgit.com.key;

    location /v1/ {
        proxy_pass http://127.0.0.1:8080/;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 120s;
        proxy_buffering off;
    }
}
```

### 6.2 方案 B：Proxy EC2 做网关（替代）

Cloudflare 上只配一条 A 记录：`api.woodgit.com` → `54.169.55.55`。

**Portal EC2**

```bash
cd /opt/ai-platform/ai-portal-go
./start.sh portal --app-base-url=https://api.woodgit.com
```

Portal 侧 Nginx（只处理 web，不处理 `/v1/`）：

```nginx
server {
    listen 443 ssl http2;
    server_name api.woodgit.com;

    ssl_certificate     /etc/ssl/certs/woodgit.com.pem;
    ssl_certificate_key /etc/ssl/private/woodgit.com.key;

    location / {
        proxy_pass http://127.0.0.1:3001;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

**Proxy EC2**

```bash
cd /opt/ai-platform/ai-proxy-go
docker compose -f docker-compose.proxy.yml up -d --build
```

Proxy 侧 Nginx（网关模式）：

```nginx
server {
    listen 443 ssl http2;
    server_name api.woodgit.com;

    ssl_certificate     /etc/ssl/certs/woodgit.com.pem;
    ssl_certificate_key /etc/ssl/private/woodgit.com.key;

    # API — 注意 proxy_pass 末尾斜杠，Nginx 会去掉 /v1/ 前缀
    location /v1/ {
        proxy_pass http://127.0.0.1:8080/;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 120s;
        proxy_buffering off;
    }

    # 其他全部转给 Portal
    location / {
        proxy_pass http://172.31.45.213:3001;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

### 6.3 公共配置

无论哪种方案，`.env.go-platform` 中的关键项相同：

**Portal：**

```bash
SERVER_ID=portal-apse1-01
SERVER_REGION=ap-southeast-1
APP_BASE_URL=https://api.woodgit.com
ALLOWED_ORIGINS=https://api.woodgit.com
PROXY_CALLBACK_URLS=http://172.31.43.214:8080
```

**Proxy：**

```bash
SERVER_ID=proxy-apse1-01
SERVER_REGION=ap-southeast-1
PORTAL_URL=http://172.31.45.213:3001

# 4 个密钥必须与 Portal 完全一致
JWT_SECRET=<same-as-portal>
ENCRYPTION_KEY=<same-as-portal>
PROXY_SYNC_SECRET=<same-as-portal>
ADMIN_SECRET=<same-as-portal>
```

> 健康检查：`curl http://172.31.43.214:8080/health` 应返回 `healthy:true`；Portal admin 的 proxy 列表应能看到 `proxy-apse1-01` 心跳。

---


## 7. 延迟与吞吐评估（重点）

担心"所有请求都过 Cloudflare 多一跳、吞吐成瓶颈"是合理的，但分层看影响有限：

### 7.1 延迟

橙云代理把链路拆成两段：**用户 → Cloudflare 边缘** + **边缘 → 源站**。

- **用户→边缘**：Cloudflare 边缘遍布全球（含悉尼/新加坡），用户就近接入，这一段通常 **比直连源站更快**（同城/同区域个位数到几十 ms）。
- **边缘→源站**：替代了原来的"用户→源站"直连。源站在新加坡、悉尼用户原直连约 ~100ms；经 Cloudflare 后走其**骨干网**回源，通常不劣于公网，常略优。两段合计与原直连**同量级**，不必然变差。
- `/v1` 是动态请求、必须回源，延迟主要由「边缘↔源站」决定。可选 **Argo Smart Routing**（付费）用智能路由再压一截回源延迟。
- **web 静态资源**被边缘缓存，管理后台加载反而更快。

**结论**：Cloudflare 中转不必然显著增延迟；用户到边缘这段更快，边缘到源站与直连相当。上线后用 **Cloudflare Analytics** 看两个指标验证：`edgeResponseTime` / `originResponseTime`，以实测为准，不要凭感觉。

### 7.2 吞吐

- Cloudflare 边缘带宽是 Tbps 级，**不是瓶颈**；瓶颈在**源站 EC2 的带宽与并发**。
- Cloudflare 对源站做**连接复用 / Keep-Alive**，多用户请求复用回源连接，源站连接数压力通常**比直连更小**。
- **计费**：Cloudflare Free/Pro/Business **不按带宽计费**，带宽不限量；额外费用只在 Argo（按流量）或 Worker（按请求数）产生。吞吐本身不加钱。
- 需注意的单请求限制（Free/Pro）：请求体上限（Pro 100MB / Business 500MB）、空闲超时 ~100s。`/v1` 是 LLM 流式、请求/响应体量小且持续出 token，**一般不撞上限**。

**结论**：吞吐不是 Cloudflare 的瓶颈，瓶颈在源站；Cloudflare 还附带 DDoS/WAF/连接复用收益。

### 7.3 真正的代价（需要知情权，但不是性能）

- **可用性依赖**：核心业务流量全量过 Cloudflare，Cloudflare 自身故障会影响服务（概率低，但存在）。可在 Cloudflare 配 Health Check + 必要时灰云降级直连源站应急。
- **数据过第三方**：LLM 请求/响应内容经过 Cloudflare。有合规/隐私要求时，评估 Cloudflare 的数据处理条款，或对 `/v1` 走"仅 DNS（灰云）直连源站"——但这会牺牲统一入口与 DDoS 防护，需权衡。
- **首字节/空闲超时**：若存在「长 reasoning 几十秒不出首 token」的模型，留意边缘空闲超时；把首 token 推前或上 Enterprise 调超时。

### 7.4 何时才考虑「API 直连源站绕过 Cloudflare」

保留统一入口为先，**按实测数据决定**，触发条件：

- Cloudflare Analytics 显示 `originResponseTime` 本身正常，但 `edgeResponseTime` 显著高于直连基线，且 Argo 也无法压回；
- 出现明确的 Cloudflare 限制（请求体/超时/并发）影响业务；
- 合规要求 `/v1` 流量不得经过第三方。

届时再给 API 一个**灰云直连入口**（如 `api-direct.woodgit.com` 或直连 ALB），仅给有需求的大客户/内部使用，web 与默认 API 仍走 `api.woodgit.com`。这是「先统一、按数据再分流」的工程路径，不要预先优化。

---

## 8. 切换与回滚

**当前已跑通的状态**：`api.woodgit.com` → Proxy EIP，Proxy Nginx 网关分流。

### 8.1 切换到 Proxy 做网关（替代，无需 Cloudflare path 分流）

1. Cloudflare 上把 `api.woodgit.com` 的 A 记录从 `47.130.132.254` 改为 `54.169.55.55`。
2. 在 Portal EC2 上把 Nginx 改成 §6.2 的「Portal 非网关模式」（只保留 `location /`，删除 `location /v1/` 转发）。
3. 在 Proxy EC2 上把 Nginx 改成 §6.2 的「Proxy 网关模式」（增加 `location /` 兜底转发到 Portal，并保留 `location /v1/` 本地处理）。
4. 测试：
   ```bash
   curl -sf https://api.woodgit.com/api/health   # Portal
   curl -sf https://api.woodgit.com/v1/health    # Proxy
   curl -sf https://api.woodgit.com/login        # Portal
   ```
5. 观察 24h。

### 8.2 切换到 Cloudflare path 分流（需要 Enterprise Origin Rules 或 Workers）

1. 创建灰云子域 `portal-origin.woodgit.com` → `47.130.132.254`，`proxy-origin.woodgit.com` → `54.169.55.55`。
2. 在 Cloudflare 创建 Origin Rule（仅 Enterprise 支持 hostname 覆盖；Free/Pro/Business 只能用 Worker 替代）：
   - 默认（不匹配 `/v1/`）→ `portal-origin.woodgit.com`
   - `/v1/*` → `proxy-origin.woodgit.com`
3. 测试与 §8.1 相同。
4. 根据网关位置删除对应的兜底 `location /` 块。

### 8.3 回滚

- **从 Portal 网关回滚到 Proxy 网关**：把 Cloudflare A 记录改回 Proxy EIP，Nginx 配置改回 §6.2。
- **从 Proxy 网关回滚到 Portal 网关**：把 Cloudflare A 记录改回 Portal EIP，Nginx 配置改回 §6.1。
- **从 Cloudflare path 分流回滚**：删除/停用 Origin Rule/Worker，流量回到 `api.woodgit.com` 当前配置的源站。
- 注意 DNS TTL：切换前把相关记录 TTL 临时调到 60–120s。

---

## 9. 验证清单

- [x] DNS：`api.woodgit.com` 橙云，指向当前网关 EIP
- [ ] 若使用 Cloudflare path 分流：`portal-origin.woodgit.com` → `47.130.132.254`，`proxy-origin.woodgit.com` → `54.169.55.55`
- [x] 经 Cloudflare：`curl https://api.woodgit.com/api/health` 200
- [x] 经 Cloudflare：`curl https://api.woodgit.com/v1/health` 200
- [x] 经 Cloudflare：`curl https://api.woodgit.com/login` 200
- [ ] Portal admin 的 proxy 列表出现 `proxy-apse1-01` 且心跳新鲜
- [ ] 创建测试 API key，经 `https://api.woodgit.com/v1/chat/completions` 实际调通一个模型
- [ ] Cloudflare Analytics：观察 `edgeResponseTime` vs `originResponseTime`、错误率、缓存命中率
- [ ] 安全组：从公网直接 `curl https://<非网关-EC2-IP>` 应被拒绝（仅 Cloudflare 能进）
- [ ] 4 个全局密钥两机一致；`ENCRYPTION_KEY` 未变更（否则存量 upstream key 解不开）

> 当前 Cloudflare 默认源站指向 Portal EIP，因此 `api.woodgit.com` 的 web 请求由 Portal Nginx 直接本地处理，只有 `/v1/*` 会转发到 Proxy。切换到 path 分流后，web 请求会直接回源到 Portal。

---

## 10. 后续演进（暂不实施）

- **跨区域 proxy-only**：当某区域用户需要更低延迟，按 `deployment-guide-202605300640.md` Stage 3 在该区域加 proxy-only 节点；Cloudflare 加一条按地域/路径的路由到新区 `proxy-origin`，或用 Cloudflare 地理负载均衡。Portal 始终单点写，避免分布式写。
- **API 直连入口**：见 §7.4，仅在实测触发条件成立时启用。
- **多 proxy 同 region 扩缩容**：proxy EC2 前加 ALB/ASG，`proxy-origin` 指 ALB；Portal 通过 `SERVER_ID` 区分实例。

---

## 11. 参考

- Cloudflare Origin Rules / Worker / Load Balancing 文档
- Cloudflare IP 段：`https://www.cloudflare.com/ips/`
- 项目内：`deployment-guide-202605300640.md`、`aws-deployment-guide-202605250930.md`、`aurora-postgresql-migration-guide.md`

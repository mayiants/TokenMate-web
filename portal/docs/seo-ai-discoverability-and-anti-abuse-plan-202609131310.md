# Portal SEO / AI-Discoverability + Anti-Abuse 方案

> **Status:** DRAFT (方案确认版，实现前基线 tag 见 §7)
> 生成时间：2026-09-13 13:10 UTC
> 作者：Kimi Code
> 范围：`ai-portal-go` 公开页面的可发现性（SEO / AI 爬虫 / 广告系统分类）+ 未认证流量的反滥用加固
> 参考：`workspace/ref-web/{woodgitweb,aynbayweb}`（静态营销站的 robots.txt / sitemap.xml / llms.txt / head meta / JSON-LD 模式）

## 1. 目标与非目标

**目标**
1. 让 portal 的**公开页面**容易被搜索引擎、AI 爬虫（GPTBot / ClaudeBot / PerplexityBot / Google-Extended 等）与广告/分类系统**发现并正确分类**（这是一个 AI API 网关平台）。
2. 同时**加固反滥用**：对未认证流量做基本限流与 bad-bot 拦截，避免爬虫无节制消耗资源。

**非目标 / 边界**
- portal 是**登录后使用的控制台**（Gin 服务端渲染 + `go:embed` 模板），不是营销站。**只有公开页做 SEO**；所有认证页面必须保持 `noindex` + 鉴权。
- 应用层限流是**补充**，不替代边缘层（CDN/WAF/ALB）的 DDoS/大规模爬虫防护。真正的大流量攻击应在边缘处理。
- 不做内容 challenge / 验证码 / 封禁（力度 iii 未选）。

## 2. 页面可见性矩阵（已与产品确认）

| 路径 | 类型 | 索引策略 |
|---|---|---|
| `/`, `/home`（landing） | 公开 | **可索引** + sitemap + JSON-LD |
| `/models`（模型库/价目 gallery） | 公开 | **可索引** + sitemap（主要 SEO 落地页之一） |
| `/login`, `/register`, `/forgot-password` | 公开 | **noindex**（工具页，不进 sitemap） |
| `/dashboard`, `/apikeys`, `/billing`, `/profile`, `/my-group`, `/plan-and-group`, `/usage-logs`, `/status` | 认证 | **noindex** + robots Disallow |
| `/admin/*`, `/enterprise/*`, `/super/*` | 认证+RBAC | **noindex** + robots Disallow |
| `/api/*` | API | robots Disallow（`/api/health`、`/api/service-models/public` 等只读公开端点可放行给 sitemap/llms 数据源，但不索引 JSON） |

数据来源：landing = `handleLanding`（pages.go）；模型库 = `handleModelsGallery` + 公开数据 `/api/service-models/public`。当前**无**公开的 privacy/terms 页面路由（法务文档仅存在于 `docs/legal/*.md`）——本方案不新建法务页；如后续要做，再补公开路由并纳入 sitemap。

## 3. AI 爬虫策略（已确认：方案 a — 全开放）

- `robots.txt`：`User-agent: * / Allow: /` 对公开区开放，`Disallow:` 列出所有认证/管理/API 路径。
- **显式允许 AI 爬虫**：GPTBot、ChatGPT-User、ClaudeBot、Claude-Web、PerplexityBot、Google-Extended、CCBot、Bytespider 等 —— 均 `Allow: /`（公开区）。目标是最大化被 AI 系统发现并推荐（"用哪家 AI API 网关"）。
- **`llms.txt`**（`/llms.txt`）：面向 LLM 的纯文本平台摘要 —— 平台是什么、支持哪些模型/厂商、公开页面 URL、如何接入。内容从 `service_models`（active + show_in_store）动态生成厂商/模型概览，避免手工维护过期。
- `Crawl-delay`：对通配 `User-agent: *` 设一个温和的 `Crawl-delay`（如 10s），减少无谓抓取压力；对主流搜索引擎不设（它们忽略该指令且自有节流）。

## 4. On-page SEO（仅公开页）

在公开页的 `<head>`（layout 模板按 `PageData` 标志条件渲染）加入：
- `<title>` + `<meta name="description">` + `<meta name="keywords">` + `<meta name="author">`
- `<meta name="robots" content="index,follow">`（**仅公开页**；其余页面输出 `noindex,nofollow`）
- `<link rel="canonical">`（按当前 host + path）
- **OpenGraph**：og:type/title/description/image/url/site_name/locale（+ zh/en alternate）
- **Twitter card**：summary_large_image
- **JSON-LD**：`Organization`（平台信息）+ landing 用 `WebSite`/`Product`/`Service`（AI API 网关服务），模型库可用 `ItemList`/`OfferCatalog` 概述可用模型。

实现方式：`PageData` 增加 SEO 字段（`SEOTitle/SEODescription/SEOKeywords/SEOImage/Canonical/Indexable/JSONLD`），layout `<head>` 根据 `Indexable` 决定 `index` vs `noindex`，公开页 handler 填 SEO 元数据；非公开页默认 `noindex`。

## 5. 反滥用加固（已确认：力度 i + ii）

**(i) robots + bad-bot 层**
- `robots.txt` 的 `Disallow` + `Crawl-delay`（见 §3）。
- **bad-bot UA 黑名单中间件**：对已知恶意/资源消耗型抓取 UA（如无视 robots 的批量抓取器、明显伪造 UA）返回 403。白名单友好爬虫（上面列的 AI/搜索爬虫）不受影响。列表保守，避免误伤。

**(ii) 未认证流量 per-IP 限流**
- 复用现有 `middleware.NewIPRateLimiter(ratePerSec, rdb)`（Redis 优先、单机内存兜底、per-IP 令牌桶，已有测试）。
- 作用范围：**公开页面 + 公开只读 API**（landing、/models、/api/service-models/public、robots/sitemap/llms、/api/health 等未认证入口）。**不影响**已认证的正常业务流量（它们有各自的 key/JWT 级限流）。
- 超限返回 `429 Too Many Requests`。
- 速率：给一个合理默认（如公开页 ~5–10 req/s/IP，可配置），既不挡真实用户/爬虫的正常抓取，又能压制暴力扫描。**具体阈值在 review 时定**。
- **注意**：`X-Robots-Tag: noindex` 响应头在 `web.SecurityHeaders()`（或专门中间件）里对所有非公开路径统一下发，作为 meta robots 的双保险（尤其对 JSON/API 响应）。

## 6. 实现清单（待实现，本文档为 review 依据）

| 项 | 位置 | 说明 |
|---|---|---|
| `GET /robots.txt` | 新 handler + 路由 | 动态生成，含正确 host + Sitemap 行 + AI 爬虫 Allow + Disallow 列表 + Crawl-delay |
| `GET /sitemap.xml` | 新 handler | 公开页 urlset（/ 、/models）loc/lastmod/changefreq/priority |
| `GET /llms.txt` | 新 handler | 平台摘要 + 从 service_models 生成的厂商/模型概览 + 公开页 URL |
| `<head>` SEO 块 | layout 模板 + PageData | 公开页 index + OG/Twitter/JSON-LD；非公开页 noindex |
| `X-Robots-Tag` | SecurityHeaders/新中间件 | 非公开路径统一 noindex,nofollow |
| bad-bot UA 拦截 | 新中间件 | 保守黑名单 → 403 |
| 未认证 per-IP 限流 | 复用 NewIPRateLimiter | 挂到公开页 + 公开只读 API 组，429 |
| i18n | en/zh | 若 SEO 文案需要多语言 |
| 测试 | routes/web 测试 | robots/sitemap/llms 输出、noindex 头、限流 429、bad-bot 403 |

## 7. 回溯基线（tag）

实现前在当前代码打 tag（见提交记录），实现不理想可回退到该 tag。tag 名：`pre-seo-antiabuse-<date>`（annotated）。

## 8. Review / 测试清单（交付后据此验收）

- [ ] `/robots.txt` 返回正确内容：公开区 Allow、认证/API Disallow、AI 爬虫段、Sitemap 行 host 正确。
- [ ] `/sitemap.xml` 只含公开页，XML 合法，host 正确。
- [ ] `/llms.txt` 返回平台+模型摘要，无敏感信息（不泄露 upstream provider / key / 内部路径）。
- [ ] landing / models：`<head>` 有 index,follow + OG + JSON-LD；curl 可见。
- [ ] `/dashboard`、`/admin/*`、`/api/*`：响应含 `X-Robots-Tag: noindex`，且 robots.txt Disallow。
- [ ] 未认证高频请求公开端点 → 429；正常低频请求不受影响；已认证业务流量不受影响。
- [ ] bad-bot UA → 403；GPTBot/ClaudeBot/Googlebot 等 → 正常 200。
- [ ] llms.txt / sitemap 不暴露认证页 URL。
- [ ] build/vet/gofmt/i18n/schema/CI 全绿。

## 9. 风险与注意

- **可发现 vs 反滥用张力**：限流阈值过低会挡住合法爬虫抓取（影响 SEO），过高则反滥用无效。默认取温和值，review 时按实际流量调。
- **多实例**：限流走 Redis 才在多实例间一致；单机内存兜底在多副本下不精确（文档化为已知约束）。
- **llms.txt / sitemap 内容安全**：只输出公开、非敏感信息；模型概览用对外 product_id / 展示名，绝不含 upstream provider、key、内部 ID。
- **边缘层**：本方案是应用层；建议前置 Cloudflare/WAF 做真正的 DDoS/bot 防护。

# Proxy Key Pool 设计意图对齐讨论

**Status:** DRAFT  
**Created:** 2026-05-05 17:31 UTC  
**Author:** Kimi Code CLI  
**Scope:** Portal ↔ Proxy 设计意图对齐

---

> 以下是我梳理的 10 个维度中，**我理解到的设计意图**与**实际代码/文档呈现**之间可能存在的偏差。请逐条在下方空白处回复，确认哪些是我的误解、哪些是意图与实现的脱节。

---

## 一、Provider Base URL 管理：谁是唯一来源？

**我的理解（设计意图）**：Portal `providers` 表是 Base URL 的唯一来源，Proxy 通过 sync 的 `providers[]` 消费，不再硬编码。

**发现的偏差**：
- Portal `providers` 表（迁移 009）里的 base_url 与 `providerDefaultURLs` map 不一致
- `providerDefaultURLs` 里 meta 是 `llama-api.meta.com/v1`，表里是 `api.llama.meta.com/v1`
- Proxy `DefaultProviderConfigs` 又改了一次（`api.llama.com/compat/v1`）
- **结果**：同一家 provider 的 base URL 在三个地方有三种值

**想确认**：
1. 你的设计意图是否确实是 **Portal `providers` 表 = 唯一来源**？
2. 那 `providerDefaultURLs` 这个硬编码 map 是过渡方案还是应该废弃？
3. Proxy 侧是否也应该完全摒弃 `DefaultProviderConfigs`，100% 依赖 sync 数据？

<A> portal的provider数据库表维护了基本的18家provider，测试时又加入两家grok和xiaomi，一共20家。这个平时基本不动的，删除provider需要super-admin才行。
这里的baseurl用于在admi添加model或upstream key时作为default url，如果用户填写了baseurl，就会覆盖这里的baseurl设置。它不是唯一来源，只是缺省来源。

逻辑上，provider表项中的baseurl是可以由admin手动修改的，但这18或20家provider基本涵盖了全球主要的大模型公司，这个地方平台被改动的机会不大。所以需要在初始时将baseurl确认准确。

至于proxy的DefaultProviderConfig，这是需要你在proxy实现时评估是作为临时变量，还是常用变量使用。我建议是作为fallback的保底数值。



**你的回复：**
```
（请在此填写）
```

---

## 二、Key 分配模式：`shared` vs `exclusive` 默认

**我的理解（设计意图）**：`upstream-keypool-management-202605051321.md` 文档说 `assignment_mode` 默认 `shared`，意味着 key 默认对所有 Proxy 共享。

**发现的偏差**：
- 实际代码 `createUpstreamKey` 中：`if req.AssignmentMode == "" { req.AssignmentMode = "exclusive" }`
- 迁移约束也是 `DEFAULT 'exclusive'`
- **结果**：如果不手动改，每个 key 默认被当前 Proxy 独占

**想确认**：
1. 文档写 `shared` 是笔误还是意图变更？
2. 多 Proxy 场景下，你是不是希望默认 `shared`，只有特殊 key 才设为 `exclusive`？

**你的回复：**
```
<A> 之前实现是shared，这次agent重写portal是默认设置为exclusive，主要是容易测试；之前的web界面添加upstream key是有个配置选项，这次的网页还未实现。
在多proxy场景下，绝大部分情况是exclusive，少量是shared. 因为部署在不同地区，这个key在各个地方随机出现容易被模型公司检测并废弃该key。

---

## 三、Model 名称映射：`productId` → `apiModelName` 是否必须？

**我的理解（设计意图）**：Portal `service_models` 区分 `productId`（用户看到的模型名）和 `apiModelName`（实际发给上游的模型名），Proxy 需要做映射转换。

**发现的偏差**：
- Proxy 目前直接把 `req.Model` 发给上游，没有任何转换
- `models/sync` 端点确实返回了 `apiModelName`，但 Proxy 只用它来校验 `allowed_models`，不做名称替换

**想确认**：
1. 在你的使用场景中，`productId` 和 `apiModelName` 通常是否一致？
2. 如果不一致（比如用户请求 `gpt-4o` 但上游要 `gpt-4o-2024-08-06`），是不是**必须**让 Proxy 做这层映射？
3. 还是说映射应该由前端/客户端完成，Proxy 只负责透传？

**你的回复：**
```
<A> productID和apiName不是一个东西，前者是用户看的，后者是实际调用的。但这个转换得看最新GO的实现，我理解应该是已经转换好了，例如 kimi:kimi k2.5 应该已经是 kimi k2.5了。你仔细读一遍portal的代码实现。

proxy不一定是简单透传，理由如下。
因为这次实现的用户key有三种：sk-wgit-xxxx（一般key），sk-wgit-en-xxxx（企业用户的key），sk-wgit-tp-xxxx（套餐用户的key）。
他们使用时填入的模型名称可能不同，尤其时sk-wgit-xxxx的economy用户，我们可能会提供一个通用的modelname，例如wgit-maas，在economy模式下，这个权限可能在5中不同的modelname中自选；
例如用户使用时配置为kimi k2.5，那proxy上行就hit到K2.5；如果用户配置为minimax m2.5，那上行就的hit到m2.5的pool... 类似这种
目前业界很多开始实现这样的做法，对于用户他使用时“baseurl+modelname+key”不变，只需要登录portal修改当前需要service的model即可。对于企业客户更是这样，你不可能要求他们在agent或编程的cli或ide上不停的更换modelname。

所以需要有一种机制，用户key的“能够使用的模型（例如有5种）” + “配置当前使用的模型（5选1）”，需要动态在portal和proxy之间交换。我看其他平台能够切换模型的生效时间只有1分钟左右。

```

---

## 四、Key 的 `models` 过滤：key 能力范围要不要严格限制？

**我的理解（设计意图）**：`upstream_keys.models` JSONB 字段记录该 key 能访问的模型列表，Proxy 应该只把请求路由到支持该模型的 key 上。

**发现的偏差**：
- `SyncKeyResponse` 确实带了 `models` 字段
- 但 Proxy 的 `GetKeyByPriority` 完全不检查 `models`，只看 provider 和 priority

**想确认**：
1. `models` 字段的设计意图是不是"如果非空，则该 key 只能服务这些模型"？
2. 如果为空，是不是表示"该 key 支持该供应商所有模型"？
3. 还是说这个字段只用于 Admin 展示，不影响路由决策？

**你的回复：**
```
<A> 这是个好问题！和问题3一样，我们对用户可以同一个key提供不同能力的模型服务，我们的upstream provider对我们也是如此，例如同一个upstream key，昨天kimi提供k2.5能力，今天就自动提升到k2.6，并不需要用户做什么设置。
所以对于portal这次重构，key pool做了检测机制，手动+24小时自动检测，如果upstream key能否返回最新的模型能力列表（例如openai），那我们就更新model列表信息；如果upstream key比较保守不返回模型能力（例如anthropic），那就由admin管理key pool时手动更新upstream key支持何种模型，将信息更新到数据库。

如果model不为空，你就理解只支持这些模型；如果是admin更新不及时导致没法使用其他模型能力，这是admin的失职，不需要proxy补救。
这次重构的proxy中，建议根据“key的model能力” + “priority” 来确定给到用户的服务能力

```

---

## 五、Rate Limit：应该基于 User 还是 Upstream Key？

**我的理解（设计意图）**：`upstream_keys` 表有 `rpm_limit` / `tpm_limit`，每个上游 key 有自己的限额，Proxy 应该按 upstream key 做 token bucket。

**发现的偏差**：
- Proxy 当前 rate limit 的 key 是 `ratelimit:user:<keyHash>`（用户 key hash）
- `rpm_limit` / `tpm_limit` 从 Portal sync 传过来了，但 Proxy 完全没有使用
- Proxy 的 rate limit 是 8 档硬编码（60~100000），不是从 key 元数据读取

**想确认**：
1. 你的设计是不是：**用户层面的限速**（根据用户订阅/套餐）和 **上游 key 层面的限速**（根据 key 的 rpm_limit/tpm_limit）应该同时存在？
2. 还是说当前阶段只需要用户层面限速，upstream key 的限额暂时不用管？

**你的回复：**
```
<A> 这个实现是proxy的能力重点。因为portal拿到的upstream key并在key pool中设置的，这个数据结构基本都是静态信息，包括rpm和tpm，这是我们上游模型公司能够提供的能力。
例如rpm=60。但我们提供服务时，可能在多个key之间轮询，这样可以提供120，300，甚至600的rpm给我我们下游的用户。proxy在使用中需要进行流量监控和负载均衡等操作，三个意图：
一是统计request和token数量汇报给portal进行计费，二是流量均衡确保某upstream key不过载，三是监控给到下游的用来能力符合他的套餐或用户key的能力设定。

简单讲：proxy 需要同时对upstream key 和 user key进行request + token的双重监控 - upstream key的主要是保证不爆单key的quota，user key这边需进行rate limit。

对于user key的rate limit，目前实现的比较粗糙，在auth成功后就简单判断rpm等就放行进入下一环节，这里你略微强化（精细化）即可，但不必做到计费形式的颗粒度。保证整体user key的接入能力。
对于upstream key的监控，就是hit、roundrobin、cooldown、re-active之间的状态转换了。这个你需要重新实现。

upstream的限额，我理解你说的 rmb/usd quota每月, 这个proxy不用管。

```

---

## 六、`validation_status`：non-valid key 能不能进生产流量？

**我的理解（设计意图）**：`validation_status` 有 `pending` / `valid` / `invalid` / `rate_limited` / `error`，只有 `valid` 的 key 才能用于生产。

**发现的偏差**：
- Portal sync 查询条件是 `WHERE enabled = true`，**不过滤 validation_status**
- Proxy 也不检查 validation_status
- **结果**：刚导入还没验证的 `pending` key、验证失败的 `invalid` key，都会被同步到 Proxy 并被选中

**想确认**：
1. 你是不是希望 Portal sync 查询中加上 `AND validation_status = 'valid'`？
2. 还是说 `enabled = true` 就是唯一的准入门槛，validation_status 只是给 Admin 看的参考？

**你的回复：**
```
<A> 重构的upstream key的状态有两种：一种是admin人为控制其active/enable or deactive/disable；第二种是pending，vaild，invalid，即手动或24小时自动检测该key是否有效，pending是刚加入的状态，valid或invalid是检测后该key是否有效的状态。

对于proxy来说：enable的key就可以进入生产使用---几种异常情况：这个key废弃显示invalid，proxy选中了实际无效；key是可用的，但portal没有进行检测，状态是pending；或者这个key暂时欠费检测是invalid，但给上游付费后下次检测就valid了。

建议维持现有条件，只检查enable；或者 enable + valid。如果决定严谨些就使用第二种
```

---

## 七、Provider ID 统一：`xai` vs `x-ai`

**我的理解（设计意图）**：所有地方用统一的 provider ID（与 `providers` 表的 `id` 字段一致）。

**发现的偏差**：
- `providers` 表 ID 是 `xai`
- `providerDefaultURLs` map 的 key 是 `x-ai`
- Proxy `DefaultProviderConfigs` 和 `GetProviderFromModel()` 用的是 `xai`

**想确认**：
1. 这是笔误还是有意区分？
2. 统一成 `xai` 有没有问题？

**你的回复：**
```
<A>xai的id是xai，x-ai是不对的，这是从openrouter同步的数据在调试中写入代码或数据库了。你在工作中发现必须改为xai。

重申一次：由于模型产品变化很快，新产品层出不穷，我们务必使用provider的公司名字作为id（公司全称，小写），不要出现qwen，claude这种产品名代替alibaba和anthropic公司名的情况。
否则以后有个新产品不叫claude了，你还重建一个provider？

```

---

## 八、Sync 机制：HTTP Polling 是临时方案还是长期方案？

**我的理解（设计意图）**：`upstream-keypool-management-202605051321.md` 说"Proxy 拉取 + Redis 缓存 + Redis Pub/Sub 通知"。

**发现的偏差**：
- 当前只有 HTTP polling（60s 间隔），没有 Pub/Sub
- Redis 只在初始加载失败时做 fallback，且 `loadFromRedis` 有 bug

**想确认**：
1. 60s HTTP polling 在当前阶段够不够用？
2. Redis Pub/Sub 是计划中的优化还是已经放弃？
3. 如果保持 HTTP polling，sync 间隔 60s 是否合适？

**你的回复：**
```
<A> 部署时需要“HTTP + Redis”，最初规划单台服务器用内存不用redis，后来发现还是不行，所以一开始就需要redis，你看start.sh脚本，初始启动就有redis。
至于redis的pub和sub，需要这次重构时实现。

```

---

## 九、Affinity Cache：真的想让它影响路由吗？

**我的理解（设计意图）**：相似请求路由到同一 upstream key，保持 prompt cache 热态，降低延迟和成本。

**发现的偏差**：
- `GetChannelWithPriority` 使用 `hash(priority:model:keyID)` 做 key
- `RecordChannelWithPriority` 使用 `priority:model:group:sha1(body)[:8]` 做 key
- 返回的 `channelID` 只用于审计日志，不改变 `GetKeyByPriority` 的选择结果

**想确认**：
1. 你是想 Affinity Cache **真正影响路由决策**（有 cache hit 时优先用历史 key），还是只是**记录**用途？
2. 如果影响路由，优先级应该怎么排？（Affinity > Priority > LRU？）

**你的回复：**
```
<A> “有cache hit记录时优先使用历史key” 是我告诉agent如此实现，我的考虑是如果每次request都被round balance到不同的upstream key，那基本就没法cache hit了。affinite的feature可以基本不用了。
至于你发现的get和recoard的hash做法不同，这个太细节了不是我规定的。你评估哪种方式合适？

就Cache hit这个事情，使用affinite的前提是路由比较顺畅，不要有太多overhead。所以对于路由的考虑60%（务必保证企业+订阅客户使用顺利），Affinite考虑30%（降低成本是第二选项）。

你有什么建议可以讨论。

```

---

## 十、Provider 数量：21 家 vs 18 家

**我的理解（设计意图）**：18 家标准供应商。

**发现的偏差**：
- Portal `providers` 表有 21 家（多了 azure、openrouter、groq）
- Proxy `DefaultProviderConfigs` 只有 18 家
- `providerDefaultURLs` 有 22 家（多了 xiaomi）

**想确认**：
1. azure / openrouter / groq 是计划内要支持的还是遗留数据？
2. 如果支持 azure，Proxy 需要 Azure 专用 adaptor（deployment-id 路径、api-key auth 等），这些是否属于当前阶段 scope？
3. xiaomi 是 plan 中的 provider 吗？

**你的回复：**
```
<A> 本来是18家，调试portal是多写了两条到数据库，目前是20个（加groq+xiaomi）。没有openrouter，这个我要求agent删除了。待会你阅读代码再看看。

这次可以不支持microsoft，mistral，cohere等，但需要支持azure，因为anthropic和openai都把业务上线到aws了，他们的基础设施强健很多。

xiaomi应该可以工作了。

对了，再这里补充一点：Admin关闭了某个provider，或是某个model，主要的意图是让用户“看不到产品”+ “无法付费购买”+“无法续费”，而不是让proxy停止对该provider的upstream key的服务！
因为很多订阅客户可能费用已经支付到3个月之后了，关闭这个模型只是不接受新用户，但之前用户的服务保证需要继续。Proxy在实现时不要弄混淆了！

其实在portal侧，关闭provider后，只是无法创建基于该provider的新的upstream key，但已经在数据库的upsteram key是不受影响的。你待会看代码时再确认一次。

```


---

## 其他问题（如有）

如果你发现我遗漏了其他意图与实现的偏差，也请一并写在这里：

```
（请在此填写）
```

---

*填写完毕后保存文件，告诉我文件名，我会读取你的回复并更新主分析文档。*

---

## 追加讨论（2026-05-05 第二轮）

> 第一轮bug修复已完成（详见对话记录）。以下是基于你10条回复后的第二轮架构层面对齐，需要你逐条确认。

---

## 十一、Model 动态切换机制（最大架构变更）

你提到用户 key 有「可用模型列表（5种）+ 当前配置模型（5选1）」，Proxy 需要用当前配置模型替换用户请求中的 model name。

**当前缺失：**
- Portal 的 auth 响应（`validateUserKey`）目前只返回 `allowed_models`，**没有 `current_model`**
- Proxy 拿到 auth 结果后，直接拿 `req.Model` 去路由，没有做替换

**需要你确认：**
1. `current_model` 应该存在哪个表里？是 `api_keys` 表新增一个 `current_model` 字段，还是存在其他地方？
2. 用户请求中的 `model` 参数如果和 `current_model` 不一致，Proxy 是**静默替换**（用户无感知）还是**报错**？
3. 切换模型的 1 分钟生效目标：是靠缩短 sync 间隔（比如 10s），还是靠 Redis Pub/Sub 即时推送？

**你的回复：**
```
<A> 建议在user的api_keys中新增加几个字段：一个是enroll_group[]，一个是current_group, 一个是current_model; 第一个是用户创建key时自己选择或是系统自动带入的，第二和第三是用户使用时在dashboard自己配置的。

简单说：重构的user key分 “user -- group” 两级管理，任何user都必须归类到至少一种group中（类似电脑的用户和群组的设定）：

Group种类：企业group（这里面分很多种，admin创建并维护）；订阅用户的套餐（每个套餐档位就是一种group）；一般用户组，即general用户，系统默认设置几种group选项；economy用户组，打折model产品的组合，也有几个group。

企业用户 和 订阅用户，有单独的 user key，这种key只能用在enterprise 或是 token plan（code plan）的群组场景。
一般用户 和 经济用户，他们使用sk-wgit-xxxx开头的key，可以加入几种不同的group，使用时用户选中某个group（current group） + 某个model（current model），就按照这个group的计费标准和服务能力例如rpm来约束。

如果用户选中了group-A，这个组里有“kimi k2.5， minimax m2.5， qwen3.6， deepseek-r1，glm-5.0” 5种模型能力，客户只能在5种里面选一个。。

group-A，我们定义的modelnmae是 wgit-maas-1；group-B，modelname是wgit-maas-2...以此类推，用户实际在ide或cli或agent中配入的是“woodit baseurl + sk-wgit-xxxx用户key + wgit-maas-1的modelname”；

如果dashboard选中k2.5，但他在ide使用时设置了wgit-maas-1不用修改可以直接使用；他想更换模型，登录dashboard换到例如glm5.0 即可，本地配置无感。

动态切换，实际场景用2~3分钟可以接受，需要redis推送机制。

注：只有一种情况，我们需要检查modelname，即客户订阅的是某模型单品，同时又订阅了其他套餐 -- 例如支付了50元的k2.6产品，这个产品属于例如group-C，modelname wgit-maas-3，而之前已经采购了group-A的产品，这种情况下，允许用户在dashboard设置优先级，优先使用单一模型，还是其他套餐。
如果设置了group-C优先于group-A，那同样的user key，电脑仍设置了wgit-maas-1，这种key的请求进入proxy后，需要返回并提醒目前是wgit-maas-3优先，请用户修改modelname。

```

---

## 十二、Upstream Key Rate Limit 的实现策略

你要求 Proxy 对 upstream key 做 RPM/TPM 监控（防止爆单），同时对 user key 做 rate limit。

**当前 Proxy 的 rate limit 完全基于 user key**，upstream key 没有任何限速逻辑。

**我的建议方案：**
- **User key rate limit**：保持现有 token bucket，但改从 Portal sync 读取 `rate_limit_rpm` / `rate_limit_tpm`（而非 8 档硬编码）
- **Upstream key rate limit**：新增一个独立的 token bucket map，key = `upstream:<provider>:<keyID>`，限额从 sync 的 `rpm_limit` / `tpm_limit` 读取
- 每次选 key 时，`GetKeyByPriority` 先检查该 key 的 upstream token bucket 是否还有余量

**问题：** upstream key 的 token bucket 应该放在 keypool manager 里还是单独一个模块？你倾向哪种？

**你的回复：**
```
<A> 同意你的方案。proxy保持初筛，细颗粒需要portal计算后返回。

但rpm或tpm_limit这个结构，你需要看代码标识key的能力（静态值），还是表示目前key的配额使用情况（动态值）。

我对portal那边的要求是：负载静态数据管理的结构体定义，和负责动态数据统计、计算、更新的结构体分开。

```

---

## 十三、validation_status 过滤决策

你说「建议维持现有条件，只检查 enable；或者 enable + valid。如果决定严谨些就使用第二种」。

**我的建议：** 用 `enable + valid`。理由：
- `pending` key 还没验证，选中了可能就是一次无效请求
- `invalid` key 已经确认无效，选中了就是浪费
- Portal sync 查询加 `AND validation_status = 'valid'` 改动极小

但如果你倾向于保持 `enable` 即可（给用户更多灵活性），我也按你的来。**你选一个？**

**你的回复：**
```
<A> 同意你的建议。
```

---

## 十四、Affinity Cache 的路由优先级

你说「有 cache hit 时优先使用历史 key」，且「路由考虑 60%，Affinite 考虑 30%」。

**当前问题：**
- `GetChannelWithPriority` 用的 key：`hash(priority:model:keyID)`
- `RecordChannelWithPriority` 用的 key：`priority:model:group:sha1(body)[:8]`
- 两个 key 不一致，cache 几乎永不命中

**我的评估：**
- 应该统一用 `priority:model:group:sha1(body)[:8]`，因为 affinity 的目的是"相似请求路由到同一 key"
- 路由优先级：**Priority 过滤 → 可用性检查 → Affinity hit（如有）→ LRU fallback**
- 即：affinity 不替代 priority 和可用性，只是在一个已过滤的候选集内优先选历史 key

**你是否同意这个方案？**

**你的回复：**
```
<A> 同意你的的方案，先实现一版我们实测affinite的hit概率，即便做不到80%的cache命中，做到30%也是对用户的费用不小的节省。
```

---

## 十五、Provider 关闭的业务规则（非常重要）

你特别强调：**Admin 关闭 provider/model 只是不让新用户看到/购买，已有订阅继续服务！**

这意味着：
- Portal sync 返回的 `providers[]` 中，`enabled = false` 的 provider **不应影响 Proxy 的现有路由**
- Proxy 消费 `providers[]` 时，只用它来获取 base_url，**不因 provider 被关闭而停用该 provider 的 upstream key**

**我的理解对吗？** 如果是这样，Proxy 的 provider 配置应该是「从 Portal sync 读取并缓存，即使 Portal 关闭了该 provider，Proxy 仍继续使用缓存的 base_url」，而不是「Portal 关闭就不可用」。

**你的回复：**
```
<A> 是的，portal的 provider的enable=false 不影响proxy的路由。只要key pool哪怕还有一个upstream key存在，proxy就能提供服务。

对于proxy的路由和服务能力等工作要点来说，upstream key结构体携带的信息原比provider的信息重要。provider的状态管理，是从根源上减少或修改user key的上行诉求，让用户逐步不要选择该provider的服务。
但并不是一刀切让proxy停止服务。

```

---

## 十六、Azure 支持的具体需求

你说需要支持 Azure。Proxy 里 `microsoft` 已经有 entry，但 BaseURL 是 `https://{resource}.openai.azure.com/openai/deployments`。

**问题：**
1. Azure 的 `{resource}` 是部署时配置的变量，这个值应该从哪来？是 upstream key 的 `base_url` 字段覆盖，还是需要一个专门的 `azure_resource` 配置？
2. Azure 的 auth 是 `api-key`（不是 bearer），且请求路径格式是 `/deployments/{deployment-id}/chat/completions`，这需要一个专门的 Azure adaptor。当前 Proxy 的 `microsoft` 用的是 generic OpenAI-compatible adaptor，可能不兼容。

**你是否已经有一个 Azure adaptor 的计划，还是让我来设计？**

**你的回复：**
```
<A> 专门开发一个azure的adapter，其baserul由upstream key传下来，因为aws提供key时会约定是哪个区域的。你在proxy的default值使用us east 1 区域。你来设计这个adapter。
```

---

*以上6条，你先看看。其中 **第11条（Model 动态切换）和第15条（Provider 关闭规则）** 是最影响后续代码架构的，我需要你的明确答复后才能动手。*


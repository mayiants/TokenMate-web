# Subscription Plans Competitive Analysis

> **ACTIVE** - 2026-05-07

## Market Research Summary

### International Providers

| Provider | Tier | Price | Tokens/Month | TPM | RPM |
|----------|------|-------|--------------|-----|-----|
| OpenAI | Free | $0 | 40K/day | 40K | 3 |
| OpenAI | Tier 1 | $5/mo | Pay-as-you-go | 90K | 200 |
| OpenAI | Tier 2 | $50/mo | Pay-as-you-go | 350K | 500 |
| Anthropic | Free | $0 | - | 40K | 5 |
| Anthropic | Tier 1 | Pay-go | - | 100K | 20 |

### Chinese Providers

| Provider | Price | Tokens | TPM | Notes |
|----------|-------|--------|-----|-------|
| DeepSeek | ¥0.14/1M | Pay-go | 1M+ | Very competitive pricing |
| Moonshot | ¥0.012/1K | Pay-go | 200K | ~¥12/1M tokens |
| Zhipu GLM | ¥0.01/1K | Pay-go | 300K | ~¥10/1M tokens |
| Alibaba Qwen | ¥0.008/1K | Pay-go | 500K | ~¥8/1M tokens |

## Our Competitive Positioning

### Token Plans (API Access)

| Tier | Price | Quota | Daily | TPM | TPH | Concurrent | Market Position |
|------|-------|-------|-------|-----|-----|------------|-----------------|
| Lite | ¥29 | 50M | 3M | 50K | 1M | 5 | Cheaper than OpenAI Tier 1, more quota |
| Standard | ¥69 | 200M | 10M | 150K | 3M | 10 | Competitive with DeepSeek pricing |
| Pro | ¥199 | 800M | 50M | 500K | 10M | 30 | Enterprise-ready |
| Max | ¥499 | 3B | 200M | 2M | 50M | 100 | High-volume use cases |

**Price per 1M tokens:**
- Lite: ¥0.58/1M (very competitive)
- Standard: ¥0.35/1M (excellent value)
- Pro: ¥0.25/1M (bulk discount)
- Max: ¥0.17/1M (enterprise pricing)

### Code Plans (Code Assistant)

| Tier | Price | Requests/Month | Daily | RPM | RPH | Concurrent | Market Position |
|------|-------|----------------|-------|-----|-----|------------|-----------------|
| Lite | ¥9 | 500 | 50 | 10 | 200 | 2 | Entry level |
| Standard | ¥29 | 2,000 | 200 | 30 | 500 | 5 | Similar to Copilot |
| Pro | ¥69 | 10,000 | 500 | 60 | 2,000 | 10 | Team tier |
| Max | ¥199 | 50,000 | 2,000 | 120 | 5,000 | 30 | Enterprise |

**Price per 100 requests:**
- Lite: ¥1.80
- Standard: ¥1.45
- Pro: ¥0.69
- Max: ¥0.40

## Competitive Advantages

1. **Better value than OpenAI**: More tokens per dollar at all tiers
2. **Competitive with Chinese providers**: Similar or better pricing than Moonshot/Zhipu
3. **Clear tier progression**: Each tier offers ~4x value increase
4. **Flexible rate limits**: TPM/TPH for token plans, RPM/RPH for code plans
5. **Concurrent request limits**: Prevents abuse while allowing reasonable usage

## Implementation Notes

- Rate limits are enforced at the Proxy level via API key validation
- TPM/TPH counters reset automatically (minute/hour windows)
- Daily quotas reset at midnight UTC
- Monthly quotas are subscription-based (30-day cycles)

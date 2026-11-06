#!/usr/bin/env python3
"""Import TokenRouter model pricing into ai-portal-go.

Input: workspace/tokenrouter/models-YYYYMMDDX.json
Output: /tmp/import_tokenrouter_models.sql

Run with:
    python3 scripts/import_tokenrouter_models.py
    export DB_PASSWORD=$(grep -oP '^DB_PASSWORD=\\K.*' .env.go-platform)
    docker exec -i -e PGPASSWORD="$DB_PASSWORD" ai-portal-postgres psql -U postgres -d ai_proxy_portal < /tmp/import_tokenrouter_models.sql
"""
import json
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]  # /opt/ai-platform
JSON_PATH = ROOT / "workspace" / "tokenrouter" / "models-20260817.json"
OUT_PATH = Path("/tmp/import_tokenrouter_models.sql")

PROVIDER = "tokenrouter"
REGION = "*"
CURRENCY = "USD"
FIXED_PER_REQUEST = 0.005  # USD per request added downstream

PRICE_RE = re.compile(r"\$([0-9]+(?:\.[0-9]+)?)")


def parse_price(text: str) -> float:
    if not text:
        return 0.0
    m = PRICE_RE.search(str(text))
    return float(m.group(1)) if m else 0.0


def label_kind(label: str) -> str:
    low = label.lower()
    # Media-specific component labels take precedence over generic input/output,
    # so e.g. "Image Output Price" is treated as an image component and can be
    # displayed separately in the gallery.
    if "audio input" in low or "audio completion" in low or "audio output" in low:
        return "audio"
    if "image output" in low or "image input" in low:
        return "image"
    if "input price" in low:
        return "input"
    if "output price" in low:
        return "output"
    if "cache read" in low or "cache hit" in low:
        return "cache_hit"
    if "cache write" in low:
        return "cache_write"
    return "other"


def model_type(cap_type: str, lines: list) -> str:
    """Return the provider-declared model type, normalised to our vocabulary.

    Models that also support other modalities (e.g. Text+Image) keep the
    declared primary type; the additional modalities are stored in
    capabilities.supported_types so filters can still treat them as media.

    Exception: models whose only modality tags are Text+Audio are surfaced as
    audio primary type so the model gallery can expose a meaningful audio
    category while still listing them under supported_types for text callers.
    """
    # TokenRouter sometimes puts modality tags in lines instead of the top-level
    # type field (e.g. openai/gpt-audio-mini lines contain Text + Audio).
    line_types = set()
    if lines:
        for line in lines:
            low = str(line).lower().strip()
            if low in ("text", "image", "video", "audio"):
                line_types.add(low)

    t = cap_type.lower()
    if t == "text":
        # A Text declaration with an explicit Audio tag (and no stronger media
        # tag) is treated as an audio model for gallery/filtering purposes.
        if "audio" in line_types and "image" not in line_types and "video" not in line_types:
            return "audio"
        return "text"
    if t in ("image", "video", "audio"):
        return t
    if "audio" in line_types and "image" not in line_types and "video" not in line_types:
        return "audio"
    return "multimodal"


def supported_types_from_lines_and_prices(lines: list, prices: list) -> list:
    """Extract modality tags from TokenRouter lines and price labels.

    Price labels like 'Audio Input', 'Image Output' reveal real capabilities
    even when the model's top-level 'type' or lines tag says Text.
    """
    types = []
    if lines:
        for line in lines:
            low = str(line).lower().strip()
            if low in ("text", "image", "video", "audio") and low not in types:
                types.append(low)
    if prices:
        for p in prices:
            label = str(p.get("label", "")).lower()
            if ("audio input" in label or "audio completion" in label or "audio output" in label) and "audio" not in types:
                types.append("audio")
            if ("image input" in label or "image output" in label) and "image" not in types:
                types.append("image")
    return types


# Backwards-compatible alias for callers that only pass lines.
def supported_types_from_lines(lines: list) -> list:
    return supported_types_from_lines_and_prices(lines, [])


def video_billing_meta(capability_type: str, prices: list) -> dict:
    """Detect video/audio billing unit from price labels (legacy flat metadata)."""
    if capability_type not in ("video", "audio") or not prices:
        return {}
    per_second = []
    block_specs = []
    variant_specs = {}
    for p in prices:
        label = p.get("label", "")
        val = parse_price(p.get("currentPrice", ""))
        if val <= 0:
            continue
        low = label.lower()
        # TokenRouter keeps the unit in 'value' while 'currentPrice' is sometimes stripped.
        value_text = str((p.get("value") or p.get("currentPrice") or "")).lower()
        if re.search(r"/\s*s\b", low) or re.search(r"/\s*s\b", value_text) or " per s" in low or " per s" in value_text:
            per_second.append(val)
            continue
        if re.search(r"\b(\d+)s\b", low):
            m = re.search(r"\b(\d+)s\b", low)
            block_specs.append({"seconds": int(m.group(1)), "price": val, "label": label})
            continue
        if re.search(r"\b(\d+p|4k|pro|std)\b", low, re.I):
            variant_specs[label] = val
    if per_second:
        return {
            "billing_unit": "second",
            "billing_base_price": round(min(per_second), 6),
        }
    if block_specs:
        base = min(block_specs, key=lambda x: x["price"])
        return {
            "billing_unit": "block",
            "block_seconds": base["seconds"],
            "block_base_price": base["price"],
        }
    if variant_specs:
        return {
            "billing_unit": "variant",
            "variants": variant_specs,
            "base_variant": min(variant_specs, key=variant_specs.get),
        }
    return {}


def unit_from_price(label: str, value_text: str) -> str:
    """Infer billing unit from a TokenRouter price label and raw value text."""
    low = (label + " " + value_text).lower()
    if re.search(r"/\s*s\b", low) or " per s" in low:
        return "second"
    if "/ image" in low or "image" in low:
        return "image"
    if re.search(r"\b(\d+)s\b", label.lower()):
        return "block"
    return "request"


TOKEN_UNIT_RE = re.compile(r"/\s*1M\s*Tokens", re.IGNORECASE)


# TokenRouter labels some video models as "Pay as You Go" while other,
# equivalent models from the same provider are "Pay Per Request". Treat these
# as per-request for consistency with the rest of the platform.
OVERRIDE_PAY_PER_REQUEST = {
    "kling-3.0-turbo",
}


def variant_pricing(capability_type: str, billing_type: str, prices: list) -> dict:
    """Build a structured pricing object with per-variant prices.

    For image/video/audio models this captures resolution, duration or spec
    buckets (e.g. 'Output Pixels <= 236W', '2k', 'pro x 5s') that cannot be
    expressed by a single flat price. Pay-as-you-go models are token-priced,
    so they return an empty pricing block.
    """
    # Pay-as-you-go models are token-priced; their audio input/completion prices
    # are not per-request variants.
    if billing_type == "per_token" or capability_type not in ("image", "video", "audio") or not prices:
        return {}

    variants = []
    seen = set()
    units = set()
    block_seconds = None

    for p in prices:
        label = p.get("label", "").strip()
        val = parse_price(p.get("currentPrice", ""))
        if val <= 0 or not label:
            continue
        kind = label_kind(label)
        # Token-style prices are not variants.
        if kind in ("input", "output", "cache_hit", "cache_write"):
            continue
        # Audio-only labels on video models are forwarded but not priced separately yet.
        if kind == "audio" and capability_type == "video":
            continue
        key = f"{label}:{val}"
        if key in seen:
            continue
        seen.add(key)
        value_text = str((p.get("value") or p.get("currentPrice") or ""))
        unit = unit_from_price(label, value_text)
        units.add(unit)
        m = re.search(r"\b(\d+)s\b", label.lower())
        if m and block_seconds is None:
            block_seconds = int(m.group(1))
        variants.append({"label": label, "price": round(val, 6), "unit": unit})

    if not variants:
        return {}

    # Choose the dominant unit by model type: video/audio prefer per-second/block,
    # image prefers per-image, otherwise fall back to request.
    if capability_type in ("video", "audio"):
        preferred = ("second", "block", "request")
    elif capability_type == "image":
        preferred = ("image", "request")
    else:
        preferred = ("request",)
    billing_unit = "request"
    for u in preferred:
        if u in units:
            billing_unit = u
            break

    result = {
        "billing_type": billing_type,
        "billing_unit": billing_unit,
        "variants": variants,
    }
    if block_seconds:
        result["block_seconds"] = block_seconds
    return result


def build_variant_note(pricing_meta: dict) -> str:
    """Create a short human-readable summary of variant prices for the rule note."""
    if not pricing_meta or not pricing_meta.get("variants"):
        return "auto-imported from TokenRouter snapshot"
    suffix_map = {"second": "/s", "image": "/image", "block": "/block", "request": "/request"}
    parts = []
    for v in pricing_meta["variants"]:
        suffix = suffix_map.get(v.get("unit", "request"), "/request")
        parts.append(f"{v['label']}: ${v['price']:.6f}{suffix}")
    # Keep the note concise; truncate if there are many variants.
    summary = "; ".join(parts)
    if len(summary) > 500:
        summary = summary[:497] + "..."
    return summary


def build_values(m: dict):
    name = m["name"].strip()
    billing = m.get("billing", "Pay as You Go")
    cap_type = m.get("type", "Text")
    prices = m.get("prices", [])
    lines = m.get("lines", [])

    billing_type = "per_token" if billing == "Pay as You Go" else "per_request"
    # TokenRouter labels some equivalent models inconsistently; force a consistent
    # billing type so the platform treats them the same way.
    if name in OVERRIDE_PAY_PER_REQUEST:
        billing_type = "per_request"
    capability_type = model_type(cap_type, lines)

    # Component buckets
    comp = {
        "input": 0.0,
        "output": 0.0,
        "cache_hit": 0.0,
        "cache_write": 0.0,
        "reasoning": 0.0,
    }
    unit_prices = []
    # Extra price rows for PAYG media models (e.g. Image Output Price, Audio Input,
    # Audio Completion Price). These are shown in the model gallery alongside the
    # standard input/output/cache rows without changing billing math.
    extra_prices = []
    # PAYG media models whose only prices are resolution/duration/spec buckets
    # (e.g. kling-3.0-turbo 1080P/720P per-second). Stored separately so the
    # gallery can list them while billing stays flat/token-based.
    payg_variants = []

    for p in prices:
        label = p.get("label", "")
        val = parse_price(p.get("currentPrice", ""))
        kind = label_kind(label)
        if kind in ("input", "output", "cache_hit", "cache_write"):
            # Prefer the first/most canonical value; ignore zero fillers for non-free models.
            if comp[kind] == 0.0:
                comp[kind] = val
        elif kind == "audio":
            # Audio input/completion: treat as input price when no input price set.
            if comp["input"] == 0.0:
                comp["input"] = val
            # Preserve the original label for gallery display.
            if val > 0:
                extra_prices.append({"label": label, "price": round(val, 6), "unit": "M tokens"})
        elif kind == "image":
            # Image output/input: treat as output price when no output price set.
            if comp["output"] == 0.0:
                comp["output"] = val
            if val > 0:
                extra_prices.append({"label": label, "price": round(val, 6), "unit": "M tokens"})
        else:
            # Per-request / per-second / per-image unit prices.
            if val > 0:
                unit_prices.append(val)
                value_text = str(p.get("value") or p.get("currentPrice") or "")
                # PAYG media models may list per-second or per-image buckets under
                # non-component labels (e.g. Kling 3.0 Turbo "1080P"). These are
                # real media unit variants, not token prices.
                if (
                    billing_type == "per_token"
                    and capability_type in ("image", "video", "audio")
                    and not TOKEN_UNIT_RE.search(value_text)
                ):
                    payg_variants.append({
                        "label": label,
                        "price": round(val, 6),
                        "unit": unit_from_price(label, value_text),
                    })

    # For Pay-Per-Request image/video models the unit price becomes the upstream
    # fixed_price and the base of the downstream fixed_request.
    if billing_type == "per_request" and unit_prices:
        fixed_price = min(unit_prices)
        fixed_request = round(fixed_price + FIXED_PER_REQUEST, 6)
    else:
        # Token models carry no upstream fixed price; downstream always charges
        # the small per-request fee on top of token prices.
        fixed_price = 0.0
        fixed_request = round(FIXED_PER_REQUEST, 6)

    # For Pay-as-you-go video models whose only labels are per-second buckets,
    # expose the highest per-second rate as the output price so the public
    # gallery still shows a representative cost. Skip this when the rows are
    # real media unit variants, because those are billed via media-mode rules.
    if billing_type == "per_token" and comp["output"] == 0.0 and unit_prices and not payg_variants:
        comp["output"] = max(unit_prices)

    aimodels = {
        "name": name,
        "type": capability_type,
        "billing_type": billing_type,
        "provider": PROVIDER,
    }
    capabilities = {"type": capability_type}

    # Multi-type support: e.g. gemini-2.5-flash-image is Text+Image,
    # qwen3.5-omni-plus is Text+Audio+Video. The declared primary type is kept
    # in 'type'; additional modalities live in supported_types for filters and
    # relaxed health thresholds.
    all_types = supported_types_from_lines_and_prices(lines, prices)
    if capability_type not in all_types:
        all_types.insert(0, capability_type)
    if len(all_types) > 1:
        capabilities["supported_types"] = all_types

    # Video billing unit metadata for duration/spec-based pricing (legacy fields).
    if capability_type == "video":
        vmeta = video_billing_meta(capability_type, prices)
        if vmeta:
            capabilities.update(vmeta)

    # Structured variant pricing for image/video models with resolution, duration
    # or spec buckets. Stored under capabilities.pricing for admin UI display and
    # future billing engines.
    pricing_meta = variant_pricing(capability_type, billing_type, prices)
    if pricing_meta:
        capabilities["pricing"] = pricing_meta
    else:
        # Any model (text/media) with additional component prices (e.g. Image Output
        # Price, Audio Input, Audio Completion Price) should surface them in the
        # gallery without changing the flat billing mode. This catches models whose
        # primary type is "text" but whose price labels reveal audio/image
        # capabilities, such as qwen3.5-omni-plus.
        all_extra = extra_prices + payg_variants
        if all_extra:
            capabilities["pricing"] = {
                "billing_type": billing_type,
                "billing_unit": "",
                "extra_prices": all_extra,
            }

    # Human-readable variant summary for the pricing rule note so the admin
    # pricing list can surface resolution/duration/spec buckets without a schema
    # change to model_pricing_rules.
    variant_note = build_variant_note(pricing_meta)

    return {
        "product_id": name,
        "api_model_name": name,
        "billing_type": billing_type,
        "input_price": comp["input"],
        "output_price": comp["output"],
        "input_price_cache_hit": comp["cache_hit"],
        "input_price_cache_miss": comp["input"],
        "cache_write_price": comp["cache_write"],
        "reasoning_price": comp["reasoning"],
        "fixed_price": fixed_price,
        "fixed_request": fixed_request,
        "aimodels": aimodels,
        "capabilities": capabilities,
        "variant_note": variant_note,
    }


def escape_str(s) -> str:
    if s is None:
        return "NULL"
    return "'" + str(s).replace("'", "''") + "'"


def json_str(d) -> str:
    return escape_str(json.dumps(d, ensure_ascii=False))


def main():
    if not JSON_PATH.exists():
        print(f"TokenRouter model JSON not found: {JSON_PATH}", file=sys.stderr)
        sys.exit(1)

    with JSON_PATH.open("r", encoding="utf-8") as f:
        models = json.load(f)

    lines = ["BEGIN;", "SET LOCAL statement_timeout = '120s';", ""]
    lines.append(
        "-- Upsert TokenRouter models into service_models and model_pricing_rules"
    )

    for m in models:
        v = build_values(m)
        name = v["product_id"]
        api_name = v["api_model_name"]
        pricing_meta = v["capabilities"].get("pricing", {})
        variants = pricing_meta.get("variants", [])
        block_seconds = pricing_meta.get("block_seconds")
        if variants:
            billing_mode = "media"
            # Variant prices already represent the upstream unit cost; the
            # downstream fixed fee is only the small per-request margin.
            rule_fixed_request = round(FIXED_PER_REQUEST, 6)
            prices_json = json.dumps(
                {
                    "fixed_request": rule_fixed_request,
                    "variants": variants,
                },
                ensure_ascii=False,
            )
        else:
            billing_mode = "flat"
            prices_json = json.dumps(
                {
                    "input": round(v["input_price"], 6),
                    "output": round(v["output_price"], 6),
                    "cache_hit": round(v["input_price_cache_hit"], 6),
                    "cache_write": round(v["cache_write_price"], 6),
                    "reasoning": round(v["reasoning_price"], 6),
                    "fixed_request": round(v["fixed_request"], 6),
                },
                ensure_ascii=False,
            )

        # 1. Upsert service_models (provider+product_id unique).
        lines.append(
            f"""INSERT INTO service_models
(provider, product_id, api_model_name, status, billing_type,
 input_price, output_price, input_price_cache_hit, input_price_cache_miss,
 cache_write_price, reasoning_price, fixed_price,
 show_in_store, hidden, validation_status, currency,
 capabilities, aimodels_data, created_at, updated_at)
VALUES (
 {escape_str(PROVIDER)}, {escape_str(v['product_id'])}, {escape_str(api_name)}, 'active', {escape_str(v['billing_type'])},
 {v['input_price'] or 'NULL'}, {v['output_price'] or 'NULL'}, {v['input_price_cache_hit'] or 'NULL'}, {v['input_price_cache_miss'] or 'NULL'},
 {v['cache_write_price'] or 'NULL'}, {v['reasoning_price'] or 'NULL'}, {v['fixed_price'] or 'NULL'},
 true, false, 'valid', {escape_str(CURRENCY)},
 {json_str(v['capabilities'])}, {json_str(v['aimodels'])}, NOW(), NOW()
)
ON CONFLICT (provider, product_id) DO UPDATE SET
 status = EXCLUDED.status,
 billing_type = EXCLUDED.billing_type,
 input_price = EXCLUDED.input_price,
 output_price = EXCLUDED.output_price,
 input_price_cache_hit = EXCLUDED.input_price_cache_hit,
 input_price_cache_miss = EXCLUDED.input_price_cache_miss,
 cache_write_price = EXCLUDED.cache_write_price,
 reasoning_price = EXCLUDED.reasoning_price,
 fixed_price = EXCLUDED.fixed_price,
 show_in_store = EXCLUDED.show_in_store,
 hidden = EXCLUDED.hidden,
 validation_status = EXCLUDED.validation_status,
 currency = EXCLUDED.currency,
 capabilities = EXCLUDED.capabilities,
 aimodels_data = EXCLUDED.aimodels_data,
 updated_at = NOW();"""
        )

        # 2. Update active pricing rule if one exists for this provider+product_id,
        #    otherwise insert a new one.
        rule_name = f"TokenRouter: {name}"
        rule_note = v["variant_note"]
        lines.append(
            f"""WITH existing AS (
  SELECT id FROM model_pricing_rules
  WHERE provider = {escape_str(PROVIDER)}
    AND product_id = {escape_str(name)}
    AND status = 'active'
  LIMIT 1
)
UPDATE model_pricing_rules
SET prices = {escape_str(prices_json)}::jsonb,
    billing_mode = {escape_str(billing_mode)},
    block_seconds = {block_seconds if block_seconds else 'NULL'},
    currency = {escape_str(CURRENCY)},
    region = {escape_str(REGION)},
    note = {escape_str(rule_note)},
    updated_at = NOW()
FROM existing
WHERE model_pricing_rules.id = existing.id;"""
        )

        lines.append(
            f"""INSERT INTO model_pricing_rules
(provider, region, product_id, api_model_name, billing_mode,
 prices, priority, status, rule_name, note, currency, block_seconds, effective_from, created_at, updated_at)
SELECT
 {escape_str(PROVIDER)}, {escape_str(REGION)}, {escape_str(name)}, {escape_str(api_name)}, {escape_str(billing_mode)},
 {escape_str(prices_json)}::jsonb, 0, 'active', {escape_str(rule_name)},
 {escape_str(rule_note)}, {escape_str(CURRENCY)}, {block_seconds if block_seconds else 'NULL'}, NOW(), NOW(), NOW()
WHERE NOT EXISTS (
  SELECT 1 FROM model_pricing_rules
  WHERE provider = {escape_str(PROVIDER)}
    AND product_id = {escape_str(name)}
    AND status = 'active'
);"""
        )
        lines.append("")

    lines.append("COMMIT;")
    OUT_PATH.write_text("\n".join(lines), encoding="utf-8")
    print(f"Generated {OUT_PATH} with {len(models)} models.")


if __name__ == "__main__":
    main()

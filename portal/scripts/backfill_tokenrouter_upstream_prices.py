#!/usr/bin/env python3
"""Backfill missing upstream prices for active TokenRouter service_models
from the latest TokenRouter snapshot (models-20260817.json).

Generates /tmp/backfill_tokenrouter_upstream_prices.sql to be applied by psql.
"""
import json
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "ai-portal-go" / "scripts"))

from import_tokenrouter_models import build_values, JSON_PATH, PROVIDER  # noqa: E402


def main():
    if not JSON_PATH.exists():
        print(f"Snapshot not found: {JSON_PATH}", file=sys.stderr)
        sys.exit(1)

    with JSON_PATH.open("r", encoding="utf-8") as f:
        models = json.load(f)

    lines = ["BEGIN;", "SET LOCAL statement_timeout = '120s';", ""]
    lines.append("-- Backfill upstream prices for active TokenRouter service_models")

    updated = 0
    for m in models:
        v = build_values(m)
        name = v["product_id"]

        # Only target active TokenRouter models that are currently incomplete.
        # We use a subquery filter so the UPDATE is idempotent and safe to re-run.
        def price_sql(val):
            # 0 is a valid price (free models / no-cache), NULL means absent.
            return "NULL" if val is None else str(val)

        lines.append(
            f"""UPDATE service_models
SET
  input_price_cache_miss = {price_sql(v['input_price'])},
  input_price_cache_hit = {price_sql(v['input_price_cache_hit'])},
  output_price = {price_sql(v['output_price'])},
  cache_write_price = {price_sql(v['cache_write_price'])},
  reasoning_price = {price_sql(v['reasoning_price'])},
  fixed_price = {v['fixed_price'] if v['fixed_price'] else 'NULL'},
  updated_at = NOW()
WHERE provider = '{PROVIDER}'
  AND product_id = {repr(name)}
  AND status = 'active'
  AND (
    input_price_cache_miss IS NULL OR input_price_cache_miss = 0
    OR input_price_cache_hit IS NULL OR input_price_cache_hit = 0
    OR output_price IS NULL OR output_price = 0
    OR (billing_type = 'per_request' AND (fixed_price IS NULL OR fixed_price = 0))
  );"""
        )
        updated += 1

    lines.append("")
    lines.append("COMMIT;")

    out_path = Path("/tmp/backfill_tokenrouter_upstream_prices.sql")
    out_path.write_text("\n".join(lines), encoding="utf-8")
    print(f"Generated {out_path} with updates for {updated} snapshot models.")


if __name__ == "__main__":
    main()

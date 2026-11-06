ALTER TABLE model_pricing_rules
ADD COLUMN IF NOT EXISTS block_seconds INT DEFAULT 0;

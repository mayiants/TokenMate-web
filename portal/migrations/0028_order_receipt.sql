-- Receipt PDF for completed top-up orders (generated at payment time,
-- stored via the storage provider, downloaded from Top-Up Records).

ALTER TABLE orders ADD COLUMN IF NOT EXISTS receipt_key VARCHAR(512);

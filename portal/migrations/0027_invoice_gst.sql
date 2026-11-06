-- GST amount shown on auto-generated invoices (Singapore 9%, zero-rated
-- overseas). Display/tax-record only — billing itself still charges the
-- consumption amount until the tax pass-through is decided.

ALTER TABLE invoices ADD COLUMN IF NOT EXISTS gst_cents BIGINT NOT NULL DEFAULT 0;

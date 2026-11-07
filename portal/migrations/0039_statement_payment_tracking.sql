-- 0039: accounts-receivable tracking on monthly post-paid statements.
--
-- WHY: post-paid customers pay their monthly statement by bank transfer after
-- the fact. The portal could show "consumed" and "invoiced" but not "paid", so
-- there was no way to see outstanding receivables (issued-but-unpaid). These
-- columns record payment against a statement; payment_status is derived from
-- paid_micro vs credit_used_micro at read time. Prepaid needs no AR (the money
-- is already in the wallet), so this applies to post-paid statements only.
ALTER TABLE enterprise_statements ADD COLUMN IF NOT EXISTS paid_micro BIGINT NOT NULL DEFAULT 0;
ALTER TABLE enterprise_statements ADD COLUMN IF NOT EXISTS paid_at TIMESTAMP;
ALTER TABLE enterprise_statements ADD COLUMN IF NOT EXISTS payment_note TEXT;

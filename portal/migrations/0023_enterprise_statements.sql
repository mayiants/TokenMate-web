-- Enterprise monthly billing statements (P1 of
-- docs/enterprise-billing-prepost-paid-design-202611010445.md).
-- ResetMonthlyBilling snapshots the pre-reset counters into one row per
-- enterprise per month BEFORE zeroing them, so postpaid consumption survives
-- the monthly reset and becomes the invoiceable base (P2).
--
-- Idempotency: partial unique index on (enterprise_id, period); re-runs on
-- the same day ON CONFLICT DO NOTHING.

CREATE TABLE IF NOT EXISTS enterprise_statements (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    enterprise_id UUID NOT NULL REFERENCES enterprises(id),
    period VARCHAR(7) NOT NULL,                       -- 'YYYY-MM' statement month
    credit_used_micro BIGINT NOT NULL DEFAULT 0,      -- postpaid consumption (micro-USD)
    quota_used BIGINT NOT NULL DEFAULT 0,             -- token consumption
    prepaid_topup_micro BIGINT NOT NULL DEFAULT 0,    -- prepaid top-ups received during the month
    prepaid_balance_end_micro BIGINT NOT NULL DEFAULT 0, -- prepaid balance at statement time
    created_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_enterprise_statements_period
    ON enterprise_statements (enterprise_id, period);

CREATE INDEX IF NOT EXISTS idx_enterprise_statements_ent
    ON enterprise_statements (enterprise_id, created_at DESC);

-- Invoice pool tagging (P2): postpaid statements vs prepaid consumption.
-- Default 'postpaid' keeps existing rows and behaviour unchanged.
ALTER TABLE invoice_requests ADD COLUMN IF NOT EXISTS pool VARCHAR(10) NOT NULL DEFAULT 'postpaid';
ALTER TABLE invoice_requests ADD COLUMN IF NOT EXISTS period VARCHAR(7);  -- statement month for pool='postpaid'

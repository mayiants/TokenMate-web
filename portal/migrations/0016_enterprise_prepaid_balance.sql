-- Enterprise prepaid wallet: prepayments land in prepaid_balance (micro-USD).
-- Deduction order is prepaid first, then the monthly allowance
-- (credit_total / credit_used, 110% overflow buffer); refunds unwind in the
-- reverse order (LIFO). The balance may go negative via manual admin
-- adjustment, so no CHECK constraint. It carries across monthly resets —
-- ResetMonthlyBilling only zeroes credit_used / quota_used.

ALTER TABLE enterprises
    ADD COLUMN IF NOT EXISTS prepaid_balance BIGINT NOT NULL DEFAULT 0;

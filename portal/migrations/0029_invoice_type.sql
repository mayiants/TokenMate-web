-- Invoice type chosen by the requester: general (default, e-invoice) or
-- special (China VAT special fapiao — requires a tax number on the header).

ALTER TABLE invoice_requests ADD COLUMN IF NOT EXISTS invoice_type VARCHAR(20) NOT NULL DEFAULT 'general';

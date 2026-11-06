-- Invoice PDF downloads (reuses the internal/storage provider that
-- enterprise application documents already use).
-- request_id links the issued invoice row back to its invoice_requests row
-- so the download endpoint can authorize by request ownership.

ALTER TABLE invoices ADD COLUMN IF NOT EXISTS request_id UUID;
ALTER TABLE invoices ADD COLUMN IF NOT EXISTS storage_key VARCHAR(512);

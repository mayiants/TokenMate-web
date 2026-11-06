-- Invoices issued from an admin-uploaded file (e.g. a China VAT fapiao
-- produced by finance) are tagged so the UI can tell auto-generated PDFs
-- apart from uploaded ones.

ALTER TABLE invoices ADD COLUMN IF NOT EXISTS invoice_source VARCHAR(10) NOT NULL DEFAULT 'auto';

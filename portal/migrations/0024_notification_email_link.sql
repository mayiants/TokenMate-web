-- Notification emails gain the deep link and recipient locale so the
-- templated email (pkg/mail emailLayout + buttonBlock) can render a
-- branded HTML message with a primary action button instead of a bare
-- <pre> text block.

ALTER TABLE email_notifications ADD COLUMN IF NOT EXISTS link VARCHAR(512);
ALTER TABLE email_notifications ADD COLUMN IF NOT EXISTS locale VARCHAR(10) NOT NULL DEFAULT 'en';

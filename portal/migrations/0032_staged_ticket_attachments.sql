-- 0032: staged ticket attachments.
-- The ticket UI uploads attachments immediately into a per-user staging area
-- (message_id NULL) and links them to their message when the comment is
-- submitted. See tickets.go stageAttachments/unstageAttachment.

ALTER TABLE support_ticket_attachments ALTER COLUMN message_id DROP NOT NULL;
ALTER TABLE support_ticket_attachments ADD COLUMN IF NOT EXISTS staged_at TIMESTAMPTZ;
ALTER TABLE support_ticket_attachments ADD COLUMN IF NOT EXISTS staged_by UUID;
CREATE INDEX IF NOT EXISTS idx_ticket_attachments_staged
  ON support_ticket_attachments(staged_by)
  WHERE message_id IS NULL AND staged_at IS NOT NULL;

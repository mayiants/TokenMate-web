-- Support ticket system (control-plane only).
-- See docs/support-ticket-system-design-202611042000.md

CREATE TABLE IF NOT EXISTS support_tickets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    ticket_no BIGSERIAL,
    user_id UUID NOT NULL REFERENCES users(id),
    enterprise_id UUID REFERENCES enterprises(id),
    subject VARCHAR(255) NOT NULL,
    category VARCHAR(40) NOT NULL DEFAULT 'general',
    priority VARCHAR(20) NOT NULL DEFAULT 'normal',
    status VARCHAR(20) NOT NULL DEFAULT 'open',
    assignee_id UUID REFERENCES users(id),
    last_reply_role VARCHAR(10),
    last_reply_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_support_tickets_user ON support_tickets(user_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_support_tickets_status ON support_tickets(status) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_support_tickets_enterprise ON support_tickets(enterprise_id) WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS support_ticket_messages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    ticket_id UUID NOT NULL REFERENCES support_tickets(id) ON DELETE CASCADE,
    author_id UUID NOT NULL REFERENCES users(id),
    author_role VARCHAR(10) NOT NULL,
    body TEXT NOT NULL,
    internal_note BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_ticket_messages_ticket ON support_ticket_messages(ticket_id, created_at);

CREATE TABLE IF NOT EXISTS support_ticket_attachments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    message_id UUID NOT NULL REFERENCES support_ticket_messages(id) ON DELETE CASCADE,
    storage_key TEXT NOT NULL,
    filename VARCHAR(255) NOT NULL,
    size_bytes BIGINT,
    mime_type VARCHAR(100),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_ticket_attachments_message ON support_ticket_attachments(message_id);

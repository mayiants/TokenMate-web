-- IM webhook channels (phase 3): admins self-configure Feishu/DingTalk/WeCom
-- group-bot webhooks that receive high-priority system-admin notifications.
-- See docs/notification-system-proposal-202610312238.md §6.

CREATE TABLE IF NOT EXISTS notification_channels (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id),
    type VARCHAR(20) NOT NULL,              -- feishu | dingtalk | wecom
    webhook_url TEXT NOT NULL,
    secret TEXT,                            -- HMAC signing secret (feishu/dingtalk); NULL for wecom
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    filters JSONB,                          -- notification-type whitelist; NULL/empty = all admin high-priority
    last_test_at TIMESTAMP,
    last_test_status VARCHAR(20),           -- ok | failed
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_notification_channels_user ON notification_channels (user_id);

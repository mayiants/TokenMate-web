-- Notification center: unified in-app inbox + per-user delivery preferences +
-- background-job heartbeat registry. See docs/notification-system-proposal-202610312238.md.
-- email_notifications already exists (outbound email queue for the delivery worker).

CREATE TABLE IF NOT EXISTS notifications (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id),
    type VARCHAR(50) NOT NULL,
    scope VARCHAR(20) NOT NULL DEFAULT 'user',
    priority VARCHAR(10) NOT NULL DEFAULT 'normal',
    title VARCHAR(255) NOT NULL,
    body TEXT,
    link VARCHAR(255),
    ref_type VARCHAR(50) NOT NULL DEFAULT '',
    ref_id VARCHAR(100) NOT NULL DEFAULT '',
    read_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- Fast unread-count lookups for the topbar badge.
CREATE INDEX IF NOT EXISTS idx_notifications_user_unread ON notifications (user_id) WHERE read_at IS NULL;

-- At most one UNREAD notification per (user, type, ref): duplicate events while
-- unread collapse instead of spamming; acknowledged/read allows re-alerting.
CREATE UNIQUE INDEX IF NOT EXISTS uq_notification_dedup ON notifications (user_id, type, ref_type, ref_id) WHERE read_at IS NULL;

CREATE TABLE IF NOT EXISTS notification_preferences (
    user_id UUID NOT NULL REFERENCES users(id),
    ntype VARCHAR(50) NOT NULL,          -- '*' = global default row
    channel VARCHAR(20) NOT NULL,        -- email | webhook
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, ntype, channel)
);

-- Heartbeat for cmd/server/background.go tickers. The inspector loop notifies
-- system admins when a job misses its window or reports last_status='failed';
-- last_notified_at dedups re-alerts until the job recovers.
CREATE TABLE IF NOT EXISTS job_runs (
    job_name VARCHAR(100) PRIMARY KEY,
    interval_min INTEGER NOT NULL,
    last_run_at TIMESTAMP,
    last_status VARCHAR(20),
    last_error TEXT,
    last_notified_at TIMESTAMP
);

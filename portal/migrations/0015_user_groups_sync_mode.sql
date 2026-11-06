-- Enterprise group follow-mode: a 'follow' enterprise group has its models
-- materialized from its parent platform group, so it never drifts behind
-- platform-side model maintenance. Propagation happens on parent update and
-- via the admin "sync follow groups" action; the column only marks intent.

ALTER TABLE user_groups
    ADD COLUMN IF NOT EXISTS sync_mode VARCHAR(20) NOT NULL DEFAULT 'manual'
    CHECK (sync_mode IN ('manual', 'follow'));

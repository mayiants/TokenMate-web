-- Add allowed_enterprise_ids to user_groups for enterprise-specific platform group visibility.
-- When non-empty, only the listed enterprises can see and use this platform group.
-- An empty array (default) means the group is visible to all enterprises.
ALTER TABLE user_groups ADD COLUMN IF NOT EXISTS allowed_enterprise_ids JSONB DEFAULT '[]';

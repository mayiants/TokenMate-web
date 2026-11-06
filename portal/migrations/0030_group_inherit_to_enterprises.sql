-- Add inherit_to_enterprises flag to user_groups.
-- When TRUE on a platform enterprise template group (group_type='enterprise',
-- enterprise_id IS NULL), every enterprise's members may create enterprise keys
-- directly against this group without the enterprise admin creating a local
-- copy. Mutually exclusive with allowed_enterprise_ids.
ALTER TABLE user_groups ADD COLUMN IF NOT EXISTS inherit_to_enterprises BOOLEAN NOT NULL DEFAULT FALSE;

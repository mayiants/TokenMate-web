-- Backfill legacy enterprise_member_role_log rows into enterprise_audit_log
-- (category='role') so the unified audit timeline also contains role changes
-- that predate dual-writing (previously only visible in the legacy Role
-- Change Log card). Idempotent via NOT EXISTS on
-- (enterprise_id, category, user_id, created_at).
INSERT INTO enterprise_audit_log
    (enterprise_id, category, change_type, field_name, old_value, new_value, delta, reason, changed_by, user_id, created_at)
SELECT r.enterprise_id,
       'role',
       'member_role_changed',
       'role',
       CASE WHEN r.old_role = 'admin' THEN 1 ELSE 0 END,
       CASE WHEN r.new_role = 'admin' THEN 1 ELSE 0 END,
       0,
       COALESCE(r.reason, 'Member role changed'),
       r.changed_by,
       r.user_id,
       r.created_at
FROM enterprise_member_role_log r
WHERE NOT EXISTS (
    SELECT 1 FROM enterprise_audit_log a
    WHERE a.enterprise_id = r.enterprise_id
      AND a.category = 'role'
      AND a.user_id IS NOT DISTINCT FROM r.user_id
      AND a.created_at = r.created_at
);

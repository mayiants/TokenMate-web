-- 0038: platform default retention windows move from 30 to 90 days.
--
-- WHY: a tenant's window is stamped at write time and enterprises default to 90
-- days; keeping the platform default (keys without an enterprise) and the log
-- window at 30 meant the same data was kept for different periods depending on
-- who owned the key. One uniform 90-day default also avoids 30d -> 90d edits
-- later.
--
--   retention_default_days  payload retention for keys without an enterprise
--   log_retention_days      window used by the sensitive_check_logs purge
--
-- Only rows still at the old default ('30') are moved: a value an admin set to
-- anything else is left alone. Re-running is a no-op.
UPDATE system_settings
   SET value = '90', updated_at = NOW()
 WHERE key IN ('retention_default_days', 'log_retention_days')
   AND value = '30';

-- 0002_seed_managed_by.sql
-- Adds a `managed_by` marker to the two tables whose rows are created by
-- SeedDefaultData but later edited by admins:
--   - subscription_plans (e.g. default plan prices may change between releases)
--   - system_settings  (e.g. default values may change between releases)
--
-- managed_by='seed'  : the row is owned by SeedDefaultData and may be updated
--                      by future seed runs (e.g. a price change in a release).
-- managed_by='admin' : the row was edited through the admin UI / API and must
--                      NOT be overwritten by subsequent seed runs.
--
-- The column DEFAULT already backfills every existing row to 'seed', which is
-- correct: every row present today was created by SeedDefaultData.

ALTER TABLE subscription_plans
    ADD COLUMN IF NOT EXISTS managed_by VARCHAR(20) NOT NULL DEFAULT 'seed';

ALTER TABLE system_settings
    ADD COLUMN IF NOT EXISTS managed_by VARCHAR(20) NOT NULL DEFAULT 'seed';

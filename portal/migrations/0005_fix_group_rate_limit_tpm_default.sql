-- Fix existing user_groups.rate_limit_tpm from the old DEFAULT 100000
-- to NULL so COALESCE in apikeys_crud.go falls through to the
-- system setting (default_user_tpm) instead of hard-capping at 100K.
UPDATE user_groups SET rate_limit_tpm = NULL WHERE rate_limit_tpm = 100000;

-- Also set rate_limit_rpm to NULL for groups created via the API with
-- value 0 (Go int zero value when omitted from JSON), so the COALESCE
-- fallback (system setting or code default 120) is used instead.
UPDATE user_groups SET rate_limit_rpm = NULL WHERE rate_limit_rpm = 0;

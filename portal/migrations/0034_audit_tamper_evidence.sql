-- 0034: tamper evidence for audit data + delete guards for ledgers.
--
-- WHY: audit_logs / enterprise_audit_log were plain tables; any DB role (or a
-- compromised super_admin) could UPDATE/DELETE history without a trace, and
-- ad-hoc SQL has already hard-deleted rows elsewhere (8,301 usage_records point
-- at api_keys that no longer exist). This migration makes the audit tables a
-- hash chain, adds DB-level append-only guards, an HMAC-signed anchor table, a
-- delete guard on the money ledgers and a tombstone log for hard-deleted keys.
--
-- THIS FILE IS EMBEDDED INTO THE BINARY (migrations/embed.go) AND ALSO EXECUTED
-- BY InitSchema, so fresh databases get the identical objects. Every statement is
-- idempotent; re-running is a no-op once the chain is backfilled.
--
-- ============================================================================
-- CANONICAL SERIALIZATION (chain version 1) — keep in sync with pkg/auditchain
-- ============================================================================
-- row_hash = lower-hex( sha256( UTF8(
--     'audit-chain-v1|' || <table name> || '|' || prev_hash || '|' || encode(fields)
-- ) ) )
--
-- prev_hash  = row_hash of the previous row (ordered by chain_seq) or, for the
--              first row of a table, the genesis value
--              lower-hex(sha256(UTF8('audit-chain-v1-genesis|' || <table name>))).
-- encode(fields) = concatenation, in the FIXED order below, of one token per
--              field: SQL NULL -> 'N;'   otherwise -> 'V' || <byte length of the
--              UTF-8 text> || ':' || <text> || ';'. Length prefixes make the
--              encoding injective (no delimiter-collision attacks).
-- Field text forms: every column is rendered with col::text (uuid, bigint,
--              varchar, jsonb::text which is the normalised jsonb output),
--              except timestamps: to_char(col, 'YYYY-MM-DD"T"HH24:MI:SS.US').
--
-- audit_logs fields, in order:
--   chain_version, chain_seq, id, log_id, user_id, category, action, level,
--   message, details, target_type, target_id, old_value, new_value, created_at
-- enterprise_audit_log fields, in order:
--   chain_version, chain_seq, id, enterprise_id, action, amount, balance_before,
--   balance_after, description, created_by, category, change_type, changed_by,
--   delta, field_name, reason, user_id, old_value, new_value, old_value_str,
--   new_value_str, target_type, target_id, created_at
--
-- EXCLUDED on purpose (mutable by design): user_email, ip_address, user_agent
-- (GDPR anonymisation rewrites them), deleted_at, deleted_by, delete_reason
-- (soft delete / restore), and any column added after chain version 1.
--
-- chain_version: 0 = row existed before this migration and was chained
--   retroactively (its hash proves nothing about the period before the
--   migration); 1 = row hashed at INSERT time by the trigger.
-- chain_seq: gap-free 1..N per table, assigned under a transaction-level
--   advisory lock so the chain is strictly serial and survives concurrent writers.
--
-- GUCs that gate destructive operations (set with SET LOCAL inside a txn):
--   app.audit_purge = 'on'          retention job / DBA purge of audit tables
--   app.allow_ledger_delete = 'on'  intentional DELETE on usage_records,
--                                   balance_transactions, orders
--   app.audit_chain_backfill = 'on' used only by the backfill block below
-- ============================================================================

-- 1. Columns -----------------------------------------------------------------
ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS chain_seq BIGINT;
ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS chain_version SMALLINT;
ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS prev_hash TEXT;
ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS row_hash TEXT;
ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS delete_reason TEXT;
ALTER TABLE enterprise_audit_log ADD COLUMN IF NOT EXISTS chain_seq BIGINT;
ALTER TABLE enterprise_audit_log ADD COLUMN IF NOT EXISTS chain_version SMALLINT;
ALTER TABLE enterprise_audit_log ADD COLUMN IF NOT EXISTS prev_hash TEXT;
ALTER TABLE enterprise_audit_log ADD COLUMN IF NOT EXISTS row_hash TEXT;

-- The BEFORE INSERT trigger reads the head with ORDER BY chain_seq DESC LIMIT 1.
CREATE UNIQUE INDEX IF NOT EXISTS uq_audit_logs_chain_seq ON audit_logs(chain_seq);
CREATE UNIQUE INDEX IF NOT EXISTS uq_enterprise_audit_log_chain_seq ON enterprise_audit_log(chain_seq);

-- 2. Anchors + tombstones ----------------------------------------------------
CREATE TABLE IF NOT EXISTS audit_anchors (
    id BIGSERIAL PRIMARY KEY,
    anchor_date DATE NOT NULL,
    table_name TEXT NOT NULL,
    row_count BIGINT NOT NULL DEFAULT 0,
    last_id TEXT,
    last_seq BIGINT,
    head_hash TEXT,
    usage_rows BIGINT,
    usage_cost_sum BIGINT,
    usage_digest TEXT,
    hmac TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (anchor_date, table_name)
);

CREATE TABLE IF NOT EXISTS api_key_tombstones (
    id BIGSERIAL PRIMARY KEY,
    key_id UUID NOT NULL,
    user_id UUID,
    enterprise_id UUID,
    name TEXT,
    key_prefix TEXT,
    key_hash_prefix TEXT,
    created_at TIMESTAMP,
    deleted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_by TEXT,
    reason TEXT
);
CREATE INDEX IF NOT EXISTS idx_api_key_tombstones_key_id ON api_key_tombstones(key_id);

-- 3. Hash primitives ---------------------------------------------------------
CREATE OR REPLACE FUNCTION audit_chain_encode(vals text[]) RETURNS text
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE
    out_s text := '';
    v text;
    i int;
BEGIN
    FOR i IN 1 .. COALESCE(array_length(vals, 1), 0) LOOP
        v := vals[i];
        IF v IS NULL THEN
            out_s := out_s || 'N;';
        ELSE
            out_s := out_s || 'V' || octet_length(convert_to(v, 'UTF8'))::text || ':' || v || ';';
        END IF;
    END LOOP;
    RETURN out_s;
END
$$;

CREATE OR REPLACE FUNCTION audit_chain_genesis(tbl text) RETURNS text
LANGUAGE sql IMMUTABLE AS $$
    SELECT encode(sha256(convert_to('audit-chain-v1-genesis|' || tbl, 'UTF8')), 'hex')
$$;

CREATE OR REPLACE FUNCTION audit_chain_hash(tbl text, prev text, vals text[]) RETURNS text
LANGUAGE sql IMMUTABLE AS $$
    SELECT encode(sha256(convert_to('audit-chain-v1|' || tbl || '|' || prev || '|' || audit_chain_encode(vals), 'UTF8')), 'hex')
$$;

CREATE OR REPLACE FUNCTION audit_logs_chain_fields(r audit_logs) RETURNS text[]
LANGUAGE sql STABLE AS $$
    SELECT ARRAY[
        r.chain_version::text, r.chain_seq::text, r.id::text, r.log_id::text, r.user_id::text,
        r.category::text, r.action::text, r.level::text, r.message::text, r.details::text,
        r.target_type::text, r.target_id::text, r.old_value::text, r.new_value::text,
        to_char(r.created_at, 'YYYY-MM-DD"T"HH24:MI:SS.US')
    ]
$$;

CREATE OR REPLACE FUNCTION enterprise_audit_log_chain_fields(r enterprise_audit_log) RETURNS text[]
LANGUAGE sql STABLE AS $$
    SELECT ARRAY[
        r.chain_version::text, r.chain_seq::text, r.id::text, r.enterprise_id::text, r.action::text,
        r.amount::text, r.balance_before::text, r.balance_after::text, r.description::text,
        r.created_by::text, r.category::text, r.change_type::text, r.changed_by::text, r.delta::text,
        r.field_name::text, r.reason::text, r.user_id::text, r.old_value::text, r.new_value::text,
        r.old_value_str::text, r.new_value_str::text, r.target_type::text, r.target_id::text,
        to_char(r.created_at, 'YYYY-MM-DD"T"HH24:MI:SS.US')
    ]
$$;

-- 4. BEFORE INSERT: serialise, link and hash ---------------------------------
CREATE OR REPLACE FUNCTION audit_logs_chain_before_insert() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    prev_seq bigint;
    prev_h text;
BEGIN
    -- Transaction-level lock: held until COMMIT/ROLLBACK, so chain_seq stays
    -- gap-free and commit-ordered even with concurrent writers.
    PERFORM pg_advisory_xact_lock(hashtextextended('audit-chain:audit_logs', 0));
    SELECT chain_seq, row_hash INTO prev_seq, prev_h
      FROM audit_logs WHERE chain_seq IS NOT NULL ORDER BY chain_seq DESC LIMIT 1;
    IF prev_h IS NULL THEN
        prev_seq := 0;
        prev_h := audit_chain_genesis('audit_logs');
    END IF;
    NEW.chain_version := 1;
    NEW.chain_seq := prev_seq + 1;
    NEW.prev_hash := prev_h;
    NEW.row_hash := audit_chain_hash('audit_logs', prev_h, audit_logs_chain_fields(NEW));
    RETURN NEW;
END
$$;

CREATE OR REPLACE FUNCTION enterprise_audit_log_chain_before_insert() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    prev_seq bigint;
    prev_h text;
BEGIN
    PERFORM pg_advisory_xact_lock(hashtextextended('audit-chain:enterprise_audit_log', 0));
    SELECT chain_seq, row_hash INTO prev_seq, prev_h
      FROM enterprise_audit_log WHERE chain_seq IS NOT NULL ORDER BY chain_seq DESC LIMIT 1;
    IF prev_h IS NULL THEN
        prev_seq := 0;
        prev_h := audit_chain_genesis('enterprise_audit_log');
    END IF;
    NEW.chain_version := 1;
    NEW.chain_seq := prev_seq + 1;
    NEW.prev_hash := prev_h;
    NEW.row_hash := audit_chain_hash('enterprise_audit_log', prev_h, enterprise_audit_log_chain_fields(NEW));
    RETURN NEW;
END
$$;

-- 5. BEFORE UPDATE: hashed columns are immutable -----------------------------
CREATE OR REPLACE FUNCTION audit_logs_chain_before_update() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    -- Backfill of rows that pre-date the chain (row_hash still NULL).
    IF OLD.row_hash IS NULL AND current_setting('app.audit_chain_backfill', true) = 'on' THEN
        RETURN NEW;
    END IF;
    IF NEW.prev_hash IS DISTINCT FROM OLD.prev_hash
       OR NEW.row_hash IS DISTINCT FROM OLD.row_hash
       OR audit_logs_chain_fields(NEW) IS DISTINCT FROM audit_logs_chain_fields(OLD) THEN
        RAISE EXCEPTION 'audit_logs is tamper-evident: hashed columns are immutable'
            USING ERRCODE = '23000';
    END IF;
    -- Soft delete must be explained; user_email/ip_address/user_agent stay
    -- writable for GDPR anonymisation.
    IF NEW.deleted_at IS NOT NULL AND OLD.deleted_at IS NULL
       AND COALESCE(btrim(NEW.delete_reason), '') = '' THEN
        RAISE EXCEPTION 'audit_logs soft delete requires a non-empty delete_reason'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END
$$;

CREATE OR REPLACE FUNCTION enterprise_audit_log_chain_before_update() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.row_hash IS NULL AND current_setting('app.audit_chain_backfill', true) = 'on' THEN
        RETURN NEW;
    END IF;
    IF NEW.prev_hash IS DISTINCT FROM OLD.prev_hash
       OR NEW.row_hash IS DISTINCT FROM OLD.row_hash
       OR enterprise_audit_log_chain_fields(NEW) IS DISTINCT FROM enterprise_audit_log_chain_fields(OLD) THEN
        RAISE EXCEPTION 'enterprise_audit_log is tamper-evident: hashed columns are immutable'
            USING ERRCODE = '23000';
    END IF;
    RETURN NEW;
END
$$;

-- 6. DELETE / TRUNCATE / anchor guards ---------------------------------------
CREATE OR REPLACE FUNCTION audit_chain_guard_delete() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF current_setting('app.audit_purge', true) = 'on' THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION '% is append-only: DELETE requires SET LOCAL app.audit_purge = ''on'' (retention job only)', TG_TABLE_NAME
        USING ERRCODE = '23000';
END
$$;

CREATE OR REPLACE FUNCTION audit_chain_guard_truncate() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF current_setting('app.audit_purge', true) = 'on' THEN
        RETURN NULL;
    END IF;
    RAISE EXCEPTION '% is append-only: TRUNCATE requires SET LOCAL app.audit_purge = ''on''', TG_TABLE_NAME
        USING ERRCODE = '23000';
END
$$;

CREATE OR REPLACE FUNCTION audit_chain_guard_update() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION '% is insert-only: rows can never be updated', TG_TABLE_NAME
        USING ERRCODE = '23000';
END
$$;

CREATE OR REPLACE FUNCTION ledger_guard_delete() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF current_setting('app.allow_ledger_delete', true) = 'on' THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION '% is a financial ledger: DELETE requires SET LOCAL app.allow_ledger_delete = ''on''', TG_TABLE_NAME
        USING ERRCODE = '23000';
END
$$;

-- 7. Tombstone for hard-deleted API keys -------------------------------------
-- deleted_by / reason come from optional session GUCs (app.deleted_by,
-- app.delete_reason); otherwise the DB session user is recorded so ad-hoc SQL
-- deletes remain attributable.
CREATE OR REPLACE FUNCTION api_keys_tombstone() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO api_key_tombstones
        (key_id, user_id, enterprise_id, name, key_prefix, key_hash_prefix, created_at, deleted_by, reason)
    VALUES
        (OLD.id, OLD.user_id, OLD.enterprise_id, OLD.name, OLD.key_prefix, left(OLD.key_hash, 8), OLD.created_at,
         COALESCE(NULLIF(current_setting('app.deleted_by', true), ''), session_user::text),
         NULLIF(current_setting('app.delete_reason', true), ''));
    RETURN NULL;
END
$$;

-- 8. Backfill existing rows (chain_version = 0), in id order -----------------
-- Runs once; the guard `row_hash IS NULL` makes re-runs a no-op. Safe for
-- ~100k rows (two UPDATEs per row, single transaction, advisory-locked).
DO $$
DECLARE
    rec audit_logs%ROWTYPE;
    r record;
    prev_seq bigint;
    prev_h text;
BEGIN
    IF NOT EXISTS (SELECT 1 FROM audit_logs WHERE row_hash IS NULL) THEN
        RETURN;
    END IF;
    PERFORM pg_advisory_xact_lock(hashtextextended('audit-chain:audit_logs', 0));
    PERFORM set_config('app.audit_chain_backfill', 'on', true);
    SELECT chain_seq, row_hash INTO prev_seq, prev_h
      FROM audit_logs WHERE chain_seq IS NOT NULL ORDER BY chain_seq DESC LIMIT 1;
    IF prev_h IS NULL THEN
        prev_seq := 0;
        prev_h := audit_chain_genesis('audit_logs');
    END IF;
    FOR r IN SELECT id FROM audit_logs WHERE row_hash IS NULL ORDER BY id LOOP
        prev_seq := prev_seq + 1;
        UPDATE audit_logs SET chain_version = 0, chain_seq = prev_seq, prev_hash = prev_h
         WHERE id = r.id RETURNING * INTO rec;
        prev_h := audit_chain_hash('audit_logs', prev_h, audit_logs_chain_fields(rec));
        UPDATE audit_logs SET row_hash = prev_h WHERE id = r.id;
    END LOOP;
    PERFORM set_config('app.audit_chain_backfill', 'off', true);
END
$$;

DO $$
DECLARE
    rec enterprise_audit_log%ROWTYPE;
    r record;
    prev_seq bigint;
    prev_h text;
BEGIN
    IF NOT EXISTS (SELECT 1 FROM enterprise_audit_log WHERE row_hash IS NULL) THEN
        RETURN;
    END IF;
    PERFORM pg_advisory_xact_lock(hashtextextended('audit-chain:enterprise_audit_log', 0));
    PERFORM set_config('app.audit_chain_backfill', 'on', true);
    SELECT chain_seq, row_hash INTO prev_seq, prev_h
      FROM enterprise_audit_log WHERE chain_seq IS NOT NULL ORDER BY chain_seq DESC LIMIT 1;
    IF prev_h IS NULL THEN
        prev_seq := 0;
        prev_h := audit_chain_genesis('enterprise_audit_log');
    END IF;
    -- id is a UUID, so order by created_at (then id) to keep a stable history.
    FOR r IN SELECT id FROM enterprise_audit_log WHERE row_hash IS NULL ORDER BY created_at, id LOOP
        prev_seq := prev_seq + 1;
        UPDATE enterprise_audit_log SET chain_version = 0, chain_seq = prev_seq, prev_hash = prev_h
         WHERE id = r.id RETURNING * INTO rec;
        prev_h := audit_chain_hash('enterprise_audit_log', prev_h, enterprise_audit_log_chain_fields(rec));
        UPDATE enterprise_audit_log SET row_hash = prev_h WHERE id = r.id;
    END LOOP;
    PERFORM set_config('app.audit_chain_backfill', 'off', true);
END
$$;

-- 9. Triggers (created after the backfill; guarded so re-runs take no lock) ---
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_audit_logs_chain_insert' AND tgrelid = 'audit_logs'::regclass) THEN
        CREATE TRIGGER trg_audit_logs_chain_insert BEFORE INSERT ON audit_logs
            FOR EACH ROW EXECUTE FUNCTION audit_logs_chain_before_insert();
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_audit_logs_chain_update' AND tgrelid = 'audit_logs'::regclass) THEN
        CREATE TRIGGER trg_audit_logs_chain_update BEFORE UPDATE ON audit_logs
            FOR EACH ROW EXECUTE FUNCTION audit_logs_chain_before_update();
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_audit_logs_guard_delete' AND tgrelid = 'audit_logs'::regclass) THEN
        CREATE TRIGGER trg_audit_logs_guard_delete BEFORE DELETE ON audit_logs
            FOR EACH ROW EXECUTE FUNCTION audit_chain_guard_delete();
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_audit_logs_guard_truncate' AND tgrelid = 'audit_logs'::regclass) THEN
        CREATE TRIGGER trg_audit_logs_guard_truncate BEFORE TRUNCATE ON audit_logs
            FOR EACH STATEMENT EXECUTE FUNCTION audit_chain_guard_truncate();
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_enterprise_audit_log_chain_insert' AND tgrelid = 'enterprise_audit_log'::regclass) THEN
        CREATE TRIGGER trg_enterprise_audit_log_chain_insert BEFORE INSERT ON enterprise_audit_log
            FOR EACH ROW EXECUTE FUNCTION enterprise_audit_log_chain_before_insert();
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_enterprise_audit_log_chain_update' AND tgrelid = 'enterprise_audit_log'::regclass) THEN
        CREATE TRIGGER trg_enterprise_audit_log_chain_update BEFORE UPDATE ON enterprise_audit_log
            FOR EACH ROW EXECUTE FUNCTION enterprise_audit_log_chain_before_update();
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_enterprise_audit_log_guard_delete' AND tgrelid = 'enterprise_audit_log'::regclass) THEN
        CREATE TRIGGER trg_enterprise_audit_log_guard_delete BEFORE DELETE ON enterprise_audit_log
            FOR EACH ROW EXECUTE FUNCTION audit_chain_guard_delete();
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_enterprise_audit_log_guard_truncate' AND tgrelid = 'enterprise_audit_log'::regclass) THEN
        CREATE TRIGGER trg_enterprise_audit_log_guard_truncate BEFORE TRUNCATE ON enterprise_audit_log
            FOR EACH STATEMENT EXECUTE FUNCTION audit_chain_guard_truncate();
    END IF;

    -- Anchors and tombstones are insert-only (DELETE only under app.audit_purge).
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_audit_anchors_guard_update' AND tgrelid = 'audit_anchors'::regclass) THEN
        CREATE TRIGGER trg_audit_anchors_guard_update BEFORE UPDATE ON audit_anchors
            FOR EACH ROW EXECUTE FUNCTION audit_chain_guard_update();
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_audit_anchors_guard_delete' AND tgrelid = 'audit_anchors'::regclass) THEN
        CREATE TRIGGER trg_audit_anchors_guard_delete BEFORE DELETE ON audit_anchors
            FOR EACH ROW EXECUTE FUNCTION audit_chain_guard_delete();
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_audit_anchors_guard_truncate' AND tgrelid = 'audit_anchors'::regclass) THEN
        CREATE TRIGGER trg_audit_anchors_guard_truncate BEFORE TRUNCATE ON audit_anchors
            FOR EACH STATEMENT EXECUTE FUNCTION audit_chain_guard_truncate();
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_api_key_tombstones_guard_update' AND tgrelid = 'api_key_tombstones'::regclass) THEN
        CREATE TRIGGER trg_api_key_tombstones_guard_update BEFORE UPDATE ON api_key_tombstones
            FOR EACH ROW EXECUTE FUNCTION audit_chain_guard_update();
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_api_key_tombstones_guard_delete' AND tgrelid = 'api_key_tombstones'::regclass) THEN
        CREATE TRIGGER trg_api_key_tombstones_guard_delete BEFORE DELETE ON api_key_tombstones
            FOR EACH ROW EXECUTE FUNCTION audit_chain_guard_delete();
    END IF;

    -- Ledger delete guards. usage_records is partitioned: PG13+ clones the row
    -- trigger onto every existing and future partition.
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_usage_records_guard_delete' AND tgrelid = 'usage_records'::regclass) THEN
        CREATE TRIGGER trg_usage_records_guard_delete BEFORE DELETE ON usage_records
            FOR EACH ROW EXECUTE FUNCTION ledger_guard_delete();
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_balance_transactions_guard_delete' AND tgrelid = 'balance_transactions'::regclass) THEN
        CREATE TRIGGER trg_balance_transactions_guard_delete BEFORE DELETE ON balance_transactions
            FOR EACH ROW EXECUTE FUNCTION ledger_guard_delete();
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_orders_guard_delete' AND tgrelid = 'orders'::regclass) THEN
        CREATE TRIGGER trg_orders_guard_delete BEFORE DELETE ON orders
            FOR EACH ROW EXECUTE FUNCTION ledger_guard_delete();
    END IF;

    -- Hard-deleted API keys leave a tombstone.
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_api_keys_tombstone' AND tgrelid = 'api_keys'::regclass) THEN
        CREATE TRIGGER trg_api_keys_tombstone AFTER DELETE ON api_keys
            FOR EACH ROW EXECUTE FUNCTION api_keys_tombstone();
    END IF;
END
$$;

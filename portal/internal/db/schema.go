package db

import (
	"context"
	"fmt"

	"github.com/mayiants/ai-portal-go/migrations"
	"go.uber.org/zap"
)

// ensureUniqueConstraint adds a UNIQUE constraint only when it does not already
// exist. The previous code dropped and re-added these constraints on every
// startup, which acquires an ACCESS EXCLUSIVE lock and blocks all writes on
// large/partitioned tables (notably usage_records) on each restart.
func (db *DB) ensureUniqueConstraint(ctx context.Context, constraintName, addSQL string) error {
	var exists bool
	if err := db.Pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM pg_constraint WHERE conname = $1)`,
		constraintName,
	).Scan(&exists); err != nil {
		return fmt.Errorf("check constraint %s: %w", constraintName, err)
	}
	if exists {
		return nil
	}
	if _, err := db.Pool.Exec(ctx, addSQL); err != nil {
		return fmt.Errorf("add constraint %s: %w", constraintName, err)
	}
	return nil
}

// InitSchema ensures all database tables, columns and indexes exist.
// It returns the first error encountered; all statements use IF NOT EXISTS / IF EXISTS for idempotency.
func (db *DB) InitSchema(ctx context.Context, logger *zap.Logger) error {

	// Ensure schema_migrations table exists
	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version VARCHAR(50) PRIMARY KEY,
			applied_at TIMESTAMP DEFAULT NOW()
		)
	`); err != nil {

		return fmt.Errorf("create table schema_migrations: %w", err)

	}

	// ============================================================================
	// 1. Core tables (ensure they exist with all fields needed by the application)
	// ============================================================================

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS users (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			name VARCHAR(255) NOT NULL,
			email VARCHAR(255),
			phone VARCHAR(20),
			password_hash VARCHAR(255) NOT NULL,
			company VARCHAR(255),
			role VARCHAR(20) DEFAULT 'user' CHECK (role IN ('user', 'enterprise_admin', 'system_admin', 'super_admin')),
			balance BIGINT DEFAULT 0,
			shipping_address JSONB,
			invoice_info JSONB,
			backup_email VARCHAR(255),
			privacy_region VARCHAR(50),
			data_retention_days INTEGER DEFAULT 365,
			gdpr_data_exported_at TIMESTAMP,
			gdpr_erasure_requested_at TIMESTAMP,
			managed_by UUID REFERENCES users(id),
			enterprise_config JSONB,
			group_id UUID,
			user_type VARCHAR(20) DEFAULT 'general',
			second_email VARCHAR(255),
			email_verified BOOLEAN DEFAULT false,
			locale VARCHAR(10) DEFAULT 'en',
			theme VARCHAR(20) DEFAULT 'system',
			login_attempts INTEGER DEFAULT 0,
			locked_until TIMESTAMP,
			last_login_at TIMESTAMP,
			remember_me_token VARCHAR(255),
			remember_me_expires_at TIMESTAMP,
			preferred_currency VARCHAR(10) DEFAULT 'USD',
			status VARCHAR(20) DEFAULT 'active',
			routing_track VARCHAR(50) DEFAULT 'standard',
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW(),
			deleted_at TIMESTAMP
		)
	`); err != nil {

		return fmt.Errorf("create table users: %w", err)

	}
	// Migrate email unique constraint from global to partial (active users only)
	// This allows soft-deleted users to re-register with the same email (new UUID)
	// Note: users_email_key may exist as a table constraint (from old schema)
	// Use DROP CONSTRAINT; DROP INDEX fails for constraints. CASCADE is safe here.
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE users DROP CONSTRAINT IF EXISTS users_email_key CASCADE`); err != nil {
		return fmt.Errorf("drop old users_email_key constraint: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS idx_users_email_active ON users(email) WHERE deleted_at IS NULL`); err != nil {
		return fmt.Errorf("create partial unique index on users(email): %w", err)
	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS gdpr_export_requests (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			user_id UUID NOT NULL REFERENCES users(id),
			email VARCHAR(255) NOT NULL,
			scope VARCHAR(20) NOT NULL DEFAULT 'full',
			status VARCHAR(20) NOT NULL DEFAULT 'pending',
			file_path TEXT,
			requested_at TIMESTAMP DEFAULT NOW(),
			processed_at TIMESTAMP,
			processed_by UUID REFERENCES users(id)
		)
	`); err != nil {

		return fmt.Errorf("create table gdpr_export_requests: %w", err)

	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS providers (
			id VARCHAR(50) PRIMARY KEY,
			name VARCHAR(100) NOT NULL,
			type VARCHAR(20) DEFAULT 'international' CHECK (type IN ('international', 'domestic')),
			base_url VARCHAR(255),
			sync_source VARCHAR(20) DEFAULT 'manual',
			enabled BOOLEAN DEFAULT true,
			sort_order INTEGER DEFAULT 0,
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW()
		)
	`); err != nil {

		return fmt.Errorf("create table providers: %w", err)

	}
	// Fields collected by the frontend but originally missing from the schema.
	if _, err := db.Pool.Exec(ctx, `
		ALTER TABLE providers
			ADD COLUMN IF NOT EXISTS api_key TEXT,
			ADD COLUMN IF NOT EXISTS description TEXT
	`); err != nil {
		return fmt.Errorf("alter table providers add api_key/description: %w", err)
	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS subscription_plans (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			name VARCHAR(255) NOT NULL,
			plan_type VARCHAR(20) DEFAULT 'token' CHECK (plan_type IN ('token', 'code')),
			price INTEGER NOT NULL,
			quota BIGINT NOT NULL,
			duration_days INTEGER NOT NULL,
			daily_limit BIGINT DEFAULT 0,
			model_permissions JSONB,
			description TEXT,
			status VARCHAR(20) DEFAULT 'active' CHECK (status IN ('active', 'inactive')),
			sort_order INTEGER DEFAULT 0,
			managed_by VARCHAR(20) NOT NULL DEFAULT 'seed',
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW(),
			deleted_at TIMESTAMP
		)
	`); err != nil {

		return fmt.Errorf("create table subscription_plans: %w", err)

	}

	// managed_by tracks whether a row is still seed-owned ('seed') or has been
	// touched by an admin ('admin'). Added via ALTER so databases created before
	// this column existed get it too (CREATE TABLE IF NOT EXISTS is a no-op there).
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE subscription_plans ADD COLUMN IF NOT EXISTS managed_by VARCHAR(20) NOT NULL DEFAULT 'seed'`); err != nil {
		return fmt.Errorf("alter subscription_plans add managed_by: %w", err)
	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS enterprises (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			name VARCHAR(255) NOT NULL,
			contact_email VARCHAR(255),
			status VARCHAR(20) DEFAULT 'active' CHECK (status IN ('active', 'suspended', 'inactive')),
			quota_total BIGINT DEFAULT 0,
			quota_used BIGINT DEFAULT 0,
			billing_cycle VARCHAR(20) DEFAULT 'monthly',
			credit_total BIGINT DEFAULT 0,
			credit_used BIGINT DEFAULT 0,
			prepaid_balance BIGINT NOT NULL DEFAULT 0,
			credit_reset_at TIMESTAMP,
			last_payment_amount BIGINT,
			last_payment_confirmed_at TIMESTAMP,
			last_payment_confirmed_by UUID REFERENCES users(id),
			max_admins INTEGER DEFAULT 3,
			admin_vacant_since TIMESTAMPTZ,
			deleted_at TIMESTAMPTZ,
			deleted_by UUID REFERENCES users(id),
			delete_reason TEXT,
			pricing_ratio DECIMAL(7,4) DEFAULT 1.0000,
			description TEXT,
			contact_name VARCHAR(255),
			contact_phone VARCHAR(50),
			max_members INTEGER DEFAULT 50,
			allowed_domains TEXT DEFAULT '',
			display_currency VARCHAR(10) DEFAULT 'USD',
			exchange_rate DECIMAL(12,6) DEFAULT 1.000000,
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW()
		)
	`); err != nil {
		return fmt.Errorf("create table enterprises: %w", err)
	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE UNIQUE INDEX IF NOT EXISTS idx_enterprises_name_active
		ON enterprises(name) WHERE deleted_at IS NULL
	`); err != nil {
		return fmt.Errorf("create unique index on enterprises(name): %w", err)
	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE UNIQUE INDEX IF NOT EXISTS idx_enterprises_email_active
		ON enterprises(contact_email) WHERE deleted_at IS NULL AND contact_email IS NOT NULL AND contact_email != ''
	`); err != nil {
		return fmt.Errorf("create unique index on enterprises(contact_email): %w", err)
	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS user_groups (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			name VARCHAR(255) NOT NULL,
			description TEXT,
			pricing_ratio DECIMAL(7,4) DEFAULT 1.0000,
			model_permissions JSONB,
			rate_limit_rpm INTEGER DEFAULT 120,
			status VARCHAR(20) DEFAULT 'active' CHECK (status IN ('active', 'inactive')),
			group_type VARCHAR(20) DEFAULT 'general',
			billing_mode VARCHAR(20) DEFAULT 'token',
			models JSONB DEFAULT '[]',
			rpm INT DEFAULT 0,
			rph BIGINT DEFAULT 0,
			rp5h BIGINT DEFAULT 0,
			rpw BIGINT DEFAULT 0,
			rpmonth BIGINT DEFAULT 0,
			tpm BIGINT DEFAULT 0,
			tph BIGINT DEFAULT 0,
			tpd BIGINT DEFAULT 0,
			tpw BIGINT DEFAULT 0,
			tpmonth BIGINT DEFAULT 0,
			enterprise_id UUID REFERENCES enterprises(id),
			visibility VARCHAR(20) DEFAULT 'public',
			is_default BOOLEAN DEFAULT false,
			routing_track VARCHAR(20) DEFAULT 'standard',
			concurrent_limit INT DEFAULT 10,
			parent_group_id UUID REFERENCES user_groups(id),
			allowed_enterprise_ids JSONB DEFAULT '[]',
			sync_mode VARCHAR(20) DEFAULT 'manual' CHECK (sync_mode IN ('manual', 'follow')),
			inherit_to_enterprises BOOLEAN NOT NULL DEFAULT FALSE,
			deleted_at TIMESTAMP,
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW()
		)
	`); err != nil {

		return fmt.Errorf("create table user_groups: %w", err)

	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS api_keys (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			user_id UUID REFERENCES users(id),
			key_hash VARCHAR(255) NOT NULL,
			key_prefix VARCHAR(20) NOT NULL,
			key_encrypted TEXT,
			name VARCHAR(255),
			status VARCHAR(20) DEFAULT 'active' CHECK (status IN ('active', 'disabled', 'revoked', 'inactive')),
			access_mode VARCHAR(20) DEFAULT 'general',
			allowed_models JSONB,
			model_priority JSONB,
			balance INTEGER DEFAULT 0,
			model_binding VARCHAR(100),
			rate_limit_rpm INTEGER DEFAULT 120,
			total_requests INTEGER DEFAULT 0,
			total_tokens BIGINT DEFAULT 0,
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW(),
			deleted_at TIMESTAMP,
			group_id UUID REFERENCES user_groups(id),
			group_priority JSONB DEFAULT '[]',
			current_model VARCHAR(100),
			billing_mode VARCHAR(20) DEFAULT 'token',
			key_type VARCHAR(20) DEFAULT 'general',
			subscription_id UUID,
			rate_limit_tpm INTEGER DEFAULT 100000,
			expires_at TIMESTAMP,
			model_routing_mode VARCHAR(20) DEFAULT 'single',
			selected_models JSONB DEFAULT '[]',
			enterprise_id UUID REFERENCES enterprises(id),
			assigned_user_id UUID REFERENCES users(id)
		)
	`); err != nil {

		return fmt.Errorf("create table api_keys: %w", err)

	}

	// Index for key validation hot path (lookup by hashed key).
	if _, err := db.Pool.Exec(ctx, `
		CREATE INDEX IF NOT EXISTS idx_api_keys_key_hash ON api_keys(key_hash)
	`); err != nil {
		return fmt.Errorf("create index on api_keys(key_hash): %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `
		CREATE INDEX IF NOT EXISTS idx_api_keys_key_hash_active ON api_keys(key_hash) WHERE deleted_at IS NULL
	`); err != nil {
		return fmt.Errorf("create partial index on api_keys(key_hash) active: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `
		CREATE INDEX IF NOT EXISTS idx_api_keys_subscription_id ON api_keys(subscription_id) WHERE subscription_id IS NOT NULL
	`); err != nil {
		return fmt.Errorf("create partial index on api_keys(subscription_id): %w", err)
	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS upstream_keys (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			provider VARCHAR(50) NOT NULL,
			key_encrypted TEXT NOT NULL,
			key_preview VARCHAR(20) NOT NULL,
			tier VARCHAR(20) DEFAULT 'default',
			base_url TEXT,
			models JSONB,
			notes TEXT,
			enabled BOOLEAN DEFAULT true,
			metadata JSONB,
			assignment_mode VARCHAR(20) DEFAULT 'exclusive',
			assigned_proxy VARCHAR(50),
			allowed_regions JSONB,
			rpm_limit INTEGER DEFAULT 0,
			tpm_limit INTEGER DEFAULT 0,
			model_limits JSONB,
			quota_limit BIGINT DEFAULT 0,
			validation_status VARCHAR(20) DEFAULT 'pending' CHECK (validation_status IN ('pending', 'valid', 'invalid', 'unknown')),
			validation_at TIMESTAMP,
			last_synced_at TIMESTAMP,
			deleted_at TIMESTAMP,
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW(),
			UNIQUE(provider, key_encrypted)
		)
	`); err != nil {

		return fmt.Errorf("create table upstream_keys: %w", err)

	}

	if _, err := db.Pool.Exec(ctx, `
		DO $$
		BEGIN
			IF NOT EXISTS (
				SELECT 1 FROM pg_constraint WHERE conname = 'upstream_keys_validation_status_check'
			) THEN
				ALTER TABLE upstream_keys ADD CONSTRAINT upstream_keys_validation_status_check
					CHECK (validation_status IN ('pending', 'valid', 'invalid', 'unknown'));
			END IF;
		END
		$$;
	`); err != nil {
		return fmt.Errorf("add upstream_keys validation_status constraint: %w", err)
	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS upstream_key_validation_log (
			id BIGSERIAL PRIMARY KEY,
			upstream_key_id UUID,
			provider VARCHAR(50),
			key_preview VARCHAR(20),
			trigger_type VARCHAR(20) NOT NULL DEFAULT 'background',
			result VARCHAR(30) NOT NULL,
			error_message TEXT,
			models_fetched INTEGER NOT NULL DEFAULT 0,
			duration_ms INTEGER NOT NULL DEFAULT 0,
			created_at TIMESTAMP NOT NULL DEFAULT NOW()
		)
	`); err != nil {
		return fmt.Errorf("create table upstream_key_validation_log: %w", err)
	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE INDEX IF NOT EXISTS idx_ukvl_created_at ON upstream_key_validation_log (created_at DESC)
	`); err != nil {
		return fmt.Errorf("create index idx_ukvl_created_at: %w", err)
	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE INDEX IF NOT EXISTS idx_ukvl_key ON upstream_key_validation_log (upstream_key_id, created_at DESC)
	`); err != nil {
		return fmt.Errorf("create index idx_ukvl_key: %w", err)
	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE INDEX IF NOT EXISTS idx_ukvl_provider_time ON upstream_key_validation_log (provider, created_at DESC)
	`); err != nil {
		return fmt.Errorf("create index idx_ukvl_provider_time: %w", err)
	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS service_models (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			provider VARCHAR(50) NOT NULL,
			product_id VARCHAR(100) NOT NULL,
			api_model_name VARCHAR(100),
			status VARCHAR(20) DEFAULT 'pending' CHECK (status IN ('pending', 'active', 'paused', 'ignored', 'eol_pending', 'eol', 'discontinued')),
			input_price DECIMAL(12,8),
			output_price DECIMAL(12,8),
			cache_read_price DECIMAL(12,8),
			aimodels_data JSONB,
			capabilities JSONB DEFAULT '{}',
			context_window INTEGER,
			max_output_tokens INTEGER,
			show_in_store BOOLEAN DEFAULT true,
			sort_order INTEGER DEFAULT 0,
			deleted_at TIMESTAMP,
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW(),
			UNIQUE(provider, product_id)
		)
	`); err != nil {

		return fmt.Errorf("create table service_models: %w", err)

	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS model_pricing_rules (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			provider VARCHAR(50) NOT NULL,
			region VARCHAR(50) DEFAULT '*',
			product_id VARCHAR(100),
			api_model_name VARCHAR(100),
			billing_mode VARCHAR(30) NOT NULL,
			prices JSONB,
			tiers JSONB,
			time_rules JSONB,
			timezone VARCHAR(50) DEFAULT 'UTC',
			currency VARCHAR(10) DEFAULT 'USD',
			effective_from TIMESTAMP DEFAULT NOW(),
			effective_to TIMESTAMP,
			priority INT DEFAULT 0,
			status VARCHAR(20) DEFAULT 'active',
			rule_name VARCHAR(255),
			note TEXT,
			block_seconds INT DEFAULT 0,
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW()
		)
	`); err != nil {

		return fmt.Errorf("create table model_pricing_rules: %w", err)

	}

	// Capability master data: one row per api model name, layered over
	// service_models.capabilities (see migrations/0014 for precedence rules).
	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS model_capability_overrides (
			model_name TEXT PRIMARY KEY,
			supported_types JSONB NOT NULL DEFAULT '[]'::jsonb,
			source TEXT NOT NULL DEFAULT 'upstream'
			        CHECK (source IN ('manual', 'probe', 'upstream')),
			note TEXT,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`); err != nil {

		return fmt.Errorf("create table model_capability_overrides: %w", err)

	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE INDEX IF NOT EXISTS idx_model_pricing_rules_match
		ON model_pricing_rules(provider, region, product_id, api_model_name, status)
	`); err != nil {

		return fmt.Errorf("create index idx_model_pricing_rules_match: %w", err)

	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE INDEX IF NOT EXISTS idx_model_pricing_rules_time
		ON model_pricing_rules(effective_from, effective_to)
	`); err != nil {

		return fmt.Errorf("create index idx_model_pricing_rules_time: %w", err)

	}

	if _, err := db.Pool.Exec(ctx, `ALTER TABLE model_pricing_rules ADD COLUMN IF NOT EXISTS block_seconds INT DEFAULT 0`); err != nil {
		return fmt.Errorf("add block_seconds column: %w", err)
	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS user_subscriptions (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			user_id UUID NOT NULL REFERENCES users(id),
			plan_id UUID REFERENCES subscription_plans(id),
			plan_name VARCHAR(255),
			plan_type VARCHAR(20),
			quota_total BIGINT DEFAULT 0,
			quota_used BIGINT DEFAULT 0,
			quota_remaining BIGINT DEFAULT 0,
			daily_limit BIGINT,
			daily_quota_used BIGINT DEFAULT 0,
			daily_quota_reset TIMESTAMP,
			duration_days INTEGER,
			status VARCHAR(20) DEFAULT 'active' CHECK (status IN ('active', 'expired', 'cancelled')),
			started_at TIMESTAMP DEFAULT NOW(),
			expires_at TIMESTAMP,
			deleted_at TIMESTAMP,
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW(),
			request_remaining BIGINT DEFAULT 0,
			request_total BIGINT DEFAULT 0,
			request_used BIGINT DEFAULT 0,
			auto_renew BOOLEAN DEFAULT false
		)
	`); err != nil {

		return fmt.Errorf("create table user_subscriptions: %w", err)

	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS enterprise_members (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			enterprise_id UUID NOT NULL REFERENCES enterprises(id) ON DELETE CASCADE,
			user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			role VARCHAR(20) DEFAULT 'member' CHECK (role IN ('member', 'admin')),
			status VARCHAR(20) DEFAULT 'active' CHECK (status IN ('active', 'inactive')),
			created_by UUID REFERENCES users(id),
			deleted_at TIMESTAMP,
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW(),
			removed_at TIMESTAMPTZ,
			removed_by UUID REFERENCES users(id),
			remove_reason TEXT,
			UNIQUE(enterprise_id, user_id)
		)
	`); err != nil {

		return fmt.Errorf("create table enterprise_members: %w", err)

	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS audit_logs (
			id BIGSERIAL PRIMARY KEY,
			log_id UUID UNIQUE DEFAULT gen_random_uuid(),
			user_id UUID,
			user_email VARCHAR(255),
			category VARCHAR(50),
			action VARCHAR(100),
			level VARCHAR(20) DEFAULT 'info' CHECK (level IN ('debug', 'info', 'warning', 'error')),
			message TEXT,
			details JSONB,
			ip_address INET,
			user_agent TEXT,
			created_at TIMESTAMP DEFAULT NOW()
		)
	`); err != nil {

		return fmt.Errorf("create table audit_logs: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_audit_logs_created_at ON audit_logs(created_at DESC)`); err != nil {
		return fmt.Errorf("create index idx_audit_logs_created_at: %w", err)
	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS proxy_heartbeats (
			server_id VARCHAR(50) PRIMARY KEY,
			uptime INTEGER DEFAULT 0,
			total_requests INTEGER DEFAULT 0,
			providers JSONB,
			created_at TIMESTAMP DEFAULT NOW()
		)
	`); err != nil {

		return fmt.Errorf("create table proxy_heartbeats: %w", err)

	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS proxy_instances (
			proxy_id VARCHAR(50) PRIMARY KEY,
			region VARCHAR(20),
			last_heartbeat_at TIMESTAMP,
			status VARCHAR(20) DEFAULT 'active',
			metadata JSONB,
			created_at TIMESTAMP DEFAULT NOW()
		)
	`); err != nil {

		return fmt.Errorf("create table proxy_instances: %w", err)

	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS usage_records (
			id BIGSERIAL,
			trace_id UUID NOT NULL,
			user_id UUID,
			user_api_key_id UUID,
			upstream_key_id VARCHAR(50),
			model VARCHAR(100),
			provider VARCHAR(50),
			region VARCHAR(50),
			input_tokens INTEGER DEFAULT 0,
			output_tokens INTEGER DEFAULT 0,
			cached_input_tokens INTEGER DEFAULT 0,
			effective_input_tokens INTEGER DEFAULT 0,
			reasoning_tokens INTEGER DEFAULT 0,
			audio_input_tokens INTEGER DEFAULT 0,
			audio_output_tokens INTEGER DEFAULT 0,
			input_cost BIGINT DEFAULT 0,
			output_cost BIGINT DEFAULT 0,
			cache_cost BIGINT DEFAULT 0,
			reasoning_cost BIGINT DEFAULT 0,
			total_cost BIGINT DEFAULT 0,
			latency_ms INTEGER,
			request_count INTEGER DEFAULT 1,
			cache_hit BOOLEAN DEFAULT false,
			error TEXT,
			balance_deduction BIGINT DEFAULT 0,
			routing_track VARCHAR(50),
			endpoint VARCHAR(128),
			is_streaming BOOLEAN DEFAULT false,
			client_ip VARCHAR(64),
			status VARCHAR(32) DEFAULT 'success',
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			PRIMARY KEY (id, created_at)
		) PARTITION BY RANGE (created_at)
	`); err != nil {

		return fmt.Errorf("create table usage_records: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS usage_records_default PARTITION OF usage_records DEFAULT`); err != nil {

		return fmt.Errorf("create table usage_records_default: %w", err)

	}

	// Create monthly range partitions so usage_records does not grow unbounded
	// inside the default partition. Old data outside the rolling window still
	// lands in usage_records_default.
	if err := db.EnsureUsagePartitions(ctx, logger); err != nil {
		return fmt.Errorf("ensure usage_records partitions: %w", err)
	}

	if _, err := db.Pool.Exec(ctx, `ALTER TABLE usage_records ADD COLUMN IF NOT EXISTS pricing_error BOOLEAN DEFAULT false`); err != nil {
		return fmt.Errorf("alter table usage_records add column pricing_error: %w", err)
	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS orders (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			user_id UUID NOT NULL REFERENCES users(id),
			order_id VARCHAR(50) UNIQUE,
			amount BIGINT NOT NULL,
			currency VARCHAR(10) DEFAULT 'USD',
			exchange_rate DECIMAL(10,4) DEFAULT 1.0,
			status VARCHAR(20) DEFAULT 'pending',
			payment_method VARCHAR(50),
			payment_id VARCHAR(255),
			product_type VARCHAR(50),
			product_name VARCHAR(255),
			model_binding VARCHAR(100),
			api_key_id UUID REFERENCES api_keys(id),
			metadata JSONB,
			completed_at TIMESTAMP,
			deleted_at TIMESTAMP,
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW()
		)
	`); err != nil {

		return fmt.Errorf("create table orders: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE orders ADD COLUMN IF NOT EXISTS receipt_key VARCHAR(512)`); err != nil {

		return fmt.Errorf("alter table orders add receipt_key: %w", err)

	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS invoices (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			user_id UUID NOT NULL REFERENCES users(id),
			invoice_number VARCHAR(50) UNIQUE,
			amount BIGINT NOT NULL,
			status VARCHAR(20) DEFAULT 'pending',
			enterprise_id UUID REFERENCES enterprises(id),
			request_id UUID,
			storage_key VARCHAR(512),
			deleted_at TIMESTAMP,
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW()
		)
	`); err != nil {

		return fmt.Errorf("create table invoices: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE invoices ADD COLUMN IF NOT EXISTS request_id UUID`); err != nil {

		return fmt.Errorf("alter table invoices add request_id: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE invoices ADD COLUMN IF NOT EXISTS storage_key VARCHAR(512)`); err != nil {

		return fmt.Errorf("alter table invoices add storage_key: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE invoices ADD COLUMN IF NOT EXISTS invoice_source VARCHAR(10) NOT NULL DEFAULT 'auto'`); err != nil {

		return fmt.Errorf("alter table invoices add invoice_source: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE invoices ADD COLUMN IF NOT EXISTS gst_cents BIGINT NOT NULL DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table invoices add gst_cents: %w", err)

	}

	// ============================================================================
	// 2. Ensure critical columns exist (defense against partial migration failures)
	// ============================================================================

	// --- Additional fields from later migrations (defense against partial migration failures) ---

	// ============================================================================
	// 2.5 Missing columns detected by scripts/check_schema_vs_code.py (2026-06-03)
	// ============================================================================

	// ============================================================================

	// Unique constraint for webhook upserts (ON CONFLICT). The constraint is
	// (trace_id, created_at) — NOT (trace_id) alone — because usage_records is
	// PARTITION BY RANGE (created_at) and PostgreSQL requires every unique
	// constraint on a partitioned table to include the partition key (which is
	// also why the primary key is (id, created_at)). A single-column
	// UNIQUE (trace_id) is rejected at runtime: "unique constraint on
	// partitioned table must include all partitioning columns".
	//
	// Dedup still works correctly: inserts pass recordCreatedAt() (the
	// record's own Timestamp, not NOW()) as created_at, so a retried/resent
	// record with the same trace_id produces the same created_at and conflicts
	// as expected. See commit ef029bb.
	//
	// Only add if missing — avoid DROP+re-ADD on every restart which blocks
	// writes on this large/partitioned table.
	if err := db.ensureUniqueConstraint(ctx,
		"usage_records_trace_id_created_at_unique",
		`ALTER TABLE usage_records ADD CONSTRAINT usage_records_trace_id_created_at_unique UNIQUE (trace_id, created_at)`); err != nil {
		return fmt.Errorf("ensure constraint on usage_records: %w", err)
	}

	// 3. Ensure all auxiliary tables exist
	// ============================================================================

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS system_settings (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			key VARCHAR(100) UNIQUE NOT NULL,
			value TEXT,
			category VARCHAR(50) DEFAULT 'general',
			description TEXT,
			managed_by VARCHAR(20) NOT NULL DEFAULT 'seed',
			updated_by UUID REFERENCES users(id),
			updated_at TIMESTAMP DEFAULT NOW()
		)
	`); err != nil {

		return fmt.Errorf("create table system_settings: %w", err)

	}

	// managed_by marks seed-owned vs admin-touched settings; see subscription_plans.
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE system_settings ADD COLUMN IF NOT EXISTS managed_by VARCHAR(20) NOT NULL DEFAULT 'seed'`); err != nil {
		return fmt.Errorf("alter system_settings add managed_by: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_system_settings_category ON system_settings(category)`); err != nil {

		return fmt.Errorf("create index idx_system_settings_category: %w", err)

	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS user_devices (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			user_id UUID NOT NULL REFERENCES users(id),
			device_id VARCHAR(255),
			device_name VARCHAR(255),
			device_type VARCHAR(50),
			os VARCHAR(50),
			browser VARCHAR(100),
			ip_address INET,
			location VARCHAR(255),
			is_current BOOLEAN DEFAULT false,
			last_active_at TIMESTAMP,
			created_at TIMESTAMP DEFAULT NOW()
		)
	`); err != nil {

		return fmt.Errorf("create table user_devices: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_user_devices_user ON user_devices(user_id)`); err != nil {

		return fmt.Errorf("create index idx_user_devices_user: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_user_devices_last_active ON user_devices(last_active_at DESC)`); err != nil {

		return fmt.Errorf("create index idx_user_devices_last_active: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE user_devices ADD COLUMN IF NOT EXISTS fingerprint VARCHAR(255)`); err != nil {

		return fmt.Errorf("alter table user_devices add fingerprint: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS idx_user_devices_user_fingerprint ON user_devices(user_id, fingerprint)`); err != nil {

		return fmt.Errorf("create unique index idx_user_devices_user_fingerprint: %w", err)

	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS verification_codes (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			user_id UUID NOT NULL REFERENCES users(id),
			code VARCHAR(20) NOT NULL,
			type VARCHAR(50),
			value VARCHAR(255),
			purpose VARCHAR(50),
			expires_at TIMESTAMP NOT NULL,
			created_at TIMESTAMP DEFAULT NOW(),
			UNIQUE(user_id, type)
		)
	`); err != nil {

		return fmt.Errorf("create table verification_codes: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_verification_codes_expires ON verification_codes(expires_at)`); err != nil {

		return fmt.Errorf("create index idx_verification_codes_expires: %w", err)

	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS enterprise_invitations (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			enterprise_id UUID NOT NULL REFERENCES enterprises(id),
			email VARCHAR(255) NOT NULL,
			token VARCHAR(255) UNIQUE NOT NULL,
			role VARCHAR(20) DEFAULT 'member',
			status VARCHAR(20) DEFAULT 'pending',
			email_confirmed BOOLEAN DEFAULT FALSE,
			email_confirmed_at TIMESTAMPTZ,
			admin_approved BOOLEAN DEFAULT FALSE,
			admin_approved_by UUID REFERENCES users(id),
			admin_approved_at TIMESTAMPTZ,
			accepted_at TIMESTAMP,
			expires_at TIMESTAMP,
			invited_by UUID,
			created_at TIMESTAMP DEFAULT NOW()
		)
	`); err != nil {

		return fmt.Errorf("create table enterprise_invitations: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_enterprise_invitations_token ON enterprise_invitations(token)`); err != nil {

		return fmt.Errorf("create index idx_enterprise_invitations_token: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_enterprise_invitations_email ON enterprise_invitations(email)`); err != nil {

		return fmt.Errorf("create index idx_enterprise_invitations_email: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_enterprise_invitations_enterprise ON enterprise_invitations(enterprise_id)`); err != nil {

		return fmt.Errorf("create index idx_enterprise_invitations_enterprise: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_enterprise_invitations_status ON enterprise_invitations(status)`); err != nil {

		return fmt.Errorf("create index idx_enterprise_invitations_status: %w", err)

	}

	// Partial unique index backing ON CONFLICT (enterprise_id, email) WHERE status IN (...)
	// used by invitation insert paths (single invite + CSV batch import).
	if _, err := db.Pool.Exec(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS uq_enterprise_invitations_active ON enterprise_invitations(enterprise_id, email) WHERE status IN ('pending', 'email_confirmed')`); err != nil {

		return fmt.Errorf("create index uq_enterprise_invitations_active: %w", err)

	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS payment_gateways (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			name VARCHAR(100) NOT NULL,
			provider VARCHAR(50) NOT NULL,
			gateway VARCHAR(50) UNIQUE,
			enabled BOOLEAN DEFAULT true,
			config JSONB,
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW()
		)
	`); err != nil {

		return fmt.Errorf("create table payment_gateways: %w", err)

	}

	// The payment link addresses gateways by the `gateway` identifier; older
	// databases created the table without it.
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE payment_gateways ADD COLUMN IF NOT EXISTS gateway VARCHAR(50) UNIQUE`); err != nil {
		return fmt.Errorf("alter table payment_gateways add gateway: %w", err)
	}

	// Seed the four supported gateways as disabled rows so the admin UI and the
	// /api/admin/payment/gateways endpoints have rows to configure.
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO payment_gateways (name, provider, gateway, enabled, config)
		VALUES
			('Stripe', 'stripe', 'stripe', false, '{}'),
			('PayPal', 'paypal', 'paypal', false, '{}'),
			('Alipay', 'alipay', 'alipay', false, '{}'),
			('WeChat Pay', 'wechat', 'wechat', false, '{}')
		ON CONFLICT (gateway) DO NOTHING
	`); err != nil {
		return fmt.Errorf("seed payment_gateways: %w", err)
	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS payment_config (
			config_key VARCHAR(100) PRIMARY KEY,
			config_value TEXT NOT NULL,
			enabled BOOLEAN DEFAULT true,
			updated_at TIMESTAMP DEFAULT NOW()
		)
	`); err != nil {

		return fmt.Errorf("create table payment_config: %w", err)

	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS subscription_purchases (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			user_id UUID NOT NULL REFERENCES users(id),
			plan_id UUID REFERENCES subscription_plans(id),
			amount BIGINT NOT NULL,
			status VARCHAR(20) DEFAULT 'pending',
			order_id UUID,
			starts_at TIMESTAMP,
			expires_at TIMESTAMP,
			deleted_at TIMESTAMP,
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW()
		)
	`); err != nil {

		return fmt.Errorf("create table subscription_purchases: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_subscription_purchases_user ON subscription_purchases(user_id)`); err != nil {

		return fmt.Errorf("create index idx_subscription_purchases_user: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_subscription_purchases_status ON subscription_purchases(status)`); err != nil {

		return fmt.Errorf("create index idx_subscription_purchases_status: %w", err)

	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS balance_transactions (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			user_id UUID NOT NULL REFERENCES users(id),
			enterprise_id UUID REFERENCES enterprises(id),
			transaction_type VARCHAR(50) NOT NULL,
			amount BIGINT NOT NULL,
			balance_before BIGINT,
			balance_after BIGINT,
			description TEXT,
			balance_type VARCHAR(20) DEFAULT 'gift',
			reference_id VARCHAR(255),
			created_at TIMESTAMP DEFAULT NOW()
		)
	`); err != nil {

		return fmt.Errorf("create table balance_transactions: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_balance_transactions_user ON balance_transactions(user_id)`); err != nil {

		return fmt.Errorf("create index idx_balance_transactions_user: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_balance_transactions_enterprise ON balance_transactions(enterprise_id)`); err != nil {

		return fmt.Errorf("create index idx_balance_transactions_enterprise: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_balance_transactions_type ON balance_transactions(transaction_type)`); err != nil {

		return fmt.Errorf("create index idx_balance_transactions_type: %w", err)

	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS exchange_rates (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			from_currency VARCHAR(10) NOT NULL,
			to_currency VARCHAR(10) NOT NULL,
			rate DECIMAL(18,8) NOT NULL,
			fetched_at TIMESTAMP,
			source VARCHAR(100),
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW(),
			UNIQUE(from_currency, to_currency)
		)
	`); err != nil {

		return fmt.Errorf("create table exchange_rates: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_exchange_rates_currencies ON exchange_rates(from_currency, to_currency)`); err != nil {

		return fmt.Errorf("create index idx_exchange_rates_currencies: %w", err)

	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS announcements (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			title VARCHAR(255) NOT NULL,
			content TEXT,
			status VARCHAR(20) DEFAULT 'draft',
			start_at TIMESTAMP,
			end_at TIMESTAMP,
			created_by UUID REFERENCES users(id),
			type VARCHAR(50) DEFAULT 'info',
			pinned BOOLEAN DEFAULT false,
			show_popup BOOLEAN DEFAULT false,
			priority INTEGER DEFAULT 0,
			display_mode VARCHAR(20) DEFAULT 'modal',
			cta_text VARCHAR(100),
			cta_link VARCHAR(500),
			banner_color VARCHAR(20) DEFAULT 'primary',
			dismiss_mode VARCHAR(20) DEFAULT 'session',
			target_audience VARCHAR(20) DEFAULT 'all',
			icon_emoji VARCHAR(10),
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW()
		)
	`); err != nil {

		return fmt.Errorf("create table announcements: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_announcements_status ON announcements(status)`); err != nil {

		return fmt.Errorf("create index idx_announcements_status: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_announcements_start_end ON announcements(start_at, end_at) WHERE status = 'published'`); err != nil {

		return fmt.Errorf("create index idx_announcements_start_end: %w", err)

	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS announcement_reads (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			announcement_id UUID NOT NULL REFERENCES announcements(id),
			user_id UUID NOT NULL REFERENCES users(id),
			read_at TIMESTAMP DEFAULT NOW(),
			UNIQUE(announcement_id, user_id)
		)
	`); err != nil {

		return fmt.Errorf("create table announcement_reads: %w", err)

	}

	// ============================================================================
	// Unified Voucher tables (replaces redemption_codes + invitation_codes)
	// ============================================================================

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS vouchers (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			code VARCHAR(50) UNIQUE NOT NULL,
                name VARCHAR(255),
                description TEXT,
			owner_type VARCHAR(20) DEFAULT 'admin' CHECK (owner_type IN ('system', 'admin', 'user')),
			created_by UUID REFERENCES users(id),
			user_id UUID REFERENCES users(id),
			amount BIGINT NOT NULL DEFAULT 0,
			reward_type VARCHAR(20) DEFAULT 'gift_balance',
			reward_config JSONB,
			max_uses INTEGER DEFAULT 1,
			used_count INTEGER DEFAULT 0,
			status VARCHAR(20) DEFAULT 'active',
			expires_at TIMESTAMP,
			plan_id UUID,
			auto_apply_on_register BOOLEAN DEFAULT false,
			apply_trigger VARCHAR(20),
			deleted_at TIMESTAMP,
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW()
		)
	`); err != nil {

		return fmt.Errorf("create table vouchers: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE vouchers ADD COLUMN IF NOT EXISTS type VARCHAR(20) DEFAULT 'gift'`); err != nil {

		return fmt.Errorf("alter table vouchers add type: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_vouchers_code ON vouchers(code)`); err != nil {

		return fmt.Errorf("create index idx_vouchers_code: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_vouchers_type ON vouchers(type)`); err != nil {

		return fmt.Errorf("create index idx_vouchers_type: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_vouchers_status ON vouchers(status) WHERE deleted_at IS NULL`); err != nil {

		return fmt.Errorf("create index idx_vouchers_status: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_vouchers_user ON vouchers(user_id) WHERE user_id IS NOT NULL`); err != nil {

		return fmt.Errorf("create index idx_vouchers_user: %w", err)

	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS voucher_uses (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			voucher_id UUID NOT NULL REFERENCES vouchers(id),
			user_id UUID NOT NULL REFERENCES users(id),
			consumer_type VARCHAR(20) DEFAULT 'redeemer' CHECK (consumer_type IN ('redeemer', 'inviter', 'invitee')),
			amount BIGINT DEFAULT 0,
			reward_granted BOOLEAN DEFAULT false,
			balance_before BIGINT,
			balance_after BIGINT,
			created_at TIMESTAMP DEFAULT NOW()
		)
	`); err != nil {

		return fmt.Errorf("create table voucher_uses: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_voucher_uses_voucher ON voucher_uses(voucher_id)`); err != nil {

		return fmt.Errorf("create index idx_voucher_uses_voucher: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_voucher_uses_user ON voucher_uses(user_id)`); err != nil {

		return fmt.Errorf("create index idx_voucher_uses_user: %w", err)

	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS provider_health_stats (
			id BIGSERIAL PRIMARY KEY,
			provider VARCHAR(50) NOT NULL,
			bucket TIMESTAMPTZ NOT NULL,
			success_count INTEGER DEFAULT 0,
			error_count INTEGER DEFAULT 0,
			avg_latency_ms INTEGER,
			error_4xx_count INTEGER DEFAULT 0,
			error_5xx_count INTEGER DEFAULT 0,
			error_rate DECIMAL(7,4) DEFAULT 0,
			status VARCHAR(20) DEFAULT 'unknown',
			created_at TIMESTAMP DEFAULT NOW()
		)
	`); err != nil {

		return fmt.Errorf("create table provider_health_stats: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_provider_health_bucket ON provider_health_stats(bucket)`); err != nil {

		return fmt.Errorf("create index idx_provider_health_bucket: %w", err)

	}
	// provider_health_stats unique constraint is already handled above (lines 722-723)
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_provider_health_provider_bucket ON provider_health_stats(provider, bucket)`); err != nil {

		return fmt.Errorf("create index idx_provider_health_provider_bucket: %w", err)

	}
	if err := db.ensureUniqueConstraint(ctx,
		"provider_health_stats_provider_bucket_unique",
		`ALTER TABLE provider_health_stats ADD CONSTRAINT provider_health_stats_provider_bucket_unique UNIQUE (provider, bucket)`); err != nil {
		return fmt.Errorf("ensure constraint on provider_health_stats: %w", err)
	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS provider_health_stats_model (
			provider VARCHAR(50) NOT NULL,
			model VARCHAR(100) NOT NULL,
			bucket TIMESTAMPTZ NOT NULL,
			request_count INTEGER DEFAULT 0,
			success_count INTEGER DEFAULT 0,
			failure_count INTEGER DEFAULT 0,
			timeout_count INTEGER DEFAULT 0,
			rate_limit_count INTEGER DEFAULT 0,
			error_5xx_count INTEGER DEFAULT 0,
			error_4xx_count INTEGER DEFAULT 0,
			auth_error_count INTEGER DEFAULT 0,
			network_error_count INTEGER DEFAULT 0,
			response_timeout_count INTEGER DEFAULT 0,
			content_mismatch_count INTEGER DEFAULT 0,
			invalid_request_count INTEGER DEFAULT 0,
			total_latency_ms BIGINT DEFAULT 0,
			avg_latency_ms INTEGER DEFAULT 0,
			slow_latency_ms INTEGER DEFAULT 5000,
			availability DECIMAL(5,2) DEFAULT -1,
			error_rate DECIMAL(7,4) DEFAULT 0,
			status VARCHAR(20) DEFAULT 'unknown',
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW(),
			PRIMARY KEY (provider, model, bucket)
		)
	`); err != nil {

		return fmt.Errorf("create table provider_health_stats_model: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_provider_health_model_bucket ON provider_health_stats_model(bucket)`); err != nil {

		return fmt.Errorf("create index idx_provider_health_model_bucket: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_provider_health_model_provider_bucket ON provider_health_stats_model(provider, model, bucket)`); err != nil {

		return fmt.Errorf("create index idx_provider_health_model_provider_bucket: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS idx_provider_health_model_pk ON provider_health_stats_model(provider, model, bucket)`); err != nil {

		return fmt.Errorf("create unique index idx_provider_health_model_pk: %w", err)

	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS model_group_health_stats (
			model VARCHAR(100) NOT NULL,
			group_id VARCHAR(50) NOT NULL,
			bucket TIMESTAMPTZ NOT NULL,
			request_count INTEGER DEFAULT 0,
			success_count INTEGER DEFAULT 0,
			failure_count INTEGER DEFAULT 0,
			timeout_count INTEGER DEFAULT 0,
			rate_limit_count INTEGER DEFAULT 0,
			error_5xx_count INTEGER DEFAULT 0,
			error_4xx_count INTEGER DEFAULT 0,
			auth_error_count INTEGER DEFAULT 0,
			network_error_count INTEGER DEFAULT 0,
			response_timeout_count INTEGER DEFAULT 0,
			content_mismatch_count INTEGER DEFAULT 0,
			invalid_request_count INTEGER DEFAULT 0,
			total_latency_ms BIGINT DEFAULT 0,
			avg_latency_ms INTEGER DEFAULT 0,
			slow_latency_ms INTEGER DEFAULT 5000,
			availability DECIMAL(5,2) DEFAULT -1,
			error_rate DECIMAL(7,4) DEFAULT 0,
			status VARCHAR(20) DEFAULT 'unknown',
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW(),
			PRIMARY KEY (model, group_id, bucket)
		)
	`); err != nil {

		return fmt.Errorf("create table model_group_health_stats: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_model_group_health_bucket ON model_group_health_stats(bucket)`); err != nil {

		return fmt.Errorf("create index idx_model_group_health_bucket: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_model_group_health_model_group ON model_group_health_stats(model, group_id, bucket)`); err != nil {

		return fmt.Errorf("create index idx_model_group_health_model_group: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS idx_model_group_health_pk ON model_group_health_stats(model, group_id, bucket)`); err != nil {

		return fmt.Errorf("create unique index idx_model_group_health_pk: %w", err)

	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS upstream_key_usage (
			id BIGSERIAL PRIMARY KEY,
			upstream_key_id UUID NOT NULL,
			provider VARCHAR(50),
			date DATE NOT NULL,
			request_count INTEGER DEFAULT 0,
			token_count BIGINT DEFAULT 0,
			cost BIGINT DEFAULT 0,
			avg_latency_ms INTEGER DEFAULT 0,
			model VARCHAR(100),
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW()
		)
	`); err != nil {

		return fmt.Errorf("create table upstream_key_usage: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_upstream_key_usage_key_date ON upstream_key_usage(upstream_key_id, date)`); err != nil {

		return fmt.Errorf("create index idx_upstream_key_usage_key_date: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_upstream_key_usage_date ON upstream_key_usage(date)`); err != nil {

		return fmt.Errorf("create index idx_upstream_key_usage_date: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_upstream_key_usage_provider ON upstream_key_usage(provider, date)`); err != nil {

		return fmt.Errorf("create index idx_upstream_key_usage_provider: %w", err)

	}
	if err := db.ensureUniqueConstraint(ctx,
		"upstream_key_usage_key_model_date_unique",
		`ALTER TABLE upstream_key_usage ADD CONSTRAINT upstream_key_usage_key_model_date_unique UNIQUE (upstream_key_id, model, date)`); err != nil {
		return fmt.Errorf("ensure constraint on upstream_key_usage: %w", err)
	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS model_provider_routing (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			model_name VARCHAR(100) NOT NULL,
			provider VARCHAR(50) NOT NULL,
			tier VARCHAR(20) DEFAULT '',
			priority INTEGER DEFAULT 0,
			enabled BOOLEAN DEFAULT true,
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW(),
			UNIQUE(model_name, provider)
		)
	`); err != nil {

		return fmt.Errorf("create table model_provider_routing: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_mpr_model ON model_provider_routing(model_name)`); err != nil {

		return fmt.Errorf("create index idx_mpr_model: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE model_provider_routing ADD COLUMN IF NOT EXISTS tier VARCHAR(20) DEFAULT ''`); err != nil {

		return fmt.Errorf("add column tier: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_mpr_provider ON model_provider_routing(provider)`); err != nil {

		return fmt.Errorf("create index idx_mpr_provider: %w", err)

	}

	// Rename the legacy enterprise_credit_log table to enterprise_audit_log. The
	// table originally tracked only credit/billing events but has since been
	// extended (category, change_type, field_name, old/new_value, etc.) to serve
	// as the general enterprise-scoped audit log. The rename keeps the existing
	// data and makes the name match its actual purpose.
	if _, err := db.Pool.Exec(ctx, `
		DO $$ BEGIN
			IF EXISTS (SELECT 1 FROM pg_tables WHERE tablename = 'enterprise_credit_log')
			   AND NOT EXISTS (SELECT 1 FROM pg_tables WHERE tablename = 'enterprise_audit_log') THEN
				ALTER TABLE enterprise_credit_log RENAME TO enterprise_audit_log;
			END IF;
		END $$`); err != nil {
		return fmt.Errorf("rename enterprise_credit_log to enterprise_audit_log: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `
		DO $$ BEGIN
			IF EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_credit_log_enterprise')
			   AND NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_enterprise_audit_log_enterprise') THEN
				ALTER INDEX idx_credit_log_enterprise RENAME TO idx_enterprise_audit_log_enterprise;
			END IF;
		END $$`); err != nil {
		return fmt.Errorf("rename idx_credit_log_enterprise: %w", err)
	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS enterprise_audit_log (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			enterprise_id UUID NOT NULL REFERENCES enterprises(id),
			action VARCHAR(50),
			amount BIGINT,
			balance_before BIGINT,
			balance_after BIGINT,
			description TEXT,
			created_by UUID REFERENCES users(id),
			category VARCHAR(50) DEFAULT 'billing',
			change_type VARCHAR(50),
			changed_by UUID,
			delta BIGINT DEFAULT 0,
			field_name VARCHAR(100),
			reason TEXT,
			user_id UUID REFERENCES users(id),
			old_value BIGINT,
			new_value BIGINT,
			old_value_str VARCHAR(255),
			new_value_str VARCHAR(255),
			target_type VARCHAR(50),
			target_id VARCHAR(100),
			created_at TIMESTAMP DEFAULT NOW()
		)
	`); err != nil {

		return fmt.Errorf("create table enterprise_audit_log: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_enterprise_audit_log_enterprise ON enterprise_audit_log(enterprise_id, created_at DESC)`); err != nil {

		return fmt.Errorf("create index idx_enterprise_audit_log_enterprise: %w", err)

	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS enterprise_member_role_log (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			enterprise_id UUID NOT NULL REFERENCES enterprises(id),
			user_id UUID NOT NULL REFERENCES users(id),
			old_role VARCHAR(20),
			new_role VARCHAR(20),
			changed_by UUID REFERENCES users(id),
			reason TEXT,
			created_at TIMESTAMP DEFAULT NOW()
		)
	`); err != nil {

		return fmt.Errorf("create table enterprise_member_role_log: %w", err)

	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS openrouter_models (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			openrouter_id VARCHAR(200) UNIQUE NOT NULL,
			provider VARCHAR(50),
			name VARCHAR(200),
			input_price NUMERIC(10,6),
			output_price NUMERIC(10,6),
			context_window INTEGER,
			max_output_tokens INTEGER,
			capabilities JSONB DEFAULT '{}',
			raw_data JSONB,
			synced_at TIMESTAMP DEFAULT NOW(),
			created_at TIMESTAMP DEFAULT NOW()
		)
	`); err != nil {

		return fmt.Errorf("create table openrouter_models: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE openrouter_models ADD COLUMN IF NOT EXISTS max_output_tokens INTEGER`); err != nil {
		return fmt.Errorf("alter table openrouter_models add max_output_tokens: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE openrouter_models ADD COLUMN IF NOT EXISTS capabilities JSONB DEFAULT '{}'`); err != nil {
		return fmt.Errorf("alter table openrouter_models add capabilities: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_openrouter_models_provider ON openrouter_models(provider)`); err != nil {

		return fmt.Errorf("create index idx_openrouter_models_provider: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_openrouter_models_synced_at ON openrouter_models(synced_at)`); err != nil {

		return fmt.Errorf("create index idx_openrouter_models_synced_at: %w", err)

	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS channel_health (
			provider VARCHAR(50) PRIMARY KEY,
			latency_ms INTEGER DEFAULT 0,
			error_rate DECIMAL(7,4) DEFAULT 0,
			success_count INTEGER DEFAULT 0,
			failure_count INTEGER DEFAULT 0,
			status VARCHAR(20) DEFAULT 'unknown' CHECK (status IN ('healthy', 'degraded', 'unhealthy', 'unknown')),
			last_tested_at TIMESTAMP,
			last_error TEXT,
			updated_at TIMESTAMP DEFAULT NOW()
		)
	`); err != nil {

		return fmt.Errorf("create table channel_health: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS channel_health_history (
			id BIGSERIAL PRIMARY KEY,
			provider VARCHAR(50) NOT NULL,
			latency_ms INTEGER,
			status VARCHAR(20),
			error_message TEXT,
			tested_at TIMESTAMP DEFAULT NOW()
		)
	`); err != nil {

		return fmt.Errorf("create table channel_health_history: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_channel_health_provider ON channel_health_history(provider)`); err != nil {

		return fmt.Errorf("create index idx_channel_health_provider: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_channel_health_tested ON channel_health_history(tested_at DESC)`); err != nil {

		return fmt.Errorf("create index idx_channel_health_tested: %w", err)

	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS delivery_addresses (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			recipient_name VARCHAR(100) NOT NULL,
			phone VARCHAR(20),
			province VARCHAR(50),
			city VARCHAR(50),
			district VARCHAR(50),
			detail VARCHAR(500),
			postal_code VARCHAR(20),
			is_default BOOLEAN DEFAULT false,
			country VARCHAR(10) NOT NULL DEFAULT 'CN',
			deleted_at TIMESTAMP,
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW()
		)
	`); err != nil {

		return fmt.Errorf("create table delivery_addresses: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_delivery_addresses_user ON delivery_addresses(user_id)`); err != nil {

		return fmt.Errorf("create index idx_delivery_addresses_user: %w", err)

	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS enterprise_alerts (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			enterprise_id UUID NOT NULL REFERENCES enterprises(id),
			alert_type VARCHAR(50) NOT NULL,
			threshold_percent INTEGER NOT NULL,
			current_value DECIMAL(12,2),
			limit_value DECIMAL(12,2),
			notified_at TIMESTAMP DEFAULT NOW(),
			acknowledged_at TIMESTAMP,
			acknowledged_by UUID REFERENCES users(id)
		)
	`); err != nil {

		return fmt.Errorf("create table enterprise_alerts: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_enterprise_alerts_enterprise ON enterprise_alerts(enterprise_id)`); err != nil {

		return fmt.Errorf("create index idx_enterprise_alerts_enterprise: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_enterprise_alerts_type ON enterprise_alerts(alert_type)`); err != nil {

		return fmt.Errorf("create index idx_enterprise_alerts_type: %w", err)

	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS enterprise_alert_settings (
			enterprise_id UUID NOT NULL REFERENCES enterprises(id) ON DELETE CASCADE,
			alert_type VARCHAR(50) NOT NULL,
			threshold_percent INTEGER NOT NULL,
			enabled BOOLEAN DEFAULT true,
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW(),
			PRIMARY KEY (enterprise_id, alert_type, threshold_percent)
		)
	`); err != nil {

		return fmt.Errorf("create table enterprise_alert_settings: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_enterprise_alert_settings_enterprise ON enterprise_alert_settings(enterprise_id)`); err != nil {

		return fmt.Errorf("create index idx_enterprise_alert_settings_enterprise: %w", err)

	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS invoice_profiles (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			name VARCHAR(200) NOT NULL,
			type VARCHAR(20) DEFAULT 'normal' CHECK (type IN ('normal', 'special', 'personal', 'proforma')),
			tax_number VARCHAR(50),
			address VARCHAR(500),
			phone VARCHAR(20),
			bank_name VARCHAR(200),
			bank_account VARCHAR(50),
			is_default BOOLEAN DEFAULT false,
			email VARCHAR(255),
			country VARCHAR(10) DEFAULT 'CN',
			company_name VARCHAR(200),
			registered_address VARCHAR(500),
			deleted_at TIMESTAMP,
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW()
		)
	`); err != nil {

		return fmt.Errorf("create table invoice_profiles: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_invoice_profiles_user ON invoice_profiles(user_id)`); err != nil {

		return fmt.Errorf("create index idx_invoice_profiles_user: %w", err)

	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS invoice_requests (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			user_id UUID NOT NULL REFERENCES users(id),
			order_id UUID REFERENCES orders(id),
			profile_id UUID REFERENCES invoice_profiles(id),
			address_id UUID REFERENCES delivery_addresses(id),
			enterprise_id UUID REFERENCES enterprises(id),
			amount INTEGER NOT NULL,
			title VARCHAR(255),
			tax_number VARCHAR(50),
			status VARCHAR(20) DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'completed', 'rejected')),
			invoice_number VARCHAR(100),
			issued_at TIMESTAMP,
			rejection_reason TEXT,
			delivery_method VARCHAR(20) DEFAULT 'email',
			delivery_email VARCHAR(255),
			deleted_at TIMESTAMP,
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW()
		)
	`); err != nil {

		return fmt.Errorf("create table invoice_requests: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE invoice_requests ADD COLUMN IF NOT EXISTS invoice_type VARCHAR(20) NOT NULL DEFAULT 'general'`); err != nil {

		return fmt.Errorf("alter table invoice_requests add invoice_type: %w", err)

	}

	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_invoice_requests_user ON invoice_requests(user_id)`); err != nil {

		return fmt.Errorf("create index idx_invoice_requests_user: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_invoice_requests_status ON invoice_requests(status)`); err != nil {

		return fmt.Errorf("create index idx_invoice_requests_status: %w", err)

	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS key_pool_status (
			provider VARCHAR(50) NOT NULL,
			key_id VARCHAR(50) NOT NULL,
			key_preview VARCHAR(20),
			rpm INTEGER DEFAULT 0,
			tpm INTEGER DEFAULT 0,
			status VARCHAR(20) DEFAULT 'active',
			updated_at TIMESTAMP DEFAULT NOW(),
			PRIMARY KEY (provider, key_id)
		)
	`); err != nil {

		return fmt.Errorf("create table key_pool_status: %w", err)

	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS service_model_approvals (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			model_id UUID REFERENCES service_models(id),
			submitted_by UUID REFERENCES users(id),
			reviewed_by UUID REFERENCES users(id),
			status VARCHAR(20) DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'rejected')),
			notes TEXT,
			submitted_at TIMESTAMP DEFAULT NOW(),
			reviewed_at TIMESTAMP
		)
	`); err != nil {

		return fmt.Errorf("create table service_model_approvals: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_service_model_approvals_status ON service_model_approvals(status)`); err != nil {

		return fmt.Errorf("create index idx_service_model_approvals_status: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_service_model_approvals_model ON service_model_approvals(model_id)`); err != nil {

		return fmt.Errorf("create index idx_service_model_approvals_model: %w", err)

	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS sensitive_check_logs (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			user_id UUID,
			content_preview TEXT,
			matched_words TEXT,
			action VARCHAR(20) DEFAULT 'block' CHECK (action IN ('block', 'warn', 'allow')),
			checked_at TIMESTAMP DEFAULT NOW()
		)
	`); err != nil {

		return fmt.Errorf("create table sensitive_check_logs: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_sensitive_logs_user ON sensitive_check_logs(user_id)`); err != nil {

		return fmt.Errorf("create index idx_sensitive_logs_user: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_sensitive_logs_checked ON sensitive_check_logs(checked_at DESC)`); err != nil {

		return fmt.Errorf("create index idx_sensitive_logs_checked: %w", err)

	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS archival_jobs (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			job_type VARCHAR(20) NOT NULL CHECK (job_type IN ('warm_to_cold', 'cold_cleanup')),
			period_start DATE NOT NULL,
			period_end DATE NOT NULL,
			records_archived INTEGER DEFAULT 0,
			records_deleted INTEGER DEFAULT 0,
			storage_location VARCHAR(500),
			status VARCHAR(20) DEFAULT 'running' CHECK (status IN ('running', 'completed', 'failed')),
			error_message TEXT,
			started_at TIMESTAMP DEFAULT NOW(),
			completed_at TIMESTAMP
		)
	`); err != nil {

		return fmt.Errorf("create table archival_jobs: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_archival_jobs_type ON archival_jobs(job_type)`); err != nil {

		return fmt.Errorf("create index idx_archival_jobs_type: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_archival_jobs_status ON archival_jobs(status)`); err != nil {

		return fmt.Errorf("create index idx_archival_jobs_status: %w", err)

	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS email_notifications (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			user_id UUID REFERENCES users(id),
			email VARCHAR(255) NOT NULL,
			type VARCHAR(50) NOT NULL,
			subject VARCHAR(255) NOT NULL,
			body TEXT,
			status VARCHAR(20) DEFAULT 'pending' CHECK (status IN ('pending', 'sent', 'failed', 'bounced')),
			sent_at TIMESTAMP,
			error_message TEXT,
			created_at TIMESTAMP DEFAULT NOW()
		)
	`); err != nil {

		return fmt.Errorf("create table email_notifications: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_email_notifications_user ON email_notifications(user_id)`); err != nil {

		return fmt.Errorf("create index idx_email_notifications_user: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_email_notifications_status ON email_notifications(status)`); err != nil {

		return fmt.Errorf("create index idx_email_notifications_status: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_email_notifications_created ON email_notifications(created_at DESC)`); err != nil {

		return fmt.Errorf("create index idx_email_notifications_created: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE email_notifications ADD COLUMN IF NOT EXISTS link VARCHAR(512)`); err != nil {

		return fmt.Errorf("alter table email_notifications add link: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE email_notifications ADD COLUMN IF NOT EXISTS locale VARCHAR(10) NOT NULL DEFAULT 'en'`); err != nil {

		return fmt.Errorf("alter table email_notifications add locale: %w", err)

	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS model_products (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			model_id VARCHAR(100) UNIQUE NOT NULL,
			vendor VARCHAR(50),
			context_window INTEGER,
			release_date DATE,
			description TEXT,
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW()
		)
	`); err != nil {

		return fmt.Errorf("create table model_products: %w", err)

	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS pricing_sources (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			model_id VARCHAR(100) NOT NULL,
			source VARCHAR(20) CHECK (source IN ('official', 'openrouter', 'manual')),
			input_price DECIMAL(18,10),
			output_price DECIMAL(18,10),
			cache_read_price DECIMAL(18,10),
			currency VARCHAR(10) DEFAULT 'USD',
			fetched_at TIMESTAMP,
			raw_data JSONB,
			created_at TIMESTAMP DEFAULT NOW()
		)
	`); err != nil {

		return fmt.Errorf("create table pricing_sources: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_pricing_sources_model ON pricing_sources(model_id)`); err != nil {

		return fmt.Errorf("create index idx_pricing_sources_model: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_pricing_sources_fetched ON pricing_sources(fetched_at DESC)`); err != nil {

		return fmt.Errorf("create index idx_pricing_sources_fetched: %w", err)

	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS model_pricing (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			model_id VARCHAR(100) UNIQUE NOT NULL,
			input_price DECIMAL(18,10),
			output_price DECIMAL(18,10),
			cache_read_price DECIMAL(18,10),
			currency VARCHAR(10) DEFAULT 'USD',
			confirmed_by UUID REFERENCES users(id),
			confirmed_at TIMESTAMP,
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW()
		)
	`); err != nil {

		return fmt.Errorf("create table model_pricing: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_model_pricing_model ON model_pricing(model_id)`); err != nil {

		return fmt.Errorf("create index idx_model_pricing_model: %w", err)

	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS pricing_history (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			model_id VARCHAR(100) NOT NULL,
			input_price_old DECIMAL(18,10),
			output_price_old DECIMAL(18,10),
			input_price_new DECIMAL(18,10),
			output_price_new DECIMAL(18,10),
			changed_by UUID REFERENCES users(id),
			changed_at TIMESTAMP DEFAULT NOW()
		)
	`); err != nil {

		return fmt.Errorf("create table pricing_history: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_pricing_history_model ON pricing_history(model_id)`); err != nil {

		return fmt.Errorf("create index idx_pricing_history_model: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_pricing_history_changed ON pricing_history(changed_at DESC)`); err != nil {

		return fmt.Errorf("create index idx_pricing_history_changed: %w", err)

	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS single_model_purchases (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			api_key_id UUID REFERENCES api_keys(id) ON DELETE SET NULL,
			model_id VARCHAR(100) NOT NULL,
			model_name VARCHAR(255),
			amount_paid INTEGER NOT NULL,
			currency VARCHAR(10) DEFAULT 'CNY',
			token_quota BIGINT DEFAULT 0,
			token_used BIGINT DEFAULT 0,
			token_remaining BIGINT DEFAULT 0,
			status VARCHAR(20) DEFAULT 'active' CHECK (status IN ('active', 'exhausted', 'expired')),
			expires_at TIMESTAMP,
			deleted_at TIMESTAMP,
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW()
		)
	`); err != nil {

		return fmt.Errorf("create table single_model_purchases: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_single_model_purchases_user ON single_model_purchases(user_id)`); err != nil {

		return fmt.Errorf("create index idx_single_model_purchases_user: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_single_model_purchases_key ON single_model_purchases(api_key_id)`); err != nil {

		return fmt.Errorf("create index idx_single_model_purchases_key: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_single_model_purchases_status ON single_model_purchases(status)`); err != nil {

		return fmt.Errorf("create index idx_single_model_purchases_status: %w", err)

	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS super_admin_downgrade_requests (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			requester_id UUID NOT NULL REFERENCES users(id),
			target_id UUID NOT NULL REFERENCES users(id),
			status VARCHAR(20) DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'rejected', 'expired', 'frozen')),
			approved_by UUID REFERENCES users(id),
			approved_at TIMESTAMP,
			expires_at TIMESTAMP NOT NULL,
			frozen_at TIMESTAMP,
			created_at TIMESTAMP DEFAULT NOW()
		)
	`); err != nil {

		return fmt.Errorf("create table super_admin_downgrade_requests: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_downgrade_target ON super_admin_downgrade_requests(target_id)`); err != nil {

		return fmt.Errorf("create index idx_downgrade_target: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_downgrade_status ON super_admin_downgrade_requests(status)`); err != nil {

		return fmt.Errorf("create index idx_downgrade_status: %w", err)

	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS usage_summary_hourly (
			id BIGSERIAL PRIMARY KEY,
			hour TIMESTAMP NOT NULL,
			user_id UUID,
			api_key_id UUID,
			model VARCHAR(100),
			provider VARCHAR(50),
			request_count INTEGER DEFAULT 0,
			input_tokens BIGINT DEFAULT 0,
			output_tokens BIGINT DEFAULT 0,
			total_cost DECIMAL(18,10) DEFAULT 0,
			UNIQUE(hour, user_id, api_key_id, model, provider)
		)
	`); err != nil {

		return fmt.Errorf("create table usage_summary_hourly: %w", err)

	}

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS usage_summary_daily (
			id BIGSERIAL PRIMARY KEY,
			day DATE NOT NULL,
			user_id UUID,
			api_key_id UUID,
			model VARCHAR(100),
			provider VARCHAR(50),
			request_count INTEGER DEFAULT 0,
			input_tokens BIGINT DEFAULT 0,
			output_tokens BIGINT DEFAULT 0,
			total_cost DECIMAL(18,10) DEFAULT 0,
			UNIQUE(day, user_id, api_key_id, model, provider)
		)
	`); err != nil {

		return fmt.Errorf("create table usage_summary_daily: %w", err)

	}

	// proxy_request_audits is the audit trail for the proxy-side audit logger
	// (POST /webhook/usage/batch). Unlike usage_records it also captures failed
	// upstream requests, so error analysis does not depend on billing records.
	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS proxy_request_audits (
			id BIGSERIAL PRIMARY KEY,
			trace_id UUID,
			api_key_hash TEXT,
			model TEXT,
			provider TEXT,
			region TEXT,
			upstream_key_id TEXT,
			endpoint TEXT,
			status TEXT,
			error TEXT,
			error_code TEXT,
			input_tokens BIGINT DEFAULT 0,
			output_tokens BIGINT DEFAULT 0,
			cached_tokens BIGINT DEFAULT 0,
			latency_ms BIGINT DEFAULT 0,
			request_count INTEGER DEFAULT 1,
			is_streaming BOOLEAN DEFAULT false,
			client_ip TEXT,
			pricing_ratio NUMERIC(7,4),
			routing_track TEXT,
			server_id TEXT,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`); err != nil {

		return fmt.Errorf("create table proxy_request_audits: %w", err)

	}

	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_proxy_request_audits_created_at ON proxy_request_audits(created_at)`); err != nil {

		return fmt.Errorf("create index idx_proxy_request_audits_created_at: %w", err)

	}

	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_proxy_request_audits_model_created_at ON proxy_request_audits(model, created_at)`); err != nil {

		return fmt.Errorf("create index idx_proxy_request_audits_model_created_at: %w", err)

	}

	// Migration 0033: audit completeness + per-enterprise retention policy.
	// proxy_request_audits gains the Proxy-reported user agent / HTTP status and
	// a server-resolved enterprise_id (nullable, no FK: audit rows must outlive
	// enterprises). trace_id gets a NON-unique index; idempotency is enforced by
	// the insert itself so legacy duplicates can never block startup.
	for _, stmt := range []string{
		`ALTER TABLE proxy_request_audits ADD COLUMN IF NOT EXISTS user_agent TEXT`,
		`ALTER TABLE proxy_request_audits ADD COLUMN IF NOT EXISTS http_status INTEGER`,
		`ALTER TABLE proxy_request_audits ADD COLUMN IF NOT EXISTS enterprise_id UUID`,
		`CREATE INDEX IF NOT EXISTS idx_proxy_request_audits_trace_id ON proxy_request_audits(trace_id)`,
		`CREATE INDEX IF NOT EXISTS idx_proxy_request_audits_enterprise_created ON proxy_request_audits(enterprise_id, created_at) WHERE enterprise_id IS NOT NULL`,
		`ALTER TABLE enterprise_audit_log ADD COLUMN IF NOT EXISTS ip_address INET`,
		`ALTER TABLE enterprise_audit_log ADD COLUMN IF NOT EXISTS user_agent TEXT`,
		`ALTER TABLE enterprises ADD COLUMN IF NOT EXISTS retention_mode VARCHAR(16) NOT NULL DEFAULT 'standard'`,
		`ALTER TABLE enterprises ADD COLUMN IF NOT EXISTS retention_days INTEGER NOT NULL DEFAULT 90`,
		`ALTER TABLE enterprises ADD COLUMN IF NOT EXISTS retention_updated_at TIMESTAMPTZ`,
		`ALTER TABLE enterprises ADD COLUMN IF NOT EXISTS retention_updated_by UUID`,
		`ALTER TABLE enterprises ADD COLUMN IF NOT EXISTS retention_last_purge_at TIMESTAMPTZ`,
		`ALTER TABLE enterprises ADD COLUMN IF NOT EXISTS retention_last_purge_by UUID`,
		`ALTER TABLE enterprises ADD COLUMN IF NOT EXISTS retention_last_purge_status VARCHAR(16)`,
		`DO $$ BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_enterprises_retention_mode') THEN
				ALTER TABLE enterprises ADD CONSTRAINT chk_enterprises_retention_mode
					CHECK (retention_mode IN ('off', 'metadata', 'standard'));
			END IF;
			IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_enterprises_retention_days') THEN
				ALTER TABLE enterprises ADD CONSTRAINT chk_enterprises_retention_days
					CHECK (retention_days BETWEEN 1 AND 365);
			END IF;
		END $$`,
	} {
		if _, err := db.Pool.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("apply audit/retention schema (0033): %w", err)
		}
	}

	// NOTE: the one-off enterprise_audit_log.action backfill lives ONLY in
	// migration 0033. It must not run here: InitSchema executes on every boot and
	// `action` becomes a hashed, immutable column once the audit chain (0034) exists.

	// ============================================================================
	// Phase: Add columns to tables (moved after all CREATE TABLE for clean init)
	// ============================================================================

	if _, err := db.Pool.Exec(ctx, `ALTER TABLE users ADD COLUMN IF NOT EXISTS enterprise_id UUID REFERENCES enterprises(id)`); err != nil {

		return fmt.Errorf("alter table users add enterprise_id: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE users ADD COLUMN IF NOT EXISTS quota_limit BIGINT DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table users add quota_limit: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE users ADD COLUMN IF NOT EXISTS group_id UUID REFERENCES user_groups(id)`); err != nil {

		return fmt.Errorf("alter table users add group_id: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE users ADD COLUMN IF NOT EXISTS user_type VARCHAR(20) DEFAULT 'general'`); err != nil {

		return fmt.Errorf("alter table users add user_type: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE users ADD COLUMN IF NOT EXISTS locale VARCHAR(10) DEFAULT 'en'`); err != nil {

		return fmt.Errorf("alter table users add locale: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE users ADD COLUMN IF NOT EXISTS theme VARCHAR(20) DEFAULT 'system'`); err != nil {

		return fmt.Errorf("alter table users add theme: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE users ADD COLUMN IF NOT EXISTS login_attempts INTEGER DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table users add login_attempts: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE users ADD COLUMN IF NOT EXISTS locked_until TIMESTAMP`); err != nil {

		return fmt.Errorf("alter table users add locked_until: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE users ADD COLUMN IF NOT EXISTS last_login_at TIMESTAMP`); err != nil {

		return fmt.Errorf("alter table users add last_login_at: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE users ADD COLUMN IF NOT EXISTS remember_me_token VARCHAR(255)`); err != nil {

		return fmt.Errorf("alter table users add remember_me_token: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE users ADD COLUMN IF NOT EXISTS remember_me_expires_at TIMESTAMP`); err != nil {

		return fmt.Errorf("alter table users add remember_me_expires_at: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE users ADD COLUMN IF NOT EXISTS aff_code VARCHAR(8)`); err != nil {

		return fmt.Errorf("alter table users add aff_code: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE users ADD COLUMN IF NOT EXISTS inviter_id UUID REFERENCES users(id)`); err != nil {

		return fmt.Errorf("alter table users add inviter_id: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE users ADD COLUMN IF NOT EXISTS aff_quota BIGINT DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table users add aff_quota: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE users ADD COLUMN IF NOT EXISTS aff_history_quota BIGINT DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table users add aff_history_quota: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS key_type VARCHAR(20) DEFAULT 'general'`); err != nil {

		return fmt.Errorf("alter table api_keys add key_type: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS group_id UUID REFERENCES user_groups(id)`); err != nil {

		return fmt.Errorf("alter table api_keys add group_id: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS group_priority JSONB DEFAULT '[]'`); err != nil {

		return fmt.Errorf("alter table api_keys add group_priority: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS current_model VARCHAR(100)`); err != nil {

		return fmt.Errorf("alter table api_keys add current_model: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS billing_mode VARCHAR(20) DEFAULT 'token'`); err != nil {

		return fmt.Errorf("alter table api_keys add billing_mode: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS model_routing_mode VARCHAR(20) DEFAULT 'single'`); err != nil {

		return fmt.Errorf("alter table api_keys add model_routing_mode: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS selected_models JSONB DEFAULT '[]'`); err != nil {

		return fmt.Errorf("alter table api_keys add selected_models: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS enterprise_id UUID REFERENCES enterprises(id)`); err != nil {

		return fmt.Errorf("alter table api_keys add enterprise_id: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS assigned_user_id UUID REFERENCES users(id)`); err != nil {

		return fmt.Errorf("alter table api_keys add assigned_user_id: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS subscription_id UUID`); err != nil {

		return fmt.Errorf("alter table api_keys add subscription_id: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS rate_limit_tpm INTEGER DEFAULT 100000`); err != nil {

		return fmt.Errorf("alter table api_keys add rate_limit_tpm: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS expires_at TIMESTAMP`); err != nil {

		return fmt.Errorf("alter table api_keys add expires_at: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE enterprises ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ`); err != nil {

		return fmt.Errorf("alter table enterprises add deleted_at: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE enterprises ADD COLUMN IF NOT EXISTS credit_total BIGINT DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table enterprises add credit_total: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE enterprises ADD COLUMN IF NOT EXISTS credit_used BIGINT DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table enterprises add credit_used: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE enterprises ADD COLUMN IF NOT EXISTS prepaid_balance BIGINT NOT NULL DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table enterprises add prepaid_balance: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE enterprises ADD COLUMN IF NOT EXISTS credit_reset_at TIMESTAMP`); err != nil {

		return fmt.Errorf("alter table enterprises add credit_reset_at: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE enterprises ADD COLUMN IF NOT EXISTS last_payment_amount BIGINT`); err != nil {

		return fmt.Errorf("alter table enterprises add last_payment_amount: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE enterprises ADD COLUMN IF NOT EXISTS last_payment_confirmed_at TIMESTAMP`); err != nil {

		return fmt.Errorf("alter table enterprises add last_payment_confirmed_at: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE enterprises ADD COLUMN IF NOT EXISTS last_payment_confirmed_by UUID REFERENCES users(id)`); err != nil {

		return fmt.Errorf("alter table enterprises add last_payment_confirmed_by: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE enterprises ADD COLUMN IF NOT EXISTS max_admins INTEGER DEFAULT 3`); err != nil {

		return fmt.Errorf("alter table enterprises add max_admins: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE enterprises ADD COLUMN IF NOT EXISTS admin_vacant_since TIMESTAMPTZ`); err != nil {

		return fmt.Errorf("alter table enterprises add admin_vacant_since: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE enterprises ADD COLUMN IF NOT EXISTS deleted_by UUID REFERENCES users(id)`); err != nil {

		return fmt.Errorf("alter table enterprises add deleted_by: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE enterprises ADD COLUMN IF NOT EXISTS delete_reason TEXT`); err != nil {

		return fmt.Errorf("alter table enterprises add delete_reason: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE enterprises ADD COLUMN IF NOT EXISTS pricing_ratio DECIMAL(7,4) DEFAULT 1.0000`); err != nil {

		return fmt.Errorf("alter table enterprises add pricing_ratio: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE subscription_plans ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMP`); err != nil {

		return fmt.Errorf("alter table subscription_plans add deleted_at: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE user_groups ADD COLUMN IF NOT EXISTS group_type VARCHAR(20) DEFAULT 'general'`); err != nil {

		return fmt.Errorf("alter table user_groups add group_type: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE user_groups ADD COLUMN IF NOT EXISTS billing_mode VARCHAR(20) DEFAULT 'token'`); err != nil {

		return fmt.Errorf("alter table user_groups add billing_mode: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE user_groups ADD COLUMN IF NOT EXISTS models JSONB DEFAULT '[]'`); err != nil {

		return fmt.Errorf("alter table user_groups add models: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE user_groups ADD COLUMN IF NOT EXISTS rpm INT DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table user_groups add rpm: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE user_groups ADD COLUMN IF NOT EXISTS rph BIGINT DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table user_groups add rph: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE user_groups ADD COLUMN IF NOT EXISTS rp5h BIGINT DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table user_groups add rp5h: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE user_groups ADD COLUMN IF NOT EXISTS rpw BIGINT DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table user_groups add rpw: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE user_groups ADD COLUMN IF NOT EXISTS rpmonth BIGINT DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table user_groups add rpmonth: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE user_groups ADD COLUMN IF NOT EXISTS tpm BIGINT DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table user_groups add tpm: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE user_groups ADD COLUMN IF NOT EXISTS tph BIGINT DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table user_groups add tph: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE user_groups ADD COLUMN IF NOT EXISTS tpd BIGINT DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table user_groups add tpd: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE user_groups ADD COLUMN IF NOT EXISTS tpw BIGINT DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table user_groups add tpw: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE user_groups ADD COLUMN IF NOT EXISTS tpmonth BIGINT DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table user_groups add tpmonth: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE user_groups ADD COLUMN IF NOT EXISTS rate_limit_tpm INTEGER`); err != nil {

		return fmt.Errorf("alter table user_groups add rate_limit_tpm: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE user_groups ADD COLUMN IF NOT EXISTS enterprise_id UUID REFERENCES enterprises(id)`); err != nil {

		return fmt.Errorf("alter table user_groups add enterprise_id: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE user_groups ADD COLUMN IF NOT EXISTS visibility VARCHAR(20) DEFAULT 'public'`); err != nil {

		return fmt.Errorf("alter table user_groups add visibility: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE user_groups ADD COLUMN IF NOT EXISTS is_default BOOLEAN DEFAULT false`); err != nil {

		return fmt.Errorf("alter table user_groups add is_default: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE user_groups ADD COLUMN IF NOT EXISTS routing_track VARCHAR(20) DEFAULT 'standard'`); err != nil {

		return fmt.Errorf("alter table user_groups add routing_track: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE user_groups ADD COLUMN IF NOT EXISTS concurrent_limit INT DEFAULT 10`); err != nil {

		return fmt.Errorf("alter table user_groups add concurrent_limit: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE user_groups ADD COLUMN IF NOT EXISTS parent_group_id UUID REFERENCES user_groups(id)`); err != nil {

		return fmt.Errorf("alter table user_groups add parent_group_id: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE user_groups ADD COLUMN IF NOT EXISTS allowed_enterprise_ids JSONB DEFAULT '[]'`); err != nil {

		return fmt.Errorf("alter table user_groups add allowed_enterprise_ids: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE user_groups ADD COLUMN IF NOT EXISTS inherit_to_enterprises BOOLEAN NOT NULL DEFAULT FALSE`); err != nil {

		return fmt.Errorf("alter table user_groups add inherit_to_enterprises: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE enterprises ADD COLUMN IF NOT EXISTS description TEXT`); err != nil {

		return fmt.Errorf("alter table enterprises add description: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE enterprises ADD COLUMN IF NOT EXISTS contact_name VARCHAR(255)`); err != nil {

		return fmt.Errorf("alter table enterprises add contact_name: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE enterprises ADD COLUMN IF NOT EXISTS contact_phone VARCHAR(50)`); err != nil {

		return fmt.Errorf("alter table enterprises add contact_phone: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE enterprises ADD COLUMN IF NOT EXISTS max_members INTEGER DEFAULT 50`); err != nil {

		return fmt.Errorf("alter table enterprises add max_members: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE enterprises ADD COLUMN IF NOT EXISTS allowed_domains TEXT DEFAULT ''`); err != nil {

		return fmt.Errorf("alter table enterprises add allowed_domains: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE users ADD COLUMN IF NOT EXISTS timezone VARCHAR(50) DEFAULT 'UTC'`); err != nil {

		return fmt.Errorf("alter table users add timezone: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE users ADD COLUMN IF NOT EXISTS quota_total BIGINT DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table users add quota_total: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE users ADD COLUMN IF NOT EXISTS quota_used BIGINT DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table users add quota_used: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE users ADD COLUMN IF NOT EXISTS credit_used BIGINT DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table users add credit_used: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE users ADD COLUMN IF NOT EXISTS quota_remaining BIGINT DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table users add quota_remaining: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE users ADD COLUMN IF NOT EXISTS gift_balance BIGINT DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table users add gift_balance: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE subscription_plans ADD COLUMN IF NOT EXISTS tpm_limit INTEGER DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table subscription_plans add tpm_limit: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE subscription_plans ADD COLUMN IF NOT EXISTS tph_limit INTEGER DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table subscription_plans add tph_limit: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE subscription_plans ADD COLUMN IF NOT EXISTS rpm_limit INTEGER DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table subscription_plans add rpm_limit: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE subscription_plans ADD COLUMN IF NOT EXISTS rph_limit INTEGER DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table subscription_plans add rph_limit: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE subscription_plans ADD COLUMN IF NOT EXISTS concurrent_limit INTEGER DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table subscription_plans add concurrent_limit: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE subscription_plans ADD COLUMN IF NOT EXISTS tpd_limit BIGINT DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table subscription_plans add tpd_limit: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE subscription_plans ADD COLUMN IF NOT EXISTS tpw_limit BIGINT DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table subscription_plans add tpw_limit: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE subscription_plans ADD COLUMN IF NOT EXISTS rpd_limit INTEGER DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table subscription_plans add rpd_limit: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE subscription_plans ADD COLUMN IF NOT EXISTS rpw_limit INTEGER DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table subscription_plans add rpw_limit: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE subscription_plans ADD COLUMN IF NOT EXISTS rp5h_limit INTEGER DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table subscription_plans add rp5h_limit: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS tpm_limit INTEGER DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table user_subscriptions add tpm_limit: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS tph_limit INTEGER DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table user_subscriptions add tph_limit: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS rpm_limit INTEGER DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table user_subscriptions add rpm_limit: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS rph_limit INTEGER DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table user_subscriptions add rph_limit: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS concurrent_limit INTEGER DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table user_subscriptions add concurrent_limit: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS tpd_limit BIGINT DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table user_subscriptions add tpd_limit: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS tpw_limit BIGINT DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table user_subscriptions add tpw_limit: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS rpd_limit INTEGER DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table user_subscriptions add rpd_limit: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS rpw_limit INTEGER DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table user_subscriptions add rpw_limit: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS rp5h_limit INTEGER DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table user_subscriptions add rp5h_limit: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS tpm_used INTEGER DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table user_subscriptions add tpm_used: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS tph_used INTEGER DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table user_subscriptions add tph_used: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS rpm_used INTEGER DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table user_subscriptions add rpm_used: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS rph_used INTEGER DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table user_subscriptions add rph_used: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS tpw_used BIGINT DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table user_subscriptions add tpw_used: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS rpw_used INTEGER DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table user_subscriptions add rpw_used: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS rp5h_used INTEGER DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table user_subscriptions add rp5h_used: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS tpm_reset_at TIMESTAMP`); err != nil {

		return fmt.Errorf("alter table user_subscriptions add tpm_reset_at: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS tph_reset_at TIMESTAMP`); err != nil {

		return fmt.Errorf("alter table user_subscriptions add tph_reset_at: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS rpm_reset_at TIMESTAMP`); err != nil {

		return fmt.Errorf("alter table user_subscriptions add rpm_reset_at: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS rph_reset_at TIMESTAMP`); err != nil {

		return fmt.Errorf("alter table user_subscriptions add rph_reset_at: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS tpw_reset_at TIMESTAMP`); err != nil {

		return fmt.Errorf("alter table user_subscriptions add tpw_reset_at: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS rpw_reset_at TIMESTAMP`); err != nil {

		return fmt.Errorf("alter table user_subscriptions add rpw_reset_at: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS rp5h_reset_at TIMESTAMP`); err != nil {

		return fmt.Errorf("alter table user_subscriptions add rp5h_reset_at: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS tpmonth_limit BIGINT DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table user_subscriptions add tpmonth_limit: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS tpmonth_used BIGINT DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table user_subscriptions add tpmonth_used: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS tpmonth_reset_at TIMESTAMP`); err != nil {

		return fmt.Errorf("alter table user_subscriptions add tpmonth_reset_at: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE usage_records ADD COLUMN IF NOT EXISTS group_id UUID`); err != nil {

		return fmt.Errorf("alter table usage_records add group_id: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE usage_records ADD COLUMN IF NOT EXISTS actual_group_id UUID`); err != nil {

		return fmt.Errorf("alter table usage_records add actual_group_id: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE usage_records ADD COLUMN IF NOT EXISTS is_fallback BOOLEAN DEFAULT false`); err != nil {

		return fmt.Errorf("alter table usage_records add is_fallback: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE usage_records ADD COLUMN IF NOT EXISTS pricing_ratio_at_request DECIMAL(7,4) DEFAULT 1.0000`); err != nil {

		return fmt.Errorf("alter table usage_records add pricing_ratio_at_request: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE usage_records ADD COLUMN IF NOT EXISTS gift_deduction DECIMAL(18,10) DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table usage_records add gift_deduction: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE usage_records ADD COLUMN IF NOT EXISTS recharge_deduction DECIMAL(18,10) DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table usage_records add recharge_deduction: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE usage_records ADD COLUMN IF NOT EXISTS credit_deduction DECIMAL(18,10) DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table usage_records add credit_deduction: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE usage_records ADD COLUMN IF NOT EXISTS subscription_deduction DECIMAL(18,10) DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table usage_records add subscription_deduction: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE usage_records ADD COLUMN IF NOT EXISTS balance_deduction BIGINT DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table usage_records add balance_deduction: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE usage_records ADD COLUMN IF NOT EXISTS project_name VARCHAR(255)`); err != nil {

		return fmt.Errorf("alter table usage_records add project_name: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE usage_records ADD COLUMN IF NOT EXISTS project_id VARCHAR(255)`); err != nil {

		return fmt.Errorf("alter table usage_records add project_id: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE usage_records ADD COLUMN IF NOT EXISTS api_key_name VARCHAR(255)`); err != nil {

		return fmt.Errorf("alter table usage_records add api_key_name: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE usage_records ADD COLUMN IF NOT EXISTS key_prefix VARCHAR(50)`); err != nil {

		return fmt.Errorf("alter table usage_records add key_prefix: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE usage_records ADD COLUMN IF NOT EXISTS original_track VARCHAR(20) DEFAULT NULL`); err != nil {

		return fmt.Errorf("alter table usage_records add original_track: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE service_models ADD COLUMN IF NOT EXISTS protected BOOLEAN DEFAULT false`); err != nil {

		return fmt.Errorf("alter table service_models add protected: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE service_models ADD COLUMN IF NOT EXISTS last_group_cleanup_at TIMESTAMP`); err != nil {

		return fmt.Errorf("alter table service_models add last_group_cleanup_at: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE service_models ADD COLUMN IF NOT EXISTS last_eol_notified_at TIMESTAMP`); err != nil {

		return fmt.Errorf("alter table service_models add last_eol_notified_at: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE service_models ADD COLUMN IF NOT EXISTS hidden BOOLEAN DEFAULT false`); err != nil {

		return fmt.Errorf("alter table service_models add hidden: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE service_models ADD COLUMN IF NOT EXISTS is_hot BOOLEAN DEFAULT false`); err != nil {
		return fmt.Errorf("alter table service_models add is_hot: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE service_models ADD COLUMN IF NOT EXISTS input_price_cache_hit NUMERIC(12,8)`); err != nil {

		return fmt.Errorf("alter table service_models add input_price_cache_hit: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE service_models ADD COLUMN IF NOT EXISTS input_price_cache_miss NUMERIC(12,8)`); err != nil {

		return fmt.Errorf("alter table service_models add input_price_cache_miss: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE service_models ADD COLUMN IF NOT EXISTS reasoning_price NUMERIC(12,8)`); err != nil {

		return fmt.Errorf("alter table service_models add reasoning_price: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE service_models ADD COLUMN IF NOT EXISTS cache_write_price NUMERIC(12,8)`); err != nil {

		return fmt.Errorf("alter table service_models add cache_write_price: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE service_models ADD COLUMN IF NOT EXISTS billing_type VARCHAR(20) DEFAULT 'per_token'`); err != nil {

		return fmt.Errorf("alter table service_models add billing_type: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE service_models ADD COLUMN IF NOT EXISTS validation_status VARCHAR(20) DEFAULT 'pending' CHECK (validation_status IN ('pending', 'valid', 'invalid', 'needs_review'))`); err != nil {

		return fmt.Errorf("alter table service_models add validation_status: %w", err)

	}
	// Drop the original CHECK constraint (from CREATE TABLE) and replace with
	// one that includes 'skipped' for non-text models and 'needs_review'.
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE service_models DROP CONSTRAINT IF EXISTS service_models_validation_status_check`); err != nil {

		return fmt.Errorf("drop service_models_validation_status_check: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE service_models ADD CONSTRAINT service_models_validation_status_check CHECK (validation_status IN ('pending', 'valid', 'invalid', 'needs_review', 'skipped', 'unknown'))`); err != nil {

		return fmt.Errorf("alter table service_models add validation_status_check (with skipped): %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE service_models ADD COLUMN IF NOT EXISTS validation_result JSONB`); err != nil {

		return fmt.Errorf("alter table service_models add validation_result: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE service_models ADD COLUMN IF NOT EXISTS validated_at TIMESTAMP`); err != nil {

		return fmt.Errorf("alter table service_models add validated_at: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE service_models ADD COLUMN IF NOT EXISTS suggested_api_name VARCHAR(100)`); err != nil {

		return fmt.Errorf("alter table service_models add suggested_api_name: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE service_models ADD COLUMN IF NOT EXISTS avg_latency_ms INT`); err != nil {

		return fmt.Errorf("alter table service_models add avg_latency_ms: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE service_models ADD COLUMN IF NOT EXISTS success_rate NUMERIC(5,4) DEFAULT 1.0`); err != nil {

		return fmt.Errorf("alter table service_models add success_rate: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE service_models ADD COLUMN IF NOT EXISTS priority INT DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table service_models add priority: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE service_models ADD COLUMN IF NOT EXISTS fixed_price NUMERIC(12,8)`); err != nil {

		return fmt.Errorf("alter table service_models add fixed_price: %w", err)

	}

	// Normalize all service_models price columns to the same precision so later
	// migrations and price imports are not truncated differently.
	if _, err := db.Pool.Exec(ctx, `
		ALTER TABLE service_models
			ALTER COLUMN input_price TYPE NUMERIC(12,8),
			ALTER COLUMN output_price TYPE NUMERIC(12,8),
			ALTER COLUMN cache_read_price TYPE NUMERIC(12,8),
			ALTER COLUMN input_price_cache_hit TYPE NUMERIC(12,8),
			ALTER COLUMN input_price_cache_miss TYPE NUMERIC(12,8),
			ALTER COLUMN reasoning_price TYPE NUMERIC(12,8),
			ALTER COLUMN cache_write_price TYPE NUMERIC(12,8),
			ALTER COLUMN fixed_price TYPE NUMERIC(12,8)
	`); err != nil {
		return fmt.Errorf("normalize service_models price column precision: %w", err)
	}

	if _, err := db.Pool.Exec(ctx, `ALTER TABLE service_models ADD COLUMN IF NOT EXISTS tiered_pricing JSONB`); err != nil {

		return fmt.Errorf("alter table service_models add tiered_pricing: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE service_models ADD COLUMN IF NOT EXISTS eol_announced_at TIMESTAMP`); err != nil {

		return fmt.Errorf("alter table service_models add eol_announced_at: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE service_models ADD COLUMN IF NOT EXISTS eol_service_at TIMESTAMP`); err != nil {

		return fmt.Errorf("alter table service_models add eol_service_at: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE service_models ADD COLUMN IF NOT EXISTS eol_purge_at TIMESTAMP`); err != nil {

		return fmt.Errorf("alter table service_models add eol_purge_at: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE service_models ADD COLUMN IF NOT EXISTS eol_confirmed_by UUID`); err != nil {

		return fmt.Errorf("alter table service_models add eol_confirmed_by: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE service_models ADD COLUMN IF NOT EXISTS eol_confirmed_at TIMESTAMP`); err != nil {

		return fmt.Errorf("alter table service_models add eol_confirmed_at: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE service_models ADD COLUMN IF NOT EXISTS eol_notes TEXT`); err != nil {

		return fmt.Errorf("alter table service_models add eol_notes: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE service_models ADD COLUMN IF NOT EXISTS paused_at TIMESTAMP`); err != nil {

		return fmt.Errorf("alter table service_models add paused_at: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE service_models ADD COLUMN IF NOT EXISTS paused_reason TEXT`); err != nil {

		return fmt.Errorf("alter table service_models add paused_reason: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE service_models ADD COLUMN IF NOT EXISTS paused_notes TEXT`); err != nil {

		return fmt.Errorf("alter table service_models add paused_notes: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE service_models ADD COLUMN IF NOT EXISTS discontinued_at TIMESTAMP`); err != nil {

		return fmt.Errorf("alter table service_models add discontinued_at: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE service_models ADD COLUMN IF NOT EXISTS discontinued_reason TEXT`); err != nil {

		return fmt.Errorf("alter table service_models add discontinued_reason: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE service_models ADD COLUMN IF NOT EXISTS discontinued_notes TEXT`); err != nil {

		return fmt.Errorf("alter table service_models add discontinued_notes: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE service_models ADD COLUMN IF NOT EXISTS confirmed_by UUID`); err != nil {

		return fmt.Errorf("alter table service_models add confirmed_by: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE service_models ADD COLUMN IF NOT EXISTS confirmed_at TIMESTAMP`); err != nil {

		return fmt.Errorf("alter table service_models add confirmed_at: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE service_models ADD COLUMN IF NOT EXISTS provider_aliases JSONB`); err != nil {

		return fmt.Errorf("alter table service_models add provider_aliases: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE service_models ADD COLUMN IF NOT EXISTS vendor VARCHAR(50)`); err != nil {

		return fmt.Errorf("alter table service_models add vendor: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE service_models ADD COLUMN IF NOT EXISTS context_window INTEGER`); err != nil {

		return fmt.Errorf("alter table service_models add context_window: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE service_models ADD COLUMN IF NOT EXISTS max_output_tokens INTEGER`); err != nil {

		return fmt.Errorf("alter table service_models add max_output_tokens: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS target_type VARCHAR(50)`); err != nil {

		return fmt.Errorf("alter table audit_logs add target_type: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS target_id VARCHAR(100)`); err != nil {

		return fmt.Errorf("alter table audit_logs add target_id: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS old_value JSONB`); err != nil {

		return fmt.Errorf("alter table audit_logs add old_value: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS new_value JSONB`); err != nil {

		return fmt.Errorf("alter table audit_logs add new_value: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMP`); err != nil {

		return fmt.Errorf("alter table audit_logs add deleted_at: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS deleted_by UUID REFERENCES users(id)`); err != nil {

		return fmt.Errorf("alter table audit_logs add deleted_by: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE upstream_keys ADD COLUMN IF NOT EXISTS rpm_limit INTEGER DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table upstream_keys add rpm_limit: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE upstream_keys ADD COLUMN IF NOT EXISTS tpm_limit INTEGER DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table upstream_keys add tpm_limit: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE upstream_keys ADD COLUMN IF NOT EXISTS model_limits JSONB`); err != nil {

		return fmt.Errorf("alter table upstream_keys add model_limits: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE upstream_keys ADD COLUMN IF NOT EXISTS quota_limit BIGINT DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table upstream_keys add quota_limit: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE upstream_keys ADD COLUMN IF NOT EXISTS quota_used BIGINT DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table upstream_keys add quota_used: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE upstream_keys ADD COLUMN IF NOT EXISTS validation_status VARCHAR(20) DEFAULT 'pending'`); err != nil {

		return fmt.Errorf("alter table upstream_keys add validation_status: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE enterprise_members ADD COLUMN IF NOT EXISTS quota_limit BIGINT DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table enterprise_members add quota_limit: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE enterprise_members ADD COLUMN IF NOT EXISTS credit_limit BIGINT DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table enterprise_members add credit_limit: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE enterprise_members ADD COLUMN IF NOT EXISTS joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW()`); err != nil {

		return fmt.Errorf("alter table enterprise_members add joined_at: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE announcements ADD COLUMN IF NOT EXISTS type VARCHAR(50) DEFAULT 'info'`); err != nil {

		return fmt.Errorf("alter table announcements add type: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE announcements ADD COLUMN IF NOT EXISTS pinned BOOLEAN DEFAULT false`); err != nil {

		return fmt.Errorf("alter table announcements add pinned: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE announcements ADD COLUMN IF NOT EXISTS show_popup BOOLEAN DEFAULT false`); err != nil {

		return fmt.Errorf("alter table announcements add show_popup: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE announcements ADD COLUMN IF NOT EXISTS priority INTEGER DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table announcements add priority: %w", err)

	}
	// Announcement extension fields for promo banners and onboarding wizards
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE announcements ADD COLUMN IF NOT EXISTS display_mode VARCHAR(20) DEFAULT 'modal'`); err != nil {

		return fmt.Errorf("alter table announcements add display_mode: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE announcements ADD COLUMN IF NOT EXISTS cta_text VARCHAR(100)`); err != nil {

		return fmt.Errorf("alter table announcements add cta_text: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE announcements ADD COLUMN IF NOT EXISTS cta_link VARCHAR(500)`); err != nil {

		return fmt.Errorf("alter table announcements add cta_link: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE announcements ADD COLUMN IF NOT EXISTS banner_color VARCHAR(20) DEFAULT 'primary'`); err != nil {

		return fmt.Errorf("alter table announcements add banner_color: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE announcements ADD COLUMN IF NOT EXISTS dismiss_mode VARCHAR(20) DEFAULT 'session'`); err != nil {

		return fmt.Errorf("alter table announcements add dismiss_mode: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE announcements ADD COLUMN IF NOT EXISTS target_audience VARCHAR(20) DEFAULT 'all'`); err != nil {

		return fmt.Errorf("alter table announcements add target_audience: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE announcements ADD COLUMN IF NOT EXISTS icon_emoji VARCHAR(10)`); err != nil {

		return fmt.Errorf("alter table announcements add icon_emoji: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE balance_transactions ADD COLUMN IF NOT EXISTS balance_type VARCHAR(20) DEFAULT 'gift'`); err != nil {

		return fmt.Errorf("alter table balance_transactions add balance_type: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE balance_transactions ADD COLUMN IF NOT EXISTS reference_id VARCHAR(255)`); err != nil {

		return fmt.Errorf("alter table balance_transactions add reference_id: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE channel_health ADD COLUMN IF NOT EXISTS last_error TEXT`); err != nil {

		return fmt.Errorf("alter table channel_health add last_error: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE enterprise_audit_log ADD COLUMN IF NOT EXISTS change_type VARCHAR(50)`); err != nil {

		return fmt.Errorf("alter table enterprise_audit_log add change_type: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE enterprise_audit_log ADD COLUMN IF NOT EXISTS changed_by UUID`); err != nil {

		return fmt.Errorf("alter table enterprise_audit_log add changed_by: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE enterprise_audit_log ADD COLUMN IF NOT EXISTS delta BIGINT DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table enterprise_audit_log add delta: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE enterprise_audit_log ADD COLUMN IF NOT EXISTS field_name VARCHAR(100)`); err != nil {

		return fmt.Errorf("alter table enterprise_audit_log add field_name: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE enterprise_audit_log ADD COLUMN IF NOT EXISTS reason TEXT`); err != nil {

		return fmt.Errorf("alter table enterprise_audit_log add reason: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE enterprise_audit_log ADD COLUMN IF NOT EXISTS category VARCHAR(50) DEFAULT 'billing'`); err != nil {

		return fmt.Errorf("alter table enterprise_audit_log add category: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE enterprise_audit_log ADD COLUMN IF NOT EXISTS user_id UUID REFERENCES users(id)`); err != nil {

		return fmt.Errorf("alter table enterprise_audit_log add user_id: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE enterprise_audit_log ADD COLUMN IF NOT EXISTS old_value BIGINT`); err != nil {

		return fmt.Errorf("alter table enterprise_audit_log add old_value: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE enterprise_audit_log ADD COLUMN IF NOT EXISTS new_value BIGINT`); err != nil {

		return fmt.Errorf("alter table enterprise_audit_log add new_value: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE enterprise_audit_log ADD COLUMN IF NOT EXISTS old_value_str VARCHAR(255)`); err != nil {

		return fmt.Errorf("alter table enterprise_audit_log add old_value_str: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE enterprise_audit_log ADD COLUMN IF NOT EXISTS new_value_str VARCHAR(255)`); err != nil {

		return fmt.Errorf("alter table enterprise_audit_log add new_value_str: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE enterprise_audit_log ADD COLUMN IF NOT EXISTS target_type VARCHAR(50)`); err != nil {

		return fmt.Errorf("alter table enterprise_audit_log add target_type: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE enterprise_audit_log ADD COLUMN IF NOT EXISTS target_id VARCHAR(100)`); err != nil {

		return fmt.Errorf("alter table enterprise_audit_log add target_id: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE enterprise_audit_log ALTER COLUMN action DROP NOT NULL`); err != nil {

		return fmt.Errorf("alter table enterprise_audit_log drop action not null: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE enterprise_audit_log ALTER COLUMN amount DROP NOT NULL`); err != nil {

		return fmt.Errorf("alter table enterprise_audit_log drop amount not null: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE enterprise_invitations ADD COLUMN IF NOT EXISTS accepted_at TIMESTAMP`); err != nil {

		return fmt.Errorf("alter table enterprise_invitations add accepted_at: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE enterprise_invitations ADD COLUMN IF NOT EXISTS expires_at TIMESTAMP`); err != nil {

		return fmt.Errorf("alter table enterprise_invitations add expires_at: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE enterprise_invitations ADD COLUMN IF NOT EXISTS invited_by UUID`); err != nil {

		return fmt.Errorf("alter table enterprise_invitations add invited_by: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE enterprise_invitations ADD COLUMN IF NOT EXISTS name VARCHAR(255)`); err != nil {

		return fmt.Errorf("alter table enterprise_invitations add name: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE enterprise_member_role_log ADD COLUMN IF NOT EXISTS reason TEXT`); err != nil {

		return fmt.Errorf("alter table enterprise_member_role_log add reason: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE exchange_rates ADD COLUMN IF NOT EXISTS fetched_at TIMESTAMP`); err != nil {

		return fmt.Errorf("alter table exchange_rates add fetched_at: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE exchange_rates ADD COLUMN IF NOT EXISTS source VARCHAR(100)`); err != nil {

		return fmt.Errorf("alter table exchange_rates add source: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE orders ADD COLUMN IF NOT EXISTS product_name VARCHAR(255)`); err != nil {

		return fmt.Errorf("alter table orders add product_name: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE orders ADD COLUMN IF NOT EXISTS gateway_order_id VARCHAR(255)`); err != nil {

		return fmt.Errorf("alter table orders add gateway_order_id: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE provider_health_stats ADD COLUMN IF NOT EXISTS error_4xx_count INTEGER DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table provider_health_stats add error_4xx_count: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE provider_health_stats ADD COLUMN IF NOT EXISTS error_5xx_count INTEGER DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table provider_health_stats add error_5xx_count: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE provider_health_stats ADD COLUMN IF NOT EXISTS error_rate DECIMAL(7,4) DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table provider_health_stats add error_rate: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE provider_health_stats ADD COLUMN IF NOT EXISTS status VARCHAR(20) DEFAULT 'unknown'`); err != nil {

		return fmt.Errorf("alter table provider_health_stats add status: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE subscription_purchases ADD COLUMN IF NOT EXISTS order_id UUID`); err != nil {

		return fmt.Errorf("alter table subscription_purchases add order_id: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE subscription_purchases ADD COLUMN IF NOT EXISTS starts_at TIMESTAMP`); err != nil {

		return fmt.Errorf("alter table subscription_purchases add starts_at: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE subscription_purchases ADD COLUMN IF NOT EXISTS expires_at TIMESTAMP`); err != nil {

		return fmt.Errorf("alter table subscription_purchases add expires_at: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE upstream_key_usage ADD COLUMN IF NOT EXISTS avg_latency_ms INTEGER DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table upstream_key_usage add avg_latency_ms: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE upstream_key_usage ADD COLUMN IF NOT EXISTS model VARCHAR(100)`); err != nil {

		return fmt.Errorf("alter table upstream_key_usage add model: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE upstream_key_usage ADD COLUMN IF NOT EXISTS updated_at TIMESTAMP DEFAULT NOW()`); err != nil {

		return fmt.Errorf("alter table upstream_key_usage add updated_at: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE upstream_keys ADD COLUMN IF NOT EXISTS validation_at TIMESTAMP`); err != nil {

		return fmt.Errorf("alter table upstream_keys add validation_at: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE usage_records ADD COLUMN IF NOT EXISTS routing_track VARCHAR(50)`); err != nil {

		return fmt.Errorf("alter table usage_records add routing_track: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE user_devices ADD COLUMN IF NOT EXISTS device_name VARCHAR(255)`); err != nil {

		return fmt.Errorf("alter table user_devices add device_name: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE user_devices ADD COLUMN IF NOT EXISTS is_current BOOLEAN DEFAULT false`); err != nil {

		return fmt.Errorf("alter table user_devices add is_current: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE user_devices ADD COLUMN IF NOT EXISTS location VARCHAR(255)`); err != nil {

		return fmt.Errorf("alter table user_devices add location: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE users ADD COLUMN IF NOT EXISTS preferred_currency VARCHAR(10) DEFAULT 'USD'`); err != nil {

		return fmt.Errorf("alter table users add preferred_currency: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE users ADD COLUMN IF NOT EXISTS status VARCHAR(20) DEFAULT 'active'`); err != nil {

		return fmt.Errorf("alter table users add status: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE users ADD COLUMN IF NOT EXISTS routing_track VARCHAR(50) DEFAULT 'standard'`); err != nil {

		return fmt.Errorf("alter table users add routing_track: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE verification_codes ADD COLUMN IF NOT EXISTS type VARCHAR(50)`); err != nil {

		return fmt.Errorf("alter table verification_codes add type: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE verification_codes ADD COLUMN IF NOT EXISTS value VARCHAR(255)`); err != nil {

		return fmt.Errorf("alter table verification_codes add value: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE provider_health_stats ADD COLUMN IF NOT EXISTS request_count INTEGER DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table provider_health_stats add request_count: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE provider_health_stats ADD COLUMN IF NOT EXISTS failure_count INTEGER DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table provider_health_stats add failure_count: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE provider_health_stats ADD COLUMN IF NOT EXISTS timeout_count INTEGER DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table provider_health_stats add timeout_count: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE provider_health_stats ADD COLUMN IF NOT EXISTS rate_limit_count INTEGER DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table provider_health_stats add rate_limit_count: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE provider_health_stats ADD COLUMN IF NOT EXISTS total_latency_ms BIGINT DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table provider_health_stats add total_latency_ms: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE provider_health_stats ADD COLUMN IF NOT EXISTS updated_at TIMESTAMP DEFAULT NOW()`); err != nil {

		return fmt.Errorf("alter table provider_health_stats add updated_at: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE provider_health_stats ADD COLUMN IF NOT EXISTS auth_error_count INTEGER DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table provider_health_stats add auth_error_count: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE provider_health_stats ADD COLUMN IF NOT EXISTS network_error_count INTEGER DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table provider_health_stats add network_error_count: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE provider_health_stats ADD COLUMN IF NOT EXISTS response_timeout_count INTEGER DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table provider_health_stats add response_timeout_count: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE provider_health_stats ADD COLUMN IF NOT EXISTS content_mismatch_count INTEGER DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table provider_health_stats add content_mismatch_count: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE provider_health_stats ADD COLUMN IF NOT EXISTS invalid_request_count INTEGER DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table provider_health_stats add invalid_request_count: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE provider_health_stats ADD COLUMN IF NOT EXISTS availability DECIMAL(5,2) DEFAULT -1`); err != nil {

		return fmt.Errorf("alter table provider_health_stats add availability: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE provider_health_stats ADD COLUMN IF NOT EXISTS slow_latency_ms INTEGER DEFAULT 5000`); err != nil {

		return fmt.Errorf("alter table provider_health_stats add slow_latency_ms: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE upstream_key_usage ADD COLUMN IF NOT EXISTS input_tokens BIGINT DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table upstream_key_usage add input_tokens: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE upstream_key_usage ADD COLUMN IF NOT EXISTS output_tokens BIGINT DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table upstream_key_usage add output_tokens: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE upstream_key_usage ADD COLUMN IF NOT EXISTS cache_read_tokens BIGINT DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table upstream_key_usage add cache_read_tokens: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE upstream_key_usage ADD COLUMN IF NOT EXISTS cache_creation_tokens BIGINT DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table upstream_key_usage add cache_creation_tokens: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE upstream_key_usage ADD COLUMN IF NOT EXISTS error_count INTEGER DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table upstream_key_usage add error_count: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE upstream_key_usage ADD COLUMN IF NOT EXISTS total_latency_ms BIGINT DEFAULT 0`); err != nil {

		return fmt.Errorf("alter table upstream_key_usage add total_latency_ms: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE delivery_addresses ADD COLUMN IF NOT EXISTS country VARCHAR(10) NOT NULL DEFAULT 'CN'`); err != nil {

		return fmt.Errorf("alter table delivery_addresses add country: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE invoice_profiles ADD COLUMN IF NOT EXISTS email VARCHAR(255)`); err != nil {

		return fmt.Errorf("alter table invoice_profiles add email: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE invoice_profiles ADD COLUMN IF NOT EXISTS country VARCHAR(10) DEFAULT 'CN'`); err != nil {

		return fmt.Errorf("alter table invoice_profiles add country: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE invoice_profiles ADD COLUMN IF NOT EXISTS company_name VARCHAR(200)`); err != nil {

		return fmt.Errorf("alter table invoice_profiles add company_name: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE invoice_profiles ADD COLUMN IF NOT EXISTS registered_address VARCHAR(500)`); err != nil {

		return fmt.Errorf("alter table invoice_profiles add registered_address: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE invoice_requests ADD COLUMN IF NOT EXISTS delivery_method VARCHAR(20) DEFAULT 'email'`); err != nil {

		return fmt.Errorf("alter table invoice_requests add delivery_method: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE invoice_requests ADD COLUMN IF NOT EXISTS delivery_email VARCHAR(255)`); err != nil {

		return fmt.Errorf("alter table invoice_requests add delivery_email: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE invoice_requests ADD COLUMN IF NOT EXISTS enterprise_id UUID REFERENCES enterprises(id)`); err != nil {

		return fmt.Errorf("alter table invoice_requests add enterprise_id: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_invoice_requests_enterprise ON invoice_requests(enterprise_id)`); err != nil {

		return fmt.Errorf("create index idx_invoice_requests_enterprise: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE enterprise_invitations ADD COLUMN IF NOT EXISTS updated_at TIMESTAMP DEFAULT NOW()`); err != nil {

		return fmt.Errorf("alter table enterprise_invitations add updated_at: %w", err)

	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE service_models ADD COLUMN IF NOT EXISTS currency VARCHAR(10) DEFAULT 'USD'`); err != nil {
		return fmt.Errorf("alter table service_models add currency: %w", err)
	}

	// Enterprise self-registration
	if _, err := db.Pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS enterprise_applications (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		applicant_user_id UUID NOT NULL,
		enterprise_name VARCHAR(255) NOT NULL,
		country VARCHAR(100) NOT NULL DEFAULT '',
		contact_name VARCHAR(255) DEFAULT '',
		contact_email VARCHAR(255) DEFAULT '',
		contact_phone VARCHAR(100) DEFAULT '',
		registration_number VARCHAR(255) DEFAULT '',
		documents JSONB DEFAULT '[]',
		status VARCHAR(20) DEFAULT 'pending_review',
		rejection_reason TEXT DEFAULT '',
		reviewed_by UUID,
		reviewed_at TIMESTAMP,
		created_at TIMESTAMP DEFAULT NOW(),
		updated_at TIMESTAMP DEFAULT NOW()
	)`); err != nil {
		return fmt.Errorf("create table enterprise_applications: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE enterprises ADD COLUMN IF NOT EXISTS verified_at TIMESTAMP`); err != nil {
		return fmt.Errorf("alter table enterprises add verified_at: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE enterprises ADD COLUMN IF NOT EXISTS application_id UUID`); err != nil {
		return fmt.Errorf("alter table enterprises add application_id: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS idx_enterprise_applications_one_active_per_user
		ON enterprise_applications(applicant_user_id)
		WHERE status IN ('pending_review', 'needs_more_info')`); err != nil {
		return fmt.Errorf("create unique index on enterprise_applications: %w", err)
	}

	// Enterprise application documents (KYC / authorization files)
	if _, err := db.Pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS enterprise_application_documents (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		application_id UUID NOT NULL REFERENCES enterprise_applications(id),
		document_type VARCHAR(50) DEFAULT 'other',
		original_name VARCHAR(255) NOT NULL,
		storage_key VARCHAR(512) NOT NULL UNIQUE,
		storage_provider VARCHAR(50) NOT NULL DEFAULT 'local',
		content_type VARCHAR(100) NOT NULL,
		file_size BIGINT NOT NULL,
		file_hash VARCHAR(64),
		uploaded_by UUID NOT NULL REFERENCES users(id),
		uploaded_at TIMESTAMP DEFAULT NOW(),
		deleted_at TIMESTAMP,
		delete_reason TEXT,
		metadata JSONB DEFAULT '{}'
	)`); err != nil {
		return fmt.Errorf("create table enterprise_application_documents: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE enterprise_application_documents ADD COLUMN IF NOT EXISTS document_type VARCHAR(50) DEFAULT 'other'`); err != nil {
		return fmt.Errorf("alter table enterprise_application_documents add document_type: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `
		CREATE INDEX IF NOT EXISTS idx_enterprise_app_docs_application_id
		ON enterprise_application_documents(application_id)
		WHERE deleted_at IS NULL`); err != nil {
		return fmt.Errorf("create index on enterprise_application_documents: %w", err)
	}

	// Enterprise info change requests (enterprise admin requests changes to verified info)
	if _, err := db.Pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS enterprise_change_requests (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		enterprise_id UUID NOT NULL REFERENCES enterprises(id),
		requested_by UUID NOT NULL REFERENCES users(id),
		field VARCHAR(50) NOT NULL,
		old_value TEXT,
		new_value TEXT NOT NULL,
		reason TEXT,
		status VARCHAR(20) NOT NULL DEFAULT 'pending',
		reviewed_by UUID REFERENCES users(id),
		reviewed_at TIMESTAMP,
		created_at TIMESTAMP DEFAULT NOW(),
		updated_at TIMESTAMP DEFAULT NOW()
	)`); err != nil {
		return fmt.Errorf("create table enterprise_change_requests: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `
		CREATE INDEX IF NOT EXISTS idx_enterprise_change_requests_enterprise
		ON enterprise_change_requests(enterprise_id, status)`); err != nil {
		return fmt.Errorf("create index on enterprise_change_requests: %w", err)
	}

	// Management tokens: long-lived, revocable, scoped credentials for
	// programmatic account management (distinct from sk- inference keys and
	// JWT sessions). P1: enterprise-scoped, read:usage / read:pricing.
	// user_id is retained so the model can later be extended to personal
	// (non-enterprise) tokens without a schema change.
	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS management_tokens (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		token_hash VARCHAR(255) NOT NULL UNIQUE,
		token_prefix VARCHAR(20) NOT NULL,
		name VARCHAR(255) NOT NULL,
		user_id UUID NOT NULL REFERENCES users(id),
		enterprise_id UUID REFERENCES enterprises(id),
		scopes JSONB NOT NULL DEFAULT '[]',
		status VARCHAR(20) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled', 'revoked')),
		last_used_at TIMESTAMP,
		expires_at TIMESTAMP,
		created_at TIMESTAMP DEFAULT NOW(),
		updated_at TIMESTAMP DEFAULT NOW(),
		deleted_at TIMESTAMP
	)`); err != nil {
		return fmt.Errorf("create table management_tokens: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `
		CREATE INDEX IF NOT EXISTS idx_management_tokens_hash
		ON management_tokens(token_hash) WHERE deleted_at IS NULL`); err != nil {
		return fmt.Errorf("create index on management_tokens hash: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `
		CREATE INDEX IF NOT EXISTS idx_management_tokens_enterprise
		ON management_tokens(enterprise_id, status) WHERE deleted_at IS NULL`); err != nil {
		return fmt.Errorf("create index on management_tokens enterprise: %w", err)
	}

	// Enterprise billing/invoice info fields. Kept as explicit ALTER TABLE
	// statements (rather than Go loops) so static schema checks can parse them.
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE enterprises ADD COLUMN IF NOT EXISTS country VARCHAR(100) DEFAULT ''`); err != nil {
		return fmt.Errorf("alter table enterprises add country: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE enterprises ADD COLUMN IF NOT EXISTS tax_number VARCHAR(100) DEFAULT ''`); err != nil {
		return fmt.Errorf("alter table enterprises add tax_number: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE enterprises ADD COLUMN IF NOT EXISTS registered_address TEXT DEFAULT ''`); err != nil {
		return fmt.Errorf("alter table enterprises add registered_address: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE enterprises ADD COLUMN IF NOT EXISTS bank_name VARCHAR(255) DEFAULT ''`); err != nil {
		return fmt.Errorf("alter table enterprises add bank_name: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE enterprises ADD COLUMN IF NOT EXISTS bank_account VARCHAR(100) DEFAULT ''`); err != nil {
		return fmt.Errorf("alter table enterprises add bank_account: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE enterprises ADD COLUMN IF NOT EXISTS legal_name VARCHAR(255) DEFAULT ''`); err != nil {
		return fmt.Errorf("alter table enterprises add legal_name: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE enterprises ADD COLUMN IF NOT EXISTS display_currency VARCHAR(10) DEFAULT 'USD'`); err != nil {
		return fmt.Errorf("alter table enterprises add display_currency: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE enterprises ADD COLUMN IF NOT EXISTS exchange_rate DECIMAL(12,6) DEFAULT 1.000000`); err != nil {
		return fmt.Errorf("alter table enterprises add exchange_rate: %w", err)
	}

	// Same fields on enterprise_applications for initial submission.
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE enterprise_applications ADD COLUMN IF NOT EXISTS registered_address TEXT DEFAULT ''`); err != nil {
		return fmt.Errorf("alter table enterprise_applications add registered_address: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE enterprise_applications ADD COLUMN IF NOT EXISTS bank_name VARCHAR(255) DEFAULT ''`); err != nil {
		return fmt.Errorf("alter table enterprise_applications add bank_name: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE enterprise_applications ADD COLUMN IF NOT EXISTS bank_account VARCHAR(100) DEFAULT ''`); err != nil {
		return fmt.Errorf("alter table enterprise_applications add bank_account: %w", err)
	}

	// Dual-track sync trigger: keep enterprise_members in sync with users.enterprise_id
	if _, err := db.Pool.Exec(ctx, `
		CREATE OR REPLACE FUNCTION sync_enterprise_membership() RETURNS TRIGGER AS $$
		DECLARE
			member_role VARCHAR(20);
		BEGIN
			IF NEW.enterprise_id IS NOT NULL AND NEW.enterprise_id != COALESCE(OLD.enterprise_id, '00000000-0000-0000-0000-000000000000'::uuid) THEN
				-- Map users.role to enterprise_members.role (member/admin)
				IF NEW.role = 'enterprise_admin' THEN member_role := 'admin'; ELSE member_role := 'member'; END IF;
				INSERT INTO enterprise_members (enterprise_id, user_id, role, joined_at, updated_at)
				VALUES (NEW.enterprise_id, NEW.id, member_role, NOW(), NOW())
				ON CONFLICT (enterprise_id, user_id) WHERE removed_at IS NULL DO UPDATE SET role = EXCLUDED.role, updated_at = NOW();
			END IF;
			IF NEW.enterprise_id IS NULL AND OLD.enterprise_id IS NOT NULL THEN
				UPDATE enterprise_members SET removed_at = NOW(), updated_at = NOW()
				WHERE enterprise_id = OLD.enterprise_id AND user_id = OLD.id AND removed_at IS NULL;
			END IF;
			RETURN NEW;
		END;
		$$ LANGUAGE plpgsql`); err != nil {
		return fmt.Errorf("create sync_enterprise_membership function: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `
		DO $$ BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_sync_enterprise_membership') THEN
				CREATE TRIGGER trg_sync_enterprise_membership
				AFTER INSERT OR UPDATE OF enterprise_id ON users
				FOR EACH ROW EXECUTE FUNCTION sync_enterprise_membership();
			END IF;
		END $$`); err != nil {
		return fmt.Errorf("create trigger trg_sync_enterprise_membership: %w", err)
	}

	// Usage logs enrichment fields: endpoint, streaming flag, client IP, status.
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE usage_records ADD COLUMN IF NOT EXISTS endpoint VARCHAR(128)`); err != nil {
		return fmt.Errorf("alter table usage_records add endpoint: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE usage_records ADD COLUMN IF NOT EXISTS is_streaming BOOLEAN DEFAULT false`); err != nil {
		return fmt.Errorf("alter table usage_records add is_streaming: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE usage_records ADD COLUMN IF NOT EXISTS client_ip VARCHAR(64)`); err != nil {
		return fmt.Errorf("alter table usage_records add client_ip: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE usage_records ADD COLUMN IF NOT EXISTS status VARCHAR(32) DEFAULT 'success'`); err != nil {
		return fmt.Errorf("alter table usage_records add status: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE usage_records ADD COLUMN IF NOT EXISTS error_code VARCHAR(32)`); err != nil {
		return fmt.Errorf("alter table usage_records add error_code: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_usage_records_error_code ON usage_records(error_code)`); err != nil {
		return fmt.Errorf("create index idx_usage_records_error_code: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_usage_records_endpoint ON usage_records(endpoint)`); err != nil {
		return fmt.Errorf("create index idx_usage_records_endpoint: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_usage_records_is_streaming ON usage_records(is_streaming)`); err != nil {
		return fmt.Errorf("create index idx_usage_records_is_streaming: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_usage_records_status ON usage_records(status)`); err != nil {
		return fmt.Errorf("create index idx_usage_records_status: %w", err)
	}

	// Usage logs enrichment fields: TTFT, client geolocation, reasoning intensity.
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE usage_records ADD COLUMN IF NOT EXISTS ttft_ms INTEGER`); err != nil {
		return fmt.Errorf("alter table usage_records add ttft_ms: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE usage_records ADD COLUMN IF NOT EXISTS client_geo VARCHAR(10)`); err != nil {
		return fmt.Errorf("alter table usage_records add client_geo: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE usage_records ADD COLUMN IF NOT EXISTS user_agent TEXT`); err != nil {
		return fmt.Errorf("alter table usage_records add user_agent: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE usage_records ADD COLUMN IF NOT EXISTS agent_tools TEXT`); err != nil {
		return fmt.Errorf("alter table usage_records add agent_tools: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE usage_records ADD COLUMN IF NOT EXISTS called_tools TEXT`); err != nil {
		return fmt.Errorf("alter table usage_records add called_tools: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE usage_records ADD COLUMN IF NOT EXISTS reasoning_intensity VARCHAR(20)`); err != nil {
		return fmt.Errorf("alter table usage_records add reasoning_intensity: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_usage_records_client_geo ON usage_records(client_geo)`); err != nil {
		return fmt.Errorf("create index idx_usage_records_client_geo: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_usage_records_reasoning_intensity ON usage_records(reasoning_intensity)`); err != nil {
		return fmt.Errorf("create index idx_usage_records_reasoning_intensity: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE usage_records ADD COLUMN IF NOT EXISTS cache_creation_tokens INTEGER DEFAULT 0`); err != nil {
		return fmt.Errorf("alter table usage_records add cache_creation_tokens: %w", err)
	}

	// S-U14: Composite indexes for high-frequency query dimensions.
	// These cover the most common filtered queries on usage_records:
	//   - (user_id, created_at): user billing pages, admin user drill-down
	//   - (user_api_key_id, created_at): key usage pages
	//   - (group_id, created_at): enterprise group usage, admin group drill-down
	//   - (model, created_at): model-level analytics
	// NONCONCURRENTLY is safe here because schema.go runs at startup before
	// traffic begins; production deployments should use CONCURRENTLY via
	// the migration file.
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_usage_records_user_id_created_at ON usage_records(user_id, created_at)`); err != nil {
		return fmt.Errorf("create index idx_usage_records_user_id_created_at: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_usage_records_api_key_id_created_at ON usage_records(user_api_key_id, created_at)`); err != nil {
		return fmt.Errorf("create index idx_usage_records_api_key_id_created_at: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_usage_records_group_id_created_at ON usage_records(group_id, created_at)`); err != nil {
		return fmt.Errorf("create index idx_usage_records_group_id_created_at: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_usage_records_model_created_at ON usage_records(model, created_at)`); err != nil {
		return fmt.Errorf("create index idx_usage_records_model_created_at: %w", err)
	}
	// (upstream_key_id, created_at): upstream key heat metrics join usage_records
	// per key; without this each key degrades to a partition scan.
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_usage_records_upstream_key_id_created_at ON usage_records(upstream_key_id, created_at)`); err != nil {
		return fmt.Errorf("create index idx_usage_records_upstream_key_id_created_at: %w", err)
	}
	// Lookup indexes for the per-request model-resolution OR-joins
	// (product_id = $1 OR api_model_name = $1) that the unique
	// (provider, product_id) constraint cannot serve.
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_service_models_product_id ON service_models(product_id)`); err != nil {
		return fmt.Errorf("create index idx_service_models_product_id: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_service_models_api_model_name ON service_models(api_model_name)`); err != nil {
		return fmt.Errorf("create index idx_service_models_api_model_name: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE users ADD COLUMN IF NOT EXISTS token_version INTEGER DEFAULT 0`); err != nil {
		return fmt.Errorf("alter table users add token_version: %w", err)
	}

	// Notification center (see docs/notification-system-proposal-202610312238.md):
	// unified in-app inbox + per-user delivery preferences + background-job
	// heartbeat registry. email_notifications already exists above as the
	// outbound email queue consumed by the delivery worker.
	if _, err := db.Pool.Exec(ctx, `
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
		)
	`); err != nil {
		return fmt.Errorf("create table notifications: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_notifications_user_unread ON notifications(user_id) WHERE read_at IS NULL`); err != nil {
		return fmt.Errorf("create index idx_notifications_user_unread: %w", err)
	}
	// Partial unique index: at most one UNREAD notification per (user, type,
	// ref) — duplicate events while unread collapse instead of spamming.
	if _, err := db.Pool.Exec(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS uq_notification_dedup ON notifications(user_id, type, ref_type, ref_id) WHERE read_at IS NULL`); err != nil {
		return fmt.Errorf("create unique index uq_notification_dedup: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS notification_preferences (
			user_id UUID NOT NULL REFERENCES users(id),
			ntype VARCHAR(50) NOT NULL,
			channel VARCHAR(20) NOT NULL,
			enabled BOOLEAN NOT NULL DEFAULT TRUE,
			updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
			PRIMARY KEY (user_id, ntype, channel)
		)
	`); err != nil {
		return fmt.Errorf("create table notification_preferences: %w", err)
	}
	// Heartbeat registry for background jobs (cmd/server/background.go tickers).
	// The inspector notifies system admins when a job misses its window or fails.
	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS job_runs (
			job_name VARCHAR(100) PRIMARY KEY,
			interval_min INTEGER NOT NULL,
			last_run_at TIMESTAMP,
			last_status VARCHAR(20),
			last_error TEXT,
			last_notified_at TIMESTAMP
		)
	`); err != nil {
		return fmt.Errorf("create table job_runs: %w", err)
	}

	// IM webhook channels: per-admin Feishu/DingTalk/WeCom group-bot webhooks
	// receiving high-priority system-admin notifications (proposal §6).
	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS notification_channels (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			user_id UUID NOT NULL REFERENCES users(id),
			type VARCHAR(20) NOT NULL,
			webhook_url TEXT NOT NULL,
			secret TEXT,
			enabled BOOLEAN NOT NULL DEFAULT TRUE,
			filters JSONB,
			last_test_at TIMESTAMP,
			last_test_status VARCHAR(20),
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW()
		)
	`); err != nil {
		return fmt.Errorf("create table notification_channels: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_notification_channels_user ON notification_channels(user_id)`); err != nil {
		return fmt.Errorf("create index idx_notification_channels_user: %w", err)
	}

	// Support tickets: users/enterprise admins open tickets, exchange messages
	// (with optional admin-only internal notes) and attachments; system admins
	// triage/reply/resolve. Control-plane only. See
	// docs/support-ticket-system-design-202611042000.md.
	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS support_tickets (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			ticket_no BIGSERIAL,
			user_id UUID NOT NULL REFERENCES users(id),
			enterprise_id UUID REFERENCES enterprises(id),
			subject VARCHAR(255) NOT NULL,
			category VARCHAR(40) NOT NULL DEFAULT 'general',
			priority VARCHAR(20) NOT NULL DEFAULT 'normal',
			status VARCHAR(20) NOT NULL DEFAULT 'open',
			assignee_id UUID REFERENCES users(id),
			last_reply_role VARCHAR(10),
			last_reply_at TIMESTAMPTZ,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			deleted_at TIMESTAMPTZ
		)
	`); err != nil {
		return fmt.Errorf("create table support_tickets: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_support_tickets_user ON support_tickets(user_id) WHERE deleted_at IS NULL`); err != nil {
		return fmt.Errorf("create index idx_support_tickets_user: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_support_tickets_status ON support_tickets(status) WHERE deleted_at IS NULL`); err != nil {
		return fmt.Errorf("create index idx_support_tickets_status: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_support_tickets_enterprise ON support_tickets(enterprise_id) WHERE deleted_at IS NULL`); err != nil {
		return fmt.Errorf("create index idx_support_tickets_enterprise: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS support_ticket_messages (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			ticket_id UUID NOT NULL REFERENCES support_tickets(id) ON DELETE CASCADE,
			author_id UUID NOT NULL REFERENCES users(id),
			author_role VARCHAR(10) NOT NULL,
			body TEXT NOT NULL,
			internal_note BOOLEAN NOT NULL DEFAULT FALSE,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`); err != nil {
		return fmt.Errorf("create table support_ticket_messages: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_ticket_messages_ticket ON support_ticket_messages(ticket_id, created_at)`); err != nil {
		return fmt.Errorf("create index idx_ticket_messages_ticket: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS support_ticket_attachments (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			message_id UUID REFERENCES support_ticket_messages(id) ON DELETE CASCADE,
			storage_key TEXT NOT NULL,
			filename VARCHAR(255) NOT NULL,
			size_bytes BIGINT,
			mime_type VARCHAR(100),
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			staged_at TIMESTAMPTZ,
			staged_by UUID
		)
	`); err != nil {
		return fmt.Errorf("create table support_ticket_attachments: %w", err)
	}
	// Staging columns (migration 0032): attachments picked in the ticket UI are
	// uploaded immediately into a per-user staging area (message_id NULL) and
	// linked to their message when the comment is submitted.
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE support_ticket_attachments ALTER COLUMN message_id DROP NOT NULL`); err != nil {
		return fmt.Errorf("drop not-null on support_ticket_attachments.message_id: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE support_ticket_attachments ADD COLUMN IF NOT EXISTS staged_at TIMESTAMPTZ`); err != nil {
		return fmt.Errorf("add staged_at to support_ticket_attachments: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE support_ticket_attachments ADD COLUMN IF NOT EXISTS staged_by UUID`); err != nil {
		return fmt.Errorf("add staged_by to support_ticket_attachments: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_ticket_attachments_message ON support_ticket_attachments(message_id)`); err != nil {
		return fmt.Errorf("create index idx_ticket_attachments_message: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_ticket_attachments_staged ON support_ticket_attachments(staged_by) WHERE message_id IS NULL AND staged_at IS NOT NULL`); err != nil {
		return fmt.Errorf("create index staged attachments: %w", err)
	}

	// Enterprise monthly billing statements (migration 0023): one row per
	// enterprise per statement month, snapshotted by ResetMonthlyBilling
	// before the counters are zeroed. See
	// docs/enterprise-billing-prepost-paid-design-202611010445.md.
	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS enterprise_statements (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			enterprise_id UUID NOT NULL REFERENCES enterprises(id),
			period VARCHAR(7) NOT NULL,
			credit_used_micro BIGINT NOT NULL DEFAULT 0,
			quota_used BIGINT NOT NULL DEFAULT 0,
			prepaid_topup_micro BIGINT NOT NULL DEFAULT 0,
			prepaid_balance_end_micro BIGINT NOT NULL DEFAULT 0,
			created_at TIMESTAMP NOT NULL DEFAULT NOW()
		)
	`); err != nil {
		return fmt.Errorf("create table enterprise_statements: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS uq_enterprise_statements_period ON enterprise_statements (enterprise_id, period)`); err != nil {
		return fmt.Errorf("create index uq_enterprise_statements_period: %w", err)
	}
	for _, col := range []string{
		`ALTER TABLE enterprise_statements ADD COLUMN IF NOT EXISTS paid_micro BIGINT NOT NULL DEFAULT 0`,
		`ALTER TABLE enterprise_statements ADD COLUMN IF NOT EXISTS paid_at TIMESTAMP`,
		`ALTER TABLE enterprise_statements ADD COLUMN IF NOT EXISTS payment_note TEXT`,
	} {
		if _, err := db.Pool.Exec(ctx, col); err != nil {
			return fmt.Errorf("enterprise_statements payment columns: %w", err)
		}
	}
	if _, err := db.Pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_enterprise_statements_ent ON enterprise_statements (enterprise_id, created_at DESC)`); err != nil {
		return fmt.Errorf("create index idx_enterprise_statements_ent: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE invoice_requests ADD COLUMN IF NOT EXISTS pool VARCHAR(10) NOT NULL DEFAULT 'postpaid'`); err != nil {
		return fmt.Errorf("alter table invoice_requests add pool: %w", err)
	}
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE invoice_requests ADD COLUMN IF NOT EXISTS period VARCHAR(7)`); err != nil {
		return fmt.Errorf("alter table invoice_requests add period: %w", err)
	}

	// Migration 0034: tamper evidence for audit data + ledger delete guards.
	// The columns/tables are spelled out here (the schema-vs-code checker only
	// reads this file); the triggers/functions/backfill come from the embedded
	// migration file so there is a single copy of that logic.
	for _, stmt := range []string{
		`ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS chain_seq BIGINT`,
		`ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS chain_version SMALLINT`,
		`ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS prev_hash TEXT`,
		`ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS row_hash TEXT`,
		`ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS delete_reason TEXT`,
		`ALTER TABLE enterprise_audit_log ADD COLUMN IF NOT EXISTS chain_seq BIGINT`,
		`ALTER TABLE enterprise_audit_log ADD COLUMN IF NOT EXISTS chain_version SMALLINT`,
		`ALTER TABLE enterprise_audit_log ADD COLUMN IF NOT EXISTS prev_hash TEXT`,
		`ALTER TABLE enterprise_audit_log ADD COLUMN IF NOT EXISTS row_hash TEXT`,
		`
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
		)`,
		`
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
		)`,
	} {
		if _, err := db.Pool.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("apply audit tamper-evidence schema (0034): %w", err)
		}
	}
	if _, err := db.Pool.Exec(ctx, migrations.AuditTamperEvidenceSQL); err != nil {
		return fmt.Errorf("apply audit tamper-evidence triggers (0034): %w", err)
	}

	// Migration 0035: TOTP multi-factor authentication. totp_secret_enc holds
	// the AES-GCM encrypted secret; totp_last_step is the replay-protection
	// high-water mark. mfa_required_roles defaults to empty so a deploy never
	// forces enrolment on anybody.
	for _, stmt := range []string{
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_secret_enc TEXT`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_enabled BOOLEAN NOT NULL DEFAULT false`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_enabled_at TIMESTAMP`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_last_step BIGINT NOT NULL DEFAULT 0`,
		`
		CREATE TABLE IF NOT EXISTS user_recovery_codes (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			code_hash VARCHAR(64) NOT NULL,
			used_at TIMESTAMP,
			created_at TIMESTAMP NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_user_recovery_codes_user ON user_recovery_codes(user_id)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_user_recovery_codes_hash ON user_recovery_codes(user_id, code_hash)`,
		`ALTER TABLE enterprises ADD COLUMN IF NOT EXISTS require_mfa BOOLEAN NOT NULL DEFAULT false`,
		`INSERT INTO system_settings (key, value, category, description, managed_by)
		 VALUES ('mfa_required_roles', '', 'security', 'Comma separated roles that must enrol in MFA (empty = nobody is forced)', 'seed')
		 ON CONFLICT (key) DO NOTHING`,
	} {
		if _, err := db.Pool.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("apply totp mfa schema (0035): %w", err)
		}
	}

	// Migration 0036: per-enterprise OIDC SSO. Nothing is enabled by default.
	// client_secret_enc is AES-GCM ciphertext; (issuer, subject) identifies the
	// person on later logins, never the e-mail address.
	for _, stmt := range []string{
		`
		CREATE TABLE IF NOT EXISTS enterprise_sso_configs (
			enterprise_id UUID PRIMARY KEY REFERENCES enterprises(id) ON DELETE CASCADE,
			enabled BOOLEAN NOT NULL DEFAULT false,
			issuer TEXT,
			client_id TEXT,
			client_secret_enc TEXT,
			scopes TEXT NOT NULL DEFAULT 'openid email profile',
			allowed_email_domains TEXT[] NOT NULL DEFAULT '{}',
			default_role VARCHAR(32) NOT NULL DEFAULT 'member',
			jit_provisioning BOOLEAN NOT NULL DEFAULT true,
			enforce_sso BOOLEAN NOT NULL DEFAULT false,
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
			updated_by UUID REFERENCES users(id) ON DELETE SET NULL
		)`,
		`
		CREATE TABLE IF NOT EXISTS user_identities (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			enterprise_id UUID NOT NULL REFERENCES enterprises(id) ON DELETE CASCADE,
			issuer TEXT NOT NULL,
			subject TEXT NOT NULL,
			email VARCHAR(255),
			created_at TIMESTAMP NOT NULL DEFAULT NOW(),
			last_login_at TIMESTAMP,
			UNIQUE (issuer, subject)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_user_identities_user ON user_identities(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_user_identities_enterprise ON user_identities(enterprise_id)`,
	} {
		if _, err := db.Pool.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("apply enterprise sso schema (0036): %w", err)
		}
	}

	return nil
}

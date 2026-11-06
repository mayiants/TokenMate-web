package db

import (
	"context"
	"os"

	"github.com/ai-proxy/ai-portal-go/internal/config"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

// SeedDefaultData inserts default providers, subscription plans and default user group.
func (db *DB) SeedDefaultData(ctx context.Context, logger *zap.Logger) error {
	baseProviders := []struct{ id, name, ptype, baseURL string }{
		{"openai", "OpenAI", "international", "https://api.openai.com/v1"},
		{"anthropic", "Anthropic", "international", "https://api.anthropic.com/v1"},
		{"google", "Google", "international", "https://generativelanguage.googleapis.com/v1beta"},
		{"microsoft", "Microsoft", "international", "https://api.openai.azure.com"},
		{"mistral", "Mistral", "international", "https://api.mistral.ai/v1"},
		{"groq", "Groq", "international", "https://api.groq.com/openai/v1"},
		{"cohere", "Cohere", "international", "https://api.cohere.ai/v1"},
		{"perplexity", "Perplexity", "international", "https://api.perplexity.ai"},
		{"meta", "Meta", "international", "https://api.llama.com/compat/v1"},
		{"amazon", "Amazon", "international", "https://bedrock-runtime.us-east-1.amazonaws.com"},
		{"xai", "xAI", "international", "https://api.x.ai/v1"},
		{"deepseek", "DeepSeek", "domestic", "https://api.deepseek.com/v1"},
		{"zhipu", "Zhipu", "domestic", "https://open.bigmodel.cn/api/paas/v4"},
		{"moonshot", "Moonshot", "domestic", "https://api.moonshot.cn/v1"},
		{"minimax", "MiniMax", "domestic", "https://api.minimax.chat/v1"},
		{"baidu", "Baidu", "domestic", "https://aip.baidubce.com/rpc/2.0/ai_custom/v1/wenxinworkshop"},
		{"alibaba", "Alibaba", "domestic", "https://dashscope.aliyuncs.com/compatible-mode/v1"},
		{"tencent", "Tencent", "domestic", "https://hunyuan.tencentcloudapi.com"},
		{"bytedance", "ByteDance", "domestic", "https://ark.cn-beijing.volces.com/api/v3"},
		{"xiaomi", "Xiaomi", "domestic", "https://api.xiaomimimo.com/v1"},
	}

	bedrockRegions := []string{
		"us-east-1", "us-east-2", "us-west-1", "us-west-2",
		"ca-central-1", "ca-west-1",
		"eu-central-1", "eu-central-2", "eu-north-1", "eu-south-1", "eu-south-2",
		"eu-west-1", "eu-west-2", "eu-west-3",
		"ap-east-2",
		"ap-northeast-1", "ap-northeast-2", "ap-northeast-3",
		"ap-south-1", "ap-south-2",
		"ap-southeast-1", "ap-southeast-2", "ap-southeast-3", "ap-southeast-4",
		"ap-southeast-5", "ap-southeast-6", "ap-southeast-7",
		"il-central-1",
		"me-central-1", "me-south-1",
		"af-south-1",
		"sa-east-1",
		"mx-central-1",
	}

	mantleRegions := []string{
		"us-east-1", "us-east-2", "us-west-2",
		"ap-northeast-1", "ap-south-1", "ap-southeast-2", "ap-southeast-3",
		"eu-central-1", "eu-north-1", "eu-south-1", "eu-west-1", "eu-west-2",
		"sa-east-1",
	}

	sortOrder := 1
	for _, p := range baseProviders {
		_ = db.Exec(ctx,
			`INSERT INTO providers (id, name, type, base_url, enabled, sort_order)
			 VALUES ($1, $2, $3, $4, true, $5)
			 ON CONFLICT (id) DO NOTHING`,
			p.id, p.name, p.ptype, p.baseURL, sortOrder,
		)
		sortOrder++
	}
	for _, region := range bedrockRegions {
		id := "amazon-" + region
		_ = db.Exec(ctx,
			`INSERT INTO providers (id, name, type, base_url, enabled, sort_order)
			 VALUES ($1, $2, $3, $4, true, $5)
			 ON CONFLICT (id) DO NOTHING`,
			id, "AWS Bedrock "+region, "international", "https://bedrock-runtime."+region+".amazonaws.com", sortOrder,
		)
		sortOrder++
	}
	for _, region := range mantleRegions {
		id := "amazon-mantle-" + region
		_ = db.Exec(ctx,
			`INSERT INTO providers (id, name, type, base_url, enabled, sort_order)
			 VALUES ($1, $2, $3, $4, true, $5)
			 ON CONFLICT (id) DO NOTHING`,
			id, "AWS Mantle "+region, "international", "https://bedrock-mantle."+region+".api.aws/v1", sortOrder,
		)
		sortOrder++
	}

	// Seed default subscription plans. Each plan is inserted if missing, and
	// then refreshed ONLY while it is still seed-managed (managed_by='seed').
	// Once an admin edits a plan through the UI it becomes managed_by='admin'
	// and subsequent seed runs leave it alone.
	type seedPlan struct {
		name                     string
		price, quota, dailyLimit int64
		durationDays, sortOrder  int
	}
	seedPlans := []seedPlan{
		{"Lite", 5000000, 50000000, 3000000, 30, 1},
		{"Standard", 10000000, 200000000, 10000000, 30, 2},
		{"Pro", 30000000, 800000000, 50000000, 30, 3},
		{"Max", 70000000, 3000000000, 200000000, 30, 4},
	}
	for _, p := range seedPlans {
		// NOTE: each parameter must appear exactly once and with an explicit cast.
		// The previous `INSERT ... SELECT $1 ... WHERE name = $1` reused $1 in two
		// different inference contexts and pgx rejected it with SQLSTATE 42P08
		// ("inconsistent types deduced for parameter $1"); because the error was
		// discarded via `_ = db.Exec`, the plans silently failed to seed.
		//
		// Existence is checked by name only (any plan_type): an admin who edits a
		// preset plan into a code plan must not cause a duplicate token plan to
		// reappear on the next boot.
		_ = db.Exec(ctx, `
			INSERT INTO subscription_plans (name, plan_type, price, quota, duration_days, daily_limit, description, sort_order, managed_by)
			SELECT v.name, 'token', v.price, v.quota, v.duration_days, v.daily_limit, '', v.sort_order, 'seed'
			FROM (VALUES ($1::text, $2::bigint, $3::bigint, $4::int, $5::bigint, $6::int))
				AS v(name, price, quota, duration_days, daily_limit, sort_order)
			WHERE NOT EXISTS (SELECT 1 FROM subscription_plans WHERE name = v.name AND deleted_at IS NULL)
		`, p.name, p.price, p.quota, p.durationDays, p.dailyLimit, p.sortOrder)
		_ = db.Exec(ctx, `
			UPDATE subscription_plans
			   SET price = $2, quota = $3, duration_days = $4, daily_limit = $5, sort_order = $6
			 WHERE name = $1 AND plan_type = 'token' AND managed_by = 'seed' AND deleted_at IS NULL
		`, p.name, p.price, p.quota, p.durationDays, p.dailyLimit, p.sortOrder)
	}

	// Seed default user group (only if none exist)
	_ = db.Exec(ctx, `
		INSERT INTO user_groups (name, description, pricing_ratio, rate_limit_rpm, status, group_type, billing_mode, is_default)
		SELECT 'default-general', 'Default general group for new users', 1.0, 60, 'active', 'general', 'token', true
		WHERE NOT EXISTS (SELECT 1 FROM user_groups LIMIT 1)
	`)

	// Seed welcome voucher ($0.68 = 680,000 micro-units for gift balance)
	_ = db.Exec(ctx, `
		INSERT INTO vouchers (code, name, type, owner_type, amount, status, max_uses, reward_type, description)
		SELECT 'WELCOME068', '新用户注册欢迎奖励', 'welcome', 'system', 680000, 'active', 999999, 'gift_balance', '$0.68 welcome gift balance for new users'
		WHERE NOT EXISTS (SELECT 1 FROM vouchers WHERE type = 'welcome' AND status = 'active' AND deleted_at IS NULL)
	`)

	// Seed default system settings so that admin/settings endpoints never 404 on a fresh/empty DB.
	defaultSettings := []struct {
		key, value, category, description string
	}{
		{"quota_for_new_user", "1000000", "operation", "Default token quota for new users"},
		{"topup_link", "", "operation", "External top-up link"},
		{"usd_exchange_rate", "720", "operation", "USD to CNY exchange rate (cents)"},
		{"retry_times", "3", "operation", "Number of retries for failed requests"},
		{"channel_disable_threshold", "0.5", "monitoring", "Error rate threshold to disable channel"},
		{"quota_remind_threshold", "100000", "monitoring", "Token threshold for quota reminder"},
		{"automatic_disable_channel_enabled", "true", "monitoring", "Auto-disable unhealthy channels"},
		{"max_user_tokens", "10", "token", "Max API keys per user"},
		{"default_token_quota", "1000000", "token", "Default monthly token quota"},
		{"token_expire_days", "30", "token", "Token expiry in days"},
		{"log_retention_days", "30", "log", "Log retention period"},
		{"log_consume_enabled", "true", "log", "Enable consumption logging"},
		{"audit_log_retention_days", "90", "log", "Audit log retention period"},
		{"alipay_enabled", "true", "payment", "Enable Alipay payments"},
		{"wechat_enabled", "false", "payment", "Enable WeChat payments"},
		{"payment_notify_url", "", "payment", "Payment callback URL"},
		{"payment_simulation_mode", "false", "payment", "Enable payment simulation mode"},
		{"announcement_enabled", "false", "announcement", "Enable system announcement"},
		{"announcement_content", "", "announcement", "Announcement content (HTML)"},
		{"gdpr_retention_days", "90", "compliance", "Days to retain data after a deletion request before erasure"},
	}
	for _, s := range defaultSettings {
		// Refresh the value only while the setting is still seed-managed. Admin
		// edits flip managed_by to 'admin' (and the explicit "reset to default"
		// action flips it back to 'seed'), so future seed runs won't clobber
		// operator choices.
		_ = db.Exec(ctx,
			`INSERT INTO system_settings (key, value, category, description, managed_by)
			 VALUES ($1, $2, $3, $4, 'seed')
			 ON CONFLICT (key) DO UPDATE SET
			   value = EXCLUDED.value,
			   category = EXCLUDED.category,
			   description = EXCLUDED.description
			 WHERE system_settings.managed_by = 'seed'`,
			s.key, s.value, s.category, s.description,
		)
	}

	logger.Info("Default seed data initialized")

	// Seed global pricing fallback if none exists.
	db.seedPricingFallback(ctx, logger)

	// Seed a test enterprise with admin and member for development/testing.
	// This is opt-in via SEED_TEST_ENTERPRISE=true to avoid creating hard-coded
	// accounts in production deployments.
	db.seedTestEnterprise(ctx, logger)

	return nil
}

// seedPricingFallback inserts a global fallback pricing rule when the table is empty.
func (db *DB) seedPricingFallback(ctx context.Context, logger *zap.Logger) {
	var exists bool
	_ = db.Pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM model_pricing_rules WHERE provider = '*' AND region = '*' AND status = 'active')`,
	).Scan(&exists)
	if exists {
		return
	}
	_, err := db.Pool.Exec(ctx,
		`INSERT INTO model_pricing_rules (provider, region, billing_mode, prices, rule_name, priority, status)
		 VALUES ('*', '*', 'flat', $1, 'Global fallback', -100, 'active')`,
		`{"input": 1.0, "output": 3.0, "cache_hit": 0.1, "cache_write": 1.0, "reasoning": 1.0, "fixed_request": 0}`,
	)
	if err != nil {
		logger.Warn("Failed to seed global pricing fallback", zap.Error(err))
		return
	}
	logger.Info("Global pricing fallback seeded")
}

// seedTestEnterprise creates a "Test Enterprise" with an enterprise_admin and member
// user for development and E2E testing. Idempotent — skips if already present.
// Enabled only when SEED_TEST_ENTERPRISE=true. Credentials and enterprise name can be
// customized via TEST_ENTERPRISE_* environment variables.
func (db *DB) seedTestEnterprise(ctx context.Context, logger *zap.Logger) {
	if os.Getenv("SEED_TEST_ENTERPRISE") != "true" {
		logger.Info("Skipping test enterprise seed (set SEED_TEST_ENTERPRISE=true to enable)")
		return
	}

	entEmail := os.Getenv("TEST_ENTERPRISE_ADMIN_EMAIL")
	if entEmail == "" {
		entEmail = "ent-admin@test.local"
	}
	memberEmail := os.Getenv("TEST_ENTERPRISE_MEMBER_EMAIL")
	if memberEmail == "" {
		memberEmail = "ent-member@test.local"
	}
	password := os.Getenv("TEST_ENTERPRISE_PASSWORD")
	if password == "" {
		logger.Error("SEED_TEST_ENTERPRISE=true but TEST_ENTERPRISE_PASSWORD is not set; refusing to seed test enterprise with default credentials")
		return
	}
	enterpriseName := os.Getenv("TEST_ENTERPRISE_NAME")
	if enterpriseName == "" {
		enterpriseName = "Test Enterprise"
	}

	// Get or create enterprise
	var entID string
	_ = db.Pool.QueryRow(ctx,
		`SELECT id::text FROM enterprises WHERE name = $1 AND deleted_at IS NULL`,
		enterpriseName,
	).Scan(&entID)

	if entID == "" {
		err := db.Pool.QueryRow(ctx,
			`INSERT INTO enterprises (name, contact_email, status, quota_total, credit_total, max_members, max_admins, billing_cycle, display_currency, exchange_rate, credit_reset_at)
			 VALUES ($1, $2, 'active', 50000000, 20000000, 20, 3, 'monthly', $3, 1.0, NOW())
			 RETURNING id::text`,
			enterpriseName, entEmail, config.DefaultCurrency,
		).Scan(&entID)
		if err != nil {
			logger.Warn("Failed to seed test enterprise", zap.Error(err))
			return
		}
	}

	// Hash password (cost=10 for speed in dev)
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 10)
	if err != nil {
		logger.Warn("Failed to hash test enterprise password", zap.Error(err))
		return
	}

	// Check if admin user already exists
	var adminExists bool
	_ = db.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE email = $1 AND deleted_at IS NULL)`, entEmail).Scan(&adminExists)
	if !adminExists {
		_, err := db.Pool.Exec(ctx,
			`INSERT INTO users (name, email, password_hash, role, enterprise_id, balance, status)
			 VALUES ('Enterprise Admin', $1, $2, 'enterprise_admin', $3::uuid, 5000000, 'active')`,
			entEmail, string(hash), entID,
		)
		if err != nil {
			logger.Warn("Failed to seed enterprise admin user", zap.Error(err))
		}
	}

	// Check if member user already exists
	var memberExists bool
	_ = db.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE email = $1 AND deleted_at IS NULL)`, memberEmail).Scan(&memberExists)
	if !memberExists {
		_, err := db.Pool.Exec(ctx,
			`INSERT INTO users (name, email, password_hash, role, enterprise_id, balance, status)
			 VALUES ('Enterprise Member', $1, $2, 'user', $3::uuid, 2000000, 'active')`,
			memberEmail, string(hash), entID,
		)
		if err != nil {
			logger.Warn("Failed to seed enterprise member user", zap.Error(err))
		}
	}

	// Ensure enterprise_members entries
	_, _ = db.Pool.Exec(ctx,
		`INSERT INTO enterprise_members (enterprise_id, user_id, role)
		 SELECT $1::uuid, id, 'admin' FROM users WHERE email = $2 AND deleted_at IS NULL
		 ON CONFLICT DO NOTHING`,
		entID, entEmail,
	)
	_, _ = db.Pool.Exec(ctx,
		`INSERT INTO enterprise_members (enterprise_id, user_id, role)
		 SELECT $1::uuid, id, 'member' FROM users WHERE email = $2 AND deleted_at IS NULL
		 ON CONFLICT DO NOTHING`,
		entID, memberEmail,
	)

	logger.Info("Test enterprise seeded",
		zap.String("enterprise_id", entID),
		zap.String("admin", entEmail),
		zap.String("member", memberEmail),
	)
}

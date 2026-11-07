package db

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mayiants/ai-portal-go/internal/testutil"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// getTestDSN returns a DSN for the shared dev database.
// Tests are safe: InitSchema is idempotent, constraint tests use temporary rows that get cleaned up.
func getTestDSN() string {
	return testutil.PostgresDSN()
}

// TestInitSchema_Idempotency verifies InitSchema can run safely on an existing database
// without destroying data. It uses only idempotent DDL (IF NOT EXISTS / IF EXISTS).
func TestInitSchema_Idempotency(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()
	logger := zap.NewNop()

	pool, err := pgxpool.New(ctx, getTestDSN())
	require.NoError(t, err, "connect to dev database")
	defer pool.Close()

	// Run InitSchema — should be safe to run on existing DB (all IF NOT EXISTS)
	t.Log("Running InitSchema on dev database (idempotent, will not drop anything)...")
	db := &DB{Pool: pool}
	err = db.InitSchema(ctx, logger)
	require.NoError(t, err, "InitSchema should complete without error on existing DB")

	// Run it a second time to verify true idempotency
	err = db.InitSchema(ctx, logger)
	require.NoError(t, err, "InitSchema should be safe to run twice")

	// Verify core tables are accessible
	coreTables := []string{
		"users", "api_keys", "providers", "service_models",
		"enterprises", "user_groups", "subscription_plans",
		"usage_records", "orders", "audit_logs",
	}
	for _, table := range coreTables {
		var n int
		err := pool.QueryRow(ctx, fmt.Sprintf("SELECT 1 FROM %s LIMIT 1", table)).Scan(&n)
		if err != nil && !strings.Contains(err.Error(), "no rows") {
			t.Errorf("table %s not accessible: %v", table, err)
		}
	}

	t.Log("InitSchema idempotency: PASS")
}

// TestInitSchema_Constraints verifies key database constraints work correctly.
// Uses temporary test rows that are cleaned up at the end.
func TestInitSchema_Constraints(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()

	pool, err := pgxpool.New(ctx, getTestDSN())
	require.NoError(t, err, "connect to dev database")
	defer pool.Close()

	testEmail := fmt.Sprintf("test_schema_%d@test.local", time.Now().UnixNano())
	testProviderID := fmt.Sprintf("test-schema-prov-%d", time.Now().UnixNano())

	// Cleanup in reverse dependency order
	defer func() {
		_, _ = pool.Exec(ctx, "DELETE FROM api_keys WHERE key_prefix = 'sk-test'")
		_, _ = pool.Exec(ctx, "DELETE FROM user_groups WHERE name = 'test_schema_group'")
		_, _ = pool.Exec(ctx, "DELETE FROM subscription_plans WHERE name = 'test_schema_plan'")
		_, _ = pool.Exec(ctx, fmt.Sprintf("DELETE FROM providers WHERE id = '%s'", testProviderID))
		_, _ = pool.Exec(ctx, fmt.Sprintf("DELETE FROM users WHERE email = '%s'", testEmail))
	}()

	// 1. users CHECK constraint: invalid role should fail
	t.Run("users_role_check", func(t *testing.T) {
		_, err := pool.Exec(ctx, `
			INSERT INTO users (id, name, email, password_hash, role)
			VALUES (gen_random_uuid(), 'test_user', $1, 'hash', 'invalid_role')
		`, testEmail)
		assert.Error(t, err, "invalid role should be rejected by CHECK constraint")

		// Valid insert should work
		_, err = pool.Exec(ctx, `
			INSERT INTO users (id, name, email, password_hash, role)
			VALUES (gen_random_uuid(), 'test_user', $1, 'hash', 'user')
		`, testEmail)
		assert.NoError(t, err, "valid user insert should succeed")
	})

	// 2. api_keys FK constraint: invalid user_id should fail
	t.Run("api_keys_fk", func(t *testing.T) {
		_, err := pool.Exec(ctx, `
			INSERT INTO api_keys (id, user_id, key_hash, key_prefix)
			VALUES (gen_random_uuid(), gen_random_uuid(), 'hash', 'sk-test')
		`)
		assert.Error(t, err, "invalid user_id FK should be rejected")
	})

	// 3. providers UNIQUE constraint
	t.Run("providers_unique", func(t *testing.T) {
		_, err := pool.Exec(ctx, `
			INSERT INTO providers (id, name, type) VALUES ($1, 'Test', 'international')
		`, testProviderID)
		assert.NoError(t, err)

		_, err = pool.Exec(ctx, `
			INSERT INTO providers (id, name, type) VALUES ($1, 'Dup', 'international')
		`, testProviderID)
		assert.Error(t, err, "duplicate PK should be rejected")
	})

	// 4. subscription_plans CHECK constraint
	t.Run("subscription_plans_type", func(t *testing.T) {
		_, err := pool.Exec(ctx, `
			INSERT INTO subscription_plans (id, name, price, quota, duration_days, plan_type)
			VALUES (gen_random_uuid(), 'test_schema_plan', 100, 10000, 30, 'invalid_type')
		`)
		assert.Error(t, err, "invalid plan_type should be rejected by CHECK")
	})

	// 5. user_groups nullable FK
	t.Run("user_groups_nullable_fk", func(t *testing.T) {
		_, err := pool.Exec(ctx, `
			INSERT INTO user_groups (id, name) VALUES (gen_random_uuid(), 'test_schema_group')
		`)
		assert.NoError(t, err, "user_group with null enterprise_id should succeed")
	})

	t.Log("Constraint checks: PASS")
}

// TestInitSchema_ColumnTypes verifies key columns have expected types.
// Read-only, no data modification.
func TestInitSchema_ColumnTypes(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, getTestDSN())
	require.NoError(t, err, "connect to dev database")
	defer pool.Close()

	checks := []struct {
		table, column, expType string
	}{
		{"users", "id", "uuid"},
		{"users", "balance", "bigint"},
		{"users", "role", "character varying"},
		{"users", "enterprise_id", "uuid"},
		{"api_keys", "selected_models", "jsonb"},
		{"usage_records", "trace_id", "uuid"},
		{"service_models", "input_price", "numeric"},
		{"service_models", "context_window", "integer"},
		{"service_models", "max_output_tokens", "integer"},
		{"invoice_profiles", "type", "character varying"},
		{"invoice_profiles", "email", "character varying"},
		{"delivery_addresses", "country", "character varying"},
	}

	for _, c := range checks {
		t.Run(fmt.Sprintf("%s.%s", c.table, c.column), func(t *testing.T) {
			var dataType string
			err := pool.QueryRow(ctx, `
				SELECT data_type FROM information_schema.columns
				WHERE table_name = $1 AND column_name = $2
			`, c.table, c.column).Scan(&dataType)
			require.NoError(t, err, "column %s.%s should exist", c.table, c.column)
			assert.Contains(t, dataType, c.expType,
				"column %s.%s: expected type containing %q, got %q",
				c.table, c.column, c.expType, dataType)
		})
	}

	t.Log("Column type checks: PASS")
}

// TestInitSchema_FullBusinessFlow performs a destructive full-flow verification:
//
//	⚠️  DROP all tables → InitSchema → SeedDefaultData → full business flow → cleanup
//
// Run only when you are OK with the dev database being completely reset:
//
//	go test -v -timeout 120s -run TestInitSchema_FullBusinessFlow ./internal/db/
func TestInitSchema_FullBusinessFlow(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping destructive integration test in short mode")
	}

	t.Log("⚠️  This test will DROP ALL TABLES in the dev database and rebuild from scratch")
	t.Log("⚠️  All existing data will be lost — this is intentional for business flow verification")

	ctx := context.Background()
	logger := zap.NewNop()

	// Hold the shared schema lock for the entire destructive test so that other
	// test packages initializing their schema wait until the schema is rebuilt.
	err := WithSchemaLock(ctx, getTestDSN(), func(pool *pgxpool.Pool) error {
		var err error
		db := &DB{Pool: pool}

		// ============================================================
		// Phase 0: Snapshot the test database so it can be restored
		// after this destructive run, and capture row counts for an
		// integrity check. A failed snapshot aborts the test — never
		// run destructive tests without a verified backup.
		// ============================================================
		t.Log("Phase 0: Snapshotting test database...")
		snapshot, snapErr := testutil.SnapshotDatabase(getTestDSN())
		require.NoError(t, snapErr, "pre-test snapshot must succeed before destructive test")
		var preCounts map[string]int64
		if snapshot != "" {
			var cntErr error
			preCounts, cntErr = testutil.DatabaseRowCounts(ctx, pool)
			require.NoError(t, cntErr, "pre-test row counts must succeed")
		} else {
			t.Log("Phase 0: snapshot skipped (CI environment) — restore + integrity check will be skipped")
		}
		defer func() {
			if snapshot == "" {
				return // CI: no snapshot to restore
			}
			if restoreErr := testutil.RestoreDatabase(getTestDSN(), snapshot); restoreErr != nil {
				t.Errorf("CRITICAL: post-test restore failed: %v (snapshot kept at %s)", restoreErr, snapshot)
				return
			}
			postCounts, err := testutil.DatabaseRowCounts(ctx, pool)
			if err != nil {
				t.Errorf("CRITICAL: post-restore row counts failed: %v", err)
				return
			}
			for table, n := range preCounts {
				if got := postCounts[table]; got != n {
					t.Errorf("integrity check: table %s row count %d before, %d after restore", table, n, got)
				}
			}
			t.Logf("Post-test restore from %s completed, integrity checked against %d tables", snapshot, len(preCounts))
		}()

		// ============================================================
		// Phase 1: Clean slate
		// ============================================================
		t.Log("Phase 1: Dropping all tables...")
		dropAllTables(ctx, t, pool)

		// ============================================================
		// Phase 2: InitSchema + SeedDefaultData
		// ============================================================
		t.Log("Phase 2: InitSchema...")
		err = db.InitSchema(ctx, logger)
		require.NoError(t, err, "InitSchema should complete without error")

		t.Log("Phase 2b: SeedDefaultData...")
		err = db.SeedDefaultData(ctx, logger)
		require.NoError(t, err, "SeedDefaultData should complete without error")

		// ============================================================
		// Phase 3: Verify seeded data
		// ============================================================
		t.Log("Phase 3: Verifying seeded data...")

		t.Run("seeded_providers", func(t *testing.T) {
			var count int
			err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM providers").Scan(&count)
			require.NoError(t, err)
			assert.Greater(t, count, 20, "should have 20+ providers seeded")
			t.Logf("Providers seeded: %d", count)
		})

		t.Run("seeded_plans", func(t *testing.T) {
			var count int
			err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM subscription_plans").Scan(&count)
			require.NoError(t, err)
			assert.GreaterOrEqual(t, count, 4, "should have 4 subscription plans seeded")
		})

		t.Run("seeded_plan_type_change_not_reseeded", func(t *testing.T) {
			// Admin editing a preset plan into a code plan must not cause the
			// seeder to re-insert a duplicate token plan on the next boot.
			_, err := pool.Exec(ctx, "UPDATE subscription_plans SET plan_type='code', managed_by='admin' WHERE name='Lite' AND deleted_at IS NULL")
			require.NoError(t, err)
			require.NoError(t, db.SeedDefaultData(ctx, logger))
			var count int
			err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM subscription_plans WHERE name='Lite' AND deleted_at IS NULL").Scan(&count)
			require.NoError(t, err)
			assert.Equal(t, 1, count, "type-changed plan must not be re-seeded")
			_, err = pool.Exec(ctx, "UPDATE subscription_plans SET plan_type='token' WHERE name='Lite' AND deleted_at IS NULL")
			require.NoError(t, err)
		})

		t.Run("seeded_default_group", func(t *testing.T) {
			var count int
			err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM user_groups WHERE is_default = true").Scan(&count)
			require.NoError(t, err)
			assert.Equal(t, 1, count, "should have 1 default user group")
		})

		// ============================================================
		// Phase 4: Business flow — Register → Login → Profile
		// ============================================================
		t.Log("Phase 4: User registration & authentication...")

		var userID string
		testEmail := fmt.Sprintf("bizflow_%d@test.local", time.Now().UnixNano())
		testPasswordHash := "$2a$10$dummyhashvalueforbusinesstest00000000000000000000000"

		t.Run("register_user", func(t *testing.T) {
			err := pool.QueryRow(ctx, `
			INSERT INTO users (name, email, password_hash, role, status, email_verified)
			VALUES ('Business Test User', $1, $2, 'user', 'active', true)
			RETURNING id::text
		`, testEmail, testPasswordHash).Scan(&userID)
			require.NoError(t, err, "user registration should succeed")
			assert.NotEmpty(t, userID)
			t.Logf("User registered: %s", userID)
		})

		t.Run("read_profile", func(t *testing.T) {
			var name, email, role string
			err := pool.QueryRow(ctx,
				"SELECT name, email, role FROM users WHERE id = $1", userID,
			).Scan(&name, &email, &role)
			require.NoError(t, err)
			assert.Equal(t, "Business Test User", name)
			assert.Equal(t, testEmail, email)
			assert.Equal(t, "user", role)
		})

		// ============================================================
		// Phase 5: API Key management
		// ============================================================
		t.Log("Phase 5: API Key CRUD...")

		var apiKeyID string
		t.Run("create_api_key", func(t *testing.T) {
			err := pool.QueryRow(ctx, `
			INSERT INTO api_keys (user_id, key_hash, key_prefix, name, status, access_mode, allowed_models, selected_models, rate_limit_rpm)
			VALUES ($1, 'sk-test-hash-bizflow', 'sk-biz', 'My Test Key', 'active', 'single_model', '["claude-sonnet-4-6"]', '["claude-sonnet-4-6"]', 60)
			RETURNING id::text
		`, userID).Scan(&apiKeyID)
			require.NoError(t, err, "API key creation should succeed")
			assert.NotEmpty(t, apiKeyID)
			t.Logf("API Key created: %s", apiKeyID)
		})

		t.Run("read_api_key", func(t *testing.T) {
			var name, status string
			err := pool.QueryRow(ctx,
				"SELECT name, status FROM api_keys WHERE id = $1", apiKeyID,
			).Scan(&name, &status)
			require.NoError(t, err)
			assert.Equal(t, "My Test Key", name)
			assert.Equal(t, "active", status)
		})

		t.Run("update_api_key", func(t *testing.T) {
			_, err := pool.Exec(ctx,
				"UPDATE api_keys SET name = $1, updated_at = NOW() WHERE id = $2",
				"Updated Key Name", apiKeyID,
			)
			require.NoError(t, err)
		})

		t.Run("validate_api_key", func(t *testing.T) {
			var count int
			err := pool.QueryRow(ctx,
				"SELECT COUNT(*) FROM api_keys WHERE key_hash = 'sk-test-hash-bizflow' AND status = 'active'",
			).Scan(&count)
			require.NoError(t, err)
			assert.Equal(t, 1, count, "API key should be valid and active")
		})

		// ============================================================
		// Phase 6: Invoice Profile (发票抬头) — the bug we fixed
		// ============================================================
		t.Log("Phase 6: Invoice profiles (发票抬头)...")

		var profileID string
		t.Run("create_invoice_profile_personal", func(t *testing.T) {
			err := pool.QueryRow(ctx, `
			INSERT INTO invoice_profiles (user_id, name, type, email, country)
			VALUES ($1, 'Personal Profile', 'personal', $2, 'CN')
			RETURNING id::text
		`, userID, testEmail).Scan(&profileID)
			require.NoError(t, err, "creating 'personal' type invoice profile should succeed (was broken before fix!)")
			t.Logf("Personal invoice profile created: %s", profileID)
		})

		t.Run("create_invoice_profile_normal", func(t *testing.T) {
			var id string
			err := pool.QueryRow(ctx, `
			INSERT INTO invoice_profiles (user_id, name, type, tax_number, address, phone, bank_name, bank_account, email, country, company_name, registered_address)
			VALUES ($1, 'Company Profile', 'normal', '91110108MA01ABCDEF', '北京市朝阳区xxx路xxx号', '13800138000', '招商银行', '6225881234567890', 'finance@company.com', 'CN', 'Example Corp', '北京市朝阳区xxx路xxx号')
			RETURNING id::text
		`, userID).Scan(&id)
			require.NoError(t, err, "creating 'normal' type invoice profile should succeed")
			t.Logf("Normal invoice profile created: %s", id)
		})

		t.Run("create_invoice_profile_proforma", func(t *testing.T) {
			var id string
			err := pool.QueryRow(ctx, `
			INSERT INTO invoice_profiles (user_id, name, type, company_name, email, country)
			VALUES ($1, 'Proforma Invoice', 'proforma', 'Overseas Corp', 'overseas@corp.com', 'US')
			RETURNING id::text
		`, userID).Scan(&id)
			require.NoError(t, err, "creating 'proforma' type invoice profile should succeed")
			t.Logf("Proforma invoice profile created: %s", id)
		})

		t.Run("read_invoice_profiles", func(t *testing.T) {
			var count int
			err := pool.QueryRow(ctx,
				"SELECT COUNT(*) FROM invoice_profiles WHERE user_id = $1", userID,
			).Scan(&count)
			require.NoError(t, err)
			assert.Equal(t, 3, count, "should have 3 invoice profiles")
		})

		t.Run("reject_bad_invoice_type", func(t *testing.T) {
			_, err := pool.Exec(ctx, `
			INSERT INTO invoice_profiles (user_id, name, type)
			VALUES ($1, 'Bad Profile', 'invalid_type')
		`, userID)
			assert.Error(t, err, "invalid type should be rejected by CHECK constraint")
		})

		// ============================================================
		// Phase 7: Delivery Address
		// ============================================================
		t.Log("Phase 7: Delivery addresses...")

		var addressID string
		t.Run("create_address", func(t *testing.T) {
			err := pool.QueryRow(ctx, `
			INSERT INTO delivery_addresses (user_id, recipient_name, phone, province, city, district, detail, postal_code, country)
			VALUES ($1, '张三', '13800138000', '北京市', '北京市', '朝阳区', 'xxx路xxx号xxx室', '100000', 'CN')
			RETURNING id::text
		`, userID).Scan(&addressID)
			require.NoError(t, err, "creating delivery address should succeed")
			t.Logf("Address created: %s", addressID)
		})

		t.Run("read_address", func(t *testing.T) {
			var recipient, country string
			err := pool.QueryRow(ctx,
				"SELECT recipient_name, country FROM delivery_addresses WHERE id = $1", addressID,
			).Scan(&recipient, &country)
			require.NoError(t, err)
			assert.Equal(t, "张三", recipient)
			assert.Equal(t, "CN", country)
		})

		// ============================================================
		// Phase 8: Orders
		// ============================================================
		t.Log("Phase 8: Orders...")

		var orderID string
		t.Run("create_order", func(t *testing.T) {
			err := pool.QueryRow(ctx, `
			INSERT INTO orders (user_id, order_id, amount, currency, status, payment_method, product_type, product_name, api_key_id)
			VALUES ($1, $2, 5000000, 'USD', 'completed', 'stripe', 'token', 'Lite Plan', $3)
			RETURNING id::text
		`, userID, "BIZFLOW-ORDER-001", apiKeyID).Scan(&orderID)
			require.NoError(t, err, "creating order should succeed")
			t.Logf("Order created: %s", orderID)
		})

		t.Run("read_order", func(t *testing.T) {
			var amount int64
			var status string
			err := pool.QueryRow(ctx,
				"SELECT amount, status FROM orders WHERE id = $1", orderID,
			).Scan(&amount, &status)
			require.NoError(t, err)
			assert.Equal(t, int64(5000000), amount)
			assert.Equal(t, "completed", status)
		})

		// ============================================================
		// Phase 9: Invoice Request
		// ============================================================
		t.Log("Phase 9: Invoice requests...")

		t.Run("create_invoice_request", func(t *testing.T) {
			var id string
			err := pool.QueryRow(ctx, `
			INSERT INTO invoice_requests (user_id, profile_id, address_id, amount, title, delivery_method, delivery_email, status)
			VALUES ($1, $2, $3, 5000000, 'Test Invoice', 'email', $4, 'pending')
			RETURNING id::text
		`, userID, profileID, addressID, testEmail).Scan(&id)
			require.NoError(t, err, "creating invoice request should succeed")
			t.Logf("Invoice request created: %s", id)
		})

		// ============================================================
		// Phase 10: Welcome Voucher (unified vouchers table)
		// ============================================================
		t.Log("Phase 10: Welcome voucher...")

		t.Run("welcome_voucher", func(t *testing.T) {
			// First verify the welcome voucher exists
			var voucherID string
			var amount int64
			err := pool.QueryRow(ctx,
				"SELECT id::text, amount FROM vouchers WHERE type = 'welcome' AND status = 'active' AND deleted_at IS NULL LIMIT 1",
			).Scan(&voucherID, &amount)
			require.NoError(t, err, "welcome voucher should exist")
			assert.Greater(t, amount, int64(0))

			// Simulate actual welcome bonus application: update user balance and record use
			_, err = pool.Exec(ctx, `
			UPDATE users SET gift_balance = COALESCE(gift_balance, 0) + $1, updated_at = NOW()
			WHERE id = $2::uuid
		`, amount, userID)
			require.NoError(t, err, "user balance should be updated")

			_, err = pool.Exec(ctx, `
			INSERT INTO voucher_uses (voucher_id, user_id, consumer_type, amount, balance_before, balance_after)
			VALUES ($1::uuid, $2, 'redeemer', $3, 0, $3)
		`, voucherID, userID, amount)
			require.NoError(t, err, "voucher use should succeed")

			_, err = pool.Exec(ctx, `
			UPDATE vouchers SET used_count = used_count + 1, updated_at = NOW()
			WHERE id = $1::uuid
		`, voucherID)
			require.NoError(t, err, "voucher used_count should be incremented")

			t.Logf("Welcome voucher used: %d micro-units", amount)
		})

		// ============================================================
		// Phase 11: Usage Records
		// ============================================================
		t.Log("Phase 11: Usage records...")

		t.Run("create_usage_record", func(t *testing.T) {
			_, err := pool.Exec(ctx, `
			INSERT INTO usage_records (trace_id, user_id, user_api_key_id, model, provider, input_tokens, output_tokens, total_cost, created_at)
			VALUES (gen_random_uuid(), $1, $2, 'claude-sonnet-4-6', 'anthropic', 100, 50, 1000, NOW())
		`, userID, apiKeyID)
			require.NoError(t, err, "creating usage record should succeed")
		})

		// ============================================================
		// Phase 12: Balance Transactions
		// ============================================================
		t.Log("Phase 12: Balance transactions...")

		t.Run("create_balance_transaction", func(t *testing.T) {
			_, err := pool.Exec(ctx, `
			INSERT INTO balance_transactions (user_id, transaction_type, amount, balance_before, balance_after, description, balance_type)
			VALUES ($1, 'recharge', 1000000, 0, 1000000, 'Test recharge', 'gift')
		`, userID)
			require.NoError(t, err, "creating balance transaction should succeed")
		})

		// ============================================================
		// Phase 13: Audit Log
		// ============================================================
		t.Log("Phase 13: Audit logs...")

		t.Run("create_audit_log", func(t *testing.T) {
			_, err := pool.Exec(ctx, `
			INSERT INTO audit_logs (user_id, user_email, category, action, level, message, details)
			VALUES ($1, $2, 'billing', 'invoice_profile_created', 'info', 'Test audit entry', '{"test":true}')
		`, userID, testEmail)
			require.NoError(t, err, "creating audit log should succeed")
		})

		// ============================================================
		// Phase 14: Enterprise flow (basic)
		// ============================================================
		t.Log("Phase 14: Enterprise flow...")

		var enterpriseID string
		enterpriseName := fmt.Sprintf("Test Enterprise %d", time.Now().UnixNano())
		t.Run("create_enterprise", func(t *testing.T) {
			err := pool.QueryRow(ctx, `
			INSERT INTO enterprises (name, contact_email, billing_cycle, quota_total)
			VALUES ($1, 'admin@test-enterprise.com', 'monthly', 100000000)
			RETURNING id::text
		`, enterpriseName).Scan(&enterpriseID)
			require.NoError(t, err, "creating enterprise should succeed")
		})

		t.Run("create_enterprise_member", func(t *testing.T) {
			_, err := pool.Exec(ctx, `
			INSERT INTO enterprise_members (enterprise_id, user_id, role, status)
			VALUES ($1, $2, 'admin', 'active')
		`, enterpriseID, userID)
			require.NoError(t, err, "adding enterprise member should succeed")
		})

		// ============================================================
		// Phase 15: Cleanup — remove all test data
		// ============================================================
		t.Log("Phase 15: Cleaning up test data...")
		cleanupBusinessFlowTestData(ctx, t, pool, userID, apiKeyID, enterpriseID, testEmail)

		t.Log("✅ Full business flow verification PASSED")
		t.Log("   - User register + profile read")
		t.Log("   - API key CRUD + validation")
		t.Log("   - Invoice profile: personal / normal / proforma (all 3 types)")
		t.Log("   - Delivery address with country")
		t.Log("   - Order creation")
		t.Log("   - Invoice request")
		t.Log("   - Redemption code")
		t.Log("   - Usage record")
		t.Log("   - Balance transaction")
		t.Log("   - Audit log")
		t.Log("   - Enterprise + member")
		return nil
	})
	require.NoError(t, err, "destructive business flow test failed")
}

// dropAllTables drops all tables in dependency-safe order.
// It refuses to run against any database whose name does not contain "test".
func dropAllTables(ctx context.Context, t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	var dbname string
	if err := pool.QueryRow(ctx, "SELECT current_database()").Scan(&dbname); err != nil {
		t.Fatalf("dropAllTables: cannot read current database: %v", err)
	}
	if !strings.Contains(strings.ToLower(dbname), "test") {
		t.Fatalf("dropAllTables: refusing to drop all tables in non-test database %q", dbname)
	}
	tables := []string{
		// FK children first
		"enterprise_member_role_log", "enterprise_audit_log",
		"enterprise_members", "enterprise_alerts", "enterprise_invitations",
		"api_keys", "user_devices", "verification_codes",
		"user_subscriptions", "subscription_purchases",
		"balance_transactions", "usage_records", "usage_summary_hourly",
		"usage_summary_daily", "audit_logs",
		"voucher_uses", "vouchers",
		"orders", "invoices", "invoice_requests", "invoice_profiles",
		"delivery_addresses", "announcement_reads",
		"single_model_purchases", "service_model_approvals",
		"super_admin_downgrade_requests",
		"provider_health_stats", "upstream_key_usage",
		"channel_health_history",
		"email_notifications", "sensitive_check_logs",
		"pricing_history", "pricing_sources",
		"archival_jobs",
		"model_provider_routing", "key_pool_status",
		// Parent tables
		"users", "enterprises", "user_groups",
		"subscription_plans", "providers", "upstream_keys",
		"service_models", "model_products", "model_pricing",
		"payment_config", "payment_gateways", "exchange_rates",
		"channel_health", "openrouter_models",
		"announcements", "system_settings",
		"proxy_heartbeats", "proxy_instances",
		"schema_migrations",
	}
	for _, table := range tables {
		_, _ = pool.Exec(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %s CASCADE", table))
	}
	t.Logf("Dropped %d tables", len(tables))
}

// cleanupBusinessFlowTestData removes test data inserted during the business flow test.
func cleanupBusinessFlowTestData(ctx context.Context, t *testing.T, pool *pgxpool.Pool,
	userID, apiKeyID, enterpriseID, testEmail string) {
	t.Helper()

	_, _ = pool.Exec(ctx, "DELETE FROM audit_logs WHERE user_email = $1", testEmail)
	_, _ = pool.Exec(ctx, "DELETE FROM balance_transactions WHERE user_id = $1", userID)
	_, _ = pool.Exec(ctx, "DELETE FROM usage_records WHERE user_id = $1", userID)
	_, _ = pool.Exec(ctx, "DELETE FROM voucher_uses WHERE user_id = $1", userID)
	_, _ = pool.Exec(ctx, "DELETE FROM invoice_requests WHERE user_id = $1", userID)
	_, _ = pool.Exec(ctx, "DELETE FROM orders WHERE user_id = $1", userID)
	_, _ = pool.Exec(ctx, "DELETE FROM invoice_profiles WHERE user_id = $1", userID)
	_, _ = pool.Exec(ctx, "DELETE FROM delivery_addresses WHERE user_id = $1", userID)
	_, _ = pool.Exec(ctx, "DELETE FROM enterprise_members WHERE user_id = $1", userID)
	_, _ = pool.Exec(ctx, "DELETE FROM api_keys WHERE id = $1", apiKeyID)
	_, _ = pool.Exec(ctx, "DELETE FROM enterprises WHERE id = $1", enterpriseID)
	_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = $1", userID)

	t.Log("Test data cleaned up")
}

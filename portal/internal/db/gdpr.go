package db

import (
	"context"
	"fmt"
	"time"
)

// AnonymizeUser permanently erases a user's PII and personal data. It is
// idempotent: users whose email is already anonymized are skipped by the
// caller's WHERE clause. Used by the deferred GDPR erasure job and by admins
// for immediate erasure. All steps run in one transaction.
func (db *DB) AnonymizeUser(ctx context.Context, userID string) error {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	steps := []struct {
		sql  string
		args []interface{}
	}{
		{`UPDATE api_keys SET deleted_at = NOW() WHERE user_id = $1::uuid AND deleted_at IS NULL`, []interface{}{userID}},
		{`UPDATE delivery_addresses SET deleted_at = NOW() WHERE user_id = $1::uuid AND deleted_at IS NULL`, []interface{}{userID}},
		{`UPDATE invoice_profiles SET deleted_at = NOW() WHERE user_id = $1::uuid AND deleted_at IS NULL`, []interface{}{userID}},
		{`UPDATE usage_records SET user_id = NULL WHERE user_id = $1::uuid`, []interface{}{userID}},
		{`UPDATE orders SET deleted_at = NOW() WHERE user_id = $1::uuid AND deleted_at IS NULL`, []interface{}{userID}},
		// Anonymize PII in linked tables instead of leaving linkable data behind.
		{`UPDATE audit_logs SET user_email = 'anonymized', ip_address = NULL WHERE user_id = $1::uuid`, []interface{}{userID}},
		{`UPDATE user_devices SET ip_address = NULL, location = NULL WHERE user_id = $1::uuid`, []interface{}{userID}},
		{`UPDATE email_notifications SET email = 'anonymized' WHERE user_id = $1::uuid`, []interface{}{userID}},
		{`UPDATE voucher_uses SET balance_before = NULL, balance_after = NULL WHERE user_id = $1::uuid`, []interface{}{userID}},
		{`UPDATE users SET
			deleted_at = NOW(),
			email = 'deleted_' || id::text || '@deleted.local',
			name = 'Deleted User',
			phone = NULL,
			company = NULL,
			second_email = NULL,
			backup_email = NULL,
			privacy_region = 'redacted',
			shipping_address = NULL,
			invoice_info = NULL,
			password_hash = 'gdpr_deleted',
			status = 'deleted',
			updated_at = NOW()
		 WHERE id = $1::uuid AND email NOT LIKE 'deleted_%@deleted.local'`, []interface{}{userID}},
	}
	for _, s := range steps {
		if _, err := tx.Exec(ctx, s.sql, s.args...); err != nil {
			return fmt.Errorf("anonymize step failed: %w", err)
		}
	}
	return tx.Commit(ctx)
}

// ExportUserData builds a GDPR data export for a user. When full is false the
// export is windowed to the last 90 days with a 1000-row cap per collection;
// when full is true everything is included (admin-processed full exports).
func (db *DB) ExportUserData(ctx context.Context, userID string, full bool) (map[string]interface{}, error) {
	out := map[string]interface{}{
		"exportedAt": time.Now().UTC().Format(time.RFC3339),
	}
	if full {
		out["exportScope"] = "full"
	} else {
		out["exportScope"] = "recent_90_days"
		out["fullExportNote"] = "This export covers the last 90 days (max 1000 rows per section). Request a full export from your profile page."
	}

	// User profile (no password_hash)
	var (
		name, email, role, status                                                 string
		phone, company, secondEmail, backupEmail, privacyRegion, locale, currency *string
		balance, giftBalance                                                      int64
		createdAt                                                                 time.Time
	)
	err := db.Pool.QueryRow(ctx,
		`SELECT COALESCE(name,''), COALESCE(email,''), COALESCE(role,''), COALESCE(status,''),
		        phone, company, second_email, backup_email, privacy_region, locale, preferred_currency,
		        COALESCE(balance,0), COALESCE(gift_balance,0), created_at
		 FROM users WHERE id = $1::uuid`, userID,
	).Scan(&name, &email, &role, &status, &phone, &company, &secondEmail, &backupEmail,
		&privacyRegion, &locale, &currency, &balance, &giftBalance, &createdAt)
	if err != nil {
		return nil, fmt.Errorf("export user row: %w", err)
	}
	out["user"] = map[string]interface{}{
		"id": userID, "name": name, "email": email, "role": role, "status": status,
		"phone": phone, "company": company, "second_email": secondEmail,
		"backup_email": backupEmail, "privacy_region": privacyRegion,
		"locale": locale, "preferred_currency": currency,
		"balance": balance, "gift_balance": giftBalance,
		"created_at": createdAt,
	}

	window := ""
	limit := ""
	if !full {
		window = " AND created_at >= NOW() - INTERVAL '90 days'"
		limit = " LIMIT 1000"
	}

	queryList := func(sql string, args ...interface{}) []map[string]interface{} {
		rows, err := db.Pool.Query(ctx, sql, args...)
		if err != nil {
			return []map[string]interface{}{}
		}
		defer rows.Close()
		cols := rows.FieldDescriptions()
		items := []map[string]interface{}{}
		for rows.Next() {
			vals, err := rows.Values()
			if err != nil {
				continue
			}
			item := map[string]interface{}{}
			for i, c := range cols {
				item[string(c.Name)] = vals[i]
			}
			items = append(items, item)
		}
		return items
	}

	out["apiKeys"] = queryList(
		`SELECT key_prefix, name, status, created_at, expires_at FROM api_keys
		 WHERE user_id = $1::uuid`+window+` ORDER BY created_at DESC`+limit, userID)
	out["usageRecords"] = queryList(
		`SELECT trace_id, model, provider, input_tokens, output_tokens, total_cost, created_at
		 FROM usage_records WHERE user_id = $1::uuid`+window+` ORDER BY created_at DESC`+limit, userID)
	out["orders"] = queryList(
		`SELECT order_id, amount, status, created_at FROM orders
		 WHERE user_id = $1::uuid`+window+` ORDER BY created_at DESC`+limit, userID)
	out["deliveryAddresses"] = queryList(
		`SELECT recipient_name, phone, country, province, city, district, detail, postal_code, is_default, created_at
		 FROM delivery_addresses WHERE user_id = $1::uuid AND deleted_at IS NULL`, userID)
	out["invoiceProfiles"] = queryList(
		`SELECT name, type, tax_number, company_name, email, address, phone, bank_name, bank_account, is_default, created_at
		 FROM invoice_profiles WHERE user_id = $1::uuid AND deleted_at IS NULL`, userID)
	out["balanceTransactions"] = queryList(
		`SELECT transaction_type, balance_type, amount, balance_before, balance_after, description, created_at
		 FROM balance_transactions WHERE user_id = $1::uuid`+window+` ORDER BY created_at DESC`+limit, userID)
	out["auditLogs"] = queryList(
		`SELECT category, action, level, message, ip_address, created_at
		 FROM audit_logs WHERE user_id = $1::uuid`+window+` ORDER BY created_at DESC`+limit, userID)

	return out, nil
}

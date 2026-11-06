package query

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/ai-proxy/ai-portal-go/internal/db"
)

// AdminEnterprise is an enterprise row for the admin list
type AdminEnterprise struct {
	ID              string
	Name            string
	ContactEmail    string
	Status          string
	QuotaTotal      int64
	QuotaUsed       int64
	CreditTotal     int64
	CreditUsed      int64
	PrepaidBalance  int64
	PricingRatio    float64
	DisplayCurrency string
	ExchangeRate    float64
	MemberCount     int
	MaxMembers      int
	AllowedDomains  string
	CreatedAt       time.Time
	VerifiedAt      *time.Time
}

// AdminEnterpriseListData holds paginated enterprise list with summary stats
type AdminEnterpriseListData struct {
	Items          []AdminEnterprise
	Search         string // echo of the active search filter (template input value)
	Total          int
	Page           int
	PageSize       int
	TotalPages     int
	ActiveCount    int
	SuspendedCount int
	VacantCount    int
}

// GetAdminEnterprisesData fetches paginated enterprise list with status counts.
// Used by both the JSON API and the page renderer.
func GetAdminEnterprisesData(ctx context.Context, db *db.DB, page, pageSize int, search string) (*AdminEnterpriseListData, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 50 {
		pageSize = 50
	}

	var total int
	_ = db.QueryRow(ctx, `SELECT COUNT(*) FROM enterprises WHERE deleted_at IS NULL`).Scan(&total)

	var activeCount, suspendedCount, vacantCount int
	_ = db.QueryRow(ctx, `SELECT COUNT(*) FILTER (WHERE status = 'active'), COUNT(*) FILTER (WHERE status = 'suspended'), COUNT(*) FILTER (WHERE status = 'admin_vacant') FROM enterprises WHERE deleted_at IS NULL`).Scan(&activeCount, &suspendedCount, &vacantCount)

	offset := (page - 1) * pageSize
	searchClause := ""
	args := []interface{}{pageSize, offset}
	if search != "" {
		searchClause = " AND (e.name ILIKE $3 OR e.contact_email ILIKE $3 OR COALESCE(e.legal_name, '') ILIKE $3)"
		args = append(args, "%"+search+"%")
	}
	rows, err := db.Query(ctx,
		`SELECT e.id::text, e.name, COALESCE(e.contact_email, ''), e.status,
		        COALESCE(e.quota_total, 0), COALESCE(e.quota_used, 0),
		        COALESCE(e.credit_total, 0), COALESCE(e.credit_used, 0), COALESCE(e.prepaid_balance, 0),
		        COALESCE(e.pricing_ratio, 1.0),
		        COALESCE(e.display_currency, 'USD'), COALESCE(e.exchange_rate, 1.0),
		        (SELECT COUNT(*) FROM users WHERE enterprise_id = e.id AND deleted_at IS NULL) as member_count,
		        COALESCE(e.max_members, 50), COALESCE(e.allowed_domains, ''),
		        e.created_at, e.verified_at
		 FROM enterprises e WHERE e.deleted_at IS NULL`+searchClause+` ORDER BY e.created_at DESC LIMIT $1 OFFSET $2`,
		args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var enterprises []AdminEnterprise
	for rows.Next() {
		var e AdminEnterprise
		if err := rows.Scan(&e.ID, &e.Name, &e.ContactEmail, &e.Status,
			&e.QuotaTotal, &e.QuotaUsed, &e.CreditTotal, &e.CreditUsed, &e.PrepaidBalance, &e.PricingRatio,
			&e.DisplayCurrency, &e.ExchangeRate,
			&e.MemberCount, &e.MaxMembers, &e.AllowedDomains,
			&e.CreatedAt, &e.VerifiedAt); err == nil {
			enterprises = append(enterprises, e)
		}
	}

	totalPages := (total + pageSize - 1) / pageSize
	if totalPages < 1 {
		totalPages = 1
	}

	return &AdminEnterpriseListData{
		Items: enterprises, Total: total, Page: page, PageSize: pageSize, TotalPages: totalPages,
		ActiveCount: activeCount, SuspendedCount: suspendedCount, VacantCount: vacantCount,
	}, nil
}

// ── Enterprise Dashboard ──────────────────────────────────────────────────────

// DailyUsagePoint holds one day's usage for trend chart.
type DailyUsagePoint struct {
	Date     string // YYYY-MM-DD
	Time     string // HH24:00, only populated for 1d range
	Requests int
	Tokens   int64
	Cost     int64
}

// EnterpriseAlert holds an alert record.
type EnterpriseAlert struct {
	ID               string
	AlertType        string
	ThresholdPercent int
	CurrentValue     int64
	LimitValue       int64
	NotifiedAt       time.Time
	AcknowledgedAt   *time.Time
}

// EnterpriseUsageRecord holds usage by user or model.
type EnterpriseUsageRecord struct {
	UserID   string
	UserName string
	Tokens   int64
	Requests int
	Percent  float64
}

// EnterpriseKeyRecord holds usage aggregated by API key.
type EnterpriseKeyRecord struct {
	KeyID     string
	KeyPrefix string
	KeyName   string
	Tokens    int64
	Requests  int
	Cost      int64
}

// EnterpriseDashboardData holds enterprise admin dashboard data.
type EnterpriseDashboardData struct {
	ID                string
	Name              string
	ContactEmail      string
	Range             string
	Status            string // active, admin_vacant, suspended
	Members           int
	MaxMembers        int
	ActiveKeys        int
	MonthlyUsage      int64
	MonthlyUsageUSD   float64
	QuotaUsed         int64
	QuotaTotal        int64
	QuotaRemaining    int64
	PricingRatio      float64
	DisplayCurrency   string
	ExchangeRate      float64
	CreditTotal       int64
	CreditUsed        int64
	PrepaidBalance    int64
	CreditRemaining   int64
	CreditPercent     float64
	CreditTotalUSD    float64
	CreditUsedUSD     float64
	CreditRemainUSD   float64
	CreditTotalLocal  float64
	CreditUsedLocal   float64
	CreditRemainLocal float64
	BillingCycle      string
	NextResetAt       *time.Time
	RecentUsage       []EnterpriseUsageRecord
	DailyUsage        []DailyUsagePoint
	Alerts            []EnterpriseAlert
	// Distribution cards
	TopModels []NameCountItem
	TopKeys   []EnterpriseKeyRecord
}

// getEnterpriseIDForUser returns the enterprise ID for a given user.
func getEnterpriseIDForUser(ctx context.Context, database *db.DB, userID string) (string, error) {
	var enterpriseID string
	err := database.QueryRow(ctx,
		`SELECT enterprise_id FROM users WHERE id = $1::uuid AND deleted_at IS NULL`,
		userID,
	).Scan(&enterpriseID)
	return enterpriseID, err
}

// computeNextResetAt returns the next reset timestamp for a billing cycle.
// It is used as a fallback when enterprises.credit_reset_at has not been set.
func computeNextResetAt(billingCycle string) *time.Time {
	now := time.Now().UTC()
	var t time.Time
	switch billingCycle {
	case "yearly":
		t = time.Date(now.Year()+1, time.January, 1, 0, 0, 0, 0, time.UTC)
	default:
		// monthly and any unrecognized cycle default to next calendar month
		t = time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC)
	}
	return &t
}

// GetEnterpriseDashboardData fetches enterprise admin dashboard data.
func GetEnterpriseDashboardData(ctx context.Context, database *db.DB, userID, rangeKey string) (*EnterpriseDashboardData, error) {
	d := &EnterpriseDashboardData{
		DailyUsage:  []DailyUsagePoint{},
		RecentUsage: []EnterpriseUsageRecord{},
		Alerts:      []EnterpriseAlert{},
	}

	// Resolve time range
	tr, ok := timeRanges[rangeKey]
	if !ok {
		tr = timeRanges[DefaultRange]
		rangeKey = DefaultRange
	}
	d.Range = rangeKey

	enterpriseID, err := getEnterpriseIDForUser(ctx, database, userID)
	if err != nil {
		return d, nil
	}

	var nextResetAt sql.NullTime
	var name, contactEmail string
	_ = database.QueryRow(ctx,
		`SELECT e.id::text, e.name, COALESCE(e.contact_email, ''),
		 COALESCE(status, 'active'),
		 COALESCE(quota_used, 0), COALESCE(quota_total, 0),
		 COALESCE(credit_total, 0), COALESCE(credit_used, 0), COALESCE(prepaid_balance, 0),
		 COALESCE(pricing_ratio, 1.0), COALESCE(max_members, 0),
		 COALESCE(display_currency, 'USD'), COALESCE(exchange_rate, 1.0),
		 COALESCE(billing_cycle, 'monthly'), credit_reset_at
		 FROM enterprises e WHERE e.id = $1::uuid AND e.deleted_at IS NULL`,
		enterpriseID,
	).Scan(&d.ID, &name, &contactEmail, &d.Status, &d.QuotaUsed, &d.QuotaTotal,
		&d.CreditTotal, &d.CreditUsed, &d.PrepaidBalance,
		&d.PricingRatio, &d.MaxMembers, &d.DisplayCurrency, &d.ExchangeRate, &d.BillingCycle, &nextResetAt)
	d.Name = name
	d.ContactEmail = contactEmail

	if nextResetAt.Valid {
		d.NextResetAt = &nextResetAt.Time
	} else if d.BillingCycle != "" {
		d.NextResetAt = computeNextResetAt(d.BillingCycle)
	}

	d.QuotaRemaining = d.QuotaTotal - d.QuotaUsed
	if d.QuotaRemaining < 0 {
		d.QuotaRemaining = 0
	}
	d.CreditRemaining = d.CreditTotal - d.CreditUsed
	if d.CreditRemaining < 0 {
		d.CreditRemaining = 0
	}
	// Available balance = prepaid wallet + remaining monthly allowance
	// (prepaid-first deduction makes the two additive from the user's view).
	d.CreditRemaining += d.PrepaidBalance
	if d.CreditTotal > 0 {
		d.CreditPercent = float64(d.CreditUsed) * 100.0 / float64(d.CreditTotal)
	}
	d.CreditTotalUSD = float64(d.CreditTotal) / 1_000_000.0
	d.CreditUsedUSD = float64(d.CreditUsed) / 1_000_000.0
	d.CreditRemainUSD = float64(d.CreditRemaining) / 1_000_000.0
	if d.DisplayCurrency != "" && d.DisplayCurrency != "USD" && d.ExchangeRate > 0 {
		d.CreditTotalLocal = d.CreditTotalUSD * d.ExchangeRate
		d.CreditUsedLocal = d.CreditUsedUSD * d.ExchangeRate
		d.CreditRemainLocal = d.CreditRemainUSD * d.ExchangeRate
	}

	_ = database.QueryRow(ctx,
		`SELECT COUNT(*) FROM users WHERE enterprise_id = $1::uuid AND deleted_at IS NULL`,
		enterpriseID,
	).Scan(&d.Members)

	_ = database.QueryRow(ctx,
		`SELECT COUNT(*) FROM api_keys
		 WHERE enterprise_id = $1::uuid
		 AND deleted_at IS NULL AND status = 'active'`,
		enterpriseID,
	).Scan(&d.ActiveKeys)

	_ = database.QueryRow(ctx,
		`SELECT COALESCE(SUM(ur.total_cost), 0) FROM usage_records ur
		 JOIN api_keys k ON ur.user_api_key_id = k.id
		 WHERE k.enterprise_id = $1::uuid AND k.key_type = 'enterprise'
		 AND ur.created_at >= DATE_TRUNC('month', CURRENT_DATE)`,
		enterpriseID,
	).Scan(&d.MonthlyUsage)
	d.MonthlyUsageUSD = float64(d.MonthlyUsage) / 1_000_000.0

	// RecentUsage: top users this month (fixed to current month), only enterprise keys
	rows, err := database.Query(ctx,
		`SELECT u.id::text, COALESCE(u.name, u.email),
		 SUM(ur.input_tokens + ur.output_tokens) as tokens, COUNT(*) as requests,
		 SUM(ur.total_cost)::bigint / NULLIF((SELECT SUM(total_cost) FROM usage_records ur2
		 JOIN api_keys k2 ON ur2.user_api_key_id = k2.id
		 WHERE k2.enterprise_id = $1::uuid AND k2.key_type = 'enterprise'
		 AND ur2.created_at >= DATE_TRUNC('month', CURRENT_DATE)), 0) * 100 as percent
		 FROM usage_records ur
		 JOIN api_keys k ON ur.user_api_key_id = k.id
		 JOIN users u ON ur.user_id = u.id
		 WHERE k.enterprise_id = $1::uuid AND k.key_type = 'enterprise'
		 AND ur.created_at >= DATE_TRUNC('month', CURRENT_DATE)
		 GROUP BY u.id, u.name, u.email
		 ORDER BY SUM(ur.total_cost) DESC
		 LIMIT 10`,
		enterpriseID,
	)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var r EnterpriseUsageRecord
			if err := rows.Scan(&r.UserID, &r.UserName, &r.Tokens, &r.Requests, &r.Percent); err == nil {
				d.RecentUsage = append(d.RecentUsage, r)
			}
		}
	}

	// DailyUsage: trend chart with dynamic time range.
	// Generate a complete set of buckets so the x-axis always has the expected
	// number of segments (e.g. 6 four-hour buckets for 1d, 7 daily buckets for 7d,
	// 15/30 daily buckets, 3 monthly buckets for quarter) even when some buckets
	// have no usage records.
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	var start, end time.Time
	switch rangeKey {
	case "1d":
		start = today
		end = start.Add(time.Duration(tr.Buckets-1) * 4 * time.Hour)
	case "quarter":
		end = time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC)
		start = end.AddDate(0, -(tr.Buckets - 1), 0)
	default:
		// 7d, 15d, 30d
		end = today
		start = today.AddDate(0, 0, -(tr.Buckets - 1))
	}

	trendSQL := fmt.Sprintf(
		`WITH buckets AS (
			SELECT generate_series($2::timestamp, $3::timestamp, $4::interval) AS bucket
		)
		SELECT TO_CHAR(b.bucket, '%s') as date,
		       CASE WHEN $5::bool THEN TO_CHAR(b.bucket, 'HH24:00') ELSE '' END as time,
		       COALESCE(COUNT(ur.id), 0),
		       COALESCE(SUM(ur.input_tokens + ur.output_tokens), 0),
		       COALESCE(SUM(ur.total_cost), 0)
		FROM buckets b
		LEFT JOIN usage_records ur
		       ON ur.created_at >= b.bucket
		      AND ur.created_at < b.bucket + $4::interval
		      AND ur.created_at >= $2::timestamp
		      AND ur.created_at < $3::timestamp + $4::interval
		      AND ur.user_api_key_id IN (SELECT id FROM api_keys WHERE enterprise_id = $1::uuid AND key_type = 'enterprise')
		GROUP BY b.bucket
		ORDER BY b.bucket`,
		tr.Format)

	trendRows, err := database.Query(ctx, trendSQL, enterpriseID, start, end, tr.Bucket, rangeKey == "1d")
	if err == nil {
		defer trendRows.Close()
		for trendRows.Next() {
			var p DailyUsagePoint
			if err := trendRows.Scan(&p.Date, &p.Time, &p.Requests, &p.Tokens, &p.Cost); err == nil {
				d.DailyUsage = append(d.DailyUsage, p)
			}
		}
	}

	// Top models by cost in selected range
	modelRows, err := database.Query(ctx,
		`SELECT COALESCE(ur.model, 'unknown'), SUM(ur.request_count)::bigint
		 FROM usage_records ur
		 JOIN api_keys k ON ur.user_api_key_id = k.id
		 WHERE k.enterprise_id = $1::uuid AND k.key_type = 'enterprise'
		   AND ur.created_at >= NOW() - $2::interval
		 GROUP BY ur.model
		 ORDER BY SUM(ur.total_cost) DESC
		 LIMIT 5`,
		enterpriseID, tr.Interval)
	if err == nil {
		defer modelRows.Close()
		for modelRows.Next() {
			var item NameCountItem
			if err := modelRows.Scan(&item.Name, &item.Count); err == nil {
				d.TopModels = append(d.TopModels, item)
			}
		}
	}

	// Top keys by cost in selected range
	keyRows, err := database.Query(ctx,
		`SELECT COALESCE(k.id::text, ''),
		        COALESCE(k.key_prefix, ''),
		        COALESCE(k.key_prefix, ''),
		        COALESCE(SUM(ur.request_count), 0)::int,
		        COALESCE(SUM(ur.input_tokens + ur.output_tokens), 0)::bigint,
		        COALESCE(SUM(ur.total_cost), 0)::bigint
		 FROM usage_records ur
		 JOIN api_keys k ON ur.user_api_key_id = k.id
		 WHERE k.enterprise_id = $1::uuid AND k.key_type = 'enterprise'
		   AND ur.created_at >= NOW() - $2::interval
		 GROUP BY k.id, k.key_prefix
		 ORDER BY SUM(ur.total_cost) DESC
		 LIMIT 5`,
		enterpriseID, tr.Interval)
	if err == nil {
		defer keyRows.Close()
		for keyRows.Next() {
			var r EnterpriseKeyRecord
			if err := keyRows.Scan(&r.KeyID, &r.KeyPrefix, &r.KeyName, &r.Requests, &r.Tokens, &r.Cost); err == nil {
				d.TopKeys = append(d.TopKeys, r)
			}
		}
	}

	// Alerts: same dynamic range
	alertRows, err := database.Query(ctx,
		`SELECT id::text, alert_type, threshold_percent, current_value, limit_value,
		        notified_at, acknowledged_at
		 FROM enterprise_alerts
		 WHERE enterprise_id = $1::uuid AND notified_at >= NOW() - $2::interval
		 ORDER BY notified_at DESC LIMIT 20`,
		enterpriseID, tr.Interval)
	if err == nil {
		defer alertRows.Close()
		for alertRows.Next() {
			var a EnterpriseAlert
			var ackAt sql.NullTime
			if err := alertRows.Scan(&a.ID, &a.AlertType, &a.ThresholdPercent, &a.CurrentValue, &a.LimitValue, &a.NotifiedAt, &ackAt); err == nil {
				if ackAt.Valid {
					a.AcknowledgedAt = &ackAt.Time
				}
				d.Alerts = append(d.Alerts, a)
			}
		}
	}

	return d, nil
}

// ── Enterprise Users ──────────────────────────────────────────────────────────

// EnterpriseUser holds an enterprise user.
type EnterpriseUser struct {
	ID          string
	Email       string
	Name        string
	Role        string
	KeysCount   int
	Status      string
	QuotaLimit  int64
	QuotaUsed   int64
	CreditLimit int64
	CreditUsed  int64
}

// GetEnterpriseUsersData fetches users in an enterprise.
func GetEnterpriseUsersData(ctx context.Context, database *db.DB, userID string) ([]EnterpriseUser, error) {
	enterpriseID, err := getEnterpriseIDForUser(ctx, database, userID)
	if err != nil {
		return []EnterpriseUser{}, nil
	}

	rows, err := database.Query(ctx,
		`SELECT u.id::text, u.email, COALESCE(u.name, ''), u.role,
		 (SELECT COUNT(*) FROM api_keys WHERE user_id = u.id AND deleted_at IS NULL) as keys_count,
		 COALESCE(u.status, 'active') as status,
		 COALESCE(em.quota_limit, 0),
		 COALESCE(u.quota_used, 0),
		 COALESCE(em.credit_limit, 0),
		 COALESCE(u.credit_used, 0)
		 FROM users u
		 LEFT JOIN enterprise_members em ON em.user_id = u.id AND em.enterprise_id = u.enterprise_id AND em.removed_at IS NULL
		 WHERE u.enterprise_id = $1::uuid AND u.deleted_at IS NULL
		 ORDER BY u.created_at DESC`,
		enterpriseID,
	)
	if err != nil {
		return []EnterpriseUser{}, nil
	}
	defer rows.Close()

	var users []EnterpriseUser
	for rows.Next() {
		var u EnterpriseUser
		if err := rows.Scan(&u.ID, &u.Email, &u.Name, &u.Role, &u.KeysCount, &u.Status, &u.QuotaLimit, &u.QuotaUsed, &u.CreditLimit, &u.CreditUsed); err == nil {
			users = append(users, u)
		}
	}
	return users, nil
}

// EnterpriseInvitation holds a pending enterprise invitation.
// The acceptance token (a SHA-256 hash at rest) is intentionally not exposed
// here; shareable links are minted on demand via the copy-link endpoint.
type EnterpriseInvitation struct {
	ID        string
	Email     string
	Name      string
	Role      string
	Status    string
	ExpiresAt time.Time
	CreatedAt time.Time
}

// GetEnterpriseInvitationsData fetches pending/email_confirmed/ea_approved invitations for an enterprise.
func GetEnterpriseInvitationsData(ctx context.Context, database *db.DB, userID string) ([]EnterpriseInvitation, error) {
	enterpriseID, err := getEnterpriseIDForUser(ctx, database, userID)
	if err != nil {
		return []EnterpriseInvitation{}, nil
	}

	rows, err := database.Query(ctx,
		`SELECT id::text, email, COALESCE(name, ''), role, status, expires_at, created_at
		 FROM enterprise_invitations ei
		 WHERE ei.enterprise_id = $1::uuid AND ei.status IN ('pending', 'email_confirmed', 'ea_approved')
		   AND NOT EXISTS (
		     SELECT 1 FROM enterprise_members em
		     JOIN users u ON em.user_id = u.id
		     WHERE em.enterprise_id = ei.enterprise_id
		       AND em.removed_at IS NULL
		       AND u.email = ei.email
		       AND u.deleted_at IS NULL
		   )
		 ORDER BY ei.created_at DESC`,
		enterpriseID,
	)
	if err != nil {
		return []EnterpriseInvitation{}, nil
	}
	defer rows.Close()

	var invitations []EnterpriseInvitation
	for rows.Next() {
		var inv EnterpriseInvitation
		if err := rows.Scan(&inv.ID, &inv.Email, &inv.Name, &inv.Role, &inv.Status, &inv.ExpiresAt, &inv.CreatedAt); err == nil {
			invitations = append(invitations, inv)
		}
	}
	return invitations, nil
}

// ── Enterprise Keys ───────────────────────────────────────────────────────────

// EnterpriseKey holds an enterprise API key.
type EnterpriseKey struct {
	ID               string
	Name             string
	KeyPrefix        string
	UserID           string
	UserName         string
	AssignedUserID   string
	AssignedUserName string
	Status           string
	GroupName        string
	GroupID          string
	CreatedAt        time.Time
	KeyPlaintext     string
	MonthRequests    int
	MonthTokens      int64
	RateLimitRPM     int
	CurrentModel     *string
	ModelRoutingMode *string
	SelectedModels   interface{}
	AllowedModels    *string
	ExpiresAt        *time.Time
}

// GetEnterpriseKeysData fetches enterprise API keys.
func GetEnterpriseKeysData(ctx context.Context, database *db.DB, userID string) ([]EnterpriseKey, error) {
	enterpriseID, err := getEnterpriseIDForUser(ctx, database, userID)
	if err != nil {
		return []EnterpriseKey{}, nil
	}

	rows, err := database.Query(ctx,
		`SELECT k.id::text, COALESCE(k.name, ''), k.key_prefix, k.user_id::text,
		 COALESCE(u.name, u.email) as user_name,
		 COALESCE(k.assigned_user_id::text, '') as assigned_user_id,
		 COALESCE(au.name, au.email, '') as assigned_user_name,
		 k.status, 			 k.created_at,
		 COALESCE(g.name, ''),
		 COALESCE(k.group_id::text, ''),
		 COALESCE(k.key_encrypted, '') as key_plaintext,
		 k.current_model, k.model_routing_mode, k.selected_models,
		 CASE WHEN k.selected_models::text IS NOT NULL AND k.selected_models::text NOT IN ('[]', 'null', '')
		      THEN k.selected_models::text
		      WHEN k.allowed_models::text IS NOT NULL AND k.allowed_models::text NOT IN ('[]', 'null', '')
		      THEN k.allowed_models::text
		      WHEN g.models::text IS NOT NULL AND g.models::text NOT IN ('[]', 'null', '')
		      THEN g.models::text
		      ELSE (SELECT COALESCE(jsonb_agg(DISTINCT m)::text, '[]')
		            FROM (SELECT jsonb_array_elements_text(eg.models) AS m
		                  FROM user_groups eg
		                  WHERE eg.enterprise_id = $1::uuid AND eg.deleted_at IS NULL) sub)
		 END,
		 COALESCE(month_stats.month_requests, 0) as month_requests,
		 COALESCE(month_stats.month_tokens, 0) as month_tokens,
		 COALESCE(k.rate_limit_rpm, 0) as rate_limit_rpm,
		 k.expires_at
		 FROM api_keys k
		 JOIN users u ON k.user_id = u.id
		 LEFT JOIN users au ON k.assigned_user_id = au.id
		 LEFT JOIN user_groups g ON k.group_id = g.id
		 LEFT JOIN LATERAL (
			 SELECT COUNT(*) as month_requests,
			        COALESCE(SUM(input_tokens + output_tokens), 0) as month_tokens
			 FROM usage_records
			 WHERE user_api_key_id = k.id
			   AND created_at >= DATE_TRUNC('month', CURRENT_DATE)
		 ) month_stats ON true
		 WHERE k.enterprise_id = $1::uuid AND k.key_type = 'enterprise' AND k.deleted_at IS NULL
		 ORDER BY k.created_at DESC`,
		enterpriseID,
	)
	if err != nil {
		return []EnterpriseKey{}, nil
	}
	defer rows.Close()

	var keys []EnterpriseKey
	for rows.Next() {
		var k EnterpriseKey
		if err := rows.Scan(&k.ID, &k.Name, &k.KeyPrefix, &k.UserID, &k.UserName, &k.AssignedUserID, &k.AssignedUserName, &k.Status, &k.CreatedAt, &k.GroupName, &k.GroupID, &k.KeyPlaintext, &k.CurrentModel, &k.ModelRoutingMode, &k.SelectedModels, &k.AllowedModels, &k.MonthRequests, &k.MonthTokens, &k.RateLimitRPM, &k.ExpiresAt); err == nil {
			keys = append(keys, k)
		}
	}
	return keys, nil
}

// ── Enterprise Groups ─────────────────────────────────────────────────────────

// EnterpriseGroupItem holds an enterprise group.
type EnterpriseGroupItem struct {
	ID               string
	Name             string
	Models           string
	ModelList        []string
	ParentGroupName  string
	ParentGroupRatio float64
	EnterpriseRatio  float64
	Visibility       string
	SyncMode         string
	Status           string
	RPM              int
	TPM              int64
	ConcurrentLimit  int
	KeyCount         int
	Inherited        bool
	CreatedAt        time.Time
}

// GetEnterpriseGroupsData fetches enterprise groups.
func GetEnterpriseGroupsData(ctx context.Context, database *db.DB, enterpriseID string) ([]EnterpriseGroupItem, error) {
	rows, err := database.Query(ctx,
		`SELECT g.id::text, g.name, COALESCE(g.models::text, '[]'), COALESCE(g.rpm, 0), COALESCE(g.tpm, 0), g.created_at, g.status,
		        COALESCE(pg.name, ''), COALESCE(pg.pricing_ratio, 1.0),
		        COALESCE(e.pricing_ratio, 1.0), COALESCE(g.visibility, 'public'),
		        COALESCE(g.concurrent_limit, 10), g.sync_mode,
		        (SELECT COUNT(*) FROM api_keys WHERE group_id = g.id AND deleted_at IS NULL) as key_count,
		        FALSE AS inherited
		 FROM user_groups g
		 LEFT JOIN user_groups pg ON g.parent_group_id = pg.id
		 LEFT JOIN enterprises e ON g.enterprise_id = e.id
		 WHERE g.deleted_at IS NULL AND g.enterprise_id = $1::uuid
		 UNION ALL
		 -- Inherited platform template groups (read-only for the enterprise).
		 SELECT g.id::text, g.name, COALESCE(g.models::text, '[]'), COALESCE(g.rpm, 0), COALESCE(g.tpm, 0), g.created_at, g.status,
		        '', 1.0, 1.0, 'public',
		        COALESCE(g.concurrent_limit, 10), 'manual',
		        (SELECT COUNT(*) FROM api_keys WHERE group_id = g.id AND deleted_at IS NULL) as key_count,
		        TRUE AS inherited
		 FROM user_groups g
		 WHERE g.deleted_at IS NULL AND g.enterprise_id IS NULL AND g.group_type = 'enterprise' AND g.inherit_to_enterprises
		 ORDER BY name`,
		enterpriseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var groups []EnterpriseGroupItem
	for rows.Next() {
		var g EnterpriseGroupItem
		if err := rows.Scan(&g.ID, &g.Name, &g.Models, &g.RPM, &g.TPM, &g.CreatedAt, &g.Status, &g.ParentGroupName, &g.ParentGroupRatio, &g.EnterpriseRatio, &g.Visibility, &g.ConcurrentLimit, &g.SyncMode, &g.KeyCount, &g.Inherited); err == nil {
			if g.Models != "" && g.Models != "[]" && g.Models != "null" {
				var ml []string
				if err := json.Unmarshal([]byte(g.Models), &ml); err == nil {
					g.ModelList = ml
				}
			}
			groups = append(groups, g)
		}
	}
	if groups == nil {
		groups = []EnterpriseGroupItem{}
	}
	return groups, nil
}

// ── Enterprise Billing ────────────────────────────────────────────────────────

// EnterpriseInvoice holds an enterprise invoice.
type EnterpriseInvoice struct {
	ID              string
	Amount          int64
	AmountUSD       float64
	AmountLocal     float64
	DisplayCurrency string
	Status          string
	CreatedAt       time.Time
	StorageKey      string `json:"-"`
	RequestID       string `json:"-"`
	InvoiceNumber   string
	Source          string // auto | uploaded
}

// EnterpriseTransaction represents a billing-related enterprise transaction.
type EnterpriseTransaction struct {
	CreatedAt   time.Time
	Type        string // topup | quota_topup | reset | invoice | refund
	Description string
	AmountUSD   float64
}

// EnterpriseBillingData holds enterprise billing data.
type EnterpriseBillingData struct {
	EnterpriseName        string
	QuotaTotal            int64
	QuotaUsed             int64
	QuotaRemaining        int64
	CreditTotal           int64
	CreditUsed            int64
	CreditRemaining       int64
	CreditTotalUSD        float64
	CreditUsedUSD         float64
	CreditRemainingUSD    float64
	CreditTotalLocal      float64
	CreditUsedLocal       float64
	CreditRemainingLocal  float64
	CreditPercent         float64
	BillingCycle          string
	NextResetAt           *time.Time
	PricingRatio          float64
	DisplayCurrency       string
	ExchangeRate          float64
	UsageByUser           []EnterpriseUsageRecord
	UsageByModel          []EnterpriseUsageRecord
	Invoices              []EnterpriseInvoice
	Transactions          []EnterpriseTransaction
	InvoiceEligible       int64 // cents: postpaid + prepaid still-invocable (two-pool model)
	PostpaidEligibleCents int64 // cents: net un-invoiced monthly statements
	PrepaidEligibleCents  int64 // cents: consumed prepaid top-ups not yet invoiced
	InvoiceRequested      int64 // cents: sum of pending+processing requests
	InvoiceIssued         int64 // cents: sum of completed requests
	TotalConsumption      int64 // cents: postpaid statement consumption + prepaid consumed
	Statements            []EnterpriseStatement
	IsEnterpriseAdmin     bool // whether the viewing user may submit invoice requests
}

// EnterpriseStatement is one monthly billing statement row.
type EnterpriseStatement struct {
	Period          string
	CreditUsedUSD   float64
	QuotaUsed       int64
	PrepaidTopupUSD float64
	InvoicedCents   int64
	EligibleCents   int64
}

// GetEnterpriseBillingData fetches enterprise billing data.
func GetEnterpriseBillingData(ctx context.Context, database *db.DB, userID string) (*EnterpriseBillingData, error) {
	d := &EnterpriseBillingData{}

	enterpriseID, err := getEnterpriseIDForUser(ctx, database, userID)
	if err != nil {
		return d, nil
	}

	var userRole string
	_ = database.QueryRow(ctx,
		`SELECT COALESCE(role, '') FROM users WHERE id = $1::uuid AND deleted_at IS NULL`,
		userID,
	).Scan(&userRole)
	d.IsEnterpriseAdmin = userRole == "enterprise_admin"

	var nextResetAt sql.NullTime
	_ = database.QueryRow(ctx,
		`SELECT name, COALESCE(quota_total, 0), COALESCE(quota_used, 0), COALESCE(quota_total - quota_used, 0),
		 COALESCE(credit_total, 0), COALESCE(credit_used, 0), COALESCE(credit_total - credit_used, 0),
		 COALESCE(billing_cycle, 'monthly'), COALESCE(pricing_ratio, 1.0), credit_reset_at,
		 COALESCE(display_currency, 'USD'), COALESCE(exchange_rate, 1.0)
		 FROM enterprises WHERE id = $1::uuid AND deleted_at IS NULL`,
		enterpriseID,
	).Scan(&d.EnterpriseName, &d.QuotaTotal, &d.QuotaUsed, &d.QuotaRemaining,
		&d.CreditTotal, &d.CreditUsed, &d.CreditRemaining,
		&d.BillingCycle, &d.PricingRatio, &nextResetAt, &d.DisplayCurrency, &d.ExchangeRate)
	if nextResetAt.Valid {
		d.NextResetAt = &nextResetAt.Time
	} else if d.BillingCycle != "" {
		d.NextResetAt = computeNextResetAt(d.BillingCycle)
	}

	if d.CreditTotal > 0 {
		d.CreditTotalUSD = float64(d.CreditTotal) / 1_000_000.0
		d.CreditUsedUSD = float64(d.CreditUsed) / 1_000_000.0
		d.CreditRemainingUSD = float64(d.CreditRemaining) / 1_000_000.0
		d.CreditPercent = float64(d.CreditUsed) / float64(d.CreditTotal) * 100
	}
	if d.DisplayCurrency != "" && d.DisplayCurrency != "USD" && d.ExchangeRate > 0 {
		d.CreditTotalLocal = d.CreditTotalUSD * d.ExchangeRate
		d.CreditUsedLocal = d.CreditUsedUSD * d.ExchangeRate
		d.CreditRemainingLocal = d.CreditRemainingUSD * d.ExchangeRate
	}

	rows, err := database.Query(ctx,
		`SELECT u.id::text, COALESCE(u.name, u.email), SUM(ur.input_tokens + ur.output_tokens), COUNT(*),
		 SUM(ur.total_cost)::bigint / NULLIF((SELECT SUM(total_cost) FROM usage_records ur2
		 JOIN api_keys k2 ON ur2.user_api_key_id = k2.id
		 WHERE k2.enterprise_id = $1::uuid AND k2.key_type = 'enterprise'
		 AND ur2.created_at >= DATE_TRUNC('month', CURRENT_DATE)), 0) * 100 as percent
		 FROM usage_records ur
		 JOIN api_keys k ON ur.user_api_key_id = k.id
		 JOIN users u ON ur.user_id = u.id
		 WHERE k.enterprise_id = $1::uuid AND k.key_type = 'enterprise'
		 AND ur.created_at >= DATE_TRUNC('month', CURRENT_DATE)
		 GROUP BY u.id, u.name, u.email
		 ORDER BY SUM(ur.total_cost) DESC`,
		enterpriseID,
	)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var r EnterpriseUsageRecord
			if err := rows.Scan(&r.UserID, &r.UserName, &r.Tokens, &r.Requests, &r.Percent); err == nil {
				d.UsageByUser = append(d.UsageByUser, r)
			}
		}
	}

	modelRows, err := database.Query(ctx,
		`SELECT COALESCE(ur.model, 'unknown'), SUM(ur.input_tokens + ur.output_tokens), COUNT(*),
		 SUM(ur.total_cost)::bigint / NULLIF((SELECT SUM(total_cost) FROM usage_records ur2
		 JOIN api_keys k2 ON ur2.user_api_key_id = k2.id
		 WHERE k2.enterprise_id = $1::uuid AND k2.key_type = 'enterprise'
		 AND ur2.created_at >= DATE_TRUNC('month', CURRENT_DATE)), 0) * 100 as percent
		 FROM usage_records ur
		 JOIN api_keys k ON ur.user_api_key_id = k.id
		 WHERE k.enterprise_id = $1::uuid AND k.key_type = 'enterprise'
		 AND ur.created_at >= DATE_TRUNC('month', CURRENT_DATE)
		 GROUP BY ur.model
		 ORDER BY SUM(ur.total_cost) DESC LIMIT 10`,
		enterpriseID)
	if err == nil {
		defer modelRows.Close()
		for modelRows.Next() {
			var r EnterpriseUsageRecord
			if err := modelRows.Scan(&r.UserName, &r.Tokens, &r.Requests, &r.Percent); err == nil {
				d.UsageByModel = append(d.UsageByModel, r)
			}
		}
	}

	invRows, err := database.Query(ctx,
		`SELECT id::text, amount, status, created_at, COALESCE(storage_key, ''), COALESCE(invoice_source, 'auto'), COALESCE(request_id::text, ''), COALESCE(invoice_number, '')
		 FROM invoices WHERE deleted_at IS NULL AND enterprise_id = $1::uuid
		 ORDER BY created_at DESC LIMIT 10`,
		enterpriseID,
	)
	if err == nil {
		defer invRows.Close()
		for invRows.Next() {
			var inv EnterpriseInvoice
			if err := invRows.Scan(&inv.ID, &inv.Amount, &inv.Status, &inv.CreatedAt, &inv.StorageKey, &inv.Source, &inv.RequestID, &inv.InvoiceNumber); err == nil {
				inv.AmountUSD = float64(inv.Amount) / 100.0
				d.Invoices = append(d.Invoices, inv)
			}
		}
	}

	// Two-pool invoice eligibility (docs/enterprise-billing-prepost-paid-design-
	// 202611010445.md). Postpaid: per-period net of statements minus their
	// non-rejected requests. Prepaid: consumed share of cumulative top-ups
	// (top-ups − wallet balance) minus invoiced. Enterprise billing is fully
	// separate from personal balance consumption.
	var postpaidNetCents, prepaidTopupMicro, prepaidBalance, prepaidInvoiced int64
	_ = database.QueryRow(ctx,
		`SELECT GREATEST(
		     COALESCE((SELECT SUM(credit_used_micro) FROM enterprise_statements
		               WHERE enterprise_id = $1::uuid), 0) / 10000
		     - COALESCE((SELECT SUM(amount) FROM invoice_requests
		                 WHERE enterprise_id = $1::uuid AND pool = 'postpaid'
		                   AND status IN ('pending', 'processing', 'completed')), 0),
		     0)`,
		enterpriseID).Scan(&postpaidNetCents)
	var postpaidConsumptionCents int64
	_ = database.QueryRow(ctx,
		`SELECT COALESCE(SUM(credit_used_micro), 0) FROM enterprise_statements
		 WHERE enterprise_id = $1::uuid`,
		enterpriseID).Scan(&postpaidConsumptionCents)
	postpaidConsumptionCents /= 10000
	var invoiceRequested, invoiceIssued int64
	_ = database.QueryRow(ctx,
		`SELECT COALESCE(SUM(amount), 0) FROM invoice_requests
		 WHERE enterprise_id = $1::uuid AND status IN ('pending', 'processing')`,
		enterpriseID).Scan(&invoiceRequested)
	_ = database.QueryRow(ctx,
		`SELECT COALESCE(SUM(amount), 0) FROM invoice_requests
		 WHERE enterprise_id = $1::uuid AND status = 'completed'`,
		enterpriseID).Scan(&invoiceIssued)
	_ = database.QueryRow(ctx,
		`SELECT COALESCE(SUM(delta), 0) FROM enterprise_audit_log
		 WHERE enterprise_id = $1::uuid AND change_type = 'prepaid_topup' AND field_name = 'prepaid_balance'`,
		enterpriseID).Scan(&prepaidTopupMicro)
	_ = database.QueryRow(ctx,
		`SELECT COALESCE(prepaid_balance, 0) FROM enterprises WHERE id = $1::uuid`,
		enterpriseID).Scan(&prepaidBalance)
	_ = database.QueryRow(ctx,
		`SELECT COALESCE(SUM(amount), 0) FROM invoice_requests
		 WHERE enterprise_id = $1::uuid AND pool = 'prepaid' AND status IN ('pending', 'processing', 'completed')`,
		enterpriseID).Scan(&prepaidInvoiced)
	prepaidConsumedCents := (prepaidTopupMicro - prepaidBalance) / 10000
	if prepaidConsumedCents < 0 {
		prepaidConsumedCents = 0
	}

	d.PostpaidEligibleCents = postpaidNetCents
	d.PrepaidEligibleCents = prepaidConsumedCents - prepaidInvoiced
	if d.PrepaidEligibleCents < 0 {
		d.PrepaidEligibleCents = 0
	}
	d.InvoiceEligible = d.PostpaidEligibleCents + d.PrepaidEligibleCents
	d.TotalConsumption = postpaidConsumptionCents + prepaidConsumedCents
	d.InvoiceRequested = invoiceRequested
	d.InvoiceIssued = invoiceIssued

	// Per-statement rows for the billing page history.
	stmtRows, err := database.Query(ctx,
		`SELECT s.period, s.credit_used_micro, s.quota_used, s.prepaid_topup_micro,
		        COALESCE(r.invoiced, 0),
		        GREATEST(s.credit_used_micro / 10000 - COALESCE(r.invoiced, 0), 0)
		 FROM enterprise_statements s
		 LEFT JOIN (
		     SELECT period, SUM(amount) AS invoiced FROM invoice_requests
		     WHERE enterprise_id = $1::uuid AND pool = 'postpaid'
		       AND status IN ('pending', 'processing', 'completed')
		     GROUP BY period
		 ) r ON r.period = s.period
		 WHERE s.enterprise_id = $1::uuid
		 ORDER BY s.period DESC LIMIT 24`,
		enterpriseID)
	if err == nil {
		defer stmtRows.Close()
		for stmtRows.Next() {
			var st EnterpriseStatement
			var creditMicro, prepaidTopup, invoiced, eligible int64
			if err := stmtRows.Scan(&st.Period, &creditMicro, &st.QuotaUsed, &prepaidTopup, &invoiced, &eligible); err == nil {
				st.CreditUsedUSD = float64(creditMicro) / 1_000_000.0
				st.PrepaidTopupUSD = float64(prepaidTopup) / 1_000_000.0
				st.InvoicedCents = invoiced
				st.EligibleCents = eligible
				d.Statements = append(d.Statements, st)
			}
		}
	}

	// Transactions: credit/quota top-ups, monthly resets, invoices and refunds.
	// Order by created_at DESC so the newest entries appear first.
	txRows, err := database.Query(ctx,
		`SELECT created_at, change_type, field_name, COALESCE(delta, 0), COALESCE(reason, '')
		 FROM enterprise_audit_log
		 WHERE enterprise_id = $1::uuid
		   AND category = 'billing'
		   AND change_type IN ('credit_topup', 'quota_topup', 'monthly_reset')
		 ORDER BY created_at DESC LIMIT 100`,
		enterpriseID)
	if err == nil {
		defer txRows.Close()
		for txRows.Next() {
			var t EnterpriseTransaction
			var changeType, fieldName, reason string
			var delta int64
			if err := txRows.Scan(&t.CreatedAt, &changeType, &fieldName, &delta, &reason); err == nil {
				switch changeType {
				case "credit_topup":
					t.Type = "topup"
					t.Description = "Credit top-up"
					if reason != "" {
						t.Description += ": " + reason
					}
					t.AmountUSD = float64(delta) / 1_000_000.0
				case "quota_topup":
					t.Type = "quota_topup"
					t.Description = "Quota top-up"
					if reason != "" {
						t.Description += ": " + reason
					}
					t.AmountUSD = 0
				case "monthly_reset":
					t.Type = "reset"
					t.Description = "Monthly reset"
					if reason != "" {
						t.Description += ": " + reason
					}
					t.AmountUSD = 0
				}
				d.Transactions = append(d.Transactions, t)
			}
		}
	}

	// Add invoice records as transactions (negative because they consume eligible credit).
	invTxRows, err := database.Query(ctx,
		`SELECT created_at, status, amount_cents
		 FROM invoices
		 WHERE deleted_at IS NULL AND enterprise_id = $1::uuid
		 ORDER BY created_at DESC LIMIT 100`,
		enterpriseID)
	if err == nil {
		defer invTxRows.Close()
		for invTxRows.Next() {
			var t EnterpriseTransaction
			var status string
			var amountCents int64
			if err := invTxRows.Scan(&t.CreatedAt, &status, &amountCents); err == nil {
				t.Type = "invoice"
				t.Description = "Invoice " + status
				t.AmountUSD = -float64(amountCents) / 100.0
				d.Transactions = append(d.Transactions, t)
			}
		}
	}

	// Sort combined transactions by created_at DESC.
	sort.Slice(d.Transactions, func(i, j int) bool {
		return d.Transactions[i].CreatedAt.After(d.Transactions[j].CreatedAt)
	})
	if len(d.Transactions) > 100 {
		d.Transactions = d.Transactions[:100]
	}

	return d, nil
}

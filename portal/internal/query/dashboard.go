package query

import (
	"context"
	"fmt"
	"time"

	"github.com/ai-proxy/ai-portal-go/internal/db"
)

// InvoiceRequestItem holds a recent invoice for dashboard display.
// Amount is in micro-USD (invoice_requests.amount is stored in USD cents and
// converted at query time) so the formatMoney template helper keeps working.
type InvoiceRequestItem struct {
	ID        string
	Title     string
	Amount    int64
	Status    string
	OrderID   string
	CreatedAt time.Time
}

// DashboardData holds user dashboard page data.
type DashboardData struct {
	Balance               int64
	GiftBalance           int64
	TotalBalance          int64
	ActiveKeys            int
	TodayTokens           int64
	TodayRequests         int
	MonthTokens           int64
	MonthRequests         int
	PendingInvoices       int
	RecentInvoices        []InvoiceRequestItem
	ActiveSubs            int
	TotalQuota            int64
	QuotaUsed             int64
	QuotaRemaining        int64
	TokenQuotaRemaining   int64
	RequestQuotaRemaining int64
	// MemberEnterprise is set when the user belongs to an enterprise: the
	// member-facing view of their org, granted token quota, and monthly
	// credit cap. Nil for standalone users.
	MemberEnterprise *MemberEnterpriseInfo
	// Subscription balance in USD (amount)
	SubscriptionRecharge int64
	SubscriptionBalance  int64
	// Usage trend (last 30 days, used by new dashboard line chart)
	UsageTrendLabels   []string
	UsageTrendTokens   []int64
	UsageTrendRequests []int64
	UsageTrendCost     []int64 // micro-USD
	// Top models by tokens (24h)
	TopUserModels []NameCountItem
	// Per-model usage detail for paired charts (24h)
	UserModelDaily       map[string][]int64                   // per-model daily requests (current month)
	UserModelDailyTokens map[string]*ModelDailyTokenBreakdown // per-model daily token breakdown
	UserModelDetails     []UserModelDetail
	// Daily cost data (30d) for bar chart (legacy, kept for compatibility)
	DailyCostLabels  []string
	DailyCostAmounts []float64
	// Today's spend (micro-USD)
	TodayCost int64
}

// MemberEnterpriseInfo is the member-facing slice of enterprise membership
// shown on the user dashboard: org name, role, granted quota, and monthly
// caps. CreditLimit/CreditUsed are micro-USD; a zero CreditLimit means no cap.
type MemberEnterpriseInfo struct {
	Name          string
	Role          string  // enterprise_members.role: 'member' | 'admin'
	QuotaCap      int64   // monthly token cap (enterprise_members.quota_limit; falls back to granted quota_total)
	QuotaUsed     int64   // tokens consumed this month (resets with the enterprise monthly reset)
	CreditLimit   int64   // monthly micro-USD cap (enterprise_members.credit_limit; 0 = no cap)
	CreditUsed    int64   // micro-USD consumed this month
	CreditPercent float64 // CreditUsed/CreditLimit*100; 0 when no cap
	QuotaPercent  float64 // QuotaUsed/QuotaCap*100; 0 when no cap
}

// UserModelDetail holds per-model breakdown for the dashboard charts.
type UserModelDetail struct {
	Model         string
	Requests      int64
	Tokens        int64
	CacheHit      int64
	CacheCreation int64
	CostUSD       float64
}

// ModelDailyTokenBreakdown stores per-day token breakdown for a single model.
type ModelDailyTokenBreakdown struct {
	CacheHit  []int64 // cached_input_tokens per day
	CacheMiss []int64 // input_tokens - cached_input_tokens per day
	Output    []int64 // output_tokens per day
}

// GetDashboardData fetches user dashboard data.
// Used by the page renderer (web/data.go).
func GetDashboardData(ctx context.Context, database *db.DB, userID string) (*DashboardData, error) {
	d := &DashboardData{}

	_ = database.QueryRow(ctx, `SELECT COALESCE(balance, 0), COALESCE(gift_balance, 0) FROM users WHERE id = $1::uuid`, userID).Scan(&d.Balance, &d.GiftBalance)
	d.TotalBalance = d.Balance + d.GiftBalance
	_ = database.QueryRow(ctx,
		`SELECT COUNT(*) FROM api_keys WHERE user_id = $1::uuid AND status = 'active' AND deleted_at IS NULL`,
		userID,
	).Scan(&d.ActiveKeys)

	// "Today"/"this month" use the database CURRENT_DATE, i.e. the DB session
	// timezone. The DB should be configured as UTC so these align with the UTC
	// daily-trend boundaries computed below. (The usage-summary endpoint on the
	// usage-logs page uses the same CURRENT_DATE basis for consistency.)
	_ = database.QueryRow(ctx,
		`SELECT COALESCE(SUM(input_tokens + output_tokens), 0), COALESCE(SUM(request_count), 0), COALESCE(SUM(total_cost), 0)
		 FROM usage_records WHERE user_id = $1::uuid AND created_at >= CURRENT_DATE`,
		userID,
	).Scan(&d.TodayTokens, &d.TodayRequests, &d.TodayCost)

	_ = database.QueryRow(ctx,
		`SELECT COALESCE(SUM(input_tokens + output_tokens), 0), COALESCE(SUM(request_count), 0)
		 FROM usage_records WHERE user_id = $1::uuid AND created_at >= date_trunc('month', CURRENT_DATE)`,
		userID,
	).Scan(&d.MonthTokens, &d.MonthRequests)

	_ = database.QueryRow(ctx,
		`SELECT COUNT(*), COALESCE(SUM(quota_total), 0), COALESCE(SUM(quota_used), 0), COALESCE(SUM(quota_remaining), 0)
		 FROM user_subscriptions WHERE deleted_at IS NULL AND user_id = $1::uuid AND status = 'active'`,
		userID,
	).Scan(&d.ActiveSubs, &d.TotalQuota, &d.QuotaUsed, &d.QuotaRemaining)

	_ = database.QueryRow(ctx,
		`SELECT COALESCE(SUM(quota_remaining), 0)
		 FROM user_subscriptions WHERE deleted_at IS NULL AND user_id = $1::uuid AND status = 'active' AND plan_type = 'token'`,
		userID,
	).Scan(&d.TokenQuotaRemaining)

	_ = database.QueryRow(ctx,
		`SELECT COALESCE(SUM(quota_remaining), 0)
		 FROM user_subscriptions WHERE deleted_at IS NULL AND user_id = $1::uuid AND status = 'active' AND plan_type != 'token'`,
		userID,
	).Scan(&d.RequestQuotaRemaining)

	// Member-enterprise context (nil for standalone users). Monthly credit
	// counters reset with the enterprise monthly billing reset; CreditLimit
	// comes from enterprise_members (0 = no per-member monthly cap).
	var me MemberEnterpriseInfo
	if err := database.QueryRow(ctx,
		`SELECT e.name, COALESCE(em.role, 'member'),
		        COALESCE(NULLIF(em.quota_limit, 0), u.quota_total, 0),
		        COALESCE(u.quota_used, 0),
		        COALESCE(em.credit_limit, 0), COALESCE(u.credit_used, 0)
		 FROM users u
		 JOIN enterprises e ON e.id = u.enterprise_id AND e.deleted_at IS NULL
		 LEFT JOIN enterprise_members em
		   ON em.enterprise_id = u.enterprise_id AND em.user_id = u.id AND em.deleted_at IS NULL
		 WHERE u.id = $1::uuid`,
		userID,
	).Scan(&me.Name, &me.Role, &me.QuotaCap, &me.QuotaUsed, &me.CreditLimit, &me.CreditUsed); err == nil {
		if me.QuotaCap > 0 {
			me.QuotaPercent = float64(me.QuotaUsed) * 100.0 / float64(me.QuotaCap)
		}
		if me.CreditLimit > 0 {
			me.CreditPercent = float64(me.CreditUsed) * 100.0 / float64(me.CreditLimit)
		}
		d.MemberEnterprise = &me
	}

	// Subscription recharge total (all completed subscription orders)
	_ = database.QueryRow(ctx,
		`SELECT COALESCE(SUM(amount), 0) FROM orders
		 WHERE deleted_at IS NULL AND user_id = $1::uuid AND status = 'completed'
		   AND product_type IN ('subscription', 'subscription_renewal')`,
		userID,
	).Scan(&d.SubscriptionRecharge)

	// Calculate subscription balance proportionally based on remaining quota
	var subQuotaTotal, subQuotaRemaining int64
	_ = database.QueryRow(ctx,
		`SELECT COALESCE(SUM(quota_total), 0), COALESCE(SUM(quota_remaining), 0)
		 FROM user_subscriptions WHERE deleted_at IS NULL AND user_id = $1::uuid AND status = 'active'`,
		userID,
	).Scan(&subQuotaTotal, &subQuotaRemaining)
	if subQuotaTotal > 0 {
		d.SubscriptionBalance = int64(float64(d.SubscriptionRecharge) * float64(subQuotaRemaining) / float64(subQuotaTotal))
	} else {
		d.SubscriptionBalance = d.SubscriptionRecharge
	}

	_ = database.QueryRow(ctx,
		`SELECT COUNT(*) FROM invoice_requests WHERE deleted_at IS NULL AND user_id = $1::uuid AND status = 'pending'`,
		userID,
	).Scan(&d.PendingInvoices)

	invRows, err := database.Query(ctx,
		`SELECT id::text, COALESCE(title, ''), amount * 10000, COALESCE(status, 'pending'),
		        COALESCE(order_id::text, ''), created_at
		 FROM invoice_requests WHERE deleted_at IS NULL AND user_id = $1::uuid ORDER BY created_at DESC LIMIT 5`,
		userID,
	)
	if err == nil {
		defer invRows.Close()
		for invRows.Next() {
			var inv InvoiceRequestItem
			if err := invRows.Scan(&inv.ID, &inv.Title, &inv.Amount, &inv.Status, &inv.OrderID, &inv.CreatedAt); err == nil {
				d.RecentInvoices = append(d.RecentInvoices, inv)
			}
		}
	}

	// Usage trend: daily requests/tokens/cost for last 30 days. All time
	// boundaries here use UTC so the dashboard daily trend is consistent with
	// the usage-logs trend endpoints (GetUserUsageTrend etc.), which also use UTC.
	now := time.Now().UTC()
	for i := 29; i >= 0; i-- {
		d.UsageTrendLabels = append(d.UsageTrendLabels, now.AddDate(0, 0, -i).Format("01-02"))
	}
	trendRows, _ := database.Query(ctx,
		`SELECT created_at::date,
		        COALESCE(SUM(request_count), 0)::bigint,
		        COALESCE(SUM(input_tokens + output_tokens), 0)::bigint,
		        COALESCE(SUM(total_cost), 0)::bigint
		 FROM usage_records WHERE user_id = $1::uuid AND created_at >= $2
		 GROUP BY created_at::date ORDER BY 1`,
		userID, now.AddDate(0, 0, -30))
	if trendRows != nil {
		defer trendRows.Close()
		type trendDay struct {
			requests int64
			tokens   int64
			cost     int64
		}
		trendMap := make(map[string]trendDay)
		for trendRows.Next() {
			var t time.Time
			var day trendDay
			if err := trendRows.Scan(&t, &day.requests, &day.tokens, &day.cost); err == nil {
				trendMap[t.Format("2006-01-02")] = day
			}
		}
		for _, label := range d.UsageTrendLabels {
			day := trendMap[now.Format("20060102")+label]
			d.UsageTrendRequests = append(d.UsageTrendRequests, day.requests)
			d.UsageTrendTokens = append(d.UsageTrendTokens, day.tokens)
			d.UsageTrendCost = append(d.UsageTrendCost, day.cost)
		}
	}

	// Top models by tokens (last 30 days, aligned with usage trend default)
	modelRows, _ := database.Query(ctx,
		`SELECT model, SUM(input_tokens + output_tokens)::bigint
		 FROM usage_records WHERE user_id = $1::uuid AND created_at >= $2
		 GROUP BY model ORDER BY 2 DESC LIMIT 5`,
		userID, now.AddDate(0, 0, -30))
	if modelRows != nil {
		defer modelRows.Close()
		for modelRows.Next() {
			var item NameCountItem
			if err := modelRows.Scan(&item.Name, &item.Count); err == nil {
				d.TopUserModels = append(d.TopUserModels, item)
			}
		}
	}

	// Per-model detail: requests, tokens, cache for paired charts (last 30 days)
	detailRows, _ := database.Query(ctx,
		`SELECT model,
		        COALESCE(SUM(request_count), 0)::bigint,
		        COALESCE(SUM(input_tokens + output_tokens), 0)::bigint,
		        COALESCE(SUM(cached_input_tokens), 0)::bigint,
		        COALESCE(SUM(total_cost), 0)
		 FROM usage_records WHERE user_id = $1::uuid AND created_at >= $2
		 GROUP BY model ORDER BY 2 DESC`,
		userID, now.AddDate(0, 0, -30))
	if detailRows != nil {
		defer detailRows.Close()
		for detailRows.Next() {
			var detail UserModelDetail
			var cost int64
			if err := detailRows.Scan(&detail.Model, &detail.Requests, &detail.Tokens, &detail.CacheHit, &cost); err == nil {
				detail.CostUSD = float64(cost) / 1000000.0
				d.UserModelDetails = append(d.UserModelDetails, detail)
			}
		}
	}

	// Daily cost for current calendar month, using UTC month boundaries
	// (consistent with the trend range above).
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	daysInMonth := time.Date(now.Year(), now.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()

	// Per-model daily requests for current month
	d.UserModelDaily = make(map[string][]int64)
	mdRows, _ := database.Query(ctx,
		`SELECT model, created_at::date, COALESCE(SUM(request_count), 0)::bigint
		 FROM usage_records WHERE user_id = $1::uuid AND created_at >= $2 AND created_at < $3
		 GROUP BY model, created_at::date ORDER BY model, created_at::date`,
		userID, monthStart, monthStart.AddDate(0, 1, 0))
	if mdRows != nil {
		defer mdRows.Close()
		for mdRows.Next() {
			var model string
			var t time.Time
			var cnt int64
			if err := mdRows.Scan(&model, &t, &cnt); err == nil && t.Day() >= 1 && t.Day() <= daysInMonth {
				arr := d.UserModelDaily[model]
				if arr == nil {
					arr = make([]int64, daysInMonth)
				}
				arr[t.Day()-1] = cnt
				d.UserModelDaily[model] = arr
			}
		}
	}

	// Per-model daily tokens breakdown (cache hit / cache miss / output) for current month
	d.UserModelDailyTokens = make(map[string]*ModelDailyTokenBreakdown)
	tokenRows, _ := database.Query(ctx,
		`SELECT model, created_at::date,
		        COALESCE(SUM(cached_input_tokens), 0)::bigint,
		        COALESCE(SUM(input_tokens - cached_input_tokens), 0)::bigint,
		        COALESCE(SUM(output_tokens), 0)::bigint
		 FROM usage_records WHERE user_id = $1::uuid AND created_at >= $2 AND created_at < $3
		 GROUP BY model, created_at::date ORDER BY model, created_at::date`,
		userID, monthStart, monthStart.AddDate(0, 1, 0))
	if tokenRows != nil {
		defer tokenRows.Close()
		for tokenRows.Next() {
			var model string
			var t time.Time
			var cacheHit, cacheMiss, output int64
			if err := tokenRows.Scan(&model, &t, &cacheHit, &cacheMiss, &output); err == nil && t.Day() >= 1 && t.Day() <= daysInMonth {
				bd := d.UserModelDailyTokens[model]
				if bd == nil {
					bd = &ModelDailyTokenBreakdown{
						CacheHit:  make([]int64, daysInMonth),
						CacheMiss: make([]int64, daysInMonth),
						Output:    make([]int64, daysInMonth),
					}
					d.UserModelDailyTokens[model] = bd
				}
				bd.CacheHit[t.Day()-1] = cacheHit
				bd.CacheMiss[t.Day()-1] = cacheMiss
				bd.Output[t.Day()-1] = output
			}
		}
	}

	for i := 0; i < daysInMonth; i++ {
		d.DailyCostLabels = append(d.DailyCostLabels, fmt.Sprintf("%02d-%02d", now.Month(), i+1))
	}
	costRows, _ := database.Query(ctx,
		`SELECT created_at::date, COALESCE(SUM(total_cost), 0)::bigint
		 FROM usage_records WHERE user_id = $1::uuid AND created_at >= $2 AND created_at < $3
		 GROUP BY created_at::date ORDER BY 1`,
		userID, monthStart, monthStart.AddDate(0, 1, 0))
	if costRows != nil {
		defer costRows.Close()
		costMap := make(map[int]float64)
		for costRows.Next() {
			var t time.Time
			var cost int64
			if err := costRows.Scan(&t, &cost); err == nil {
				costMap[t.Day()] = float64(cost) / 1000000.0
			}
		}
		for i := 1; i <= daysInMonth; i++ {
			d.DailyCostAmounts = append(d.DailyCostAmounts, costMap[i])
		}
	}

	return d, nil
}

// AdminAuditLogItem holds an audit log entry for dashboard display.
type AdminAuditLogItem struct {
	ID        int64
	Email     string
	Category  string
	Action    string
	Level     string
	Message   string
	CreatedAt time.Time
}

// NameCountItem is a generic name+count pair for rankings.
type NameCountItem struct {
	Name  string
	Count int64
}

// AdminDashboardData holds admin overview stats.
type AdminDashboardData struct {
	TotalUsers           int
	NewUsersThisWeek     int
	TodayRequests        int64
	RequestsTrend        int
	ActiveModels         int
	ActiveUpstreamKeys   int
	DisabledUpstreamKeys int
	TotalRevenue         int64
	ActiveEnterprises    int
	AlertCount           int
	AvgLatencyMs         int
	UnhealthyProviders   int
	// Upstream metrics (last 24h)
	UpstreamRequests24h int64
	UpstreamErrorRate   float64
	// Provider health breakdown (last 15 min)
	ProviderHealthy   int
	ProviderDegraded  int
	ProviderUnhealthy int
	ProviderUnknown   int
	// Rankings
	TopModels  []NameCountItem
	TopAPIKeys []NameCountItem
	// Reconciliation cost diff (7d)
	ReconUpstreamCostUSD float64
	ReconBilledCostUSD   float64
	ReconCostDiffUSD     float64
	RecentUsers          []AdminUserItem
	RecentAuditLogs      []AdminAuditLogItem
}

// GetAdminDashboardData fetches admin dashboard data.
// Used by the page renderer (web/data.go).
func GetAdminDashboardData(ctx context.Context, database *db.DB) (*AdminDashboardData, error) {
	d := &AdminDashboardData{}

	_ = database.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE deleted_at IS NULL`).Scan(&d.TotalUsers)
	_ = database.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE deleted_at IS NULL AND created_at >= NOW() - INTERVAL '7 days'`).Scan(&d.NewUsersThisWeek)
	_ = database.QueryRow(ctx, `SELECT COALESCE(SUM(request_count), 0) FROM usage_records WHERE created_at >= NOW() - INTERVAL '1 day'`).Scan(&d.TodayRequests)
	_ = database.QueryRow(ctx, `SELECT COUNT(DISTINCT model) FROM usage_records WHERE created_at >= NOW() - INTERVAL '30 days'`).Scan(&d.ActiveModels)
	_ = database.QueryRow(ctx, `SELECT COUNT(*) FROM upstream_keys WHERE deleted_at IS NULL AND enabled = true`).Scan(&d.ActiveUpstreamKeys)
	_ = database.QueryRow(ctx, `SELECT COUNT(*) FROM upstream_keys WHERE deleted_at IS NULL AND enabled = false`).Scan(&d.DisabledUpstreamKeys)
	_ = database.QueryRow(ctx, `SELECT COUNT(*) FROM enterprises WHERE deleted_at IS NULL AND status = 'active'`).Scan(&d.ActiveEnterprises)
	_ = database.QueryRow(ctx, `SELECT COUNT(*) FROM audit_logs WHERE level IN ('error','critical') AND created_at >= NOW() - INTERVAL '24 hours'`).Scan(&d.AlertCount)
	_ = database.QueryRow(ctx, `SELECT COALESCE(AVG(latency_ms)::int, 0) FROM usage_records WHERE created_at >= NOW() - INTERVAL '1 hour' AND latency_ms > 0`).Scan(&d.AvgLatencyMs)
	// Abnormal providers: unhealthy/degraded in the last 15 min of health
	// stats (NOT disabled providers — disabling unused regions is normal).
	_ = database.QueryRow(ctx, `
		SELECT COUNT(*) FROM (
			SELECT provider,
				MAX(CASE status WHEN 'unhealthy' THEN 3 WHEN 'degraded' THEN 2 WHEN 'healthy' THEN 1 ELSE 0 END) AS sev
			FROM provider_health_stats
			WHERE bucket >= NOW() - INTERVAL '15 minutes'
			GROUP BY provider
		) t WHERE sev >= 2
	`).Scan(&d.UnhealthyProviders)

	// Upstream metrics (today, UTC). upstream_key_usage.date is day-granular,
	// so a true rolling 24h is not expressible; scope to today and label the
	// cards accordingly (a 2-day window was previously shown as "24h").
	_ = database.QueryRow(ctx, `SELECT COALESCE(SUM(request_count), 0) FROM upstream_key_usage WHERE date >= CURRENT_DATE`).Scan(&d.UpstreamRequests24h)
	var upErr, upReq int64
	_ = database.QueryRow(ctx, `SELECT COALESCE(SUM(error_count), 0), COALESCE(SUM(request_count), 0) FROM upstream_key_usage WHERE date >= CURRENT_DATE`).Scan(&upErr, &upReq)
	if upReq > 0 {
		d.UpstreamErrorRate = float64(upErr) / float64(upReq) * 100
	}

	// Top models by requests (7d)
	modelRows, _ := database.Query(ctx, `SELECT model, SUM(request_count)::bigint FROM usage_records WHERE created_at >= NOW() - INTERVAL '7 days' GROUP BY model ORDER BY 2 DESC LIMIT 5`)
	if modelRows != nil {
		defer modelRows.Close()
		for modelRows.Next() {
			var item NameCountItem
			if err := modelRows.Scan(&item.Name, &item.Count); err == nil {
				d.TopModels = append(d.TopModels, item)
			}
		}
	}

	// Top API keys by cost (7d)
	keyRows, _ := database.Query(ctx, `SELECT COALESCE(k.key_prefix, 'unknown'), SUM(ur.total_cost)::bigint FROM usage_records ur LEFT JOIN api_keys k ON k.id = ur.user_api_key_id WHERE ur.created_at >= NOW() - INTERVAL '7 days' GROUP BY k.key_prefix ORDER BY 2 DESC LIMIT 5`)
	if keyRows != nil {
		defer keyRows.Close()
		for keyRows.Next() {
			var item NameCountItem
			if err := keyRows.Scan(&item.Name, &item.Count); err == nil {
				d.TopAPIKeys = append(d.TopAPIKeys, item)
			}
		}
	}

	// Provider health breakdown (last 15 min)
	_ = database.QueryRow(ctx, `
		SELECT
			COALESCE(COUNT(*) FILTER (WHERE status = 'healthy'), 0),
			COALESCE(COUNT(*) FILTER (WHERE status = 'degraded'), 0),
			COALESCE(COUNT(*) FILTER (WHERE status = 'unhealthy'), 0),
			COALESCE(COUNT(*) FILTER (WHERE status = 'unknown'), 0)
		FROM (
			SELECT provider,
				CASE MAX(CASE status WHEN 'unhealthy' THEN 3 WHEN 'degraded' THEN 2 WHEN 'healthy' THEN 1 ELSE 0 END)
					WHEN 3 THEN 'unhealthy' WHEN 2 THEN 'degraded' WHEN 1 THEN 'healthy' ELSE 'unknown' END as status
			FROM provider_health_stats
			WHERE bucket >= NOW() - INTERVAL '15 minutes'
			GROUP BY provider
		) t
	`).Scan(&d.ProviderHealthy, &d.ProviderDegraded, &d.ProviderUnhealthy, &d.ProviderUnknown)

	// Reconciliation cost diff (last 7d) - upstream vs billed cost estimate.
	// Window alignment: upstream side is day-granular, so use 7 calendar days
	// (today + 6 back) to mirror the billed side's rolling 7x24h as closely as
	// the granularity allows (CURRENT_DATE - 7 would silently span 8 days).
	// Bedrock's input_tokens already include cache reads, so price only the
	// cache-miss remainder at the miss rate (same fix as reconciliation-ops).
	var upCostF, billedCostF float64
	_ = database.QueryRow(ctx,
		`SELECT COALESCE(SUM(
			COALESCE(GREATEST(u.input_tokens - u.cache_read_tokens, 0), 0) * COALESCE(sm.input_price, 0) +
			COALESCE(u.output_tokens, 0) * COALESCE(sm.output_price, 0) +
			COALESCE(u.cache_read_tokens, 0) * COALESCE(sm.input_price_cache_hit, 0)
		), 0) FROM upstream_key_usage u
		LEFT JOIN service_models sm ON sm.provider = u.provider AND sm.product_id = u.model
		WHERE u.date >= CURRENT_DATE - 6`,
	).Scan(&upCostF)
	_ = database.QueryRow(ctx,
		`SELECT COALESCE(SUM(COALESCE(total_cost, 0))::numeric / 1000000.0, 0)
		 FROM usage_records WHERE created_at >= NOW() - INTERVAL '7 days'`,
	).Scan(&billedCostF)
	d.ReconUpstreamCostUSD = upCostF
	d.ReconBilledCostUSD = billedCostF
	d.ReconCostDiffUSD = upCostF - billedCostF
	if d.ReconCostDiffUSD < 0 {
		d.ReconCostDiffUSD = 0
	}

	var todayReqs, yesterdayReqs int64
	_ = database.QueryRow(ctx, `SELECT COALESCE(SUM(request_count), 0) FROM usage_records WHERE created_at >= NOW() - INTERVAL '1 day'`).Scan(&todayReqs)
	_ = database.QueryRow(ctx, `SELECT COALESCE(SUM(request_count), 0) FROM usage_records WHERE created_at >= NOW() - INTERVAL '2 days' AND created_at < NOW() - INTERVAL '1 day'`).Scan(&yesterdayReqs)
	if yesterdayReqs > 0 {
		d.RequestsTrend = int((todayReqs - yesterdayReqs) * 100 / yesterdayReqs)
	}

	_ = database.QueryRow(ctx, `SELECT COALESCE(SUM(total_cost), 0) FROM usage_records`).Scan(&d.TotalRevenue)

	userRows, _ := database.Query(ctx, `SELECT id::text, email, COALESCE(name, ''), role, COALESCE(balance, 0), created_at FROM users WHERE deleted_at IS NULL ORDER BY created_at DESC LIMIT 10`)
	if userRows != nil {
		defer userRows.Close()
		for userRows.Next() {
			var u AdminUserItem
			if err := userRows.Scan(&u.ID, &u.Email, &u.Name, &u.Role, &u.Balance, &u.CreatedAt); err == nil {
				d.RecentUsers = append(d.RecentUsers, u)
			}
		}
	}

	logRows, _ := database.Query(ctx, `SELECT id, user_email, category, action, level, message, created_at FROM audit_logs ORDER BY created_at DESC LIMIT 10`)
	if logRows != nil {
		defer logRows.Close()
		for logRows.Next() {
			var l AdminAuditLogItem
			if err := logRows.Scan(&l.ID, &l.Email, &l.Category, &l.Action, &l.Level, &l.Message, &l.CreatedAt); err == nil {
				d.RecentAuditLogs = append(d.RecentAuditLogs, l)
			}
		}
	}

	return d, nil
}

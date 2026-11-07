package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/mayiants/ai-portal-go/internal/query"
	"github.com/mayiants/ai-portal-go/internal/routes"
)

// DashboardData holds dashboard page data
type DashboardData struct {
	Balance         int64
	GiftBalance     int64
	TotalBalance    int64
	ActiveKeys      int
	TodayTokens     int64
	TodayRequests   int
	MonthTokens     int64
	MonthRequests   int
	PendingInvoices int
	RecentInvoices  []query.InvoiceRequest
	// Subscription quota stats
	ActiveSubs     int
	TotalQuota     int64
	QuotaUsed      int64
	QuotaRemaining int64
	// Per-plan-type quota stats
	TokenQuotaRemaining   int64
	RequestQuotaRemaining int64
}

// GetDashboardData fetches data for the dashboard via the unified routes layer.
func (h *Handler) GetDashboardData(ctx context.Context, userID string) (*query.DashboardData, error) {
	return query.GetDashboardData(ctx, h.db, userID)
}

// GetRecentKeys returns the 5 most recently used API keys for the dashboard.
func (h *Handler) GetRecentKeys(ctx context.Context, userID string, timezone string, locale string) []RecentKey {
	rawKeys, err := query.GetRecentKeysData(ctx, h.db, userID)
	if err != nil {
		return nil
	}
	var keys []RecentKey
	for _, k := range rawKeys {
		lastUsedStr := h.i18n.T("status.unused", locale)
		if !k.LastUsed.IsZero() {
			lastUsedStr = formatUserTime(k.LastUsed, timezone, h.i18n.T("label.yesterday", locale))
		}
		keys = append(keys, RecentKey{
			Name:      k.Name,
			KeyPrefix: k.KeyPrefix,
			Status:    k.Status,
			LastUsed:  lastUsedStr,
		})
	}
	return keys
}

// formatUserTime formats a time for user-facing display with relative dates.
//   - Today: "03:54"
//   - Yesterday: "Yesterday 03:54" / "昨天 03:54"
//   - Otherwise: "06-01 03:54"
func formatUserTime(t time.Time, timezone string, yesterdayText string) string {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		loc = time.UTC
	}
	t = t.In(loc)
	now := time.Now().In(loc)
	if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
		return t.Format("15:04")
	}
	if t.Year() == now.Year() && t.YearDay() == now.YearDay()-1 {
		return yesterdayText + t.Format("15:04")
	}
	return t.Format("01-02 15:04")
}

// RecentKey holds a single recent API key for the dashboard partial
type RecentKey struct {
	Name      string
	KeyPrefix string
	Status    string
	LastUsed  string
}

// APIKeyData holds a single API key for display
type APIKeyData struct {
	ID               string
	Name             string
	KeyPrefix        string
	Status           string
	KeyType          string
	GroupName        string
	ModelRoutingMode string
	CurrentModel     string
	SelectedModels   []string
	CreatedAt        time.Time
	MonthRequests    int
	MonthTokens      int64
	RateLimitRPM     int
	RateLimitTPM     int
	ExpiresAt        *time.Time
}

// EnterpriseKeyData holds an enterprise key for display (all *string dereferenced for template safety).
type EnterpriseKeyData struct {
	ID               string
	Name             string
	KeyPrefix        string
	Status           string
	AssignedUserName string
	AssignedUserID   string
	GroupName        string
	GroupID          string
	CreatedAt        time.Time
	MonthRequests    int
	MonthTokens      int64
	RateLimitRPM     int
	ModelRoutingMode string
	CurrentModel     string
	SelectedModels   []string
	AllowedModels    []string
	ExpiresAt        *time.Time
	Expired          bool
}

// PaginatedData is a generic wrapper for paginated results
type PaginatedData struct {
	Items                interface{}
	Total                int
	ActiveCount          int
	SuspendedCount       int
	EnterpriseAdminCount int
	Page                 int
	PageSize             int
	TotalPages           int
}

func newPaginatedData(items interface{}, total, page, pageSize int) PaginatedData {
	totalPages := (total + pageSize - 1) / pageSize
	if totalPages < 1 {
		totalPages = 1
	}
	return PaginatedData{Items: items, Total: total, Page: page, PageSize: pageSize, TotalPages: totalPages}
}

// GetAPIKeys fetches user's API keys with pagination
func (h *Handler) GetAPIKeys(ctx context.Context, userID string, page, pageSize int) (*PaginatedData, error) {
	data, err := query.GetAPIKeysData(ctx, h.db, userID, query.APIKeysOptions{
		Page:              page,
		PageSize:          pageSize,
		ExcludeEnterprise: true,
		IncludeStats:      true,
	})
	if err != nil {
		return nil, err
	}

	var keys []APIKeyData
	for _, k := range data.Items {
		rpm := k.RateLimitRPM
		if rpm <= 0 {
			rpm = routes.DefaultUserRPM(h.db)
		}
		tpm := k.RateLimitTPM
		if tpm <= 0 {
			tpm = routes.DefaultUserTPM(h.db)
		}
		keys = append(keys, APIKeyData{
			ID:               k.ID,
			Name:             k.Name,
			KeyPrefix:        k.KeyPrefix,
			Status:           k.Status,
			KeyType:          derefStr(k.KeyType),
			GroupName:        k.GroupName,
			ModelRoutingMode: derefStr(k.ModelRoutingMode),
			CurrentModel:     derefStr(k.CurrentModel),
			SelectedModels:   toStringSlice(k.SelectedModels),
			CreatedAt:        k.CreatedAt,
			MonthRequests:    k.MonthRequests,
			MonthTokens:      k.MonthTokens,
			RateLimitRPM:     rpm,
			RateLimitTPM:     tpm,
			ExpiresAt:        k.ExpiresAt,
		})
	}

	return &PaginatedData{
		Items: keys, Total: data.Total, Page: data.Page,
		PageSize: data.PageSize, TotalPages: data.TotalPages,
	}, nil
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func unmarshalStringSlice(s *string) []string {
	if s == nil || *s == "" {
		return nil
	}
	var result []string
	if err := json.Unmarshal([]byte(*s), &result); err == nil {
		return result
	}
	return nil
}

func toStringSlice(v interface{}) []string {
	if v == nil {
		return nil
	}
	if arr, ok := v.([]string); ok {
		return arr
	}
	if arr, ok := v.([]interface{}); ok {
		result := make([]string, 0, len(arr))
		for _, item := range arr {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
		return result
	}
	return nil
}

func (h *Handler) GetAssignedEnterpriseKeys(ctx context.Context, userID string, page, pageSize int) (*query.AssignedEnterpriseKeysData, error) {
	return query.GetAssignedEnterpriseKeysData(ctx, h.db, userID, query.AssignedEnterpriseKeysOptions{
		Page:     page,
		PageSize: pageSize,
	})
}

// GetBillingData fetches billing summary + usage records via the query layer.
func (h *Handler) GetBillingData(ctx context.Context, userID string, limit int) (*query.BillingData, error) {
	return query.GetBillingData(ctx, h.db, userID, limit)
}

// GetOrders fetches user's orders via the query layer.
func (h *Handler) GetOrders(ctx context.Context, userID string, page, pageSize int) (*PaginatedData, error) {
	data, err := query.GetOrdersData(ctx, h.db, userID, page, pageSize, query.OrderFilter{})
	if err != nil {
		return nil, err
	}
	return &PaginatedData{
		Items: data.Items, Total: data.Total, Page: data.Page,
		PageSize: data.PageSize, TotalPages: data.TotalPages,
	}, nil
}

// ProfileData holds user profile page data
// ProfileData is an alias for the unified query type.
type ProfileData = query.ProfileData

// GetProfileData fetches user profile info via the query layer.
func (h *Handler) GetProfileData(ctx context.Context, userID string) (*ProfileData, error) {
	return query.GetProfileData(ctx, h.db, userID)
}

// GroupInfo is an alias for the unified query type.
type GroupInfo = query.GroupInfo

// MyGroupData is an alias for the unified query type.
type MyGroupData = query.MyGroupData

// GetMyGroupData fetches all visible groups and user's current group via the query layer.
func (h *Handler) GetMyGroupData(ctx context.Context, userID string) (*MyGroupData, error) {
	return query.GetMyGroupData(ctx, h.db, userID)
}

// AdminDashboardData holds admin overview stats
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
	// Reconciliation cost diff (last 7d)
	ReconUpstreamCostUSD float64
	ReconBilledCostUSD   float64
	ReconCostDiffUSD     float64
	RecentUsers          []AdminUser
	RecentAuditLogs      []query.AdminAuditLog
}

// AdminUser holds a user record for admin display
type AdminUser struct {
	ID        string
	Email     string
	Name      string
	Role      string
	Balance   int64
	Status    string
	CreatedAt time.Time
	GroupID   *string
	GroupName string
}

// AdminModel holds a model record for admin
type AdminModel struct {
	ID                  string
	ModelID             string // openrouter_id or product_id
	Vendor              string // provider
	Name                string
	Status              string
	InputPrice          float64
	OutputPrice         float64
	InputPriceCacheHit  float64
	InputPriceCacheMiss float64
	ReasoningPrice      float64
	ContextWindow       int
	CreatedAt           time.Time
	Source              string     // "openrouter" or "service"
	EOLAnnouncedAt      *time.Time // EOL announced time
	EOLServiceAt        *time.Time // EOL service stop time
	EOLPurgeAt          *time.Time // EOL purge time
	EOLNotes            string     // EOL notes
	Hidden              bool       // Hidden from default view (for discontinued)
}

// AdminProvider holds a provider record
type AdminProvider struct {
	ID         string
	Name       string
	Type       string
	BaseURL    string
	Enabled    bool
	SortOrder  int
	Priority   int
	KeyCount   int
	ModelCount int
}

type AdminProvidersData struct {
	Providers      []AdminProvider
	EnabledCount   int
	DisabledCount  int
	ActiveKeyCount int
}

// AdminUpstreamKey holds an upstream key record
type AdminUpstreamKey struct {
	ID               string
	Provider         string
	Preview          string
	RpmLimit         *int
	TpmLimit         *int
	QuotaLimit       *int64
	Tier             string
	BaseURL          string
	Enabled          bool
	ValidationStatus string
	CreatedAt        time.Time
}

// AdminEnterprise holds enterprise data for admin page
// GetAdminEnterprises fetches paginated enterprise list via the routes layer.
func (h *Handler) GetAdminEnterprises(ctx context.Context, page, pageSize int, search string) (*query.AdminEnterpriseListData, error) {
	return query.GetAdminEnterprisesData(ctx, h.db, page, pageSize, search)
}

// GetAdminDashboardData fetches admin overview stats
// GetAdminDashboardData fetches admin overview stats via the unified routes layer.
func (h *Handler) GetAdminDashboardData(ctx context.Context) (*query.AdminDashboardData, error) {
	return query.GetAdminDashboardData(ctx, h.db)
}

// GetAdminUsers fetches all users for admin via the unified routes layer.
func (h *Handler) GetAdminUsers(ctx context.Context, page, pageSize int) (*PaginatedData, error) {
	data, err := query.GetAdminUsersData(ctx, h.db, page, pageSize)
	if err != nil {
		return nil, err
	}
	return &PaginatedData{
		Items:                data.Items,
		Total:                data.Total,
		ActiveCount:          data.ActiveCount,
		SuspendedCount:       data.SuspendedCount,
		EnterpriseAdminCount: data.EnterpriseAdminCount,
		Page:                 data.Page,
		PageSize:             data.PageSize,
		TotalPages:           data.TotalPages,
	}, nil
}

// GetAdminModels fetches models for admin page via the unified routes layer.
func (h *Handler) GetAdminModels(ctx context.Context, page, pageSize int, providerFilter string) (*PaginatedData, error) {
	data, err := query.GetAdminModelsData(ctx, h.db, page, pageSize, providerFilter)
	if err != nil {
		return nil, err
	}
	return &PaginatedData{
		Items:      data,
		Total:      data.Total,
		Page:       data.Page,
		PageSize:   data.PageSize,
		TotalPages: data.TotalPages,
	}, nil
}

// GetAdminProviders fetches all providers via the unified routes layer.
func (h *Handler) GetAdminProviders(ctx context.Context) (*query.AdminProvidersData, error) {
	return query.GetAdminProvidersData(ctx, h.db)
}

// GetAdminUpstreamKeys fetches all upstream keys with stats
func (h *Handler) GetAdminUpstreamKeys(ctx context.Context, page, pageSize int) (*PaginatedData, error) {
	data, err := query.GetAdminUpstreamKeysData(ctx, h.db, page, pageSize)
	if err != nil {
		return nil, err
	}
	return &PaginatedData{
		Items:      data,
		Total:      data.Total,
		Page:       data.Page,
		PageSize:   data.PageSize,
		TotalPages: data.TotalPages,
	}, nil
}

// GetAdminPlans fetches all subscription plans via the unified routes layer.
func (h *Handler) GetAdminPlans(ctx context.Context) (*query.AdminPlansData, error) {
	return query.GetAdminPlansData(ctx, h.db)
}

// GetAdminAuditLogs fetches audit logs with stats via the query layer.
// search matches user_email or action (substring); category and level are exact matches.
func (h *Handler) GetAdminAuditLogs(ctx context.Context, page, pageSize int, search, category, level string) (*PaginatedData, error) {
	data, err := query.GetAdminAuditLogsData(ctx, h.db, page, pageSize, search, category, level)
	if err != nil {
		return nil, err
	}
	fq := url.Values{}
	if search != "" {
		fq.Set("search", search)
	}
	if category != "" {
		fq.Set("category", category)
	}
	if level != "" {
		fq.Set("level", level)
	}
	filterQuery := ""
	if len(fq) > 0 {
		filterQuery = "&" + fq.Encode()
	}
	return &PaginatedData{
		Items: struct {
			Logs         []query.AdminAuditLog
			ErrorCount   int
			WarningCount int
			InfoCount    int
			Search       string
			Category     string
			Level        string
			FilterQuery  string
		}{data.Logs, data.ErrorCount, data.WarningCount, data.InfoCount, search, category, level, filterQuery},
		Total: data.Total, Page: data.Page, PageSize: data.PageSize, TotalPages: data.TotalPages,
	}, nil
}

// ExportAdminAuditLogs exports all audit logs as CSV (max 10000 rows).
func (h *Handler) ExportAdminAuditLogs(ctx context.Context) (string, error) {
	var b strings.Builder
	b.WriteString("\xEF\xBB\xBF") // UTF-8 BOM for Excel
	b.WriteString("ID,Time,User,Category,Action,Level,Message,IP\n")

	rows, err := h.db.Query(ctx,
		`SELECT id, COALESCE(user_email, ''), category, action, level, message, COALESCE(ip_address::text, ''), created_at
		 FROM audit_logs
		 WHERE created_at >= NOW() - INTERVAL '12 months'
		 ORDER BY created_at DESC LIMIT 10000`)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	for rows.Next() {
		var id int64
		var email, category, action, level, message, ip string
		var createdAt time.Time
		if err := rows.Scan(&id, &email, &category, &action, &level, &message, &ip, &createdAt); err != nil {
			continue
		}
		fmt.Fprintf(&b, "%d,%s,%s,%s,%s,%s,\"%s\",%s\n",
			id, createdAt.Format("2006-01-02 15:04:05"),
			csvEscape(email), csvEscape(category), csvEscape(action),
			csvEscape(level), csvEscape(message), csvEscape(ip))
	}
	return b.String(), nil
}

// csvEscape escapes a string for CSV output.
func csvEscape(s string) string {
	s = strings.ReplaceAll(s, "\"", "\"\"")
	// Neutralize CSV formula injection: Excel/Sheets interpret a leading
	// = + - @ as a formula. audit_logs.message/user_email can carry user
	// input, so force such cells to text by prepending a single quote.
	if strings.HasPrefix(s, "=") || strings.HasPrefix(s, "+") || strings.HasPrefix(s, "-") || strings.HasPrefix(s, "@") {
		s = "'" + s
	}
	if strings.Contains(s, ",") || strings.Contains(s, "\n") || strings.Contains(s, "\"") {
		s = "\"" + s + "\""
	}
	return s
}

// GetAdminSettings fetches system settings via the query layer.
func (h *Handler) GetAdminSettings(ctx context.Context) ([]query.AdminSetting, error) {
	return query.GetAdminSettingsData(ctx, h.db)
}

// GetInvoiceData fetches invoice profiles and requests
func (h *Handler) GetInvoiceData(ctx context.Context, userID string, page, pageSize int) (*PaginatedData, error) {
	data, err := query.GetInvoiceData(ctx, h.db, userID, page, pageSize)
	if err != nil {
		return nil, err
	}
	return &PaginatedData{
		Items: struct {
			Profiles []query.InvoiceProfile
			Requests []query.InvoiceRequest
		}{data.Profiles, data.Requests},
		Total: data.Total, Page: data.Page,
		PageSize: data.PageSize, TotalPages: data.TotalPages,
	}, nil
}

// --- Enterprise Admin Data Types ---

// EnterpriseDashboardData holds enterprise admin dashboard data

// GetEnterpriseDashboardData fetches enterprise admin dashboard data via the query layer.
func (h *Handler) GetEnterpriseDashboardData(ctx context.Context, userID, rangeKey string) (*query.EnterpriseDashboardData, error) {
	return query.GetEnterpriseDashboardData(ctx, h.db, userID, rangeKey)
}

func (h *Handler) GetEnterpriseUsers(ctx context.Context, userID string) ([]query.EnterpriseUser, error) {
	return query.GetEnterpriseUsersData(ctx, h.db, userID)
}

func (h *Handler) GetEnterpriseInvitations(ctx context.Context, userID string) ([]query.EnterpriseInvitation, error) {
	return query.GetEnterpriseInvitationsData(ctx, h.db, userID)
}

func (h *Handler) GetEnterpriseKeys(ctx context.Context, userID string) ([]query.EnterpriseKey, error) {
	return query.GetEnterpriseKeysData(ctx, h.db, userID)
}

func (h *Handler) GetEnterpriseGroups(ctx context.Context, enterpriseID string) ([]query.EnterpriseGroupItem, error) {
	return query.GetEnterpriseGroupsData(ctx, h.db, enterpriseID)
}

func (h *Handler) GetEnterpriseBillingData(ctx context.Context, userID string) (*query.EnterpriseBillingData, error) {
	return query.GetEnterpriseBillingData(ctx, h.db, userID)
}

// SuperAdminData is an alias for the unified query type.
type SuperAdminData = query.SuperAdminData

// GetSuperAdmins fetches all super admins via the query layer.
func (h *Handler) GetSuperAdmins(ctx context.Context) ([]SuperAdminData, error) {
	return query.GetSuperAdmins(ctx, h.db)
}

// PaymentConfig is an alias for the unified query type.
type PaymentConfig = query.PaymentConfig

// GetPaymentConfig fetches payment gateway configuration via the query layer.
func (h *Handler) GetPaymentConfig(ctx context.Context) (*PaymentConfig, error) {
	return query.GetPaymentConfig(ctx, h.db, h.cfg.Auth.EncryptionKey)
}

// --- Admin Groups Data ---

// GetAdminGroups fetches all user groups for admin management via the routes layer.
func (h *Handler) GetAdminGroups(ctx context.Context) (*query.AdminGroupsData, error) {
	return query.GetAdminGroupsData(ctx, h.db)
}

// --- Monitoring Data Types ---

// UpstreamKeyStatus is an alias for the unified query type.
type UpstreamKeyStatus = query.UpstreamKeyStatus

// DownstreamKeyStatus is an alias for the unified query type.
type DownstreamKeyStatus = query.DownstreamKeyStatus

// GetUpstreamKeyMonitorData fetches upstream key monitoring data via the query layer.
func (h *Handler) GetUpstreamKeyMonitorData(ctx context.Context, page, pageSize int) (*PaginatedData, error) {
	data, err := query.GetUpstreamKeyMonitorData(ctx, h.db, page, pageSize)
	if err != nil {
		return nil, err
	}
	return &PaginatedData{
		Items: struct {
			Keys        []query.UpstreamKeyStatus
			TotalKeys   int
			ActiveKeys  int
			FailedKeys  int
			PendingKeys int
			Providers   []string
		}{data.Keys, data.TotalKeys, data.ActiveKeys, data.FailedKeys, data.PendingKeys, data.Providers},
		Total: data.Total, Page: data.Page, PageSize: data.PageSize, TotalPages: data.TotalPages,
	}, nil
}

// GetDownstreamKeyMonitorData fetches downstream key monitoring data via the query layer.
func (h *Handler) GetDownstreamKeyMonitorData(ctx context.Context, period, search, status, keyType, sort string, page, pageSize int) (*PaginatedData, error) {
	data, err := query.GetDownstreamKeyMonitorData(ctx, h.db, period, search, status, keyType, sort, page, pageSize)
	if err != nil {
		return nil, err
	}
	return &PaginatedData{
		Items: struct {
			Keys         []query.DownstreamKeyStatus
			TotalKeys    int
			ActiveKeys   int
			DisabledKeys int
			RequestCount int64
			TokenCount   int64
			Period       string
			Search       string
			Status       string
			KeyType      string
			Sort         string
			GroupBy      string
		}{
			data.Keys, data.TotalKeys, data.ActiveKeys, data.DisabledKeys,
			data.RequestCount, data.TokenCount,
			period, search, status, keyType, sort, "key",
		},
		Total: data.Total, Page: data.Page, PageSize: data.PageSize, TotalPages: data.TotalPages,
	}, nil
}

// GetDownstreamUserMonitorData fetches user-grouped downstream monitoring data via the query layer.
func (h *Handler) GetDownstreamUserMonitorData(ctx context.Context, period, search, status, keyType, sort string, page, pageSize int) (*PaginatedData, error) {
	data, err := query.GetDownstreamUserMonitorData(ctx, h.db, period, search, status, keyType, sort, page, pageSize)
	if err != nil {
		return nil, err
	}
	return &PaginatedData{
		Items: struct {
			Users        []query.DownstreamUserStatus
			TotalKeys    int
			ActiveKeys   int
			DisabledKeys int
			RequestCount int64
			TokenCount   int64
			Period       string
			Search       string
			Status       string
			KeyType      string
			Sort         string
			GroupBy      string
		}{
			data.Users, data.TotalKeys, data.ActiveKeys, data.DisabledKeys,
			data.RequestCount, data.TokenCount,
			period, search, status, keyType, sort, "user",
		},
		Total: data.Total, Page: data.Page, PageSize: data.PageSize, TotalPages: data.TotalPages,
	}, nil
}

// GetDownstreamEnterpriseMonitorData fetches enterprise-grouped downstream monitoring data via the query layer.
func (h *Handler) GetDownstreamEnterpriseMonitorData(ctx context.Context, period, search, status, keyType, sort string, page, pageSize int) (*PaginatedData, error) {
	data, err := query.GetDownstreamEnterpriseMonitorData(ctx, h.db, period, search, status, keyType, sort, page, pageSize)
	if err != nil {
		return nil, err
	}
	return &PaginatedData{
		Items: struct {
			Enterprises  []query.DownstreamEnterpriseStatus
			TotalKeys    int
			ActiveKeys   int
			DisabledKeys int
			RequestCount int64
			TokenCount   int64
			Period       string
			Search       string
			Status       string
			KeyType      string
			Sort         string
			GroupBy      string
		}{
			data.Enterprises, data.TotalKeys, data.ActiveKeys, data.DisabledKeys,
			data.RequestCount, data.TokenCount,
			period, search, status, keyType, sort, "enterprise",
		},
		Total: data.Total, Page: data.Page, PageSize: data.PageSize, TotalPages: data.TotalPages,
	}, nil
}

// UpstreamUsageRecordItem is an alias for the unified query type.
type UpstreamUsageRecordItem = query.UpstreamUsageRecordItem

// AdminUpstreamUsageData is an alias for the unified query type.
type AdminUpstreamUsageData = query.AdminUpstreamUsageData

// GetAdminUpstreamUsageData fetches upstream usage data for admin page rendering via the query layer.
func (h *Handler) GetAdminUpstreamUsageData(ctx context.Context, page, pageSize int) (*AdminUpstreamUsageData, error) {
	return query.GetAdminUpstreamUsageData(ctx, h.db, page, pageSize)
}

// ProviderHealthItem is an alias for the unified query type.
type ProviderHealthItem = query.ProviderHealthItem

// AdminProviderHealthData is an alias for the unified query type.
type AdminProviderHealthData = query.AdminProviderHealthData

// GetAdminProviderHealthData fetches provider health data for admin page rendering via the query layer.
func (h *Handler) GetAdminProviderHealthData(ctx context.Context) (*AdminProviderHealthData, error) {
	return query.GetAdminProviderHealthData(ctx, h.db)
}

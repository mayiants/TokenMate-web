package query

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/ai-proxy/ai-portal-go/internal/db"
)

// AdminUserItem holds a user record for admin display.
type AdminUserItem struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Email   string `json:"email"`
	Role    string `json:"role"`
	Balance int64  `json:"balance"`
	// GiftBalance is the compensation/promo wallet (consumption is never
	// invoiceable) — displayed next to Balance in the admin user table.
	GiftBalance int64     `json:"gift_balance"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	GroupID     *string   `json:"group_id"`
	GroupName   string    `json:"group_name"`
	KeyCount    int       `json:"key_count"`
	// EnterpriseID/EnterpriseName describe the user's current enterprise
	// membership (empty when the user is standalone). Used by the admin UI to
	// show current enterprise and drive the force-reassign control.
	EnterpriseID   string `json:"enterprise_id"`
	EnterpriseName string `json:"enterprise_name"`
}

// AdminUsersData holds paginated user list.
type AdminUsersData struct {
	Items                []AdminUserItem `json:"users"`
	Total                int             `json:"total"`
	ActiveCount          int             `json:"active_count"`
	SuspendedCount       int             `json:"suspended_count"`
	EnterpriseAdminCount int             `json:"enterprise_admin_count"`
	Page                 int             `json:"page"`
	PageSize             int             `json:"page_size"`
	TotalPages           int             `json:"total_pages"`
}

// GetAdminUsersData fetches paginated users for admin.
// When pageSize <= 0 it returns all users (no pagination).
// Used by both the JSON API and the page renderer (web/data.go).
func GetAdminUsersData(ctx context.Context, database *db.DB, page, pageSize int) (*AdminUsersData, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 100
	}

	var total, activeCount, suspendedCount, enterpriseAdminCount int
	_ = database.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE deleted_at IS NULL`).Scan(&total)
	_ = database.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE deleted_at IS NULL AND status = 'active'`).Scan(&activeCount)
	_ = database.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE deleted_at IS NULL AND status = 'suspended'`).Scan(&suspendedCount)
	_ = database.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE deleted_at IS NULL AND role = 'enterprise_admin'`).Scan(&enterpriseAdminCount)

	query := `SELECT u.id, u.name, u.email, u.role, u.balance, u.gift_balance, u.status, u.created_at,
				u.group_id::text,
				COALESCE(g.name, '') as group_name,
				(SELECT COUNT(*) FROM api_keys WHERE user_id = u.id AND deleted_at IS NULL) as key_count,
				COALESCE(u.enterprise_id::text, '') as enterprise_id,
				COALESCE(e.name, '') as enterprise_name
				 FROM users u
				 LEFT JOIN user_groups g ON u.group_id = g.id
				 LEFT JOIN enterprises e ON u.enterprise_id = e.id AND e.deleted_at IS NULL
				 WHERE u.deleted_at IS NULL
				 ORDER BY u.created_at DESC`
	args := []interface{}{}
	if pageSize > 0 {
		query += " LIMIT $1 OFFSET $2"
		offset := (page - 1) * pageSize
		args = append(args, pageSize, offset)
	}

	rows, err := database.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	users := []AdminUserItem{}
	for rows.Next() {
		var u AdminUserItem
		var groupID *string
		_ = rows.Scan(&u.ID, &u.Name, &u.Email, &u.Role, &u.Balance, &u.GiftBalance, &u.Status, &u.CreatedAt,
			&groupID, &u.GroupName, &u.KeyCount, &u.EnterpriseID, &u.EnterpriseName)
		u.GroupID = groupID
		users = append(users, u)
	}

	totalPages := 1
	if pageSize > 0 {
		totalPages = (total + pageSize - 1) / pageSize
		if totalPages < 1 {
			totalPages = 1
		}
	}

	return &AdminUsersData{
		Items: users, Total: total, ActiveCount: activeCount, SuspendedCount: suspendedCount, EnterpriseAdminCount: enterpriseAdminCount, Page: page, PageSize: pageSize, TotalPages: totalPages,
	}, nil
}

// AdminProvider holds a provider record for admin display.
type AdminProvider struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Type        string  `json:"type"`
	BaseURL     string  `json:"base_url"`
	SyncSource  string  `json:"sync_source"`
	APIKey      *string `json:"api_key"`
	Description *string `json:"description"`
	Enabled     bool    `json:"enabled"`
	SortOrder   int     `json:"sort_order"`
	Priority    int     `json:"priority"`
	KeyCount    int     `json:"key_count"`
	ModelCount  int     `json:"model_count"`
}

// AdminProvidersData holds provider list with summary stats.
type AdminProvidersData struct {
	Providers      []AdminProvider `json:"providers"`
	EnabledCount   int             `json:"enabled_count"`
	DisabledCount  int             `json:"disabled_count"`
	ActiveKeyCount int             `json:"active_key_count"`
}

// GetAdminProvidersData fetches all providers for admin.
// Used by both the JSON API and the page renderer (web/data.go).
func GetAdminProvidersData(ctx context.Context, database *db.DB) (*AdminProvidersData, error) {
	d := &AdminProvidersData{Providers: []AdminProvider{}}

	rows, err := database.Query(ctx,
		`SELECT id, name, type, COALESCE(base_url, ''), COALESCE(sync_source, 'manual'), api_key, description, enabled, sort_order FROM providers ORDER BY sort_order, name`)
	if err != nil {
		logQueryError(ctx, "GetAdminProvidersData", err)
		return d, nil
	}
	defer rows.Close()

	for rows.Next() {
		var p AdminProvider
		if err := rows.Scan(&p.ID, &p.Name, &p.Type, &p.BaseURL, &p.SyncSource, &p.APIKey, &p.Description, &p.Enabled, &p.SortOrder); err == nil {
			p.Priority = p.SortOrder
			d.Providers = append(d.Providers, p)
			if p.Enabled {
				d.EnabledCount++
			} else {
				d.DisabledCount++
			}
		}
	}

	// Populate per-provider upstream key counts (and total active keys).
	keyCountRows, err := database.Query(ctx,
		`SELECT provider, COUNT(*), COUNT(*) FILTER (WHERE enabled)
		 FROM upstream_keys WHERE deleted_at IS NULL GROUP BY provider`)
	if err == nil {
		defer keyCountRows.Close()
		keyCounts := map[string]int{}
		for keyCountRows.Next() {
			var provider string
			var total, active int
			if err := keyCountRows.Scan(&provider, &total, &active); err == nil {
				keyCounts[provider] = total
				d.ActiveKeyCount += active
			}
		}
		for i := range d.Providers {
			d.Providers[i].KeyCount = keyCounts[d.Providers[i].ID]
		}
	}

	// Populate per-provider model counts.
	modelCountRows, err := database.Query(ctx,
		`SELECT provider, COUNT(*) FROM service_models WHERE deleted_at IS NULL GROUP BY provider`)
	if err == nil {
		defer modelCountRows.Close()
		modelCounts := map[string]int{}
		for modelCountRows.Next() {
			var provider string
			var total int
			if err := modelCountRows.Scan(&provider, &total); err == nil {
				modelCounts[provider] = total
			}
		}
		for i := range d.Providers {
			d.Providers[i].ModelCount = modelCounts[d.Providers[i].ID]
		}
	}

	return d, nil
}

// AdminModelItem holds a model record for admin display.
type AdminModelItem struct {
	ID                  string
	ModelID             string
	Vendor              string
	Name                string
	Status              string
	InputPrice          float64
	OutputPrice         float64
	InputPriceCacheHit  float64
	InputPriceCacheMiss float64
	ReasoningPrice      float64
	ContextWindow       int
	MaxOutputTokens     int
	Capabilities        map[string]interface{}
	CreatedAt           time.Time
	Source              string
	EOLAnnouncedAt      *time.Time
	EOLServiceAt        *time.Time
	EOLPurgeAt          *time.Time
	EOLNotes            string
	Hidden              bool
}

// AdminModelProvider holds a lightweight provider record for model filters.
type AdminModelProvider struct {
	ID   string
	Name string
}

// AdminModelsData holds paginated models with stats.
type AdminModelsData struct {
	Models            []AdminModelItem
	Providers         []AdminModelProvider
	ServiceCount      int
	ActiveCount       int
	PendingCount      int
	PausedCount       int
	EOLPendingCount   int
	EOLCount          int
	DiscontinuedCount int
	VendorCount       int
	SyncedCount       int
	Total             int
	Page              int
	PageSize          int
	TotalPages        int
}

// GetAdminModelsData fetches paginated service models with stats for admin.
// Used by both the JSON API and the page renderer (web/data.go).
func GetAdminModelsData(ctx context.Context, database *db.DB, page, pageSize int, providerFilter string) (*AdminModelsData, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 50 {
		pageSize = 50
	}

	providers := []AdminModelProvider{}
	providerRows, err := database.Query(ctx,
		`SELECT id, name FROM providers WHERE enabled = true AND id NOT LIKE 'amazon%' ORDER BY name`)
	if err == nil {
		defer providerRows.Close()
		for providerRows.Next() {
			var p AdminModelProvider
			if err := providerRows.Scan(&p.ID, &p.Name); err == nil {
				providers = append(providers, p)
			}
		}
	}

	var total, activeCount, pendingCount, pausedCount int
	var eolPendingCount, eolCount, discontinuedCount, vendorCount, syncedCount int
	providerWhere := ""
	switch providerFilter {
	case "amazon":
		providerWhere = " AND provider LIKE 'amazon%'"
	case "!amazon":
		providerWhere = " AND provider NOT LIKE 'amazon%'"
	}

	_ = database.QueryRow(ctx, `SELECT COUNT(*) FROM service_models WHERE true`+providerWhere).Scan(&total)
	_ = database.QueryRow(ctx, `SELECT COUNT(*) FROM service_models WHERE status = 'active'`+strings.Replace(providerWhere, "WHERE", "AND", 1)).Scan(&activeCount)
	_ = database.QueryRow(ctx, `SELECT COUNT(*) FROM service_models WHERE status = 'pending'`+strings.Replace(providerWhere, "WHERE", "AND", 1)).Scan(&pendingCount)
	_ = database.QueryRow(ctx, `SELECT COUNT(*) FROM service_models WHERE status = 'paused'`+strings.Replace(providerWhere, "WHERE", "AND", 1)).Scan(&pausedCount)
	_ = database.QueryRow(ctx, `SELECT COUNT(*) FROM service_models WHERE status = 'eol_pending'`+strings.Replace(providerWhere, "WHERE", "AND", 1)).Scan(&eolPendingCount)
	_ = database.QueryRow(ctx, `SELECT COUNT(*) FROM service_models WHERE status = 'eol'`+strings.Replace(providerWhere, "WHERE", "AND", 1)).Scan(&eolCount)
	_ = database.QueryRow(ctx, `SELECT COUNT(*) FROM service_models WHERE status = 'discontinued'`+strings.Replace(providerWhere, "WHERE", "AND", 1)).Scan(&discontinuedCount)
	_ = database.QueryRow(ctx, `SELECT COUNT(DISTINCT provider) FROM service_models WHERE true`+providerWhere).Scan(&vendorCount)
	_ = database.QueryRow(ctx, `SELECT COUNT(*) FROM openrouter_models`).Scan(&syncedCount)

	offset := (page - 1) * pageSize
	modelQuery := `SELECT id::text, product_id, provider, COALESCE(api_model_name, product_id),
			 COALESCE(input_price_cache_miss, input_price, 0), COALESCE(output_price, 0),
			 COALESCE(input_price_cache_hit, 0), COALESCE(reasoning_price, 0),
			 COALESCE(context_window, 0), COALESCE(max_output_tokens, 0),
			 COALESCE(capabilities, '{}'),
			 status, created_at, eol_announced_at, eol_service_at, eol_purge_at, eol_notes,
			 COALESCE(hidden, false)
			 FROM service_models WHERE deleted_at IS NULL` + providerWhere + `
			 ORDER BY provider, product_id LIMIT $1 OFFSET $2`

	models := []AdminModelItem{}
	serviceRows, err := database.Query(ctx, modelQuery, pageSize, offset)
	if err != nil {
		return nil, err
	}
	defer serviceRows.Close()
	for serviceRows.Next() {
		var m AdminModelItem
		var eolNotes *string
		var capsRaw []byte
		if err := serviceRows.Scan(&m.ID, &m.ModelID, &m.Vendor, &m.Name,
			&m.InputPriceCacheMiss, &m.OutputPrice,
			&m.InputPriceCacheHit, &m.ReasoningPrice,
			&m.ContextWindow, &m.MaxOutputTokens, &capsRaw,
			&m.Status, &m.CreatedAt,
			&m.EOLAnnouncedAt, &m.EOLServiceAt, &m.EOLPurgeAt, &eolNotes,
			&m.Hidden); err == nil {
			m.Source = "service"
			m.InputPrice = m.InputPriceCacheMiss
			if eolNotes != nil {
				m.EOLNotes = *eolNotes
			}
			if len(capsRaw) > 0 && string(capsRaw) != "{}" && string(capsRaw) != "null" {
				_ = json.Unmarshal(capsRaw, &m.Capabilities)
			}
			models = append(models, m)
		}
	}

	totalPages := (total + pageSize - 1) / pageSize
	if totalPages < 1 {
		totalPages = 1
	}

	return &AdminModelsData{
		Models: models, Providers: providers,
		ServiceCount: total, ActiveCount: activeCount, PendingCount: pendingCount, PausedCount: pausedCount,
		EOLPendingCount: eolPendingCount, EOLCount: eolCount, DiscontinuedCount: discontinuedCount,
		VendorCount: vendorCount, SyncedCount: syncedCount,
		Total: total, Page: page, PageSize: pageSize, TotalPages: totalPages,
	}, nil
}

// AdminUpstreamKeyItem holds an upstream key record for admin display.
type AdminUpstreamKeyItem struct {
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
	// ModelLimits is the raw JSONB text (empty when NULL), passed through to
	// the template so the per-model rate-limit override is visible/editable.
	ModelLimits string
}

// AdminUpstreamKeysData holds paginated upstream keys with stats.
type AdminUpstreamKeysData struct {
	Providers     []AdminModelProvider
	Keys          []AdminUpstreamKeyItem
	ActiveCount   int
	DisabledCount int
	ValidCount    int
	Total         int
	Page          int
	PageSize      int
	TotalPages    int
	// ModelLimitsByKey maps upstream key ID -> per-model rate-limit override,
	// rendered into the page as a JS lookup for the edit drawer.
	ModelLimitsByKey map[string]map[string]db.ModelLimit
}

// GetAdminUpstreamKeysData fetches paginated upstream keys with stats.
// Used by both the JSON API and the page renderer (web/data.go).
func GetAdminUpstreamKeysData(ctx context.Context, database *db.DB, page, pageSize int) (*AdminUpstreamKeysData, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 50 {
		pageSize = 50
	}

	providers := []AdminModelProvider{}
	providerRows, err := database.Query(ctx,
		`SELECT id, name, type FROM providers WHERE enabled = true ORDER BY name`)
	if err == nil {
		defer providerRows.Close()
		for providerRows.Next() {
			var p AdminModelProvider
			if err := providerRows.Scan(&p.ID, &p.Name, new(string)); err == nil {
				providers = append(providers, p)
			}
		}
	}

	var total, activeCount, disabledCount, validCount int
	_ = database.QueryRow(ctx, `SELECT COUNT(*) FROM upstream_keys WHERE deleted_at IS NULL`).Scan(&total)
	_ = database.QueryRow(ctx, `SELECT COUNT(*) FROM upstream_keys WHERE deleted_at IS NULL AND enabled = true`).Scan(&activeCount)
	_ = database.QueryRow(ctx, `SELECT COUNT(*) FROM upstream_keys WHERE deleted_at IS NULL AND enabled = false`).Scan(&disabledCount)
	_ = database.QueryRow(ctx, `SELECT COUNT(*) FROM upstream_keys WHERE deleted_at IS NULL AND COALESCE(validation_status, 'pending') = 'valid'`).Scan(&validCount)

	offset := (page - 1) * pageSize
	rows, err := database.Query(ctx,
		`SELECT id::text, provider, key_preview, rpm_limit, tpm_limit, quota_limit,
		        COALESCE(tier, 'default'), COALESCE(base_url, ''), enabled, COALESCE(validation_status, 'pending'), created_at,
		        COALESCE(model_limits::text, '')
		 FROM upstream_keys WHERE deleted_at IS NULL
		 ORDER BY created_at DESC LIMIT $1 OFFSET $2`,
		pageSize, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	keys := []AdminUpstreamKeyItem{}
	limitsByKey := map[string]map[string]db.ModelLimit{}
	for rows.Next() {
		var k AdminUpstreamKeyItem
		if err := rows.Scan(&k.ID, &k.Provider, &k.Preview, &k.RpmLimit, &k.TpmLimit, &k.QuotaLimit,
			&k.Tier, &k.BaseURL, &k.Enabled, &k.ValidationStatus, &k.CreatedAt, &k.ModelLimits); err == nil {
			keys = append(keys, k)
			if k.ModelLimits != "" {
				var ml map[string]db.ModelLimit
				if json.Unmarshal([]byte(k.ModelLimits), &ml) == nil && len(ml) > 0 {
					limitsByKey[k.ID] = ml
				}
			}
		}
	}

	totalPages := (total + pageSize - 1) / pageSize
	if totalPages < 1 {
		totalPages = 1
	}

	return &AdminUpstreamKeysData{
		Providers: providers, Keys: keys,
		ActiveCount: activeCount, DisabledCount: disabledCount, ValidCount: validCount,
		Total: total, Page: page, PageSize: pageSize, TotalPages: totalPages,
		ModelLimitsByKey: limitsByKey,
	}, nil
}

// AdminAuditLog holds an audit log entry.
type AdminAuditLog struct {
	ID        int64
	Email     string
	Category  string
	Action    string
	Level     string
	Message   string
	CreatedAt time.Time
}

// AuditLogsData holds paginated audit logs with stats.
type AuditLogsData struct {
	Logs         []AdminAuditLog
	Total        int
	Page         int
	PageSize     int
	TotalPages   int
	ErrorCount   int
	WarningCount int
	InfoCount    int
}

// GetAdminAuditLogsData fetches paginated audit logs with stats.
// search matches user_email or action (substring); category and level are exact matches.
func GetAdminAuditLogsData(ctx context.Context, database *db.DB, page, pageSize int, search, category, level string) (*AuditLogsData, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 50 {
		pageSize = 50
	}

	d := &AuditLogsData{Logs: []AdminAuditLog{}}

	hotWindow := `created_at >= NOW() - INTERVAL '12 months'`
	where := "WHERE " + hotWindow
	args := []interface{}{}
	argIdx := 1
	if search != "" {
		where += " AND (user_email ILIKE $" + strconv.Itoa(argIdx) + " OR action ILIKE $" + strconv.Itoa(argIdx) + ")"
		args = append(args, "%"+search+"%")
		argIdx++
	}
	if category != "" {
		where += " AND category = $" + strconv.Itoa(argIdx)
		args = append(args, category)
		argIdx++
	}
	if level != "" {
		where += " AND level = $" + strconv.Itoa(argIdx)
		args = append(args, level)
		argIdx++
	}

	_ = database.QueryRow(ctx, `SELECT COUNT(*) FROM audit_logs `+where, args...).Scan(&d.Total)
	_ = database.QueryRow(ctx, `SELECT COUNT(*) FROM audit_logs WHERE level = 'error' AND `+hotWindow).Scan(&d.ErrorCount)
	_ = database.QueryRow(ctx, `SELECT COUNT(*) FROM audit_logs WHERE level = 'warning' AND `+hotWindow).Scan(&d.WarningCount)
	_ = database.QueryRow(ctx, `SELECT COUNT(*) FROM audit_logs WHERE level = 'info' AND `+hotWindow).Scan(&d.InfoCount)

	offset := (page - 1) * pageSize
	queryArgs := append(args, pageSize, offset)
	rows, err := database.Query(ctx,
		`SELECT id, COALESCE(user_email, ''), category, action, level, message, created_at
		 FROM audit_logs `+where+` ORDER BY created_at DESC LIMIT $`+strconv.Itoa(argIdx)+` OFFSET $`+strconv.Itoa(argIdx+1),
		queryArgs...,
	)
	if err != nil {
		logQueryError(ctx, "GetAdminAuditLogsData", err)
		return d, nil
	}
	defer rows.Close()

	for rows.Next() {
		var l AdminAuditLog
		if err := rows.Scan(&l.ID, &l.Email, &l.Category, &l.Action, &l.Level, &l.Message, &l.CreatedAt); err == nil {
			d.Logs = append(d.Logs, l)
		}
	}

	d.Page = page
	d.PageSize = pageSize
	totalPages := (d.Total + pageSize - 1) / pageSize
	if totalPages < 1 {
		totalPages = 1
	}
	d.TotalPages = totalPages
	return d, nil
}

// ── Settings ──────────────────────────────────────────────────────────────────

// AdminSetting holds a system setting.
type AdminSetting struct {
	Key         string
	Value       string
	Category    string
	Description string
}

// GetAdminSettingsData fetches all system settings.
func GetAdminSettingsData(ctx context.Context, database *db.DB) ([]AdminSetting, error) {
	rows, err := database.Query(ctx,
		`SELECT key, value, COALESCE(category, 'general'), COALESCE(description, '')
		 FROM system_settings ORDER BY category, key`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var settings []AdminSetting
	for rows.Next() {
		var s AdminSetting
		if err := rows.Scan(&s.Key, &s.Value, &s.Category, &s.Description); err == nil {
			settings = append(settings, s)
		}
	}
	return settings, nil
}

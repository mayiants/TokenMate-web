package query

import (
	"context"
	"time"

	"github.com/mayiants/ai-portal-go/internal/db"
)

// AdminPlan holds a subscription plan for admin display.
type AdminPlan struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	Description     *string    `json:"description"`
	Price           int64      `json:"price"`
	Quota           int64      `json:"quota"`
	DailyLimit      int64      `json:"daily_limit"`
	PlanType        string     `json:"plan_type"`
	DurationDays    int        `json:"duration_days"`
	Status          string     `json:"status"`
	DeletedAt       *time.Time `json:"deleted_at"`
	CreatedAt       time.Time  `json:"created_at"`
	RPMLimit        int        `json:"rpm_limit"`
	RPHLimit        int        `json:"rph_limit"`
	RP5HLimit       int        `json:"rp5h_limit"`
	RPDLimit        int        `json:"rpd_limit"`
	RPWLimit        int        `json:"rpw_limit"`
	TPMLimit        int        `json:"tpm_limit"`
	TPHLimit        int        `json:"tph_limit"`
	TPDLimit        int64      `json:"tpd_limit"`
	TPWLimit        int64      `json:"tpw_limit"`
	ConcurrentLimit int        `json:"concurrent_limit"`
	// ModelPermissions is the plan's model allowlist (jsonb array of model
	// names); nil/empty = all models allowed.
	ModelPermissions interface{} `json:"model_permissions"`
}

// AdminPlansData holds plan list with summary stats.
type AdminPlansData struct {
	Plans       []AdminPlan `json:"plans"`
	ActiveCount int         `json:"active_count"`
}

// GetAdminPlansData fetches all subscription plans for admin.
// Used by both the JSON API and the page renderer (web/data.go).
func GetAdminPlansData(ctx context.Context, database *db.DB) (*AdminPlansData, error) {
	d := &AdminPlansData{Plans: []AdminPlan{}}

	rows, err := database.Query(ctx,
		`SELECT sp.id, sp.name, sp.description, sp.price, sp.quota, COALESCE(sp.daily_limit, 0),
		        COALESCE(sp.plan_type, 'token'), COALESCE(sp.duration_days, 30),
		        sp.status, sp.created_at,
		        COALESCE(sp.rpm_limit, 0), COALESCE(sp.rph_limit, 0), COALESCE(sp.rp5h_limit, 0),
		        COALESCE(sp.rpd_limit, 0), COALESCE(sp.rpw_limit, 0),
		        COALESCE(sp.tpm_limit, 0), COALESCE(sp.tph_limit, 0), COALESCE(sp.tpd_limit, 0),
		        COALESCE(sp.tpw_limit, 0), COALESCE(sp.concurrent_limit, 0),
		        sp.model_permissions
		 FROM subscription_plans sp
		 WHERE sp.deleted_at IS NULL
		 ORDER BY sp.sort_order, sp.created_at`)
	if err != nil {
		logQueryError(ctx, "GetAdminPlansData", err)
		return d, nil
	}
	defer rows.Close()

	for rows.Next() {
		var p AdminPlan
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.Price, &p.Quota, &p.DailyLimit,
			&p.PlanType, &p.DurationDays,
			&p.Status, &p.CreatedAt,
			&p.RPMLimit, &p.RPHLimit, &p.RP5HLimit, &p.RPDLimit, &p.RPWLimit,
			&p.TPMLimit, &p.TPHLimit, &p.TPDLimit, &p.TPWLimit, &p.ConcurrentLimit,
			&p.ModelPermissions); err != nil {
			continue
		}
		d.Plans = append(d.Plans, p)
		if p.Status == "active" {
			d.ActiveCount++
		}
	}
	return d, nil
}

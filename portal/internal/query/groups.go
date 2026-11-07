package query

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/mayiants/ai-portal-go/internal/db"
)

// AdminGroup holds a user group for admin display.
type AdminGroup struct {
	ID             string
	Name           string
	Description    *string
	GroupType      string
	BillingMode    string
	PricingRatio   float64
	RoutingTrack   string
	Models         []string
	ModelCount     int
	MemberCount    int
	KeyCount       int
	Status         string
	IsDefault      bool
	CanDelete      bool
	EnterpriseID   *string
	EnterpriseName *string
	ParentName     string
	SyncMode       string
	MissingModels  int
	StaleModels    int
	RPM            int
	TPM            int64
	Inherited      bool
}

// AdminGroupsData holds groups split by platform vs enterprise
type AdminGroupsData struct {
	SystemGroups     []AdminGroup
	EnterpriseGroups []AdminGroup
	ActiveCount      int
}

// GetAdminGroupsData fetches all user groups for admin, split by platform vs enterprise.
// This is used by both the JSON API and the page renderer.
func GetAdminGroupsData(ctx context.Context, db *db.DB) (*AdminGroupsData, error) {
	rows, err := db.Query(ctx,
		`SELECT g.id::text, g.name, g.description, g.group_type, g.billing_mode,
		        g.pricing_ratio, g.routing_track, g.models, g.status, g.is_default,
		        COALESCE(g.rpm, 0), COALESCE(g.tpm, 0), g.inherit_to_enterprises,
		        COALESCE(jsonb_array_length(g.models), 0) as model_count,
		        (SELECT COUNT(*) FROM users WHERE group_id = g.id AND deleted_at IS NULL) as member_count,
		        (SELECT COUNT(*) FROM api_keys WHERE group_id = g.id AND deleted_at IS NULL) as key_count,
		        g.enterprise_id::text, COALESCE(e.name, ''),
		        COALESCE(pg.name, ''), g.sync_mode,
		        (SELECT COUNT(*) FROM jsonb_array_elements_text(COALESCE(pg.models, '[]'::jsonb)) pm(name)
		         WHERE pm.name NOT IN (SELECT jsonb_array_elements_text(COALESCE(g.models, '[]'::jsonb)))) AS missing_models,
		        (SELECT COUNT(*) FROM jsonb_array_elements_text(COALESCE(g.models, '[]'::jsonb)) cm(name)
		         WHERE cm.name NOT IN (
		           SELECT DISTINCT COALESCE(api_model_name, product_id) FROM service_models
		           WHERE status IN ('active', 'eol_pending', 'eol') AND deleted_at IS NULL)) AS stale_models
		 FROM user_groups g
		 LEFT JOIN enterprises e ON g.enterprise_id = e.id
		 LEFT JOIN user_groups pg ON pg.id = g.parent_group_id
		 WHERE g.deleted_at IS NULL
		 ORDER BY g.enterprise_id NULLS FIRST, g.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	data := &AdminGroupsData{}
	for rows.Next() {
		var g AdminGroup
		var modelsRaw []byte
		var description *string
		if err := rows.Scan(&g.ID, &g.Name, &description, &g.GroupType, &g.BillingMode,
			&g.PricingRatio, &g.RoutingTrack, &modelsRaw, &g.Status, &g.IsDefault, &g.RPM, &g.TPM, &g.Inherited,
			&g.ModelCount, &g.MemberCount, &g.KeyCount,
			&g.EnterpriseID, &g.EnterpriseName, &g.ParentName, &g.SyncMode, &g.MissingModels, &g.StaleModels); err != nil {
			continue
		}
		g.Description = description
		g.CanDelete = !g.IsDefault && g.MemberCount == 0 && g.KeyCount == 0
		if len(modelsRaw) > 0 {
			var modelList []string
			if err := json.Unmarshal(modelsRaw, &modelList); err == nil {
				g.Models = modelList
			}
		}
		if g.Status == "active" {
			data.ActiveCount++
		}
		if g.EnterpriseID != nil && *g.EnterpriseID != "" {
			data.EnterpriseGroups = append(data.EnterpriseGroups, g)
		} else {
			data.SystemGroups = append(data.SystemGroups, g)
		}
	}
	return data, nil
}

// GroupInfo holds info for a single group.
type GroupInfo struct {
	ID              string
	Name            string
	Description     string
	GroupType       string
	BillingMode     string
	PricingRatio    float64
	Status          string
	IsDefault       bool
	Models          []string
	TotalModels     int
	ConcurrentLimit int
	RPM             int
	RPH             int
	RP5H            int
	RPW             int
	RPMonth         int
	TPM             int
	TPH             int
	TPD             int
	TPW             int
	TPMonth         int
	Inherited       bool
}

// MyGroupData holds user group info for the my-group page.
type MyGroupData struct {
	UserGroupID      string
	UserGroupName    string
	EnterpriseID     string
	EnterpriseName   string
	Groups           []GroupInfo
	EnterpriseGroups []GroupInfo
	HasEnterprise    bool
	// ShowEnterpriseTab renders the enterprise tab for enterprise members AND
	// for system admins (oversight of inherited platform groups).
	ShowEnterpriseTab bool
}

// GetMyGroupData fetches all visible groups and user's current group.
func GetMyGroupData(ctx context.Context, database *db.DB, userID string) (*MyGroupData, error) {
	data := &MyGroupData{Groups: []GroupInfo{}, EnterpriseGroups: []GroupInfo{}}

	var userGroupID, userGroupName, userGroupType, userRole sql.NullString
	var enterpriseID, entID, entName sql.NullString
	err := database.QueryRow(ctx,
		`SELECT u.group_id::text, g.name, g.group_type, u.enterprise_id::text, e.id::text, e.name, u.role
		 FROM users u
		 LEFT JOIN user_groups g ON u.group_id = g.id
		 LEFT JOIN enterprises e ON u.enterprise_id = e.id AND e.deleted_at IS NULL
		 WHERE u.id = $1::uuid`,
		userID,
	).Scan(&userGroupID, &userGroupName, &userGroupType, &enterpriseID, &entID, &entName, &userRole)
	if err == nil {
		// e.id/e.name are NULL for users without an enterprise — scan into
		// NullString or the error would skip the role check below.
		if entID.Valid {
			data.EnterpriseID = entID.String
		}
		if entName.Valid {
			data.EnterpriseName = entName.String
		}
		if userGroupID.Valid {
			data.UserGroupID = userGroupID.String
		}
		if enterpriseID.Valid && enterpriseID.String != "" {
			data.HasEnterprise = true
		}
		// Admins get the enterprise tab for oversight: without an enterprise of
		// their own they still see platform-inherited groups there.
		if userRole.Valid && (userRole.String == "super_admin" || userRole.String == "system_admin") {
			data.ShowEnterpriseTab = true
		}
	}
	if data.HasEnterprise {
		data.ShowEnterpriseTab = true
	}

	pgRows, err := database.Query(ctx,
		`SELECT g.id::text, g.name, g.description, g.group_type, g.billing_mode,
		        g.pricing_ratio, g.status, g.is_default, g.models,
		        COALESCE(g.rpm, 0), COALESCE(g.rph, 0), COALESCE(g.rp5h, 0),
		        COALESCE(g.rpw, 0), COALESCE(g.rpmonth, 0),
		        COALESCE(g.tpm, 0), COALESCE(g.tph, 0), COALESCE(g.tpd, 0),
		        COALESCE(g.tpw, 0), COALESCE(g.tpmonth, 0),
		        (SELECT COUNT(*) FROM service_models WHERE status IN ('active','eol_pending','eol') AND COALESCE(hidden, false) = false)
		 FROM user_groups g
		 WHERE g.deleted_at IS NULL AND g.status = 'active' AND (g.enterprise_id IS NULL AND g.group_type != 'enterprise')
		 ORDER BY g.is_default DESC, g.group_type, g.name`)
	if err != nil {
		return data, err
	}
	defer pgRows.Close()

	for pgRows.Next() {
		var g GroupInfo
		var models []byte
		var description *string
		if err := pgRows.Scan(&g.ID, &g.Name, &description, &g.GroupType, &g.BillingMode,
			&g.PricingRatio, &g.Status, &g.IsDefault, &models,
			&g.RPM, &g.RPH, &g.RP5H, &g.RPW, &g.RPMonth,
			&g.TPM, &g.TPH, &g.TPD, &g.TPW, &g.TPMonth,
			&g.TotalModels); err != nil {
			continue
		}
		if description != nil {
			g.Description = *description
		}
		if len(models) > 0 {
			_ = json.Unmarshal(models, &g.Models)
		}
		data.Groups = append(data.Groups, g)
	}

	if data.ShowEnterpriseTab {
		egRows, err := database.Query(ctx,
			`SELECT g.id::text, g.name, g.description, g.group_type, g.billing_mode,
			        g.pricing_ratio, g.status, g.is_default, g.models,
			        COALESCE(g.rpm, 0), COALESCE(g.rph, 0), COALESCE(g.rp5h, 0),
			        COALESCE(g.rpw, 0), COALESCE(g.rpmonth, 0),
			        COALESCE(g.tpm, 0), COALESCE(g.tph, 0), COALESCE(g.tpd, 0),
			        COALESCE(g.tpw, 0), COALESCE(g.tpmonth, 0),
			        (SELECT COUNT(*) FROM service_models WHERE status IN ('active','eol_pending','eol') AND COALESCE(hidden, false) = false)
			 , FALSE AS inherited
			 FROM user_groups g
			 WHERE g.deleted_at IS NULL AND g.enterprise_id = $1::uuid AND g.status = 'active' AND g.visibility = 'public'
			 UNION ALL
			 -- Platform template groups inherited by all enterprises.
			 SELECT g.id::text, g.name, g.description, g.group_type, g.billing_mode,
			        g.pricing_ratio, g.status, g.is_default, g.models,
			        COALESCE(g.rpm, 0), COALESCE(g.rph, 0), COALESCE(g.rp5h, 0),
			        COALESCE(g.rpw, 0), COALESCE(g.rpmonth, 0),
			        COALESCE(g.tpm, 0), COALESCE(g.tph, 0), COALESCE(g.tpd, 0),
			        COALESCE(g.tpw, 0), COALESCE(g.tpmonth, 0),
			        (SELECT COUNT(*) FROM service_models WHERE status IN ('active','eol_pending','eol') AND COALESCE(hidden, false) = false),
			        TRUE AS inherited
			 FROM user_groups g
			 WHERE g.deleted_at IS NULL AND g.status = 'active' AND g.enterprise_id IS NULL
			   AND g.group_type = 'enterprise' AND g.inherit_to_enterprises
			 ORDER BY name`,
			enterpriseID)
		if err == nil {
			defer egRows.Close()
			for egRows.Next() {
				var g GroupInfo
				var models []byte
				var description *string
				if err := egRows.Scan(&g.ID, &g.Name, &description, &g.GroupType, &g.BillingMode,
					&g.PricingRatio, &g.Status, &g.IsDefault, &models,
					&g.RPM, &g.RPH, &g.RP5H, &g.RPW, &g.RPMonth,
					&g.TPM, &g.TPH, &g.TPD, &g.TPW, &g.TPMonth,
					&g.TotalModels, &g.Inherited); err != nil {
					continue
				}
				if description != nil {
					g.Description = *description
				}
				if len(models) > 0 {
					_ = json.Unmarshal(models, &g.Models)
				}
				data.EnterpriseGroups = append(data.EnterpriseGroups, g)
			}
		}
	}

	return data, nil
}

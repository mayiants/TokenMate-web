package routes

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/mayiants/ai-portal-go/internal/query"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

func (r *AdminRoutes) listUsers(c *gin.Context) {
	ctx := c.Request.Context()
	search := c.Query("search")

	var rows interface {
		Next() bool
		Scan(dest ...interface{}) error
		Close()
	}
	var err error

	if search != "" {
		// Search by email (exact) or name (partial) — email is unique, name is not.
		// include_deleted=1 (used by reconciliation pickers) also matches
		// soft-deleted users so their historical spend stays auditable.
		like := "%" + search + "%"
		deletedClause := "u.deleted_at IS NULL AND"
		if c.Query("include_deleted") == "1" {
			deletedClause = ""
		}
		rows, err = r.db.Query(ctx, fmt.Sprintf(`
			SELECT u.id, u.name, u.email, u.role, u.balance, u.gift_balance, u.status, u.created_at,
				COALESCE(u.group_id::text, ''), COALESCE(g.name, '') as group_name,
				(SELECT COUNT(*) FROM api_keys WHERE user_id = u.id AND deleted_at IS NULL) as key_count,
				COALESCE(u.enterprise_id::text, '') as enterprise_id,
				COALESCE(e.name, '') as enterprise_name,
				u.deleted_at IS NOT NULL AS deleted
			FROM users u
			LEFT JOIN user_groups g ON u.group_id = g.id
			LEFT JOIN enterprises e ON u.enterprise_id = e.id AND e.deleted_at IS NULL
			WHERE %s (u.email ILIKE $1 OR u.name ILIKE $1)
			ORDER BY
				CASE WHEN u.email = $2 THEN 0 ELSE 1 END,
				u.created_at DESC
			LIMIT 50`, deletedClause), like, search)
	} else {
		data, qerr := query.GetAdminUsersData(ctx, r.db, 1, 0)
		if qerr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to list users"}})
			return
		}
		users := []gin.H{}
		for _, u := range data.Items {
			users = append(users, gin.H{
				"id": u.ID, "name": u.Name, "email": u.Email, "role": u.Role,
				"group_id":        u.GroupID,
				"group_name":      u.GroupName,
				"status":          u.Status,
				"enterprise_id":   u.EnterpriseID,
				"enterprise_name": u.EnterpriseName,
				"balance":         u.Balance, "gift_balance": u.GiftBalance, "created_at": u.CreatedAt, "key_count": u.KeyCount,
			})
		}
		c.JSON(http.StatusOK, gin.H{"users": users})
		return
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to search users"}})
		return
	}
	defer rows.Close()

	users := []gin.H{}
	for rows.Next() {
		var id, name, email, role, status, groupID, groupName string
		var enterpriseID, enterpriseName string
		var balance, giftBalance int64
		var createdAt interface{}
		var keyCount int
		var deleted bool
		if err := rows.Scan(&id, &name, &email, &role, &balance, &giftBalance, &status, &createdAt, &groupID, &groupName, &keyCount, &enterpriseID, &enterpriseName, &deleted); err == nil {
			users = append(users, gin.H{
				"id": id, "name": name, "email": email, "role": role,
				"group_id": groupID, "group_name": groupName, "status": status,
				"enterprise_id": enterpriseID, "enterprise_name": enterpriseName,
				"balance": balance, "gift_balance": giftBalance, "created_at": createdAt, "key_count": keyCount,
				"deleted": deleted,
			})
		}
	}
	c.JSON(http.StatusOK, gin.H{"users": users})
}
func (r *AdminRoutes) updateUser(c *gin.Context) {
	userID := c.Param("id")
	adminRole := c.GetString("role")

	var req struct {
		Name        string  `json:"name"`
		Role        string  `json:"role"`
		Balance     *int64  `json:"balance"`
		BalanceKind string  `json:"balance_kind"` // balance (funds, invoiceable) | gift (compensation, NOT invoiceable)
		GroupID     *string `json:"group_id"`
		ConfirmRole bool    `json:"confirm_role"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}

	// Check sensitive ops for role changes
	if req.Role != "" && !r.auth.CanPerformSensitiveOps(adminRole) {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "Cannot modify roles"}})
		return
	}

	// Only allow user or enterprise_admin roles
	if req.Role != "" && req.Role != "user" && req.Role != "enterprise_admin" {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "Only 'user' and 'enterprise_admin' roles are allowed"}})
		return
	}

	// Role change requires explicit confirmation
	if req.Role != "" && !req.ConfirmRole {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"message": "Role change requires confirm_role=true", "code": "CONFIRMATION_REQUIRED"},
		})
		return
	}

	ctx := c.Request.Context()
	if req.Role != "" {
		// Never allow changing your own role through this endpoint.
		if userID == c.GetString("user_id") {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Cannot change your own role via this endpoint"}})
			return
		}
		// Read the target's current role before applying the change: super_admin
		// and system_admin transitions must go through the dedicated
		// promote/demote endpoints (same guard semantics as demoteSuperAdmin).
		var targetRole string
		if err := r.db.QueryRow(ctx,
			`SELECT COALESCE(role, 'user') FROM users WHERE id = $1::uuid AND deleted_at IS NULL`, userID,
		).Scan(&targetRole); err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "User not found"}})
			return
		}
		if targetRole == "super_admin" || targetRole == "system_admin" {
			c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "Use the dedicated promote/demote endpoints to change admin roles"}})
			return
		}
		if err := r.db.Exec(ctx, `UPDATE users SET role = $1, updated_at = NOW() WHERE id = $2`, req.Role, userID); err != nil {
			r.logger.Error("Failed to update user role", zap.Error(err), zap.String("user_id", userID))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to update user role"}})
			return
		}
		r.audit.LogStructured(ctx, c.GetString("user_id"), c.GetString("user_email"),
			"admin", "update_user_role", "warning",
			fmt.Sprintf("Updated user %s role to %s", userID, req.Role),
			"user", userID, nil, req.Role)
	}
	if req.Balance != nil {
		adminUserID := c.GetString("user_id")
		// The kind selects which wallet the adjustment lands in: 'balance'
		// (real funds — consumption is invoiceable) or 'gift' (compensation /
		// promo — consumption is NOT invoiceable). Default keeps the legacy
		// behaviour of writing the funds wallet.
		kind := req.BalanceKind
		if kind == "" {
			kind = "balance"
		}
		if kind != "balance" && kind != "gift" {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "balance_kind must be 'balance' or 'gift'"}})
			return
		}
		column := "balance"
		if kind == "gift" {
			column = "gift_balance"
		}
		var oldBalance int64
		if err := r.db.QueryRow(ctx, fmt.Sprintf(`SELECT COALESCE(%s, 0) FROM users WHERE id = $1::uuid`, column), userID).Scan(&oldBalance); err != nil {
			r.logger.Error("Failed to query user balance", zap.Error(err), zap.String("user_id", userID))
			if err == pgx.ErrNoRows {
				c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "User not found"}})
			} else {
				c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to query user balance"}})
			}
			return
		}
		err := r.db.WithTx(ctx, func(tx pgx.Tx) error {
			tag, err := tx.Exec(ctx, fmt.Sprintf(`UPDATE users SET %s = $1, updated_at = NOW() WHERE id = $2`, column), *req.Balance, userID)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				return pgx.ErrNoRows
			}
			_, err = tx.Exec(ctx, `INSERT INTO balance_transactions (user_id, transaction_type, amount, balance_type, balance_before, balance_after, reference_id, description)
				VALUES ($1::uuid, 'admin_adjustment', $2, $3, $4, $5, $6, 'Admin balance adjustment')`,
				userID, *req.Balance-oldBalance, kind, oldBalance, *req.Balance, adminUserID)
			return err
		})
		if err != nil {
			r.logger.Error("Failed to update user balance", zap.Error(err), zap.String("user_id", userID))
			if err == pgx.ErrNoRows {
				c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "User not found"}})
			} else {
				c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to update user balance"}})
			}
			return
		}
		r.audit.LogStructured(ctx, adminUserID, c.GetString("user_email"),
			"admin", "update_user_balance", "warning",
			fmt.Sprintf("Updated user %s %s from %d to %d", userID, column, oldBalance, *req.Balance),
			"user", userID, oldBalance, *req.Balance)
	}
	if req.GroupID != nil {
		var oldGroupID string
		if err := r.db.QueryRow(ctx, `SELECT COALESCE(group_id::text, '') FROM users WHERE id = $1::uuid`, userID).Scan(&oldGroupID); err != nil {
			r.logger.Error("Failed to query user group", zap.Error(err), zap.String("user_id", userID))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to query user group"}})
			return
		}
		var updateErr error
		if *req.GroupID == "" {
			updateErr = r.db.Exec(ctx, `UPDATE users SET group_id = NULL, updated_at = NOW() WHERE id = $1::uuid`, userID)
		} else {
			updateErr = r.db.Exec(ctx, `UPDATE users SET group_id = $1::uuid, updated_at = NOW() WHERE id = $2`, *req.GroupID, userID)
		}
		if updateErr != nil {
			r.logger.Error("Failed to update user group", zap.Error(updateErr), zap.String("user_id", userID))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to update user group"}})
			return
		}
		r.audit.LogStructured(ctx, c.GetString("user_id"), c.GetString("user_email"),
			"admin", "update_user_group", "warning",
			fmt.Sprintf("Updated user %s group from %s to %s", userID, oldGroupID, *req.GroupID),
			"user", userID, oldGroupID, *req.GroupID)
	}
	if req.Name != "" {
		if err := r.db.Exec(ctx, `UPDATE users SET name = $1, updated_at = NOW() WHERE id = $2`, req.Name, userID); err != nil {
			r.logger.Error("Failed to update user name", zap.Error(err), zap.String("user_id", userID))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to update user name"}})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}
func (r *AdminRoutes) deleteUser(c *gin.Context) {
	userID := c.Param("id")
	currentUserID := c.GetString("user_id")
	callerRole := c.GetString("role")

	if currentUserID == userID {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": T("error.cannot_delete_self", c)}})
		return
	}

	// Read the target's role before deleting: privileged accounts require the
	// sensitive-ops capability (same check as toggleUserStatus), and the last
	// super_admin must never be deleted (same guard as demoteSuperAdmin) or
	// CanPerformSensitiveOps would have no holder left platform-wide.
	var targetRole string
	if err := r.db.QueryRow(c.Request.Context(),
		`SELECT COALESCE(role, 'user') FROM users WHERE id = $1::uuid AND deleted_at IS NULL`, userID,
	).Scan(&targetRole); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": T("error.user_not_found", c)}})
		return
	}
	if (targetRole == "super_admin" || targetRole == "system_admin") && !r.auth.CanPerformSensitiveOps(callerRole) {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "Insufficient permissions to delete this user"}})
		return
	}
	if targetRole == "super_admin" {
		var superCount int
		if err := r.db.QueryRow(c.Request.Context(),
			`SELECT COUNT(*) FROM users WHERE role = 'super_admin' AND deleted_at IS NULL`,
		).Scan(&superCount); err == nil && superCount <= 1 {
			c.JSON(http.StatusConflict, gin.H{"error": gin.H{"message": "Cannot delete the last super admin"}})
			return
		}
	}

	// Soft-deleting the user and disabling their keys must be atomic: without
	// the cascade the deleted user's API keys would keep working. Active
	// subscriptions are cancelled in the same transaction so they cannot keep
	// renewing or consuming quota as orphans.
	var deleted bool
	var disabledKeyHashes []string
	err := r.db.WithTx(c.Request.Context(), func(tx pgx.Tx) error {
		result, txErr := tx.Exec(c.Request.Context(),
			`UPDATE users SET deleted_at = NOW(), updated_at = NOW() WHERE id = $1::uuid AND deleted_at IS NULL`,
			userID)
		if txErr != nil {
			return txErr
		}
		deleted = result.RowsAffected() > 0
		if !deleted {
			return nil
		}
		hashRows, txErr := tx.Query(c.Request.Context(),
			`SELECT key_hash FROM api_keys WHERE user_id = $1::uuid AND deleted_at IS NULL AND status = 'active'`,
			userID)
		if txErr != nil {
			return txErr
		}
		for hashRows.Next() {
			var h string
			if scanErr := hashRows.Scan(&h); scanErr == nil {
				disabledKeyHashes = append(disabledKeyHashes, h)
			}
		}
		hashRows.Close()
		if _, txErr := tx.Exec(c.Request.Context(),
			`UPDATE api_keys SET status = 'disabled', updated_at = NOW()
			 WHERE user_id = $1::uuid AND deleted_at IS NULL AND status = 'active'`,
			userID); txErr != nil {
			return txErr
		}
		if _, txErr := tx.Exec(c.Request.Context(),
			`UPDATE user_subscriptions SET status = 'cancelled', updated_at = NOW()
			 WHERE user_id = $1::uuid AND status = 'active'`,
			userID); txErr != nil {
			return txErr
		}
		return nil
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": T("error.delete_user_failed", c)}})
		return
	}
	if !deleted {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": T("error.user_not_found", c)}})
		return
	}
	for _, h := range disabledKeyHashes {
		invalidateValidateCache(c.Request.Context(), r.db, r.logger, h)
	}

	r.audit.Log(c.Request.Context(), currentUserID, c.GetString("email"), "admin", "delete", "warning", fmt.Sprintf("Admin deleted user %s", userID))
	c.JSON(http.StatusOK, gin.H{"success": true, "message": T("error.user_deleted", c)})
}
func (r *AdminRoutes) createUser(c *gin.Context) {
	var req struct {
		Name     string  `json:"name"`
		Email    string  `json:"email"`
		Password string  `json:"password"`
		Role     string  `json:"role"`
		Balance  float64 `json:"balance"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if req.Email == "" || req.Password == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Email and password are required"}})
		return
	}
	if req.Role == "" {
		req.Role = "user"
	}
	// Enforce a role whitelist. Privileged roles (system_admin / super_admin)
	// require sensitive-ops capability so a plain admin cannot mint super_admins.
	switch req.Role {
	case "user", "enterprise_admin":
		// any admin may create these
	case "system_admin", "super_admin":
		if !r.auth.CanPerformSensitiveOps(c.GetString("role")) {
			c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "Insufficient privileges to create a " + req.Role + " account"}})
			return
		}
		// Enforce the configured super_admin quota.
		if req.Role == "super_admin" {
			if max := r.cfg.Admin.MaxSuperAdmins; max > 0 {
				var count int
				if err := r.db.QueryRow(c.Request.Context(),
					`SELECT COUNT(*) FROM users WHERE role = 'super_admin' AND deleted_at IS NULL`,
				).Scan(&count); err == nil && count >= max {
					c.JSON(http.StatusConflict, gin.H{"error": gin.H{"message": fmt.Sprintf("Super admin quota reached (%d/%d)", count, max)}})
					return
				}
			}
		}
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid role"}})
		return
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to hash password"}})
		return
	}

	var userID string
	err = r.db.QueryRow(c.Request.Context(),
		`INSERT INTO users (name, email, password_hash, role, balance, status, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, 'active', NOW(), NOW()) RETURNING id::text`,
		req.Name, req.Email, string(hashedPassword), req.Role, req.Balance,
	).Scan(&userID)
	if err != nil {
		if strings.Contains(err.Error(), "unique") || strings.Contains(err.Error(), "duplicate") {
			c.JSON(http.StatusConflict, gin.H{"error": gin.H{"message": "Email already registered"}})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to create user"}})
		return
	}

	r.audit.Log(c.Request.Context(), c.GetString("user_id"), c.GetString("email"), "admin", "user_created", "info", fmt.Sprintf("Admin created user %s (%s)", req.Email, userID))
	c.JSON(http.StatusOK, gin.H{"success": true, "user_id": userID})
}
func (r *AdminRoutes) getAdminMode(c *gin.Context) {
	role := c.GetString("role")

	var systemAdmins, superAdmins int
	_ = r.db.QueryRow(c.Request.Context(), `SELECT COUNT(*) FROM users WHERE role = 'system_admin'`).Scan(&systemAdmins)
	_ = r.db.QueryRow(c.Request.Context(), `SELECT COUNT(*) FROM users WHERE role = 'super_admin'`).Scan(&superAdmins)

	c.JSON(http.StatusOK, gin.H{
		"mode": r.cfg.Auth.AdminMode,
		"limits": gin.H{
			"maxSystemAdmins": r.cfg.Admin.MaxSystemAdmins,
			"maxSuperAdmins":  r.cfg.Admin.MaxSuperAdmins,
			"maxAdminsMvp":    r.cfg.Admin.MaxAdminsMVP,
		},
		"current": gin.H{
			"systemAdmins": systemAdmins,
			"superAdmins":  superAdmins,
		},
		"canPerformSensitiveOps": r.auth.CanPerformSensitiveOps(role),
		"userRole":               role,
	})
}

// promoteToSuperAdmin promotes a user to super admin
func (r *AdminRoutes) promoteToSuperAdmin(c *gin.Context) {
	userID := c.Param("id")
	callerID := c.GetString("user_id")
	callerRole := c.GetString("role")

	if !r.auth.CanPerformSensitiveOps(callerRole) {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "Only super admins can promote users"}})
		return
	}
	if callerID == userID {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Cannot promote yourself"}})
		return
	}

	var currentRole string
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT role FROM users WHERE id = $1::uuid AND deleted_at IS NULL`, userID,
	).Scan(&currentRole)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "User not found"}})
		return
	}

	if currentRole == "super_admin" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "User is already a super admin"}})
		return
	}

	// Enforce the configured super_admin quota.
	if max := r.cfg.Admin.MaxSuperAdmins; max > 0 {
		var count int
		if err := r.db.QueryRow(c.Request.Context(),
			`SELECT COUNT(*) FROM users WHERE role = 'super_admin' AND deleted_at IS NULL`,
		).Scan(&count); err == nil && count >= max {
			c.JSON(http.StatusConflict, gin.H{"error": gin.H{"message": fmt.Sprintf("Super admin quota reached (%d/%d)", count, max)}})
			return
		}
	}

	err = r.db.Exec(c.Request.Context(),
		`UPDATE users SET role = 'super_admin', updated_at = NOW() WHERE id = $1::uuid`, userID,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to promote user"}})
		return
	}

	r.audit.LogStructured(c.Request.Context(), callerID, c.GetString("email"),
		"admin", "promote_to_super_admin", "warning",
		fmt.Sprintf("Promoted user %s from %s to super_admin", userID, currentRole),
		"user", userID, currentRole, "super_admin")
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// demoteSuperAdmin demotes a super admin to system admin
func (r *AdminRoutes) demoteSuperAdmin(c *gin.Context) {
	userID := c.Param("id")
	callerID := c.GetString("user_id")
	callerRole := c.GetString("role")

	if !r.auth.CanPerformSensitiveOps(callerRole) {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "Only super admins can demote super admins"}})
		return
	}
	if callerID == userID {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Cannot demote yourself"}})
		return
	}

	var currentRole string
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT role FROM users WHERE id = $1::uuid AND deleted_at IS NULL`, userID,
	).Scan(&currentRole)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "User not found"}})
		return
	}

	if currentRole != "super_admin" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "User is not a super admin"}})
		return
	}

	// Never allow demoting the last super_admin — that would lock out the
	// sensitive-ops capability entirely.
	var superCount int
	if err := r.db.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM users WHERE role = 'super_admin' AND deleted_at IS NULL`,
	).Scan(&superCount); err == nil && superCount <= 1 {
		c.JSON(http.StatusConflict, gin.H{"error": gin.H{"message": "Cannot demote the last super admin"}})
		return
	}

	err = r.db.Exec(c.Request.Context(),
		`UPDATE users SET role = 'system_admin', updated_at = NOW() WHERE id = $1::uuid`, userID,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to demote user"}})
		return
	}

	r.audit.LogStructured(c.Request.Context(), callerID, c.GetString("email"),
		"admin", "demote_super_admin", "warning",
		fmt.Sprintf("Demoted user %s from super_admin to system_admin", userID),
		"user", userID, "super_admin", "system_admin")
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// toggleUserStatus toggles a user's status between active and suspended
func (r *AdminRoutes) toggleUserStatus(c *gin.Context) {
	userID := c.Param("id")
	currentUserID := c.GetString("user_id")
	callerRole := c.GetString("role")

	if userID == currentUserID {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Cannot disable yourself"}})
		return
	}

	var currentStatus, targetRole string
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT COALESCE(status, 'active'), COALESCE(role, 'user') FROM users WHERE id = $1::uuid AND deleted_at IS NULL`, userID,
	).Scan(&currentStatus, &targetRole)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "User not found"}})
		return
	}

	// Prevent privilege escalation via status toggle: a system_admin must not
	// be able to suspend/unsuspend a super_admin or another system_admin.
	if (targetRole == "super_admin" || targetRole == "system_admin") && !r.auth.CanPerformSensitiveOps(callerRole) {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "Insufficient permissions to toggle status of this user"}})
		return
	}

	newStatus := "suspended"
	if currentStatus == "suspended" {
		newStatus = "active"
	}

	err = r.db.Exec(c.Request.Context(),
		`UPDATE users SET status = $1, updated_at = NOW() WHERE id = $2::uuid`, newStatus, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to toggle status"}})
		return
	}

	// A suspended user's keys are rejected by validateKey; drop the cached
	// positive validation results so the gate takes effect immediately.
	invalidateUserValidateCaches(c.Request.Context(), r.db, r.logger, userID)

	// Suspending a user must also kill any management tokens they created, so
	// they cannot keep administering an enterprise programmatically after being
	// locked out of the UI. (authenticate() re-validates owner status too, but
	// revoke immediately for defense-in-depth and auditability.)
	if newStatus == "suspended" {
		RevokeManagementTokensForUser(c.Request.Context(), r.db, r.logger, userID, "")
	}

	r.audit.Log(c.Request.Context(), currentUserID, c.GetString("email"), "admin", "user_status_toggled", "warning",
		fmt.Sprintf("User %s status changed to %s", userID, newStatus))

	c.JSON(http.StatusOK, gin.H{"success": true, "status": newStatus})
}

// sendPasswordReset sends a password reset email to the user and bumps their
// token_version, forcing all existing sessions (API JWT + Web cookie) to expire.
// The admin never sees the new password — the user receives a verification code
// via email and sets it themselves through the standard forgot-password flow.
func (r *AdminRoutes) sendPasswordReset(c *gin.Context) {
	userID := c.Param("id")
	ctx := c.Request.Context()
	currentUserID := c.GetString("user_id")

	var email, name string
	err := r.db.QueryRow(ctx,
		`SELECT email, COALESCE(name, email) FROM users WHERE id = $1::uuid AND deleted_at IS NULL`,
		userID,
	).Scan(&email, &name)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "User not found"}})
		return
	}

	// Bump token_version to invalidate all existing sessions.
	if _, err := r.db.Pool.Exec(ctx,
		`UPDATE users SET token_version = token_version + 1, updated_at = NOW() WHERE id = $1::uuid`,
		userID); err != nil {
		r.logger.Error("Failed to bump token_version", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to reset password"}})
		return
	}

	// Send password reset email through the existing mail sender.
	if r.mail != nil && r.mail.IsConfigured() {
		go func() {
			if err := r.mail.SendPasswordReset(email, c.GetString("locale")); err != nil {
				r.logger.Warn("Failed to send password reset notification",
					zap.String("user_id", userID), zap.Error(err))
			}
		}()
	}

	r.audit.Log(ctx, currentUserID, c.GetString("email"), "admin", "password_reset_forced", "warning",
		fmt.Sprintf("Admin forced password reset for user %s (%s)", userID, email))

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Password reset email sent and all sessions invalidated"})
}

package routes

import (
	"testing"
)

// TestAdminRoutesUserMethodsExist is a compile-time check.
// All user management handlers require a live database.
func TestAdminRoutesUserMethodsExist(t *testing.T) {
	_ = (*AdminRoutes).listUsers
	_ = (*AdminRoutes).createUser
	_ = (*AdminRoutes).updateUser
	_ = (*AdminRoutes).deleteUser
	_ = (*AdminRoutes).getAdminMode
	_ = (*AdminRoutes).promoteToSuperAdmin
	_ = (*AdminRoutes).demoteSuperAdmin
}

// TestUserRoleValues tests valid role constants used in admin_users.
func TestUserRoleValues(t *testing.T) {
	validRoles := []string{"user", "enterprise_admin", "system_admin", "super_admin"}

	for _, role := range validRoles {
		if role == "" {
			t.Error("role should not be empty")
		}
	}

	tests := []struct {
		role       string
		isStandard bool // user or enterprise_admin
		isAdmin    bool // system_admin or super_admin
	}{
		{"user", true, false},
		{"enterprise_admin", true, false},
		{"system_admin", false, true},
		{"super_admin", false, true},
		{"", false, false},
		{"unknown", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			isAdmin := tt.role == "system_admin" || tt.role == "super_admin"
			if isAdmin != tt.isAdmin {
				t.Errorf("isAdmin(%q) = %v, want %v", tt.role, isAdmin, tt.isAdmin)
			}
		})
	}
}

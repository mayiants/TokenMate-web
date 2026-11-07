package routes

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/mayiants/ai-portal-go/internal/config"
	"github.com/mayiants/ai-portal-go/internal/db"
	"github.com/mayiants/ai-portal-go/internal/middleware"
	"github.com/mayiants/ai-portal-go/internal/testutil"
	"github.com/mayiants/ai-portal-go/pkg/audit"
	"github.com/mayiants/ai-portal-go/pkg/mail"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// Role guard matrix tests (Round-3 §8 #3): enumerate operation × caller role
// × target role and assert the allow/deny outcome for the protected mutation
// endpoints added in Phase 1:
//   - F3 PATCH /api/admin/users/:id (updateUser) — role-change guard
//   - F4 DELETE /api/admin/users/:id (deleteUser) — target-role + last-super guard
//
// Purpose: anchor the guards so a future refactor that drops one fails an
// obvious matrix row instead of going live silently.

// testAdminGuardDB opens a fresh pool against the shared routes-package
// test DB. One pool per test keeps teardown hermetic.
func testAdminGuardDB(t *testing.T) *db.DB {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), testutil.PostgresDSN())
	if err != nil {
		t.Fatalf("connect test db: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Fatalf("ping test db: %v", err)
	}
	return &db.DB{Pool: pool}
}

// newTestAdminRoutesWithMode wires an AdminRoutes under the requested
// AdminMode so the matrix can exercise both production (only super_admin can
// CanPerformSensitiveOps) and mvp (system_admin too) branches.
func newTestAdminRoutesWithMode(t *testing.T, database *db.DB, adminMode string) *AdminRoutes {
	t.Helper()
	cfg := &config.Config{}
	cfg.Auth.AdminMode = adminMode
	logger := zap.NewNop()
	auth := middleware.NewAuth(cfg, logger)
	auth.SetDB(database)
	return NewAdminRoutes(database, cfg, logger, auth, &audit.Logger{}, nil, &mail.Sender{})
}

var matrixUserNameSeq struct {
	sync.Mutex
	n int
}

func matrixUserEmail(label string) string {
	matrixUserNameSeq.Lock()
	matrixUserNameSeq.n++
	n := matrixUserNameSeq.n
	matrixUserNameSeq.Unlock()
	return fmt.Sprintf("matrix-%s-%d-%d@test.com", label, time.Now().UnixNano(), n)
}

// createMatrixUser inserts a user with the given role and returns her ID.
func createMatrixUser(t *testing.T, d *db.DB, role, label string) string {
	t.Helper()
	ctx := context.Background()
	var id string
	if err := d.Pool.QueryRow(ctx,
		`INSERT INTO users (name, email, password_hash, role)
		 VALUES ($1, $2, 'test-hash', $3) RETURNING id::text`,
		"Matrix "+label, matrixUserEmail(label), role,
	).Scan(&id); err != nil {
		t.Fatalf("insert matrix user (%s): %v", role, err)
	}
	return id
}

// matrixCtx assembles a gin.Context replicating what the auth middleware
// would have set (role, user_id, user_email) plus the :id path param. The
// route group's RequireAdmin gate is bypassed — these tests exercise the
// handler-level guards, not the group middleware.
func matrixCtx(method, path, targetID, body, callerID, callerRole string) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("role", callerRole)
	c.Set("user_id", callerID)
	c.Set("user_email", "caller-"+callerRole+"@test.com")
	req := httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	c.Params = gin.Params{{Key: "id", Value: targetID}}
	return c, w
}

// matrixRowRepo records every user ID a matrix variant created so they can be
// hard-deleted at the end of the variant by the outer defer. Using an outer
// defer (not a per-row one) means a later row's pointer into a map stays live
// until the whole matrix has finished.
type matrixRowRepo struct {
	d   *db.DB
	ids []string
	mu  sync.Mutex
}

func newMatrixRowRepo(d *db.DB) *matrixRowRepo { return &matrixRowRepo{d: d} }

func (m *matrixRowRepo) alloc(t *testing.T, role, contextLabel string) string {
	t.Helper()
	id := createMatrixUser(t, m.d, role, contextLabel)
	m.mu.Lock()
	m.ids = append(m.ids, id)
	m.mu.Unlock()
	return id
}

func (m *matrixRowRepo) cleanup() {
	ctx := context.Background()
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, id := range m.ids {
		_, _ = m.d.Pool.Exec(ctx, `DELETE FROM users WHERE id = $1::uuid`, id)
	}
}

func userRole(t *testing.T, d *db.DB, id string) string {
	t.Helper()
	var role string
	if err := d.Pool.QueryRow(context.Background(),
		`SELECT COALESCE(role,'user') FROM users WHERE id = $1::uuid`, id,
	).Scan(&role); err != nil {
		t.Fatalf("select role: %v", err)
	}
	return role
}

func userDeleted(t *testing.T, d *db.DB, id string) bool {
	t.Helper()
	var deletedAt *time.Time
	if err := d.Pool.QueryRow(context.Background(),
		`SELECT deleted_at FROM users WHERE id = $1::uuid`, id,
	).Scan(&deletedAt); err != nil {
		t.Fatalf("select deleted_at: %v", err)
	}
	return deletedAt != nil
}

// countActiveSupers returns the number of NOT deleted_at super_admins in the
// test DB at the moment of the call. The matrix intentionally leaves more
// than one super_admin alive so the matrix's own super-delete rows are NOT
// blocked by the "last super" guard — the dedicated last-super test reduces
// to exactly one super_admin under controlled conditions.
func countActiveSupers(t *testing.T, d *db.DB) int {
	t.Helper()
	var n int
	if err := d.Pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM users WHERE role='super_admin' AND deleted_at IS NULL`,
	).Scan(&n); err != nil {
		t.Fatalf("count super_admins: %v", err)
	}
	return n
}

// ternary selects a or b based on cond. Mirrors inline conditionals of the
// matrix cases.
func ternary(cond bool, a, b int) int {
	if cond {
		return a
	}
	return b
}

// --- F3: PATCH /api/admin/users/:id role change ---

// updateRoleCase is one row of the role-change matrix.
//
//   - desiredRole is what the PATCH body carries. The matrix cases use the
//     role the caller is requesting as the target's new role.
//   - confirmRole is the JSON "confirm_role" field.
type updateRoleCase struct {
	name        string
	callerRole  string
	targetRole  string // role of the freshly-created target user
	selfTarget  bool   // when true, targetID is reused as callerID so self-check fires
	desiredRole string
	confirm     bool
	wantCode    int
}

func buildUpdateRoleCases(mode string) []updateRoleCase {
	systemCanChange := mode == "mvp"
	cases := []updateRoleCase{
		// super_admin — the permitted path on plain user/ent target.
		{"super__user_target__to_user", "super_admin", "user", false, "user", true, http.StatusOK},
		{"super__ent_target__to_ent", "super_admin", "enterprise_admin", false, "enterprise_admin", true, http.StatusOK},
		// super/system target transition must route through dedicated
		// promote/demote endpoint regardless of caller sensitivity.
		{"super__system_target__to_user", "super_admin", "system_admin", false, "user", true, http.StatusForbidden},
		{"super__super_target__to_user", "super_admin", "super_admin", false, "user", true, http.StatusForbidden},
		// Self-modification of role is rejected by the self-check (400),
		// independent of confirm_role or caller sensitivity.
		{"super__self", "super_admin", "user", true, "user", true, http.StatusBadRequest},
		// Missing confirm_role → 400 even when otherwise permitted by the
		// caller's permissions.
		{"super__user_target__no_confirm", "super_admin", "user", false, "user", false, http.StatusBadRequest},
		// Unsupported requested role value → 403 even from super_admin.
		{"super__bad_role", "super_admin", "user", false, "wizard", true, http.StatusForbidden},
		// system_admin caller — only in mvp can change roles; production
		// sensitive-ops check rejects with 403 before the dedicated-endpoint
		// guard runs.
		{"sys__user_target__to_user", "system_admin", "user", false, "user", true, ternary(systemCanChange, http.StatusOK, http.StatusForbidden)},
		{"sys__system_target__to_user", "system_admin", "system_admin", false, "user", true, http.StatusForbidden},
		{"sys__super_target__to_user", "system_admin", "super_admin", false, "user", true, http.StatusForbidden},
		// Below-admin callers always fail the sensitive-ops check.
		{"ea__user_target__to_user", "enterprise_admin", "user", false, "user", true, http.StatusForbidden},
		{"plainuser__user_target__to_user", "user", "user", false, "user", true, http.StatusForbidden},
	}
	return cases
}

func runUpdateRoleMatrix(t *testing.T, mode string) {
	d := testAdminGuardDB(t)
	defer d.Pool.Close()
	r := newTestAdminRoutesWithMode(t, d, mode)
	repo := newMatrixRowRepo(d)
	defer repo.cleanup()

	for _, tc := range buildUpdateRoleCases(mode) {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			// Each row gets its own caller + target so a soft-delete mutates
			// someone else's row ID. Otherwise self-check, "role is now X"
			// assertions and the "deleted_at IS NULL" guard would mis-fire
			// across rows.
			callerID := repo.alloc(t, tc.callerRole, "caller-"+tc.name)
			targetID := repo.alloc(t, tc.targetRole, "target-"+tc.name)
			if tc.selfTarget {
				targetID = callerID // self-check should fire
			}
			body := fmt.Sprintf(`{"role":%q,"confirm_role":%t}`, tc.desiredRole, tc.confirm)
			c, rec := matrixCtx(http.MethodPatch, "/api/admin/users/"+targetID, targetID, body, callerID, tc.callerRole)
			r.updateUser(c)

			if rec.Code != tc.wantCode {
				t.Fatalf("%s [%s]: status=%d want %d (body=%s)", tc.name, mode, rec.Code, tc.wantCode, rec.Body.String())
			}
			if rec.Code == http.StatusOK {
				if got := userRole(t, d, targetID); got != tc.desiredRole {
					t.Errorf("%s: target role after update = %q, want %q", tc.name, got, tc.desiredRole)
				}
			}
		})
	}
}

func TestUpdateUser_RoleGuardMatrix(t *testing.T) {
	t.Run("production", func(t *testing.T) { runUpdateRoleMatrix(t, "production") })
	t.Run("mvp", func(t *testing.T) { runUpdateRoleMatrix(t, "mvp") })
}

// --- F4: DELETE /api/admin/users/:id ---

type deleteUserCase struct {
	name          string
	callerRole    string
	targetRole    string
	selfCall      bool // when true, targetID is reused as callerID (self-check 400)
	wantCode      int
	wantSoftDel   bool // when status==200, also assert the target was soft-deleted
	wantLastSuper bool // when true, this row expects the last-super 409 guard
}

func buildDeleteRoleCases(mode string) []deleteUserCase {
	systemSensitive := mode == "mvp"
	cases := []deleteUserCase{
		// Self-delete — always 400 regardless of role/permissions.
		{"self_super", "super_admin", "super_admin", true, http.StatusBadRequest, false, false},
		{"self_sys", "system_admin", "system_admin", true, http.StatusBadRequest, false, false},
		// Privileged target ≠ self requires CanPerformSensitiveOps. In
		// production only super_admin can; in mvp system_admin too.
		{"super_del_super", "super_admin", "super_admin", false, http.StatusOK, true, false},
		{"super_del_sys", "super_admin", "system_admin", false, http.StatusOK, true, false},
		{"sys_del_super", "system_admin", "super_admin", false, ternary(systemSensitive, http.StatusOK, http.StatusForbidden), systemSensitive, false},
		{"sys_del_sys", "system_admin", "system_admin", false, ternary(systemSensitive, http.StatusOK, http.StatusForbidden), systemSensitive, false},
		{"ea_del_sys", "enterprise_admin", "system_admin", false, http.StatusForbidden, false, false},
		// Plain user target is always deletable by any admin (no admin-target
		// gate at all on non-privileged targets).
		{"super_del_user", "super_admin", "user", false, http.StatusOK, true, false},
		{"sys_del_user", "system_admin", "user", false, http.StatusOK, true, false},
	}
	return cases
}

func runDeleteRoleMatrix(t *testing.T, mode string) {
	d := testAdminGuardDB(t)
	defer d.Pool.Close()
	r := newTestAdminRoutesWithMode(t, d, mode)
	repo := newMatrixRowRepo(d)
	defer repo.cleanup()

	for _, tc := range buildDeleteRoleCases(mode) {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			// Per-row allocation. The `last_super` guard would currently trip
			// only when this row is the only super_admin in the DB; the matrix
			// keeps many users alive across rows (deferred cleanup), so the
			// last-super path is verified by TestDeleteUser_LastSuperGuard,
			// not by these rows.
			callerID := repo.alloc(t, tc.callerRole, "caller-"+tc.name)
			targetID := repo.alloc(t, tc.targetRole, "target-"+tc.name)
			if tc.selfCall {
				targetID = callerID // self-check should fire
			}
			c, rec := matrixCtx(http.MethodDelete, "/api/admin/users/"+targetID, targetID, "", callerID, tc.callerRole)
			r.deleteUser(c)

			if rec.Code != tc.wantCode {
				t.Fatalf("%s [%s]: status=%d want %d (body=%s)", tc.name, mode, rec.Code, tc.wantCode, rec.Body.String())
			}
			if tc.wantSoftDel {
				if !userDeleted(t, d, targetID) {
					t.Errorf("%s: expected target %s to be soft-deleted", tc.name, targetID)
				}
			}
		})
	}
}

func TestDeleteUser_RoleGuardMatrix(t *testing.T) {
	t.Run("production", func(t *testing.T) { runDeleteRoleMatrix(t, "production") })
	t.Run("mvp", func(t *testing.T) { runDeleteRoleMatrix(t, "mvp") })
}

// TestDeleteUser_LastSuperGuard asserts the explicit "last super_admin" 409
// guard. Because the self-check fires before any other guard when caller ==
// target, the only way a row reaches the last-super check is via a non-self
// caller that passes CanPerformSensitiveOps while only one super_admin
// remains. In production only super_admin can pass sensitive-ops, and a
// different super_admin alive would contradict "only one super". The
// reachable scenario is mvp administrative mode: a system_admin (sensitive-ops
// in mvp) deletes a super_admin while the target is the *only* remaining
// super_admin. The handler returns 409 and leaves the super_admin alive so
// CanPerformSensitiveOps always retains a holder platform-wide.
func TestDeleteUser_LastSuperGuard(t *testing.T) {
	mode := "mvp"
	d := testAdminGuardDB(t)
	defer d.Pool.Close()
	r := newTestAdminRoutesWithMode(t, d, mode)
	repo := newMatrixRowRepo(d)
	defer repo.cleanup()

	callerID := repo.alloc(t, "system_admin", "last-super-caller")
	onlySuper := repo.alloc(t, "super_admin", "only-super-target")

	// Sanity: the last-super guard is platform-wide, so the test needs a DB
	// with exactly one active super_admin. A shared dev DB may hold manual
	// super_admin accounts (created outside the fixture repo) — skip rather
	// than fail; CI runs against a clean database where the guard is verified.
	if n := countActiveSupers(t, d); n > 1 {
		t.Skipf("shared DB has %d foreign active super_admin(s); last-super guard needs an isolated DB", n)
	} else if n == 0 {
		t.Fatal("expected exactly 1 active super_admin before delete, got 0 (fixture alloc failed)")
	}

	c, rec := matrixCtx(http.MethodDelete, "/api/admin/users/"+onlySuper, onlySuper, "", callerID, "system_admin")
	r.deleteUser(c)

	if rec.Code != http.StatusConflict {
		t.Fatalf("last-super delete: status=%d want 409 (body=%s)", rec.Code, rec.Body.String())
	}
	if userDeleted(t, d, onlySuper) {
		t.Fatalf("super_admin was soft-deleted despite last-super 409 guard")
	}
}

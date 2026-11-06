package routes

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// TestModelCleanup_MultiProviderNameSurvives is the regression guard for the
// recurring "deepseek-v4-flash / deepseek-v4-pro keep disappearing from groups"
// bug: ignoring ONE provider's row of a model whose name is still served by
// another active provider must NOT strip that name from any group.
func TestModelCleanup_MultiProviderNameSurvives(t *testing.T) {
	d := testEnterpriseDB(t)
	defer d.Close()
	ctx := context.Background()
	r := &ModelRoutes{db: d, logger: zap.NewNop()}

	suffix := uuid.NewString()[:8]
	name := "tst-multi-" + suffix
	var idA, idB, gid string
	// Two provider rows of the SAME model name, both active.
	_ = d.Pool.QueryRow(ctx,
		`INSERT INTO service_models (provider, product_id, api_model_name, status, created_at, updated_at)
		 VALUES ('provA',$1,$1,'active',NOW(),NOW()) RETURNING id::text`, name).Scan(&idA)
	_ = d.Pool.QueryRow(ctx,
		`INSERT INTO service_models (provider, product_id, api_model_name, status, created_at, updated_at)
		 VALUES ('provB',$1,$1,'active',NOW(),NOW()) RETURNING id::text`, name).Scan(&idB)
	// A group listing the name.
	models, _ := json.Marshal([]string{name, "other-model"})
	_ = d.Pool.QueryRow(ctx,
		`INSERT INTO user_groups (name, group_type, models, status, created_at, updated_at)
		 VALUES ('tst-grp-'||$2,'general',$1::jsonb,'active',NOW(),NOW()) RETURNING id::text`,
		string(models), suffix).Scan(&gid)
	defer func() {
		_, _ = d.Pool.Exec(ctx, "DELETE FROM user_groups WHERE id=$1::uuid", gid)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM service_models WHERE id=ANY($1::uuid[])", []string{idA, idB})
	}()

	// Guard says the name is still available elsewhere (provB active) -> cleanup skipped.
	if !r.modelAvailableElsewhere(ctx, idA) {
		t.Fatalf("modelAvailableElsewhere(provA row) = false, want true (provB still active)")
	}

	// Simulate the ignore-path decision: only clean up when NOT available elsewhere.
	if !r.modelAvailableElsewhere(ctx, idA) {
		r.cleanupSingleModelFromGroups(ctx, idA, false)
	}

	// The name must still be in the group.
	var present bool
	_ = d.Pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM user_groups g, jsonb_array_elements_text(g.models) m
		 WHERE g.id=$1::uuid AND m=$2)`, gid, name).Scan(&present)
	if !present {
		t.Errorf("model name %q was stripped from the group even though provB still serves it", name)
	}

	// Now discontinue provB too -> no longer available elsewhere -> cleanup proceeds.
	_, _ = d.Pool.Exec(ctx, "UPDATE service_models SET status='discontinued' WHERE id=$1::uuid", idB)
	if r.modelAvailableElsewhere(ctx, idA) {
		t.Fatalf("after discontinuing provB, modelAvailableElsewhere should be false")
	}
	r.cleanupSingleModelFromGroups(ctx, idA, false)
	_ = d.Pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM user_groups g, jsonb_array_elements_text(g.models) m
		 WHERE g.id=$1::uuid AND m=$2)`, gid, name).Scan(&present)
	if present {
		t.Errorf("model name %q should have been removed once no active provider served it", name)
	}
}

// TestCleanupAllInvalidModels_KeepsMultiProviderActiveName guards the background
// cleanup: a name that is discontinued on one provider but ACTIVE on another
// must NOT be stripped from groups (the deepseek-v4-flash/pro vanishing bug).
func TestCleanupAllInvalidModels_KeepsMultiProviderActiveName(t *testing.T) {
	d := testEnterpriseDB(t)
	defer d.Close()
	ctx := context.Background()
	r := &ModelRoutes{db: d, logger: zap.NewNop()}

	suffix := uuid.NewString()[:8]
	keepName := "tst-keep-" + suffix // active on provA, discontinued on provB
	goneName := "tst-gone-" + suffix // only discontinued
	var gid string
	_, _ = d.Pool.Exec(ctx, `INSERT INTO service_models (provider, product_id, api_model_name, status, protected, created_at, updated_at) VALUES
		('provA',$1,$1,'active',false,NOW(),NOW()),
		('provB',$1,$1,'discontinued',false,NOW(),NOW()),
		('provC',$2,$2,'discontinued',false,NOW(),NOW())`, keepName, goneName)
	models, _ := json.Marshal([]string{keepName, goneName, "stable-x"})
	_ = d.Pool.QueryRow(ctx, `INSERT INTO user_groups (name, group_type, models, status, created_at, updated_at)
		VALUES ('tst-cl-'||$2,'general',$1::jsonb,'active',NOW(),NOW()) RETURNING id::text`, string(models), suffix).Scan(&gid)
	defer func() {
		_, _ = d.Pool.Exec(ctx, "DELETE FROM user_groups WHERE id=$1::uuid", gid)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM service_models WHERE product_id=ANY($1)", []string{keepName, goneName})
	}()

	r.CleanupAllInvalidModels(ctx)

	var hasKeep, hasGone bool
	_ = d.Pool.QueryRow(ctx, `SELECT
		EXISTS(SELECT 1 FROM user_groups g, jsonb_array_elements_text(g.models) m WHERE g.id=$1::uuid AND m=$2),
		EXISTS(SELECT 1 FROM user_groups g, jsonb_array_elements_text(g.models) m WHERE g.id=$1::uuid AND m=$3)`,
		gid, keepName, goneName).Scan(&hasKeep, &hasGone)
	if !hasKeep {
		t.Errorf("%q was stripped but it is still active on provA (multi-provider protection failed)", keepName)
	}
	if hasGone {
		t.Errorf("%q should have been removed (discontinued on all providers)", goneName)
	}
}

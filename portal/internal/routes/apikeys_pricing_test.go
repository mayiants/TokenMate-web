package routes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mayiants/ai-portal-go/pkg/crypto"
	"github.com/gin-gonic/gin"
)

// TestKeyPricing_ReturnsGroupModelPrices verifies the key-authed pricing
// endpoint: a valid API key returns its group's model prices (scaled by the
// group pricing_ratio); an invalid/missing key is rejected.
func TestKeyPricing_ReturnsGroupModelPrices(t *testing.T) {
	d := testEnterpriseDB(t)
	defer d.Close()
	r := newGateTestAPIKeyRoutes(d)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	ctx := context.Background()

	// A platform group with a known model + a pricing rule.
	var groupID string
	if err := d.Pool.QueryRow(ctx,
		`INSERT INTO user_groups (name, group_type, models, pricing_ratio, status)
		 VALUES ($1,'general', '["kp-model-`+suffix+`"]'::jsonb, 1.0, 'active') RETURNING id::text`,
		"kp-grp-"+suffix).Scan(&groupID); err != nil {
		t.Fatalf("insert group: %v", err)
	}
	_, _ = d.Pool.Exec(ctx,
		`INSERT INTO service_models (provider, product_id, api_model_name, status, show_in_store)
		 VALUES ('laozhang','kp-model-`+suffix+`','kp-model-`+suffix+`','active', true)
		 ON CONFLICT (provider, product_id) DO NOTHING`)
	_, _ = d.Pool.Exec(ctx,
		`INSERT INTO model_pricing_rules (provider, region, product_id, api_model_name, billing_mode, prices, status)
		 VALUES ('laozhang','*','kp-model-`+suffix+`','kp-model-`+suffix+`','flat',
		         '{"input":1.0,"output":2.0,"fixed_request":0.005}'::jsonb,'active')`)

	userID := createGateTestUser(t, d, "kp-"+suffix+"@test.com", 100, "")
	rawKey := "sk-wgit-kp-" + suffix
	if _, err := d.Pool.Exec(ctx,
		`INSERT INTO api_keys (user_id, name, key_hash, key_prefix, key_encrypted, key_type, status, group_id, access_mode)
		 VALUES ($1::uuid,'kp',$2,$3,'','general','active',$4::uuid,'general')`,
		userID, crypto.HashKeySHA256(rawKey), rawKey[:12], groupID); err != nil {
		t.Fatalf("insert key: %v", err)
	}
	defer func() {
		_, _ = d.Pool.Exec(ctx, `DELETE FROM api_keys WHERE user_id=$1::uuid`, userID)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM model_pricing_rules WHERE product_id='kp-model-`+suffix+`'`)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM service_models WHERE product_id='kp-model-`+suffix+`'`)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM user_groups WHERE id=$1::uuid`, groupID)
		cleanupGateTestData(t, d, suffix)
	}()

	// Valid key → 200 with the model's scaled price.
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/keys/pricing", nil)
	c.Request.Header.Set("Authorization", "Bearer "+rawKey)
	r.keyPricing(c)
	if w.Code != http.StatusOK {
		t.Fatalf("keyPricing status=%d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	var resp struct {
		PricingRatio float64 `json:"pricing_ratio"`
		Data         []struct {
			Model       string   `json:"model"`
			InputPrice  *float64 `json:"input_price"`
			OutputPrice *float64 `json:"output_price"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	found := false
	for _, m := range resp.Data {
		if m.Model == "kp-model-"+suffix {
			found = true
			if m.InputPrice == nil || *m.InputPrice != 1.0 || m.OutputPrice == nil || *m.OutputPrice != 2.0 {
				t.Errorf("prices = in %v out %v, want 1.0/2.0", m.InputPrice, m.OutputPrice)
			}
		}
	}
	if !found {
		t.Errorf("model kp-model-%s not in pricing response: %s", suffix, w.Body.String())
	}

	// Missing key → 401.
	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	c2.Request = httptest.NewRequest(http.MethodGet, "/api/keys/pricing", nil)
	r.keyPricing(c2)
	if w2.Code != http.StatusUnauthorized {
		t.Errorf("no-key status=%d, want 401", w2.Code)
	}

	// Invalid key → 401.
	w3 := httptest.NewRecorder()
	c3, _ := gin.CreateTestContext(w3)
	c3.Request = httptest.NewRequest(http.MethodGet, "/api/keys/pricing", nil)
	c3.Request.Header.Set("Authorization", "Bearer sk-wgit-nonexistent-"+suffix)
	r.keyPricing(c3)
	if w3.Code != http.StatusUnauthorized {
		t.Errorf("bad-key status=%d, want 401", w3.Code)
	}
}

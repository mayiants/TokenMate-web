package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ai-proxy/ai-portal-go/internal/config"
	"github.com/ai-proxy/ai-portal-go/internal/db"
	"github.com/ai-proxy/ai-portal-go/internal/middleware"
	"github.com/ai-proxy/ai-portal-go/internal/services"
	"github.com/ai-proxy/ai-portal-go/internal/testutil"
	"github.com/ai-proxy/ai-portal-go/pkg/audit"
	"github.com/ai-proxy/ai-portal-go/pkg/mail"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

func testAdminPricingDB(t *testing.T) *db.DB {
	t.Helper()
	ctx := context.Background()
	if err := db.InitTestSchema(ctx, testutil.PostgresDSN(), zap.NewNop()); err != nil {
		t.Fatalf("init test schema: %v", err)
	}
	pool, err := pgxpool.New(ctx, testutil.PostgresDSN())
	if err != nil {
		t.Fatalf("connect to test db: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("ping test db: %v", err)
	}
	return &db.DB{Pool: pool}
}

func newTestAdminPricingRoutes(t *testing.T, database *db.DB) *AdminRoutes {
	t.Helper()
	cfg := &config.Config{}
	logger := zap.NewNop()
	auth := middleware.NewAuth(cfg, logger)
	auth.SetDB(database)
	return NewAdminRoutes(database, cfg, logger, auth, &audit.Logger{}, nil, &mail.Sender{})
}

func pricingCtx(method, path, body string, params ...gin.Param) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	for _, p := range params {
		c.Params = append(c.Params, p)
	}
	return c, w
}

func TestAdminPricing_CreateGetUpdateDelete(t *testing.T) {
	d := testAdminPricingDB(t)
	defer d.Close()
	r := newTestAdminPricingRoutes(t, d)
	ctx := context.Background()

	suffix := fmt.Sprintf("pricing-%d", time.Now().UnixNano())
	provider := "test-provider-" + suffix
	productID := "test-product-" + suffix

	// 1. Create a pricing rule
	createBody := fmt.Sprintf(`{
		"provider": "%s",
		"product_id": "%s",
		"billing_mode": "flat",
		"prices": {"input": 2.0, "output": 4.0, "cache_hit": 0.5, "cache_write": 1.0},
		"status": "active",
		"rule_name": "Test Rule %s",
		"currency": "USD",
		"region": "*"
	}`, provider, productID, suffix)

	c, w := pricingCtx("POST", "/api/admin/pricing", createBody)
	r.createPricingRule(c)

	var createResp struct {
		Success bool   `json:"success"`
		ID      string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("create unmarshal: %v (body: %s)", err, w.Body.String())
	}
	if !createResp.Success {
		t.Fatalf("create failed: %s", w.Body.String())
	}
	if createResp.ID == "" {
		t.Fatalf("expected non-empty id")
	}
	ruleID := createResp.ID

	// 2. Get the rule
	c, w = pricingCtx("GET", "/api/admin/pricing/"+ruleID, "", gin.Param{Key: "id", Value: ruleID})
	r.getPricingRule(c)

	var getResp struct {
		Rule pricingRuleJSON `json:"rule"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &getResp); err != nil {
		t.Fatalf("get unmarshal: %v (body: %s)", err, w.Body.String())
	}
	if getResp.Rule.ID != ruleID {
		t.Errorf("expected rule id %s, got %s", ruleID, getResp.Rule.ID)
	}
	if getResp.Rule.Provider != provider {
		t.Errorf("expected provider %s, got %s", provider, getResp.Rule.Provider)
	}
	if getResp.Rule.BillingMode != "flat" {
		t.Errorf("expected billing_mode 'flat', got '%s'", getResp.Rule.BillingMode)
	}

	// 3. List rules
	c, w = pricingCtx("GET", "/api/admin/pricing?provider="+provider, "")
	r.listPricingRules(c)

	var listResp struct {
		Rules []pricingRuleJSON `json:"rules"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("list unmarshal: %v (body: %s)", err, w.Body.String())
	}
	if len(listResp.Rules) == 0 {
		t.Fatalf("expected at least 1 rule in list")
	}
	found := false
	for _, rule := range listResp.Rules {
		if rule.ID == ruleID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("created rule not found in list")
	}

	// 4. Update the rule
	updateBody := fmt.Sprintf(`{
		"provider": "%s",
		"product_id": "%s",
		"billing_mode": "flat",
		"prices": {"input": 3.0, "output": 6.0, "cache_hit": 0.5, "cache_write": 1.0},
		"status": "active",
		"rule_name": "Updated Rule %s",
		"currency": "USD",
		"region": "*"
	}`, provider, productID, suffix)

	c, w = pricingCtx("PATCH", "/api/admin/pricing/"+ruleID, updateBody, gin.Param{Key: "id", Value: ruleID})
	r.updatePricingRule(c)

	var updateResp struct {
		Success bool `json:"success"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &updateResp); err != nil {
		t.Fatalf("update unmarshal: %v (body: %s)", err, w.Body.String())
	}
	if !updateResp.Success {
		t.Fatalf("update failed: %s", w.Body.String())
	}

	c, w = pricingCtx("GET", "/api/admin/pricing/"+ruleID, "", gin.Param{Key: "id", Value: ruleID})
	r.getPricingRule(c)
	if err := json.Unmarshal(w.Body.Bytes(), &getResp); err != nil {
		t.Fatalf("get after update unmarshal: %v", err)
	}

	// 5. Toggle status
	c, w = pricingCtx("POST", "/api/admin/pricing/"+ruleID+"/toggle", "", gin.Param{Key: "id", Value: ruleID})
	r.togglePricingRuleStatus(c)

	var toggleResp struct {
		Success bool   `json:"success"`
		Status  string `json:"status"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &toggleResp); err != nil {
		t.Fatalf("toggle unmarshal: %v (body: %s)", err, w.Body.String())
	}
	if !toggleResp.Success {
		t.Fatalf("toggle failed: %s", w.Body.String())
	}
	// togglePricingRuleStatus flips active -> inactive (the canonical
	// pricing-rule status pair per schema.go: status IN ('active','inactive')).
	if toggleResp.Status != "inactive" {
		t.Errorf("expected status 'inactive' after toggle, got '%s'", toggleResp.Status)
	}

	// 6. Delete the rule
	c, w = pricingCtx("DELETE", "/api/admin/pricing/"+ruleID, "", gin.Param{Key: "id", Value: ruleID})
	r.deletePricingRule(c)

	var deleteResp struct {
		Success bool `json:"success"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &deleteResp); err != nil {
		t.Fatalf("delete unmarshal: %v (body: %s)", err, w.Body.String())
	}
	if !deleteResp.Success {
		t.Fatalf("delete failed: %s", w.Body.String())
	}

	c, w = pricingCtx("GET", "/api/admin/pricing/"+ruleID, "", gin.Param{Key: "id", Value: ruleID})
	r.getPricingRule(c)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 after delete, got %d", w.Code)
	}

	// 7. Cleanup - delete any remaining test data
	_, _ = d.Pool.Exec(ctx, "DELETE FROM model_pricing_rules WHERE provider = $1", provider)
}

func TestAdminPricing_ListWithFilters(t *testing.T) {
	d := testAdminPricingDB(t)
	defer d.Close()
	r := newTestAdminPricingRoutes(t, d)
	ctx := context.Background()

	suffix := fmt.Sprintf("filter-%d", time.Now().UnixNano())
	provider := "filter-provider-" + suffix

	for i := 0; i < 3; i++ {
		name := fmt.Sprintf("filter-rule-%d-%s", i, suffix)
		_, err := d.Pool.Exec(ctx,
			`INSERT INTO model_pricing_rules (provider, product_id, billing_mode, prices, status, rule_name)
			 VALUES ($1, $2, 'flat', $3, 'active', $4)`,
			provider, "prod-"+name,
			`{"input": 1.0, "output": 2.0}`,
			name,
		)
		if err != nil {
			t.Fatalf("insert rule %d: %v", i, err)
		}
	}
	defer func() {
		_, _ = d.Pool.Exec(ctx, "DELETE FROM model_pricing_rules WHERE provider = $1", provider)
	}()

	c, w := pricingCtx("GET", "/api/admin/pricing?provider="+provider, "")
	r.listPricingRules(c)

	var listResp struct {
		Rules []pricingRuleJSON `json:"rules"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("list unmarshal: %v (body: %s)", err, w.Body.String())
	}
	if len(listResp.Rules) != 3 {
		t.Errorf("expected 3 rules, got %d", len(listResp.Rules))
	}
}

func TestAdminPricing_GetNotFound(t *testing.T) {
	d := testAdminPricingDB(t)
	defer d.Close()
	r := newTestAdminPricingRoutes(t, d)

	c, w := pricingCtx("GET", "/api/admin/pricing/"+uuid.NewString(), "", gin.Param{Key: "id", Value: uuid.NewString()})
	r.getPricingRule(c)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for nonexistent rule, got %d", w.Code)
	}
}

func TestAdminPricing_ToggleNotFound(t *testing.T) {
	d := testAdminPricingDB(t)
	defer d.Close()
	r := newTestAdminPricingRoutes(t, d)

	c, w := pricingCtx("POST", "/api/admin/pricing/"+uuid.NewString()+"/toggle", "", gin.Param{Key: "id", Value: uuid.NewString()})
	r.togglePricingRuleStatus(c)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for nonexistent toggle, got %d", w.Code)
	}
}

func TestAdminPricing_ExportEmpty(t *testing.T) {
	d := testAdminPricingDB(t)
	defer d.Close()
	r := newTestAdminPricingRoutes(t, d)

	c, w := pricingCtx("GET", "/api/admin/pricing/export?provider=nonexistent", "")
	r.exportPricingCSV(c)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for export, got %d", w.Code)
	}
	if w.Header().Get("Content-Disposition") == "" {
		t.Errorf("expected Content-Disposition header for CSV download")
	}
}

func TestAdminPricing_ImportCSV(t *testing.T) {
	d := testAdminPricingDB(t)
	defer d.Close()
	r := newTestAdminPricingRoutes(t, d)
	ctx := context.Background()

	suffix := fmt.Sprintf("csv-%d", time.Now().UnixNano())
	provider := "csv-provider-" + suffix
	productID := "csv-product-" + suffix

	_, err := d.Pool.Exec(ctx,
		`INSERT INTO model_pricing_rules (provider, product_id, billing_mode, prices, status, rule_name)
		 VALUES ($1, $2, 'flat', $3, 'active', $4)`,
		provider, productID, `{"input": 1.0, "output": 2.0}`, "CSV Import Rule "+suffix,
	)
	if err != nil {
		t.Fatalf("insert rule for import: %v", err)
	}
	defer func() {
		_, _ = d.Pool.Exec(ctx, "DELETE FROM model_pricing_rules WHERE provider = $1", provider)
	}()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("file", "pricing.csv")
	csvContent := "provider,product_id,billing_mode,input,output,cache_hit,cache_write,status,rule_name\n"
	csvContent += provider + "," + productID + ",flat,3.0,6.0,0.5,1.0,active,Updated via CSV\n"
	_, _ = part.Write([]byte(csvContent))
	_ = writer.Close()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest("POST", "/api/admin/pricing/import", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	c.Request = req

	r.importPricingCSV(c)

	var importResp struct {
		Success bool `json:"success"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &importResp); err != nil {
		t.Fatalf("import unmarshal: %v (body: %s)", err, w.Body.String())
	}
	if !importResp.Success {
		t.Fatalf("import failed: %s", w.Body.String())
	}
}

// --- Upstream pricing tests ---

func TestAdminUpstreamPricing_ListEmpty(t *testing.T) {
	d := testAdminPricingDB(t)
	defer d.Close()
	r := newTestAdminPricingRoutes(t, d)

	c, w := pricingCtx("GET", "/api/admin/upstream-pricing?provider=nonexistent", "")
	r.listUpstreamPricing(c)

	var listResp struct {
		Models  []upstreamPricingModel `json:"models"`
		Total   int                    `json:"total"`
		Summary upstreamPricingSummary `json:"summary"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("list unmarshal: %v (body: %s)", err, w.Body.String())
	}
	if listResp.Total != 0 {
		t.Errorf("expected total=0 for nonexistent provider, got %d", listResp.Total)
	}
	if len(listResp.Models) != 0 {
		t.Errorf("expected 0 models, got %d", len(listResp.Models))
	}
}

func TestAdminUpstreamPricing_ListWithData(t *testing.T) {
	d := testAdminPricingDB(t)
	defer d.Close()
	r := newTestAdminPricingRoutes(t, d)
	ctx := context.Background()

	suffix := fmt.Sprintf("up-%d", time.Now().UnixNano())
	provider := "up-provider-" + suffix
	productID := "up-model-" + suffix

	_, err := d.Pool.Exec(ctx,
		`INSERT INTO service_models (provider, product_id, api_model_name, status, input_price, output_price)
		 VALUES ($1, $2, $3, 'active', 2.0, 4.0)`,
		provider, productID, "test-model-"+suffix,
	)
	if err != nil {
		t.Fatalf("insert service model: %v", err)
	}
	defer func() {
		_, _ = d.Pool.Exec(ctx, "DELETE FROM service_models WHERE provider = $1", provider)
	}()

	c, w := pricingCtx("GET", "/api/admin/upstream-pricing?provider="+provider, "")
	r.listUpstreamPricing(c)

	var listResp struct {
		Models  []upstreamPricingModel `json:"models"`
		Total   int                    `json:"total"`
		Summary upstreamPricingSummary `json:"summary"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("list unmarshal: %v (body: %s)", err, w.Body.String())
	}
	if listResp.Total != 1 {
		t.Errorf("expected total=1, got %d", listResp.Total)
	}
	if len(listResp.Models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(listResp.Models))
	}
	if listResp.Models[0].Provider != provider {
		t.Errorf("expected provider %s, got %s", provider, listResp.Models[0].Provider)
	}
	if listResp.Models[0].Status != "active" {
		t.Errorf("expected status 'active', got '%s'", listResp.Models[0].Status)
	}
}

func TestAdminUpstreamPricing_ListWithPagination(t *testing.T) {
	d := testAdminPricingDB(t)
	defer d.Close()
	r := newTestAdminPricingRoutes(t, d)
	ctx := context.Background()

	suffix := fmt.Sprintf("pag-%d", time.Now().UnixNano())
	provider := "pag-provider-" + suffix

	for i := 0; i < 2; i++ {
		_, err := d.Pool.Exec(ctx,
			`INSERT INTO service_models (provider, product_id, api_model_name, status)
			 VALUES ($1, $2, $3, 'active')`,
			provider, "pag-model-"+fmt.Sprintf("%d", i)+"-"+suffix, "pag-test-"+fmt.Sprintf("%d", i),
		)
		if err != nil {
			t.Fatalf("insert model %d: %v", i, err)
		}
	}
	defer func() {
		_, _ = d.Pool.Exec(ctx, "DELETE FROM service_models WHERE provider = $1", provider)
	}()

	c, w := pricingCtx("GET", "/api/admin/upstream-pricing?provider="+provider+"&page=1&limit=1", "")
	r.listUpstreamPricing(c)

	var listResp struct {
		Models     []upstreamPricingModel `json:"models"`
		Total      int                    `json:"total"`
		Page       int                    `json:"page"`
		Limit      int                    `json:"limit"`
		TotalPages int                    `json:"total_pages"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("list unmarshal: %v (body: %s)", err, w.Body.String())
	}
	if listResp.Total != 2 {
		t.Errorf("expected total=2, got %d", listResp.Total)
	}
	if len(listResp.Models) != 1 {
		t.Errorf("expected 1 model on page 1, got %d", len(listResp.Models))
	}
	if listResp.Page != 1 {
		t.Errorf("expected page=1, got %d", listResp.Page)
	}
	if listResp.Limit != 1 {
		t.Errorf("expected limit=1, got %d", listResp.Limit)
	}
	if listResp.TotalPages != 2 {
		t.Errorf("expected total_pages=2, got %d", listResp.TotalPages)
	}
}

func TestAdminUpstreamPricing_Update(t *testing.T) {
	d := testAdminPricingDB(t)
	defer d.Close()
	r := newTestAdminPricingRoutes(t, d)
	ctx := context.Background()

	suffix := fmt.Sprintf("upd-%d", time.Now().UnixNano())
	provider := "upd-provider-" + suffix

	var modelID string
	err := d.Pool.QueryRow(ctx,
		`INSERT INTO service_models (provider, product_id, api_model_name, status)
		 VALUES ($1, $2, $3, 'active') RETURNING id::text`,
		provider, "upd-model-"+suffix, "upd-test-"+suffix,
	).Scan(&modelID)
	if err != nil {
		t.Fatalf("insert service model: %v", err)
	}
	defer func() {
		_, _ = d.Pool.Exec(ctx, "DELETE FROM service_models WHERE provider = $1", provider)
	}()

	updateBody := `{"input_price_cache_miss": 5.0, "output_price": 10.0, "currency": "CNY"}`
	c, w := pricingCtx("PATCH", "/api/admin/upstream-pricing/"+modelID, updateBody, gin.Param{Key: "id", Value: modelID})
	r.updateUpstreamPricing(c)

	var updateResp struct {
		Success bool `json:"success"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &updateResp); err != nil {
		t.Fatalf("update unmarshal: %v (body: %s)", err, w.Body.String())
	}
	if !updateResp.Success {
		t.Fatalf("update failed: %s", w.Body.String())
	}

	var inputMiss, output float64
	var currency string
	err = d.Pool.QueryRow(ctx,
		`SELECT COALESCE(input_price_cache_miss, 0), COALESCE(output_price, 0), COALESCE(currency, 'USD')
		 FROM service_models WHERE id = $1::uuid`,
		modelID).Scan(&inputMiss, &output, &currency)
	if err != nil {
		t.Fatalf("query updated model: %v", err)
	}
	if inputMiss != 5.0 {
		t.Errorf("expected input_price_cache_miss=5.0, got %f", inputMiss)
	}
	if output != 10.0 {
		t.Errorf("expected output_price=10.0, got %f", output)
	}
	if currency != "CNY" {
		t.Errorf("expected currency=CNY, got %s", currency)
	}
}

func TestAdminUpstreamPricing_UpdateNotFound(t *testing.T) {
	d := testAdminPricingDB(t)
	defer d.Close()
	r := newTestAdminPricingRoutes(t, d)

	body := `{"input_price_cache_miss": 1.0}`
	c, w := pricingCtx("PATCH", "/api/admin/upstream-pricing/"+uuid.NewString(), body, gin.Param{Key: "id", Value: uuid.NewString()})
	r.updateUpstreamPricing(c)

	// Note: updateUpstreamPricing does not check RowsAffected, so missing
	// rows result in 200 (silent no-op). The test documents this gap.
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 (no-op for nonexistent), got %d", w.Code)
	}
}

func TestAdminUpstreamPricing_ExportCSV(t *testing.T) {
	d := testAdminPricingDB(t)
	defer d.Close()
	r := newTestAdminPricingRoutes(t, d)
	ctx := context.Background()

	suffix := fmt.Sprintf("exp-%d", time.Now().UnixNano())
	provider := "exp-provider-" + suffix

	_, err := d.Pool.Exec(ctx,
		`INSERT INTO service_models (provider, product_id, api_model_name, status, input_price, output_price)
		 VALUES ($1, $2, $3, 'active', 2.5, 5.0)`,
		provider, "exp-model-"+suffix, "exp-test-"+suffix,
	)
	if err != nil {
		t.Fatalf("insert service model: %v", err)
	}
	defer func() {
		_, _ = d.Pool.Exec(ctx, "DELETE FROM service_models WHERE provider = $1", provider)
	}()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest("GET", "/api/admin/upstream-pricing/export", nil)
	c.Request = req

	r.exportUpstreamPricingCSV(c)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for export, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, provider) {
		t.Errorf("expected export CSV to contain provider %s", provider)
	}
	if !strings.Contains(body, "input_price_cache_miss") {
		t.Errorf("expected CSV header 'input_price_cache_miss'")
	}
}

func TestAdminUpstreamPricing_ImportCSV(t *testing.T) {
	d := testAdminPricingDB(t)
	defer d.Close()
	r := newTestAdminPricingRoutes(t, d)
	ctx := context.Background()

	suffix := fmt.Sprintf("impup-%d", time.Now().UnixNano())
	provider := "impup-provider-" + suffix
	productID := "impup-model-" + suffix

	_, err := d.Pool.Exec(ctx,
		`INSERT INTO service_models (provider, product_id, api_model_name, status)
		 VALUES ($1, $2, $3, 'active')`,
		provider, productID, "impup-test-"+suffix,
	)
	if err != nil {
		t.Fatalf("insert service model: %v", err)
	}
	defer func() {
		_, _ = d.Pool.Exec(ctx, "DELETE FROM service_models WHERE provider = $1", provider)
	}()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("file", "upstream.csv")
	csvContent := "provider,product_id,input_price_cache_miss,input_price_cache_hit,output_price\n"
	csvContent += provider + "," + productID + ",3.0,2.5,6.0\n"
	_, _ = part.Write([]byte(csvContent))
	_ = writer.Close()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest("POST", "/api/admin/upstream-pricing/import", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	c.Request = req

	r.importUpstreamPricingCSV(c)

	var importResp struct {
		Success bool `json:"success"`
		Updated int  `json:"updated"`
		Skipped int  `json:"skipped"`
		Total   int  `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &importResp); err != nil {
		t.Fatalf("import unmarshal: %v (body: %s)", err, w.Body.String())
	}
	if !importResp.Success {
		t.Fatalf("import failed: %s", w.Body.String())
	}
	if importResp.Updated != 1 {
		t.Errorf("expected updated=1, got %d", importResp.Updated)
	}
	if importResp.Total != 1 {
		t.Errorf("expected total=1, got %d", importResp.Total)
	}

	var inputMiss float64
	_ = d.Pool.QueryRow(ctx,
		`SELECT COALESCE(input_price_cache_miss, 0) FROM service_models WHERE provider = $1 AND product_id = $2`,
		provider, productID).Scan(&inputMiss)
	if inputMiss != 3.0 {
		t.Errorf("expected input_price_cache_miss=3.0 after import, got %f", inputMiss)
	}
}

func TestAdminUpstreamPricing_SearchFilter(t *testing.T) {
	d := testAdminPricingDB(t)
	defer d.Close()
	r := newTestAdminPricingRoutes(t, d)
	ctx := context.Background()

	suffix := fmt.Sprintf("srch-%d", time.Now().UnixNano())
	provider := "srch-provider-" + suffix

	_, err := d.Pool.Exec(ctx,
		`INSERT INTO service_models (provider, product_id, api_model_name, status)
		 VALUES ($1, $2, $3, 'active')`,
		provider, "gpt-4-"+suffix, "gpt-4-model",
	)
	if err != nil {
		t.Fatalf("insert model: %v", err)
	}
	defer func() {
		_, _ = d.Pool.Exec(ctx, "DELETE FROM service_models WHERE provider = $1", provider)
	}()

	c, w := pricingCtx("GET", "/api/admin/upstream-pricing?search=gpt-4&provider="+provider, "")
	r.listUpstreamPricing(c)

	var listResp struct {
		Models []upstreamPricingModel `json:"models"`
		Total  int                    `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("list unmarshal: %v (body: %s)", err, w.Body.String())
	}
	if listResp.Total == 0 {
		t.Errorf("expected search results for 'gpt-4', got 0")
	}
}

func TestAdminUpstreamPricing_InvalidPageClamp(t *testing.T) {
	d := testAdminPricingDB(t)
	defer d.Close()
	r := newTestAdminPricingRoutes(t, d)

	c, w := pricingCtx("GET", "/api/admin/upstream-pricing?page=0&limit=999", "")
	r.listUpstreamPricing(c)

	var listResp struct {
		Page  int `json:"page"`
		Limit int `json:"limit"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if listResp.Page != 1 {
		t.Errorf("expected page=1 (clamped from 0), got %d", listResp.Page)
	}
	if listResp.Limit > 500 {
		t.Errorf("expected limit clamped to 500, got %d", listResp.Limit)
	}
}

func TestAdminPricing_ImportEmptyCSV(t *testing.T) {
	d := testAdminPricingDB(t)
	defer d.Close()
	r := newTestAdminPricingRoutes(t, d)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("file", "empty.csv")
	_, _ = part.Write([]byte{})
	_ = writer.Close()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest("POST", "/api/admin/pricing/import", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	c.Request = req

	r.importPricingCSV(c)

	// Empty CSV is handled as 0 records — not an error.
	var resp struct {
		Success bool `json:"success"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.Success {
		t.Errorf("expected success=true for empty CSV (0 records)")
	}
}

func TestAdminPricing_InvalidJSON(t *testing.T) {
	d := testAdminPricingDB(t)
	defer d.Close()
	r := newTestAdminPricingRoutes(t, d)

	c, w := pricingCtx("POST", "/api/admin/pricing", `{"invalid": json`)
	r.createPricingRule(c)

	var resp struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Error.Message == "" {
		t.Errorf("expected error message for invalid JSON")
	}
}

// Helper type for pricingRuleJSON (used by tests above)
var _ = pricingRuleJSON{}

func TestFmtPrice(t *testing.T) {
	if got := fmtPrice(nil); got != "" {
		t.Errorf("expected empty string for nil, got %s", got)
	}
	v := 3.141592
	if got := fmtPrice(&v); got != "3.141592" {
		t.Errorf("expected '3.141592', got '%s'", got)
	}
}

func TestParsePrice(t *testing.T) {
	if got := parsePrice(""); got != nil {
		t.Errorf("expected nil for empty string")
	}
	if got := parsePrice("invalid"); got != nil {
		t.Errorf("expected nil for invalid string")
	}
	if got := parsePrice("  3.14  "); got == nil || *got != 3.14 {
		t.Errorf("expected 3.14, got %v", got)
	}
}

func TestReqToRule_Defaults(t *testing.T) {
	r := &AdminRoutes{}
	req := &pricingRuleRequest{
		Provider:    "test-provider",
		BillingMode: "flat",
	}
	rule := r.reqToRule(req)
	if rule.Timezone != "UTC" {
		t.Errorf("expected default timezone UTC, got %s", rule.Timezone)
	}
	if rule.Currency != "USD" {
		t.Errorf("expected default currency USD, got %s", rule.Currency)
	}
	if rule.Region != "*" {
		t.Errorf("expected default region '*', got %s", rule.Region)
	}
	if rule.Status != "active" {
		t.Errorf("expected default status 'active', got %s", rule.Status)
	}
}

func TestRuleToJSON(t *testing.T) {
	now := time.Now()
	rule := &services.PricingRule{
		ID:          "test-id",
		Provider:    "test-provider",
		BillingMode: "flat",
		Status:      "active",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	j := ruleToJSON(rule)
	if j.ID != "test-id" {
		t.Errorf("expected ID 'test-id', got '%s'", j.ID)
	}
	if j.BillingMode != "flat" {
		t.Errorf("expected BillingMode 'flat', got '%s'", j.BillingMode)
	}
}

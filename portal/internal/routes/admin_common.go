package routes

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ai-proxy/ai-portal-go/internal/aws"
	"github.com/ai-proxy/ai-portal-go/internal/config"
	"github.com/ai-proxy/ai-portal-go/internal/db"
	"github.com/ai-proxy/ai-portal-go/internal/middleware"
	"github.com/ai-proxy/ai-portal-go/internal/services"
	"github.com/ai-proxy/ai-portal-go/internal/storage"
	"github.com/ai-proxy/ai-portal-go/pkg/audit"
	"github.com/ai-proxy/ai-portal-go/pkg/crypto"
	"github.com/ai-proxy/ai-portal-go/pkg/mail"
	"github.com/ai-proxy/ai-portal-go/pkg/validation"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// exportLimitEntry tracks per-user export rate limiting
type exportLimitEntry struct {
	count   int
	resetAt time.Time
}

var exportLimitMu sync.Mutex
var exportLimits = make(map[string]exportLimitEntry)

// checkExportLimit returns true if the user has exceeded 10 exports per hour
func checkExportLimit(userID string) bool {
	exportLimitMu.Lock()
	defer exportLimitMu.Unlock()

	now := time.Now()
	entry, exists := exportLimits[userID]
	if !exists || now.After(entry.resetAt) {
		exportLimits[userID] = exportLimitEntry{count: 1, resetAt: now.Add(time.Hour)}
		return false
	}
	if entry.count >= 10 {
		return true
	}
	entry.count++
	exportLimits[userID] = entry
	return false
}

type AdminRoutes struct {
	db         *db.DB
	cfg        *config.Config
	logger     *zap.Logger
	auth       *middleware.Auth
	audit      *audit.Logger
	storage    storage.Provider
	mail       *mail.Sender
	validator  *validation.ModelValidator
	pricingSvc *services.PricingService
}

func NewAdminRoutes(db *db.DB, cfg *config.Config, logger *zap.Logger, auth *middleware.Auth, auditLog *audit.Logger, storageProvider storage.Provider, mailSender *mail.Sender) *AdminRoutes {
	return &AdminRoutes{
		db: db, cfg: cfg, logger: logger, auth: auth, audit: auditLog,
		storage: storageProvider, mail: mailSender,
		validator:  validation.NewModelValidator(db, cfg, logger),
		pricingSvc: services.NewPricingService(db, logger),
	}
}
func (r *AdminRoutes) Register(rg *gin.RouterGroup) {
	// All admin routes require auth first
	rg.Use(r.auth.RequireAuth())

	// Users
	rg.GET("/users", r.auth.RequireAdmin(), r.listUsers)
	rg.POST("/users", r.auth.RequireAdmin(), r.createUser)
	rg.PATCH("/users/:id", r.auth.RequireAdmin(), r.updateUser)
	rg.DELETE("/users/:id", r.auth.RequireAdmin(), r.deleteUser)
	rg.POST("/users/:id/promote", r.auth.RequireAdmin(), r.promoteToSuperAdmin)
	rg.POST("/users/:id/demote", r.auth.RequireAdmin(), r.demoteSuperAdmin)
	rg.POST("/users/:id/toggle-status", r.auth.RequireAdmin(), r.toggleUserStatus)
	rg.POST("/users/:id/send-password-reset", r.auth.RequireAdmin(), r.sendPasswordReset)
	// MFA recovery: super_admin, or enterprise_admin for members of their own
	// enterprise. No RequireAdmin here - the handler enforces that scope.
	rg.POST("/users/:id/mfa/reset", r.resetUserMFA)

	// Admin mode info
	rg.GET("/admin-mode", r.auth.RequireAdmin(), r.getAdminMode)

	// Enterprise applications (admin-facing)
	rg.GET("/enterprise-applications", r.auth.RequireAdmin(), r.listEnterpriseApplications)
	rg.GET("/enterprise-applications/:id", r.auth.RequireAdmin(), r.getEnterpriseApplicationDetail)
	rg.POST("/enterprise-applications/:id/approve", r.auth.RequireAdmin(), r.approveEnterpriseApplication)
	rg.POST("/enterprise-applications/:id/reject", r.auth.RequireAdmin(), r.rejectEnterpriseApplication)
	rg.POST("/enterprise-applications/:id/request-info", r.auth.RequireAdmin(), r.requestMoreInfoEnterpriseApplication)

	// Enterprise application documents (admin-facing)
	rg.GET("/enterprise-applications/:id/documents", r.auth.RequireAdmin(), r.listEnterpriseApplicationDocuments)
	rg.GET("/enterprise-applications/:id/documents/:docId/download", r.auth.RequireAdmin(), r.downloadEnterpriseApplicationDocument)

	// Upstream keys
	rg.GET("/upstream-keys", r.auth.RequireAdmin(), r.listUpstreamKeys)
	rg.POST("/upstream-keys", r.auth.RequireAdmin(), r.createUpstreamKey)

	// Providers
	rg.GET("/providers", r.auth.RequireAdmin(), r.listProviders)
	rg.GET("/providers/list", r.auth.RequireAdmin(), r.listProviders)
	rg.POST("/providers", r.auth.RequireAdmin(), r.createProvider)
	rg.PATCH("/providers/:id", r.auth.RequireAdmin(), r.updateProvider)
	rg.POST("/providers/:id/toggle", r.auth.RequireAdmin(), r.toggleProvider)
	rg.DELETE("/providers/:id", r.auth.RequireAdmin(), r.deleteProvider)

	// Service models (basic list + batch validate only; CRUD moved to models.go)
	rg.GET("/service-models", r.auth.RequireAdmin(), r.listServiceModels)
	rg.POST("/service-models/batch-validate", r.auth.RequireAdmin(), r.batchValidateModels)
	rg.GET("/service-models/sync-bedrock", r.auth.RequireAdmin(), r.syncBedrockModels)
	rg.POST("/service-models/sync-bedrock", r.auth.RequireAdmin(), r.syncBedrockModels)
	rg.POST("/service-models/sync-from-key", r.auth.RequireAdmin(), r.syncFromUpstreamKey)

	// Pricing rules
	rg.GET("/pricing", r.auth.RequireAdmin(), r.listPricingRules)
	rg.POST("/pricing", r.auth.RequireAdmin(), r.createPricingRule)
	rg.GET("/pricing/export/csv", r.auth.RequireAdmin(), r.exportPricingCSV)
	rg.POST("/pricing/import/csv", r.auth.RequireAdmin(), r.importPricingCSV)
	rg.GET("/pricing/:id", r.auth.RequireAdmin(), r.getPricingRule)
	rg.PATCH("/pricing/:id", r.auth.RequireAdmin(), r.updatePricingRule)
	rg.POST("/pricing/:id/toggle", r.auth.RequireAdmin(), r.togglePricingRuleStatus)
	rg.DELETE("/pricing/:id", r.auth.RequireAdmin(), r.deletePricingRule)

	// Upstream pricing configuration (cost basis for reconciliation-ops)
	rg.GET("/upstream-pricing", r.auth.RequireAdmin(), r.listUpstreamPricing)
	rg.PUT("/upstream-pricing/:id", r.auth.RequireAdmin(), r.updateUpstreamPricing)
	rg.POST("/upstream-pricing/:id/generate-downstream", r.auth.RequireAdmin(), r.generateDownstreamPricing)
	rg.GET("/upstream-pricing/export/csv", r.auth.RequireAdmin(), r.exportUpstreamPricingCSV)
	rg.POST("/upstream-pricing/import/csv", r.auth.RequireAdmin(), r.importUpstreamPricingCSV)

	// Models grouped by provider (for model selector)
	rg.GET("/models/grouped", r.auth.RequireAdmin(), r.listModelsGrouped)
	rg.GET("/models/grouped/public", r.listPublicModelsGrouped) // accessible to all authenticated users (no admin required)
	rg.GET("/models/capabilities", r.auth.RequireAdmin(), r.getModelCapabilities)
	rg.PUT("/models/capabilities", r.auth.RequireAdmin(), r.setModelCapabilities)

	// Models
	rg.GET("/models", r.auth.RequireAdmin(), r.listModelsAdmin)
	rg.GET("/models/status", r.auth.RequireAdmin(), r.getModelsStatus)
	rg.GET("/sync-status", r.auth.RequireAdmin(), r.getSyncStatus)
	rg.POST("/sync-openrouter", r.auth.RequireAdmin(), r.syncOpenRouter)
	rg.POST("/sync-openrouter-live", r.auth.RequireAdmin(), r.syncOpenRouterLive)
	rg.POST("/validate-openrouter", r.auth.RequireAdmin(), r.validateOpenRouter)
	rg.GET("/openrouter-models", r.auth.RequireAdmin(), r.getOpenRouterModels)

	// Key Pool (frontend uses /keypool, backend uses /key-pool)
	rg.GET("/keypool", r.auth.RequireAdmin(), r.getKeyPool)
	rg.GET("/key-pool", r.auth.RequireAdmin(), r.getKeyPool)
	rg.GET("/key-pool/:provider/:keyId/history", r.auth.RequireAdmin(), r.getKeyHistory)
	rg.POST("/keypool", r.auth.RequireAdmin(), r.addKeyPoolKey)
	rg.POST("/key-pool", r.auth.RequireAdmin(), r.addKeyPoolKey)
	rg.DELETE("/keypool/:keyId", r.auth.RequireAdmin(), r.deleteKeyPoolKeySimple)
	rg.DELETE("/key-pool/:provider/:keyId", r.auth.RequireAdmin(), r.deleteKeyPoolKey)
	rg.PATCH("/keypool/:keyId", r.auth.RequireAdmin(), r.toggleKeyPoolKeySimple)
	rg.PUT("/keypool/:keyId", r.auth.RequireAdmin(), r.updateKeyPoolKey)
	rg.PATCH("/key-pool/:provider/:keyId", r.auth.RequireAdmin(), r.toggleKeyPoolKey)
	rg.POST("/keypool/import", r.auth.RequireAdmin(), r.importKeyPoolKeys)
	rg.POST("/key-pool/import", r.auth.RequireAdmin(), r.importKeyPoolKeys)
	rg.POST("/keypool/export", r.auth.RequireAdmin(), r.exportKeyPoolKeys)
	rg.POST("/key-pool/export", r.auth.RequireAdmin(), r.exportKeyPoolKeys)
	rg.POST("/keypool/:keyId/validate", r.auth.RequireAdmin(), r.validateKeyPoolKey)
	rg.POST("/keypool/validate-batch", r.auth.RequireAdmin(), r.validateKeyPoolKeysBatch)

	// Rate limit usage dashboard
	rg.GET("/rate-limits/usage", r.auth.RequireAdmin(), r.getRateLimitUsage)
	rg.GET("/rate-limits/groups", r.auth.RequireAdmin(), r.getRateLimitGroups)
	rg.GET("/rate-limits/apikeys", r.auth.RequireAdmin(), r.getRateLimitAPIKeys)
	rg.GET("/rate-limits/upstream-keys", r.auth.RequireAdmin(), r.getRateLimitUpstreamKeys)
	rg.GET("/rate-limits/capacity", r.auth.RequireAdmin(), r.getRateLimitCapacity)
	rg.GET("/rate-limits/export", r.auth.RequireAdmin(), r.exportRateLimits)

	// Upstream usage statistics
	rg.GET("/upstream-usage", r.auth.RequireAdmin(), r.listUpstreamUsage)
	rg.GET("/upstream-usage/summary", r.auth.RequireAdmin(), r.getUpstreamUsageSummary)
	rg.GET("/upstream-usage/export", r.auth.RequireAdmin(), r.exportUpstreamUsage)

	// Provider health monitoring
	rg.GET("/provider-health", r.auth.RequireAdmin(), r.listProviderHealth)
	rg.GET("/provider-health/export", r.auth.RequireAdmin(), r.exportProviderHealth)
	rg.GET("/provider-health/summary", r.auth.RequireAdmin(), r.getProviderHealthSummary)
	rg.GET("/provider-health/:provider", r.auth.RequireAdmin(), r.getProviderHealthDetail)

	// Upstream operations center (merged upstream-usage + provider-health)
	rg.GET("/upstream-ops/health", r.auth.RequireAdmin(), r.listUpstreamOpsHealth)
	rg.GET("/upstream-ops/health/providers", r.auth.RequireAdmin(), r.listUpstreamOpsProviderHealth)
	rg.GET("/upstream-ops/health/summary", r.auth.RequireAdmin(), r.getUpstreamOpsHealthSummary)
	rg.GET("/upstream-ops/errors", r.auth.RequireAdmin(), r.listUpstreamOpsErrors)
	rg.GET("/upstream-ops/export", r.auth.RequireAdmin(), r.exportUpstreamOpsHealth)
	rg.GET("/upstream-ops/history", r.auth.RequireAdmin(), r.getUpstreamOpsHistory)

	// Key ops console API
	rg.GET("/monitor-upstream/keys", r.auth.RequireAdmin(), r.listUpstreamOpsMonitorKeys)
	rg.GET("/monitor-upstream/keys/export", r.auth.RequireAdmin(), r.exportUpstreamMonitorKeys)
	rg.GET("/monitor-downstream/export", r.auth.RequireAdmin(), r.exportDownstreamMonitor)

	// Manual reconciliation (NDJSON upload from Proxy fallback logs)
	rg.POST("/reconcile/preview", r.auth.RequireAdmin(), r.handleReconcilePreview)
	rg.POST("/reconcile/upload", r.auth.RequireAdmin(), r.handleReconcileUpload)

	// Reconciliation & Invoice (P2)
	rg.GET("/invoice-requests", r.auth.RequireAdmin(), r.listInvoiceRequests)
	rg.POST("/invoice-requests/:id/approve", r.auth.RequireAdmin(), r.approveInvoiceRequest)
	rg.POST("/invoice-requests/:id/reject", r.auth.RequireAdmin(), r.rejectInvoiceRequest)
	rg.POST("/invoice-requests/:id/complete", r.auth.RequireAdmin(), r.completeInvoiceRequest)
	rg.POST("/invoice-requests/external", r.auth.RequireAdmin(), r.recordExternalInvoice)
	rg.GET("/reconciliation/user/:id", r.auth.RequireAdmin(), r.getUserReconciliation)
	rg.GET("/reconciliation/ledger", r.auth.RequireAdmin(), r.getLedger)
	rg.GET("/reconciliation/enterprise/:id", r.auth.RequireAdmin(), r.getEnterpriseReconciliation)
	rg.POST("/reconciliation/enterprise/:id/statements/:period/payment", r.auth.RequireAdmin(), r.recordStatementPayment)
	rg.GET("/reconciliation/top-users", r.auth.RequireAdmin(), r.listTopUsers)
	rg.GET("/reconciliation/top-enterprises", r.auth.RequireAdmin(), r.listTopEnterprises)

	// User / Enterprise usage drill-down (reconciliation detail)
	rg.GET("/reconciliation/users/:id/usage", r.auth.RequireAdmin(), r.getUserUsage)
	rg.GET("/reconciliation/users/:id/logs", r.auth.RequireAdmin(), r.getUserLogs)
	rg.GET("/reconciliation/users/:id/logs/export", r.auth.RequireAdmin(), r.exportUserLogs)
	rg.GET("/reconciliation/enterprises/:id/usage", r.auth.RequireAdmin(), r.getEnterpriseUsage)
	rg.GET("/reconciliation/enterprises/:id/logs", r.auth.RequireAdmin(), r.getEnterpriseLogs)
	rg.GET("/reconciliation/enterprises/:id/logs/export", r.auth.RequireAdmin(), r.exportEnterpriseLogs)

	// Upstream provider reconciliation (new page /admin/reconciliation-ops)
	rg.GET("/reconciliation-ops/summary", r.auth.RequireAdmin(), r.getUpstreamReconciliationSummary)
	rg.GET("/reconciliation-ops/trend", r.auth.RequireAdmin(), r.getUpstreamReconciliationTrend)
	rg.GET("/reconciliation-ops/detail", r.auth.RequireAdmin(), r.getUpstreamReconciliationDetail)
	rg.GET("/reconciliation-ops/export", r.auth.RequireAdmin(), r.exportUpstreamReconciliation)

	// Dead letter queue status and unbilled records
	rg.GET("/dead-letter/status", r.auth.RequireAdmin(), r.handleDeadLetterStatus)
	rg.GET("/dead-letter/unbilled", r.auth.RequireAdmin(), r.handleListUnbilledRecords)
	rg.POST("/reconcile/unbilled", r.auth.RequireAdmin(), r.reconcileUnbilled)

	// Platform usage explorer (system admin advanced analytics)
	rg.GET("/usage-explorer/aggregate", r.auth.RequireAdmin(), r.listUsageExplorerAggregate)
	rg.GET("/usage-explorer/logs", r.auth.RequireAdmin(), r.listUsageExplorerLogs)
	rg.GET("/usage-explorer/export/aggregate", r.auth.RequireAdmin(), r.exportUsageExplorerAggregate)
	rg.GET("/usage-explorer/export/logs", r.auth.RequireAdmin(), r.exportUsageExplorerLogs)

	// Enterprise search for admin reconciliation
	rg.GET("/enterprises", r.auth.RequireAdmin(), r.searchEnterprises)
}

// syncAIModels reads aiModels.json and syncs to service_models table
func syncAIModels(db *db.DB, ctx context.Context) (gin.H, error) {
	// Valid model providers - only these 18 companies
	validProviders := map[string]bool{
		"alibaba": true, "amazon": true, "anthropic": true, "baidu": true,
		"bytedance": true, "cohere": true, "deepseek": true, "google": true,
		"meta": true, "microsoft": true, "minimax": true, "mistral": true,
		"moonshot": true, "openai": true, "perplexity": true, "tencent": true,
		"x-ai": true, "zhipu": true,
	}

	aiModelsPath := os.Getenv("AI_MODELS_PATH")
	if aiModelsPath == "" {
		aiModelsPath = "./config/aiModels.json"
	}

	data, err := os.ReadFile(aiModelsPath)
	if err != nil {
		return nil, err
	}

	var aiModels map[string]struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Products []struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Status  string `json:"status"`
			Pricing struct {
				Input  float64 `json:"input"`
				Output float64 `json:"output"`
				Unit   string  `json:"unit"`
			} `json:"pricing"`
			Features      []string `json:"features"`
			ContextWindow int      `json:"contextWindow"`
			ReleaseDate   string   `json:"releaseDate"`
			Description   string   `json:"description"`
		} `json:"products"`
	}
	if err := json.Unmarshal(data, &aiModels); err != nil {
		return nil, err
	}

	added := 0
	skipped := 0
	vendors := 0
	models := 0

	for vendorName, vendor := range aiModels {
		provider := vendor.ID
		if provider == "" {
			provider = vendorName
		}

		// Only sync from valid providers
		if !validProviders[provider] {
			vendors++
			skipped += len(vendor.Products)
			continue
		}
		vendors++

		if len(vendor.Products) == 0 {
			continue
		}

		for _, product := range vendor.Products {
			models++
			inputPrice := product.Pricing.Input
			outputPrice := product.Pricing.Output

			aimodelsData := gin.H{
				"name":          product.Name,
				"description":   product.Description,
				"features":      product.Features,
				"contextWindow": product.ContextWindow,
				"status":        product.Status,
				"releaseDate":   product.ReleaseDate,
			}
			aimodelsJSON, _ := json.Marshal(aimodelsData)

			// Upsert model
			_, err := db.Pool.Exec(ctx,
				`INSERT INTO service_models (provider, product_id, status, input_price, output_price, aimodels_data, created_at)
				 VALUES ($1, $2, 'pending', $3, $4, $5, NOW())
				 ON CONFLICT (provider, product_id) DO UPDATE SET
				 input_price = EXCLUDED.input_price,
				 output_price = EXCLUDED.output_price,
				 aimodels_data = EXCLUDED.aimodels_data,
				 updated_at = NOW()`,
				provider, product.ID, inputPrice, outputPrice, aimodelsJSON)
			if err == nil {
				added++
			}
		}
	}

	return gin.H{
		"added":   added,
		"skipped": skipped,
		"vendors": vendors,
		"models":  models,
	}, nil
}

// syncServiceModelsToKeys updates upstream_keys.models for the given provider(s)
// with active service_models (status: active, eol_pending, eol).
// If provider is empty, syncs all providers.
// Returns the number of keys updated.
func syncServiceModelsToKeys(db *db.DB, ctx context.Context, provider string) (int64, error) {
	query := `
		WITH active_models AS (
			SELECT provider, COALESCE(jsonb_agg(product_id ORDER BY product_id), '[]'::jsonb) AS models_json
			FROM service_models
			WHERE status IN ('active', 'eol_pending', 'eol')
			  AND ($1 = '' OR provider = $1)
			GROUP BY provider
		)
		UPDATE upstream_keys uk
		SET models = COALESCE(
			(SELECT am.models_json FROM active_models am WHERE am.provider = uk.provider),
			'[]'::jsonb
		),
		updated_at = NOW()
		WHERE ($1 = '' OR uk.provider = $1)
		  AND uk.deleted_at IS NULL
		  AND uk.enabled = TRUE
		  AND (uk.models IS NULL OR uk.models = '[]'::jsonb)
	`
	tag, err := db.Pool.Exec(ctx, query, provider)
	if err != nil {
		return 0, err
	}

	// The active service model set changed; invalidate the cached set used by
	// /api/keys/validate so the next validation refreshes from the database.
	invalidateActiveServiceModelsCache(ctx, db, db.Logger())

	return tag.RowsAffected(), nil
}

func trimSpace(s string) string {
	return strings.TrimSpace(s)
}

// ============================================
// Key Pool Validation
// ============================================

// providerDefaultURLs maps provider names to their default API URLs.
// Design principles:
//  1. Key = company name (e.g. "anthropic", "microsoft"), NOT product name ("claude", "azure").
//  2. Value = most common/default API endpoint. For multi-region providers (Azure, AWS),
//     this is a sensible default; per-model endpoint can override at the model level.
//  3. Provider list rarely changes; maintained as static presets.
//
// Contains 20 supported providers for upstream key validation.
var providerDefaultURLs = map[string]string{
	"openai":     "https://api.openai.com/v1",
	"anthropic":  "https://api.anthropic.com/v1",
	"google":     "https://generativelanguage.googleapis.com/v1beta",
	"deepseek":   "https://api.deepseek.com/v1",
	"moonshot":   "https://api.moonshot.cn/v1",
	"zhipu":      "https://open.bigmodel.cn/api/paas/v4",
	"alibaba":    "https://dashscope.aliyuncs.com/compatible-mode/v1",
	"baidu":      "https://aip.baidubce.com/rpc/2.0/ai_custom/v1/wenxinworkshop",
	"minimax":    "https://api.minimax.chat/v1",
	"mistral":    "https://api.mistral.ai/v1",
	"cohere":     "https://api.cohere.ai/v1",
	"perplexity": "https://api.perplexity.ai",
	"amazon":     "https://bedrock-runtime.us-east-1.amazonaws.com",
	"bytedance":  "https://ark.cn-beijing.volces.com/api/v3",
	"meta":       "https://api.llama.com/compat/v1",
	"microsoft":  "https://api.openai.azure.com",
	"tencent":    "https://hunyuan.tencentcloudapi.com",
	"xai":        "https://api.x.ai/v1",
	// Additional providers (not in the 18 core providers)
	"groq":      "https://api.groq.com/openai/v1",
	"xiaomi":    "https://api.xiaomimimo.com/v1",
	"yunfei":    "https://maas-coding-api.cn-huabei-1.xf-yun.com/v2",
	"accessapi": "https://accessapi.ai/v1",
}

// getProviderDefaultURL returns the default API URL for a provider
func getProviderDefaultURL(provider string) string {
	if url, ok := providerDefaultURLs[provider]; ok {
		return url
	}
	return ""
}

// buildModelsURL constructs the provider's /models endpoint URL.
// If baseURL already ends with a version path like /v1, /v2, /v3, /v4, /v1beta, etc.,
// just append /models. Otherwise append /v1/models.
func buildModelsURL(baseURL string) string {
	base := strings.TrimSuffix(baseURL, "/")
	if strings.HasSuffix(base, "/v1") ||
		strings.HasSuffix(base, "/v2") ||
		strings.HasSuffix(base, "/v3") ||
		strings.HasSuffix(base, "/v4") ||
		strings.HasSuffix(base, "/v1beta") ||
		strings.HasSuffix(base, "/v2beta") {
		return base + "/models"
	}
	return base + "/v1/models"
}

// validateUpstreamKey calls the provider's API to validate a key with TLS retry

func validateViaProxyFilter(provider, apiKey, providerFilter string) (string, []string, error) {
	// Resolution order: explicit PROXY_URL, then the first PROXY_CALLBACK_URLS
	// entry (already mandatory in distributed deployments, so it tracks proxy
	// host changes), then the in-stack Docker hostname.
	proxyURL := os.Getenv("PROXY_URL")
	if proxyURL == "" {
		if callbacks := os.Getenv("PROXY_CALLBACK_URLS"); callbacks != "" {
			proxyURL = strings.TrimSpace(strings.Split(callbacks, ",")[0])
		}
	}
	if proxyURL == "" {
		proxyURL = "http://ai-proxy-go:8080"
	}

	reqBodyMap := map[string]string{
		"provider": provider,
		"key":      apiKey,
	}
	if providerFilter != "" {
		reqBodyMap["provider_filter"] = providerFilter
	}
	reqBody, _ := json.Marshal(reqBodyMap)

	// Use the same secret that Portal uses for Proxy sync
	adminSecret := os.Getenv("PROXY_SYNC_SECRET")
	if adminSecret == "" {
		return "error", nil, fmt.Errorf("PROXY_SYNC_SECRET environment variable is not configured")
	}

	httpReq, err := http.NewRequest("POST",
		strings.TrimSuffix(proxyURL, "/")+"/api/internal/validate-upstream-key",
		bytes.NewReader(reqBody),
	)
	if err != nil {
		return "error", nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Admin-Secret", adminSecret)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "error", nil, fmt.Errorf("proxy unreachable: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var result struct {
		Status string `json:"status"`
		Models []struct {
			ModelID   string `json:"model_id"`
			ModelName string `json:"model_name"`
		} `json:"models"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "error", nil, fmt.Errorf("proxy response error: %w", err)
	}

	var modelIDs []string
	for _, m := range result.Models {
		id := m.ModelID
		if id == "" {
			id = m.ModelName
		}
		if id != "" {
			modelIDs = append(modelIDs, id)
		}
	}

	if result.Error != "" {
		return result.Status, modelIDs, fmt.Errorf("%s", result.Error)
	}

	return result.Status, modelIDs, nil
}

func validateUpstreamKey(provider, apiKey, baseURL string) (status string, models []string, err error) {
	// Build the models endpoint URL
	modelsURL := buildModelsURL(baseURL)

	// TLS verification is enabled by default. It can be disabled only via an explicit
	// environment variable for emergency debugging; this is strongly discouraged in production.
	tlsConfig := &tls.Config{InsecureSkipVerify: false}
	if os.Getenv("SKIP_UPSTREAM_TLS_VERIFY") == "true" {
		tlsConfig.InsecureSkipVerify = true
	}
	transport := &http.Transport{
		TLSClientConfig:   tlsConfig,
		ForceAttemptHTTP2: false,
	}
	client := &http.Client{Timeout: 5 * time.Second, Transport: transport}

	// Set auth header based on provider
	var authHeader string
	var authValue string
	switch {
	case provider == "anthropic":
		authHeader = "x-api-key"
		authValue = apiKey
	case provider == "google":
		// Google uses API key in URL
		modelsURL = strings.TrimSuffix(baseURL, "/") + "/v1/models?key=" + apiKey
		authHeader = ""
		authValue = ""
	case provider == "amazon" || (strings.HasPrefix(provider, "amazon-") && !strings.HasPrefix(provider, "amazon-mantle-")):
		// AWS Bedrock Runtime. AK/SK keys are validated locally with SigV4;
		// Bedrock API keys (ABSK... bearer, no ':') are validated via the
		// proxy, which authenticates them with an Authorization: Bearer header
		// (the local SigV4 path cannot). This keeps the per-key "Verify" button
		// working for both credential forms.
		if isBedrockBearerKey(apiKey) {
			return validateViaProxyFilter(provider, apiKey, "")
		}
		return aws.ValidateBedrockKey(provider, apiKey)
	default:
		authHeader = "Authorization"
		authValue = "Bearer " + apiKey
	}

	// Retry up to 3 times for TLS/network errors
	var resp *http.Response
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		req, err := http.NewRequest("GET", modelsURL, nil)
		if err != nil {
			return "error", nil, err
		}
		if authHeader != "" {
			req.Header.Set(authHeader, authValue)
		}
		if provider == "anthropic" {
			req.Header.Set("anthropic-version", "2023-06-01")
		}
		resp, err = client.Do(req)
		if err == nil {
			break
		}
		lastErr = err
		time.Sleep(time.Duration(attempt) * time.Second)
	}
	if lastErr != nil {
		return "error", nil, lastErr
	}
	defer func() { _ = resp.Body.Close() }()

	// Check response
	body, _ := io.ReadAll(resp.Body)

	// Some providers return HTTP 200 with error in JSON body (e.g., Minimax)
	// Check for provider-specific error patterns first
	var errorResp struct {
		BaseResp struct {
			StatusCode int    `json:"status_code"`
			StatusMsg  string `json:"status_msg"`
		} `json:"base_resp"`
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
		ErrMsg string `json:"errMsg"`
	}
	_ = json.Unmarshal(body, &errorResp)

	// Check for Minimax-style error (base_resp.status_code != 0 means error)
	if errorResp.BaseResp.StatusCode != 0 {
		return "invalid", nil, fmt.Errorf("auth failed: %s", errorResp.BaseResp.StatusMsg)
	}
	// Check for OpenAI-style error
	if errorResp.Error.Message != "" {
		return "invalid", nil, fmt.Errorf("auth failed: %s", errorResp.Error.Message)
	}
	// Check for generic error message
	if errorResp.ErrMsg != "" {
		return "invalid", nil, fmt.Errorf("error: %s", errorResp.ErrMsg)
	}

	switch resp.StatusCode {
	case 200:
		// Parse models from response
		var modelsResp struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
			Models []struct {
				ID string `json:"id"`
			} `json:"models"`
		}
		if err := json.Unmarshal(body, &modelsResp); err != nil {
			return "error", nil, fmt.Errorf("failed to parse models response: %w", err)
		}
		for _, m := range modelsResp.Data {
			models = append(models, m.ID)
		}
		for _, m := range modelsResp.Models {
			models = append(models, m.ID)
		}
		return "valid", models, nil
	case 401, 403:
		return "invalid", nil, fmt.Errorf("authentication failed")
	case 429:
		return "rate_limited", nil, fmt.Errorf("rate limited")
	default:
		return "error", nil, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}
}

// SyncUpstreamKeyModelsBackground refreshes upstream_keys.models for every
// enabled key by merging the provider's /v1/models list into the stored list.
//
// The merge is additive-only: models already on the key are never removed
// automatically, because some providers (e.g. TokenRouter) omit media models
// from /v1/models — a replace would silently break video routing. Removal
// stays a manual admin operation. Models that appear upstream but not in the
// service_models catalog are logged as onboarding candidates; they are not
// routable until a catalog entry and pricing rule exist.
func SyncUpstreamKeyModelsBackground(database *db.DB, cfg *config.Config, logger *zap.Logger) {
	logger.Info("Starting upstream key model list sync")

	// Catalog model names, for candidate detection.
	catalog := map[string]bool{}
	cRows, err := database.Query(context.Background(),
		`SELECT COALESCE(api_model_name, ''), COALESCE(product_id, '') FROM service_models WHERE deleted_at IS NULL`)
	if err == nil {
		defer cRows.Close()
		for cRows.Next() {
			var apiName, productID string
			if cRows.Scan(&apiName, &productID) == nil {
				catalog[apiName] = true
				catalog[productID] = true
			}
		}
		cRows.Close()
	}

	rows, err := database.Query(context.Background(),
		`SELECT id::text, provider, key_encrypted, COALESCE(base_url, ''), COALESCE(models::text, '[]')
		 FROM upstream_keys WHERE enabled = true AND deleted_at IS NULL
		 ORDER BY provider, created_at`)
	if err != nil {
		logger.Error("Failed to query upstream keys for model sync", zap.Error(err))
		return
	}
	defer rows.Close()

	type keyEntry struct {
		id, provider, key, baseURL string
		models                     []string
	}
	var entries []keyEntry
	for rows.Next() {
		var e keyEntry
		var enc, modelsJSON string
		if err := rows.Scan(&e.id, &e.provider, &enc, &e.baseURL, &modelsJSON); err != nil {
			continue
		}
		if e.baseURL == "" {
			e.baseURL = getProviderDefaultURL(e.provider)
		}
		if e.baseURL == "" {
			continue
		}
		if cfg.Auth.EncryptionKey != "" {
			decrypted, err := crypto.Decrypt(enc, cfg.Auth.EncryptionKey)
			if err != nil {
				continue
			}
			e.key = decrypted
		} else {
			e.key = enc
		}
		_ = json.Unmarshal([]byte(modelsJSON), &e.models)
		entries = append(entries, e)
	}
	rows.Close()

	addedTotal := 0
	candidates := map[string]bool{}
	for _, e := range entries {
		status, fetched, err := validateUpstreamKey(e.provider, e.key, e.baseURL)
		if err != nil || status != "valid" || len(fetched) == 0 {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		existing := map[string]bool{}
		for _, m := range e.models {
			existing[m] = true
		}
		var added []string
		for _, m := range fetched {
			if m == "" || existing[m] {
				continue
			}
			existing[m] = true
			e.models = append(e.models, m)
			added = append(added, m)
			if !catalog[m] {
				candidates[m] = true
			}
		}
		if len(added) > 0 {
			modelsJSON, _ := json.Marshal(e.models)
			if err := database.Exec(context.Background(),
				`UPDATE upstream_keys SET models = $1::jsonb, updated_at = NOW() WHERE id = $2::uuid`,
				string(modelsJSON), e.id); err != nil {
				logger.Error("Failed to update key models", zap.String("key_id", e.id), zap.Error(err))
				continue
			}
			addedTotal += len(added)
			logger.Info("Synced upstream key models",
				zap.String("provider", e.provider),
				zap.String("key_id", e.id),
				zap.Strings("added", added))
		}
		time.Sleep(100 * time.Millisecond)
	}

	if len(candidates) > 0 {
		list := make([]string, 0, len(candidates))
		for m := range candidates {
			list = append(list, m)
		}
		sort.Strings(list)
		logger.Info("Upstream models not in catalog (onboarding candidates)",
			zap.Strings("models", list))
	}
	logger.Info("Upstream key model list sync completed",
		zap.Int("models_added", addedTotal),
		zap.Int("catalog_candidates", len(candidates)))
}

// ValidateUpstreamKeysBackground validates keys that haven't been validated in 24 hours
func ValidateUpstreamKeysBackground(database *db.DB, cfg *config.Config, logger *zap.Logger) {
	logger.Info("Starting background key validation")

	// Get keys that need validation: invalid, pending, or not validated in 24 hours
	rows, err := database.Query(context.Background(),
		`SELECT id::text, provider, key_encrypted, key_preview, base_url
		 FROM upstream_keys
		 WHERE enabled = true
		   AND deleted_at IS NULL
		   AND (validation_status != 'valid' OR validation_at IS NULL OR validation_at < NOW() - INTERVAL '24 hours')
		 ORDER BY validation_at ASC NULLS FIRST
		 LIMIT 50`)
	if err != nil {
		logger.Error("Failed to query keys for validation", zap.Error(err))
		return
	}
	defer rows.Close()

	validated := 0
	failed := 0

	for rows.Next() {
		var id, provider, keyEncrypted, keyPreview string
		var baseURL *string
		if err := rows.Scan(&id, &provider, &keyEncrypted, &keyPreview, &baseURL); err != nil {
			continue
		}

		// Decrypt key
		var apiKey string
		if cfg.Auth.EncryptionKey != "" {
			decrypted, err := crypto.Decrypt(keyEncrypted, cfg.Auth.EncryptionKey)
			if err != nil {
				failed++
				logUpstreamKeyValidation(database, id, provider, keyPreview, "background", "decrypt_error", err.Error(), 0, 0)
				if err := database.Exec(context.Background(), `UPDATE upstream_keys SET validation_status = 'invalid', validation_at = NOW() WHERE id = $1::uuid`, id); err != nil {
					logger.Error("database operation failed", zap.Error(err))
				}
				continue
			}
			apiKey = decrypted
		} else {
			apiKey = keyEncrypted
		}

		// Resolve base URL: custom > providers table > hardcoded fallback map
		url := ""
		if baseURL != nil {
			url = *baseURL
		}
		if url == "" {
			var providerURL string
			_ = database.QueryRow(context.Background(),
				`SELECT COALESCE(base_url, '') FROM providers WHERE id = $1`,
				provider,
			).Scan(&providerURL)
			if providerURL != "" {
				url = providerURL
			}
		}
		if url == "" {
			url = getProviderDefaultURL(provider)
		}
		if url == "" {
			continue
		}

		// Validate key
		vStart := time.Now()
		status, fetchedModels, vErr := validateUpstreamKey(provider, apiKey, url)
		vDurationMs := int(time.Since(vStart).Milliseconds())
		errMsg := ""
		if vErr != nil {
			errMsg = vErr.Error()
		}
		// Network-level outcomes ("error", "rate_limited") say nothing about key
		// validity. Do NOT demote the stored status on a transient probe failure:
		// sync only ships 'valid'/'pending' keys, so one failed probe would evict a
		// healthy key from every proxy and take its provider's models down platform-
		// wide (2026-10-27: a single approach key went 'unknown' at 03:43 and both
		// its models 503'd for ~20 min until manually repaired). Only authoritative
		// outcomes update the status; probes that never reached the upstream only
		// stamp validation_at so the next scheduled run retries it.
		if status == "valid" || status == "invalid" || status == "pending" {
			if err := database.Exec(context.Background(), `UPDATE upstream_keys SET validation_status = $1, validation_at = NOW() WHERE id = $2::uuid`, status, id); err != nil {
				logger.Error("database operation failed", zap.Error(err))
			}
		} else {
			if err := database.Exec(context.Background(), `UPDATE upstream_keys SET validation_at = NOW() WHERE id = $1::uuid`, id); err != nil {
				logger.Error("database operation failed", zap.Error(err))
			}
		}
		logUpstreamKeyValidation(database, id, provider, keyPreview, "background", status, errMsg, len(fetchedModels), vDurationMs)

		if status == "valid" {
			validated++
		} else {
			failed++
		}

		// Rate limit: 100ms between validations
		time.Sleep(100 * time.Millisecond)
	}

	logger.Info("Background key validation completed",
		zap.Int("validated", validated),
		zap.Int("failed", failed))
}

// logUpstreamKeyValidation persists one upstream-key validation outcome.
// Best-effort by design: logging must never fail the validation run itself,
// and the table may not exist yet on deployments that have not applied
// migration 0020.
func logUpstreamKeyValidation(database *db.DB, keyID, provider, keyPreview, triggerType, result, errMsg string, modelsFetched, durationMs int) {
	if database == nil {
		return
	}
	if len(errMsg) > 500 {
		errMsg = errMsg[:500]
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := database.Exec(ctx, `
		INSERT INTO upstream_key_validation_log
			(upstream_key_id, provider, key_preview, trigger_type, result, error_message, models_fetched, duration_ms)
		VALUES ($1::uuid, $2, $3, $4, $5, NULLIF($6, ''), $7, $8)`,
		keyID, provider, keyPreview, triggerType, result, errMsg, modelsFetched, durationMs); err != nil {
		// Table missing (migration not applied) or transient DB error — the
		// validation outcome still stands; only the audit trail is lost.
		return
	}
}

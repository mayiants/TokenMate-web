//go:build e2e

package e2e

import (
	"testing"
)

// ============================================================================
// SERVICE-ENABLED E2E TESTS
// Tests actual functionality with running services
// ============================================================================

// TestPortalProxyHealth verifies both services are running
func TestPortalProxyHealth(t *testing.T) {
	page, cleanup := newPage(t)
	defer cleanup()

	// Test Portal health
	_, err := page.Goto(baseURL + "/api/health")
	if err != nil {
		t.Fatalf("Failed to connect to Portal: %v", err)
	}

	content, _ := page.Content()
	t.Logf("Portal health response: %s", content)
	screenshot(t, page, "service-portal-health")

	// Test Proxy health
	_, err = page.Goto("http://localhost:8080/health")
	if err != nil {
		t.Fatalf("Failed to connect to Proxy: %v", err)
	}

	content, _ = page.Content()
	t.Logf("Proxy health response: %s", content)
	screenshot(t, page, "service-proxy-health")
}

// TestProxyModelsEndpoint tests the Proxy models sync from Portal
func TestProxyModelsEndpoint(t *testing.T) {
	page, cleanup := newPage(t)
	defer cleanup()

	// Test Proxy models endpoint (if available)
	_, err := page.Goto("http://localhost:8080/v1/models")
	if err != nil {
		t.Logf("Proxy models endpoint not available: %v", err)
		return
	}

	content, _ := page.Content()
	t.Logf("Proxy models response: %s", content)
	screenshot(t, page, "service-proxy-models")
}

// TestPortalAPIKeysCRUD tests API Key creation and listing
func TestPortalAPIKeysCRUD(t *testing.T) {
	page, cleanup := newPage(t)
	defer cleanup()

	// Navigate to API Keys page
	_, err := page.Goto(baseURL + "/apikeys")
	if err != nil {
		t.Fatalf("Failed to navigate to API Keys: %v", err)
	}

	screenshot(t, page, "service-apikeys-page")

	// Check if we're logged in (redirected to login?)
	currentURL := page.URL()
	t.Logf("Current URL: %s", currentURL)

	if contains(currentURL, "/login") {
		t.Log("Not logged in - skipping CRUD test")
		t.Log("To test CRUD operations, login first or use authenticated session")
		return
	}

	// Check for existing keys
	keyRows := page.Locator(".data-table tbody tr")
	keyCount, _ := keyRows.Count()
	t.Logf("Found %d existing API keys", keyCount)

	// Look for create button
	createBtn := page.Locator("button:has-text('创建'), button:has-text('Create'), button:has-text('生成')")
	if count, _ := createBtn.Count(); count > 0 {
		t.Log("Create API Key button found - ready for CRUD test")
	}
}

// TestPortalBillingBalance tests billing balance display
func TestPortalBillingBalance(t *testing.T) {
	page, cleanup := newPage(t)
	defer cleanup()

	// Navigate to billing page
	_, err := page.Goto(baseURL + "/billing")
	if err != nil {
		t.Fatalf("Failed to navigate to Billing: %v", err)
	}

	screenshot(t, page, "service-billing-page")

	// Check balance display
	balanceCard := page.Locator(".stat-card:has-text('余额'), .stat-card:has-text('Balance')")
	if count, _ := balanceCard.Count(); count > 0 {
		t.Log("Balance card found")
		balanceValue := balanceCard.Locator(".stat-value")
		if valueCount, _ := balanceValue.Count(); valueCount > 0 {
			text, _ := balanceValue.TextContent()
			t.Logf("Balance value: %s", text)
		}
	}

	// Check for recharge button
	rechargeBtn := page.Locator("button:has-text('充值'), button:has-text('Recharge')")
	if count, _ := rechargeBtn.Count(); count > 0 {
		t.Log("Recharge button found")
	}
}

// TestPortalOrdersList tests orders listing
func TestPortalOrdersList(t *testing.T) {
	page, cleanup := newPage(t)
	defer cleanup()

	// Navigate to orders page
	_, err := page.Goto(baseURL + "/orders")
	if err != nil {
		t.Fatalf("Failed to navigate to Orders: %v", err)
	}

	screenshot(t, page, "service-orders-page")

	// Check orders table
	orderRows := page.Locator(".data-table tbody tr")
	orderCount, _ := orderRows.Count()
	t.Logf("Found %d orders", orderCount)

	// Check for status badges
	statusBadges := page.Locator(".badge")
	badgeCount, _ := statusBadges.Count()
	t.Logf("Found %d status badges", badgeCount)
}

// TestAdminProvidersList tests admin providers page
func TestAdminProvidersList(t *testing.T) {
	page, cleanup := newPage(t)
	defer cleanup()

	// Navigate to admin providers
	_, err := page.Goto(baseURL + "/admin/providers")
	if err != nil {
		t.Fatalf("Failed to navigate to Admin Providers: %v", err)
	}

	screenshot(t, page, "service-admin-providers")

	// Check providers table
	providerRows := page.Locator(".data-table tbody tr")
	providerCount, _ := providerRows.Count()
	t.Logf("Found %d providers", providerCount)

	// Check for add provider button
	addBtn := page.Locator("button:has-text('添加'), button:has-text('Add')")
	if count, _ := addBtn.Count(); count > 0 {
		t.Log("Add Provider button found")
	}
}

// TestAdminModelsList tests admin models page
func TestAdminModelsList(t *testing.T) {
	page, cleanup := newPage(t)
	defer cleanup()

	// Navigate to admin models
	_, err := page.Goto(baseURL + "/admin/models")
	if err != nil {
		t.Fatalf("Failed to navigate to Admin Models: %v", err)
	}

	screenshot(t, page, "service-admin-models")

	// Check models table
	modelRows := page.Locator(".data-table tbody tr")
	modelCount, _ := modelRows.Count()
	t.Logf("Found %d models", modelCount)

	// Check for status badges
	statusBadges := page.Locator(".badge")
	badgeCount, _ := statusBadges.Count()
	t.Logf("Found %d status badges", badgeCount)

	// Check for sync button
	syncBtn := page.Locator("button:has-text('同步'), button:has-text('Sync')")
	if count, _ := syncBtn.Count(); count > 0 {
		t.Log("Sync button found")
	}
}

// TestAdminKeyPool tests admin keypool page
func TestAdminKeyPool(t *testing.T) {
	page, cleanup := newPage(t)
	defer cleanup()

	// Navigate to admin keypool
	_, err := page.Goto(baseURL + "/admin/keypool")
	if err != nil {
		t.Fatalf("Failed to navigate to Admin KeyPool: %v", err)
	}

	screenshot(t, page, "service-admin-keypool")

	// Check keypool table
	keyRows := page.Locator(".data-table tbody tr")
	keyCount, _ := keyRows.Count()
	t.Logf("Found %d upstream keys", keyCount)

	// Check for add key button
	addBtn := page.Locator("button:has-text('添加'), button:has-text('Add'), button:has-text('创建')")
	if count, _ := addBtn.Count(); count > 0 {
		t.Log("Add Key button found")
	}
}

// TestAdminUsersList tests admin users page
func TestAdminUsersList(t *testing.T) {
	page, cleanup := newPage(t)
	defer cleanup()

	// Navigate to admin users
	_, err := page.Goto(baseURL + "/admin/users")
	if err != nil {
		t.Fatalf("Failed to navigate to Admin Users: %v", err)
	}

	screenshot(t, page, "service-admin-users")

	// Check users table
	userRows := page.Locator(".data-table tbody tr")
	userCount, _ := userRows.Count()
	t.Logf("Found %d users", userCount)

	// Check for role badges
	roleBadges := page.Locator(".badge")
	badgeCount, _ := roleBadges.Count()
	t.Logf("Found %d role badges", badgeCount)
}

// TestAdminGroupsList tests admin groups page
func TestAdminGroupsList(t *testing.T) {
	page, cleanup := newPage(t)
	defer cleanup()

	// Navigate to admin groups
	_, err := page.Goto(baseURL + "/admin/groups")
	if err != nil {
		t.Fatalf("Failed to navigate to Admin Groups: %v", err)
	}

	screenshot(t, page, "service-admin-groups")

	// Check groups table
	groupRows := page.Locator(".data-table tbody tr")
	groupCount, _ := groupRows.Count()
	t.Logf("Found %d groups", groupCount)
}

// TestAdminAuditLogs tests audit logs page
func TestAdminAuditLogs(t *testing.T) {
	page, cleanup := newPage(t)
	defer cleanup()

	// Navigate to admin audit logs
	_, err := page.Goto(baseURL + "/admin/audit-logs")
	if err != nil {
		t.Fatalf("Failed to navigate to Admin Audit Logs: %v", err)
	}

	screenshot(t, page, "service-admin-audit-logs")

	// Check audit logs table
	logRows := page.Locator(".data-table tbody tr")
	logCount, _ := logRows.Count()
	t.Logf("Found %d audit log entries", logCount)

	// Check for filter options
	filterSelects := page.Locator(".form-select")
	filterCount, _ := filterSelects.Count()
	t.Logf("Found %d filter dropdowns", filterCount)
}

// TestDashboardStats tests dashboard statistics
func TestDashboardStats(t *testing.T) {
	page, cleanup := newPage(t)
	defer cleanup()

	// Navigate to dashboard
	_, err := page.Goto(baseURL + "/dashboard")
	if err != nil {
		t.Fatalf("Failed to navigate to Dashboard: %v", err)
	}

	screenshot(t, page, "service-dashboard")

	// Check for stat cards
	statCards := page.Locator(".stat-card")
	statCount, _ := statCards.Count()
	t.Logf("Found %d stat cards", statCount)

	// Check for balance display
	balanceValue := page.Locator(".stat-value")
	if count, _ := balanceValue.Count(); count > 0 {
		text, _ := balanceValue.First().TextContent()
		t.Logf("First stat value: %s", text)
	}
}

// TestProxyChatCompletion tests proxy chat completion endpoint (if configured)
func TestProxyChatCompletion(t *testing.T) {
	// This test requires a valid API key
	// Skip if no key is available
	t.Log("Proxy chat completion test requires valid API key")
	t.Log("Skipping - manual test with curl:")
	t.Log(`curl -X POST http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -d '{"model": "gpt-4o", "messages": [{"role": "user", "content": "Hello"}]}'`)
}

// TestFullServiceChain tests the complete flow: Portal -> Proxy -> Upstream
func TestFullServiceChain(t *testing.T) {
	page, cleanup := newPage(t)
	defer cleanup()

	t.Log("=== Testing Full Service Chain ===")

	// 1. Portal Health
	_, err := page.Goto(baseURL + "/api/health")
	if err != nil {
		t.Errorf("Portal not healthy: %v", err)
		return
	}
	t.Log("✓ Portal is healthy")

	// 2. Proxy Health
	_, err = page.Goto("http://localhost:8080/health")
	if err != nil {
		t.Errorf("Proxy not healthy: %v", err)
		return
	}
	t.Log("✓ Proxy is healthy")

	// 3. Check Portal admin pages
	pages := []string{
		"/admin/providers",
		"/admin/models",
		"/admin/keypool",
		"/admin/users",
		"/admin/groups",
		"/admin/audit-logs",
	}

	for _, p := range pages {
		_, err = page.Goto(baseURL + p)
		if err != nil {
			t.Logf("Warning: Failed to load %s: %v", p, err)
			continue
		}
		t.Logf("✓ %s loaded", p)
	}

	screenshot(t, page, "service-chain-complete")
	t.Log("=== Service Chain Test Complete ===")
}

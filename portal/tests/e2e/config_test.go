//go:build e2e

package e2e

import (
	"testing"
	"time"
)

// ============================================================================
// STYLE CONSISTENCY TESTS
// ============================================================================

// TestStyleConsistency checks UI elements follow style guide
func TestStyleConsistency(t *testing.T) {
	page, cleanup := newPage(t)
	defer cleanup()

	// Navigate to login page
	_, err := page.Goto(baseURL + "/login")
	if err != nil {
		t.Fatalf("Failed to navigate: %v", err)
	}

	screenshot(t, page, "style-login")

	// 1. Check font family
	fontFamily, _ := page.Evaluate("() => getComputedStyle(document.body).fontFamily")
	t.Logf("Body font family: %s", fontFamily)
	if !contains(fontFamily.(string), "Inter") && !contains(fontFamily.(string), "system-ui") {
		t.Error("Font family should include Inter or system-ui")
	}

	// 2. Check button styling
	buttons := page.Locator(".btn")
	count, _ := buttons.Count()
	t.Logf("Found %d buttons", count)

	if count > 0 {
		firstBtn := buttons.First()
		btnRadius, _ := firstBtn.Evaluate("el => getComputedStyle(el).borderRadius", nil)
		t.Logf("Button border-radius: %s", btnRadius)
	}

	// 3. Check form inputs
	inputs := page.Locator(".form-input, input[type='text'], input[type='email'], input[type='password']")
	inputCount, _ := inputs.Count()
	t.Logf("Found %d form inputs", inputCount)

	if inputCount > 0 {
		firstInput := inputs.First()
		inputRadius, _ := firstInput.Evaluate("el => getComputedStyle(el).borderRadius", nil)
		t.Logf("Input border-radius: %s", inputRadius)
	}

	// 4. Check no emojis in visible text
	bodyText, _ := page.Locator("body").TextContent()
	emojis := findEmojis(bodyText)
	if len(emojis) > 0 {
		t.Logf("Warning: Found emojis in page: %v", emojis)
	}

	// 5. Check color scheme consistency
	bgColor, _ := page.Evaluate("() => getComputedStyle(document.body).backgroundColor")
	t.Logf("Background color: %s", bgColor)
}

// TestAdminPagesStyleConsistency checks all admin pages follow style guide
func TestAdminPagesStyleConsistency(t *testing.T) {
	adminPages := []struct {
		name string
		path string
	}{
		{"admin-dashboard", "/admin"},
		{"admin-users", "/admin/users"},
		{"admin-keys", "/admin/keys"},
		{"admin-groups", "/admin/groups"},
		{"admin-providers", "/admin/providers"},
		{"admin-models", "/admin/models"},
		{"admin-keypool", "/admin/keypool"},
		{"admin-settings", "/admin/settings"},
		{"admin-subscriptions", "/admin/subscriptions"},
		{"admin-audit-logs", "/admin/audit-logs"},
	}

	page, cleanup := newPage(t)
	defer cleanup()

	for _, p := range adminPages {
		t.Run(p.name, func(t *testing.T) {
			_, err := page.Goto(baseURL + p.path)
			if err != nil {
				t.Logf("Failed to navigate to %s: %v", p.path, err)
				return
			}

			screenshot(t, page, "style-"+p.name)

			// Check for consistent elements
			// 1. Sidebar presence
			sidebar := page.Locator(".sidebar")
			if sidebarCount, _ := sidebar.Count(); sidebarCount == 0 {
				t.Logf("Warning: No sidebar found on %s", p.name)
			}

			// 2. Card styling
			cards := page.Locator(".card")
			cardCount, _ := cards.Count()
			t.Logf("%s: Found %d cards", p.name, cardCount)

			// 3. Table styling
			tables := page.Locator(".data-table")
			tableCount, _ := tables.Count()
			t.Logf("%s: Found %d tables", p.name, tableCount)

			// 4. Badge consistency
			badges := page.Locator(".badge")
			badgeCount, _ := badges.Count()
			t.Logf("%s: Found %d badges", p.name, badgeCount)

			// 5. Button consistency
			buttons := page.Locator(".btn")
			btnCount, _ := buttons.Count()
			t.Logf("%s: Found %d buttons", p.name, btnCount)

			// 6. Check for emojis
			pageText, _ := page.Locator("body").TextContent()
			emojis := findEmojis(pageText)
			if len(emojis) > 0 {
				t.Logf("Warning: %s has emojis: %v", p.name, emojis)
			}
		})
	}
}

// ============================================================================
// PROVIDER + MODEL + UPSTREAM KEY CONFIGURATION TESTS
// ============================================================================

// TestProviderModelKeyRelationship tests the provider-model-upstream key configuration
func TestProviderModelKeyRelationship(t *testing.T) {
	page, cleanup := newPage(t)
	defer cleanup()

	// Navigate to admin providers page
	_, err := page.Goto(baseURL + "/admin/providers")
	if err != nil {
		t.Fatalf("Failed to navigate: %v", err)
	}

	screenshot(t, page, "config-providers")

	// Check providers list
	providerRows := page.Locator(".data-table tbody tr")
	providerCount, _ := providerRows.Count()
	t.Logf("Found %d providers", providerCount)

	// Navigate to admin models page
	_, err = page.Goto(baseURL + "/admin/models")
	if err != nil {
		t.Fatalf("Failed to navigate: %v", err)
	}

	screenshot(t, page, "config-models")

	// Check models list and their provider association
	modelRows := page.Locator(".data-table tbody tr")
	modelCount, _ := modelRows.Count()
	t.Logf("Found %d models", modelCount)

	// Check model status badges
	statusBadges := page.Locator(".badge")
	badgeCount, _ := statusBadges.Count()
	t.Logf("Found %d status badges", badgeCount)

	// Navigate to admin keypool page
	_, err = page.Goto(baseURL + "/admin/keypool")
	if err != nil {
		t.Fatalf("Failed to navigate: %v", err)
	}

	screenshot(t, page, "config-keypool")

	// Check upstream keys
	keyRows := page.Locator(".data-table tbody tr")
	keyCount, _ := keyRows.Count()
	t.Logf("Found %d upstream keys", keyCount)

	// Test relationship: click on a provider to see associated models
	// This tests the UI coupling between provider and models
	t.Log("Testing Provider -> Model relationship UI")

	// Check if provider column exists in models table
	providerCol := page.Locator(".data-table th:has-text('供应商'), .data-table th:has-text('Provider')")
	if providerColCount, _ := providerCol.Count(); providerColCount > 0 {
		t.Log("Provider column found in models table - relationship visible")
	}

	// Test relationship: upstream key should show provider association
	_, err = page.Goto(baseURL + "/admin/keypool")
	if err != nil {
		t.Logf("Failed to navigate to keypool: %v", err)
	}

	// Check if provider column exists in keypool table
	keypoolProviderCol := page.Locator(".data-table th:has-text('供应商'), .data-table th:has-text('Provider')")
	if keypoolProviderColCount, _ := keypoolProviderCol.Count(); keypoolProviderColCount > 0 {
		t.Log("Provider column found in keypool table - relationship visible")
	}
}

// TestProviderModelKeyCreateFlow tests creating a complete configuration chain
func TestProviderModelKeyCreateFlow(t *testing.T) {
	page, cleanup := newPage(t)
	defer cleanup()

	// Step 1: Check if we can add a provider
	_, err := page.Goto(baseURL + "/admin/providers")
	if err != nil {
		t.Fatalf("Failed to navigate: %v", err)
	}

	// Look for "Add Provider" button
	addProviderBtn := page.Locator("button:has-text('添加'), button:has-text('Add'), button:has-text('创建')")
	if count, _ := addProviderBtn.Count(); count > 0 {
		t.Log("Add Provider button found")
		_ = addProviderBtn.First().Click()
		time.Sleep(500 * time.Millisecond)
		screenshot(t, page, "config-add-provider-modal")

		// Check modal appeared
		modal := page.Locator(".modal")
		if modalCount, _ := modal.Count(); modalCount > 0 {
			t.Log("Add Provider modal appeared")

			// Check form fields
			nameInput := modal.Locator("input[name='name'], input[name='Name']")
			if nameInputCount, _ := nameInput.Count(); nameInputCount > 0 {
				t.Log("Provider name input found")
			}

			// Close modal
			closeBtn := modal.Locator(".modal-close, button:has-text('取消'), button:has-text('Cancel')")
			if closeBtnCount, _ := closeBtn.Count(); closeBtnCount > 0 {
				_ = closeBtn.First().Click()
			}
		}
	}

	// Step 2: Check model creation
	_, err = page.Goto(baseURL + "/admin/models")
	if err != nil {
		t.Logf("Failed to navigate: %v", err)
	}

	addModelBtn := page.Locator("button:has-text('添加'), button:has-text('手动添加')")
	if count, _ := addModelBtn.Count(); count > 0 {
		t.Log("Add Model button found")
	}

	// Step 3: Check upstream key creation
	_, err = page.Goto(baseURL + "/admin/keypool")
	if err != nil {
		t.Logf("Failed to navigate: %v", err)
	}

	addKeyBtn := page.Locator("button:has-text('添加'), button:has-text('创建')")
	if count, _ := addKeyBtn.Count(); count > 0 {
		t.Log("Add Upstream Key button found")
	}
}

// ============================================================================
// USER + USER GROUP + USER KEY CONFIGURATION TESTS
// ============================================================================

// TestUserGroupKeyRelationship tests the user-group-key configuration
func TestUserGroupKeyRelationship(t *testing.T) {
	page, cleanup := newPage(t)
	defer cleanup()

	// Navigate to admin users page
	_, err := page.Goto(baseURL + "/admin/users")
	if err != nil {
		t.Fatalf("Failed to navigate: %v", err)
	}

	screenshot(t, page, "config-users")

	// Check users list
	userRows := page.Locator(".data-table tbody tr")
	userCount, _ := userRows.Count()
	t.Logf("Found %d users", userCount)

	// Check for group column in users table
	groupCol := page.Locator(".data-table th:has-text('企业'), .data-table th:has-text('Enterprise'), .data-table th:has-text('Group')")
	if groupColCount, _ := groupCol.Count(); groupColCount > 0 {
		t.Log("Group/Enterprise column found in users table - relationship visible")
	}

	// Navigate to admin groups page
	_, err = page.Goto(baseURL + "/admin/groups")
	if err != nil {
		t.Fatalf("Failed to navigate: %v", err)
	}

	screenshot(t, page, "config-groups")

	// Check groups list
	groupRows := page.Locator(".data-table tbody tr")
	groupCount, _ := groupRows.Count()
	t.Logf("Found %d groups", groupCount)

	// Navigate to admin keys page (user API keys)
	_, err = page.Goto(baseURL + "/admin/keys")
	if err != nil {
		t.Fatalf("Failed to navigate: %v", err)
	}

	screenshot(t, page, "config-admin-keys")

	// Check user keys list
	keyRows := page.Locator(".data-table tbody tr")
	keyCount, _ := keyRows.Count()
	t.Logf("Found %d user API keys", keyCount)

	// Check for user column in keys table
	userCol := page.Locator(".data-table th:has-text('用户'), .data-table th:has-text('User'), .data-table th:has-text('Owner')")
	if userColCount, _ := userCol.Count(); userColCount > 0 {
		t.Log("User column found in keys table - relationship visible")
	}

	// Check for group column in keys table
	keyGroupCol := page.Locator(".data-table th:has-text('分组'), .data-table th:has-text('Group')")
	if keyGroupColCount, _ := keyGroupCol.Count(); keyGroupColCount > 0 {
		t.Log("Group column found in keys table - relationship visible")
	}
}

// TestUserKeyCreateFlow tests creating a user key with group assignment
func TestUserKeyCreateFlow(t *testing.T) {
	page, cleanup := newPage(t)
	defer cleanup()

	// Navigate to user's API keys page
	_, err := page.Goto(baseURL + "/apikeys")
	if err != nil {
		t.Fatalf("Failed to navigate: %v", err)
	}

	screenshot(t, page, "config-user-apikeys")

	// Look for create key button
	createKeyBtn := page.Locator("button:has-text('创建'), button:has-text('Create'), button:has-text('生成')")
	if count, _ := createKeyBtn.Count(); count > 0 {
		t.Log("Create API Key button found")
		_ = createKeyBtn.First().Click()
		time.Sleep(500 * time.Millisecond)
		screenshot(t, page, "config-create-key-modal")

		// Check modal appeared
		modal := page.Locator(".modal")
		if modalCount, _ := modal.Count(); modalCount > 0 {
			t.Log("Create Key modal appeared")

			// Check for group selection dropdown
			groupSelect := modal.Locator("select[name='group'], select[name='Group']")
			if groupSelectCount, _ := groupSelect.Count(); groupSelectCount > 0 {
				t.Log("Group selection dropdown found - user can assign key to group")
			}

			// Close modal
			closeBtn := modal.Locator(".modal-close, button:has-text('取消'), button:has-text('Cancel')")
			if closeBtnCount, _ := closeBtn.Count(); closeBtnCount > 0 {
				_ = closeBtn.First().Click()
			}
		}
	}
}

// ============================================================================
// ORDER + BILLING + TOKEN/REQUEST STATISTICS TESTS
// ============================================================================

// TestOrderBillingStatisticsRelationship tests the order-billing-statistics chain
func TestOrderBillingStatisticsRelationship(t *testing.T) {
	page, cleanup := newPage(t)
	defer cleanup()

	// Navigate to orders page
	_, err := page.Goto(baseURL + "/orders")
	if err != nil {
		t.Fatalf("Failed to navigate: %v", err)
	}

	screenshot(t, page, "config-orders")

	// Check orders list
	orderRows := page.Locator(".data-table tbody tr")
	orderCount, _ := orderRows.Count()
	t.Logf("Found %d orders", orderCount)

	// Check order status badges
	statusBadges := page.Locator(".badge")
	badgeCount, _ := statusBadges.Count()
	t.Logf("Found %d order status badges", badgeCount)

	// Navigate to billing page
	_, err = page.Goto(baseURL + "/billing")
	if err != nil {
		t.Fatalf("Failed to navigate: %v", err)
	}

	screenshot(t, page, "config-billing")

	// Check balance display
	balanceValue := page.Locator(".stat-value, .balance-value")
	if balanceCount, _ := balanceValue.Count(); balanceCount > 0 {
		t.Log("Balance display found")
	}

	// Check billing history
	billingRows := page.Locator(".data-table tbody tr")
	billingCount, _ := billingRows.Count()
	t.Logf("Found %d billing records", billingCount)

	// Navigate to invoices page
	_, err = page.Goto(baseURL + "/invoices")
	if err != nil {
		t.Logf("Failed to navigate to invoices: %v", err)
	} else {
		screenshot(t, page, "config-invoices")
	}

	// Navigate to dashboard for usage statistics
	_, err = page.Goto(baseURL + "/dashboard")
	if err != nil {
		t.Fatalf("Failed to navigate: %v", err)
	}

	screenshot(t, page, "config-dashboard-stats")

	// Check token usage stats
	tokenStats := page.Locator(".stat-card:has-text('tokens'), .stat-card:has-text('用量')")
	tokenStatCount, _ := tokenStats.Count()
	t.Logf("Found %d token usage stat cards", tokenStatCount)

	// Check request stats
	requestStats := page.Locator(".stat-card:has-text('请求'), .stat-card:has-text('requests')")
	requestStatCount, _ := requestStats.Count()
	t.Logf("Found %d request stat cards", requestStatCount)
}

// TestOrderCreateFlow tests creating an order and its billing impact
func TestOrderCreateFlow(t *testing.T) {
	page, cleanup := newPage(t)
	defer cleanup()

	// Navigate to billing page to see recharge options
	_, err := page.Goto(baseURL + "/billing")
	if err != nil {
		t.Fatalf("Failed to navigate: %v", err)
	}

	screenshot(t, page, "billing-recharge")

	// Look for recharge/create order button
	rechargeBtn := page.Locator("button:has-text('充值'), button:has-text('Recharge'), button:has-text('购买')")
	if count, _ := rechargeBtn.Count(); count > 0 {
		t.Log("Recharge button found")
		_ = rechargeBtn.First().Click()
		time.Sleep(500 * time.Millisecond)
		screenshot(t, page, "config-recharge-modal")

		// Check modal appeared
		modal := page.Locator(".modal")
		if modalCount, _ := modal.Count(); modalCount > 0 {
			t.Log("Recharge modal appeared")

			// Check for amount input
			amountInput := modal.Locator("input[name='amount'], input[type='number']")
			if amountInputCount, _ := amountInput.Count(); amountInputCount > 0 {
				t.Log("Amount input found")
			}

			// Check for payment method selection
			paymentSelect := modal.Locator("select[name='payment'], select[name='method']")
			if paymentSelectCount, _ := paymentSelect.Count(); paymentSelectCount > 0 {
				t.Log("Payment method selection found")
			}

			// Close modal
			closeBtn := modal.Locator(".modal-close, button:has-text('取消'), button:has-text('Cancel')")
			if closeBtnCount, _ := closeBtn.Count(); closeBtnCount > 0 {
				_ = closeBtn.First().Click()
			}
		}
	}
}

// ============================================================================
// AUDIT LOGS AND RECORDS TESTS
// ============================================================================

// TestAuditLogs tests the audit logging system
func TestAuditLogs(t *testing.T) {
	page, cleanup := newPage(t)
	defer cleanup()

	// Navigate to admin audit logs page
	_, err := page.Goto(baseURL + "/admin/audit-logs")
	if err != nil {
		t.Fatalf("Failed to navigate: %v", err)
	}

	screenshot(t, page, "audit-logs")

	// Check audit log table
	logRows := page.Locator(".data-table tbody tr")
	logCount, _ := logRows.Count()
	t.Logf("Found %d audit log entries", logCount)

	// Check for required audit log columns
	requiredColumns := []string{"时间", "Time", "用户", "User", "操作", "Action", "类型", "Type"}
	for _, col := range requiredColumns {
		colHeader := page.Locator(".data-table th:has-text('" + col + "')")
		if colCount, _ := colHeader.Count(); colCount > 0 {
			t.Logf("Audit log column '%s' found", col)
		}
	}

	// Check for filter options
	filterSelects := page.Locator(".form-select, select")
	filterCount, _ := filterSelects.Count()
	t.Logf("Found %d filter dropdowns", filterCount)

	// Check for date range filter
	dateInputs := page.Locator("input[type='date']")
	dateCount, _ := dateInputs.Count()
	t.Logf("Found %d date inputs", dateCount)
}

// TestUsageRecords tests the usage records/statistics
func TestUsageRecords(t *testing.T) {
	page, cleanup := newPage(t)
	defer cleanup()

	// Navigate to admin usage/monitor page
	_, err := page.Goto(baseURL + "/admin/monitor")
	if err != nil {
		t.Logf("Failed to navigate to monitor: %v", err)
	}

	// Try downstream monitor
	_, err = page.Goto(baseURL + "/admin/monitor/downstream")
	if err != nil {
		t.Logf("Failed to navigate to downstream monitor: %v", err)
	} else {
		screenshot(t, page, "usage-downstream")
	}

	// Navigate to user usage page
	_, err = page.Goto(baseURL + "/usage")
	if err != nil {
		t.Logf("Failed to navigate to usage: %v", err)
	} else {
		screenshot(t, page, "usage-user")
	}

	// Check usage table
	usageRows := page.Locator(".data-table tbody tr")
	usageCount, _ := usageRows.Count()
	t.Logf("Found %d usage records", usageCount)

	// Check for required usage columns
	requiredColumns := []string{"模型", "Model", "输入", "Input", "输出", "Output", "费用", "Cost"}
	for _, col := range requiredColumns {
		colHeader := page.Locator(".data-table th:has-text('" + col + "')")
		if colCount, _ := colHeader.Count(); colCount > 0 {
			t.Logf("Usage column '%s' found", col)
		}
	}
}

// TestAllAuditRelatedPages tests all pages that should have audit functionality
func TestAllAuditRelatedPages(t *testing.T) {
	auditPages := []struct {
		name        string
		path        string
		description string
	}{
		{"audit-logs", "/admin/audit-logs", "System audit logs"},
		{"admin-keys", "/admin/keys", "Key management with audit"},
		{"admin-users", "/admin/users", "User management with audit"},
		{"admin-groups", "/admin/groups", "Group management with audit"},
		{"admin-providers", "/admin/providers", "Provider management with audit"},
		{"admin-models", "/admin/models", "Model management with audit"},
		{"admin-keypool", "/admin/keypool", "KeyPool management with audit"},
		{"admin-orders", "/admin/orders", "Order management with audit"},
	}

	page, cleanup := newPage(t)
	defer cleanup()

	for _, p := range auditPages {
		t.Run(p.name, func(t *testing.T) {
			_, err := page.Goto(baseURL + p.path)
			if err != nil {
				t.Logf("Failed to navigate to %s: %v", p.path, err)
				return
			}

			screenshot(t, page, "audit-"+p.name)

			// Check for action buttons that should trigger audit
			actionBtns := page.Locator(".btn:has-text('删除'), .btn:has-text('Delete'), .btn:has-text('编辑'), .btn:has-text('Edit'), .btn:has-text('创建'), .btn:has-text('Create')")
			actionCount, _ := actionBtns.Count()
			t.Logf("%s: Found %d action buttons (should trigger audit)", p.name, actionCount)

			// Check for status badges
			badges := page.Locator(".badge")
			badgeCount, _ := badges.Count()
			t.Logf("%s: Found %d status badges", p.name, badgeCount)
		})
	}
}

// ============================================================================
// HELPER FUNCTIONS
// ============================================================================

func contains(s, substr string) bool {
	return len(s) >= len(substr) && s[:len(substr)] == substr || len(s) > len(substr) && contains(s[1:], substr)
}

func findEmojis(text string) []string {
	// Common emoji ranges
	emojis := []string{}
	for i := 0; i < len(text); i++ {
		if i < len(text)-1 && text[i] >= 0xD8 && text[i] <= 0xDB && text[i+1] >= 0xDC && text[i+1] <= 0xDF {
			emojis = append(emojis, text[i:i+2])
		}
	}
	return emojis
}

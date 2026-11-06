//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/playwright-community/playwright-go"
)

const (
	baseURL        = "http://localhost:3001"
	screenshotsDir = "screenshots"
)

var pw *playwright.Playwright
var browser playwright.Browser

func TestMain(m *testing.M) {
	var err error
	pw, err = playwright.Run()
	if err != nil {
		panic("Failed to start Playwright: " + err.Error())
	}

	browser, err = pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(true),
	})
	if err != nil {
		panic("Failed to launch browser: " + err.Error())
	}

	// Create screenshots directory
	_ = os.MkdirAll(filepath.Join(screenshotsDir), 0755)

	code := m.Run()

	_ = browser.Close()
	_ = pw.Stop()
	os.Exit(code)
}

func newPage(t *testing.T) (playwright.Page, func()) {
	context, err := browser.NewContext(playwright.BrowserNewContextOptions{
		Viewport: &playwright.Size{
			Width:  1280,
			Height: 720,
		},
	})
	if err != nil {
		t.Fatalf("Failed to create context: %v", err)
	}

	page, err := context.NewPage()
	if err != nil {
		t.Fatalf("Failed to create page: %v", err)
	}

	return page, func() {
		_ = context.Close()
	}
}

func screenshot(t *testing.T, page playwright.Page, name string) {
	path := filepath.Join(screenshotsDir, name+".png")
	_, err := page.Screenshot(playwright.PageScreenshotOptions{
		Path: playwright.String(path),
	})
	if err != nil {
		t.Logf("Failed to take screenshot: %v", err)
	} else {
		t.Logf("Screenshot saved: %s", path)
	}
}

// TestLoginPage tests the login page layout and functionality
func TestLoginPage(t *testing.T) {
	page, cleanup := newPage(t)
	defer cleanup()

	// Navigate to login page
	_, err := page.Goto(baseURL + "/login")
	if err != nil {
		t.Fatalf("Failed to navigate: %v", err)
	}

	// Take screenshot
	screenshot(t, page, "login-page")

	// Verify page title
	title, _ := page.Title()
	t.Logf("Page title: %s", title)

	// Check for login form elements
	emailInput := page.Locator("input[name='email'], input[type='email']")
	if count, _ := emailInput.Count(); count == 0 {
		t.Error("Email input not found")
	}

	passwordInput := page.Locator("input[name='password'], input[type='password']")
	if count, _ := passwordInput.Count(); count == 0 {
		t.Error("Password input not found")
	}

	submitBtn := page.Locator("button[type='submit']")
	if count, _ := submitBtn.Count(); count == 0 {
		t.Error("Submit button not found")
	}

	// Test form validation - empty submit
	_ = submitBtn.Click()
	time.Sleep(500 * time.Millisecond)

	// Check for error message or validation
	errorVisible, _ := page.Locator(".error, .alert-danger, [class*='error']").IsVisible()
	t.Logf("Error message visible after empty submit: %v", errorVisible)
}

// TestDashboardLayout tests the dashboard page layout
func TestDashboardLayout(t *testing.T) {
	page, cleanup := newPage(t)
	defer cleanup()

	// Navigate to dashboard (will redirect to login if not authenticated)
	_, err := page.Goto(baseURL + "/dashboard")
	if err != nil {
		t.Fatalf("Failed to navigate: %v", err)
	}

	// Take screenshot
	screenshot(t, page, "dashboard-page")

	// Check if redirected to login or showing dashboard
	currentURL := page.URL()
	t.Logf("Current URL: %s", currentURL)

	// Check for navigation elements
	nav := page.Locator("nav, .navbar, .sidebar")
	if count, _ := nav.Count(); count > 0 {
		t.Log("Navigation found")
		screenshot(t, page, "dashboard-nav")
	}
}

// TestAPIKeysPage tests the API keys page
func TestAPIKeysPage(t *testing.T) {
	page, cleanup := newPage(t)
	defer cleanup()

	_, err := page.Goto(baseURL + "/keys")
	if err != nil {
		t.Fatalf("Failed to navigate: %v", err)
	}

	screenshot(t, page, "apikeys-page")

	// Check for create key button
	createBtn := page.Locator("button:has-text('Create'), button:has-text('New'), a:has-text('Create')")
	if count, _ := createBtn.Count(); count > 0 {
		t.Log("Create key button found")
	}
}

// TestResponsiveLayout tests responsive design at different viewport sizes
func TestResponsiveLayout(t *testing.T) {
	viewports := []struct {
		name   string
		width  int
		height int
	}{
		{"desktop", 1920, 1080},
		{"laptop", 1366, 768},
		{"tablet", 768, 1024},
		{"mobile", 375, 667},
	}

	for _, vp := range viewports {
		t.Run(vp.name, func(t *testing.T) {
			context, err := browser.NewContext(playwright.BrowserNewContextOptions{
				Viewport: &playwright.Size{
					Width:  vp.width,
					Height: vp.height,
				},
			})
			if err != nil {
				t.Fatalf("Failed to create context: %v", err)
			}
			defer func() { _ = context.Close() }()

			page, err := context.NewPage()
			if err != nil {
				t.Fatalf("Failed to create page: %v", err)
			}

			_, err = page.Goto(baseURL + "/")
			if err != nil {
				t.Fatalf("Failed to navigate: %v", err)
			}

			screenshot(t, page, "homepage-"+vp.name)

			// Check for horizontal scroll (layout issue indicator)
			scrollWidth, _ := page.Evaluate("() => document.body.scrollWidth")
			clientWidth, _ := page.Evaluate("() => document.body.clientWidth")

			// Handle both int and float64 types
			var sw, cw float64
			if v, ok := scrollWidth.(int); ok {
				sw = float64(v)
			} else if v, ok := scrollWidth.(float64); ok {
				sw = v
			}
			if v, ok := clientWidth.(int); ok {
				cw = float64(v)
			} else if v, ok := clientWidth.(float64); ok {
				cw = v
			}

			if sw > cw+10 {
				t.Logf("Warning: Horizontal scroll detected at %s viewport", vp.name)
			}
		})
	}
}

// TestAdminDashboard tests admin dashboard elements
func TestAdminDashboard(t *testing.T) {
	page, cleanup := newPage(t)
	defer cleanup()

	_, err := page.Goto(baseURL + "/admin")
	if err != nil {
		t.Fatalf("Failed to navigate: %v", err)
	}

	screenshot(t, page, "admin-dashboard")

	// Check for admin menu items
	adminLinks := []string{"Users", "Keys", "Providers", "Settings"}
	for _, link := range adminLinks {
		locator := page.Locator("a:has-text('" + link + "'), button:has-text('" + link + "')")
		if count, _ := locator.Count(); count > 0 {
			t.Logf("Admin link '%s' found", link)
		}
	}
}

// TestFormInteractions tests form interactions
func TestFormInteractions(t *testing.T) {
	page, cleanup := newPage(t)
	defer cleanup()

	_, err := page.Goto(baseURL + "/login")
	if err != nil {
		t.Fatalf("Failed to navigate: %v", err)
	}

	// Test input focus
	emailInput := page.Locator("input[name='email'], input[type='email']").First()
	if count, _ := emailInput.Count(); count > 0 {
		_ = emailInput.Click()
		screenshot(t, page, "email-input-focused")

		// Type test email
		_ = emailInput.Fill("test@example.com")
		value, _ := emailInput.InputValue()
		if value != "test@example.com" {
			t.Errorf("Email input value mismatch: got %s", value)
		}
	}

	// Test password input
	passwordInput := page.Locator("input[name='password'], input[type='password']").First()
	if count, _ := passwordInput.Count(); count > 0 {
		_ = passwordInput.Fill("TestPassword123!")
		screenshot(t, page, "password-filled")
	}
}

// TestAccessibility checks basic accessibility
func TestAccessibility(t *testing.T) {
	page, cleanup := newPage(t)
	defer cleanup()

	_, err := page.Goto(baseURL + "/")
	if err != nil {
		t.Fatalf("Failed to navigate: %v", err)
	}

	// Check for images without alt text
	images := page.Locator("img")
	count, _ := images.Count()
	t.Logf("Found %d images", count)

	imagesWithoutAlt := page.Locator("img:not([alt])")
	badCount, _ := imagesWithoutAlt.Count()
	if badCount > 0 {
		t.Logf("Warning: %d images without alt text", badCount)
	}

	// Check for buttons without accessible name
	buttons := page.Locator("button")
	btnCount, _ := buttons.Count()
	t.Logf("Found %d buttons", btnCount)

	// Check page language
	lang, _ := page.Evaluate("() => document.documentElement.lang")
	if lang == "" {
		t.Log("Warning: No language attribute on HTML element")
	} else {
		t.Logf("Page language: %s", lang)
	}
}

// testEmail and testPassword are credentials for e2e testing.
// Set via E2E_EMAIL / E2E_PASSWORD env vars, or defaults to admin account.
var (
	testEmail    = envOr("E2E_EMAIL", "admin@example.com")
	testPassword = envOr("E2E_PASSWORD", "Admin@123456!")
)

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// loginIfNeeded checks if the page was redirected to login, and if so performs login.
// After successful login, it navigates to targetURL (the page we originally wanted).
func loginIfNeeded(t *testing.T, page playwright.Page, targetURL ...string) {
	t.Helper()
	url := page.URL()
	if !strings.Contains(url, "/login") {
		return
	}
	t.Logf("Redirected to login, authenticating with %s...", testEmail)
	_ = page.Locator("input[name='email']").Fill(testEmail)
	_ = page.Locator("input[name='password']").Fill(testPassword)
	_, _ = page.Evaluate("() => document.querySelector('form[action=\"/login\"]').submit()", nil)
	time.Sleep(2 * time.Second)
	newURL := page.URL()
	if strings.Contains(newURL, "/login") {
		flash, _ := page.Locator(".flash").TextContent()
		t.Fatalf("Login failed, still on login page. URL=%s flash=%q", newURL, flash)
	}
	// Re-navigate to original target if provided and different from current page
	if len(targetURL) > 0 && targetURL[0] != "" && !strings.HasSuffix(newURL, targetURL[0]) {
		_, _ = page.Goto(baseURL + targetURL[0])
		time.Sleep(500 * time.Millisecond)
	}
	t.Logf("Logged in, now at: %s", page.URL())
}

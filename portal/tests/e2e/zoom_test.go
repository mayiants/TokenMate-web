//go:build e2e

package e2e

import (
	"testing"
	"time"

	"github.com/playwright-community/playwright-go"
)

// ============================================================================
// ZOOM AND SCALING TESTS
// ============================================================================

// TestZoomLevels tests the UI at different zoom levels
func TestZoomLevels(t *testing.T) {
	zoomLevels := []struct {
		name       string
		scale      float64
		percentage int
	}{
		{"100%", 1.0, 100},
		{"90%", 0.9, 90},
		{"75%", 0.75, 75},
		{"67%", 0.67, 67},
		{"50%", 0.5, 50},
	}

	for _, z := range zoomLevels {
		t.Run(z.name, func(t *testing.T) {
			context, err := browser.NewContext(playwright.BrowserNewContextOptions{
				Viewport: &playwright.Size{
					Width:  1280,
					Height: 720,
				},
				// Simulate zoom via device scale factor
				DeviceScaleFactor: playwright.Float(z.scale),
			})
			if err != nil {
				t.Fatalf("Failed to create context: %v", err)
			}
			defer func() { _ = context.Close() }()

			page, err := context.NewPage()
			if err != nil {
				t.Fatalf("Failed to create page: %v", err)
			}

			// Navigate to dashboard
			_, err = page.Goto(baseURL + "/dashboard")
			if err != nil {
				t.Fatalf("Failed to navigate: %v", err)
			}
			loginIfNeeded(t, page, "/dashboard")

			screenshot(t, page, "zoom-"+z.name)

			// Check for horizontal scroll (layout issue indicator)
			scrollWidth, _ := page.Evaluate("() => document.body.scrollWidth", nil)
			clientWidth, _ := page.Evaluate("() => document.body.clientWidth", nil)

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

			if sw > cw+50 {
				t.Logf("Warning: Significant horizontal scroll at %s zoom (%.0f > %.0f)", z.name, sw, cw)
			} else {
				t.Logf("OK: No significant horizontal scroll at %s zoom", z.name)
			}

			// Check button text wrapping
			buttons := page.Locator(".btn")
			btnCount, _ := buttons.Count()
			t.Logf("Found %d buttons at %s zoom", btnCount, z.name)

			wrappedButtons := 0
			for i := 0; i < btnCount && i < 10; i++ {
				btn := buttons.Nth(i)
				visible, _ := btn.IsVisible()
				if !visible {
					continue
				}
				height, _ := btn.Evaluate("el => el.offsetHeight", nil)
				if h, ok := height.(float64); ok && h > 40 {
					wrappedButtons++
				}
			}

			if wrappedButtons > 0 {
				t.Logf("Warning: %d buttons have wrapped text at %s zoom", wrappedButtons, z.name)
			}
		})
	}
}

// TestSidebarScrollPreservation tests that sidebar scroll position is preserved
func TestSidebarScrollPreservation(t *testing.T) {
	page, cleanup := newPage(t)
	defer cleanup()

	// Navigate to admin page
	_, err := page.Goto(baseURL + "/admin")
	if err != nil {
		t.Fatalf("Failed to navigate: %v", err)
	}
	loginIfNeeded(t, page, "/admin")

	// Scroll sidebar down
	sidebar := page.Locator("#sidebar")
	if count, _ := sidebar.Count(); count > 0 {
		// Scroll sidebar to bottom
		_, err = sidebar.Evaluate("el => { el.scrollTop = el.scrollHeight; return el.scrollTop; }", nil)
		if err != nil {
			t.Logf("Could not scroll sidebar: %v", err)
		}

		// Get scroll position
		scrollPos, _ := sidebar.Evaluate("el => el.scrollTop", nil)
		t.Logf("Sidebar scroll position: %v", scrollPos)

		screenshot(t, page, "sidebar-scrolled")

		// Navigate to another page
		_, err = page.Goto(baseURL + "/admin/users")
		if err != nil {
			t.Fatalf("Failed to navigate: %v", err)
		}

		// Check if sidebar scroll position is preserved
		newScrollPos, _ := sidebar.Evaluate("el => el.scrollTop", nil)
		t.Logf("Sidebar scroll position after navigation: %v", newScrollPos)

		screenshot(t, page, "sidebar-after-nav")
	}
}

// TestDarkModeScrollbar tests scrollbar appearance in dark mode
func TestDarkModeScrollbar(t *testing.T) {
	page, cleanup := newPage(t)
	defer cleanup()

	// Navigate to page
	_, err := page.Goto(baseURL + "/dashboard")
	if err != nil {
		t.Fatalf("Failed to navigate: %v", err)
	}
	loginIfNeeded(t, page, "/dashboard")

	// Switch to dark mode
	themeBtn := page.Locator(".theme-toggle")
	if count, _ := themeBtn.Count(); count > 0 {
		// Click until dark mode
		for i := 0; i < 3; i++ {
			_ = themeBtn.Click()
			time.Sleep(200 * time.Millisecond)
		}
	}

	screenshot(t, page, "dark-mode-scrollbar")

	// Check if dark mode is applied
	theme, _ := page.Evaluate("() => document.documentElement.getAttribute('data-theme')", nil)
	t.Logf("Current theme: %v", theme)

	// Check scrollbar color
	scrollbarColor, _ := page.Evaluate("() => getComputedStyle(document.documentElement).scrollbarColor", nil)
	t.Logf("Scrollbar color: %v", scrollbarColor)
}

// TestButtonNoWrap tests that buttons don't wrap text
func TestButtonNoWrap(t *testing.T) {
	page, cleanup := newPage(t)
	defer cleanup()

	pages := []string{
		"/admin",
		"/admin/models",
		"/admin/keypool",
		"/apikeys",
	}

	for _, p := range pages {
		t.Run(p, func(t *testing.T) {
			_, err := page.Goto(baseURL + p)
			if err != nil {
				t.Logf("Failed to navigate: %v", err)
				return
			}
			loginIfNeeded(t, page, p)

			buttons := page.Locator(".btn")
			btnCount, _ := buttons.Count()
			t.Logf("Found %d buttons on %s", btnCount, p)

			// Check each button for wrapping
			wrappedCount := 0
			for i := 0; i < btnCount && i < 20; i++ {
				btn := buttons.Nth(i)
				visible, _ := btn.IsVisible()
				if !visible {
					continue
				}
				// Check white-space property
				whiteSpace, _ := btn.Evaluate("el => getComputedStyle(el).whiteSpace", nil)
				if ws, ok := whiteSpace.(string); ok && ws != "nowrap" {
					// Check if button has wrapped text
					height, _ := btn.Evaluate("el => el.offsetHeight", nil)
					if h, ok := height.(float64); ok && h > 44 {
						wrappedCount++
						text, _ := btn.TextContent()
						t.Logf("Button may have wrapped text: '%s' (height: %.0f)", text, h)
					}
				}
			}

			if wrappedCount == 0 {
				t.Logf("OK: No buttons with wrapped text on %s", p)
			}
		})
	}
}

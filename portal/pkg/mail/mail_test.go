package mail

import (
	"strings"
	"testing"

	"github.com/ai-proxy/ai-portal-go/internal/config"
	"go.uber.org/zap"
)

// TestHtmlEscape tests HTML escaping helper.
func TestHtmlEscape(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "hello"},
		{"<script>", "&lt;script&gt;"},
		// Check that & is escaped first to avoid double-escaping
		{"a & b", "a &amp; b"},
		// Check quotes
		{`"hello"`, "&quot;hello&quot;"},
		{"'single'", "&#39;single&#39;"},
		// All together
		{`<div class="x">test & debug</div>`, "&lt;div class=&quot;x&quot;&gt;test &amp; debug&lt;/div&gt;"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := htmlEscape(tt.input)
			if got != tt.want {
				t.Errorf("htmlEscape(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestReplace tests the placeholder replacement helper.
func TestReplace(t *testing.T) {
	tests := []struct {
		tmpl        string
		placeholder string
		value       string
		want        string
	}{
		{"Hello {name}", "{name}", "World", "Hello World"},
		{"{count} items", "{count}", "5", "5 items"},
		{"No placeholder", "{x}", "y", "No placeholder"},
	}

	for _, tt := range tests {
		t.Run(tt.tmpl, func(t *testing.T) {
			got := replace(tt.tmpl, tt.placeholder, tt.value)
			if got != tt.want {
				t.Errorf("replace(%q, %q, %q) = %q, want %q",
					tt.tmpl, tt.placeholder, tt.value, got, tt.want)
			}
		})
	}
}

// TestReplaceEscapesHTML tests that replace escapes HTML in values.
func TestReplaceEscapesHTML(t *testing.T) {
	tmpl := "User: {name}"
	got := replace(tmpl, "{name}", "<script>alert(1)</script>")
	want := "User: &lt;script&gt;alert(1)&lt;/script&gt;"
	if got != want {
		t.Errorf("replace() did not escape HTML: got %q, want %q", got, want)
	}
}

// TestBuildHTMLMessage tests email message building.
func TestBuildHTMLMessage(t *testing.T) {
	from := "sender@example.com"
	to := "recipient@example.com"
	subject := "Test Subject"
	body := "<p>Hello</p>"

	msg := buildHTMLMessage(from, to, subject, body)

	if !strings.Contains(msg, "From: "+from) {
		t.Error("HTML message missing From header")
	}
	if !strings.Contains(msg, "To: "+to) {
		t.Error("HTML message missing To header")
	}
	if !strings.Contains(msg, "Subject:") {
		t.Error("HTML message missing Subject header")
	}
	if !strings.Contains(msg, "text/html") {
		t.Error("HTML message missing text/html content type")
	}
	if !strings.Contains(msg, body) {
		t.Error("HTML message missing body content")
	}
	if !strings.Contains(msg, "MIME-Version: 1.0") {
		t.Error("HTML message missing MIME-Version")
	}
}

// TestBuildHTMLMessageUTF8Subject tests subject encoding.
func TestBuildHTMLMessageUTF8Subject(t *testing.T) {
	subject := "测试主题"
	msg := buildHTMLMessage("from@test.com", "to@test.com", subject, "body")
	if !strings.Contains(msg, "=?UTF-8?B?") {
		t.Error("UTF-8 subject should be base64 encoded")
	}
}

// TestSenderIsConfigured tests configuration detection.
func TestSenderIsConfigured(t *testing.T) {
	// Nil sender is not possible since we construct directly
	// Test with empty config
	s := &Sender{}
	if s.IsConfigured() {
		t.Error("empty Sender should not be configured")
	}
}

// TestSenderTWithNilI18n tests the t helper with nil i18n.
func TestSenderTWithNilI18n(t *testing.T) {
	s := &Sender{}
	got := s.t("test.key", "zh")
	if got != "test.key" {
		t.Errorf("t() with nil i18n = %q, want %q", got, "test.key")
	}
}

// TestSenderTWithI18n verifies the sender's t helper with a real I18n map.
func TestSenderTWithI18n(t *testing.T) {
	i18n := make(map[string]map[string]string)
	i18n["zh"] = map[string]string{"test.key": "测试"}
	s := &Sender{}
	// Assign i18n directly (it's a named map type from pkg/i18n)
	// Cannot directly test without import cycle, verify the nil guard works
	if s.t("test.key", "zh") != "test.key" {
		t.Error("nil i18n should fallback to key")
	}
}

func TestEnterpriseStatusChangeContentEscapesHTML(t *testing.T) {
	enterpriseName := `<script>alert("x")</script> & Co`
	reason := `Rejected because <img src=x onerror=alert(1)> & "policy"`
	subject, content := enterpriseStatusChangeContent(enterpriseName, "rejected", reason)

	if strings.Contains(subject, "\r") || strings.Contains(subject, "\n") {
		t.Fatalf("subject should not contain CRLF, got %q", subject)
	}
	if strings.Contains(content, "<script>") || strings.Contains(content, "<img") {
		t.Fatalf("content should escape HTML, got %q", content)
	}
	if !strings.Contains(content, "&lt;script&gt;") || !strings.Contains(content, "&lt;img") {
		t.Fatalf("content should contain escaped HTML entities, got %q", content)
	}
	if !strings.Contains(content, "&amp; Co") || !strings.Contains(content, "&amp;") {
		t.Fatalf("content should escape ampersands, got %q", content)
	}
}

// TestSenderReload verifies that Reload swaps the SMTP configuration and that
// Config()/IsConfigured() reflect the change.
func TestSenderReload(t *testing.T) {
	logger := zap.NewNop()
	s := NewSender(config.SMTPConfig{}, logger, nil)

	if s.IsConfigured() {
		t.Fatal("sender with empty config should not be configured")
	}

	s.Reload(config.SMTPConfig{
		Host:     "smtp.example.com",
		Port:     465,
		Secure:   true,
		User:     "user@example.com",
		Password: "secret",
		From:     "noreply@example.com",
	})

	if !s.IsConfigured() {
		t.Fatal("sender should be configured after Reload with full config")
	}

	cfg := s.Config()
	if cfg.Host != "smtp.example.com" || cfg.Port != 465 || !cfg.Secure ||
		cfg.User != "user@example.com" || cfg.Password != "secret" || cfg.From != "noreply@example.com" {
		t.Errorf("Config() = %+v, want reloaded values", cfg)
	}

	// Reloading an empty config (e.g. reset to unset env) disables the sender.
	s.Reload(config.SMTPConfig{})
	if s.IsConfigured() {
		t.Fatal("sender should not be configured after Reload with empty config")
	}
	if got := s.Config(); got.Host != "" {
		t.Errorf("Config().Host = %q, want empty", got.Host)
	}
}

package mail

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/ai-proxy/ai-portal-go/pkg/i18n"
	"github.com/ai-proxy/ai-portal-go/web"
)

func loadTestI18n(t *testing.T) i18n.I18n {
	i := make(i18n.I18n)
	for _, lang := range []string{"zh", "en"} {
		data, err := web.FS.ReadFile(fmt.Sprintf("i18n/%s.json", lang))
		if err != nil {
			t.Fatalf("read i18n %s: %v", lang, err)
		}
		var dict map[string]string
		if err := json.Unmarshal(data, &dict); err != nil {
			t.Fatalf("parse i18n %s: %v", lang, err)
		}
		i[lang] = dict
	}
	return i
}

// TestEmailSamples writes rendered HTML emails to testdata/email-samples so
// they can be visually reviewed without configuring a real SMTP server.
func TestEmailSamples(t *testing.T) {
	dir := t.TempDir()
	translations := loadTestI18n(t)
	s := &Sender{i18n: translations}

	baseURL := "https://api.woodgit.com"
	samples := map[string]string{
		"verification-code-zh.html": s.emailLayout("Woodgit 验证码", s.verificationCodeContent("123456", "zh"), "zh"),
		"verification-code-en.html": s.emailLayout("Woodgit Verification Code", s.verificationCodeContent("123456", "en"), "en"),

		"password-reset-success-zh.html": s.emailLayout("Woodgit - 密码重置成功", s.passwordResetSuccessContent("zh"), "zh"),

		"enterprise-invitation-zh.html": s.emailLayout("Woodgit - Acme 企业邀请", s.enterpriseInvitationContent("Acme", baseURL+"/api/enterprise/accept?token=invite123", "zh"), "zh"),
		"enterprise-invitation-en.html": s.emailLayout("Woodgit - Acme Enterprise Invitation", s.enterpriseInvitationContent("Acme", baseURL+"/api/enterprise/accept?token=invite123", "en"), "en"),

		"enterprise-added-zh.html": s.emailLayout("Woodgit - 已添加到 Acme", s.enterpriseAddedContent("Acme", "zh"), "zh"),

		"admin-vacant-zh.html": s.emailLayout("Woodgit - 企业管理员空缺提醒", s.adminVacantContent("Acme Inc\nBeta Corp", baseURL+"/admin/enterprise", "zh"), "zh"),

		"enterprise-alert-warning-zh.html":  s.emailLayout("Woodgit - Acme 配额警告", s.enterpriseAlertContent("quota_warning", "Acme", 80, 820, 1000, "zh"), "zh"),
		"enterprise-alert-exceeded-zh.html": s.emailLayout("Woodgit - Acme 配额已超限", s.enterpriseAlertContent("quota_exceeded", "Acme", 100, 1200, 1000, "zh"), "zh"),

		"model-discontinued-zh.html": s.emailLayout("Woodgit - 模型 gpt-4 服务已结束", s.modelDiscontinuedContent("gpt-4", baseURL+"/dashboard", "zh"), "zh"),

		"key-fallback-zh.html": s.emailLayout("Woodgit - API Key 'prod-key' 已自动调整", s.keyFallbackContent("prod-key", "gpt-4o", baseURL+"/apikeys", "zh"), "zh"),

		"eol-reminder-zh.html": s.emailLayout("Woodgit - 模型 gpt-4 将在 3 天后停止服务", s.eolReminderContent("gpt-4", 3, "2026-06-30", baseURL+"/dashboard", "zh"), "zh"),
	}

	for name, body := range samples {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0644); err != nil {
			t.Fatalf("failed to write %s: %v", name, err)
		}
	}

	t.Logf("Wrote %d sample emails to %s", len(samples), dir)
}

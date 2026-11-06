package mail

import (
	"fmt"
	"strings"
	"time"
)

// emailLayout wraps content in a clean, responsive HTML email shell.
// The style is intentionally minimal: a centered card, clear typography,
// one primary action if needed, and a small footer. No extra decoration.
func (s *Sender) emailLayout(title, content, locale string) string {
	lang := "en"
	if locale == "zh" {
		lang = "zh-CN"
	}

	tmpl := `<!DOCTYPE html>
<html lang="{{lang}}">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>{{title}}</title>
</head>
<body style="margin:0;padding:0;background-color:#f4f6f8;">
<table role="presentation" width="100%" cellspacing="0" cellpadding="0" border="0">
<tr>
<td align="center" style="padding:40px 16px;">
<table role="presentation" width="600" cellspacing="0" cellpadding="0" border="0" style="max-width:600px;width:100%;background:#ffffff;border-radius:8px;box-shadow:0 1px 3px rgba(0,0,0,0.08);overflow:hidden;">
<tr>
<td style="padding:28px 32px 20px;border-bottom:1px solid #f0f0f0;">
<div style="font-size:20px;font-weight:700;color:#3b82f6;letter-spacing:-0.5px;">Woodgit</div>
</td>
</tr>
<tr>
<td style="padding:32px;">
<h1 style="margin:0 0 20px;font-size:22px;font-weight:600;color:#111827;line-height:1.35;">{{title}}</h1>
{{content}}
</td>
</tr>
<tr>
<td style="padding:24px 32px;background:#f9fafb;border-top:1px solid #f0f0f0;font-size:12px;color:#6b7280;line-height:1.6;">
{{footerNote}}<br>
<span style="color:#9ca3af;">Woodgit © {{year}}</span>
</td>
</tr>
</table>
</td>
</tr>
</table>
</body>
</html>`

	out := strings.ReplaceAll(tmpl, "{{title}}", htmlEscape(title))
	out = strings.ReplaceAll(out, "{{content}}", content)
	out = strings.ReplaceAll(out, "{{footerNote}}", htmlEscape(s.t("mail.footer_note", locale)))
	out = strings.ReplaceAll(out, "{{year}}", fmt.Sprintf("%d", time.Now().Year()))
	out = strings.ReplaceAll(out, "{{lang}}", lang)
	return out
}

// buttonBlock returns a centered primary button.
func buttonBlock(url, text string) string {
	return fmt.Sprintf(
		`<p style="margin:28px 0;text-align:center;">
<a href="%s" style="display:inline-block;padding:12px 28px;background:#3b82f6;color:#ffffff;text-decoration:none;border-radius:6px;font-size:15px;font-weight:500;">%s</a>
</p>`,
		htmlEscape(url), htmlEscape(text),
	)
}

// fallbackLinkBlock returns "or copy this link" text with a wrapped URL.
func fallbackLinkBlock(prefix, url string) string {
	return fmt.Sprintf(
		`<p style="margin:16px 0 0;color:#6b7280;font-size:13px;line-height:1.5;">
%s<br>
<a href="%s" style="color:#3b82f6;word-break:break-all;">%s</a>
</p>`,
		htmlEscape(prefix), htmlEscape(url), htmlEscape(url),
	)
}

func (s *Sender) verificationCodeContent(code, locale string) string {
	return replace(s.t("mail.verification_code.body", locale), "{code}", code)
}

func (s *Sender) testEmailContent(locale string) string {
	return s.t("mail.test.body", locale)
}

func (s *Sender) gdprExportReadyContent(locale string) string {
	return s.t("mail.gdpr_export_ready.body", locale)
}

func (s *Sender) passwordResetSuccessContent(locale string) string {
	return s.t("mail.password_reset.success.body", locale)
}

func (s *Sender) enterpriseInvitationContent(enterpriseName, acceptURL, locale string) string {
	body := s.t("mail.enterprise.invitation.body", locale)
	body = replace(body, "{enterpriseName}", enterpriseName)
	body = strings.ReplaceAll(body, "{button}", buttonBlock(acceptURL, s.t("mail.button.accept_invitation", locale)))
	body = strings.ReplaceAll(body, "{fallback}", fallbackLinkBlock(s.t("mail.fallback.copy_link", locale), acceptURL))
	return body
}

func (s *Sender) enterpriseAddedContent(enterpriseName, locale string) string {
	return replace(s.t("mail.enterprise.added.body", locale), "{enterpriseName}", enterpriseName)
}

func (s *Sender) adminVacantContent(summary, dashboardURL, locale string) string {
	dashboardLink := fmt.Sprintf(`<a href="%s" style="color:#3b82f6;">%s</a>`, htmlEscape(dashboardURL), htmlEscape(s.t("mail.admin.vacant.dashboard_link_text", locale)))
	body := s.t("mail.admin.vacant.body", locale)
	body = strings.ReplaceAll(body, "{summary}", htmlEscape(summary))
	body = strings.ReplaceAll(body, "{dashboardLink}", dashboardLink)
	return body
}

func (s *Sender) enterpriseAlertContent(alertType, enterpriseName string, threshold, current, limit float64, locale string) string {
	var message string
	switch alertType {
	case "quota_warning":
		message = replace(replace(s.t("mail.enterprise.alert.message.quota_warning", locale), "{enterpriseName}", enterpriseName), "{threshold}", fmt.Sprintf("%.0f", threshold))
	case "quota_exceeded":
		message = replace(replace(s.t("mail.enterprise.alert.message.quota_exceeded", locale), "{enterpriseName}", enterpriseName), "{threshold}", fmt.Sprintf("%.0f", threshold))
	default:
		message = replace(replace(s.t("mail.enterprise.alert.message.default", locale), "{enterpriseName}", enterpriseName), "{threshold}", fmt.Sprintf("%.0f", threshold))
	}

	usageLabel := s.t("mail.enterprise.alert.label.usage", locale)
	limitLabel := s.t("mail.enterprise.alert.label.limit", locale)
	thresholdLabel := s.t("mail.enterprise.alert.label.threshold", locale)
	note := s.t("mail.enterprise.alert.note", locale)

	return fmt.Sprintf(
		`<p style="margin:0 0 16px;color:#374151;line-height:1.6;">%s</p>
<table role="presentation" width="100%%" cellspacing="0" cellpadding="0" border="0" style="background:#f9fafb;border-radius:6px;margin:16px 0;">
<tr>
<td style="padding:12px 16px;border-bottom:1px solid #f0f0f0;font-size:13px;color:#6b7280;">%s</td>
<td style="padding:12px 16px;border-bottom:1px solid #f0f0f0;font-size:15px;font-weight:500;color:#111827;text-align:right;">%.2f</td>
</tr>
<tr>
<td style="padding:12px 16px;border-bottom:1px solid #f0f0f0;font-size:13px;color:#6b7280;">%s</td>
<td style="padding:12px 16px;border-bottom:1px solid #f0f0f0;font-size:15px;font-weight:500;color:#111827;text-align:right;">%.2f</td>
</tr>
<tr>
<td style="padding:12px 16px;font-size:13px;color:#6b7280;">%s</td>
<td style="padding:12px 16px;font-size:15px;font-weight:500;color:#111827;text-align:right;">%.0f%%</td>
</tr>
</table>
<p style="margin:16px 0 0;color:#6b7280;font-size:14px;line-height:1.5;">%s</p>`,
		message,
		htmlEscape(usageLabel), current,
		htmlEscape(limitLabel), limit,
		htmlEscape(thresholdLabel), threshold,
		htmlEscape(note),
	)
}

func (s *Sender) modelDiscontinuedContent(modelName, dashboardURL, locale string) string {
	body := s.t("mail.model.discontinued.body", locale)
	body = replace(body, "{modelName}", modelName)
	body = strings.ReplaceAll(body, "{button}", buttonBlock(dashboardURL, s.t("mail.button.go_to_dashboard", locale)))
	return body
}

func (s *Sender) keyFallbackContent(keyName, fallbackModel, dashboardURL, locale string) string {
	body := s.t("mail.key.fallback.body", locale)
	body = replace(replace(body, "{keyName}", keyName), "{fallbackModel}", fallbackModel)
	body = strings.ReplaceAll(body, "{button}", buttonBlock(dashboardURL, s.t("mail.button.go_to_dashboard", locale)))
	return body
}

func (s *Sender) eolReminderContent(modelName string, daysRemaining int, eolDate, dashboardURL, locale string) string {
	body := s.t("mail.eol_reminder.body", locale)
	body = replace(replace(replace(body, "{modelName}", modelName), "{eolDate}", eolDate), "{daysRemaining}", fmt.Sprintf("%d", daysRemaining))
	body = strings.ReplaceAll(body, "{button}", buttonBlock(dashboardURL, s.t("mail.button.go_to_dashboard", locale)))
	return body
}

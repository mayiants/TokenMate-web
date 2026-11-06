package mail

import (
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"sync"
	"time"

	"github.com/ai-proxy/ai-portal-go/internal/config"
	"github.com/ai-proxy/ai-portal-go/pkg/i18n"
	"github.com/ai-proxy/ai-portal-go/pkg/metrics"
	"go.uber.org/zap"
)

// Sender handles email sending
type Sender struct {
	mu     sync.RWMutex
	cfg    config.SMTPConfig
	logger *zap.Logger
	i18n   i18n.I18n
}

// SMTP timeouts. A single SMTP operation (dial, auth, data) that exceeds this
// window is aborted so request handlers do not hang indefinitely on a
// misbehaving mail server.
const (
	smtpDialTimeout    = 30 * time.Second
	smtpOpDeadline     = 2 * time.Minute
	smtpPlainDialLimit = 30 * time.Second
)

// NewSender creates a new mail sender
func NewSender(cfg config.SMTPConfig, logger *zap.Logger, i18n i18n.I18n) *Sender {
	return &Sender{cfg: cfg, logger: logger, i18n: i18n}
}

// Reload replaces the SMTP configuration at runtime (e.g. after an admin
// updates SMTP settings in the database).
func (s *Sender) Reload(cfg config.SMTPConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg = cfg
	s.logger.Info("SMTP configuration reloaded",
		zap.String("host", cfg.Host),
		zap.Int("port", cfg.Port),
		zap.Bool("secure", cfg.Secure),
		zap.String("user", cfg.User),
	)
}

// Config returns a snapshot of the current SMTP configuration.
func (s *Sender) Config() config.SMTPConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

// IsConfigured returns true if SMTP is configured
func (s *Sender) IsConfigured() bool {
	cfg := s.Config()
	return cfg.Host != "" && cfg.User != "" && cfg.Password != ""
}

// SendVerificationCode sends a verification code email
func (s *Sender) SendVerificationCode(to, code, locale string) error {
	subject := s.t("mail.subject.verification_code", locale)
	body := s.emailLayout(subject, s.verificationCodeContent(code, locale), locale)
	return s.sendHTML(to, subject, body)
}

// SendPasswordReset sends a password reset confirmation
func (s *Sender) SendPasswordReset(to, locale string) error {
	subject := s.t("mail.subject.password_reset_success", locale)
	body := s.emailLayout(subject, s.passwordResetSuccessContent(locale), locale)
	return s.sendHTML(to, subject, body)
}

// PasswordResetToken represents a password reset token
type PasswordResetToken struct {
	Token     string
	UserID    string
	ExpiresAt time.Time
	CreatedAt time.Time
}

// SendEnterpriseInvitation sends an enterprise invitation email as HTML
func (s *Sender) SendEnterpriseInvitation(to, enterpriseName, acceptURL, locale string) error {
	subject := replace(s.t("mail.subject.enterprise_invitation", locale), "{enterpriseName}", enterpriseName)
	body := s.emailLayout(subject, s.enterpriseInvitationContent(enterpriseName, acceptURL, locale), locale)
	return s.sendHTML(to, subject, body)
}

// SendEnterpriseAddedNotification notifies an existing user they were added to an enterprise
func (s *Sender) SendEnterpriseAddedNotification(to, enterpriseName, locale string) error {
	subject := replace(s.t("mail.subject.enterprise_added", locale), "{enterpriseName}", enterpriseName)
	body := s.emailLayout(subject, s.enterpriseAddedContent(enterpriseName, locale), locale)
	return s.sendHTML(to, subject, body)
}

// SendAdminVacantReminder notifies system admins about enterprises with no admin
func (s *Sender) SendAdminVacantReminder(to, summary, baseURL, locale string) error {
	subject := s.t("mail.subject.admin_vacant", locale)
	dashboardURL := baseURL + "/admin/enterprise"
	body := s.emailLayout(subject, s.adminVacantContent(summary, dashboardURL, locale), locale)
	return s.sendHTML(to, subject, body)
}

// SendEnterpriseAlert sends an enterprise quota alert email
func (s *Sender) SendEnterpriseAlert(to, enterpriseName, alertType string, threshold, current, limit float64, locale string) error {
	var subject string
	switch alertType {
	case "quota_warning":
		subject = replace(s.t("mail.subject.quota_warning", locale), "{enterpriseName}", enterpriseName)
	case "quota_exceeded":
		subject = replace(s.t("mail.subject.quota_exceeded", locale), "{enterpriseName}", enterpriseName)
	default:
		subject = replace(s.t("mail.subject.enterprise_notice", locale), "{enterpriseName}", enterpriseName)
	}
	body := s.emailLayout(subject, s.enterpriseAlertContent(alertType, enterpriseName, threshold, current, limit, locale), locale)
	return s.sendHTML(to, subject, body)
}

// SendModelDiscontinuedNotification notifies user that a model has been discontinued
func (s *Sender) SendModelDiscontinuedNotification(to, modelName, dashboardURL, locale string) error {
	subject := replace(s.t("mail.subject.model_discontinued", locale), "{modelName}", modelName)
	body := s.emailLayout(subject, s.modelDiscontinuedContent(modelName, dashboardURL, locale), locale)
	return s.sendHTML(to, subject, body)
}

// SendKeyFallbackNotification notifies user that their key was auto-adjusted
func (s *Sender) SendKeyFallbackNotification(to, keyName, fallbackModel, dashboardURL, locale string) error {
	subject := replace(s.t("mail.subject.key_fallback", locale), "{keyName}", keyName)
	body := s.emailLayout(subject, s.keyFallbackContent(keyName, fallbackModel, dashboardURL, locale), locale)
	return s.sendHTML(to, subject, body)
}

// SendEOLReminderNotification sends EOL reminder at 7/3/1 days before service end
func (s *Sender) SendEOLReminderNotification(to, modelName string, daysRemaining int, eolDate, dashboardURL, locale string) error {
	subject := replace(replace(s.t("mail.subject.eol_reminder", locale), "{modelName}", modelName), "{daysRemaining}", fmt.Sprintf("%d", daysRemaining))
	body := s.emailLayout(subject, s.eolReminderContent(modelName, daysRemaining, eolDate, dashboardURL, locale), locale)
	return s.sendHTML(to, subject, body)
}

// SendTestEmail sends a simple test email to verify the SMTP configuration
func (s *Sender) SendTestEmail(to, locale string) error {
	subject := s.t("mail.subject.test", locale)
	body := s.emailLayout(subject, s.testEmailContent(locale), locale)
	return s.sendHTML(to, subject, body)
}

// SendGDPRExportReady notifies a user that their requested full data export is
// ready for download from their profile page.
func (s *Sender) SendGDPRExportReady(to, locale string) error {
	subject := s.t("mail.subject.gdpr_export_ready", locale)
	body := s.emailLayout(subject, s.gdprExportReadyContent(locale), locale)
	return s.sendHTML(to, subject, body)
}

// SendNotificationEmail delivers a pre-rendered notification message (from
// the email_notifications queue drained by the background worker). The
// subject/body were rendered per recipient locale before enqueueing; here
// they are wrapped in the branded emailLayout shell with a primary action
// button when the notification carries a link.
func (s *Sender) SendNotificationEmail(to, subject, body, link, locale string) error {
	if !s.IsConfigured() {
		return fmt.Errorf("SMTP not configured")
	}
	var content strings.Builder
	for _, line := range strings.Split(strings.TrimSpace(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		content.WriteString("<p style=\"margin:0 0 12px;font-size:15px;line-height:1.7;color:#374151;\">")
		content.WriteString(htmlEscape(line))
		content.WriteString("</p>")
	}
	if link != "" {
		content.WriteString(buttonBlock(link, s.t("mail.notification.open_button", locale)))
	}
	return s.sendHTML(to, subject, s.emailLayout(subject, content.String(), locale))
}

func (s *Sender) sendHTML(to, subject, body string) error {
	if !s.IsConfigured() {
		metrics.GetMetrics().EmailFailed.Add(1)
		s.logger.Warn("SMTP not configured, email not sent",
			zap.String("to", to),
			zap.String("subject", subject),
		)
		return fmt.Errorf("SMTP not configured")
	}

	cfg := s.Config()

	from := cfg.From
	if from == "" {
		from = cfg.User
	}

	msg := buildHTMLMessage(from, to, subject, body)

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)

	if cfg.Secure || cfg.Port == 465 {
		if err := s.sendWithTLS(addr, from, to, []byte(msg)); err != nil {
			metrics.GetMetrics().EmailFailed.Add(1)
			return err
		}
		metrics.GetMetrics().EmailSent.Add(1)
		return nil
	}

	auth := smtp.PlainAuth("", cfg.User, cfg.Password, cfg.Host)
	if err := s.sendWithStartTLS(addr, auth, from, to, []byte(msg)); err != nil {
		metrics.GetMetrics().EmailFailed.Add(1)
		return err
	}

	metrics.GetMetrics().EmailSent.Add(1)
	s.logger.Info("HTML email sent", zap.String("to", to), zap.String("subject", subject))
	return nil
}

// sendWithTLS sends email using direct TLS connection (for port 465)
func (s *Sender) sendWithTLS(addr, from, to string, msg []byte) error {
	cfg := s.Config()

	// TLS config
	tlsConfig := &tls.Config{
		InsecureSkipVerify: false,
		ServerName:         cfg.Host,
	}

	// Connect to the SMTP server with a bounded dial timeout so a dead server
	// cannot block the caller forever.
	dialer := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: smtpDialTimeout},
		Config:    tlsConfig,
	}
	conn, err := dialer.Dial("tcp", addr)
	if err != nil {
		s.logger.Error("Failed to connect to SMTP server", zap.Error(err))
		return fmt.Errorf("TLS connection failed: %w", err)
	}

	// Cap all subsequent reads/writes so a stalled server cannot hold the
	// connection open indefinitely.
	_ = conn.SetDeadline(time.Now().Add(smtpOpDeadline))

	client, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("failed to create SMTP client: %w", err)
	}
	defer func() { _ = client.Close() }()

	// Authenticate
	auth := smtp.PlainAuth("", cfg.User, cfg.Password, cfg.Host)
	if err := client.Auth(auth); err != nil {
		return fmt.Errorf("SMTP auth failed: %w", err)
	}

	// Set sender
	if err := client.Mail(from); err != nil {
		return fmt.Errorf("failed to set sender: %w", err)
	}

	// Set recipient
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("failed to set recipient: %w", err)
	}

	// Send data
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("failed to get data writer: %w", err)
	}

	_, err = w.Write(msg)
	if err != nil {
		return fmt.Errorf("failed to write message: %w", err)
	}

	if err := w.Close(); err != nil {
		return fmt.Errorf("failed to close writer: %w", err)
	}

	if err := client.Quit(); err != nil {
		s.logger.Warn("Failed to close SMTP connection cleanly", zap.Error(err))
	}

	s.logger.Info("Email sent via TLS", zap.String("to", to))
	return nil
}

// sendWithStartTLS sends email over a plain TCP connection that is upgraded to
// TLS via STARTTLS when the server advertises it. It replaces smtp.SendMail
// with explicit dial/operation deadlines so a stalled server cannot block the
// caller indefinitely.
func (s *Sender) sendWithStartTLS(addr string, auth smtp.Auth, from, to string, msg []byte) error {
	conn, err := net.DialTimeout("tcp", addr, smtpPlainDialLimit)
	if err != nil {
		s.logger.Error("Failed to connect to SMTP server", zap.Error(err))
		return fmt.Errorf("SMTP connection failed: %w", err)
	}

	if err := conn.SetDeadline(time.Now().Add(smtpOpDeadline)); err != nil {
		_ = conn.Close()
		return fmt.Errorf("failed to set deadline: %w", err)
	}

	client, err := smtp.NewClient(conn, s.Config().Host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("failed to create SMTP client: %w", err)
	}
	defer func() { _ = client.Close() }()

	// Attempt STARTTLS if the server supports it.
	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(&tls.Config{ServerName: s.Config().Host}); err != nil {
			return fmt.Errorf("STARTTLS failed: %w", err)
		}
	}

	if err := client.Auth(auth); err != nil {
		return fmt.Errorf("SMTP auth failed: %w", err)
	}

	if err := client.Mail(from); err != nil {
		return fmt.Errorf("failed to set sender: %w", err)
	}

	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("failed to set recipient: %w", err)
	}

	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("failed to get data writer: %w", err)
	}

	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("failed to write message: %w", err)
	}

	if err := w.Close(); err != nil {
		return fmt.Errorf("failed to close writer: %w", err)
	}

	if err := client.Quit(); err != nil {
		s.logger.Warn("Failed to close SMTP connection cleanly", zap.Error(err))
	}
	return nil
}

func buildHTMLMessage(from, to, subject, body string) string {
	return buildMessageWithContentType(from, to, subject, body, "text/html")
}

func buildMessageWithContentType(from, to, subject, body, contentType string) string {
	encodedSubject := "=?UTF-8?B?" + base64.StdEncoding.EncodeToString([]byte(subject)) + "?="
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", to)
	fmt.Fprintf(&b, "Subject: %s\r\n", encodedSubject)
	fmt.Fprintf(&b, "Content-Type: %s; charset=UTF-8\r\n", contentType)
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("\r\n")
	b.WriteString(body)
	return b.String()
}

// t is a small helper to translate a key with fallback to the key itself.
func (s *Sender) t(key, locale string) string {
	if s.i18n == nil {
		return key
	}
	return s.i18n.T(key, locale)
}

// replace substitutes placeholders and escapes the replacement value for HTML.
func replace(tmpl, placeholder, value string) string {
	return strings.ReplaceAll(tmpl, placeholder, htmlEscape(value))
}

func htmlEscape(s string) string {
	return strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		"\"", "&quot;",
		"'", "&#39;",
	).Replace(s)
}

func sanitizeSubject(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r", " "), "\n", " ")
}

func enterpriseStatusChangeContent(enterpriseName, status, reason string) (subject, content string) {
	statusLabels := map[string]string{
		"approved":        "Approved",
		"rejected":        "Rejected",
		"needs_more_info": "More Information Required",
	}
	label := statusLabels[status]
	if label == "" {
		label = status
	}

	safeName := htmlEscape(enterpriseName)
	safeLabel := htmlEscape(label)
	safeReason := htmlEscape(reason)

	subject = "Enterprise Application: " + sanitizeSubject(label+" - "+enterpriseName)
	content = "<p>Your enterprise registration application for <strong>" + safeName + "</strong> has been updated.</p>"
	content += "<p><strong>Status:</strong> " + safeLabel + "</p>"
	if reason != "" {
		content += "<p><strong>Details:</strong> " + safeReason + "</p>"
	}
	switch status {
	case "approved":
		content += "<p>You are now the enterprise administrator. Log in to access your enterprise console.</p>"
	case "needs_more_info":
		content += "<p>Please log in and provide the requested information.</p>"
	}
	return subject, content
}

// SendEnterpriseApplicationStatusChange notifies applicant of status change
func (s *Sender) SendEnterpriseApplicationStatusChange(to, enterpriseName, status, reason, locale string) error {
	cfg := s.Config()
	if cfg.Host == "" {
		return nil // SMTP not configured, skip silently
	}
	subject, content := enterpriseStatusChangeContent(enterpriseName, status, reason)
	body := s.emailLayout(subject, content, locale)
	return s.sendHTML(to, subject, body)
}

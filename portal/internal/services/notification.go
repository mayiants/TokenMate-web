package services

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mayiants/ai-portal-go/internal/db"
	"github.com/mayiants/ai-portal-go/pkg/i18n"
	"github.com/mayiants/ai-portal-go/pkg/notify"
	"go.uber.org/zap"
)

// NotificationService is the unified touch-point layer for user-facing
// events (see docs/notification-system-proposal-202610312238.md): every
// noteworthy event lands in the per-user `notifications` inbox, and
// high-priority events are additionally queued into `email_notifications`
// for the delivery worker (P2).
//
// Titles/bodies are i18n keys rendered per recipient locale at insert time,
// mirroring how pkg/mail templates resolve per recipient.
type NotificationService struct {
	db     *db.DB
	logger *zap.Logger
	i18n   i18n.I18n
}

func NewNotificationService(database *db.DB, logger *zap.Logger, translations i18n.I18n) *NotificationService {
	return &NotificationService{db: database, logger: logger, i18n: translations}
}

// Notification is one inbox row.
type Notification struct {
	ID        string     `json:"id"`
	Type      string     `json:"type"`
	Scope     string     `json:"scope"`
	Priority  string     `json:"priority"`
	Title     string     `json:"title"`
	Body      string     `json:"body"`
	Link      string     `json:"link"`
	ReadAt    *time.Time `json:"read_at"`
	CreatedAt time.Time  `json:"created_at"`
}

// render resolves an i18n key for the locale and substitutes {{param}} placeholders.
func (s *NotificationService) render(locale, key string, params map[string]string) string {
	out := s.i18n.T(key, locale)
	for k, v := range params {
		out = strings.ReplaceAll(out, "{{"+k+"}}", v)
	}
	return out
}

// NotifyUser inserts one already-rendered notification for a specific user.
// The partial unique index uq_notification_dedup collapses duplicates while
// unread; inserted reports whether a new row was created. Email queueing for
// high-priority items is the caller's concern via MaybeEnqueueEmail.
// A per-user preference row with enabled=false for this type suppresses the
// notification entirely (no in-app row, no email) — see the role-scoped
// preference list in NotificationRoutes (preferenceTypesForRole).
func (s *NotificationService) NotifyUser(ctx context.Context, userID, ntype, scope, priority, title, body, link, refType, refID string) bool {
	if !s.typeEnabled(ctx, userID, ntype) {
		return false
	}
	tag, err := s.db.Pool.Exec(ctx,
		`INSERT INTO notifications (user_id, type, scope, priority, title, body, link, ref_type, ref_id)
		 VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9)
		 ON CONFLICT (user_id, type, ref_type, ref_id) WHERE read_at IS NULL DO NOTHING`,
		userID, ntype, scope, priority, title, body, link, refType, refID)
	if err != nil {
		s.logger.Warn("failed to insert notification", zap.Error(err), zap.String("user_id", userID), zap.String("type", ntype))
		return false
	}
	return tag.RowsAffected() > 0
}

type recipient struct {
	id     string
	email  string
	locale string
}

// enterpriseAdmins resolves active admins of an enterprise with their locales.
func (s *NotificationService) enterpriseAdmins(ctx context.Context, enterpriseID string) []recipient {
	rows, err := s.db.Pool.Query(ctx,
		`SELECT u.id::text, u.email, COALESCE(u.locale, 'en')
		 FROM users u
		 WHERE u.enterprise_id = $1::uuid AND u.role = 'enterprise_admin' AND u.deleted_at IS NULL
		 ORDER BY u.created_at`,
		enterpriseID)
	if err != nil {
		s.logger.Warn("failed to resolve enterprise admins", zap.Error(err), zap.String("enterprise_id", enterpriseID))
		return nil
	}
	defer rows.Close()
	var out []recipient
	for rows.Next() {
		var rc recipient
		if rows.Scan(&rc.id, &rc.email, &rc.locale) == nil {
			out = append(out, rc)
		}
	}
	return out
}

// systemAdmins resolves super/system admins with their locales.
func (s *NotificationService) systemAdmins(ctx context.Context) []recipient {
	rows, err := s.db.Pool.Query(ctx,
		`SELECT u.id::text, u.email, COALESCE(u.locale, 'en')
		 FROM users u
		 WHERE u.role IN ('super_admin', 'system_admin') AND u.deleted_at IS NULL
		 ORDER BY u.created_at`)
	if err != nil {
		s.logger.Warn("failed to resolve system admins", zap.Error(err))
		return nil
	}
	defer rows.Close()
	var out []recipient
	for rows.Next() {
		var rc recipient
		if rows.Scan(&rc.id, &rc.email, &rc.locale) == nil {
			out = append(out, rc)
		}
	}
	return out
}

func (s *NotificationService) notifyRecipients(ctx context.Context, recipients []recipient, ntype, scope, priority, titleKey, bodyKey string, params map[string]string, link, refType, refID string, enqueueEmail bool) int {
	inserted := 0
	for _, rc := range recipients {
		title := s.render(rc.locale, titleKey, params)
		var body string
		if bodyKey != "" {
			body = s.render(rc.locale, bodyKey, params)
		}
		if s.NotifyUser(ctx, rc.id, ntype, scope, priority, title, body, link, refType, refID) {
			inserted++
			// Email honors the per-type preference (default on): high-priority
			// notices email as before; normal notices now also email unless the
			// recipient disabled that type in profile → Notification Settings
			// (policy: high always emails, others are user-configurable).
			if enqueueEmail {
				s.MaybeEnqueueEmail(ctx, rc, ntype, title, body, link)
			}
			// IM channels: only system-admin high-priority notices fan out to
			// the admin's configured group bots (proposal §6 routing rule).
			if priority == "high" && scope == "system_admin" {
				s.deliverToChannels(ctx, rc, ntype, title, body)
			}
		}
	}
	return inserted
}

// deliverToChannels posts the rendered message to every enabled IM channel
// the admin owns whose filter list includes this type (empty filter = all).
// Delivery is fire-and-forget with a short timeout: a slow IM endpoint must
// never delay the API path that raised the event.
func (s *NotificationService) deliverToChannels(ctx context.Context, rc recipient, ntype, title, body string) {
	channels, err := s.channelsForUser(ctx, rc.id)
	if err != nil || len(channels) == 0 {
		return
	}
	text := title + "\n" + body
	go func() {
		gctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		for _, ch := range channels {
			if !ch.matches(ntype) {
				continue
			}
			if err := notify.Send(gctx, ch.Type, ch.WebhookURL, ch.Secret, title, text); err != nil {
				s.logger.Warn("webhook channel delivery failed",
					zap.String("user_id", rc.id), zap.String("channel", ch.ID),
					zap.String("type", ch.Type), zap.Error(err))
			}
		}
	}()
}

// NotifyEnterpriseAdmins renders and notifies every admin of the enterprise.
func (s *NotificationService) NotifyEnterpriseAdmins(ctx context.Context, enterpriseID, ntype, priority, titleKey, bodyKey string, params map[string]string, link, refType, refID string) int {
	return s.notifyRecipients(ctx, s.enterpriseAdmins(ctx, enterpriseID), ntype, "enterprise_admin", priority, titleKey, bodyKey, params, link, refType, refID, true)
}

// NotifyEnterpriseAdminsInApp mirrors an event into the admins' in-app inbox
// WITHOUT queueing a notification email. Used for enterprise quota/credit
// threshold alerts, which already have their own alert-email pipeline
// (sendEnterpriseAlertEmail) writing from the enterprise_alerts table —
// enqueueing here as well would deliver two emails for the same threshold.
func (s *NotificationService) NotifyEnterpriseAdminsInApp(ctx context.Context, enterpriseID, ntype, priority, titleKey, bodyKey string, params map[string]string, link, refType, refID string) int {
	return s.notifyRecipients(ctx, s.enterpriseAdmins(ctx, enterpriseID), ntype, "enterprise_admin", priority, titleKey, bodyKey, params, link, refType, refID, false)
}

// NotifyUserLocalized renders title/body in the recipient's own locale and
// notifies a single user (enqueuing email for high-priority types).
func (s *NotificationService) NotifyUserLocalized(ctx context.Context, userID, ntype, priority, titleKey, bodyKey string, params map[string]string, link, refType, refID string) bool {
	var email, locale string
	if err := s.db.Pool.QueryRow(ctx,
		`SELECT email, COALESCE(locale, 'en') FROM users WHERE id = $1::uuid`, userID).Scan(&email, &locale); err != nil {
		return false
	}
	rc := recipient{id: userID, email: email, locale: locale}
	return s.notifyRecipients(ctx, []recipient{rc}, ntype, "user", priority, titleKey, bodyKey, params, link, refType, refID, true) > 0
} // NotifySystemAdmins renders and notifies every system admin.
func (s *NotificationService) NotifySystemAdmins(ctx context.Context, ntype, priority, titleKey, bodyKey string, params map[string]string, link, refType, refID string) int {
	return s.notifyRecipients(ctx, s.systemAdmins(ctx), ntype, "system_admin", priority, titleKey, bodyKey, params, link, refType, refID, true)
}

// MaybeEnqueueEmail inserts a pending row into email_notifications when the
// recipient's preferences allow it. The per-type preference row is the master
// delivery switch (notification_preferences, channel='email'): per-type row,
// then the '*' default row, then default enabled. The deep link and the
// recipient locale travel with the row so the drained email can render the
// branded template with a primary action button.
func (s *NotificationService) MaybeEnqueueEmail(ctx context.Context, rc recipient, ntype, subject, body, link string) {
	enabled := s.typeEnabled(ctx, rc.id, ntype)
	if !enabled {
		return
	}
	var linkArg interface{}
	if link != "" {
		linkArg = link
	}
	if _, err := s.db.Pool.Exec(ctx,
		`INSERT INTO email_notifications (user_id, email, type, subject, body, link, locale)
		 VALUES ($1::uuid, $2, $3, $4, $5, $6, $7)`,
		rc.id, rc.email, ntype, subject, body, linkArg, rc.locale); err != nil {
		s.logger.Warn("failed to enqueue notification email", zap.Error(err), zap.String("user_id", rc.id), zap.String("type", ntype))
	}
}

// typeEnabled reports whether notifications of this type should be delivered
// to the user at all. The preference row doubles as the master switch for
// both the in-app inbox (checked in NotifyUser) and email (checked here).
func (s *NotificationService) typeEnabled(ctx context.Context, userID, ntype string) bool {
	var perType, globalDefault *bool
	var enabled bool
	err := s.db.Pool.QueryRow(ctx,
		`SELECT enabled FROM notification_preferences WHERE user_id = $1::uuid AND ntype = $2 AND channel = 'email'`,
		userID, ntype).Scan(&enabled)
	if err == nil {
		perType = &enabled
	}
	err = s.db.Pool.QueryRow(ctx,
		`SELECT enabled FROM notification_preferences WHERE user_id = $1::uuid AND ntype = '*' AND channel = 'email'`,
		userID).Scan(&enabled)
	if err == nil {
		globalDefault = &enabled
	}
	if perType != nil {
		return *perType
	}
	if globalDefault != nil {
		return *globalDefault
	}
	return true
}

// UnreadCount returns the badge number for the topbar bell.
func (s *NotificationService) UnreadCount(ctx context.Context, userID string) int {
	var n int
	if err := s.db.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM notifications WHERE user_id = $1::uuid AND read_at IS NULL`, userID).Scan(&n); err != nil {
		return 0
	}
	return n
}

// List returns the most recent notifications plus the unread total.
func (s *NotificationService) List(ctx context.Context, userID string, limit, offset int) ([]Notification, int, error) {
	var total, unread int
	if err := s.db.Pool.QueryRow(ctx,
		`SELECT COUNT(*), COUNT(*) FILTER (WHERE read_at IS NULL) FROM notifications WHERE user_id = $1::uuid`,
		userID).Scan(&total, &unread); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.Pool.Query(ctx,
		`SELECT id::text, type, scope, priority, title, COALESCE(body, ''), COALESCE(link, ''), read_at, created_at
		 FROM notifications WHERE user_id = $1::uuid
		 ORDER BY created_at DESC LIMIT $2 OFFSET $3`,
		userID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []Notification
	for rows.Next() {
		var n Notification
		if err := rows.Scan(&n.ID, &n.Type, &n.Scope, &n.Priority, &n.Title, &n.Body, &n.Link, &n.ReadAt, &n.CreatedAt); err != nil {
			continue
		}
		out = append(out, n)
	}
	return out, unread, nil
}

// MarkRead marks one notification read if it belongs to the user.
func (s *NotificationService) MarkRead(ctx context.Context, userID, id string) {
	if _, err := s.db.Pool.Exec(ctx,
		`UPDATE notifications SET read_at = NOW() WHERE id = $1::uuid AND user_id = $2::uuid AND read_at IS NULL`, id, userID); err != nil {
		s.logger.Warn("failed to mark notification read", zap.Error(err), zap.String("id", id))
	}
}

// MarkAllRead marks every notification of the user read.
func (s *NotificationService) MarkAllRead(ctx context.Context, userID string) {
	if _, err := s.db.Pool.Exec(ctx,
		`UPDATE notifications SET read_at = NOW() WHERE user_id = $1::uuid AND read_at IS NULL`, userID); err != nil {
		s.logger.Warn("failed to mark all notifications read", zap.Error(err), zap.String("user_id", userID))
	}
}

// RecordJobHeartbeat upserts the heartbeat row for a background job. Called
// by the withJobHeartbeat wrapper around every background.go ticker body.
func (s *NotificationService) RecordJobHeartbeat(ctx context.Context, jobName string, intervalMin int, failed bool, jobErr error) {
	status := "ok"
	errMsg := ""
	if failed {
		status = "failed"
		if jobErr != nil {
			errMsg = jobErr.Error()
		}
	}
	if _, err := s.db.Pool.Exec(ctx,
		`INSERT INTO job_runs (job_name, interval_min, last_run_at, last_status, last_error)
		 VALUES ($1, $2, NOW(), $3, NULLIF($4, ''))
		 ON CONFLICT (job_name) DO UPDATE SET
		   last_run_at = NOW(), last_status = $3, last_error = NULLIF($4, '')`,
		jobName, intervalMin, status, errMsg); err != nil {
		s.logger.Warn("failed to record job heartbeat", zap.Error(err), zap.String("job", jobName))
	}
}

// PendingApprovalCounts returns per-enterprise counts of invitations awaiting
// the system admin's second approval (status='ea_approved') — feeds the
// badge counts on /admin/enterprise.
func (s *NotificationService) PendingApprovalCounts(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.Pool.Query(ctx,
		`SELECT enterprise_id::text, COUNT(*) FROM enterprise_invitations
		 WHERE status = 'ea_approved' GROUP BY enterprise_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var id string
		var n int
		if rows.Scan(&id, &n) == nil {
			out[id] = n
		}
	}
	return out, nil
}

// PendingEACounts returns per-enterprise counts of invitations awaiting the
// enterprise admin's confirmation (status='email_confirmed').
func (s *NotificationService) PendingEACounts(ctx context.Context, enterpriseID string) (int, error) {
	var n int
	err := s.db.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM enterprise_invitations WHERE enterprise_id = $1::uuid AND status = 'email_confirmed'`,
		enterpriseID).Scan(&n)
	return n, err
}

// EmailPreferenceSummary returns the user's effective email preference per
// displayed type for the preferences UI: (per-type override?, global default?).
func (s *NotificationService) EmailPreferenceSummary(ctx context.Context, userID string, ntypes []string) map[string]*bool {
	out := map[string]*bool{}
	for _, t := range ntypes {
		var enabled bool
		err := s.db.Pool.QueryRow(ctx,
			`SELECT enabled FROM notification_preferences WHERE user_id = $1::uuid AND ntype = $2 AND channel = 'email'`,
			userID, t).Scan(&enabled)
		if err == nil {
			v := enabled
			out[t] = &v
		}
	}
	return out
}

// SetEmailPreference upserts one preference row.
func (s *NotificationService) SetEmailPreference(ctx context.Context, userID, ntype string, enabled bool) error {
	_, err := s.db.Pool.Exec(ctx,
		`INSERT INTO notification_preferences (user_id, ntype, channel, enabled, updated_at)
		 VALUES ($1::uuid, $2, 'email', $3, NOW())
		 ON CONFLICT (user_id, ntype, channel) DO UPDATE SET enabled = $3, updated_at = NOW()`,
		userID, ntype, enabled)
	if err != nil {
		return fmt.Errorf("upsert notification preference: %w", err)
	}
	return nil
}

// NotificationChannel is one IM webhook configuration owned by an admin.
type NotificationChannel struct {
	ID             string     `json:"id"`
	Type           string     `json:"type"` // feishu | dingtalk | wecom
	WebhookURL     string     `json:"webhook_url"`
	HasSecret      bool       `json:"has_secret"`
	Secret         string     `json:"-"` // populated for internal delivery only
	Enabled        bool       `json:"enabled"`
	Filters        []string   `json:"filters"` // empty = all admin high-priority types
	LastTestAt     *time.Time `json:"last_test_at"`
	LastTestStatus string     `json:"last_test_status"`
	CreatedAt      time.Time  `json:"created_at"`
}

// matches reports whether the channel accepts the given notification type.
func (c NotificationChannel) matches(ntype string) bool {
	if !c.Enabled {
		return false
	}
	if len(c.Filters) == 0 {
		return true
	}
	for _, f := range c.Filters {
		if f == ntype {
			return true
		}
	}
	return false
}

func (s *NotificationService) channelsForUser(ctx context.Context, userID string) ([]NotificationChannel, error) {
	rows, err := s.db.Pool.Query(ctx,
		`SELECT id::text, type, webhook_url, (secret IS NOT NULL AND secret <> ''), COALESCE(secret, ''), enabled, COALESCE(filters::text, '[]'), last_test_at, COALESCE(last_test_status, ''), created_at
		 FROM notification_channels WHERE user_id = $1::uuid ORDER BY created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NotificationChannel
	for rows.Next() {
		var c NotificationChannel
		var filtersRaw string
		if err := rows.Scan(&c.ID, &c.Type, &c.WebhookURL, &c.HasSecret, &c.Secret, &c.Enabled, &filtersRaw, &c.LastTestAt, &c.LastTestStatus, &c.CreatedAt); err != nil {
			continue
		}
		_ = json.Unmarshal([]byte(filtersRaw), &c.Filters)
		out = append(out, c)
	}
	return out, nil
}

// ListChannels returns the user's own channels.
func (s *NotificationService) ListChannels(ctx context.Context, userID string) ([]NotificationChannel, error) {
	return s.channelsForUser(ctx, userID)
}

// CreateChannel inserts a new webhook channel.
func (s *NotificationService) CreateChannel(ctx context.Context, userID, ctype, webhookURL, secret string, filters []string) (*NotificationChannel, error) {
	var id string
	err := s.db.Pool.QueryRow(ctx,
		`INSERT INTO notification_channels (user_id, type, webhook_url, secret, filters)
		 VALUES ($1::uuid, $2, $3, NULLIF($4, ''), $5) RETURNING id::text`,
		userID, ctype, webhookURL, secret, filtersJSON(filters)).Scan(&id)
	if err != nil {
		return nil, fmt.Errorf("create notification channel: %w", err)
	}
	channels, _ := s.channelsForUser(ctx, userID)
	for _, c := range channels {
		if c.ID == id {
			return &c, nil
		}
	}
	return &NotificationChannel{ID: id, Type: ctype, WebhookURL: webhookURL, Enabled: true, Filters: filters}, nil
}

func filtersJSON(filters []string) []byte {
	if len(filters) == 0 {
		return []byte("[]")
	}
	raw, _ := json.Marshal(filters)
	return raw
}

// DeleteChannel removes a channel owned by the user.
func (s *NotificationService) DeleteChannel(ctx context.Context, userID, id string) error {
	_, err := s.db.Pool.Exec(ctx,
		`DELETE FROM notification_channels WHERE id = $1::uuid AND user_id = $2::uuid`, id, userID)
	return err
}

// SetChannelEnabled toggles a channel.
func (s *NotificationService) SetChannelEnabled(ctx context.Context, userID, id string, enabled bool) error {
	_, err := s.db.Pool.Exec(ctx,
		`UPDATE notification_channels SET enabled = $3, updated_at = NOW() WHERE id = $1::uuid AND user_id = $2::uuid`, id, userID, enabled)
	return err
}

// TestChannel sends the fixed test message through the channel and records
// the outcome on the row.
func (s *NotificationService) TestChannel(ctx context.Context, userID, id string, locale string) error {
	var ctype, webhookURL, secret string
	if err := s.db.Pool.QueryRow(ctx,
		`SELECT type, webhook_url, COALESCE(secret, '') FROM notification_channels WHERE id = $1::uuid AND user_id = $2::uuid`,
		id, userID).Scan(&ctype, &webhookURL, &secret); err != nil {
		return fmt.Errorf("channel not found")
	}
	title := s.render(locale, "notif.test.title", nil)
	text := s.render(locale, "notif.test.body", nil)
	tctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	err := notify.Send(tctx, ctype, webhookURL, secret, title, text)
	status := "ok"
	if err != nil {
		status = "failed"
	}
	if _, uerr := s.db.Pool.Exec(ctx,
		`UPDATE notification_channels SET last_test_at = NOW(), last_test_status = $3, updated_at = NOW() WHERE id = $1::uuid AND user_id = $2::uuid`,
		id, userID, status); uerr != nil {
		s.logger.Warn("failed to record channel test result", zap.Error(uerr))
	}
	return err
}

// BalanceLowThresholdMicro reads the admin-configured low-balance threshold
// (system_settings key balance_low_threshold_usd, dollars) with the $1 default.
func (s *NotificationService) BalanceLowThresholdMicro(ctx context.Context) int64 {
	var val string
	if err := s.db.Pool.QueryRow(ctx,
		`SELECT value FROM system_settings WHERE key = 'balance_low_threshold_usd'`).Scan(&val); err == nil && val != "" {
		var dollars float64
		if _, err := fmt.Sscanf(val, "%f", &dollars); err == nil && dollars >= 0 {
			return int64(dollars * 1e6)
		}
	}
	return defaultBalanceLowThresholdMicro
}

const defaultBalanceLowThresholdMicro = 1_000_000 // $1

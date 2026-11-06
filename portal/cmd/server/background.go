package main

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/ai-proxy/ai-portal-go/internal/routes"
	"go.uber.org/zap"
)

// Advisory lock IDs for singleton scheduled jobs. They must be unique across
// the application. PostgreSQL advisory locks are 64-bit signed integers.
const (
	lockRetentionCleanup       = 1001
	lockMonthlyBillingReset    = 1002
	lockAutoRenewals           = 1003
	lockEnterpriseAppExpiry    = 1004
	lockModelLifecycle         = 1005
	lockUpstreamKeyValidation  = 1006
	lockModelLifecycleReminder = 1007
	lockOpenRouterSync         = 1008
	lockOpenRouterBackfill     = 1009
	lockBedrockBackfill        = 1010
	lockUpstreamModelSync      = 1011
	lockSubscriptionExpiry     = 1012
	lockTicketAutoclose        = 1013
	// lockAuditAnchor is deliberately far from the sequential block above so a
	// parallel change adding 1014+ cannot collide with it.
	lockAuditAnchor = 1034
)

// safeGo starts a goroutine with panic recovery to prevent the entire process
// from crashing. It also tracks the goroutine on the provided WaitGroup so the
// shutdown path can wait for in-flight background tasks before closing the DB.
func safeGo(wg *sync.WaitGroup, fn func(), logger *zap.Logger) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() {
			if r := recover(); r != nil {
				logger.Error("Background goroutine panicked", zap.Any("recover", r))
			}
		}()
		fn()
	}()
}

// runSingletonJob acquires a Postgres advisory lock and only executes fn if
// the lock is acquired. This prevents multiple Portal instances from running
// the same scheduled job concurrently and causing duplicate work or conflicts.
// runSingletonJobIntervals maps each heartbeat-registered job to its schedule
// interval in minutes; the inspector uses it to detect missed windows.
var runSingletonJobIntervals = map[string]int{
	"retention-cleanup":                24 * 60,
	"monthly-billing-reset":            24 * 60,
	"model-lifecycle":                  24 * 60,
	"eol-reminders":                    6 * 60,
	"upstream-key-validation":          6 * 60,
	"openrouter-capabilities-backfill": 24 * 60,
	"bedrock-capabilities-backfill":    24 * 60,
	"upstream-model-sync":              6 * 60,
	"openrouter-sync":                  6 * 60,
	"subscription-expiry":              6 * 60,
	"enterprise-app-expiry":            6 * 60,
	"ticket-autoclose":                 24 * 60,
	"audit-anchor":                     24 * 60,
}

// drainNotificationEmails sends up to 50 pending rows from the
// email_notifications queue per tick, marking each sent or failed. Rows are
// claimed with FOR UPDATE SKIP LOCKED so concurrent portal replicas never
// double-send.
func drainNotificationEmails(ctx context.Context, deps *AppDeps) {
	if deps.mail == nil || !deps.mail.IsConfigured() {
		return
	}
	rows, err := deps.database.Pool.Query(ctx,
		`SELECT id::text, email, subject, COALESCE(body, ''), COALESCE(link, ''), COALESCE(locale, 'en')
		 FROM email_notifications
		 WHERE status = 'pending' ORDER BY created_at LIMIT 50 FOR UPDATE SKIP LOCKED`)
	if err != nil {
		return
	}
	defer rows.Close()
	type job struct {
		id, email, subject, body, link, locale string
	}
	var jobs []job
	for rows.Next() {
		var j job
		if rows.Scan(&j.id, &j.email, &j.subject, &j.body, &j.link, &j.locale) == nil {
			jobs = append(jobs, j)
		}
	}
	rows.Close()
	base := strings.TrimRight(deps.cfg.App.BaseURL, "/")
	for _, j := range jobs {
		link := j.link
		if link != "" && strings.HasPrefix(link, "/") {
			link = base + link
		}
		err := deps.mail.SendNotificationEmail(j.email, j.subject, j.body, link, j.locale)
		status := "sent"
		errMsg := ""
		if err != nil {
			status = "failed"
			errMsg = err.Error()
			deps.logger.Warn("notification email send failed",
				zap.String("id", j.id), zap.String("to", j.email), zap.Error(err))
		}
		if _, uerr := deps.database.Pool.Exec(ctx,
			`UPDATE email_notifications SET status = $2, sent_at = NOW(), error_message = NULLIF($3, '') WHERE id = $1::uuid`,
			j.id, status, errMsg); uerr != nil {
			deps.logger.Warn("failed to update email notification status", zap.String("id", j.id), zap.Error(uerr))
		}
	}
}

// inspectJobRuns scans the heartbeat registry once an hour and notifies system
// admins when a job misses its scheduled window or keeps failing. last_notified_at
// dedups: a failing job re-alerts at most once per day until it recovers.
func inspectJobRuns(ctx context.Context, deps *AppDeps) {
	if deps.notificationSvc == nil {
		return
	}
	rows, err := deps.database.Pool.Query(ctx,
		`SELECT job_name, interval_min, last_run_at, last_status, last_error, last_notified_at FROM job_runs`)
	if err != nil {
		return
	}
	defer rows.Close()
	type jr struct {
		name      string
		interval  int
		lastRun   *time.Time
		status    string
		lastError string
		notified  *time.Time
	}
	var jobs []jr
	for rows.Next() {
		var j jr
		if rows.Scan(&j.name, &j.interval, &j.lastRun, &j.status, &j.lastError, &j.notified) == nil {
			jobs = append(jobs, j)
		}
	}
	for _, j := range jobs {
		stale := j.lastRun == nil || time.Since(*j.lastRun) > time.Duration(j.interval)*time.Minute*2
		failed := j.status == "failed"
		if !stale && !failed {
			continue
		}
		if j.notified != nil && time.Since(*j.notified) < 24*time.Hour {
			continue
		}
		kind := "stalled"
		if failed {
			kind = "failed"
		}
		inserted := deps.notificationSvc.NotifySystemAdmins(ctx, "cron_job_anomaly", "high",
			"notif.cron_job_anomaly.title", "notif.cron_job_anomaly.body",
			map[string]string{"job": j.name, "kind": kind, "error": j.lastError},
			"/admin", "job_run", j.name)
		if inserted > 0 {
			if _, err := deps.database.Pool.Exec(ctx,
				`UPDATE job_runs SET last_notified_at = NOW() WHERE job_name = $1`, j.name); err != nil {
				deps.logger.Warn("Failed to update job notification dedup", zap.String("job", j.name), zap.Error(err))
			}
		}
	}
}

func runSingletonJob(ctx context.Context, deps *AppDeps, lockID int64, name string, fn func(context.Context) error) {
	lock, acquired, err := deps.database.TryAdvisoryLock(ctx, lockID)
	if err != nil {
		deps.logger.Error("Failed to acquire advisory lock for job", zap.String("job", name), zap.Error(err))
		return
	}
	if !acquired {
		deps.logger.Info("Skipping scheduled job, another instance holds the lock", zap.String("job", name))
		return
	}
	defer func() {
		if err := lock.Release(ctx); err != nil {
			deps.logger.Error("Failed to release advisory lock", zap.String("job", name), zap.Error(err))
		}
	}()

	// Run the job without inheriting cancellation so a shutdown does not leave
	// the database in an inconsistent state midway through a batch update.
	jobErr := fn(context.WithoutCancel(ctx))

	// Heartbeat: record success/failure so the inspector can alert on missed
	// windows or repeated failures (notification-system proposal §3.5).
	if deps.notificationSvc != nil {
		deps.notificationSvc.RecordJobHeartbeat(ctx, name, runSingletonJobIntervals[name], jobErr != nil, jobErr)
	}
}

// startBackgroundJobs launches all non-blocking background tasks: exchange rate
// updates, webhook queue processing, async job workers, scheduled admin
// reminders, model lifecycle tasks, upstream key validation and auto-renewals.
// The provided ctx should be cancellable so shutdown can signal the tickers to
// stop; wg tracks each launched goroutine so the shutdown path can wait for
// in-flight work before closing the database.
func startBackgroundJobs(ctx context.Context, deps *AppDeps, wg *sync.WaitGroup) {
	// Exchange rate service (auto-update every 8 hours)
	safeGo(wg, func() { deps.exchangeService.Start(ctx) }, deps.logger)

	// Webhook queue processor
	safeGo(wg, func() { deps.webhookRoutes.StartQueueProcessor(ctx) }, deps.logger)

	// Async email job worker
	safeGo(wg, func() { deps.jobQueue.StartWorker(ctx, "email") }, deps.logger)

	// Notification email worker: drains email_notifications (queued by the
	// notification service for high-priority events) every 30 seconds.
	safeGo(wg, func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		drainNotificationEmails(ctx, deps)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				drainNotificationEmails(ctx, deps)
			}
		}
	}, deps.logger)

	// Job heartbeat inspector: alerts system admins on missed windows / failures.
	safeGo(wg, func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		inspectJobRuns(ctx, deps)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				inspectJobRuns(ctx, deps)
			}
		}
	}, deps.logger)

	// Retention cleanup: removes old audit logs, health stats, verification codes,
	// email notifications, and other high-volume tables that grow without bound.
	safeGo(wg, func() {
		// Run once at startup, then daily.
		runSingletonJob(ctx, deps, lockRetentionCleanup, "retention-cleanup", func(jobCtx context.Context) error {
			if err := deps.database.RunRetentionCleanup(jobCtx, deps.logger); err != nil {
				deps.logger.Error("Retention cleanup failed", zap.Error(err))
				return err
			}
			return nil
		})

		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				runSingletonJob(ctx, deps, lockRetentionCleanup, "retention-cleanup", func(jobCtx context.Context) error {
					if err := deps.database.RunRetentionCleanup(jobCtx, deps.logger); err != nil {
						deps.logger.Error("Retention cleanup failed", zap.Error(err))
						return err
					}
					return nil
				})
			}
		}
	}, deps.logger)

	// Audit tamper-evidence anchors: daily HMAC-signed snapshots of the audit
	// hash-chain heads and sealed usage_records day digests (migration 0034).
	// Waits briefly after startup so the first run sees a settled schema.
	safeGo(wg, func() {
		run := func() {
			runSingletonJob(ctx, deps, lockAuditAnchor, "audit-anchor", func(jobCtx context.Context) error {
				res, err := deps.database.CreateAuditAnchors(jobCtx, deps.cfg.Auth.EncryptionKey, time.Now())
				if err != nil {
					deps.logger.Error("Audit anchor job failed", zap.Error(err))
					return err
				}
				if len(res.Created) > 0 {
					deps.logger.Info("Audit anchors created", zap.Strings("anchors", res.Created))
				}
				return nil
			})
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Minute):
		}
		run()
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				run()
			}
		}
	}, deps.logger)

	// Daily admin vacant reminder + monthly billing reset
	safeGo(wg, func() {
		runSingletonJob(ctx, deps, lockMonthlyBillingReset, "monthly-billing-reset", func(jobCtx context.Context) error {
			deps.enterpriseRoutes.CheckAdminVacantEnterprises(jobCtx)
			deps.enterpriseRoutes.ResetMonthlyBilling(jobCtx)
			return nil
		})

		for {
			now := time.Now()
			tomorrow := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 5, 0, now.Location())
			waitDuration := time.Until(tomorrow)
			if waitDuration <= 0 {
				waitDuration = time.Minute
			}

			select {
			case <-ctx.Done():
				return
			case <-time.After(waitDuration):
				runSingletonJob(ctx, deps, lockMonthlyBillingReset, "monthly-billing-reset", func(jobCtx context.Context) error {
					deps.enterpriseRoutes.CheckAdminVacantEnterprises(jobCtx)
					deps.enterpriseRoutes.ResetMonthlyBilling(jobCtx)
					return nil
				})
			}
		}
	}, deps.logger)

	// Model lifecycle background tasks (daily)
	safeGo(wg, func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()

		runSingletonJob(ctx, deps, lockModelLifecycle, "model-lifecycle", func(jobCtx context.Context) error {
			deps.modelRoutes.AutoDiscontinueExpiredEOLModels(jobCtx)
			deps.modelRoutes.CleanupAllInvalidModels(jobCtx)
			deps.modelRoutes.CheckAndSendEOLReminders(jobCtx)
			return nil
		})

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				runSingletonJob(ctx, deps, lockModelLifecycle, "model-lifecycle", func(jobCtx context.Context) error {
					deps.modelRoutes.AutoDiscontinueExpiredEOLModels(jobCtx)
					deps.modelRoutes.CleanupAllInvalidModels(jobCtx)
					deps.modelRoutes.CheckAndSendEOLReminders(jobCtx)
					return nil
				})
			}
		}
	}, deps.logger)

	// EOL reminders (every 6 hours)
	safeGo(wg, func() {
		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				runSingletonJob(ctx, deps, lockModelLifecycleReminder, "eol-reminders", func(jobCtx context.Context) error {
					deps.modelRoutes.CheckAndSendEOLReminders(jobCtx)
					return nil
				})
			}
		}
	}, deps.logger)

	// Upstream key validation (every 6 hours)
	safeGo(wg, func() {
		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()
		time.Sleep(30 * time.Second)
		runSingletonJob(ctx, deps, lockUpstreamKeyValidation, "upstream-key-validation", func(jobCtx context.Context) error {
			routes.ValidateUpstreamKeysBackground(deps.database, deps.cfg, deps.logger)
			return nil
		})
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				runSingletonJob(ctx, deps, lockUpstreamKeyValidation, "upstream-key-validation", func(jobCtx context.Context) error {
					routes.ValidateUpstreamKeysBackground(deps.database, deps.cfg, deps.logger)
					return nil
				})
			}
		}
	}, deps.logger)

	// OpenRouter capabilities backfill for existing service_models (once at startup)
	safeGo(wg, func() {
		time.Sleep(1 * time.Minute)
		runSingletonJob(ctx, deps, lockOpenRouterBackfill, "openrouter-capabilities-backfill", func(jobCtx context.Context) error {
			updated, err := deps.modelRoutes.BackfillOpenRouterCapabilities(jobCtx)
			if err != nil {
				deps.logger.Error("OpenRouter capabilities backfill failed", zap.Error(err))
			} else {
				deps.logger.Info("OpenRouter capabilities backfill completed", zap.Int("updated", updated))
			}
			return nil
		})
	}, deps.logger)

	// Bedrock capabilities backfill for existing service_models (once at startup)
	safeGo(wg, func() {
		time.Sleep(90 * time.Second)
		runSingletonJob(ctx, deps, lockBedrockBackfill, "bedrock-capabilities-backfill", func(jobCtx context.Context) error {
			updated, err := deps.adminRoutes.BackfillBedrockCapabilities(jobCtx)
			if err != nil {
				deps.logger.Error("Bedrock capabilities backfill failed", zap.Error(err))
			} else {
				deps.logger.Info("Bedrock capabilities backfill completed", zap.Int("updated", updated))
			}
			return nil
		})
	}, deps.logger)

	// Upstream key model list sync (every 6 hours, additive merge from /v1/models)
	safeGo(wg, func() {
		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()
		time.Sleep(60 * time.Second)
		runSingletonJob(ctx, deps, lockUpstreamModelSync, "upstream-model-sync", func(jobCtx context.Context) error {
			routes.SyncUpstreamKeyModelsBackground(deps.database, deps.cfg, deps.logger)
			return nil
		})
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				runSingletonJob(ctx, deps, lockUpstreamModelSync, "upstream-model-sync", func(jobCtx context.Context) error {
					routes.SyncUpstreamKeyModelsBackground(deps.database, deps.cfg, deps.logger)
					return nil
				})
			}
		}
	}, deps.logger)

	// OpenRouter candidate model sync (every 6 hours)
	safeGo(wg, func() {
		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()
		time.Sleep(2 * time.Minute)
		runSingletonJob(ctx, deps, lockOpenRouterSync, "openrouter-sync", func(jobCtx context.Context) error {
			total, synced, skipped, err := deps.adminRoutes.SyncOpenRouterModels(jobCtx)
			if err != nil {
				deps.logger.Error("OpenRouter background sync failed", zap.Error(err))
			} else {
				deps.logger.Info("OpenRouter background sync completed",
					zap.Int("total", total),
					zap.Int("synced", synced),
					zap.Int("skipped", skipped),
				)
			}
			return nil
		})
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				runSingletonJob(ctx, deps, lockOpenRouterSync, "openrouter-sync", func(jobCtx context.Context) error {
					total, synced, skipped, err := deps.adminRoutes.SyncOpenRouterModels(jobCtx)
					if err != nil {
						deps.logger.Error("OpenRouter background sync failed", zap.Error(err))
					} else {
						deps.logger.Info("OpenRouter background sync completed",
							zap.Int("total", total),
							zap.Int("synced", synced),
							zap.Int("skipped", skipped),
						)
					}
					return nil
				})
			}
		}
	}, deps.logger)

	// Subscription expiry sweeper (every hour).
	// The payment platform is disabled, so auto-renewal does not run: a
	// subscription that passes its expiry simply lapses (status -> 'expired').
	// Users who want to keep using the service must subscribe again. This
	// sweeper keeps the stored status in sync with the expiry the service layer
	// already enforces via `expires_at > NOW()`.
	safeGo(wg, func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		time.Sleep(5 * time.Minute)
		runSingletonJob(ctx, deps, lockSubscriptionExpiry, "subscription-expiry", func(jobCtx context.Context) error {
			_, err := routes.ExpireLapsedSubscriptions(jobCtx, deps.database, deps.logger)
			return err
		})
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				runSingletonJob(ctx, deps, lockSubscriptionExpiry, "subscription-expiry", func(jobCtx context.Context) error {
					_, err := routes.ExpireLapsedSubscriptions(jobCtx, deps.database, deps.logger)
					return err
				})
			}
		}
	}, deps.logger)

	// Support ticket housekeeping (daily): tickets with no reply for 30 days
	// are closed silently, and staged attachments abandoned for over 24 hours
	// are deleted. A user reply to a closed ticket reopens it.
	safeGo(wg, func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		time.Sleep(15 * time.Minute)
		runTicketHousekeeping := func(jobCtx context.Context) error {
			if _, err := routes.AutoCloseStaleTickets(jobCtx, deps.database, deps.logger, 30); err != nil {
				return err
			}
			if deps.ticketRoutes != nil {
				_, err := deps.ticketRoutes.CleanupStaged(jobCtx, 24)
				return err
			}
			return nil
		}
		runSingletonJob(ctx, deps, lockTicketAutoclose, "ticket-autoclose", runTicketHousekeeping)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				runSingletonJob(ctx, deps, lockTicketAutoclose, "ticket-autoclose", runTicketHousekeeping)
			}
		}
	}, deps.logger)

	// Enterprise application expiry (every 12 hours)
	safeGo(wg, func() {
		ticker := time.NewTicker(12 * time.Hour)
		defer ticker.Stop()
		time.Sleep(10 * time.Minute)
		runSingletonJob(ctx, deps, lockEnterpriseAppExpiry, "enterprise-app-expiry", func(jobCtx context.Context) error {
			expireEnterpriseApplications(jobCtx, deps)
			return nil
		})
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				runSingletonJob(ctx, deps, lockEnterpriseAppExpiry, "enterprise-app-expiry", func(jobCtx context.Context) error {
					expireEnterpriseApplications(jobCtx, deps)
					return nil
				})
			}
		}
	}, deps.logger)
}

func expireEnterpriseApplications(ctx context.Context, deps *AppDeps) {
	result, err := deps.database.Pool.Exec(ctx,
		`UPDATE enterprise_applications SET status = 'expired', updated_at = NOW()
		 WHERE status = 'needs_more_info' AND updated_at < NOW() - INTERVAL '7 days'`)
	if err != nil {
		deps.logger.Error("Failed to expire enterprise applications", zap.Error(err))
		return
	}
	if result.RowsAffected() > 0 {
		deps.logger.Info("Expired enterprise applications", zap.Int64("count", result.RowsAffected()))
	}
}

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/ai-proxy/ai-portal-go/internal/config"
	"github.com/ai-proxy/ai-portal-go/internal/db"
	"github.com/ai-proxy/ai-portal-go/internal/middleware"
	"github.com/ai-proxy/ai-portal-go/internal/routes"
	"github.com/ai-proxy/ai-portal-go/internal/services"
	"github.com/ai-proxy/ai-portal-go/internal/storage"
	webhandler "github.com/ai-proxy/ai-portal-go/internal/web"
	"github.com/ai-proxy/ai-portal-go/pkg/audit"
	"github.com/ai-proxy/ai-portal-go/pkg/mail"
	"github.com/ai-proxy/ai-portal-go/pkg/queue"
	"github.com/ai-proxy/ai-portal-go/pkg/telemetry"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

// AppDeps holds all initialized dependencies that the router and background
// workers need. Keeping them in one place makes setup and teardown explicit.
type AppDeps struct {
	cfg *config.Config

	logger   *zap.Logger
	database *db.DB
	auditLog *audit.Logger
	mail     *mail.Sender
	auth     *middleware.Auth

	jobQueue        *queue.JobQueue
	exchangeService *services.ExchangeService
	rewardService   *services.RewardService

	webHandler *webhandler.Handler

	authRoutes          *routes.AuthRoutes
	webhookRoutes       *routes.WebhookRoutes
	apiKeyRoutes        *routes.APIKeyRoutes
	statusRoutes        *routes.StatusRoutes
	dashboardRoutes     *routes.DashboardRoutes
	usageRoutes         *routes.UsageRoutes
	adminRoutes         *routes.AdminRoutes
	enterpriseAppRoutes *routes.EnterpriseApplicationRoutes
	syncRoutes          *routes.SyncRoutes
	profileRoutes       *routes.ProfileRoutes
	mfaRoutes           *routes.MFARoutes
	gdprRoutes          *routes.GDPRRoutes
	billingRoutes       *routes.BillingRoutes
	orderRoutes         *routes.OrderRoutes
	invoiceRoutes       *routes.InvoiceRoutes
	addressRoutes       *routes.AddressRoutes
	settingsRoutes      *routes.SettingsRoutes
	voucherRoutes       *routes.VoucherRoutes
	subscriptionRoutes  *routes.SubscriptionRoutes
	auditRoutes         *routes.AuditRoutes
	announcementRoutes  *routes.AnnouncementRoutes
	groupRoutes         *routes.GroupRoutes
	enterpriseRoutes    *routes.EnterpriseRoutes
	paymentRoutes       *routes.PaymentRoutes
	downstreamRoutes    *routes.DownstreamRoutes
	modelRoutes         *routes.ModelRoutes
	channelHealthRoutes *routes.ChannelHealthRoutes
	deviceRoutes        *routes.DeviceRoutes
	mgmtTokenRoutes     *routes.ManagementTokenRoutes
	agentEnrollRoutes   *routes.AgentEnrollmentRoutes
	agentDeviceRoutes   *routes.AgentDeviceRoutes
	notificationRoutes  *routes.NotificationRoutes
	ticketRoutes        *routes.TicketRoutes
	notificationSvc     *services.NotificationService
}

// bootstrap loads configuration, validates security settings, initializes the
// database, logger, mailer, auth, job queue and all route handlers.
func bootstrap() *AppDeps {
	cfg := config.Load()

	// Validate JWT secret
	if cfg.Auth.JWTSecret == "" || len(cfg.Auth.JWTSecret) < 32 {
		fmt.Println("ERROR: JWT_SECRET must be at least 32 characters")
		os.Exit(1)
	}

	// Validate encryption key for upstream API keys
	if cfg.Auth.EncryptionKey == "" {
		fmt.Println("WARNING: ENCRYPTION_KEY not set - upstream API keys will be stored in plaintext")
		fmt.Println("WARNING: For production, set ENCRYPTION_KEY to a 32+ character secret")
	} else if len(cfg.Auth.EncryptionKey) < 32 {
		fmt.Println("WARNING: ENCRYPTION_KEY should be at least 32 characters for AES-256 encryption")
	}

	// Production security validations
	if cfg.Auth.AdminMode == "production" {
		if cfg.Auth.EncryptionKey == "" || len(cfg.Auth.EncryptionKey) < 32 {
			fmt.Println("ERROR: ENCRYPTION_KEY must be at least 32 characters in production mode")
			os.Exit(1)
		}
		adminPassword := os.Getenv("ADMIN_PASSWORD")
		if adminPassword == "" || len(adminPassword) < 12 {
			fmt.Println("ERROR: ADMIN_PASSWORD must be at least 12 characters in production mode")
			os.Exit(1)
		}
		switch adminPassword {
		case "Admin@123456!", "Admin@123456", "admin123", "admin", "password", "changeme", "12345678", "123456789012", "YourStrongAdminPassword12!", "CHANGE_ME_admin_password_min_12_chars", "CHANGE_ME", "CHANGEME":
			fmt.Println("ERROR: ADMIN_PASSWORD uses a known weak/default value; set a strong unique password in production mode")
			os.Exit(1)
		}
	}

	logger, _ := zap.NewProduction()

	// Initialize OpenTelemetry tracer (no-op when OTEL env vars are absent)
	if _, err := telemetry.InitTracer("ai-portal-go"); err != nil {
		logger.Warn("Failed to initialize telemetry tracer, continuing without tracing", zap.Error(err))
	}

	database, err := db.New(cfg, logger)
	if err != nil {
		logger.Fatal("Failed to connect to database", zap.Error(err))
	}

	auditLog := audit.NewLogger(database, logger)

	translations, err := webhandler.LoadTranslations()
	if err != nil {
		logger.Fatal("Failed to load i18n translations", zap.Error(err))
	}

	mailSender := mail.NewSender(cfg.SMTP, logger, translations)

	auth := middleware.NewAuth(cfg, logger)
	auth.SetDB(database)

	ctx := context.Background()

	if err := database.InitSchema(ctx, logger); err != nil {
		logger.Fatal("Schema init failed -- cannot start without database tables", zap.Error(err))
	}
	// Apply versioned migrations on top of the InitSchema baseline. The directory
	// defaults to ./migrations and can be overridden with MIGRATIONS_DIR. Migrate
	// is a no-op when the directory is missing or empty, so this is safe to ship
	// before any migration files exist.
	migrationsDir := os.Getenv("MIGRATIONS_DIR")
	if migrationsDir == "" {
		migrationsDir = "./migrations"
	}
	if err := database.Migrate(ctx, logger, migrationsDir); err != nil {
		logger.Fatal("Migration failed -- cannot start with a partially migrated schema", zap.Error(err))
	}
	if err := database.SeedDefaultData(ctx, logger); err != nil {
		logger.Warn("Seed warning", zap.Error(err))
	}

	// Load global system settings into memory so hot paths (validate / key creation)
	// do not need to query the database for default RPM/TPM values.
	if err := routes.LoadSystemSettings(database); err != nil {
		logger.Warn("Failed to load system settings cache, using defaults", zap.Error(err))
	}

	// Keep monthly usage_records partitions ahead of incoming data.
	database.StartUsagePartitionMaintenance(ctx, logger)

	initDefaultAdmin(database, cfg, logger)
	initE2EAccounts(database, cfg, logger)

	jobQueue := queue.NewJobQueue(database.Redis, logger)
	jobQueue.Register("email", func(payload json.RawMessage) error {
		var job struct {
			To     string `json:"to"`
			Code   string `json:"code"`
			Locale string `json:"locale"`
		}
		if err := json.Unmarshal(payload, &job); err != nil {
			return err
		}
		if job.Locale == "" {
			job.Locale = "zh"
		}
		return mailSender.SendVerificationCode(job.To, job.Code, job.Locale)
	})

	rewardSvc := services.NewRewardService(database, logger, auditLog)
	exchangeService := services.NewExchangeService(database, logger)

	webHandler, err := webhandler.NewHandler(database, cfg, logger, auth, mailSender, auditLog, exchangeService)
	if err != nil {
		logger.Fatal("Failed to create web handler", zap.Error(err))
	}

	storageProvider, err := storage.New(cfg.FileStorage)
	if err != nil {
		logger.Fatal("Failed to initialize file storage", zap.Error(err))
	}

	notificationSvc := services.NewNotificationService(database, logger, translations)

	deps := &AppDeps{
		cfg:      cfg,
		logger:   logger,
		database: database,
		auditLog: auditLog,
		mail:     mailSender,
		auth:     auth,

		jobQueue:        jobQueue,
		exchangeService: exchangeService,
		rewardService:   rewardSvc,
		webHandler:      webHandler,

		notificationSvc:     notificationSvc,
		authRoutes:          routes.NewAuthRoutes(database, cfg, logger, auth, auditLog, mailSender, rewardSvc),
		webhookRoutes:       routes.NewWebhookRoutes(database, cfg, logger, mailSender, exchangeService, notificationSvc),
		apiKeyRoutes:        routes.NewAPIKeyRoutes(database, cfg, logger, auth, auditLog),
		statusRoutes:        routes.NewStatusRoutes(database),
		dashboardRoutes:     routes.NewDashboardRoutes(database, auth, logger),
		usageRoutes:         routes.NewUsageRoutes(database, auth),
		adminRoutes:         routes.NewAdminRoutes(database, cfg, logger, auth, auditLog, storageProvider, mailSender),
		enterpriseAppRoutes: routes.NewEnterpriseApplicationRoutes(database, cfg, logger, auth, auditLog, storageProvider, mailSender),
		syncRoutes:          routes.NewSyncRoutes(database, cfg, logger, services.NewPricingService(database, logger)),
		profileRoutes:       routes.NewProfileRoutes(database, auth, logger, auditLog, mailSender),
		mfaRoutes:           routes.NewMFARoutes(database, cfg, auth, logger, auditLog),
		gdprRoutes:          routes.NewGDPRRoutes(database, auth, mailSender, storageProvider, logger),
		billingRoutes:       routes.NewBillingRoutes(database, auth, storageProvider),
		orderRoutes:         routes.NewOrderRoutes(database, auth),
		invoiceRoutes:       routes.NewInvoiceRoutes(database, auth, logger, auditLog, notificationSvc, storageProvider, cfg),
		addressRoutes:       routes.NewAddressRoutes(database, auth, logger),
		settingsRoutes:      routes.NewSettingsRoutes(database, auth, mailSender, cfg.SMTP, cfg.Auth.EncryptionKey, logger).WithAudit(auditLog),
		voucherRoutes:       routes.NewVoucherRoutes(database, auth, logger),
		subscriptionRoutes:  routes.NewSubscriptionRoutes(database, auth, auditLog, logger),
		auditRoutes:         routes.NewAuditRoutes(database, auth, auditLog),
		announcementRoutes:  routes.NewAnnouncementRoutes(database, auth),
		groupRoutes:         routes.NewGroupRoutes(database, auth),
		enterpriseRoutes:    routes.NewEnterpriseRoutes(database, cfg, logger, auth, auditLog, mailSender, exchangeService, rewardSvc, notificationSvc),
		paymentRoutes:       routes.NewPaymentRoutes(database, cfg, logger, auth, auditLog, storageProvider),
		downstreamRoutes:    routes.NewDownstreamRoutes(database, auth, auditLog),
		modelRoutes:         routes.NewModelRoutes(database, auth, cfg, logger, mailSender, auditLog),
		channelHealthRoutes: routes.NewChannelHealthRoutes(database, auth),
		deviceRoutes:        routes.NewDeviceRoutes(database, auth, logger),
		mgmtTokenRoutes:     routes.NewManagementTokenRoutes(database, cfg, auth, logger, auditLog),
		agentEnrollRoutes:   routes.NewAgentEnrollmentRoutes(database, cfg, logger, auditLog, mailSender),
		agentDeviceRoutes:   routes.NewAgentDeviceRoutes(database, cfg, logger, auth),
		notificationRoutes:  routes.NewNotificationRoutes(database, auth, notificationSvc),
		ticketRoutes:        routes.NewTicketRoutes(database, auth, notificationSvc, auditLog, cfg, storageProvider, logger),
	}

	// Apply SMTP settings stored by admins in system_settings, overriding the
	// environment-based configuration the sender was constructed with.
	routes.LoadSMTPFromDB(ctx, database, mailSender, cfg.Auth.EncryptionKey, logger)

	// The audit verify endpoint re-checks anchor HMACs, which are keyed from
	// ENCRYPTION_KEY (see pkg/auditchain.DeriveAnchorKey).
	deps.auditRoutes.SetAnchorKey(cfg.Auth.EncryptionKey)

	return deps
}

// initDefaultAdmin creates the default super admin user from environment
// variables if one does not already exist.
func initDefaultAdmin(database *db.DB, cfg *config.Config, logger *zap.Logger) {
	ctx := context.Background()

	adminEmail := os.Getenv("ADMIN_EMAIL")
	if adminEmail == "" {
		adminEmail = "admin@example.com"
	}
	adminPassword := os.Getenv("ADMIN_PASSWORD")
	if adminPassword == "" {
		logger.Fatal("ADMIN_PASSWORD not set. Set ADMIN_PASSWORD environment variable with at least 12 characters.")
	}
	adminName := os.Getenv("ADMIN_NAME")
	if adminName == "" {
		adminName = "Admin"
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(adminPassword), 10)
	if err != nil {
		logger.Error("Failed to hash admin password", zap.Error(err))
		return
	}

	var exists bool
	_ = database.Pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM users WHERE email = $1 AND deleted_at IS NULL)`,
		adminEmail,
	).Scan(&exists)
	if exists {
		logger.Info("Default admin already exists", zap.String("email", adminEmail))
		return
	}

	_, err = database.Pool.Exec(ctx,
		`INSERT INTO users (name, email, password_hash, role, balance) VALUES ($1, $2, $3, 'super_admin', 0)`,
		adminName, adminEmail, string(hash),
	)
	if err != nil {
		logger.Warn("Failed to create default admin", zap.Error(err))
		return
	}

	logger.Info("Default admin initialized", zap.String("email", adminEmail))
}

// initE2EAccounts seeds optional end-to-end test accounts when
// SEED_E2E_ACCOUNTS=true and the corresponding E2E_* credentials are set.
// This keeps local development and CI environments in sync without creating
// dummy accounts in production deployments that leave the variables unset.
func initE2EAccounts(database *db.DB, cfg *config.Config, logger *zap.Logger) {
	if os.Getenv("SEED_E2E_ACCOUNTS") != "true" {
		return
	}

	ctx := context.Background()

	if err := seedE2EUser(ctx, database, logger,
		os.Getenv("E2E_ADMIN_EMAIL"),
		os.Getenv("E2E_ADMIN_PASSWORD"),
		"super_admin",
		"",
	); err != nil {
		logger.Warn("Failed to seed E2E admin", zap.Error(err))
	}

	if err := seedE2EUser(ctx, database, logger,
		os.Getenv("E2E_USER_EMAIL"),
		os.Getenv("E2E_USER_PASSWORD"),
		"user",
		"",
	); err != nil {
		logger.Warn("Failed to seed E2E user", zap.Error(err))
	}

	if err := seedE2EEnterpriseAdmin(ctx, database, logger,
		os.Getenv("E2E_ENTERPRISE_EMAIL"),
		os.Getenv("E2E_ENTERPRISE_PASSWORD"),
	); err != nil {
		logger.Warn("Failed to seed E2E enterprise admin", zap.Error(err))
	}
}

func hashPassword(password string) (string, error) {
	if len(password) < 8 {
		return "", fmt.Errorf("password must be at least 8 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 10)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

func seedE2EUser(ctx context.Context, database *db.DB, logger *zap.Logger, email, password, role, enterpriseID string) error {
	if email == "" || password == "" {
		return nil
	}

	var exists bool
	_ = database.Pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM users WHERE email = $1 AND deleted_at IS NULL)`,
		email,
	).Scan(&exists)
	if exists {
		logger.Info("E2E account already exists", zap.String("email", email))
		return nil
	}

	hash, err := hashPassword(password)
	if err != nil {
		return err
	}

	name := "E2E " + role
	switch role {
	case "user":
		name = "E2E User"
	case "super_admin":
		name = "E2E Admin"
	case "enterprise_admin":
		name = "E2E Enterprise Admin"
	}

	if enterpriseID != "" {
		_, err = database.Pool.Exec(ctx,
			`INSERT INTO users (name, email, password_hash, role, enterprise_id, balance) VALUES ($1, $2, $3, $4, $5::uuid, 0)`,
			name, email, hash, role, enterpriseID,
		)
	} else {
		_, err = database.Pool.Exec(ctx,
			`INSERT INTO users (name, email, password_hash, role, balance) VALUES ($1, $2, $3, $4, 0)`,
			name, email, hash, role,
		)
	}
	if err != nil {
		return err
	}

	logger.Info("E2E account initialized", zap.String("email", email), zap.String("role", role))
	return nil
}

func seedE2EEnterpriseAdmin(ctx context.Context, database *db.DB, logger *zap.Logger, email, password string) error {
	if email == "" || password == "" {
		return nil
	}

	// Create an enterprise for the E2E enterprise admin if one does not exist.
	var enterpriseID string
	err := database.Pool.QueryRow(ctx,
		`INSERT INTO enterprises (name, contact_email, status, quota_total, credit_total, billing_cycle, credit_reset_at)
		 SELECT $1, $2::varchar, 'active', 10000000, 10000000, 'monthly', NOW()
		 WHERE NOT EXISTS (SELECT 1 FROM enterprises WHERE contact_email = $2::varchar)
		 RETURNING id::text`,
		"E2E Enterprise", email,
	).Scan(&enterpriseID)
	if err != nil {
		logger.Info("E2E enterprise insert did not return ID, looking up existing", zap.Error(err))
		// Enterprise may already exist; look it up.
		lookupErr := database.Pool.QueryRow(ctx,
			`SELECT id::text FROM enterprises WHERE contact_email = $1`,
			email,
		).Scan(&enterpriseID)
		if lookupErr != nil {
			return fmt.Errorf("insert E2E enterprise failed: %w; lookup also failed: %v", err, lookupErr)
		}
	}
	if enterpriseID == "" {
		return fmt.Errorf("could not create or find E2E enterprise")
	}

	return seedE2EUser(ctx, database, logger, email, password, "enterprise_admin", enterpriseID)
}

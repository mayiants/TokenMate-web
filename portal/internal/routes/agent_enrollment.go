package routes

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/ai-proxy/ai-portal-go/internal/config"
	"github.com/ai-proxy/ai-portal-go/internal/db"
	"github.com/ai-proxy/ai-portal-go/pkg/audit"
	"github.com/ai-proxy/ai-portal-go/pkg/crypto"
	"github.com/ai-proxy/ai-portal-go/pkg/mail"
	"github.com/ai-proxy/ai-portal-go/pkg/metrics"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// AgentEnrollmentRoutes serves the PC agent: OTP-verified enrollment that
// exchanges a one-time session for an enterprise sub-key, plus heartbeat and
// adapter manifest endpoints. Mounted under /api/v1/agent without JWT auth —
// enrollment authorizes via email OTP + one-time session token, heartbeat
// authorizes via the issued sub-key itself.
type AgentEnrollmentRoutes struct {
	db     *db.DB
	cfg    *config.Config
	logger *zap.Logger
	audit  *audit.Logger
	mail   *mail.Sender
}

func NewAgentEnrollmentRoutes(database *db.DB, cfg *config.Config, logger *zap.Logger, auditLog *audit.Logger, mailSender *mail.Sender) *AgentEnrollmentRoutes {
	return &AgentEnrollmentRoutes{db: database, cfg: cfg, logger: logger, audit: auditLog, mail: mailSender}
}

const (
	agentOTPTTL         = 10 * time.Minute
	agentSessionTTL     = 10 * time.Minute
	agentOTPMaxPerEmail = 3
	agentOTPMaxPerIP    = 10
	agentTokenBytes     = 32
	maxHostnameLen      = 64
	maxFingerprintLen   = 128
	minFingerprintLen   = 8
)

var hostnameSanitizer = regexp.MustCompile(`[^a-zA-Z0-9\-_.]`)

func (r *AgentEnrollmentRoutes) Register(rg *gin.RouterGroup) {
	rg.POST("/otp/send", r.sendOTP)
	rg.POST("/otp/verify", r.verifyOTP)
	rg.POST("/enroll/exchange", r.exchange)
	rg.POST("/heartbeat", r.heartbeat)
	rg.GET("/config", r.getConfig)
}

func agentError(c *gin.Context, status int, message string) {
	c.JSON(status, gin.H{"error": gin.H{"message": message}})
}

// sendOTP emails a 6-digit code to the member's corporate mailbox. Rate
// limits and responses mirror auth.go's sendCode so the endpoint cannot be
// used to enumerate member emails or drain SMTP.
func (r *AgentEnrollmentRoutes) sendOTP(c *gin.Context) {
	var req struct {
		Email string `json:"email" binding:"required,email"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		agentError(c, http.StatusBadRequest, "A valid email is required")
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))

	rateKey := "agent_otp:email_rate:" + email
	newCount, incrErr := r.db.Redis.Incr(c.Request.Context(), rateKey).Result()
	if incrErr != nil {
		r.logger.Warn("Redis error in agent OTP rate limit, skipping rate check", zap.Error(incrErr))
	} else {
		if newCount == 1 {
			r.db.Redis.Expire(c.Request.Context(), rateKey, time.Hour)
		}
		if newCount > agentOTPMaxPerEmail {
			agentError(c, http.StatusTooManyRequests, "Too many requests. Please try again later.")
			return
		}
	}

	ipKey := "agent_otp:ip_rate:" + c.ClientIP()
	ipCount, ipErr := r.db.Redis.Incr(c.Request.Context(), ipKey).Result()
	if ipErr != nil {
		r.logger.Warn("Redis error in agent OTP IP rate limit, skipping rate check", zap.Error(ipErr))
	} else {
		if ipCount == 1 {
			r.db.Redis.Expire(c.Request.Context(), ipKey, time.Hour)
		}
		if ipCount > agentOTPMaxPerIP {
			agentError(c, http.StatusTooManyRequests, "Too many requests. Please try again later.")
			return
		}
	}

	code := generateCode()
	key := "verification:agent_enroll:" + email
	ok, err := r.db.Redis.SetNX(c.Request.Context(), key, code, agentOTPTTL).Result()
	if err != nil {
		r.logger.Warn("Failed to store agent OTP in Redis", zap.Error(err))
		agentError(c, http.StatusInternalServerError, "Failed to generate code")
		return
	}
	if !ok {
		c.JSON(http.StatusOK, gin.H{"success": true, "message": "Verification code sent"})
		return
	}

	if r.mail.IsConfigured() {
		if err := r.mail.SendVerificationCode(email, code, GetLocale(c)); err != nil {
			r.logger.Warn("Failed to send agent OTP email", zap.Error(err), zap.String("email", email))
		}
	} else {
		r.logger.Debug("SMTP not configured, agent OTP generated", zap.String("email", email))
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Verification code sent", "ttl_seconds": int(agentOTPTTL.Seconds())})
}

// memberByEmail resolves an active enterprise member by email, accepting both
// direct enterprise users and enterprise_members rows.
func (r *AgentEnrollmentRoutes) memberByEmail(c *gin.Context, email string) (userID, name, enterpriseID string, found bool) {
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT u.id::text, u.name, u.enterprise_id::text FROM users u
		 WHERE u.email = $1 AND u.deleted_at IS NULL AND u.enterprise_id IS NOT NULL
		 UNION
		 SELECT u.id::text, u.name, em.enterprise_id::text
		 FROM users u JOIN enterprise_members em ON em.user_id = u.id AND em.enterprise_id IS NOT NULL AND em.removed_at IS NULL
		 WHERE u.email = $1 AND u.deleted_at IS NULL
		 LIMIT 1`,
		email,
	).Scan(&userID, &name, &enterpriseID)
	if err != nil {
		return "", "", "", false
	}
	return userID, name, enterpriseID, true
}

// verifyOTP checks the code and, on success, mints a one-time enrollment
// session. Deliberately returns the same error for a wrong code and a
// correct code for a non-member email.
func (r *AgentEnrollmentRoutes) verifyOTP(c *gin.Context) {
	var req struct {
		Email string `json:"email" binding:"required,email"`
		Code  string `json:"code" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		agentError(c, http.StatusBadRequest, "Email and code are required")
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))

	key := "verification:agent_enroll:" + email
	stored, err := r.db.Redis.Get(c.Request.Context(), key).Result()
	if err != nil || subtle.ConstantTimeCompare([]byte(stored), []byte(req.Code)) != 1 {
		agentError(c, http.StatusBadRequest, "Invalid or expired code")
		return
	}
	r.db.Redis.Del(c.Request.Context(), key)

	userID, name, enterpriseID, found := r.memberByEmail(c, email)
	if !found {
		agentError(c, http.StatusBadRequest, "Invalid or expired code")
		return
	}

	tokenBytes := make([]byte, agentTokenBytes)
	if _, err := rand.Read(tokenBytes); err != nil {
		agentError(c, http.StatusInternalServerError, "Failed to create enrollment session")
		return
	}
	token := hex.EncodeToString(tokenBytes)

	_, err = r.db.Pool.Exec(c.Request.Context(),
		`INSERT INTO agent_enroll_sessions (token_hash, user_id, enterprise_id, expires_at)
		 VALUES ($1, $2::uuid, $3::uuid, NOW() + $4 * INTERVAL '1 second')`,
		crypto.HashKeySHA256(token), userID, enterpriseID, int64(agentSessionTTL/time.Second),
	)
	if err != nil {
		r.logger.Error("Failed to create agent enrollment session", zap.Error(err))
		agentError(c, http.StatusInternalServerError, "Failed to create enrollment session")
		return
	}

	r.logger.Info("Agent enrollment session created",
		zap.String("user_id", userID), zap.String("enterprise_id", enterpriseID))
	c.JSON(http.StatusOK, gin.H{
		"success":       true,
		"session_token": token,
		"expires_in":    int(agentSessionTTL.Seconds()),
		"member_name":   name,
	})
}

// exchange consumes an enrollment session and issues the device-bound
// enterprise sub-key. The session is single-use: the UPDATE checks
// consumed_at atomically so two concurrent exchanges cannot both succeed.
func (r *AgentEnrollmentRoutes) exchange(c *gin.Context) {
	var req struct {
		SessionToken      string `json:"session_token" binding:"required"`
		DeviceFingerprint string `json:"device_fingerprint" binding:"required"`
		Hostname          string `json:"hostname"`
		OSVersion         string `json:"os_version"`
		AgentVersion      string `json:"agent_version"`
		Client            string `json:"client"`
		OS                string `json:"os"`
		GroupID           string `json:"group_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		agentError(c, http.StatusBadRequest, "session_token and device_fingerprint are required")
		return
	}

	fp := strings.TrimSpace(req.DeviceFingerprint)
	if len(fp) < minFingerprintLen || len(fp) > maxFingerprintLen {
		agentError(c, http.StatusBadRequest, "device_fingerprint must be 8-128 characters")
		return
	}
	hostname := hostnameSanitizer.ReplaceAllString(strings.TrimSpace(req.Hostname), "-")
	if len(hostname) > maxHostnameLen {
		hostname = hostname[:maxHostnameLen]
	}
	client := strings.ToLower(strings.TrimSpace(req.Client))
	if client == "" {
		client = "workbuddy"
	}
	osName := strings.ToLower(strings.TrimSpace(req.OS))
	if osName == "" {
		osName = "windows"
	}

	var sessionID, userID, enterpriseID string
	var consumedAt *time.Time
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT id::text, user_id::text, enterprise_id::text, consumed_at FROM agent_enroll_sessions
		 WHERE token_hash = $1 AND expires_at > NOW()`,
		crypto.HashKeySHA256(req.SessionToken),
	).Scan(&sessionID, &userID, &enterpriseID, &consumedAt)
	if err != nil {
		agentError(c, http.StatusUnauthorized, "Invalid or expired session")
		return
	}
	if consumedAt != nil {
		agentError(c, http.StatusConflict, "Session already used")
		return
	}

	tag, err := r.db.Pool.Exec(c.Request.Context(),
		`UPDATE agent_enroll_sessions SET consumed_at = NOW() WHERE id = $1::uuid AND consumed_at IS NULL`,
		sessionID,
	)
	if err != nil || tag.RowsAffected() == 0 {
		agentError(c, http.StatusConflict, "Session already used")
		return
	}

	// Re-verify membership at exchange time: the OTP may predate a removal.
	var isMember bool
	err = r.db.QueryRow(c.Request.Context(),
		`SELECT EXISTS (
			SELECT 1 FROM users u WHERE u.id = $1::uuid AND u.enterprise_id = $2::uuid AND u.deleted_at IS NULL
			UNION
			SELECT 1 FROM enterprise_members em WHERE em.user_id = $1::uuid AND em.enterprise_id = $2::uuid AND em.removed_at IS NULL
		)`,
		userID, enterpriseID,
	).Scan(&isMember)
	if err != nil || !isMember {
		agentError(c, http.StatusForbidden, "Membership no longer active")
		return
	}

	// Optional enterprise group: inherits model whitelist and rate limits,
	// same resolution rules as manual enterprise key creation.
	var groupID interface{}
	var allowedModels interface{}
	var keyRPM, keyTPM int64
	var selectedModels []string
	if req.GroupID != "" {
		var entModels []byte
		var entRPM int
		var entTPM int64
		err = r.db.QueryRow(c.Request.Context(),
			`SELECT COALESCE(g.models::text, '[]')::bytea, COALESCE(g.rpm, 0), COALESCE(g.tpm, 0)
			 FROM user_groups g WHERE g.id = $1::uuid AND g.deleted_at IS NULL
			   AND (g.enterprise_id = $2::uuid
			        OR (g.enterprise_id IS NULL AND g.group_type = 'enterprise' AND g.inherit_to_enterprises))`,
			req.GroupID, enterpriseID,
		).Scan(&entModels, &entRPM, &entTPM)
		if err != nil {
			agentError(c, http.StatusBadRequest, "Group not found in your enterprise")
			return
		}
		if len(entModels) > 0 && string(entModels) != "[]" && string(entModels) != "null" {
			_ = json.Unmarshal(entModels, &selectedModels)
			if len(selectedModels) > 0 {
				allowedModels = selectedModels
			}
		}
		if entRPM > 0 {
			keyRPM = int64(entRPM)
		}
		if entTPM > 0 {
			keyTPM = entTPM
		}
		groupID = req.GroupID
	}

	key := "sk-wgit-en-" + uuid.New().String()
	keyPrefix := key
	if len(key) > apiKeyPrefixDisplayLength {
		keyPrefix = key[:apiKeyPrefixDisplayLength] + "..."
	}
	keyHash := crypto.HashKeySHA256(key)
	keyEncrypted, encErr := crypto.Encrypt(key, r.cfg.Auth.EncryptionKey)
	if encErr != nil {
		r.logger.Error("Failed to encrypt agent-issued key", zap.Error(encErr))
		agentError(c, http.StatusInternalServerError, "Key encryption failed")
		return
	}

	var memberName string
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT name FROM users WHERE id = $1::uuid`, userID).Scan(&memberName)
	keyName := "[agent] " + memberName + "-pc-" + hostname
	if len(keyName) > 128 {
		keyName = keyName[:128]
	}

	var currentModel *string
	if len(selectedModels) > 0 {
		m := selectedModels[0]
		currentModel = &m
	}

	var keyID string
	err = r.db.QueryRow(c.Request.Context(),
		`INSERT INTO api_keys (user_id, name, key_hash, key_prefix, key_encrypted, key_type, status,
		   enterprise_id, assigned_user_id, group_id, access_mode, allowed_models, rate_limit_rpm, rate_limit_tpm,
		   model_routing_mode, current_model, selected_models, expires_at)
		 VALUES ($1::uuid, $2, $3, $4, $5, 'enterprise', 'active',
		   $6::uuid, $7::uuid, $8::uuid, 'enterprise', $9, $10, $11, 'single', $12, $13, NULL)
		 RETURNING id::text`,
		userID, keyName, keyHash, keyPrefix, keyEncrypted, enterpriseID, userID,
		groupID, allowedModels, keyRPM, keyTPM, currentModel, selectedModels,
	).Scan(&keyID)
	if err != nil {
		r.logger.Error("Failed to create agent-issued key", zap.Error(err))
		agentError(c, http.StatusInternalServerError, "Key creation failed")
		return
	}
	metrics.GetMetrics().ActiveKeys.Add(1)
	invalidateValidateCache(c.Request.Context(), r.db, r.logger, keyHash)

	// Re-enrollment of the same device revokes its previous key so a device
	// does not accumulate live keys across reinstalls.
	fpHash := crypto.HashKeySHA256(fp)
	var deviceID, oldKeyID string
	err = r.db.QueryRow(c.Request.Context(),
		`SELECT id::text, COALESCE(api_key_id::text, '') FROM agent_devices
		 WHERE user_id = $1::uuid AND fingerprint_hash = $2`,
		userID, fpHash,
	).Scan(&deviceID, &oldKeyID)
	if err != nil {
		// First enrollment of this device.
		err = r.db.QueryRow(c.Request.Context(),
			`INSERT INTO agent_devices (user_id, enterprise_id, api_key_id, fingerprint_hash, hostname, os_version, agent_version, last_seen_at)
			 VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, NOW())
			 RETURNING id::text`,
			userID, enterpriseID, keyID, fpHash, hostname, req.OSVersion, req.AgentVersion,
		).Scan(&deviceID)
	} else {
		_, err = r.db.Pool.Exec(c.Request.Context(),
			`UPDATE agent_devices SET api_key_id = $3::uuid, hostname = $4, os_version = $5,
			   agent_version = $6, status = 'active', last_seen_at = NOW(), updated_at = NOW()
			 WHERE id = $1::uuid AND user_id = $2::uuid`,
			deviceID, userID, keyID, hostname, req.OSVersion, req.AgentVersion,
		)
	}
	if err != nil {
		r.logger.Error("Failed to record agent device", zap.Error(err))
		agentError(c, http.StatusInternalServerError, "Device registration failed")
		return
	}
	if oldKeyID != "" && oldKeyID != keyID {
		var oldHash string
		_ = r.db.QueryRow(c.Request.Context(),
			`UPDATE api_keys SET status = 'revoked' WHERE id = $1::uuid AND status != 'revoked' RETURNING key_hash`,
			oldKeyID).Scan(&oldHash)
		if oldHash != "" {
			invalidateValidateCache(c.Request.Context(), r.db, r.logger, oldHash)
		}
	}

	if r.audit != nil {
		r.audit.Log(c.Request.Context(), userID, "", "enterprise", "key_created", "info",
			"Agent enrollment issued enterprise key for device "+hostname)
	}
	writeEnterpriseAudit(c.Request.Context(), r.db, r.logger, enterpriseAuditEntry{
		EnterpriseID: enterpriseID,
		Category:     "key",
		ChangeType:   "key_created",
		FieldName:    "key",
		UserID:       &userID,
		ChangedBy:    userID,
		NewValueStr:  &keyName,
		TargetType:   "key",
		TargetID:     keyID,
		Reason:       "Key issued via agent enrollment",
	})

	c.JSON(http.StatusOK, gin.H{
		"success":      true,
		"api_key":      key,
		"key_id":       keyID,
		"device_id":    deviceID,
		"api_base_url": r.cfg.App.BaseURL,
		"adapter":      r.adapterManifest(c, client, osName),
	})
}

func (r *AgentEnrollmentRoutes) adapterManifest(c *gin.Context, client, osName string) gin.H {
	var configPath, method, baseVar, keyVar string
	var manifest []byte
	var version int
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT config_path, inject_method, env_base_url_var, env_api_key_var, manifest::text, manifest_version
		 FROM agent_adapters WHERE client = $1 AND os = $2 AND active`,
		client, osName,
	).Scan(&configPath, &method, &baseVar, &keyVar, &manifest, &version)
	if err != nil {
		return gin.H{"status": "no_adapter"}
	}
	var m gin.H
	_ = json.Unmarshal(manifest, &m)
	return gin.H{
		"client":           client,
		"os":               osName,
		"config_path":      configPath,
		"inject_method":    method,
		"env_base_url_var": baseVar,
		"env_api_key_var":  keyVar,
		"manifest":         m,
		"version":          version,
	}
}

// heartbeat authenticates with the issued sub-key and refreshes device
// liveness. Agents poll key_status so a revoked key is detected without
// waiting for the next request to fail at the proxy.
func (r *AgentEnrollmentRoutes) heartbeat(c *gin.Context) {
	authHeader := c.GetHeader("Authorization")
	rawKey := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
	if rawKey == "" || rawKey == authHeader {
		agentError(c, http.StatusUnauthorized, "Missing bearer key")
		return
	}

	var keyID, userID, keyStatus string
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT id::text, assigned_user_id::text, status FROM api_keys
		 WHERE key_hash = $1 AND deleted_at IS NULL`,
		crypto.HashKeySHA256(rawKey),
	).Scan(&keyID, &userID, &keyStatus)
	if err != nil {
		agentError(c, http.StatusUnauthorized, "Invalid key")
		return
	}

	var req struct {
		DeviceID          string `json:"device_id" binding:"required"`
		DeviceFingerprint string `json:"device_fingerprint" binding:"required"`
		AgentVersion      string `json:"agent_version"`
		ClientVersion     string `json:"client_version"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		agentError(c, http.StatusBadRequest, "device_id and device_fingerprint are required")
		return
	}

	var deviceKeyID, deviceFP string
	err = r.db.QueryRow(c.Request.Context(),
		`SELECT api_key_id::text, fingerprint_hash FROM agent_devices WHERE id = $1::uuid AND user_id = $2::uuid AND status = 'active'`,
		req.DeviceID, userID,
	).Scan(&deviceKeyID, &deviceFP)
	if err != nil || deviceKeyID != keyID || deviceFP != crypto.HashKeySHA256(req.DeviceFingerprint) {
		agentError(c, http.StatusForbidden, "Device does not match this key")
		return
	}

	_, _ = r.db.Pool.Exec(c.Request.Context(),
		`UPDATE agent_devices SET last_seen_at = NOW(), agent_version = $3, client_version = $4, updated_at = NOW()
		 WHERE id = $1::uuid AND user_id = $2::uuid`,
		req.DeviceID, userID, req.AgentVersion, req.ClientVersion,
	)

	var configVersion int
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT COALESCE(MAX(manifest_version), 0) FROM agent_adapters WHERE active`).Scan(&configVersion)

	c.JSON(http.StatusOK, gin.H{
		"success":        true,
		"server_time":    time.Now().UTC(),
		"key_status":     keyStatus,
		"config_version": configVersion,
	})
}

// getConfig serves the adapter manifest for a client/OS pair. The manifest
// contains no secrets, so the endpoint stays public; agents fetch it at
// enrollment and whenever config_version advances.
func (r *AgentEnrollmentRoutes) getConfig(c *gin.Context) {
	client := strings.ToLower(strings.TrimSpace(c.DefaultQuery("client", "workbuddy")))
	osName := strings.ToLower(strings.TrimSpace(c.DefaultQuery("os", "windows")))
	manifest := r.adapterManifest(c, client, osName)
	if manifest["status"] == "no_adapter" {
		agentError(c, http.StatusNotFound, "No adapter for this client and OS")
		return
	}
	c.JSON(http.StatusOK, manifest)
}

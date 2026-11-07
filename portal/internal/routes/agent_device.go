package routes

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/mayiants/ai-portal-go/internal/config"
	"github.com/mayiants/ai-portal-go/internal/db"
	"github.com/mayiants/ai-portal-go/internal/middleware"
	"github.com/mayiants/ai-portal-go/pkg/crypto"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

var errDeviceConsumed = errors.New("device auth session already consumed")

// AgentDeviceRoutes implements the device authorization flow (RFC 8626
// style) as the primary enrollment path: the agent opens a browser to the
// verification URL, the logged-in member approves, the agent polls and
// exchanges the one-time session via the existing /enroll/exchange.
// The agent-facing create/poll endpoints are unauthenticated (the device
// code IS the credential); the browser-facing endpoints require a session.
type AgentDeviceRoutes struct {
	db     *db.DB
	cfg    *config.Config
	logger *zap.Logger
	auth   *middleware.Auth
}

func NewAgentDeviceRoutes(database *db.DB, cfg *config.Config, logger *zap.Logger, auth *middleware.Auth) *AgentDeviceRoutes {
	return &AgentDeviceRoutes{db: database, cfg: cfg, logger: logger, auth: auth}
}

const (
	deviceCodeBytes = 16 // 128-bit unguessable code
	deviceAuthTTL   = 10 * time.Minute
	maxDeviceName   = 128
)

func (r *AgentDeviceRoutes) Register(rg *gin.RouterGroup) {
	rg.POST("/device", r.create)
	rg.POST("/device/poll", r.poll)

	sess := rg.Group("")
	sess.Use(r.auth.OptionalAuth())
	sess.GET("/device/authorize", r.authorizePage)
	sess.POST("/device/decide", r.decide)
}

func deviceError(c *gin.Context, status int, msg string) {
	c.JSON(status, gin.H{"success": false, "error": gin.H{"message": msg}})
}

// create starts a device session and returns the code plus the browser URL.
func (r *AgentDeviceRoutes) create(c *gin.Context) {
	var req struct {
		DeviceName string `json:"device_name"`
	}
	_ = c.ShouldBindJSON(&req)
	name := strings.TrimSpace(req.DeviceName)
	if len(name) > maxDeviceName {
		name = name[:maxDeviceName]
	}

	buf := make([]byte, deviceCodeBytes)
	if _, err := rand.Read(buf); err != nil {
		deviceError(c, http.StatusInternalServerError, "Failed to create device session")
		return
	}
	code := hex.EncodeToString(buf)

	_, err := r.db.Pool.Exec(c.Request.Context(),
		`INSERT INTO agent_device_auth (code_hash, device_name, expires_at)
		 VALUES ($1, $2, NOW() + $3 * INTERVAL '1 second')`,
		crypto.HashKeySHA256(code), name, int64(deviceAuthTTL/time.Second),
	)
	if err != nil {
		r.logger.Error("Failed to create device auth session", zap.Error(err))
		deviceError(c, http.StatusInternalServerError, "Failed to create device session")
		return
	}

	verification := strings.TrimRight(r.cfg.App.BaseURL, "/") +
		"/api/v1/agent/device/authorize?code=" + code
	c.JSON(http.StatusOK, gin.H{
		"success":          true,
		"code":             code,
		"verification_url": verification,
		"expires_in":       int(deviceAuthTTL.Seconds()),
	})
}

// poll is called by the agent until the member approves (or it expires).
func (r *AgentDeviceRoutes) poll(c *gin.Context) {
	var req struct {
		Code string `json:"code" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		deviceError(c, http.StatusBadRequest, "code is required")
		return
	}

	var id, status, tokenEnc string
	var expiresAt time.Time
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT id::text, status, session_token_enc, expires_at FROM agent_device_auth
		 WHERE code_hash = $1`, crypto.HashKeySHA256(strings.TrimSpace(req.Code)),
	).Scan(&id, &status, &tokenEnc, &expiresAt)
	if err != nil {
		deviceError(c, http.StatusNotFound, "Unknown device code")
		return
	}
	if time.Now().After(expiresAt) {
		if status == "pending" {
			_, _ = r.db.Pool.Exec(c.Request.Context(),
				`UPDATE agent_device_auth SET status = 'expired' WHERE id = $1::uuid`, id)
		}
		c.JSON(http.StatusOK, gin.H{"status": "expired"})
		return
	}

	switch status {
	case "pending":
		c.JSON(http.StatusOK, gin.H{"status": "pending"})
	case "rejected":
		c.JSON(http.StatusOK, gin.H{"status": "rejected"})
	case "approved":
		token, err := crypto.Decrypt(tokenEnc, r.cfg.Auth.EncryptionKey)
		if err != nil || token == "" {
			r.logger.Error("Failed to decrypt device session token", zap.Error(err))
			deviceError(c, http.StatusInternalServerError, "Session decryption failed")
			return
		}
		// Single delivery: clear the ciphertext so a second poll cannot
		// re-fetch the token after the exchange consumed it.
		_, _ = r.db.Pool.Exec(c.Request.Context(),
			`UPDATE agent_device_auth SET session_token_enc = '' WHERE id = $1::uuid AND session_token_enc <> ''`, id)
		// Identify the approver so the agent can fill member fields in its
		// local state (status display, server-side reporting dimensions).
		var memberName, memberEmail, userID string
		_ = r.db.QueryRow(c.Request.Context(),
			`SELECT COALESCE(user_id::text, '') FROM agent_device_auth WHERE id = $1::uuid`,
			id).Scan(&userID)
		if userID != "" {
			_ = r.db.QueryRow(c.Request.Context(),
				`SELECT name, email FROM users WHERE id = $1::uuid`, userID).
				Scan(&memberName, &memberEmail)
		}
		c.JSON(http.StatusOK, gin.H{
			"status":        "approved",
			"session_token": token,
			"expires_in":    int(time.Until(expiresAt).Seconds()),
			"member_name":   memberName,
			"member_email":  memberEmail,
		})
	default:
		c.JSON(http.StatusOK, gin.H{"status": status})
	}
}

// authorizePage is the browser-facing confirmation page.
func (r *AgentDeviceRoutes) authorizePage(c *gin.Context) {
	code := strings.TrimSpace(c.Query("code"))
	if code == "" {
		r.renderMessage(c, http.StatusBadRequest, "链接缺少授权码", "请从 woodgit-agent 重新发起。")
		return
	}
	userID := c.GetString("user_id")
	if userID == "" {
		// Not logged in: offer a login link that opens in a new tab; the
		// authorize URL stays in this tab and works after a refresh.
		loginURL := "/login"
		r.renderHTML(c, http.StatusOK, `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8">
<title>设备授权 — 需要登录</title><style>body{font-family:system-ui,sans-serif;max-width:480px;margin:60px auto;padding:0 20px;color:#1f2937}
a.btn{display:inline-block;background:#2563eb;color:#fff;padding:10px 22px;border-radius:8px;text-decoration:none}</style></head><body>
<h2>授权此设备需要先登录</h2>
<p>1. 点击 <a class="btn" href="`+loginURL+`" target="_blank">去登录</a>（新窗口打开）<br>
2. 登录成功后<strong>回到本页刷新</strong>，即可看到授权确认。</p>
</body></html>`)
		return
	}

	var status, deviceName string
	var expiresAt time.Time
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT status, device_name, expires_at FROM agent_device_auth WHERE code_hash = $1`,
		crypto.HashKeySHA256(code),
	).Scan(&status, &deviceName, &expiresAt)
	if err != nil || status != "pending" || time.Now().After(expiresAt) {
		r.renderMessage(c, http.StatusGone, "授权链接已失效", "请从 woodgit-agent 重新发起授权。")
		return
	}

	var memberName string
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT name FROM users WHERE id = $1::uuid`, userID).Scan(&memberName)
	if deviceName == "" {
		deviceName = "未知设备"
	}

	esc := func(s string) string {
		r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&#34;")
		return r.Replace(s)
	}
	r.renderHTML(c, http.StatusOK, `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8">
<title>设备授权</title><style>body{font-family:system-ui,sans-serif;max-width:480px;margin:60px auto;padding:0 20px;color:#1f2937}
.card{border:1px solid #e5e7eb;border-radius:12px;padding:24px}
button{padding:10px 22px;border-radius:8px;border:0;cursor:pointer;font-size:15px}
.ok{background:#16a34a;color:#fff}.no{background:#e5e7eb;color:#374151;margin-left:10px}
small{color:#6b7280}</style></head><body><div class="card">
<h2>授权新设备</h2>
<p><strong>`+esc(memberName)+`</strong>，设备 <strong>`+esc(deviceName)+`</strong> 请求接入企业 AI 网关。</p>
<p><small>授权后该设备将获得一把设备绑定的访问密钥，可在管理后台随时吊销。链接 10 分钟内有效。</small></p>
<form method="post" action="/api/v1/agent/device/decide" style="display:inline">
  <input type="hidden" name="code" value="`+esc(code)+`">
  <input type="hidden" name="decision" value="approve">
  <button class="ok" type="submit">授权</button>
</form>
<form method="post" action="/api/v1/agent/device/decide" style="display:inline">
  <input type="hidden" name="code" value="`+esc(code)+`">
  <input type="hidden" name="decision" value="reject">
  <button class="no" type="submit">拒绝</button>
</form>
</div></body></html>`)
}

// decide approves or rejects a pending device session (browser, session required).
func (r *AgentDeviceRoutes) decide(c *gin.Context) {
	userID := c.GetString("user_id")
	if userID == "" {
		deviceError(c, http.StatusUnauthorized, "Login required")
		return
	}
	code := strings.TrimSpace(c.PostForm("code"))
	decision := strings.TrimSpace(c.PostForm("decision"))
	if code == "" || (decision != "approve" && decision != "reject") {
		r.renderMessage(c, http.StatusBadRequest, "请求参数无效", "请从授权页面重新发起。")
		return
	}

	var id, status string
	var expiresAt time.Time
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT id::text, status, expires_at FROM agent_device_auth WHERE code_hash = $1 FOR UPDATE`,
		crypto.HashKeySHA256(code),
	).Scan(&id, &status, &expiresAt)
	if err != nil || status == "" {
		r.renderMessage(c, http.StatusNotFound, "授权会话不存在", "")
		return
	}
	if status != "pending" || time.Now().After(expiresAt) {
		r.renderMessage(c, http.StatusGone, "该授权请求已被处理或已过期", "")
		return
	}

	if decision == "reject" {
		_, _ = r.db.Pool.Exec(c.Request.Context(),
			`UPDATE agent_device_auth SET status = 'rejected', decided_at = NOW() WHERE id = $1::uuid`, id)
		r.renderMessage(c, http.StatusOK, "已拒绝该设备的授权请求", "")
		return
	}

	// Approve: resolve the member's enterprise (same membership rules as the
	// OTP exchange) and mint a one-time enroll session for the agent.
	var enterpriseID string
	err = r.db.QueryRow(c.Request.Context(),
		`SELECT enterprise_id::text FROM users
		 WHERE id = $1::uuid AND enterprise_id IS NOT NULL AND deleted_at IS NULL
		 UNION
		 SELECT em.enterprise_id::text FROM enterprise_members em
		 WHERE em.user_id = $1::uuid AND em.enterprise_id IS NOT NULL AND em.removed_at IS NULL
		 LIMIT 1`,
		userID,
	).Scan(&enterpriseID)
	if err != nil || enterpriseID == "" {
		r.renderMessage(c, http.StatusForbidden, "当前账号不属于任何企业", "请联系管理员将您加入企业后再试。")
		return
	}

	tokenBytes := make([]byte, agentTokenBytes)
	if _, err := rand.Read(tokenBytes); err != nil {
		r.renderMessage(c, http.StatusInternalServerError, "创建授权会话失败", "")
		return
	}
	token := hex.EncodeToString(tokenBytes)
	encToken, err := crypto.Encrypt(token, r.cfg.Auth.EncryptionKey)
	if err != nil {
		r.renderMessage(c, http.StatusInternalServerError, "创建授权会话失败", "")
		return
	}

	txErr := r.db.WithTx(c.Request.Context(), func(tx pgx.Tx) error {
		if _, err := tx.Exec(c.Request.Context(),
			`INSERT INTO agent_enroll_sessions (token_hash, user_id, enterprise_id, expires_at)
			 VALUES ($1, $2::uuid, $3::uuid, NOW() + $4 * INTERVAL '1 second')`,
			crypto.HashKeySHA256(token), userID, enterpriseID, int64(agentSessionTTL/time.Second),
		); err != nil {
			return err
		}
		tag, err := tx.Exec(c.Request.Context(),
			`UPDATE agent_device_auth SET status = 'approved', user_id = $2::uuid,
			   session_token_enc = $3, decided_at = NOW()
			 WHERE id = $1::uuid AND status = 'pending'`,
			id, userID, encToken,
		)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errDeviceConsumed
		}
		return nil
	})
	if txErr != nil {
		r.logger.Warn("Device auth approve failed", zap.Error(txErr))
		r.renderMessage(c, http.StatusConflict, "该授权请求已被处理", "")
		return
	}
	r.logger.Info("Agent device authorized via browser",
		zap.String("user_id", userID), zap.String("enterprise_id", enterpriseID))
	r.renderMessage(c, http.StatusOK, "授权成功", "请回到客户端（woodgit-agent）继续。")
}

func (r *AgentDeviceRoutes) renderMessage(c *gin.Context, status int, title, hint string) {
	h := `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><title>` + title + `</title>
<style>body{font-family:system-ui,sans-serif;max-width:480px;margin:60px auto;padding:0 20px;color:#1f2937}</style></head><body>
<h2>` + title + `</h2><p>` + hint + `</p></body></html>`
	r.renderHTML(c, status, h)
}

func (r *AgentDeviceRoutes) renderHTML(c *gin.Context, status int, html string) {
	c.Data(status, "text/html; charset=utf-8", []byte(html))
}

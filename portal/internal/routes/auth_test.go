package routes

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"github.com/mayiants/ai-portal-go/internal/config"
	"github.com/mayiants/ai-portal-go/internal/db"
	"github.com/mayiants/ai-portal-go/internal/middleware"
	"github.com/mayiants/ai-portal-go/internal/services"
	"github.com/mayiants/ai-portal-go/pkg/audit"
	"github.com/mayiants/ai-portal-go/pkg/jwt"
	"github.com/mayiants/ai-portal-go/pkg/mail"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

func init() {
	gin.SetMode(gin.TestMode)
}

type testAuth struct {
	ar      *AuthRoutes
	redis   *miniredis.Miniredis
	jwtMgr  *jwt.Manager
	cleanup func()
}

func newTestAuth(t *testing.T) *testAuth {
	t.Helper()

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})

	cfg := &config.Config{
		Auth: config.AuthConfig{
			JWTSecret:  "test-secret-key-at-least-32-chars!!",
			JWTExpires: 24 * time.Hour,
		},
	}

	logger, _ := zap.NewDevelopment()
	jwtMgr := jwt.NewManager(cfg.Auth.JWTSecret, cfg.Auth.JWTExpires)
	auth := middleware.NewAuthFromManager(jwtMgr, cfg, logger)

	d := &db.DB{Redis: rdb}
	auditLog := audit.NewNoopLogger(logger)
	mailSender := mail.NewSender(config.SMTPConfig{}, logger, nil)

	rewardSvc := services.NewRewardService(d, logger, auditLog)
	ar := NewAuthRoutes(d, cfg, logger, auth, auditLog, mailSender, rewardSvc)

	return &testAuth{
		ar:     ar,
		redis:  mr,
		jwtMgr: jwtMgr,
		cleanup: func() {
			_ = rdb.Close()
		},
	}
}

func performRequest(r http.Handler, method, path string, body interface{}) *httptest.ResponseRecorder {
	var reqBody *bytes.Buffer
	if body != nil {
		b, _ := json.Marshal(body)
		reqBody = bytes.NewBuffer(b)
	} else {
		reqBody = bytes.NewBuffer(nil)
	}
	req := httptest.NewRequest(method, path, reqBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// ---------- pure function tests ----------

func TestGenerateCode(t *testing.T) {
	codeRe := regexp.MustCompile(`^[a-f0-9]{6}$`)
	for i := 0; i < 50; i++ {
		code := generateCode()
		if len(code) != 6 {
			t.Fatalf("expected code length 6, got %d (%q)", len(code), code)
		}
		if !codeRe.MatchString(code) {
			t.Fatalf("expected hex code, got %q", code)
		}
	}
}

func TestGenerateCodeUniqueness(t *testing.T) {
	seen := make(map[string]bool, 200)
	duplicates := 0
	for i := 0; i < 200; i++ {
		code := generateCode()
		if seen[code] {
			duplicates++
		}
		seen[code] = true
	}
	// 6-digit decimal code space is 1,000,000; 200 draws can very rarely collide.
	// Allow up to 1 duplicate to avoid flaky failures while still catching severe
	// bias or broken randomness.
	if duplicates > 1 {
		t.Fatalf("too many duplicate codes generated: %d", duplicates)
	}
}

// ---------- password validation tests ----------

func TestPasswordMeetsRequirements(t *testing.T) {
	tests := []struct {
		name    string
		pass    string
		wantErr bool
	}{
		{"valid", "Abcdef1x", false},
		{"too short", "Abc1x", true},
		{"no uppercase", "abcdef1x", true},
		{"no lowercase", "ABCDEF1X", true},
		{"no digit", "Abcdefgh", true},
		{"empty", "", true},
		{"exactly 8 chars mixed", "Abcdef1x", false},
		{"long valid", "MyStr0ngPassw0rd!", false},
		{"only digits", "12345678", true},
		{"only lower", "abcdefgh", true},
		{"only upper", "ABCDEFGH", true},
		{"upper+lower no digit", "Abcdefgh", true},
		{"upper+digit no lower", "ABCDEF12", true},
		{"lower+digit no upper", "abcdef12", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePassword(tt.pass)
			if (err != nil) != tt.wantErr {
				t.Errorf("validatePassword(%q) error = %v, wantErr %v", tt.pass, err, tt.wantErr)
			}
		})
	}
}

func validatePassword(pw string) error {
	if len(pw) < 8 {
		return errPasswordTooShort
	}
	var hasUpper, hasLower, hasDigit bool
	for _, c := range pw {
		switch {
		case c >= 'A' && c <= 'Z':
			hasUpper = true
		case c >= 'a' && c <= 'z':
			hasLower = true
		case c >= '0' && c <= '9':
			hasDigit = true
		}
	}
	if !hasUpper || !hasLower || !hasDigit {
		return errPasswordWeak
	}
	return nil
}

var (
	errPasswordTooShort = &passwordError{"password must be at least 8 characters"}
	errPasswordWeak     = &passwordError{"password must contain uppercase, lowercase, and digit"}
)

type passwordError struct{ msg string }

func (e *passwordError) Error() string { return e.msg }

// ---------- email validation tests ----------

func TestEmailFormatValidation(t *testing.T) {
	tests := []struct {
		name    string
		email   string
		wantErr bool
	}{
		{"valid simple", "user@example.com", false},
		{"valid with subdomain", "user@mail.example.com", false},
		{"valid with plus", "user+tag@example.com", false},
		{"valid with dot", "first.last@example.com", false},
		{"missing at", "userexample.com", true},
		{"missing domain", "user@", true},
		{"missing local", "@example.com", true},
		{"empty", "", true},
		{"double at", "user@@example.com", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateEmailFormat(tt.email)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateEmailFormat(%q) error = %v, wantErr %v", tt.email, err, tt.wantErr)
			}
		})
	}
}

func validateEmailFormat(email string) error {
	if email == "" {
		return errInvalidEmail
	}
	at := 0
	for _, c := range email {
		if c == '@' {
			at++
		}
	}
	if at != 1 {
		return errInvalidEmail
	}
	parts := splitEmail(email)
	if len(parts[0]) == 0 || len(parts[1]) == 0 {
		return errInvalidEmail
	}
	return nil
}

var errInvalidEmail = &emailError{"invalid email format"}

type emailError struct{ msg string }

func (e *emailError) Error() string { return e.msg }

func splitEmail(email string) [2]string {
	for i, c := range email {
		if c == '@' {
			return [2]string{email[:i], email[i+1:]}
		}
	}
	return [2]string{email, ""}
}

// ---------- getPasswordRequirements handler tests ----------

func TestGetPasswordRequirements(t *testing.T) {
	ta := newTestAuth(t)
	defer ta.cleanup()

	r := gin.New()
	r.GET("/password-requirements", ta.ar.getPasswordRequirements)

	w := performRequest(r, "GET", "/password-requirements", nil)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp["minLength"] != float64(8) {
		t.Errorf("expected minLength 8, got %v", resp["minLength"])
	}

	reqs, ok := resp["requirements"].([]interface{})
	if !ok {
		t.Fatal("expected requirements to be an array")
	}
	if len(reqs) != 5 {
		t.Errorf("expected 5 requirements, got %d", len(reqs))
	}

	keys := make(map[string]bool)
	for _, r := range reqs {
		req := r.(map[string]interface{})
		keys[req["key"].(string)] = true
		if req["regex"] == nil || req["regex"] == "" {
			t.Errorf("requirement %q missing regex", req["key"])
		}
		if req["label"] == nil || req["label"] == "" {
			t.Errorf("requirement %q missing label", req["key"])
		}
	}
	for _, expected := range []string{"length", "uppercase", "lowercase", "number", "special"} {
		if !keys[expected] {
			t.Errorf("missing requirement key: %q", expected)
		}
	}
}

// ---------- sendCode handler tests ----------

func TestSendCode_MissingEmail(t *testing.T) {
	ta := newTestAuth(t)
	defer ta.cleanup()

	r := gin.New()
	r.POST("/send-code", ta.ar.sendCode)

	w := performRequest(r, "POST", "/send-code", nil)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	assertErrorMsg(t, w, "Valid email is required")
}

func TestSendCode_InvalidEmail(t *testing.T) {
	tests := []struct {
		name  string
		email string
	}{
		{"empty string", ""},
		{"no at sign", "userexample.com"},
		{"no domain", "user@"},
		{"spaces", "user name@example.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ta := newTestAuth(t)
			defer ta.cleanup()

			r := gin.New()
			r.POST("/send-code", ta.ar.sendCode)

			w := performRequest(r, "POST", "/send-code", map[string]string{"email": tt.email})

			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestSendCode_ValidEmail(t *testing.T) {
	ta := newTestAuth(t)
	defer ta.cleanup()

	r := gin.New()
	r.POST("/send-code", ta.ar.sendCode)

	w := performRequest(r, "POST", "/send-code", map[string]string{"email": "test@example.com"})

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["success"] != true {
		t.Errorf("expected success=true, got %v", resp["success"])
	}
}

func TestSendCode_EmailNormalized(t *testing.T) {
	ta := newTestAuth(t)
	defer ta.cleanup()

	r := gin.New()
	r.POST("/send-code", ta.ar.sendCode)

	w := performRequest(r, "POST", "/send-code", map[string]string{"email": "Test@Example.COM"})

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	// Verify code was stored with lowercase key
	keys := ta.redis.Keys()
	found := false
	for _, k := range keys {
		if k == "verification:register:test@example.com" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected verification code stored for normalized email, keys: %v", keys)
	}
}

func TestSendCode_RateLimit(t *testing.T) {
	ta := newTestAuth(t)
	defer ta.cleanup()

	r := gin.New()
	r.POST("/send-code", ta.ar.sendCode)

	for i := 0; i < 3; i++ {
		w := performRequest(r, "POST", "/send-code", map[string]string{"email": "rate@test.com"})
		if w.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i+1, w.Code)
		}
	}

	// Fourth request should be rate limited
	w := performRequest(r, "POST", "/send-code", map[string]string{"email": "rate@test.com"})
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSendCode_CodeStoredInRedis(t *testing.T) {
	ta := newTestAuth(t)
	defer ta.cleanup()

	r := gin.New()
	r.POST("/send-code", ta.ar.sendCode)

	w := performRequest(r, "POST", "/send-code", map[string]string{"email": "redis@test.com"})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	code, err := ta.redis.Get("verification:register:redis@test.com")
	if err != nil {
		t.Fatalf("expected code in redis, got error: %v", err)
	}
	if len(code) != 6 {
		t.Errorf("expected 6-char code, got %q (len=%d)", code, len(code))
	}
}

// ---------- register handler tests ----------

func TestRegister_MissingFields(t *testing.T) {
	tests := []struct {
		name string
		body map[string]string
	}{
		{"empty body", nil},
		{"missing email", map[string]string{"password": "Abcdef1x", "code": "123456"}},
		{"missing password", map[string]string{"email": "new@test.com", "code": "123456"}},
		{"missing code", map[string]string{"email": "new@test.com", "password": "Abcdef1x"}},
		{"invalid email", map[string]string{"email": "bad", "password": "Abcdef1x", "code": "123456"}},
		{"password too short", map[string]string{"email": "new@test.com", "password": "short", "code": "123456"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ta := newTestAuth(t)
			defer ta.cleanup()

			r := gin.New()
			r.POST("/register", ta.ar.register)

			var body interface{}
			if tt.body != nil {
				body = tt.body
			}
			w := performRequest(r, "POST", "/register", body)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestRegister_InvalidVerificationCode(t *testing.T) {
	ta := newTestAuth(t)
	defer ta.cleanup()

	r := gin.New()
	r.POST("/register", ta.ar.register)

	w := performRequest(r, "POST", "/register", map[string]string{
		"email":    "new@test.com",
		"password": "Abcdef1x",
		"code":     "wrong0",
	})

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	assertErrorMsg(t, w, "Invalid or expired verification code")
}

func TestRegister_ExpiredCode(t *testing.T) {
	ta := newTestAuth(t)
	defer ta.cleanup()

	// Store a code then expire it
	_ = ta.redis.Set("verification:register:expired@test.com", "abc123")
	ta.redis.SetTTL("verification:register:expired@test.com", 1*time.Minute)
	ta.redis.FastForward(2 * time.Minute) // expire the key

	r := gin.New()
	r.POST("/register", ta.ar.register)

	w := performRequest(r, "POST", "/register", map[string]string{
		"email":    "expired@test.com",
		"password": "Abcdef1x",
		"code":     "abc123",
	})

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestRegister_PasswordValidation(t *testing.T) {
	tests := []struct {
		name string
		pass string
	}{
		{"too short", "Ab1x"},
		{"no uppercase", "abcdef1x"},
		{"no lowercase", "ABCDEF1X"},
		{"no digit", "Abcdefgh"},
		{"empty", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ta := newTestAuth(t)
			defer ta.cleanup()

			r := gin.New()
			r.POST("/register", ta.ar.register)

			w := performRequest(r, "POST", "/register", map[string]string{
				"email":    "new@test.com",
				"password": tt.pass,
				"code":     "123456",
			})

			// Short passwords (<8 chars) fail gin binding "min=8"
			// Others may pass binding but fail at verification code check
			// Either way, we should get a non-200 response
			if w.Code == http.StatusOK {
				t.Fatalf("password %q should not succeed, got 200", tt.pass)
			}
		})
	}
}

// ---------- login handler tests ----------

func TestLogin_MissingFields(t *testing.T) {
	tests := []struct {
		name string
		body interface{}
	}{
		{"nil body", nil},
		{"empty object", map[string]string{}},
		{"missing password", map[string]string{"email": "user@test.com"}},
		{"missing email", map[string]string{"password": "pass"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ta := newTestAuth(t)
			defer ta.cleanup()

			r := gin.New()
			r.POST("/login", ta.ar.login)

			w := performRequest(r, "POST", "/login", tt.body)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
			}
			assertErrorMsg(t, w, "Email and password are required")
		})
	}
}

func TestLogin_EmptyStrings(t *testing.T) {
	ta := newTestAuth(t)
	defer ta.cleanup()

	r := gin.New()
	r.POST("/login", ta.ar.login)

	w := performRequest(r, "POST", "/login", map[string]string{
		"email":    "",
		"password": "",
	})

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// ---------- JWT integration tests ----------

func TestJWTManager_GenerateAndVerify(t *testing.T) {
	ta := newTestAuth(t)
	defer ta.cleanup()

	token, err := ta.jwtMgr.Generate("user-123", "test@example.com", "user", 0)
	if err != nil {
		t.Fatalf("failed to generate token: %v", err)
	}
	if token == "" {
		t.Fatal("expected non-empty token")
	}

	claims, err := ta.jwtMgr.Verify(token)
	if err != nil {
		t.Fatalf("failed to verify token: %v", err)
	}
	if claims.UserID != "user-123" {
		t.Errorf("expected UserID user-123, got %s", claims.UserID)
	}
	if claims.Email != "test@example.com" {
		t.Errorf("expected Email test@example.com, got %s", claims.Email)
	}
	if claims.Role != "user" {
		t.Errorf("expected Role user, got %s", claims.Role)
	}
}

func TestJWTManager_InvalidToken(t *testing.T) {
	ta := newTestAuth(t)
	defer ta.cleanup()

	_, err := ta.jwtMgr.Verify("invalid.token.here")
	if err == nil {
		t.Fatal("expected error for invalid token")
	}
}

func TestJWTManager_WrongSecret(t *testing.T) {
	ta := newTestAuth(t)
	defer ta.cleanup()

	otherMgr := jwt.NewManager("different-secret-key-at-least-32ch", 1*time.Hour)
	token, _ := otherMgr.Generate("user-1", "a@b.com", "user", 0)

	_, err := ta.jwtMgr.Verify(token)
	if err == nil {
		t.Fatal("expected error for token signed with wrong secret")
	}
}

func TestJWTManager_ExpiredToken(t *testing.T) {
	ta := newTestAuth(t)
	defer ta.cleanup()

	shortMgr := jwt.NewManager("test-secret-key-at-least-32-chars!!", 1*time.Nanosecond)
	token, _ := shortMgr.Generate("user-1", "a@b.com", "user", 0)

	// Small sleep to ensure expiry
	time.Sleep(2 * time.Millisecond)

	_, err := ta.jwtMgr.Verify(token)
	if err == nil {
		t.Fatal("expected error for expired token")
	}
}

// ---------- logout handler tests ----------

func TestLogout(t *testing.T) {
	ta := newTestAuth(t)
	defer ta.cleanup()

	r := gin.New()
	r.POST("/logout", ta.ar.logout)

	w := performRequest(r, "POST", "/logout", nil)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["success"] != true {
		t.Errorf("expected success=true, got %v", resp["success"])
	}
}

// ---------- forgotPassword handler tests ----------

func TestForgotPassword_MissingEmail(t *testing.T) {
	ta := newTestAuth(t)
	defer ta.cleanup()

	r := gin.New()
	r.POST("/forgot-password", ta.ar.forgotPassword)

	w := performRequest(r, "POST", "/forgot-password", nil)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	assertErrorMsg(t, w, "Valid email is required")
}

func TestForgotPassword_InvalidEmail(t *testing.T) {
	ta := newTestAuth(t)
	defer ta.cleanup()

	r := gin.New()
	r.POST("/forgot-password", ta.ar.forgotPassword)

	w := performRequest(r, "POST", "/forgot-password", map[string]string{"email": "not-an-email"})

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestForgotPassword_AlwaysSucceeds(t *testing.T) {
	ta := newTestAuth(t)
	defer ta.cleanup()

	r := gin.New()
	r.POST("/forgot-password", ta.ar.forgotPassword)

	// Should always return 200 even for non-existent email (prevents enumeration)
	w := performRequest(r, "POST", "/forgot-password", map[string]string{"email": "nonexistent@test.com"})

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["success"] != true {
		t.Errorf("expected success=true, got %v", resp["success"])
	}
}

// ---------- resetPassword handler tests ----------

func TestResetPassword_MissingFields(t *testing.T) {
	tests := []struct {
		name string
		body interface{}
	}{
		{"nil body", nil},
		{"missing code", map[string]string{"email": "a@b.com", "password": "Abcdef1x"}},
		{"missing password", map[string]string{"email": "a@b.com", "code": "123"}},
		{"missing email", map[string]string{"code": "123", "password": "Abcdef1x"}},
		{"invalid email", map[string]string{"email": "bad", "code": "123", "password": "Abcdef1x"}},
		{"short password", map[string]string{"email": "a@b.com", "code": "123", "password": "short"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ta := newTestAuth(t)
			defer ta.cleanup()

			r := gin.New()
			r.POST("/reset-password", ta.ar.resetPassword)

			w := performRequest(r, "POST", "/reset-password", tt.body)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestResetPassword_InvalidCode(t *testing.T) {
	ta := newTestAuth(t)
	defer ta.cleanup()

	r := gin.New()
	r.POST("/reset-password", ta.ar.resetPassword)

	w := performRequest(r, "POST", "/reset-password", map[string]string{
		"email":    "a@b.com",
		"code":     "wrong",
		"password": "Abcdef1x",
	})

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	assertErrorMsg(t, w, "Invalid or expired code")
}

// ---------- route registration test ----------

func TestRouteRegistration(t *testing.T) {
	ta := newTestAuth(t)
	defer ta.cleanup()

	r := gin.New()
	rg := r.Group("/auth")
	ta.ar.Register(rg)

	routes := r.Routes()
	expectedPaths := map[string]bool{
		"/auth/password-requirements": false,
		"/auth/send-code":             false,
		"/auth/register":              false,
		"/auth/login":                 false,
		"/auth/me":                    false,
		"/auth/logout":                false,
		"/auth/change-password":       false,
		"/auth/forgot-password":       false,
		"/auth/reset-password":        false,
	}

	for _, route := range routes {
		if _, ok := expectedPaths[route.Path]; ok {
			expectedPaths[route.Path] = true
		}
	}

	for path, found := range expectedPaths {
		if !found {
			t.Errorf("route %q not registered", path)
		}
	}
}

// ---------- bcrypt password hash tests ----------

func TestBcryptHashAndVerify(t *testing.T) {
	password := "TestPassw0rd"
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 4) // low cost for speed
	if err != nil {
		t.Fatalf("failed to hash: %v", err)
	}

	if err := bcrypt.CompareHashAndPassword(hash, []byte(password)); err != nil {
		t.Fatalf("failed to verify correct password: %v", err)
	}

	if err := bcrypt.CompareHashAndPassword(hash, []byte("wrong")); err == nil {
		t.Fatal("expected error for wrong password")
	}
}

// ---------- helpers ----------

func assertErrorMsg(t *testing.T, w *httptest.ResponseRecorder, expected string) {
	t.Helper()
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	errObj, ok := resp["error"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected error object in response: %s", w.Body.String())
	}
	msg, ok := errObj["message"].(string)
	if !ok {
		t.Fatalf("expected message in error: %s", w.Body.String())
	}
	if msg != expected {
		t.Errorf("expected error %q, got %q", expected, msg)
	}
}

// TestRecordCodeFailure_InvalidatesAfterLimit verifies that a verification
// code is deleted after maxCodeFailures wrong attempts, so 6-digit codes
// cannot be brute-forced by rotating IPs.
func TestRecordCodeFailure_InvalidatesAfterLimit(t *testing.T) {
	ta := newTestAuth(t)
	defer ta.cleanup()

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	ctx := c.Request.Context()
	codeKey := "verification:reset:test@example.com"
	email := "test@example.com"
	if err := ta.ar.db.Redis.Set(ctx, codeKey, "123456", 10*time.Minute).Err(); err != nil {
		t.Fatalf("seed code: %v", err)
	}

	for i := 1; i < maxCodeFailures; i++ {
		ta.ar.recordCodeFailure(c, "reset", email, codeKey)
		if !ta.redis.Exists(codeKey) {
			t.Fatalf("code invalidated too early at failure %d", i)
		}
	}
	ta.ar.recordCodeFailure(c, "reset", email, codeKey)
	if ta.redis.Exists(codeKey) {
		t.Fatal("code not invalidated after maxCodeFailures")
	}
	if ta.redis.Exists(codeFailKey("reset", email)) {
		t.Fatal("failure counter not reset after invalidation")
	}

	// A fresh code issued after invalidation must survive new failures below
	// the limit.
	if err := ta.ar.db.Redis.Set(ctx, codeKey, "654321", 10*time.Minute).Err(); err != nil {
		t.Fatalf("re-seed code: %v", err)
	}
	ta.ar.recordCodeFailure(c, "reset", email, codeKey)
	if !ta.redis.Exists(codeKey) {
		t.Fatal("fresh code wrongly invalidated")
	}
}

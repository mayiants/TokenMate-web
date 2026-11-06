package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ai-proxy/ai-portal-go/internal/config"
	"github.com/ai-proxy/ai-portal-go/internal/db"
	"github.com/ai-proxy/ai-portal-go/internal/services"
	"github.com/ai-proxy/ai-portal-go/internal/testutil"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

func testPreConsumeDB(t *testing.T) *db.DB {
	t.Helper()
	ctx := context.Background()
	if err := db.InitTestSchema(ctx, testutil.PostgresDSN(), zap.NewNop()); err != nil {
		t.Fatalf("init test schema: %v", err)
	}
	pool, err := pgxpool.New(ctx, testutil.PostgresDSN())
	if err != nil {
		t.Fatalf("connect to test db: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("ping test db: %v", err)
	}
	_, _ = pool.Exec(ctx, `ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS name VARCHAR(255)`)
	_, _ = pool.Exec(ctx, `ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS status VARCHAR(20) DEFAULT 'active'`)
	return &db.DB{Pool: pool, Redis: nil}
}

func newTestPreConsumeRoutes(t *testing.T, database *db.DB) *WebhookRoutes {
	t.Helper()
	cfg := &config.Config{
		Portal: config.PortalConfig{SyncSecret: "test-secret"},
	}
	logger := zap.NewNop()
	exchangeSvc := services.NewExchangeService(database, logger)
	return NewWebhookRoutes(database, cfg, logger, nil, exchangeSvc, nil)
}

func preConsumeCtx(body interface{}) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/webhook/pre-consume", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	return c, w
}

func settleCtx(body interface{}) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/webhook/settle", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	return c, w
}

func insertPreConsumeUser(t *testing.T, d *db.DB, balance, giftBalance int64, suffix string) string {
	t.Helper()
	ctx := context.Background()
	id := uuid.NewString()
	email := fmt.Sprintf("preconsume-%s-%d@test.com", suffix, time.Now().UnixNano())
	_, err := d.Pool.Exec(ctx,
		`INSERT INTO users (id, name, email, password_hash, role, balance, gift_balance, status, created_at, updated_at)
		 VALUES ($1::uuid, $2, $3, 'hash', 'user', $4, $5, 'active', NOW(), NOW())`,
		id, "PreConsume "+suffix, email, balance, giftBalance)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	return id
}

func insertPreConsumeKey(t *testing.T, d *db.DB, userID, keyType, suffix string) string {
	t.Helper()
	ctx := context.Background()
	keyHash := "preconsume-key-" + suffix + "-" + uuid.NewString()[:8]
	_, err := d.Pool.Exec(ctx,
		`INSERT INTO api_keys (user_id, key_hash, key_prefix, name, status, key_type, created_at, updated_at)
		 VALUES ($1::uuid, $2, $3, $4, 'active', $5, NOW(), NOW())`,
		userID, keyHash, "sk-pc-"+suffix, "PC Key "+suffix, keyType)
	if err != nil {
		t.Fatalf("insert key: %v", err)
	}
	return keyHash
}

func TestPreConsume_EstimatedCostZero(t *testing.T) {
	d := testPreConsumeDB(t)
	defer d.Close()
	r := newTestPreConsumeRoutes(t, d)
	userID := insertPreConsumeUser(t, d, 1000, 500, "zerocost")

	c, w := preConsumeCtx(PreConsumeWebhookRequest{
		UserID:        userID,
		RequestID:     uuid.NewString(),
		EstimatedCost: 0,
	})
	r.handlePreConsume(c)

	var resp struct {
		Success bool  `json:"success"`
		Amount  int64 `json:"amount"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !resp.Success {
		t.Errorf("expected success=true for zero cost, got false")
	}
	if resp.Amount != 0 {
		t.Errorf("expected amount=0 for zero cost, got %d", resp.Amount)
	}
}

func TestPreConsume_SufficientBalance(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	d := testPreConsumeDB(t)
	defer d.Close()
	d.Redis = rdb
	r := newTestPreConsumeRoutes(t, d)

	userID := insertPreConsumeUser(t, d, 10000, 0, "sufficient")
	reqID := uuid.NewString()

	c, w := preConsumeCtx(PreConsumeWebhookRequest{
		UserID:        userID,
		KeyHash:       insertPreConsumeKey(t, d, userID, "general", "sufficient"),
		Model:         "gpt-4",
		Provider:      "openai",
		InputTokens:   100,
		RequestID:     reqID,
		EstimatedCost: 5000,
	})
	r.handlePreConsume(c)

	var resp struct {
		Success   bool  `json:"success"`
		Rejected  bool  `json:"rejected"`
		Amount    int64 `json:"amount"`
		Remaining int64 `json:"remaining"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !resp.Success {
		t.Errorf("expected success=true")
	}
	if resp.Rejected {
		t.Errorf("expected rejected=false, got true")
	}
	if resp.Amount != 5000 {
		t.Errorf("expected amount=5000, got %d", resp.Amount)
	}
	if resp.Remaining != 5000 {
		t.Errorf("expected remaining=5000 (10000-5000), got %d", resp.Remaining)
	}

	freezeKey := "billing:freeze:" + reqID
	if !mr.Exists(freezeKey) {
		t.Errorf("freeze key %s not found in Redis", freezeKey)
	}
}

func TestPreConsume_InsufficientBalance(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	d := testPreConsumeDB(t)
	defer d.Close()
	d.Redis = rdb
	r := newTestPreConsumeRoutes(t, d)

	userID := insertPreConsumeUser(t, d, 1000, 0, "insufficient")

	c, w := preConsumeCtx(PreConsumeWebhookRequest{
		UserID:        userID,
		KeyHash:       insertPreConsumeKey(t, d, userID, "general", "insufficient"),
		RequestID:     uuid.NewString(),
		EstimatedCost: 5000,
	})
	r.handlePreConsume(c)

	var resp struct {
		Success   bool  `json:"success"`
		Rejected  bool  `json:"rejected"`
		Amount    int64 `json:"amount"`
		Remaining int64 `json:"remaining"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if w.Code != http.StatusPaymentRequired {
		t.Errorf("expected 402, got %d", w.Code)
	}
	if !resp.Rejected {
		t.Errorf("expected rejected=true for insufficient balance")
	}
}

func TestPreConsume_InactiveUser(t *testing.T) {
	d := testPreConsumeDB(t)
	defer d.Close()
	r := newTestPreConsumeRoutes(t, d)
	ctx := context.Background()

	userID := uuid.NewString()
	_, _ = d.Pool.Exec(ctx,
		`INSERT INTO users (id, name, email, password_hash, role, status, created_at, updated_at)
		 VALUES ($1::uuid, 'Deleted User', $2, 'hash', 'user', 'deleted', NOW(), NOW())`,
		userID, "deleted-"+uuid.NewString()[:8]+"@test.com")

	c, w := preConsumeCtx(PreConsumeWebhookRequest{
		UserID:        userID,
		RequestID:     uuid.NewString(),
		EstimatedCost: 100,
	})
	r.handlePreConsume(c)

	if w.Code != http.StatusPaymentRequired {
		t.Errorf("expected 402 for inactive user, got %d", w.Code)
	}
}

func TestPreConsume_GiftBalanceFirst(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	d := testPreConsumeDB(t)
	defer d.Close()
	d.Redis = rdb
	r := newTestPreConsumeRoutes(t, d)

	userID := insertPreConsumeUser(t, d, 2000, 3000, "giftfirst")

	c, w := preConsumeCtx(PreConsumeWebhookRequest{
		UserID:        userID,
		KeyHash:       insertPreConsumeKey(t, d, userID, "general", "giftfirst"),
		RequestID:     uuid.NewString(),
		EstimatedCost: 4000,
	})
	r.handlePreConsume(c)

	var resp struct {
		Success   bool  `json:"success"`
		Rejected  bool  `json:"rejected"`
		Amount    int64 `json:"amount"`
		Remaining int64 `json:"remaining"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Rejected {
		t.Errorf("expected rejected=false, gift+balance=5000 >= 4000")
	}
	if resp.Amount != 4000 {
		t.Errorf("expected amount=4000, got %d", resp.Amount)
	}
}

func TestPreConsume_ActiveFreezesDeducted(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	d := testPreConsumeDB(t)
	defer d.Close()
	d.Redis = rdb
	r := newTestPreConsumeRoutes(t, d)

	userID := insertPreConsumeUser(t, d, 10000, 0, "activefreeze")
	existingReqID := uuid.NewString()

	fd := freezeData{UserID: userID, Amount: 6000, CreatedAt: time.Now().Unix()}
	fdJSON, _ := json.Marshal(fd)
	_ = mr.Set("billing:freeze:"+existingReqID, string(fdJSON))
	_, _ = mr.SAdd("billing:freeze-set:"+userID, existingReqID)

	c, w := preConsumeCtx(PreConsumeWebhookRequest{
		UserID:        userID,
		KeyHash:       insertPreConsumeKey(t, d, userID, "general", "freeze"),
		RequestID:     uuid.NewString(),
		EstimatedCost: 5000,
	})
	r.handlePreConsume(c)

	var resp struct {
		Success   bool  `json:"success"`
		Rejected  bool  `json:"rejected"`
		Remaining int64 `json:"remaining"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !resp.Rejected {
		t.Errorf("expected rejected=true (10000-6000=4000 < 5000), got rejected=%v", resp.Rejected)
	}
}

func TestSettle_Unfreeze(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	d := testPreConsumeDB(t)
	defer d.Close()
	d.Redis = rdb
	r := newTestPreConsumeRoutes(t, d)

	userID := insertPreConsumeUser(t, d, 10000, 0, "unfreeze")
	reqID := uuid.NewString()
	fd := freezeData{UserID: userID, KeyHash: "test", Amount: 5000, CreatedAt: time.Now().Unix()}
	fdJSON, _ := json.Marshal(fd)
	_ = mr.Set("billing:freeze:"+reqID, string(fdJSON))
	_, _ = mr.SAdd("billing:freeze-set:"+userID, reqID)

	c, w := settleCtx(SettleWebhookRequest{
		RequestID:   reqID,
		UserID:      userID,
		Action:      "unfreeze",
		PreConsumed: 5000,
	})
	r.handleSettle(c)

	var resp struct {
		Success bool  `json:"success"`
		Charged int64 `json:"charged"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !resp.Success {
		t.Errorf("expected success=true")
	}
	if resp.Charged != 0 {
		t.Errorf("expected charged=0 for unfreeze, got %d", resp.Charged)
	}
	if mr.Exists("billing:freeze:" + reqID) {
		t.Errorf("freeze key should be deleted after settle")
	}
}

func TestSettle_Charge(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	d := testPreConsumeDB(t)
	defer d.Close()
	d.Redis = rdb
	r := newTestPreConsumeRoutes(t, d)

	userID := insertPreConsumeUser(t, d, 10000, 5000, "charge")
	reqID := uuid.NewString()

	_ = insertPreConsumeKey(t, d, userID, "general", "charge")

	fd := freezeData{
		UserID: userID, KeyHash: "test-charge", Model: "gpt-4", Provider: "openai",
		Amount: 3000, CreatedAt: time.Now().Unix(),
	}
	fdJSON, _ := json.Marshal(fd)
	_ = mr.Set("billing:freeze:"+reqID, string(fdJSON))
	_, _ = mr.SAdd("billing:freeze-set:"+userID, reqID)

	c, w := settleCtx(SettleWebhookRequest{
		RequestID:   reqID,
		UserID:      userID,
		Action:      "charge",
		PreConsumed: 3000,
		LatencyMs:   1500,
		ErrorType:   "timeout",
	})
	r.handleSettle(c)

	var resp struct {
		Success bool  `json:"success"`
		Charged int64 `json:"charged"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !resp.Success {
		t.Errorf("expected success=true, got false: body=%s", w.Body.String())
	}
	if resp.Charged != 3000 {
		t.Errorf("expected charged=3000, got %d", resp.Charged)
	}

	var giftBalance, balance int64
	err := d.Pool.QueryRow(context.Background(),
		`SELECT COALESCE(gift_balance,0), COALESCE(balance,0) FROM users WHERE id = $1::uuid`,
		userID).Scan(&giftBalance, &balance)
	if err != nil {
		t.Fatalf("query user balance: %v", err)
	}
	if giftBalance != 2000 {
		t.Errorf("expected gift_balance=2000 (5000-3000), got %d", giftBalance)
	}
	if balance != 10000 {
		t.Errorf("expected balance=10000 unchanged, got %d", balance)
	}

	var recordCount int
	_ = d.Pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM usage_records WHERE trace_id = $1 AND user_id = $2::uuid`,
		reqID, userID).Scan(&recordCount)
	if recordCount != 1 {
		t.Errorf("expected 1 usage_record, got %d", recordCount)
	}
}

func TestSettle_ChargeEnterprise(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	d := testPreConsumeDB(t)
	defer d.Close()
	d.Redis = rdb
	r := newTestPreConsumeRoutes(t, d)
	ctx := context.Background()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	var enterpriseID string
	err := d.Pool.QueryRow(ctx,
		`INSERT INTO enterprises (name, contact_email, status, credit_total, credit_used)
		 VALUES ($1, $2, 'active', 50000, 10000) RETURNING id::text`,
		"ent-charge-"+suffix, "ent-charge-"+suffix+"@test.com").Scan(&enterpriseID)
	if err != nil {
		t.Fatalf("insert enterprise: %v", err)
	}
	defer func() {
		_, _ = d.Pool.Exec(ctx, "DELETE FROM usage_records WHERE user_id = $1::uuid", enterpriseID)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM balance_transactions WHERE user_id = $1::uuid", enterpriseID)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM enterprise_audit_log WHERE enterprise_id = $1::uuid", enterpriseID)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM api_keys WHERE key_hash = 'ent-charge-key'")
		_, _ = d.Pool.Exec(ctx, "DELETE FROM users WHERE enterprise_id = $1::uuid", enterpriseID)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM enterprises WHERE id = $1::uuid", enterpriseID)
	}()

	var userID string
	err = d.Pool.QueryRow(ctx,
		`INSERT INTO users (name, email, password_hash, role, enterprise_id)
		 VALUES ('Ent User', $1, 'hash', 'user', $2::uuid) RETURNING id::text`,
		"entuser-charge-"+suffix+"@test.com", enterpriseID).Scan(&userID)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	_, err = d.Pool.Exec(ctx,
		`INSERT INTO api_keys (user_id, key_hash, key_prefix, name, status, key_type, enterprise_id)
		 VALUES ($1::uuid, 'ent-charge-key', 'sk-ent', 'Ent Key', 'active', 'enterprise', $2::uuid)`,
		userID, enterpriseID)
	if err != nil {
		t.Fatalf("insert key: %v", err)
	}

	reqID := uuid.NewString()
	fd := freezeData{
		UserID: userID, KeyHash: "ent-charge-key", Model: "gpt-4", Provider: "openai",
		Amount: 8000, CreatedAt: time.Now().Unix(),
		KeyType: "enterprise", EnterpriseID: enterpriseID,
	}
	fdJSON, _ := json.Marshal(fd)
	_ = mr.Set("billing:freeze:"+reqID, string(fdJSON))
	_, _ = mr.SAdd("billing:freeze-set:"+userID, reqID)

	c, w := settleCtx(SettleWebhookRequest{
		RequestID:   reqID,
		UserID:      userID,
		Action:      "charge",
		PreConsumed: 8000,
		LatencyMs:   2000,
		ErrorType:   "timeout",
	})
	r.handleSettle(c)

	var resp struct {
		Success bool  `json:"success"`
		Charged int64 `json:"charged"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !resp.Success {
		t.Errorf("expected success=true, got body=%s", w.Body.String())
	}
	if resp.Charged != 8000 {
		t.Errorf("expected charged=8000, got %d", resp.Charged)
	}

	var creditUsed int64
	err = d.Pool.QueryRow(ctx,
		`SELECT COALESCE(credit_used,0) FROM enterprises WHERE id = $1::uuid`,
		enterpriseID).Scan(&creditUsed)
	if err != nil {
		t.Fatalf("query enterprise credit: %v", err)
	}
	if creditUsed != 18000 {
		t.Errorf("expected credit_used=18000 (10000+8000), got %d", creditUsed)
	}
}

func TestSettle_FreezeNotFound(t *testing.T) {
	d := testPreConsumeDB(t)
	defer d.Close()
	r := newTestPreConsumeRoutes(t, d)

	c, w := settleCtx(SettleWebhookRequest{
		RequestID: "nonexistent-" + uuid.NewString(),
		UserID:    uuid.NewString(),
		Action:    "unfreeze",
	})
	r.handleSettle(c)

	var resp struct {
		Success bool  `json:"success"`
		Charged int64 `json:"charged"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !resp.Success {
		t.Errorf("expected success=true for missing freeze")
	}
	if resp.Charged != 0 {
		t.Errorf("expected charged=0, got %d", resp.Charged)
	}
}

func TestSettle_UnknownAction(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	d := testPreConsumeDB(t)
	defer d.Close()
	d.Redis = rdb
	r := newTestPreConsumeRoutes(t, d)

	userID := insertPreConsumeUser(t, d, 10000, 0, "unknownact")
	reqID := uuid.NewString()
	fd := freezeData{UserID: userID, Amount: 1000, CreatedAt: time.Now().Unix()}
	fdJSON, _ := json.Marshal(fd)
	_ = mr.Set("billing:freeze:"+reqID, string(fdJSON))
	_, _ = mr.SAdd("billing:freeze-set:"+userID, reqID)

	c, w := settleCtx(SettleWebhookRequest{
		RequestID:   reqID,
		UserID:      userID,
		Action:      "invalid_action",
		PreConsumed: 1000,
	})
	r.handleSettle(c)

	var resp struct {
		Success bool  `json:"success"`
		Charged int64 `json:"charged"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !resp.Success {
		t.Errorf("expected success=true for unknown action (safe default)")
	}
	if resp.Charged != 0 {
		t.Errorf("expected charged=0 for unknown action, got %d", resp.Charged)
	}
}

func TestPreConsume_EnterpriseKey(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	d := testPreConsumeDB(t)
	defer d.Close()
	d.Redis = rdb
	r := newTestPreConsumeRoutes(t, d)
	ctx := context.Background()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	var enterpriseID string
	err := d.Pool.QueryRow(ctx,
		`INSERT INTO enterprises (name, contact_email, status, credit_total, credit_used)
		 VALUES ($1, $2, 'active', 50000, 5000) RETURNING id::text`,
		"ent-pc-"+suffix, "ent-pc-"+suffix+"@test.com").Scan(&enterpriseID)
	if err != nil {
		t.Fatalf("insert enterprise: %v", err)
	}

	var userID string
	err = d.Pool.QueryRow(ctx,
		`INSERT INTO users (name, email, password_hash, role, enterprise_id)
		 VALUES ('Ent PC User', $1, 'hash', 'user', $2::uuid) RETURNING id::text`,
		"entpc-"+suffix+"@test.com", enterpriseID).Scan(&userID)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	keyHash := "ent-pc-key-" + suffix
	_, err = d.Pool.Exec(ctx,
		`INSERT INTO api_keys (user_id, key_hash, key_prefix, name, status, key_type, enterprise_id)
		 VALUES ($1::uuid, $2, 'sk-ent', 'Ent PC Key', 'active', 'enterprise', $3::uuid)`,
		userID, keyHash, enterpriseID)
	if err != nil {
		t.Fatalf("insert key: %v", err)
	}

	c, w := preConsumeCtx(PreConsumeWebhookRequest{
		UserID:        userID,
		KeyHash:       keyHash,
		Model:         "gpt-4",
		Provider:      "openai",
		RequestID:     uuid.NewString(),
		EstimatedCost: 10000,
	})
	r.handlePreConsume(c)

	var resp struct {
		Success  bool  `json:"success"`
		Rejected bool  `json:"rejected"`
		Amount   int64 `json:"amount"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Rejected {
		t.Errorf("expected rejected=false, credit cap=55000, used=5000, need=10000")
	}
	if resp.Amount != 10000 {
		t.Errorf("expected amount=10000, got %d", resp.Amount)
	}
}

func TestPreConsume_EnterpriseInsufficientCredit(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	d := testPreConsumeDB(t)
	defer d.Close()
	d.Redis = rdb
	r := newTestPreConsumeRoutes(t, d)
	ctx := context.Background()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	var enterpriseID string
	err := d.Pool.QueryRow(ctx,
		`INSERT INTO enterprises (name, contact_email, status, credit_total, credit_used)
		 VALUES ($1, $2, 'active', 5000, 5000) RETURNING id::text`,
		"ent-insuf-"+suffix, "ent-insuf-"+suffix+"@test.com").Scan(&enterpriseID)
	if err != nil {
		t.Fatalf("insert enterprise: %v", err)
	}

	var userID string
	err = d.Pool.QueryRow(ctx,
		`INSERT INTO users (name, email, password_hash, role, enterprise_id)
		 VALUES ('Ent Insuf User', $1, 'hash', 'user', $2::uuid) RETURNING id::text`,
		"entinsuf-"+suffix+"@test.com", enterpriseID).Scan(&userID)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	keyHash := "ent-insuf-key-" + suffix
	_, err = d.Pool.Exec(ctx,
		`INSERT INTO api_keys (user_id, key_hash, key_prefix, name, status, key_type, enterprise_id)
		 VALUES ($1::uuid, $2, 'sk-ent', 'Ent Insuf Key', 'active', 'enterprise', $3::uuid)`,
		userID, keyHash, enterpriseID)
	if err != nil {
		t.Fatalf("insert key: %v", err)
	}

	c, w := preConsumeCtx(PreConsumeWebhookRequest{
		UserID:        userID,
		KeyHash:       keyHash,
		Model:         "gpt-4",
		RequestID:     uuid.NewString(),
		EstimatedCost: 5000,
	})
	r.handlePreConsume(c)

	if w.Code != http.StatusPaymentRequired {
		t.Errorf("expected 402 for insufficient enterprise credit, got %d", w.Code)
	}
}

func TestPreConsume_SubscriptionKey(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	d := testPreConsumeDB(t)
	defer d.Close()
	d.Redis = rdb
	r := newTestPreConsumeRoutes(t, d)
	ctx := context.Background()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	userID := uuid.NewString()
	planID := uuid.NewString()

	_, _ = d.Pool.Exec(ctx, "DELETE FROM user_subscriptions WHERE user_id = $1::uuid", userID)
	_, _ = d.Pool.Exec(ctx, "DELETE FROM users WHERE id = $1::uuid", userID)
	_, _ = d.Pool.Exec(ctx, "DELETE FROM subscription_plans WHERE id = $1::uuid", planID)

	defer func() {
		_, _ = d.Pool.Exec(ctx, "DELETE FROM api_keys WHERE key_hash = 'sub-key-"+suffix+"'")
		_, _ = d.Pool.Exec(ctx, "DELETE FROM user_subscriptions WHERE user_id = $1::uuid", userID)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM users WHERE id = $1::uuid", userID)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM subscription_plans WHERE id = $1::uuid", planID)
	}()

	_, err := d.Pool.Exec(ctx,
		`INSERT INTO subscription_plans (id, name, plan_type, price, quota, duration_days, daily_limit, status, tpm_limit, tph_limit, rpm_limit, rph_limit, created_at, updated_at)
		 VALUES ($1::uuid, 'Sub Test Plan', 'token', 1000, 100000, 30, 5000, 'active', 100000, 1000000, 60, 3600, NOW(), NOW())`,
		planID)
	if err != nil {
		t.Fatalf("insert plan: %v", err)
	}

	_, err = d.Pool.Exec(ctx,
		`INSERT INTO users (id, name, email, password_hash, role, balance, created_at, updated_at)
		 VALUES ($1::uuid, 'Sub User', $2, 'hash', 'user', 0, NOW(), NOW())`,
		userID, "sub-"+suffix+"@test.com")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	_, err = d.Pool.Exec(ctx,
		`INSERT INTO user_subscriptions (user_id, plan_id, status, quota_total, quota_used, quota_remaining, expires_at, created_at, updated_at)
		 VALUES ($1::uuid, $2::uuid, 'active', 10000, 0, 10000, NOW() + INTERVAL '1 year', NOW(), NOW())`,
		userID, planID)
	if err != nil {
		t.Fatalf("insert subscription: %v", err)
	}

	keyHash := "sub-key-" + suffix
	_, err = d.Pool.Exec(ctx,
		`INSERT INTO api_keys (user_id, key_hash, key_prefix, name, status, key_type)
		 VALUES ($1::uuid, $2, 'sk-sub', 'Sub Key', 'active', 'subscription')`,
		userID, keyHash)
	if err != nil {
		t.Fatalf("insert key: %v", err)
	}

	c, w := preConsumeCtx(PreConsumeWebhookRequest{
		UserID:        userID,
		KeyHash:       keyHash,
		RequestID:     uuid.NewString(),
		EstimatedCost: 5000,
	})
	r.handlePreConsume(c)

	var resp struct {
		Success   bool  `json:"success"`
		Rejected  bool  `json:"rejected"`
		Amount    int64 `json:"amount"`
		Remaining int64 `json:"remaining"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !resp.Success {
		t.Errorf("expected success=true")
	}
	if resp.Rejected {
		t.Errorf("expected rejected=false (subscription is active)")
	}
	if resp.Amount != 0 {
		t.Errorf("expected amount=0 for subscription key, got %d", resp.Amount)
	}
	if resp.Remaining != 10000 {
		t.Errorf("expected remaining=10000, got %d", resp.Remaining)
	}
}

func TestPreConsume_SubscriptionKeyNoQuota(t *testing.T) {
	d := testPreConsumeDB(t)
	defer d.Close()
	r := newTestPreConsumeRoutes(t, d)
	ctx := context.Background()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	userID := uuid.NewString()
	planID := uuid.NewString()

	_, _ = d.Pool.Exec(ctx, "DELETE FROM user_subscriptions WHERE user_id = $1::uuid", userID)
	_, _ = d.Pool.Exec(ctx, "DELETE FROM users WHERE id = $1::uuid", userID)
	_, _ = d.Pool.Exec(ctx, "DELETE FROM subscription_plans WHERE id = $1::uuid", planID)

	defer func() {
		_, _ = d.Pool.Exec(ctx, "DELETE FROM api_keys WHERE key_hash = 'sub-noq-"+suffix+"'")
		_, _ = d.Pool.Exec(ctx, "DELETE FROM user_subscriptions WHERE user_id = $1::uuid", userID)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM users WHERE id = $1::uuid", userID)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM subscription_plans WHERE id = $1::uuid", planID)
	}()

	_, err := d.Pool.Exec(ctx,
		`INSERT INTO subscription_plans (id, name, plan_type, price, quota, duration_days, daily_limit, status, tpm_limit, tph_limit, rpm_limit, rph_limit, created_at, updated_at)
		 VALUES ($1::uuid, 'Sub NoQ Plan', 'token', 1000, 100000, 30, 5000, 'active', 100000, 1000000, 60, 3600, NOW(), NOW())`,
		planID)
	if err != nil {
		t.Fatalf("insert plan: %v", err)
	}

	_, err = d.Pool.Exec(ctx,
		`INSERT INTO users (id, name, email, password_hash, role, balance, created_at, updated_at)
		 VALUES ($1::uuid, 'Sub NoQ User', $2, 'hash', 'user', 0, NOW(), NOW())`,
		userID, "subnoq-"+suffix+"@test.com")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	_, err = d.Pool.Exec(ctx,
		`INSERT INTO user_subscriptions (user_id, plan_id, status, quota_total, quota_used, quota_remaining, expires_at, created_at, updated_at)
		 VALUES ($1::uuid, $2::uuid, 'active', 1000, 1000, 0, NOW() + INTERVAL '1 year', NOW(), NOW())`,
		userID, planID)
	if err != nil {
		t.Fatalf("insert subscription: %v", err)
	}

	keyHash := "sub-noq-" + suffix
	_, err = d.Pool.Exec(ctx,
		`INSERT INTO api_keys (user_id, key_hash, key_prefix, name, status, key_type)
		 VALUES ($1::uuid, $2, 'sk-sub', 'Sub NoQ Key', 'active', 'subscription')`,
		userID, keyHash)
	if err != nil {
		t.Fatalf("insert key: %v", err)
	}

	c, w := preConsumeCtx(PreConsumeWebhookRequest{
		UserID:        userID,
		KeyHash:       keyHash,
		RequestID:     uuid.NewString(),
		EstimatedCost: 1000,
	})
	r.handlePreConsume(c)

	if w.Code != http.StatusPaymentRequired {
		t.Errorf("expected 402 for subscription with no quota, got %d", w.Code)
	}
}

func TestSettle_InvalidRequest(t *testing.T) {
	d := testPreConsumeDB(t)
	defer d.Close()
	r := newTestPreConsumeRoutes(t, d)

	c, w := settleCtx(map[string]string{"invalid": "body"})
	r.handleSettle(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid settle request, got %d", w.Code)
	}
}

func TestPreConsume_NoRedisFailOpen(t *testing.T) {
	d := testPreConsumeDB(t)
	defer d.Close()
	r := newTestPreConsumeRoutes(t, d)

	userID := insertPreConsumeUser(t, d, 10000, 0, "noredis")

	c, w := preConsumeCtx(PreConsumeWebhookRequest{
		UserID:        userID,
		KeyHash:       insertPreConsumeKey(t, d, userID, "general", "noredis"),
		RequestID:     uuid.NewString(),
		EstimatedCost: 5000,
	})
	r.handlePreConsume(c)

	var resp struct {
		Success bool  `json:"success"`
		Amount  int64 `json:"amount"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !resp.Success {
		t.Errorf("expected success=true (fail-open when Redis unavailable)")
	}
	if resp.Amount != 0 {
		t.Errorf("expected amount=0 when Redis unavailable (no freeze), got %d", resp.Amount)
	}
}

func TestGetActiveFreezeTotal(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	d := testPreConsumeDB(t)
	defer d.Close()
	d.Redis = rdb
	r := newTestPreConsumeRoutes(t, d)
	ctx := context.Background()

	userID := uuid.NewString()

	fd1 := freezeData{UserID: userID, Amount: 3000, CreatedAt: time.Now().Unix()}
	fd1JSON, _ := json.Marshal(fd1)
	fd2 := freezeData{UserID: userID, Amount: 2000, CreatedAt: time.Now().Unix()}
	fd2JSON, _ := json.Marshal(fd2)

	_ = mr.Set("billing:freeze:req1", string(fd1JSON))
	_ = mr.Set("billing:freeze:req2", string(fd2JSON))
	_, _ = mr.SAdd("billing:freeze-set:"+userID, "req1", "req2")

	total := r.getActiveFreezeTotal(ctx, userID)
	if total != 5000 {
		t.Errorf("expected total=5000, got %d", total)
	}
}

func TestGetActiveFreezeTotal_Empty(t *testing.T) {
	d := testPreConsumeDB(t)
	defer d.Close()
	r := newTestPreConsumeRoutes(t, d)
	ctx := context.Background()

	total := r.getActiveFreezeTotal(ctx, uuid.NewString())
	if total != 0 {
		t.Errorf("expected total=0 for user with no freezes, got %d", total)
	}
}

func TestSettle_ChargeBalanceExhausted(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	d := testPreConsumeDB(t)
	defer d.Close()
	d.Redis = rdb
	r := newTestPreConsumeRoutes(t, d)
	ctx := context.Background()

	userID := insertPreConsumeUser(t, d, 1000, 0, "exhausted")
	reqID := uuid.NewString()
	_ = insertPreConsumeKey(t, d, userID, "general", "exhausted")

	fd := freezeData{UserID: userID, KeyHash: "test-exh", Model: "gpt-4", Provider: "openai",
		Amount: 5000, CreatedAt: time.Now().Unix()}
	fdJSON, _ := json.Marshal(fd)
	_ = mr.Set("billing:freeze:"+reqID, string(fdJSON))
	_, _ = mr.SAdd("billing:freeze-set:"+userID, reqID)

	c, w := settleCtx(SettleWebhookRequest{
		RequestID:   reqID,
		UserID:      userID,
		Action:      "charge",
		PreConsumed: 5000,
		LatencyMs:   500,
		ErrorType:   "timeout",
	})
	r.handleSettle(c)

	var resp struct {
		Success bool  `json:"success"`
		Charged int64 `json:"charged"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !resp.Success {
		t.Errorf("expected success=true, got body=%s", w.Body.String())
	}
	if resp.Charged != 1000 {
		t.Errorf("expected charged=1000 (only available balance), got %d", resp.Charged)
	}

	var balance int64
	_ = d.Pool.QueryRow(ctx,
		`SELECT COALESCE(balance,0) FROM users WHERE id = $1::uuid`,
		userID).Scan(&balance)
	if balance != 0 {
		t.Errorf("expected balance=0 after exhausting, got %d", balance)
	}
}

func TestSettle_NoRedisSettle(t *testing.T) {
	d := testPreConsumeDB(t)
	defer d.Close()
	r := newTestPreConsumeRoutes(t, d)

	c, w := settleCtx(SettleWebhookRequest{
		RequestID: uuid.NewString(),
		UserID:    uuid.NewString(),
		Action:    "unfreeze",
	})
	r.handleSettle(c)

	var resp struct {
		Success bool  `json:"success"`
		Charged int64 `json:"charged"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !resp.Success {
		t.Errorf("expected success=true when Redis unavailable")
	}
}

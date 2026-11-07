package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"mime/multipart"
	"os"
	"path/filepath"

	"github.com/mayiants/ai-portal-go/internal/db"
	"github.com/mayiants/ai-portal-go/internal/storage"
	"github.com/mayiants/ai-portal-go/pkg/audit"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// newTicketTestRoutes builds TicketRoutes against the test DB (notif nil; real
// audit logger so admin handlers' r.audit.Log is safe).
func newTicketTestRoutes(t *testing.T) (*TicketRoutes, *db.DB) {
	t.Helper()
	d := testWebhookBillingDB(t) // InitTestSchema + pool
	logger := zap.NewNop()
	return &TicketRoutes{db: d, notif: nil, audit: audit.NewLogger(d, logger), logger: logger}, d
}

func ticketCtx(method, path, body, userID, role, email string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	c.Request = r
	c.Set("user_id", userID)
	c.Set("role", role)
	c.Set("email", email)
	return c, w
}

func mkTicketUser(t *testing.T, d *db.DB, role string) string {
	t.Helper()
	id := uuid.NewString()
	if _, err := d.Pool.Exec(context.Background(),
		`INSERT INTO users (id, name, email, password_hash, role, created_at, updated_at)
		 VALUES ($1::uuid,'Ticket User',$2,'h',$3,NOW(),NOW())`,
		id, fmt.Sprintf("tk-%s@example.com", id[:8]), role); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	return id
}

func TestTicket_CreateReplyAndIsolation(t *testing.T) {
	r, d := newTicketTestRoutes(t)
	defer d.Close()
	ctx := context.Background()
	uA := mkTicketUser(t, d, "user")
	uB := mkTicketUser(t, d, "user")
	admin := mkTicketUser(t, d, "system_admin")
	t.Cleanup(func() {
		_, _ = d.Pool.Exec(ctx, "DELETE FROM support_ticket_messages WHERE author_id IN ($1::uuid,$2::uuid,$3::uuid)", uA, uB, admin)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM support_tickets WHERE user_id IN ($1::uuid,$2::uuid)", uA, uB)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM users WHERE id IN ($1::uuid,$2::uuid,$3::uuid)", uA, uB, admin)
	})

	// Create a ticket as user A.
	c, w := ticketCtx("POST", "/api/tickets", `{"subject":"Help me","category":"billing","priority":"high","body":"first message"}`, uA, "user", "a@x.com")
	r.create(c)
	if w.Code != http.StatusOK {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var created struct {
		ID       string `json:"id"`
		TicketNo int64  `json:"ticket_no"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	if created.ID == "" || created.TicketNo == 0 {
		t.Fatalf("missing id/ticket_no: %s", w.Body.String())
	}

	// User B must NOT read it (ownership isolation).
	c, w = ticketCtx("GET", "/api/tickets/"+created.ID, "", uB, "user", "b@x.com")
	c.Params = gin.Params{{Key: "id", Value: created.ID}}
	r.detail(c)
	if w.Code != http.StatusForbidden && w.Code != http.StatusNotFound {
		t.Errorf("user B read other's ticket: got %d, want 403/404", w.Code)
	}

	// Admin replies with a public message + an internal note.
	c, w = ticketCtx("POST", "/api/admin/tickets/"+created.ID+"/messages", `{"body":"we are on it"}`, admin, "system_admin", "admin@x.com")
	c.Params = gin.Params{{Key: "id", Value: created.ID}}
	r.adminReply(c)
	if w.Code != http.StatusOK {
		t.Fatalf("admin reply: %d %s", w.Code, w.Body.String())
	}
	c, w = ticketCtx("POST", "/api/admin/tickets/"+created.ID+"/messages", `{"body":"secret note","internal_note":true}`, admin, "system_admin", "admin@x.com")
	c.Params = gin.Params{{Key: "id", Value: created.ID}}
	r.adminReply(c)
	if w.Code != http.StatusOK {
		t.Fatalf("admin internal note: %d", w.Code)
	}

	// Status should be 'answered' after an admin public reply.
	var status, lastRole string
	_ = d.Pool.QueryRow(ctx, "SELECT status, last_reply_role FROM support_tickets WHERE id=$1::uuid", created.ID).Scan(&status, &lastRole)
	if status != "answered" || lastRole != "admin" {
		t.Errorf("after admin reply status=%q lastRole=%q, want answered/admin", status, lastRole)
	}

	// User A detail must show the public reply but NOT the internal note.
	c, w = ticketCtx("GET", "/api/tickets/"+created.ID, "", uA, "user", "a@x.com")
	c.Params = gin.Params{{Key: "id", Value: created.ID}}
	r.detail(c)
	if w.Code != http.StatusOK {
		t.Fatalf("user A detail: %d", w.Code)
	}
	var det struct {
		Messages []struct {
			Body         string `json:"body"`
			InternalNote bool   `json:"internal_note"`
		} `json:"messages"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &det)
	bodies := ""
	for _, m := range det.Messages {
		bodies += m.Body + "|"
		if m.InternalNote {
			t.Errorf("internal note leaked to user surface")
		}
	}
	if !strings.Contains(bodies, "first message") || !strings.Contains(bodies, "we are on it") {
		t.Errorf("user thread missing messages: %q", bodies)
	}
	if strings.Contains(bodies, "secret note") {
		t.Errorf("internal note body leaked: %q", bodies)
	}

	// User A replies -> ticket reopens to 'pending'.
	c, w = ticketCtx("POST", "/api/tickets/"+created.ID+"/messages", `{"body":"thanks, more info"}`, uA, "user", "a@x.com")
	c.Params = gin.Params{{Key: "id", Value: created.ID}}
	r.reply(c)
	if w.Code != http.StatusOK {
		t.Fatalf("user reply: %d %s", w.Code, w.Body.String())
	}
	_ = d.Pool.QueryRow(ctx, "SELECT status FROM support_tickets WHERE id=$1::uuid", created.ID).Scan(&status)
	if status != "pending" {
		t.Errorf("after user reply status=%q, want pending", status)
	}

	// Admin PATCH -> resolved.
	c, w = ticketCtx("PATCH", "/api/admin/tickets/"+created.ID, `{"status":"resolved"}`, admin, "system_admin", "admin@x.com")
	c.Params = gin.Params{{Key: "id", Value: created.ID}}
	r.adminPatch(c)
	if w.Code != http.StatusOK {
		t.Fatalf("admin patch: %d %s", w.Code, w.Body.String())
	}
	_ = d.Pool.QueryRow(ctx, "SELECT status FROM support_tickets WHERE id=$1::uuid", created.ID).Scan(&status)
	if status != "resolved" {
		t.Errorf("after patch status=%q, want resolved", status)
	}
	_ = time.Now()
}

func TestTicket_CreateValidation(t *testing.T) {
	r, d := newTicketTestRoutes(t)
	defer d.Close()
	u := mkTicketUser(t, d, "user")
	t.Cleanup(func() { _, _ = d.Pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1::uuid", u) })

	// Missing subject/body -> 400.
	c, w := ticketCtx("POST", "/api/tickets", `{"subject":"","body":""}`, u, "user", "u@x.com")
	r.create(c)
	if w.Code != http.StatusBadRequest {
		t.Errorf("empty create: got %d, want 400", w.Code)
	}
}

func TestTicket_AttachmentUploadDownload(t *testing.T) {
	r, d := newTicketTestRoutes(t)
	defer d.Close()
	ctx := context.Background()
	sp, err := storageLocalForTest(t)
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	r.storage = sp
	u := mkTicketUser(t, d, "user")
	t.Cleanup(func() {
		_, _ = d.Pool.Exec(ctx, "DELETE FROM support_ticket_attachments WHERE message_id IN (SELECT id FROM support_ticket_messages WHERE author_id=$1::uuid)", u)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM support_ticket_messages WHERE author_id=$1::uuid", u)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM support_tickets WHERE user_id=$1::uuid", u)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM users WHERE id=$1::uuid", u)
	})

	// Create a ticket.
	c, w := ticketCtx("POST", "/api/tickets", `{"subject":"att","category":"general","priority":"normal","body":"m"}`, u, "user", "u@x.com")
	r.create(c)
	if w.Code != http.StatusOK {
		t.Fatalf("create: %d", w.Code)
	}
	var created struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)

	// Multipart upload of 2 files.
	body, ctype := buildMultipart(map[string]string{"a.txt": "hello", "b.log": "world"})
	c, w = ticketCtx("POST", "/api/tickets/"+created.ID+"/attachments", "", u, "user", "u@x.com")
	c.Request = httptest.NewRequest("POST", "/api/tickets/"+created.ID+"/attachments", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", ctype)
	c.Set("user_id", u)
	c.Set("role", "user")
	c.Params = gin.Params{{Key: "id", Value: created.ID}}
	r.uploadAttachments(c)
	if w.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", w.Code, w.Body.String())
	}
	var up struct {
		Attachments []struct{ ID, Filename string } `json:"attachments"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &up)
	if len(up.Attachments) != 2 {
		t.Fatalf("want 2 attachments, got %d", len(up.Attachments))
	}

	// Download the first and verify bytes.
	aid := up.Attachments[0].ID
	c, w = ticketCtx("GET", "/api/tickets/"+created.ID+"/attachments/"+aid, "", u, "user", "u@x.com")
	c.Params = gin.Params{{Key: "id", Value: created.ID}, {Key: "aid", Value: aid}}
	r.downloadAttachment(c)
	if w.Code != http.StatusOK {
		t.Fatalf("download: %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "hello") && !strings.Contains(w.Body.String(), "world") {
		t.Errorf("download body missing content: %q", w.Body.String())
	}
}

// TestTicket_AttachmentLimit verifies the 5-file-per-upload cap.
func TestTicket_AttachmentLimit(t *testing.T) {
	r, d := newTicketTestRoutes(t)
	defer d.Close()
	sp, _ := storageLocalForTest(t)
	r.storage = sp
	u := mkTicketUser(t, d, "user")
	t.Cleanup(func() {
		_, _ = d.Pool.Exec(context.Background(), "DELETE FROM support_ticket_messages WHERE author_id=$1::uuid", u)
		_, _ = d.Pool.Exec(context.Background(), "DELETE FROM support_tickets WHERE user_id=$1::uuid", u)
		_, _ = d.Pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1::uuid", u)
	})
	c, w := ticketCtx("POST", "/api/tickets", `{"subject":"a","body":"m"}`, u, "user", "u@x.com")
	r.create(c)
	var created struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)

	m := map[string]string{}
	for i := 0; i < 6; i++ {
		m[fmt.Sprintf("f%d.txt", i)] = "x"
	}
	body, ctype := buildMultipart(m)
	c, w = ticketCtx("POST", "/api/tickets/"+created.ID+"/attachments", "", u, "user", "u@x.com")
	c.Request = httptest.NewRequest("POST", "/api/tickets/"+created.ID+"/attachments", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", ctype)
	c.Set("user_id", u)
	c.Set("role", "user")
	c.Params = gin.Params{{Key: "id", Value: created.ID}}
	r.uploadAttachments(c)
	if w.Code != http.StatusBadRequest {
		t.Errorf("6 files: got %d, want 400", w.Code)
	}
}

func storageLocalForTest(t *testing.T) (storage.Provider, error) {
	t.Helper()
	return storage.NewLocalProvider(t.TempDir(), "")
}

// buildMultipart builds a multipart/form-data body with the given filename->content
// files under the "files" field. Returns body + content-type header.
func buildMultipart(files map[string]string) (string, string) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for name, content := range files {
		fw, _ := mw.CreateFormFile("files", name)
		_, _ = fw.Write([]byte(content))
	}
	_ = mw.Close()
	return buf.String(), mw.FormDataContentType()
}

func TestTicketAttachmentAllowed(t *testing.T) {
	cases := []struct {
		ct, filename string
		want         bool
	}{
		{"text/plain", "notes.txt", true},
		{"application/pdf", "doc.pdf", true},
		{"image/png", "pic.png", true},
		{"application/octet-stream", "server.log", true}, // browsers send this for .log
		{"application/octet-stream", "run.exe", false},   // the hole: unknown types ride octet-stream
		{"application/octet-stream", "script.sh", false},
		{"application/zip", "archive.zip", false},
		{"text/html", "page.html", false},
	}
	for _, tc := range cases {
		if got := ticketAttachmentAllowed(tc.ct, tc.filename); got != tc.want {
			t.Errorf("ticketAttachmentAllowed(%q, %q) = %v, want %v", tc.ct, tc.filename, got, tc.want)
		}
	}
}

// buildMultipartNames is the ordered-sibling of buildMultipart (Go map order
// is random, which is fine there but this flow asserts per-file counts).
func buildMultipartNames(filenames []string) (string, string) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, name := range filenames {
		fw, _ := mw.CreateFormFile("files", name)
		_, _ = fw.Write([]byte("content of " + name))
	}
	_ = mw.Close()
	return buf.String(), mw.FormDataContentType()
}

func TestTicket_StagedAttachmentsFlow(t *testing.T) {
	r, d := newTicketTestRoutes(t)
	defer d.Close()
	ctx := context.Background()
	root := t.TempDir()
	sp, err := storage.NewLocalProvider(root, "")
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	r.storage = sp
	u := mkTicketUser(t, d, "user")
	t.Cleanup(func() {
		_, _ = d.Pool.Exec(ctx, `DELETE FROM support_ticket_attachments WHERE staged_by=$1::uuid`, u)
		_, _ = d.Pool.Exec(ctx, "DELETE FROM users WHERE id=$1::uuid", u)
	})

	stageCall := func(filenames []string) (*httptest.ResponseRecorder, map[string]interface{}) {
		body, ctype := buildMultipartNames(filenames)
		c, w := ticketCtx("POST", "/api/tickets/attachments/stage", "", u, "user", "u@x.com")
		c.Request = httptest.NewRequest("POST", "/api/tickets/attachments/stage", strings.NewReader(body))
		c.Request.Header.Set("Content-Type", ctype)
		c.Set("user_id", u)
		c.Set("role", "user")
		r.stageAttachments(c)
		var parsed map[string]interface{}
		_ = json.Unmarshal(w.Body.Bytes(), &parsed)
		return w, parsed
	}

	// Stage two files; they must land with message_id NULL.
	w, parsed := stageCall([]string{"a.txt", "b.txt"})
	if w.Code != http.StatusOK {
		t.Fatalf("stage: got %d body=%s", w.Code, w.Body.String())
	}
	atts := parsed["attachments"].([]interface{})
	if len(atts) != 2 {
		t.Fatalf("staged 2 files, got %v", atts)
	}
	var stagedIDs []string
	for _, a := range atts {
		stagedIDs = append(stagedIDs, a.(map[string]interface{})["id"].(string))
	}
	var nullMsg int
	if err := d.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM support_ticket_attachments WHERE staged_by=$1::uuid AND message_id IS NULL`, u).Scan(&nullMsg); err != nil || nullMsg != 2 {
		t.Fatalf("staged rows with NULL message = %d, want 2 (err=%v)", nullMsg, err)
	}

	// Create a ticket linking the staged ids; they must move onto its first message.
	cc, wc := ticketCtx("POST", "/api/tickets", `{"subject":"staged flow","body":"body","staged_attachment_ids":["`+stagedIDs[0]+`","`+stagedIDs[1]+`"]}`, u, "user", "u@x.com")
	r.create(cc)
	if wc.Code != http.StatusOK {
		t.Fatalf("create: got %d body=%s", wc.Code, wc.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(wc.Body.Bytes(), &created)
	if created.ID == "" {
		t.Fatalf("create returned no id: %s", wc.Body.String())
	}
	var linked int
	if err := d.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM support_ticket_attachments WHERE message_id IN (SELECT id FROM support_ticket_messages WHERE ticket_id=$1::uuid)`,
		created.ID).Scan(&linked); err != nil || linked != 2 {
		t.Fatalf("linked attachments = %d, want 2 (err=%v)", linked, err)
	}

	// Staging again then unstaging removes the row and the storage object.
	_, parsed2 := stageCall([]string{"c.txt"})
	id3 := parsed2["attachments"].([]interface{})[0].(map[string]interface{})["id"].(string)
	var storageKey string
	if err := d.Pool.QueryRow(ctx, `SELECT storage_key FROM support_ticket_attachments WHERE id=$1::uuid`, id3).Scan(&storageKey); err != nil {
		t.Fatalf("select storage key: %v", err)
	}
	c2, w2 := ticketCtx("DELETE", "/api/tickets/attachments/staged/"+id3, "", u, "user", "u@x.com")
	c2.Params = gin.Params{{Key: "aid", Value: id3}}
	c2.Set("user_id", u)
	r.unstageAttachment(c2)
	if w2.Code != http.StatusOK {
		t.Fatalf("unstage: got %d body=%s", w2.Code, w2.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, storageKey)); !os.IsNotExist(err) {
		t.Fatalf("storage object still exists after unstage: %v", err)
	}
}

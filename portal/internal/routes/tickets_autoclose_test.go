package routes

import (
	"context"
	"testing"
	"time"

	"github.com/mayiants/ai-portal-go/internal/db"
	"github.com/google/uuid"
)

// insertStaleTicket creates a ticket with a controlled creation time and
// optional last reply time, returning its id.
func insertStaleTicket(t *testing.T, d *db.DB, userID string, status string, createdAgoDays int, lastReplyAgoDays *int) string {
	t.Helper()
	var lastReply interface{}
	if lastReplyAgoDays != nil {
		lastReply = time.Now().Add(-time.Duration(*lastReplyAgoDays) * 24 * time.Hour)
	}
	created := time.Now().Add(-time.Duration(createdAgoDays) * 24 * time.Hour)
	id := uuid.NewString()
	if _, err := d.Pool.Exec(context.Background(),
		`INSERT INTO support_tickets (id, user_id, subject, category, priority, status, last_reply_at, created_at, updated_at)
		 VALUES ($1::uuid, $2::uuid, 'stale-test', 'general', 'normal', $3, $4, $5, $5)`,
		id, userID, status, lastReply, created); err != nil {
		t.Fatalf("insert ticket: %v", err)
	}
	return id
}

func ticketStatus(t *testing.T, d *db.DB, id string) string {
	t.Helper()
	var s string
	if err := d.Pool.QueryRow(context.Background(),
		`SELECT status FROM support_tickets WHERE id = $1::uuid`, id).Scan(&s); err != nil {
		t.Fatalf("select ticket status: %v", err)
	}
	return s
}

func TestAutoCloseStaleTickets(t *testing.T) {
	d := testWebhookBillingDB(t)
	defer d.Close()
	ctx := context.Background()
	user := mkTicketUser(t, d, "user")

	old := 31
	recent := 2

	cases := []struct {
		name       string
		status     string
		created    int
		lastReply  *int
		wantClosed bool
	}{
		{"old open with old reply closes", "open", 40, &old, true},
		{"old resolved with no reply closes", "resolved", 35, nil, true},
		{"old pending closes", "pending", 33, &old, true},
		{"recent open stays", "open", 5, &recent, false},
		{"old answered but recent reply stays", "answered", 60, &recent, false},
		{"old closed untouched", "closed", 40, &old, false}, // excluded by WHERE; stays closed
	}
	ids := map[string]string{}
	for _, tc := range cases {
		ids[tc.name] = insertStaleTicket(t, d, user, tc.status, tc.created, tc.lastReply)
	}
	t.Cleanup(func() {
		_, _ = d.Pool.Exec(context.Background(), `DELETE FROM support_tickets WHERE subject = 'stale-test'`)
	})

	closed, err := AutoCloseStaleTickets(ctx, d, nil, 30)
	if err != nil {
		t.Fatalf("AutoCloseStaleTickets: %v", err)
	}
	if closed != 3 {
		t.Fatalf("closed = %d, want 3", closed)
	}
	for _, tc := range cases {
		got := ticketStatus(t, d, ids[tc.name])
		want := tc.status
		if tc.wantClosed {
			want = "closed"
		}
		if got != want {
			t.Errorf("%s: status = %s, want %s", tc.name, got, want)
		}
	}

	// days <= 0 falls back to 30 and finds nothing new.
	if _, err := AutoCloseStaleTickets(ctx, d, nil, 0); err != nil {
		t.Fatalf("AutoCloseStaleTickets(0): %v", err)
	}
}

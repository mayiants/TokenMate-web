package routes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Regression: ip_address is INET. Scanning it into *string fails in pgx, and
// the list handler used to ignore the scan error, returning rows with a zero
// created_at once client IPs started being recorded for every request.
func TestAuditLogList_InetAndNullColumns(t *testing.T) {
	e := newAuditVerifyEnv(t)
	var id int64
	require.NoError(t, e.d.Pool.QueryRow(context.Background(),
		`INSERT INTO audit_logs (user_id, user_email, category, action, level, message, ip_address, user_agent)
		 VALUES (NULL, NULL, 'route-test', 'user_created', 'info', 'inet row', '203.0.113.7', 'ua-test')
		 RETURNING id`).Scan(&id))

	// List (system_admin path applies the hot-window filter too).
	for _, role := range []string{"super_admin", "system_admin"} {
		w := e.do(t, role, "GET", "/api/admin/audit-logs?limit=200", "")
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		var body struct {
			Logs []struct {
				ID        int64     `json:"id"`
				Action    string    `json:"action"`
				IPAddress *string   `json:"ip_address"`
				CreatedAt time.Time `json:"created_at"`
			} `json:"logs"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		require.Len(t, body.Logs, 1, role)
		got := body.Logs[0]
		assert.Equal(t, id, got.ID, role)
		assert.Equal(t, "user_created", got.Action, role)
		require.NotNil(t, got.IPAddress, role)
		assert.Equal(t, "203.0.113.7", *got.IPAddress, role)
		assert.WithinDuration(t, time.Now(), got.CreatedAt, 24*time.Hour, role)
	}

	// Detail.
	w := e.do(t, "super_admin", "GET", fmt.Sprintf("/api/admin/audit-logs/%d", id), "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var detail map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &detail))
	assert.Equal(t, "203.0.113.7", detail["ip_address"])
	assert.Equal(t, "ua-test", detail["user_agent"])

	// CSV export.
	w = e.do(t, "super_admin", "GET", "/api/admin/audit-logs/export", "")
	require.Equal(t, http.StatusOK, w.Code)
	assert.True(t, strings.Contains(w.Body.String(), "203.0.113.7"), w.Body.String())
}

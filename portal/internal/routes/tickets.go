package routes

import (
	"context"
	"fmt"
	"mime"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mayiants/ai-portal-go/internal/config"
	"github.com/mayiants/ai-portal-go/internal/db"
	"github.com/mayiants/ai-portal-go/internal/middleware"
	"github.com/mayiants/ai-portal-go/internal/services"
	"github.com/mayiants/ai-portal-go/internal/storage"
	"github.com/mayiants/ai-portal-go/pkg/audit"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

// TicketRoutes implements the support ticket system (control-plane only).
// See docs/support-ticket-system-design-202611042000.md.
type TicketRoutes struct {
	db      *db.DB
	auth    *middleware.Auth
	notif   *services.NotificationService
	audit   *audit.Logger
	cfg     *config.Config
	storage storage.Provider
	logger  *zap.Logger
}

func NewTicketRoutes(database *db.DB, auth *middleware.Auth, notif *services.NotificationService, auditLog *audit.Logger, cfg *config.Config, storageProvider storage.Provider, logger *zap.Logger) *TicketRoutes {
	return &TicketRoutes{db: database, auth: auth, notif: notif, audit: auditLog, cfg: cfg, storage: storageProvider, logger: logger}
}

// Register mounts the user surface (/api/tickets) and the admin surface
// (/api/admin/tickets). The admin group is mounted by the caller under the
// admin router with RequireAdmin already applied.
func (r *TicketRoutes) Register(rg *gin.RouterGroup) {
	g := rg.Use(r.auth.RequireAuth())
	g.POST("", r.create)
	g.GET("", r.listMine)
	g.GET("/:id", r.detail)
	g.POST("/:id/messages", r.reply)
	g.POST("/:id/close", r.close)
	g.POST("/:id/attachments", r.uploadAttachments)
	g.GET("/:id/attachments/:aid", r.downloadAttachment)
	// Immediate-upload staging: files are stored on selection, listed with a
	// remove option, and linked to the comment by create/reply/adminReply.
	g.POST("/attachments/stage", r.stageAttachments)
	g.DELETE("/attachments/staged/:aid", r.unstageAttachment)
}

// RegisterAdmin mounts the admin surface. rg must already have RequireAdmin.
func (r *TicketRoutes) RegisterAdmin(rg *gin.RouterGroup) {
	rg.GET("/tickets", r.adminList)
	rg.GET("/tickets/stats", r.adminStats)
	rg.GET("/tickets/:id", r.adminDetail)
	rg.POST("/tickets/:id/messages", r.adminReply)
	rg.PATCH("/tickets/:id", r.adminPatch)
}

var ticketCategories = map[string]bool{"general": true, "billing": true, "technical": true, "abuse": true, "feature": true}
var ticketPriorities = map[string]bool{"low": true, "normal": true, "high": true, "urgent": true}
var ticketStatuses = map[string]bool{"open": true, "pending": true, "answered": true, "resolved": true, "closed": true}

const (
	maxTicketSubject = 255
	maxTicketBody    = 10000
)

func isAdminRole(role string) bool { return role == "system_admin" || role == "super_admin" }

// ---- user surface ----

func (r *TicketRoutes) create(c *gin.Context) {
	userID := c.GetString("user_id")
	var req struct {
		Subject             string   `json:"subject"`
		Category            string   `json:"category"`
		Priority            string   `json:"priority"`
		Body                string   `json:"body"`
		StagedAttachmentIDs []string `json:"staged_attachment_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}
	req.Subject = strings.TrimSpace(req.Subject)
	req.Body = strings.TrimSpace(req.Body)
	if req.Subject == "" || req.Body == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Subject and message are required"}})
		return
	}
	if len(req.Subject) > maxTicketSubject || len(req.Body) > maxTicketBody {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Subject or message too long"}})
		return
	}
	if !ticketCategories[req.Category] {
		req.Category = "general"
	}
	if !ticketPriorities[req.Priority] {
		req.Priority = "normal"
	}

	ctx := c.Request.Context()
	var entID *string
	if v := c.GetString("enterprise_id"); v != "" {
		entID = &v
	}

	var ticketID string
	var ticketNo int64
	err := r.db.WithTx(ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx,
			`INSERT INTO support_tickets (user_id, enterprise_id, subject, category, priority, status, last_reply_role, last_reply_at)
			 VALUES ($1::uuid, $2::uuid, $3, $4, $5, 'open', 'user', NOW())
			 RETURNING id::text, ticket_no`,
			userID, entID, req.Subject, req.Category, req.Priority,
		).Scan(&ticketID, &ticketNo); err != nil {
			return err
		}
		var firstMsgID string
		if err := tx.QueryRow(ctx,
			`INSERT INTO support_ticket_messages (ticket_id, author_id, author_role, body)
			 VALUES ($1::uuid, $2::uuid, 'user', $3) RETURNING id::text`,
			ticketID, userID, req.Body).Scan(&firstMsgID); err != nil {
			return err
		}
		linkStagedAttachments(ctx, tx, userID, firstMsgID, req.StagedAttachmentIDs)
		return nil
	})
	if err != nil {
		r.logger.Error("create ticket failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to create ticket"}})
		return
	}

	// Notify system admins (in-app). Link back to the admin ticket view.
	if r.notif != nil {
		r.notif.NotifySystemAdmins(ctx, "support_ticket", "normal",
			"notif.ticket_new.title", "notif.ticket_new.body",
			map[string]string{"subject": req.Subject},
			"/admin/tickets", "ticket", ticketID)
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "id": ticketID, "ticket_no": ticketNo})
}

func (r *TicketRoutes) listMine(c *gin.Context) {
	userID := c.GetString("user_id")
	status := c.Query("status")
	ctx := c.Request.Context()
	rows, err := r.db.Query(ctx,
		`SELECT id::text, ticket_no, subject, category, priority, status,
		        last_reply_role, last_reply_at, created_at, updated_at
		 FROM support_tickets
		 WHERE user_id = $1::uuid AND deleted_at IS NULL
		   AND ($2 = '' OR status = $2)
		 ORDER BY updated_at DESC LIMIT 200`, userID, status)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to list tickets"}})
		return
	}
	defer rows.Close()
	c.JSON(http.StatusOK, gin.H{"tickets": scanTicketList(rows)})
}

func (r *TicketRoutes) detail(c *gin.Context) {
	userID := c.GetString("user_id")
	id := c.Param("id")
	ctx := c.Request.Context()
	// Ownership: requester, or enterprise admin of the ticket's enterprise.
	t, ok := r.loadTicketForUser(ctx, c, id)
	if !ok {
		return
	}
	// Messages: hide internal notes from the user surface.
	msgs, err := r.loadMessages(ctx, id, false)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to load messages"}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ticket": t, "messages": msgs})
	_ = userID
}

func (r *TicketRoutes) reply(c *gin.Context) {
	userID := c.GetString("user_id")
	id := c.Param("id")
	ctx := c.Request.Context()
	var req struct {
		Body                string   `json:"body"`
		StagedAttachmentIDs []string `json:"staged_attachment_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}
	if len(req.Body) > maxTicketBody {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Message too long"}})
		return
	}
	if _, ok := r.loadTicketForUser(ctx, c, id); !ok {
		return
	}
	var assignee *string
	err := r.db.WithTx(ctx, func(tx pgx.Tx) error {
		var msgID string
		if err := tx.QueryRow(ctx,
			`INSERT INTO support_ticket_messages (ticket_id, author_id, author_role, body)
			 VALUES ($1::uuid, $2::uuid, 'user', $3) RETURNING id::text`,
			id, userID, strings.TrimSpace(req.Body)).Scan(&msgID); err != nil {
			return err
		}
		linkStagedAttachments(ctx, tx, userID, msgID, req.StagedAttachmentIDs)
		// A user reply reopens a resolved ticket and marks it pending.
		return tx.QueryRow(ctx,
			`UPDATE support_tickets
			 SET last_reply_role='user', last_reply_at=NOW(), updated_at=NOW(),
			     status = CASE WHEN status IN ('resolved','closed','answered') THEN 'pending' ELSE status END
			 WHERE id=$1::uuid RETURNING assignee_id::text`, id).Scan(&assignee)
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to reply"}})
		return
	}
	if r.notif != nil {
		// Notify the assignee if set, else all system admins.
		if assignee != nil && *assignee != "" {
			r.notif.NotifyUser(ctx, *assignee, "support_ticket", "system_admin", "normal",
				"New reply on a support ticket", "", "/admin/tickets", "ticket", id)
		} else {
			r.notif.NotifySystemAdmins(ctx, "support_ticket", "normal",
				"notif.ticket_reply.title", "notif.ticket_reply.body", nil, "/admin/tickets", "ticket", id)
		}
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (r *TicketRoutes) close(c *gin.Context) {
	id := c.Param("id")
	ctx := c.Request.Context()
	if _, ok := r.loadTicketForUser(ctx, c, id); !ok {
		return
	}
	if err := r.db.Exec(ctx,
		`UPDATE support_tickets SET status='closed', updated_at=NOW() WHERE id=$1::uuid`, id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to close ticket"}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ---- admin surface ----

func (r *TicketRoutes) adminList(c *gin.Context) {
	ctx := c.Request.Context()
	status, category, priority, assignee, search := c.Query("status"), c.Query("category"), c.Query("priority"), c.Query("assignee"), strings.TrimSpace(c.Query("search"))
	rows, err := r.db.Query(ctx,
		`SELECT t.id::text, t.ticket_no, t.subject, t.category, t.priority, t.status,
		        t.last_reply_role, t.last_reply_at, t.created_at, t.updated_at,
		        COALESCE(u.email,''), COALESCE(a.email,'')
		 FROM support_tickets t
		 JOIN users u ON u.id = t.user_id
		 LEFT JOIN users a ON a.id = t.assignee_id
		 WHERE t.deleted_at IS NULL
		   AND ($1='' OR t.status=$1) AND ($2='' OR t.category=$2) AND ($3='' OR t.priority=$3)
		   AND ($4='' OR t.assignee_id=$4::uuid)
		   AND ($5='' OR t.subject ILIKE '%'||$5||'%' OR u.email ILIKE '%'||$5||'%')
		 ORDER BY (t.status IN ('open','pending')) DESC, t.updated_at DESC
		 LIMIT 300`, status, category, priority, assignee, search)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to list tickets"}})
		return
	}
	defer rows.Close()
	tickets := []gin.H{}
	for rows.Next() {
		var id, subject, category, priority, st string
		var no int64
		var lastRole *string
		var lastAt, createdAt, updatedAt interface{}
		var requester, assigneeEmail string
		if err := rows.Scan(&id, &no, &subject, &category, &priority, &st, &lastRole, &lastAt, &createdAt, &updatedAt, &requester, &assigneeEmail); err == nil {
			tickets = append(tickets, gin.H{
				"id": id, "ticket_no": no, "subject": subject, "category": category,
				"priority": priority, "status": st, "last_reply_role": derefStr(lastRole),
				"last_reply_at": lastAt, "created_at": createdAt, "updated_at": updatedAt,
				"requester_email": requester, "assignee_email": assigneeEmail,
			})
		}
	}
	c.JSON(http.StatusOK, gin.H{"tickets": tickets})
}

func (r *TicketRoutes) adminStats(c *gin.Context) {
	ctx := c.Request.Context()
	stats := gin.H{"open": 0, "pending": 0, "answered": 0, "resolved": 0, "closed": 0, "needs_reply": 0}
	rows, err := r.db.Query(ctx, `SELECT status, COUNT(*) FROM support_tickets WHERE deleted_at IS NULL GROUP BY status`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var st string
			var n int
			if rows.Scan(&st, &n) == nil {
				stats[st] = n
			}
		}
	}
	var needsReply int
	_ = r.db.QueryRow(ctx, `SELECT COUNT(*) FROM support_tickets WHERE deleted_at IS NULL AND last_reply_role='user' AND status IN ('open','pending')`).Scan(&needsReply)
	stats["needs_reply"] = needsReply
	c.JSON(http.StatusOK, gin.H{"stats": stats})
}

func (r *TicketRoutes) adminDetail(c *gin.Context) {
	ctx := c.Request.Context()
	id := c.Param("id")
	t, err := r.loadTicket(ctx, id)
	if err != nil || t == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Ticket not found"}})
		return
	}
	msgs, err := r.loadMessages(ctx, id, true) // include internal notes
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to load messages"}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ticket": t, "messages": msgs})
}

func (r *TicketRoutes) adminReply(c *gin.Context) {
	adminID := c.GetString("user_id")
	id := c.Param("id")
	ctx := c.Request.Context()
	var req struct {
		Body                string   `json:"body"`
		InternalNote        bool     `json:"internal_note"`
		StagedAttachmentIDs []string `json:"staged_attachment_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}
	if len(req.Body) > maxTicketBody {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Message too long"}})
		return
	}
	var requesterID string
	err := r.db.WithTx(ctx, func(tx pgx.Tx) error {
		var msgID string
		if err := tx.QueryRow(ctx,
			`INSERT INTO support_ticket_messages (ticket_id, author_id, author_role, body, internal_note)
			 VALUES ($1::uuid, $2::uuid, 'admin', $3, $4) RETURNING id::text`,
			id, adminID, strings.TrimSpace(req.Body), req.InternalNote).Scan(&msgID); err != nil {
			return err
		}
		linkStagedAttachments(ctx, tx, adminID, msgID, req.StagedAttachmentIDs)
		// Internal notes do not change the ticket state or notify the user.
		if req.InternalNote {
			return tx.QueryRow(ctx, `SELECT user_id::text FROM support_tickets WHERE id=$1::uuid`, id).Scan(&requesterID)
		}
		return tx.QueryRow(ctx,
			`UPDATE support_tickets
			 SET last_reply_role='admin', last_reply_at=NOW(), updated_at=NOW(),
			     status = CASE WHEN status IN ('open','pending') THEN 'answered' ELSE status END,
			     assignee_id = COALESCE(assignee_id, $2::uuid)
			 WHERE id=$1::uuid RETURNING user_id::text`, id, adminID).Scan(&requesterID)
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to reply"}})
		return
	}
	if r.audit != nil {
		r.audit.Log(ctx, adminID, c.GetString("email"), "admin", "ticket_reply", "info", "Replied to ticket "+id)
	}
	if !req.InternalNote && r.notif != nil && requesterID != "" {
		r.notif.NotifyUser(ctx, requesterID, "support_ticket", "user", "normal",
			"Your support ticket has a new reply", "", "/support", "ticket", id)
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (r *TicketRoutes) adminPatch(c *gin.Context) {
	adminID := c.GetString("user_id")
	id := c.Param("id")
	ctx := c.Request.Context()
	var req struct {
		Status   *string `json:"status"`
		Priority *string `json:"priority"`
		Category *string `json:"category"`
		Assignee *string `json:"assignee_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}
	sets := []string{}
	args := []interface{}{}
	n := 1
	add := func(col string, val interface{}) {
		sets = append(sets, col+" = $"+strconv.Itoa(n))
		args = append(args, val)
		n++
	}
	if req.Status != nil {
		if !ticketStatuses[*req.Status] {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid status"}})
			return
		}
		add("status", *req.Status)
	}
	if req.Priority != nil {
		if !ticketPriorities[*req.Priority] {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid priority"}})
			return
		}
		add("priority", *req.Priority)
	}
	if req.Category != nil {
		if !ticketCategories[*req.Category] {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid category"}})
			return
		}
		add("category", *req.Category)
	}
	if req.Assignee != nil {
		if *req.Assignee == "" {
			sets = append(sets, "assignee_id = NULL")
		} else {
			add("assignee_id", *req.Assignee)
		}
	}
	if len(sets) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Nothing to update"}})
		return
	}
	sets = append(sets, "updated_at = NOW()")
	args = append(args, id)
	sql := "UPDATE support_tickets SET " + strings.Join(sets, ", ") + " WHERE id = $" + strconv.Itoa(n) + "::uuid"
	if err := r.db.Exec(ctx, sql, args...); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to update ticket"}})
		return
	}
	if r.audit != nil {
		r.audit.Log(ctx, adminID, c.GetString("email"), "admin", "ticket_updated", "info", "Updated ticket "+id)
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ---- helpers ----

// scanTicketList scans the user-facing ticket list columns.
func scanTicketList(rows pgx.Rows) []gin.H {
	out := []gin.H{}
	for rows.Next() {
		var id, subject, category, priority, st string
		var no int64
		var lastRole *string
		var lastAt, createdAt, updatedAt interface{}
		if err := rows.Scan(&id, &no, &subject, &category, &priority, &st, &lastRole, &lastAt, &createdAt, &updatedAt); err == nil {
			out = append(out, gin.H{
				"id": id, "ticket_no": no, "subject": subject, "category": category,
				"priority": priority, "status": st, "last_reply_role": derefStr(lastRole),
				"last_reply_at": lastAt, "created_at": createdAt, "updated_at": updatedAt,
			})
		}
	}
	return out
}

// loadTicket loads a ticket row (admin surface, no ownership check).
func (r *TicketRoutes) loadTicket(ctx context.Context, id string) (gin.H, error) {
	var tid, subject, category, priority, st, userEmail string
	var no int64
	var entID, assignee, lastRole *string
	var lastAt, createdAt, updatedAt interface{}
	err := r.db.QueryRow(ctx,
		`SELECT t.id::text, t.ticket_no, t.subject, t.category, t.priority, t.status,
		        t.enterprise_id::text, t.assignee_id::text, t.last_reply_role, t.last_reply_at,
		        t.created_at, t.updated_at, COALESCE(u.email,'')
		 FROM support_tickets t JOIN users u ON u.id=t.user_id
		 WHERE t.id=$1::uuid AND t.deleted_at IS NULL`, id).
		Scan(&tid, &no, &subject, &category, &priority, &st, &entID, &assignee, &lastRole, &lastAt, &createdAt, &updatedAt, &userEmail)
	if err != nil {
		return nil, err
	}
	return gin.H{
		"id": tid, "ticket_no": no, "subject": subject, "category": category, "priority": priority,
		"status": st, "enterprise_id": derefStr(entID), "assignee_id": derefStr(assignee),
		"last_reply_role": derefStr(lastRole), "last_reply_at": lastAt,
		"created_at": createdAt, "updated_at": updatedAt, "requester_email": userEmail,
	}, nil
}

// loadTicketForUser loads a ticket enforcing user ownership (requester, or
// enterprise admin of the ticket's enterprise). Writes the HTTP error itself
// and returns ok=false when not permitted.
func (r *TicketRoutes) loadTicketForUser(ctx context.Context, c *gin.Context, id string) (gin.H, bool) {
	userID := c.GetString("user_id")
	role := c.GetString("role")
	entID := c.GetString("enterprise_id")
	var ownerID, tEnt string
	var t gin.H
	full, err := r.loadTicket(ctx, id)
	if err != nil || full == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Ticket not found"}})
		return nil, false
	}
	t = full
	// re-read owner for the permission check
	_ = r.db.QueryRow(ctx, `SELECT user_id::text, COALESCE(enterprise_id::text,'') FROM support_tickets WHERE id=$1::uuid`, id).Scan(&ownerID, &tEnt)
	allowed := ownerID == userID ||
		isAdminRole(role) ||
		(role == "enterprise_admin" && entID != "" && tEnt == entID)
	if !allowed {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "Not allowed"}})
		return nil, false
	}
	return t, true
}

// loadMessages returns the thread. includeInternal=false hides admin notes.
func (r *TicketRoutes) loadMessages(ctx context.Context, ticketID string, includeInternal bool) ([]gin.H, error) {
	rows, err := r.db.Query(ctx,
		`SELECT m.id::text, m.author_role, m.body, m.internal_note, m.created_at, COALESCE(u.email,'')
		 FROM support_ticket_messages m JOIN users u ON u.id=m.author_id
		 WHERE m.ticket_id=$1::uuid AND ($2 OR m.internal_note = FALSE)
		 ORDER BY m.created_at ASC`, ticketID, includeInternal)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []gin.H{}
	for rows.Next() {
		var mid, role, body, email string
		var internal bool
		var createdAt interface{}
		if err := rows.Scan(&mid, &role, &body, &internal, &createdAt, &email); err == nil {
			out = append(out, gin.H{
				"id": mid, "author_role": role, "body": body,
				"internal_note": internal, "created_at": createdAt, "author_email": email,
				"attachments": r.messageAttachments(ctx, mid),
			})
		}
	}
	return out, nil
}

// ---- attachments ----

const (
	maxTicketAttachments    = 5                // per upload request
	maxTicketAttachmentSize = 10 * 1024 * 1024 // 10 MB per file
)

// messageAttachments returns the attachment metadata for a message (no bodies).
func (r *TicketRoutes) messageAttachments(ctx context.Context, messageID string) []gin.H {
	rows, err := r.db.Query(ctx,
		`SELECT id::text, filename, size_bytes, mime_type FROM support_ticket_attachments
		 WHERE message_id=$1::uuid ORDER BY created_at ASC`, messageID)
	if err != nil {
		return []gin.H{}
	}
	defer rows.Close()
	out := []gin.H{}
	for rows.Next() {
		var id, filename string
		var size *int64
		var mimeType *string
		if err := rows.Scan(&id, &filename, &size, &mimeType); err == nil {
			var sz int64
			if size != nil {
				sz = *size
			}
			out = append(out, gin.H{"id": id, "filename": filename, "size_bytes": sz, "mime_type": derefStr(mimeType)})
		}
	}
	return out
}

// uploadAttachments attaches up to maxTicketAttachments files to the ticket's
// latest message by the current user. The caller must own/access the ticket.
func (r *TicketRoutes) uploadAttachments(c *gin.Context) {
	userID := c.GetString("user_id")
	id := c.Param("id")
	ctx := c.Request.Context()
	if r.storage == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"message": "Storage not configured"}})
		return
	}
	if _, ok := r.loadTicketForUser(ctx, c, id); !ok {
		return
	}
	if err := c.Request.ParseMultipartForm(maxTicketAttachmentSize + 1024*1024); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Failed to parse upload"}})
		return
	}
	var files []*multipart.FileHeader
	if c.Request.MultipartForm != nil {
		files = c.Request.MultipartForm.File["files"]
	}
	if len(files) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "No files"}})
		return
	}
	if len(files) > maxTicketAttachments {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": fmt.Sprintf("At most %d files per upload", maxTicketAttachments)}})
		return
	}

	// Attach to the user's most recent message on this ticket; if none exists
	// (shouldn't happen — create always inserts one), create a wrapper message.
	var msgID string
	if err := r.db.QueryRow(ctx,
		`SELECT id::text FROM support_ticket_messages WHERE ticket_id=$1::uuid AND author_id=$2::uuid
		 ORDER BY created_at DESC LIMIT 1`, id, userID).Scan(&msgID); err != nil {
		if err2 := r.db.QueryRow(ctx,
			`INSERT INTO support_ticket_messages (ticket_id, author_id, author_role, body)
			 VALUES ($1::uuid,$2::uuid,'user','(attachment)') RETURNING id::text`, id, userID).Scan(&msgID); err2 != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to attach"}})
			return
		}
	}

	saved := []gin.H{}
	for _, fh := range files {
		if fh.Size > maxTicketAttachmentSize {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": fmt.Sprintf("%s exceeds %d MB", fh.Filename, maxTicketAttachmentSize/(1024*1024))}})
			return
		}
		contentType := fh.Header.Get("Content-Type")
		if contentType == "" {
			contentType = mime.TypeByExtension(filepath.Ext(fh.Filename))
		}
		if !ticketAttachmentAllowed(contentType, fh.Filename) {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": fmt.Sprintf("File type not allowed: %s", fh.Filename)}})
			return
		}
		opened, err := fh.Open()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to read file"}})
			return
		}
		key := "tickets/" + id + "/" + uuid.NewString() + filepath.Ext(fh.Filename)
		if err := r.storage.Upload(ctx, key, opened, storage.UploadOptions{ContentType: contentType}); err != nil {
			_ = opened.Close()
			r.logger.Error("ticket attachment upload failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to store file"}})
			return
		}
		_ = opened.Close()
		var aid string
		if err := r.db.QueryRow(ctx,
			`INSERT INTO support_ticket_attachments (message_id, storage_key, filename, size_bytes, mime_type)
			 VALUES ($1::uuid,$2,$3,$4,$5) RETURNING id::text`,
			msgID, key, fh.Filename, fh.Size, contentType).Scan(&aid); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to record attachment"}})
			return
		}
		saved = append(saved, gin.H{"id": aid, "filename": fh.Filename, "size_bytes": fh.Size, "mime_type": contentType})
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "attachments": saved})
}

// downloadAttachment streams an attachment to a caller who may access the
// parent ticket (requester, enterprise admin of the ticket, or system admin).
func (r *TicketRoutes) downloadAttachment(c *gin.Context) {
	id := c.Param("id")
	aid := c.Param("aid")
	ctx := c.Request.Context()
	if r.storage == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"message": "Storage not configured"}})
		return
	}
	if _, ok := r.loadTicketForUser(ctx, c, id); !ok {
		return
	}
	var key, filename, mimeType string
	var size *int64
	// Join through the message to confirm the attachment belongs to THIS ticket.
	if err := r.db.QueryRow(ctx,
		`SELECT a.storage_key, a.filename, COALESCE(a.mime_type,''), a.size_bytes
		 FROM support_ticket_attachments a
		 JOIN support_ticket_messages m ON m.id = a.message_id
		 WHERE a.id=$1::uuid AND m.ticket_id=$2::uuid`, aid, id).Scan(&key, &filename, &mimeType, &size); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Attachment not found"}})
		return
	}
	reader, err := r.storage.Download(ctx, key)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to read attachment"}})
		return
	}
	defer func() { _ = reader.Close() }()
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
	var sz int64 = -1
	if size != nil {
		sz = *size
	}
	c.DataFromReader(http.StatusOK, sz, mimeType, reader, nil)
}

// ticketAttachmentAllowed accepts common support-attachment types: images, pdf,
// and plain text/log/csv + office docs. Checks MIME first, then extension.
func ticketAttachmentAllowed(contentType, filename string) bool {
	ct := strings.ToLower(strings.TrimSpace(contentType))
	if strings.HasPrefix(ct, "image/") {
		return true
	}
	mimeOK := map[string]bool{
		"application/pdf": true, "text/plain": true, "text/csv": true,
		"application/msword": true,
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document": true,
		// application/octet-stream is NOT whitelisted wholesale: browsers send
		// it for unknown types (including .exe), so it must fall through to the
		// extension check — which is what lets .log files through.
	}
	if mimeOK[ct] {
		return true
	}
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(filename), "."))
	switch ext {
	case "png", "jpg", "jpeg", "gif", "webp", "bmp", "svg",
		"pdf", "txt", "log", "csv", "doc", "docx":
		return true
	}
	return false
}

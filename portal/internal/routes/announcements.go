package routes

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mayiants/ai-portal-go/internal/db"
	"github.com/mayiants/ai-portal-go/internal/middleware"
	"github.com/gin-gonic/gin"
)

type AnnouncementRoutes struct {
	db   *db.DB
	auth *middleware.Auth
}

func NewAnnouncementRoutes(db *db.DB, auth *middleware.Auth) *AnnouncementRoutes {
	return &AnnouncementRoutes{db: db, auth: auth}
}

func (r *AnnouncementRoutes) Register(rg *gin.RouterGroup) {
	// Public endpoints (require auth for read tracking)
	rg.GET("/announcements", r.auth.RequireAuth(), r.listPublished)
	rg.GET("/announcements/unread-count", r.auth.RequireAuth(), r.getUnreadCount)
	rg.POST("/announcements/:id/read", r.auth.RequireAuth(), r.markRead)

	// Admin endpoints
	admin := rg.Group("/admin/announcements")
	admin.Use(r.auth.RequireAuth(), r.auth.RequireAdmin())
	admin.GET("", r.adminList)
	admin.POST("", r.create)
	admin.GET("/:id", r.get)
	admin.PUT("/:id", r.update)
	admin.DELETE("/:id", r.delete)
	admin.POST("/:id/publish", r.publish)
	admin.POST("/:id/archive", r.archive)
}

func (r *AnnouncementRoutes) listPublished(c *gin.Context) {
	userID := c.GetString("user_id")
	role := c.GetString("role")

	rows, err := r.db.Query(c.Request.Context(),
		`SELECT id, title, content, type, pinned, show_popup, start_at, end_at, created_at,
		        display_mode, cta_text, cta_link, banner_color, dismiss_mode, target_audience, icon_emoji
		 FROM announcements
		 WHERE status = 'published'
		   AND (start_at IS NULL OR start_at <= NOW())
		   AND (end_at IS NULL OR end_at >= NOW())
		   AND (
		     target_audience = 'all'
		     OR (target_audience = 'new_user' AND EXISTS (
		       SELECT 1 FROM users WHERE id = $1::uuid AND created_at > NOW() - INTERVAL '7 days'
		     ))
		     OR (target_audience = 'enterprise' AND $2 IN ('enterprise_admin', 'enterprise_member'))
		     OR (target_audience = 'free' AND NOT EXISTS (
		       SELECT 1 FROM user_subscriptions WHERE user_id = $1::uuid AND status = 'active'
		     ))
		   )
		   AND (dismiss_mode <> 'permanent' OR NOT EXISTS (
		     SELECT 1 FROM announcement_reads ar
		     WHERE ar.announcement_id = announcements.id AND ar.user_id = $1::uuid
		   ))
		 ORDER BY pinned DESC, priority DESC, created_at DESC`,
		userID, role)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed"}})
		return
	}
	defer rows.Close()

	announcements := []gin.H{}
	for rows.Next() {
		var id string
		var title, content, aType string
		var pinned, showPopup bool
		var startAt, endAt *time.Time
		var createdAt time.Time
		var displayMode, bannerColor, dismissMode, targetAudience string
		var ctaText, ctaLink, iconEmoji *string
		_ = rows.Scan(&id, &title, &content, &aType, &pinned, &showPopup, &startAt, &endAt, &createdAt,
			&displayMode, &ctaText, &ctaLink, &bannerColor, &dismissMode, &targetAudience, &iconEmoji)
		item := gin.H{
			"id": id, "title": title, "content": content, "type": aType,
			"pinned": pinned, "show_popup": showPopup,
			"start_at": startAt, "end_at": endAt, "created_at": createdAt,
			"display_mode": displayMode, "banner_color": bannerColor,
			"dismiss_mode": dismissMode, "target_audience": targetAudience,
		}
		if ctaText != nil {
			item["cta_text"] = *ctaText
		}
		if ctaLink != nil {
			item["cta_link"] = *ctaLink
		}
		if iconEmoji != nil {
			item["icon_emoji"] = *iconEmoji
		}
		announcements = append(announcements, item)
	}
	c.JSON(http.StatusOK, gin.H{"announcements": announcements})
}

func (r *AnnouncementRoutes) getUnreadCount(c *gin.Context) {
	userID := c.GetString("user_id")
	role := c.GetString("role")

	var count int
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT COUNT(*) FROM announcements a
		 WHERE a.status = 'published'
		   AND (a.start_at IS NULL OR a.start_at <= NOW())
		   AND (a.end_at IS NULL OR a.end_at >= NOW())
		   AND (
		     a.target_audience = 'all'
		     OR (a.target_audience = 'new_user' AND EXISTS (
		       SELECT 1 FROM users WHERE id = $1::uuid AND created_at > NOW() - INTERVAL '7 days'
		     ))
		     OR (a.target_audience = 'enterprise' AND $2 IN ('enterprise_admin', 'enterprise_member'))
		     OR (a.target_audience = 'free' AND NOT EXISTS (
		       SELECT 1 FROM user_subscriptions WHERE user_id = $1::uuid AND status = 'active'
		     ))
		   )
		   AND NOT EXISTS (
		     SELECT 1 FROM announcement_reads ar
		     WHERE ar.announcement_id = a.id AND ar.user_id = $1::uuid
		   )`,
		userID, role).Scan(&count)
	c.JSON(http.StatusOK, gin.H{"unread_count": count})
}

func (r *AnnouncementRoutes) markRead(c *gin.Context) {
	userID := c.GetString("user_id")
	id := c.Param("id")
	_, err := r.db.Pool.Exec(c.Request.Context(),
		`INSERT INTO announcement_reads (announcement_id, user_id, read_at)
		 VALUES ($1::uuid, $2::uuid, NOW()) ON CONFLICT DO NOTHING`,
		id, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed"}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (r *AnnouncementRoutes) adminList(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}
	offset := (page - 1) * limit

	var total int
	_ = r.db.QueryRow(c.Request.Context(),
		"SELECT COUNT(*) FROM announcements").Scan(&total)

	rows, err := r.db.Query(c.Request.Context(),
		`SELECT id, title, type, status, pinned, priority, display_mode, target_audience,
		        cta_text, cta_link, banner_color, dismiss_mode, icon_emoji,
		        start_at, end_at, created_by, created_at, updated_at
		 FROM announcements ORDER BY created_at DESC LIMIT $1 OFFSET $2`,
		limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed"}})
		return
	}
	defer rows.Close()

	items := []gin.H{}
	for rows.Next() {
		var id string
		var title, aType, status string
		var pinned bool
		var priority int
		var displayMode, targetAudience, bannerColor, dismissMode string
		var ctaText, ctaLink, iconEmoji *string
		var startAt, endAt *time.Time
		var createdBy *string
		var createdAt, updatedAt time.Time
		_ = rows.Scan(&id, &title, &aType, &status, &pinned, &priority,
			&displayMode, &targetAudience, &ctaText, &ctaLink, &bannerColor, &dismissMode, &iconEmoji,
			&startAt, &endAt, &createdBy, &createdAt, &updatedAt)
		item := gin.H{
			"id": id, "title": title, "type": aType, "status": status,
			"pinned": pinned, "priority": priority,
			"display_mode": displayMode, "target_audience": targetAudience,
			"banner_color": bannerColor, "dismiss_mode": dismissMode,
			"start_at": startAt, "end_at": endAt,
			"created_by": createdBy, "created_at": createdAt, "updated_at": updatedAt,
		}
		if ctaText != nil {
			item["cta_text"] = *ctaText
		}
		if ctaLink != nil {
			item["cta_link"] = *ctaLink
		}
		if iconEmoji != nil {
			item["icon_emoji"] = *iconEmoji
		}
		items = append(items, item)
	}
	c.JSON(http.StatusOK, gin.H{
		"announcements": items, "total": total,
		"page": page, "limit": limit,
		"total_pages": (total + limit - 1) / limit,
	})
}

func (r *AnnouncementRoutes) create(c *gin.Context) {
	var req struct {
		Title          string  `json:"title" binding:"required"`
		Content        string  `json:"content" binding:"required"`
		Type           string  `json:"type"`
		Pinned         bool    `json:"pinned"`
		ShowPopup      bool    `json:"show_popup"`
		Priority       int     `json:"priority"`
		DisplayMode    string  `json:"display_mode"`
		CtaText        *string `json:"cta_text"`
		CtaLink        *string `json:"cta_link"`
		BannerColor    string  `json:"banner_color"`
		DismissMode    string  `json:"dismiss_mode"`
		TargetAudience string  `json:"target_audience"`
		IconEmoji      *string `json:"icon_emoji"`
		StartAt        *string `json:"start_at"`
		EndAt          *string `json:"end_at"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": T("error.title_content_required", c)}})
		return
	}
	if req.Type == "" {
		req.Type = "info"
	}
	if req.DisplayMode == "" {
		req.DisplayMode = "modal"
	}
	if req.BannerColor == "" {
		req.BannerColor = "primary"
	}
	if req.DismissMode == "" {
		req.DismissMode = "session"
	}
	if req.TargetAudience == "" {
		req.TargetAudience = "all"
	}

	var startAt, endAt *time.Time
	if req.StartAt != nil {
		t, _ := time.Parse("2006-01-02", *req.StartAt)
		startAt = &t
	}
	if req.EndAt != nil {
		t, _ := time.Parse("2006-01-02", *req.EndAt)
		endAt = &t
	}

	userID := c.GetString("user_id")
	var id string
	err := r.db.QueryRow(c.Request.Context(),
		`INSERT INTO announcements (title, content, type, pinned, show_popup, priority,
		  display_mode, cta_text, cta_link, banner_color, dismiss_mode, target_audience, icon_emoji,
		  start_at, end_at, status, created_by, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, 'draft', $16::uuid, NOW(), NOW()) RETURNING id`,
		req.Title, req.Content, req.Type, req.Pinned, req.ShowPopup, req.Priority,
		req.DisplayMode, req.CtaText, req.CtaLink, req.BannerColor, req.DismissMode, req.TargetAudience, req.IconEmoji,
		startAt, endAt, userID).Scan(&id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": T("error.create_failed", c)}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": id, "success": true})
}

func (r *AnnouncementRoutes) get(c *gin.Context) {
	id := c.Param("id")
	var title, content, aType, status string
	var pinned, showPopup bool
	var priority int
	var displayMode, bannerColor, dismissMode, targetAudience string
	var ctaText, ctaLink, iconEmoji *string
	var startAt, endAt *time.Time
	var createdAt, updatedAt time.Time
	err := r.db.QueryRow(c.Request.Context(),
		`SELECT title, content, type, status, pinned, show_popup, priority,
		        display_mode, cta_text, cta_link, banner_color, dismiss_mode, target_audience, icon_emoji,
		        start_at, end_at, created_at, updated_at
		 FROM announcements WHERE id = $1::uuid`, id).Scan(
		&title, &content, &aType, &status, &pinned, &showPopup, &priority,
		&displayMode, &ctaText, &ctaLink, &bannerColor, &dismissMode, &targetAudience, &iconEmoji,
		&startAt, &endAt, &createdAt, &updatedAt)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Not found"}})
		return
	}

	// Get read count
	var readCount int
	_ = r.db.QueryRow(c.Request.Context(),
		"SELECT COUNT(*) FROM announcement_reads WHERE announcement_id = $1::uuid", id).Scan(&readCount)

	item := gin.H{
		"id": id, "title": title, "content": content, "type": aType,
		"status": status, "pinned": pinned, "show_popup": showPopup,
		"priority":     priority,
		"display_mode": displayMode, "banner_color": bannerColor,
		"dismiss_mode": dismissMode, "target_audience": targetAudience,
		"start_at": startAt, "end_at": endAt,
		"created_at": createdAt, "updated_at": updatedAt,
		"read_count": readCount,
	}
	if ctaText != nil {
		item["cta_text"] = *ctaText
	}
	if ctaLink != nil {
		item["cta_link"] = *ctaLink
	}
	if iconEmoji != nil {
		item["icon_emoji"] = *iconEmoji
	}

	c.JSON(http.StatusOK, item)
}

func (r *AnnouncementRoutes) update(c *gin.Context) {
	id := c.Param("id")
	var req struct {
		Title          string  `json:"title"`
		Content        string  `json:"content"`
		Type           string  `json:"type"`
		Pinned         *bool   `json:"pinned"`
		ShowPopup      *bool   `json:"show_popup"`
		Priority       *int    `json:"priority"`
		DisplayMode    string  `json:"display_mode"`
		CtaText        *string `json:"cta_text"`
		CtaLink        *string `json:"cta_link"`
		BannerColor    string  `json:"banner_color"`
		DismissMode    string  `json:"dismiss_mode"`
		TargetAudience string  `json:"target_audience"`
		IconEmoji      *string `json:"icon_emoji"`
		StartAt        *string `json:"start_at"`
		EndAt          *string `json:"end_at"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}

	var setClauses []string
	var args []interface{}
	argIdx := 1

	if req.Title != "" {
		setClauses = append(setClauses, fmt.Sprintf("title = $%d", argIdx))
		args = append(args, req.Title)
		argIdx++
	}
	if req.Content != "" {
		setClauses = append(setClauses, fmt.Sprintf("content = $%d", argIdx))
		args = append(args, req.Content)
		argIdx++
	}
	if req.Type != "" {
		setClauses = append(setClauses, fmt.Sprintf("type = $%d", argIdx))
		args = append(args, req.Type)
		argIdx++
	}
	if req.Pinned != nil {
		setClauses = append(setClauses, fmt.Sprintf("pinned = $%d", argIdx))
		args = append(args, *req.Pinned)
		argIdx++
	}
	if req.ShowPopup != nil {
		setClauses = append(setClauses, fmt.Sprintf("show_popup = $%d", argIdx))
		args = append(args, *req.ShowPopup)
		argIdx++
	}
	if req.Priority != nil {
		setClauses = append(setClauses, fmt.Sprintf("priority = $%d", argIdx))
		args = append(args, *req.Priority)
		argIdx++
	}
	if req.DisplayMode != "" {
		setClauses = append(setClauses, fmt.Sprintf("display_mode = $%d", argIdx))
		args = append(args, req.DisplayMode)
		argIdx++
	}
	if req.CtaText != nil {
		setClauses = append(setClauses, fmt.Sprintf("cta_text = $%d", argIdx))
		args = append(args, *req.CtaText)
		argIdx++
	}
	if req.CtaLink != nil {
		setClauses = append(setClauses, fmt.Sprintf("cta_link = $%d", argIdx))
		args = append(args, *req.CtaLink)
		argIdx++
	}
	if req.BannerColor != "" {
		setClauses = append(setClauses, fmt.Sprintf("banner_color = $%d", argIdx))
		args = append(args, req.BannerColor)
		argIdx++
	}
	if req.DismissMode != "" {
		setClauses = append(setClauses, fmt.Sprintf("dismiss_mode = $%d", argIdx))
		args = append(args, req.DismissMode)
		argIdx++
	}
	if req.TargetAudience != "" {
		setClauses = append(setClauses, fmt.Sprintf("target_audience = $%d", argIdx))
		args = append(args, req.TargetAudience)
		argIdx++
	}
	if req.IconEmoji != nil {
		setClauses = append(setClauses, fmt.Sprintf("icon_emoji = $%d", argIdx))
		args = append(args, *req.IconEmoji)
		argIdx++
	}
	if req.StartAt != nil {
		t, _ := time.Parse("2006-01-02", *req.StartAt)
		setClauses = append(setClauses, fmt.Sprintf("start_at = $%d", argIdx))
		args = append(args, t)
		argIdx++
	}
	if req.EndAt != nil {
		t, _ := time.Parse("2006-01-02", *req.EndAt)
		setClauses = append(setClauses, fmt.Sprintf("end_at = $%d", argIdx))
		args = append(args, t)
		argIdx++
	}

	if len(setClauses) == 0 {
		c.JSON(http.StatusOK, gin.H{"success": true})
		return
	}

	query := fmt.Sprintf("UPDATE announcements SET %s, updated_at = NOW() WHERE id = $%d::uuid",
		strings.Join(setClauses, ", "), argIdx)
	args = append(args, id)

	_, err := r.db.Pool.Exec(c.Request.Context(), query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": T("error.update_failed", c)}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (r *AnnouncementRoutes) delete(c *gin.Context) {
	id := c.Param("id")
	_, err := r.db.Pool.Exec(c.Request.Context(),
		"DELETE FROM announcements WHERE id = $1::uuid", id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": T("error.delete_failed", c)}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (r *AnnouncementRoutes) publish(c *gin.Context) {
	id := c.Param("id")
	_, err := r.db.Pool.Exec(c.Request.Context(),
		`UPDATE announcements SET status='published', updated_at=NOW() WHERE id=$1::uuid`, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": T("error.publish_failed", c)}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (r *AnnouncementRoutes) archive(c *gin.Context) {
	id := c.Param("id")
	_, err := r.db.Pool.Exec(c.Request.Context(),
		`UPDATE announcements SET status='archived', updated_at=NOW() WHERE id=$1::uuid`, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": T("error.archive_failed", c)}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

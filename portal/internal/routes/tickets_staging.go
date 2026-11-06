package routes

import (
	"context"
	"fmt"
	"mime"
	"mime/multipart"
	"net/http"
	"path/filepath"

	"github.com/ai-proxy/ai-portal-go/internal/db"
	"github.com/ai-proxy/ai-portal-go/internal/storage"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

// stageAttachments implements the ticket UI's immediate-upload flow: files
// are stored and recorded without a message (message_id NULL, staged_by set),
// shown to the user as a removable list, and linked to their comment only
// when the ticket/reply is submitted (see linkStagedAttachments).
func (r *TicketRoutes) stageAttachments(c *gin.Context) {
	userID := c.GetString("user_id")
	ctx := c.Request.Context()
	if r.storage == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"message": "Storage not configured"}})
		return
	}
	if err := c.Request.ParseMultipartForm(maxTicketAttachmentSize*maxTicketAttachments + 1024*1024); err != nil {
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
	// Cap the user's TOTAL staged set at maxTicketAttachments, so the picker
	// can never accumulate more files than one message may carry.
	var stagedCount int
	_ = r.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM support_ticket_attachments WHERE staged_by=$1::uuid AND message_id IS NULL`,
		userID).Scan(&stagedCount)
	if len(files) > maxTicketAttachments || stagedCount+len(files) > maxTicketAttachments {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": fmt.Sprintf("At most %d files per upload", maxTicketAttachments)}})
		return
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
		key := "tickets/staged/" + uuid.NewString() + filepath.Ext(fh.Filename)
		if err := r.storage.Upload(ctx, key, opened, storage.UploadOptions{ContentType: contentType}); err != nil {
			_ = opened.Close()
			r.logger.Error("staged attachment upload failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to store file"}})
			return
		}
		_ = opened.Close()
		var aid string
		if err := r.db.QueryRow(ctx,
			`INSERT INTO support_ticket_attachments (message_id, storage_key, filename, size_bytes, mime_type, staged_at, staged_by)
			 VALUES (NULL,$1,$2,$3,$4,NOW(),$5::uuid) RETURNING id::text`,
			key, fh.Filename, fh.Size, contentType, userID).Scan(&aid); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to record attachment"}})
			return
		}
		saved = append(saved, gin.H{"id": aid, "filename": fh.Filename, "size_bytes": fh.Size, "mime_type": contentType})
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "attachments": saved})
}

// unstageAttachment deletes a staged attachment (storage object + row). Only
// the stager may remove it, and only while it is still unlinked.
func (r *TicketRoutes) unstageAttachment(c *gin.Context) {
	userID := c.GetString("user_id")
	aid := c.Param("aid")
	ctx := c.Request.Context()
	if r.storage == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"message": "Storage not configured"}})
		return
	}
	var key string
	err := r.db.QueryRow(ctx,
		`DELETE FROM support_ticket_attachments
		  WHERE id=$1::uuid AND staged_by=$2::uuid AND message_id IS NULL
		  RETURNING storage_key`, aid, userID).Scan(&key)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Attachment not found"}})
		return
	}
	if err := r.storage.Delete(ctx, key); err != nil {
		r.logger.Warn("failed to delete staged attachment object", zap.String("key", key), zap.Error(err))
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// linkStagedAttachments attaches previously staged files to a message inside
// the caller's transaction. Ownership (staged_by) and the still-unlinked
// guard make id replay harmless.
func linkStagedAttachments(ctx context.Context, tx pgx.Tx, userID, messageID string, ids []string) {
	if len(ids) == 0 {
		return
	}
	if _, err := tx.Exec(ctx,
		`UPDATE support_ticket_attachments
		    SET message_id = $1::uuid, staged_at = NULL, staged_by = NULL
		  WHERE id = ANY($2::uuid[]) AND message_id IS NULL AND staged_by = $3::uuid`,
		messageID, ids, userID); err != nil {
		// Linking is best-effort: the comment must post even if a staged id is
		// stale; the row stays staged and the sweeper reclaims it.
		_ = err
	}
}

// CleanupStaged removes staged attachments older than the given hours via the
// routes' storage provider; wired into the daily background job.
func (r *TicketRoutes) CleanupStaged(ctx context.Context, hours int) (int, error) {
	return CleanupStagedAttachments(ctx, r.db, r.storage, r.logger, hours)
}

// CleanupStagedAttachments removes staged attachments older than the given
// number of hours (abandoned drafts). Storage objects are deleted best-effort.
func CleanupStagedAttachments(ctx context.Context, database *db.DB, store storage.Provider, logger *zap.Logger, hours int) (int, error) {
	if hours <= 0 {
		hours = 24
	}
	rows, err := database.Query(ctx,
		`DELETE FROM support_ticket_attachments
		  WHERE message_id IS NULL AND staged_at IS NOT NULL
		    AND staged_at < NOW() - ($1 * INTERVAL '1 hour')
		  RETURNING storage_key`, hours)
	if err != nil {
		return 0, fmt.Errorf("cleanup staged attachments: %w", err)
	}
	defer rows.Close()

	removed := 0
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err == nil {
			keys = append(keys, k)
			removed++
		}
	}
	if store != nil {
		for _, k := range keys {
			if err := store.Delete(ctx, k); err != nil && logger != nil {
				logger.Warn("failed to delete stale staged attachment object", zap.String("key", k), zap.Error(err))
			}
		}
	}
	if removed > 0 && logger != nil {
		logger.Info("cleaned up stale staged attachments", zap.Int("removed", removed))
	}
	return removed, nil
}

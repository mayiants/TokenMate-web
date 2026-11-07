package routes

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/mayiants/ai-portal-go/internal/config"
	"github.com/mayiants/ai-portal-go/internal/db"
	"github.com/mayiants/ai-portal-go/internal/storage"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// uploadEnterpriseApplicationDocument handles user upload of KYC/authorization files.
func (r *EnterpriseApplicationRoutes) uploadEnterpriseApplicationDocument(c *gin.Context) {
	userID := c.GetString("user_id")
	appID := c.Param("id")
	ctx := c.Request.Context()
	r.logger.Info("Enterprise document upload requested", zap.String("user_id", userID), zap.String("application_id", appID))

	// Verify the application belongs to the user and is in an editable state.
	var status string
	var ownerID string
	err := r.db.QueryRow(ctx,
		`SELECT applicant_user_id::text, status FROM enterprise_applications WHERE id = $1::uuid`,
		appID).Scan(&ownerID, &status)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Application not found"}})
		return
	}
	if ownerID != userID {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "Access denied"}})
		return
	}
	if status != "pending_review" && status != "needs_more_info" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Application cannot be updated in current status"}})
		return
	}

	// Parse multipart form with a generous memory limit before spooling to disk.
	maxSize := r.cfg.FileStorage.MaxSizeMB * 1024 * 1024
	if maxSize == 0 {
		maxSize = 10 * 1024 * 1024
	}
	if err := c.Request.ParseMultipartForm(maxSize); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Failed to parse upload"}})
		return
	}

	fileHeader, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "File is required"}})
		return
	}

	if err := validateAndCheckUploadLimit(ctx, r.cfg, r.db, appID, fileHeader); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": err.Error()}})
		return
	}

	contentType := fileHeader.Header.Get("Content-Type")
	if contentType == "" {
		contentType = mime.TypeByExtension(filepath.Ext(fileHeader.Filename))
	}

	opened, err := fileHeader.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to read file"}})
		return
	}
	defer func() { _ = opened.Close() }()

	// Compute hash and read content into memory for validation + upload.
	hash, data, err := computeFileHash(opened)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to process file"}})
		return
	}
	if !isAllowedContentType(r.cfg, contentType, fileHeader.Filename) {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "File type not allowed"}})
		return
	}

	storageProvider := r.storage
	if storageProvider == nil {
		r.logger.Error("Storage provider not initialized")
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Storage not configured"}})
		return
	}

	// Parse document_type from form field.
	docType := c.PostForm("document_type")
	if docType == "" {
		docType = "other"
	}
	validDocTypes := map[string]bool{
		"business_registration": true,
		"tax_certificate":       true,
		"proof_of_address":      true,
		"representative_auth":   true,
		"representative_id":     true,
		"bank_proof":            true,
		"other":                 true,
	}
	if !validDocTypes[docType] {
		docType = "other"
	}

	docID := uuid.New().String()
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(fileHeader.Filename), "."))
	if ext == "" {
		ext = "bin"
	}
	// The configured FILE_STORAGE_PATH_PREFIX already acts as the top-level directory.
	storageKey := fmt.Sprintf("%s/%s.%s", appID, docID, ext)

	if err := storageProvider.Upload(ctx, storageKey, bytes.NewReader(data), storage.UploadOptions{
		ContentType: contentType,
		Size:        fileHeader.Size,
	}); err != nil {
		r.logger.Error("Failed to upload file", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to upload file"}})
		return
	}

	var dbDocID string
	err = r.db.QueryRow(ctx, `
		INSERT INTO enterprise_application_documents
		(application_id, original_name, storage_key, storage_provider, content_type, file_size, file_hash, uploaded_by, document_type)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8::uuid, $9)
		RETURNING id::text`,
		appID, fileHeader.Filename, storageKey, r.cfg.FileStorage.Provider, contentType, fileHeader.Size, hash, userID, docType,
	).Scan(&dbDocID)
	if err != nil {
		// Best-effort cleanup of orphaned file.
		_ = storageProvider.Delete(ctx, storageKey)
		r.logger.Error("Failed to save document metadata", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to save document"}})
		return
	}

	r.audit.Log(ctx, userID, c.GetString("email"), "user", "enterprise_document_uploaded", "info",
		fmt.Sprintf("Uploaded document %s for application %s", dbDocID, appID))

	c.JSON(http.StatusOK, gin.H{"success": true, "document_id": dbDocID})
}

// listMyEnterpriseApplicationDocuments lists documents for the current user's application.
func (r *EnterpriseApplicationRoutes) listMyEnterpriseApplicationDocuments(c *gin.Context) {
	userID := c.GetString("user_id")
	appID := c.Param("id")
	ctx := c.Request.Context()

	var ownerID string
	err := r.db.QueryRow(ctx,
		`SELECT applicant_user_id::text FROM enterprise_applications WHERE id = $1::uuid`, appID,
	).Scan(&ownerID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Application not found"}})
		return
	}
	if ownerID != userID {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "Access denied"}})
		return
	}

	docs, err := queryApplicationDocuments(ctx, r.db, appID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to list documents"}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"documents": docs})
}

// deleteMyEnterpriseApplicationDocument soft-deletes a document uploaded by the user.
func (r *EnterpriseApplicationRoutes) deleteMyEnterpriseApplicationDocument(c *gin.Context) {
	userID := c.GetString("user_id")
	appID := c.Param("id")
	docID := c.Param("docId")
	ctx := c.Request.Context()

	var ownerID, status string
	err := r.db.QueryRow(ctx,
		`SELECT applicant_user_id::text, status FROM enterprise_applications WHERE id = $1::uuid`, appID,
	).Scan(&ownerID, &status)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Application not found"}})
		return
	}
	if ownerID != userID {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "Access denied"}})
		return
	}
	if status != "pending_review" && status != "needs_more_info" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Application cannot be updated in current status"}})
		return
	}

	var storageKey string
	err = r.db.QueryRow(ctx,
		`SELECT storage_key FROM enterprise_application_documents
		 WHERE id = $1::uuid AND application_id = $2::uuid AND uploaded_by = $3::uuid AND deleted_at IS NULL`,
		docID, appID, userID).Scan(&storageKey)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Document not found"}})
		return
	}

	storageProvider := r.storage
	if storageProvider == nil {
		r.logger.Error("Storage provider not initialized")
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Storage not configured"}})
		return
	}

	if err := storageProvider.Delete(ctx, storageKey); err != nil {
		r.logger.Error("Failed to delete file", zap.Error(err))
		// Continue to mark deleted in DB even if physical delete fails; background job can clean up.
	}

	if err := r.db.Exec(ctx,
		`UPDATE enterprise_application_documents SET deleted_at = NOW(), delete_reason = 'user_deleted'
		 WHERE id = $1::uuid`, docID); err != nil {
		r.logger.Error("Failed to mark document deleted", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to delete document"}})
		return
	}

	r.audit.Log(ctx, userID, c.GetString("email"), "user", "enterprise_document_deleted", "info",
		fmt.Sprintf("Deleted document %s from application %s", docID, appID))

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// listEnterpriseApplicationDocuments lists documents for admin review.
func (r *AdminRoutes) listEnterpriseApplicationDocuments(c *gin.Context) {
	appID := c.Param("id")
	ctx := c.Request.Context()

	docs, err := queryApplicationDocuments(ctx, r.db, appID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to list documents"}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"documents": docs})
}

// downloadEnterpriseApplicationDocument serves a document to admin for review.
func (r *AdminRoutes) downloadEnterpriseApplicationDocument(c *gin.Context) {
	appID := c.Param("id")
	docID := c.Param("docId")
	adminUserID := c.GetString("user_id")
	ctx := c.Request.Context()

	var doc struct {
		OriginalName string
		StorageKey   string
		ContentType  string
		FileSize     int64
	}
	err := r.db.QueryRow(ctx,
		`SELECT original_name, storage_key, content_type, file_size
		 FROM enterprise_application_documents
		 WHERE id = $1::uuid AND application_id = $2::uuid AND deleted_at IS NULL`,
		docID, appID).Scan(&doc.OriginalName, &doc.StorageKey, &doc.ContentType, &doc.FileSize)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Document not found"}})
		return
	}

	storageProvider := r.storage
	if storageProvider == nil {
		r.logger.Error("Storage provider not initialized")
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Storage not configured"}})
		return
	}

	reader, err := storageProvider.Download(ctx, doc.StorageKey)
	if err != nil {
		r.logger.Error("Failed to download file", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to download file"}})
		return
	}
	defer func() { _ = reader.Close() }()

	c.Header("Content-Type", doc.ContentType)
	c.Header("Content-Disposition", fmt.Sprintf("inline; filename=\"%s\"", doc.OriginalName))
	c.Header("Content-Length", fmt.Sprintf("%d", doc.FileSize))
	c.DataFromReader(http.StatusOK, doc.FileSize, doc.ContentType, reader, nil)

	r.audit.Log(ctx, adminUserID, c.GetString("email"), "admin", "enterprise_document_downloaded", "info",
		fmt.Sprintf("Admin downloaded document %s of application %s", docID, appID))
}

// queryApplicationDocuments returns non-deleted documents for an application.
func queryApplicationDocuments(ctx context.Context, database *db.DB, appID string) ([]gin.H, error) {
	rows, err := database.Query(ctx,
		`SELECT id::text, original_name, content_type, file_size, file_hash, uploaded_at::text, COALESCE(document_type, 'other')
		 FROM enterprise_application_documents
		 WHERE application_id = $1::uuid AND deleted_at IS NULL
		 ORDER BY uploaded_at DESC`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var docs []gin.H
	for rows.Next() {
		var id, originalName, contentType, fileHash, uploadedAt, documentType string
		var fileSize int64
		if err := rows.Scan(&id, &originalName, &contentType, &fileSize, &fileHash, &uploadedAt, &documentType); err != nil {
			continue
		}
		docs = append(docs, gin.H{
			"id":            id,
			"original_name": originalName,
			"content_type":  contentType,
			"file_size":     fileSize,
			"file_hash":     fileHash,
			"uploaded_at":   uploadedAt,
			"document_type": documentType,
		})
	}
	return docs, nil
}

// validateAndCheckUploadLimit checks file size and total application size limits.
func validateAndCheckUploadLimit(ctx context.Context, cfg *config.Config, database *db.DB, appID string, fileHeader *multipart.FileHeader) error {
	maxSize := cfg.FileStorage.MaxSizeMB * 1024 * 1024
	if maxSize == 0 {
		maxSize = 10 * 1024 * 1024
	}
	if fileHeader.Size > maxSize {
		return fmt.Errorf("file exceeds maximum size of %d MB", cfg.FileStorage.MaxSizeMB)
	}

	maxTotal := cfg.FileStorage.MaxTotalMBPerApp * 1024 * 1024
	if maxTotal == 0 {
		maxTotal = 50 * 1024 * 1024
	}
	var totalUsed int64
	_ = database.QueryRow(ctx,
		`SELECT COALESCE(SUM(file_size), 0) FROM enterprise_application_documents
		 WHERE application_id = $1::uuid AND deleted_at IS NULL`, appID).Scan(&totalUsed)
	if totalUsed+fileHeader.Size > maxTotal {
		return fmt.Errorf("total upload size would exceed %d MB per application", cfg.FileStorage.MaxTotalMBPerApp)
	}
	return nil
}

// isAllowedContentType validates the file type against the configured allow list.
func isAllowedContentType(cfg *config.Config, contentType, filename string) bool {
	allowed := cfg.FileStorage.AllowedTypes
	if len(allowed) == 0 {
		allowed = []string{"application/pdf", "image/jpeg", "image/png"}
	}

	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(filename), "."))
	allowedMap := make(map[string]bool)
	for _, t := range allowed {
		allowedMap[strings.ToLower(strings.TrimSpace(t))] = true
	}

	if allowedMap[strings.ToLower(contentType)] {
		return true
	}
	// Fallback by extension.
	switch ext {
	case "pdf":
		return allowedMap["application/pdf"]
	case "jpg", "jpeg":
		return allowedMap["image/jpeg"]
	case "png":
		return allowedMap["image/png"]
	}
	return false
}

// computeFileHash reads all data and returns SHA-256 hex + bytes.
func computeFileHash(src io.Reader) (string, []byte, error) {
	data, err := io.ReadAll(src)
	if err != nil {
		return "", nil, err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), data, nil
}

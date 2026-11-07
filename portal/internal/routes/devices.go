package routes

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"

	"github.com/mayiants/ai-portal-go/internal/db"
	"github.com/mayiants/ai-portal-go/internal/middleware"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

// DeviceRoutes handles user device management
type DeviceRoutes struct {
	db     *db.DB
	auth   *middleware.Auth
	logger *zap.Logger
}

func NewDeviceRoutes(db *db.DB, auth *middleware.Auth, logger *zap.Logger) *DeviceRoutes {
	return &DeviceRoutes{db: db, auth: auth, logger: logger}
}

func (r *DeviceRoutes) Register(rg *gin.RouterGroup) {
	authGroup := rg.Use(r.auth.RequireAuth())
	authGroup.GET("", r.listDevices)
	authGroup.POST("", r.recordDevice)
	authGroup.DELETE("/:id", r.deleteDevice)
}

// listDevices returns all devices for the current user
func (r *DeviceRoutes) listDevices(c *gin.Context) {
	userID := c.GetString("user_id")

	rows, err := r.db.Query(c.Request.Context(),
		`SELECT id::text, device_name, device_type, browser, os,
		        ip_address::text, location, is_current, last_active_at, created_at
		 FROM user_devices
		 WHERE user_id = $1::uuid
		 ORDER BY last_active_at DESC`,
		userID)
	if err != nil {
		r.logger.Warn("Failed to list devices", zap.Error(err), zap.String("user_id", userID))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to list devices"}})
		return
	}
	defer rows.Close()

	devices := []gin.H{}
	for rows.Next() {
		var id, deviceName, deviceType, browser, os string
		var ipAddress, location *string
		var isCurrent bool
		var lastActiveAt, createdAt interface{}
		_ = rows.Scan(&id, &deviceName, &deviceType, &browser, &os,
			&ipAddress, &location, &isCurrent, &lastActiveAt, &createdAt)
		devices = append(devices, gin.H{
			"id":             id,
			"device_name":    deviceName,
			"device_type":    deviceType,
			"browser":        browser,
			"os":             os,
			"ip_address":     ipAddress,
			"location":       location,
			"is_current":     isCurrent,
			"last_active_at": lastActiveAt,
			"created_at":     createdAt,
		})
	}

	c.JSON(http.StatusOK, gin.H{"devices": devices})
}

// recordDevice records or updates a device for the current user
func (r *DeviceRoutes) recordDevice(c *gin.Context) {
	userID := c.GetString("user_id")

	var req struct {
		DeviceName string `json:"device_name"`
		DeviceType string `json:"device_type"`
		Browser    string `json:"browser"`
		OS         string `json:"os"`
		Location   string `json:"location"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}

	// Use request info if not provided
	if req.Browser == "" {
		req.Browser = c.Request.UserAgent()
	}
	ip := c.ClientIP()

	// UPSERT on (user_id, fingerprint) within a transaction: if the fingerprint
	// already exists, update the record; otherwise insert a new one. Also mark
	// all other devices as not current in the same transaction.
	fingerprint := fingerprintHash(req.DeviceName, req.DeviceType, req.Browser, req.OS)
	var deviceID string
	err := r.db.WithTx(c.Request.Context(), func(tx pgx.Tx) error {
		if _, err := tx.Exec(c.Request.Context(),
			`UPDATE user_devices SET is_current = false WHERE user_id = $1::uuid`, userID); err != nil {
			return err
		}
		return tx.QueryRow(c.Request.Context(),
			`INSERT INTO user_devices (user_id, device_name, device_type, browser, os, ip_address, location, fingerprint, is_current, last_active_at)
			 VALUES ($1::uuid, $2, $3, $4, $5, $6::inet, $7, $8, true, NOW())
			 ON CONFLICT (user_id, fingerprint) DO UPDATE SET
			   device_name = EXCLUDED.device_name,
			   device_type = EXCLUDED.device_type,
			   browser = EXCLUDED.browser,
			   os = EXCLUDED.os,
			   ip_address = EXCLUDED.ip_address,
			   location = EXCLUDED.location,
			   is_current = true,
			   last_active_at = NOW()
			 RETURNING id::text`,
			userID, req.DeviceName, req.DeviceType, req.Browser, req.OS, ip, req.Location, fingerprint,
		).Scan(&deviceID)
	})
	if err != nil {
		r.logger.Warn("Failed to record device", zap.Error(err), zap.String("user_id", userID))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to record device"}})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "device_id": deviceID})
}

// deleteDevice removes a device for the current user
func (r *DeviceRoutes) deleteDevice(c *gin.Context) {
	userID := c.GetString("user_id")
	deviceID := c.Param("id")

	_, err := r.db.Pool.Exec(c.Request.Context(),
		`DELETE FROM user_devices WHERE id = $1::uuid AND user_id = $2::uuid`,
		deviceID, userID)
	if err != nil {
		r.logger.Warn("Failed to delete device", zap.Error(err), zap.String("device_id", deviceID))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to delete device"}})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// fingerprintHash generates a stable device fingerprint from device attributes.
func fingerprintHash(deviceName, deviceType, browser, os string) string {
	h := sha256.Sum256([]byte(deviceType + "|" + browser + "|" + os + "|" + deviceName))
	return hex.EncodeToString(h[:16])
}

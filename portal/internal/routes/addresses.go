package routes

import (
	"net/http"

	"github.com/ai-proxy/ai-portal-go/internal/db"
	"github.com/ai-proxy/ai-portal-go/internal/middleware"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

type AddressRoutes struct {
	db     *db.DB
	auth   *middleware.Auth
	logger *zap.Logger
}

func NewAddressRoutes(db *db.DB, auth *middleware.Auth, logger *zap.Logger) *AddressRoutes {
	return &AddressRoutes{db: db, auth: auth, logger: logger}
}

func (r *AddressRoutes) Register(rg *gin.RouterGroup) {
	rg.GET("", r.auth.RequireAuth(), r.listAddresses)
	rg.POST("", r.auth.RequireAuth(), r.createAddress)
	rg.PUT("/:id", r.auth.RequireAuth(), r.updateAddress)
	rg.DELETE("/:id", r.auth.RequireAuth(), r.deleteAddress)
}

func (r *AddressRoutes) listAddresses(c *gin.Context) {
	userID := c.GetString("user_id")

	rows, err := r.db.Query(c.Request.Context(),
		`SELECT id::text, COALESCE(recipient_name, ''), COALESCE(phone, ''),
		        COALESCE(country, 'CN'), COALESCE(province, ''), COALESCE(city, ''),
		        COALESCE(district, ''), COALESCE(detail, ''), COALESCE(postal_code, ''),
		        COALESCE(is_default, false), created_at
		 FROM delivery_addresses
		 WHERE user_id = $1::uuid AND deleted_at IS NULL
		 ORDER BY is_default DESC, created_at DESC`,
		userID)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"addresses": []gin.H{}})
		return
	}
	defer rows.Close()

	addresses := []gin.H{}
	for rows.Next() {
		var id, name, phone, country, province, city, district, detail, postal string
		var isDefault bool
		var createdAt interface{}
		if err := rows.Scan(&id, &name, &phone, &country, &province, &city,
			&district, &detail, &postal, &isDefault, &createdAt); err != nil {
			continue
		}
		// For international addresses, province stores state
		state := province
		if country == "CN" || country == "HK" || country == "TW" {
			state = ""
		}
		addresses = append(addresses, gin.H{
			"id": id, "recipient_name": name, "phone": phone,
			"country": country, "province": province, "city": city,
			"district": district, "state": state,
			"detail": detail, "postal_code": postal,
			"is_default": isDefault, "created_at": createdAt,
		})
	}

	c.JSON(http.StatusOK, gin.H{"addresses": addresses})
}

func (r *AddressRoutes) createAddress(c *gin.Context) {
	userID := c.GetString("user_id")

	var req struct {
		RecipientName string `json:"recipient_name" binding:"required"`
		Phone         string `json:"phone"`
		Country       string `json:"country"`
		Province      string `json:"province"`
		City          string `json:"city"`
		District      string `json:"district"`
		State         string `json:"state"`
		Detail        string `json:"detail" binding:"required"`
		PostalCode    string `json:"postal_code"`
		IsDefault     bool   `json:"is_default"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": T("error.address_required", c)}})
		return
	}

	if req.Country == "" {
		req.Country = "CN"
	}

	// For international addresses, store state in province field
	province := req.Province
	if req.Country != "CN" && req.Country != "HK" && req.Country != "TW" {
		province = req.State
	}

	// If setting as default, unset other defaults first
	if req.IsDefault {
		if err := r.db.Exec(c.Request.Context(), `UPDATE delivery_addresses SET is_default = false WHERE user_id = $1::uuid`, userID); err != nil {
			r.logger.Error("database operation failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
			return
		}
	}

	var id string
	err := r.db.QueryRow(c.Request.Context(),
		`INSERT INTO delivery_addresses (user_id, recipient_name, phone, country, province, city, district, detail, postal_code, is_default)
		 VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		 RETURNING id::text`,
		userID, req.RecipientName, req.Phone, req.Country,
		province, req.City, req.District, req.Detail, req.PostalCode, req.IsDefault,
	).Scan(&id)
	if err != nil {
		r.logger.Error("Failed to create address", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": T("error.create_address_failed", c)}})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "id": id})
}

func (r *AddressRoutes) updateAddress(c *gin.Context) {
	userID := c.GetString("user_id")
	addrID := c.Param("id")

	var req struct {
		RecipientName *string `json:"recipient_name"`
		Phone         *string `json:"phone"`
		Country       *string `json:"country"`
		Province      *string `json:"province"`
		City          *string `json:"city"`
		District      *string `json:"district"`
		State         *string `json:"state"`
		Detail        *string `json:"detail"`
		PostalCode    *string `json:"postal_code"`
		IsDefault     *bool   `json:"is_default"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": T("error.invalid_request", c)}})
		return
	}

	// Apply all field updates in a single transaction so a mid-failure does
	// not leave the address in an inconsistent state (previously up to 10
	// separate UPDATE statements ran independently).
	err := r.db.WithTx(c.Request.Context(), func(tx pgx.Tx) error {
		// If setting as default, unset other defaults first
		if req.IsDefault != nil && *req.IsDefault {
			if _, err := tx.Exec(c.Request.Context(), `UPDATE delivery_addresses SET is_default = false WHERE user_id = $1::uuid`, userID); err != nil {
				return err
			}
		}
		if req.RecipientName != nil {
			if _, err := tx.Exec(c.Request.Context(), `UPDATE delivery_addresses SET recipient_name = $1, updated_at = NOW() WHERE id = $2::uuid AND user_id = $3::uuid`, *req.RecipientName, addrID, userID); err != nil {
				return err
			}
		}
		if req.Phone != nil {
			if _, err := tx.Exec(c.Request.Context(), `UPDATE delivery_addresses SET phone = $1, updated_at = NOW() WHERE id = $2::uuid AND user_id = $3::uuid`, *req.Phone, addrID, userID); err != nil {
				return err
			}
		}
		if req.Country != nil {
			if _, err := tx.Exec(c.Request.Context(), `UPDATE delivery_addresses SET country = $1, updated_at = NOW() WHERE id = $2::uuid AND user_id = $3::uuid`, *req.Country, addrID, userID); err != nil {
				return err
			}
		}
		if req.Province != nil {
			if _, err := tx.Exec(c.Request.Context(), `UPDATE delivery_addresses SET province = $1, updated_at = NOW() WHERE id = $2::uuid AND user_id = $3::uuid`, *req.Province, addrID, userID); err != nil {
				return err
			}
		}
		if req.State != nil {
			// For international, store state in province
			if _, err := tx.Exec(c.Request.Context(), `UPDATE delivery_addresses SET province = $1, updated_at = NOW() WHERE id = $2::uuid AND user_id = $3::uuid`, *req.State, addrID, userID); err != nil {
				return err
			}
		}
		if req.City != nil {
			if _, err := tx.Exec(c.Request.Context(), `UPDATE delivery_addresses SET city = $1, updated_at = NOW() WHERE id = $2::uuid AND user_id = $3::uuid`, *req.City, addrID, userID); err != nil {
				return err
			}
		}
		if req.District != nil {
			if _, err := tx.Exec(c.Request.Context(), `UPDATE delivery_addresses SET district = $1, updated_at = NOW() WHERE id = $2::uuid AND user_id = $3::uuid`, *req.District, addrID, userID); err != nil {
				return err
			}
		}
		if req.Detail != nil {
			if _, err := tx.Exec(c.Request.Context(), `UPDATE delivery_addresses SET detail = $1, updated_at = NOW() WHERE id = $2::uuid AND user_id = $3::uuid`, *req.Detail, addrID, userID); err != nil {
				return err
			}
		}
		if req.PostalCode != nil {
			if _, err := tx.Exec(c.Request.Context(), `UPDATE delivery_addresses SET postal_code = $1, updated_at = NOW() WHERE id = $2::uuid AND user_id = $3::uuid`, *req.PostalCode, addrID, userID); err != nil {
				return err
			}
		}
		if req.IsDefault != nil {
			if _, err := tx.Exec(c.Request.Context(), `UPDATE delivery_addresses SET is_default = $1, updated_at = NOW() WHERE id = $2::uuid AND user_id = $3::uuid`, *req.IsDefault, addrID, userID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		r.logger.Error("database operation failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (r *AddressRoutes) deleteAddress(c *gin.Context) {
	userID := c.GetString("user_id")
	addrID := c.Param("id")

	if err := r.db.Exec(c.Request.Context(), `UPDATE delivery_addresses SET deleted_at = NOW() WHERE id = $1::uuid AND user_id = $2::uuid AND deleted_at IS NULL`, addrID, userID); err != nil {
		r.logger.Error("database operation failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Database operation failed"}})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

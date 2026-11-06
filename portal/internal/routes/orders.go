package routes

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ai-proxy/ai-portal-go/internal/config"
	"github.com/ai-proxy/ai-portal-go/internal/db"
	"github.com/ai-proxy/ai-portal-go/internal/middleware"
	"github.com/ai-proxy/ai-portal-go/internal/query"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// OrderRoutes handles order management
type OrderRoutes struct {
	db   *db.DB
	auth *middleware.Auth
}

// NewOrderRoutes creates a new order routes handler
func NewOrderRoutes(db *db.DB, auth *middleware.Auth) *OrderRoutes {
	return &OrderRoutes{db: db, auth: auth}
}

// Register registers all order routes
func (r *OrderRoutes) Register(rg *gin.RouterGroup) {
	rg.GET("", r.auth.RequireAuth(), r.listOrders)
	rg.GET("/export", r.auth.RequireAuth(), r.exportOrders)
	rg.GET("/:id", r.auth.RequireAuth(), r.getOrder)
	rg.POST("/create", r.auth.RequireAuth(), r.createOrder)
	rg.PATCH("/:id", r.auth.RequireAuth(), r.updateOrder)
	rg.POST("/:id/cancel", r.auth.RequireAuth(), r.cancelOrder)
}

func (r *OrderRoutes) listOrders(c *gin.Context) {
	userID := c.GetString("user_id")

	page := 1
	pageSize := 20
	if p := c.Query("page"); p != "" {
		if parsed, err := strconv.Atoi(p); err == nil && parsed > 0 {
			page = parsed
		}
	}
	if ps := c.Query("page_size"); ps != "" {
		if parsed, err := strconv.Atoi(ps); err == nil && parsed > 0 {
			pageSize = parsed
		}
	}

	filter := query.OrderFilter{}
	if status := c.Query("status"); status != "" {
		filter.Status = status
	}
	if startDate := c.Query("start_date"); startDate != "" {
		filter.StartDate = startDate
	}
	if endDate := c.Query("end_date"); endDate != "" {
		filter.EndDate = endDate
	}

	data, err := query.GetOrdersData(c.Request.Context(), r.db, userID, page, pageSize, filter)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to list orders"}})
		return
	}

	orders := []gin.H{}
	for _, o := range data.Items {
		orders = append(orders, gin.H{
			"id":             o.ID,
			"order_id":       o.OrderID,
			"amount":         o.Amount,
			"currency":       o.Currency,
			"exchange_rate":  o.ExchangeRate,
			"status":         o.Status,
			"payment_method": o.Method,
			"product_type":   o.ProductType,
			"created_at":     o.CreatedAt,
			"updated_at":     o.UpdatedAt,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"orders":      orders,
		"total":       data.Total,
		"page":        data.Page,
		"page_size":   data.PageSize,
		"total_pages": data.TotalPages,
	})
}

func (r *OrderRoutes) exportOrders(c *gin.Context) {
	userID := c.GetString("user_id")

	// Build filter conditions
	whereClause := "WHERE user_id = $1::uuid"
	args := []interface{}{userID}
	argIndex := 2

	if status := c.Query("status"); status != "" {
		whereClause += fmt.Sprintf(" AND status = $%d", argIndex)
		args = append(args, status)
		argIndex++
	}
	if startDate := c.Query("start_date"); startDate != "" {
		whereClause += fmt.Sprintf(" AND created_at >= $%d", argIndex)
		args = append(args, startDate)
		argIndex++
	}
	if endDate := c.Query("end_date"); endDate != "" {
		whereClause += fmt.Sprintf(" AND created_at < $%d::date + INTERVAL '1 day'", argIndex)
		args = append(args, endDate)
	}

	rows, err := r.db.Query(c.Request.Context(),
		fmt.Sprintf(`SELECT order_id, amount, status, COALESCE(payment_method, ''), created_at
		 FROM orders %s ORDER BY created_at DESC`, whereClause),
		args...,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to export orders"}})
		return
	}
	defer rows.Close()

	// Build CSV
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=orders-"+time.Now().UTC().Format("200601021504")+".csv")

	// Write BOM for Excel
	_, _ = c.Writer.Write([]byte{0xEF, 0xBB, 0xBF})

	// Write header
	_, _ = c.Writer.WriteString(T("csv.header.order_id", c) + "," + T("csv.header.amount", c) + "," + T("csv.header.status", c) + "," + T("csv.header.payment_method", c) + "," + T("csv.header.order_time", c) + "\n")

	for rows.Next() {
		var orderID, status, paymentMethod string
		var amount int64
		var createdAt time.Time

		if err := rows.Scan(&orderID, &amount, &status, &paymentMethod, &createdAt); err != nil {
			continue
		}

		// Translate status
		statusLabel := status
		switch status {
		case "pending":
			statusLabel = T("csv.status.pending", c)
		case "completed":
			statusLabel = T("csv.status.completed", c)
		case "cancelled":
			statusLabel = T("csv.status.cancelled", c)
		}

		// Translate payment method
		methodLabel := paymentMethod
		switch paymentMethod {
		case "balance":
			methodLabel = T("csv.method.balance", c)
		case "alipay":
			methodLabel = T("csv.method.alipay", c)
		case "wechat", "wechat_pay":
			methodLabel = T("csv.method.wechat", c)
		case "stripe":
			methodLabel = T("csv.method.stripe", c)
		case "paypal":
			methodLabel = "PayPal"
		case "credit_card", "card":
			methodLabel = T("csv.method.stripe", c)
		}

		amountDisplay := QuotaToUSD(int64(amount))
		createdAtStr := createdAt.Format("2006-01-02 15:04:05")

		_, _ = fmt.Fprintf(c.Writer, "%s,%s,%s,%s,%s\n",
			csvSanitize(orderID), csvSanitize(amountDisplay), csvSanitize(statusLabel), csvSanitize(methodLabel), createdAtStr)
	}
}

func (r *OrderRoutes) getOrder(c *gin.Context) {
	userID := c.GetString("user_id")
	orderID := c.Param("id")

	var id, oid, status, productType, paymentMethod, currency string
	var amount int64
	var exchangeRate float64
	var apiKeyID *string
	var createdAt time.Time
	var completedAt *time.Time

	// Try by UUID first, then by order_id
	query := `SELECT id::text, order_id, amount, COALESCE(currency, 'USD') as currency, COALESCE(exchange_rate, 1.0) as exchange_rate, status, product_type, COALESCE(payment_method, '') as payment_method,
		         api_key_id::text, created_at, completed_at
		  FROM orders
		  WHERE id = $1::uuid AND user_id = $2::uuid`
	err := r.db.QueryRow(c.Request.Context(), query, orderID, userID).Scan(
		&id, &oid, &amount, &currency, &exchangeRate, &status, &productType, &paymentMethod,
		&apiKeyID, &createdAt, &completedAt)

	if err != nil {
		// Try by order_id
		query = `SELECT id::text, order_id, amount, COALESCE(currency, 'USD') as currency, COALESCE(exchange_rate, 1.0) as exchange_rate, status, product_type, COALESCE(payment_method, '') as payment_method,
			        api_key_id::text, created_at, completed_at
			 FROM orders
			 WHERE order_id = $1 AND user_id = $2::uuid`
		err = r.db.QueryRow(c.Request.Context(), query, orderID, userID).Scan(
			&id, &oid, &amount, &currency, &exchangeRate, &status, &productType, &paymentMethod,
			&apiKeyID, &createdAt, &completedAt)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Order not found"}})
			return
		}
	}

	resp := gin.H{
		"id":             id,
		"order_id":       oid,
		"amount":         amount,
		"currency":       currency,
		"exchange_rate":  exchangeRate,
		"status":         status,
		"product_type":   productType,
		"payment_method": paymentMethod,
		"created_at":     createdAt,
	}
	if apiKeyID != nil {
		resp["api_key_id"] = *apiKeyID
	}
	if completedAt != nil {
		resp["completed_at"] = *completedAt
	}

	c.JSON(http.StatusOK, gin.H{"order": resp})
}

// checkPaymentAvailable returns true if at least one payment gateway is enabled
// or if simulation mode is forced by admin.
func (r *OrderRoutes) checkPaymentAvailable(ctx context.Context) bool {
	var simulationMode string
	_ = r.db.QueryRow(ctx,
		`SELECT COALESCE(value, 'false') FROM system_settings WHERE key = 'payment_simulation_mode'`,
	).Scan(&simulationMode)
	if simulationMode == "true" {
		return true
	}
	var enabledCount int
	_ = r.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM payment_gateways WHERE enabled = true`,
	).Scan(&enabledCount)
	return enabledCount > 0
}

// allowedPaymentMethods is the whitelist of valid payment method values for
// order creation. Prevents storing arbitrary user-supplied strings.
var allowedPaymentMethods = map[string]bool{
	"alipay":      true,
	"wechat":      true,
	"stripe":      true,
	"paypal":      true,
	"balance":     true,
	"credit_card": true,
	"card":        true,
	"wechat_pay":  true,
}

func (r *OrderRoutes) createOrder(c *gin.Context) {
	userID := c.GetString("user_id")

	var req struct {
		ProductType   string `json:"product_type" binding:"required"`
		Amount        int    `json:"amount" binding:"required,min=1"`
		PaymentMethod string `json:"payment_method"`
		APIKeyID      string `json:"api_key_id"`
		Currency      string `json:"currency"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Product type and amount are required"}})
		return
	}

	if req.PaymentMethod == "" {
		req.PaymentMethod = "alipay"
	}

	if !allowedPaymentMethods[req.PaymentMethod] {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid payment method"}})
		return
	}

	if req.Currency != "" && !config.IsSupportedCurrency(req.Currency) {
		req.Currency = ""
	}

	// Check payment availability before creating order
	if !r.checkPaymentAvailable(c.Request.Context()) {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": T("error.payment_gateway_disabled", c)}})
		return
	}

	var currency string
	if req.Currency != "" {
		currency = req.Currency
	} else {
		_ = r.db.QueryRow(c.Request.Context(),
			`SELECT COALESCE(preferred_currency, 'USD') FROM users WHERE id = $1::uuid`,
			userID,
		).Scan(&currency)
	}
	var rateStr string
	_ = r.db.QueryRow(c.Request.Context(),
		`SELECT value FROM system_settings WHERE key = 'usd_exchange_rate'`,
	).Scan(&rateStr)
	exchangeRate := 7.20
	if rateStr != "" {
		var rateCents int
		_, _ = fmt.Sscanf(rateStr, "%d", &rateCents)
		if rateCents > 0 {
			exchangeRate = float64(rateCents) / 100.0
		}
	}

	// Convert to micro-USD quota for storage
	var amountQuota int64
	if currency == "USD" {
		amountQuota = USDtoQuota(float64(req.Amount))
	} else {
		amountQuota = RMBYuanToQuota(float64(req.Amount), exchangeRate)
	}
	orderID := "ORD-" + uuid.New().String()[:8]

	var id string
	err := r.db.QueryRow(c.Request.Context(),
		`INSERT INTO orders (user_id, order_id, amount, currency, exchange_rate, status, product_type, payment_method, api_key_id)
		 VALUES ($1::uuid, $2, $3, $4, $5, 'pending', $6, $7, $8)
		 RETURNING id::text`,
		userID, orderID, amountQuota, currency, exchangeRate, req.ProductType, req.PaymentMethod,
		nullIfEmpty(req.APIKeyID),
	).Scan(&id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to create order"}})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"order": gin.H{
			"id":       id,
			"order_id": orderID,
			"amount":   amountQuota,
			"status":   "pending",
		},
	})
}

func (r *OrderRoutes) updateOrder(c *gin.Context) {
	userID := c.GetString("user_id")
	orderID := c.Param("id")

	var req struct {
		Amount        int    `json:"amount"`
		PaymentMethod string `json:"payment_method"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Invalid request"}})
		return
	}

	// P0: the order amount is immutable after creation. Allowing a user to
	// raise the amount on a pending order after a payment session was created
	// lets them pay the original (lower) price but get credited the inflated
	// amount. Users who need a different amount must cancel and re-create.
	if req.Amount > 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Order amount cannot be changed. Please cancel and create a new order."}})
		return
	}

	if req.PaymentMethod == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "No fields to update"}})
		return
	}

	// Build dynamic update
	setParts := []string{"payment_method = $1"}
	args := []interface{}{req.PaymentMethod}
	argIndex := 2

	setParts = append(setParts, fmt.Sprintf("updated_at = $%d", argIndex))
	args = append(args, time.Now())
	argIndex++

	// Try by UUID first, then by order_id
	query := fmt.Sprintf(`UPDATE orders SET %s
		 WHERE id = $%d::uuid AND user_id = $%d::uuid AND status = 'pending'`,
		strings.Join(setParts, ", "), argIndex, argIndex+1)
	args = append(args, orderID, userID)

	result, err := r.db.Pool.Exec(c.Request.Context(), query, args...)
	if err != nil {
		// Try by order_id
		args[len(args)-2] = orderID
		query = fmt.Sprintf(`UPDATE orders SET %s
			 WHERE order_id = $%d AND user_id = $%d::uuid AND status = 'pending'`,
			strings.Join(setParts, ", "), argIndex, argIndex+1)
		result, err = r.db.Pool.Exec(c.Request.Context(), query, args...)
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to update order"}})
		return
	}

	if result.RowsAffected() == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Order not found or cannot be updated"}})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Order updated"})
}

func (r *OrderRoutes) cancelOrder(c *gin.Context) {
	userID := c.GetString("user_id")
	orderID := c.Param("id")

	// Only allow cancelling pending orders
	result, err := r.db.Pool.Exec(c.Request.Context(),
		`UPDATE orders SET status = 'cancelled', updated_at = NOW()
		 WHERE (id::text = $1 OR order_id = $1) AND user_id = $2::uuid AND status = 'pending'`,
		orderID, userID,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to cancel order"}})
		return
	}

	if result.RowsAffected() == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Order not found or cannot be cancelled"}})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Order cancelled"})
}

func nullIfEmpty(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

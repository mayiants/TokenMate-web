package query

import (
	"context"
	"time"

	"github.com/mayiants/ai-portal-go/internal/db"
)

// ── Usage / Billing ───────────────────────────────────────────────────────────

// UsageRecord holds a billing usage record.
type UsageRecord struct {
	TraceID      string    `json:"trace_id"`
	Model        string    `json:"model"`
	Provider     string    `json:"provider"`
	InputTokens  int64     `json:"input_tokens"`
	OutputTokens int64     `json:"output_tokens"`
	CachedTokens int64     `json:"cached_tokens"`
	Requests     int       `json:"requests"`
	Cost         int64     `json:"cost"` // micro-USD
	LatencyMs    int       `json:"latency_ms"`
	CreatedAt    time.Time `json:"created_at"`
	Endpoint     string    `json:"endpoint"`
	ErrorCode    string    `json:"error_code"`
	IsStreaming  bool      `json:"is_streaming"`
	ClientIP     string    `json:"client_ip"`
	UserAgent    string    `json:"user_agent"`
	AgentTools   string    `json:"agent_tools"`
	CalledTools  string    `json:"called_tools"`
	Status       string    `json:"status"`
	GroupID      string    `json:"group_id"`
	GroupName    string    `json:"group_name"`
	APIKeyName   string    `json:"api_key_name"`
	KeyPrefix    string    `json:"key_prefix"`
}

// BillingData holds billing summary + recent usage records.
type BillingData struct {
	Balance       int64
	TotalTokens   int64
	TotalRequests int64
	Records       []UsageRecord
}

// GetBillingData fetches billing summary + recent usage records for a user.
func GetBillingData(ctx context.Context, database *db.DB, userID string, limit int) (*BillingData, error) {
	d := &BillingData{Records: []UsageRecord{}}

	// Balance
	_ = database.QueryRow(ctx, `SELECT COALESCE(balance, 0) FROM users WHERE id = $1::uuid`, userID).Scan(&d.Balance)

	// Total tokens and requests
	_ = database.QueryRow(ctx,
		`SELECT COALESCE(SUM(input_tokens + output_tokens), 0), COALESCE(SUM(request_count), 0)
		 FROM usage_records WHERE user_id = $1::uuid`,
		userID,
	).Scan(&d.TotalTokens, &d.TotalRequests)

	// Recent records
	rows, err := database.Query(ctx,
		`SELECT model, input_tokens, output_tokens, request_count, total_cost, created_at
		 FROM usage_records WHERE user_id = $1::uuid ORDER BY created_at DESC LIMIT $2`,
		userID, limit,
	)
	if err != nil {
		logQueryError(ctx, "GetBillingData", err)
		return d, nil
	}
	defer rows.Close()

	for rows.Next() {
		var r UsageRecord
		if err := rows.Scan(&r.Model, &r.InputTokens, &r.OutputTokens, &r.Requests, &r.Cost, &r.CreatedAt); err == nil {
			d.Records = append(d.Records, r)
		}
	}
	return d, nil
}

// ── Orders ────────────────────────────────────────────────────────────────────

// OrderRecord holds an order.
type OrderRecord struct {
	ID           string
	OrderID      string
	Amount       int64
	Currency     string
	ExchangeRate float64
	Status       string
	Method       string
	ProductType  string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// OrderFilter holds optional filters for order queries.
type OrderFilter struct {
	Status    string
	StartDate string
	EndDate   string
}

// OrdersData holds paginated order results.
type OrdersData struct {
	Items      []OrderRecord
	Total      int
	Page       int
	PageSize   int
	TotalPages int
}

// GetOrdersData fetches paginated orders for a user.
func GetOrdersData(ctx context.Context, database *db.DB, userID string, page, pageSize int, filter OrderFilter) (*OrdersData, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 50 {
		pageSize = 50
	}

	// Build WHERE clause
	where := "deleted_at IS NULL AND user_id = $1::uuid"
	args := []interface{}{userID}
	argIdx := 2

	if filter.Status != "" {
		where += " AND status = $" + itoa(argIdx)
		args = append(args, filter.Status)
		argIdx++
	}
	if filter.StartDate != "" {
		where += " AND created_at >= $" + itoa(argIdx)
		args = append(args, filter.StartDate)
		argIdx++
	}
	if filter.EndDate != "" {
		where += " AND created_at < $" + itoa(argIdx) + "::date + INTERVAL '1 day'"
		args = append(args, filter.EndDate)
		argIdx++
	}

	// Count total
	var total int
	_ = database.QueryRow(ctx, "SELECT COUNT(*) FROM orders WHERE "+where, args...).Scan(&total)

	offset := (page - 1) * pageSize
	queryArgs := append(args, pageSize, offset)
	rows, err := database.Query(ctx,
		`SELECT id::text, order_id, amount, COALESCE(currency, 'CNY') as currency, COALESCE(exchange_rate, 7.20) as exchange_rate, status, payment_method, product_type, created_at, updated_at
		 FROM orders WHERE `+where+`
		 ORDER BY created_at DESC LIMIT $`+itoa(argIdx)+` OFFSET $`+itoa(argIdx+1),
		queryArgs...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []OrderRecord
	for rows.Next() {
		var o OrderRecord
		var createdAt, updatedAt interface{}
		if err := rows.Scan(&o.ID, &o.OrderID, &o.Amount, &o.Currency, &o.ExchangeRate, &o.Status, &o.Method, &o.ProductType, &createdAt, &updatedAt); err == nil {
			if t, ok := createdAt.(time.Time); ok {
				o.CreatedAt = t
			}
			if t, ok := updatedAt.(time.Time); ok {
				o.UpdatedAt = t
			}
			items = append(items, o)
		}
	}

	totalPages := (total + pageSize - 1) / pageSize
	if totalPages < 1 {
		totalPages = 1
	}

	return &OrdersData{
		Items: items, Total: total, Page: page, PageSize: pageSize, TotalPages: totalPages,
	}, nil
}

// ── Invoices ──────────────────────────────────────────────────────────────────

// InvoiceProfile holds an invoice profile.
type InvoiceProfile struct {
	ID        string
	Name      string
	Type      string
	TaxNumber string
	IsDefault bool
	CreatedAt time.Time
}

// InvoiceRequest holds an invoice request record.
type InvoiceRequest struct {
	ID        string
	Title     string
	Amount    int64
	Status    string
	OrderID   string
	CreatedAt time.Time
}

// InvoiceData holds invoice profiles + paginated requests.
type InvoiceData struct {
	Profiles   []InvoiceProfile
	Requests   []InvoiceRequest
	Total      int
	Page       int
	PageSize   int
	TotalPages int
}

// GetInvoiceData fetches invoice profiles and paginated requests for a user.
func GetInvoiceData(ctx context.Context, database *db.DB, userID string, page, pageSize int) (*InvoiceData, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 50 {
		pageSize = 50
	}

	d := &InvoiceData{}

	// Invoice profiles (not paginated, usually few)
	rows, err := database.Query(ctx,
		`SELECT id::text, name, type, tax_number, is_default, created_at
		 FROM invoice_profiles WHERE deleted_at IS NULL AND user_id = $1::uuid ORDER BY is_default DESC, created_at DESC`,
		userID,
	)
	if err == nil {
		for rows.Next() {
			var p InvoiceProfile
			if err := rows.Scan(&p.ID, &p.Name, &p.Type, &p.TaxNumber, &p.IsDefault, &p.CreatedAt); err == nil {
				d.Profiles = append(d.Profiles, p)
			}
		}
		rows.Close()
	}

	// Count total requests
	_ = database.QueryRow(ctx, `SELECT COUNT(*) FROM invoice_requests WHERE deleted_at IS NULL AND user_id = $1::uuid`, userID).Scan(&d.Total)

	// Paginated requests
	offset := (page - 1) * pageSize
	rows, err = database.Query(ctx,
		`SELECT id::text, title, amount, status, COALESCE(order_id::text, ''), created_at
		 FROM invoice_requests WHERE deleted_at IS NULL AND user_id = $1::uuid ORDER BY created_at DESC LIMIT $2 OFFSET $3`,
		userID, pageSize, offset,
	)
	if err == nil {
		for rows.Next() {
			var r InvoiceRequest
			if err := rows.Scan(&r.ID, &r.Title, &r.Amount, &r.Status, &r.OrderID, &r.CreatedAt); err == nil {
				d.Requests = append(d.Requests, r)
			}
		}
		rows.Close()
	}

	d.Page = page
	d.PageSize = pageSize
	totalPages := (d.Total + pageSize - 1) / pageSize
	if totalPages < 1 {
		totalPages = 1
	}
	d.TotalPages = totalPages

	return d, nil
}

// ── Recharge Records ──────────────────────────────────────────────────────────

// RechargeRecord holds a recharge order record (from orders table).
type RechargeRecord struct {
	ID            string
	OrderID       string
	Amount        int64
	Currency      string
	ExchangeRate  float64
	Status        string
	PaymentMethod string
	ProductType   string
	ReceiptKey    string `json:"-"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// RechargeRecordsData holds paginated recharge records.
type RechargeRecordsData struct {
	Items      []RechargeRecord
	Total      int
	Page       int
	PageSize   int
	TotalPages int
}

// GetRechargeRecords fetches paginated recharge orders for a user from the orders table.
func GetRechargeRecords(ctx context.Context, database *db.DB, userID string, page, pageSize int, startDate, endDate, status string) (*RechargeRecordsData, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 50 {
		pageSize = 50
	}

	where := "deleted_at IS NULL AND product_type IN ('recharge', 'subscription', 'subscription_renewal') AND user_id = $1::uuid"
	args := []interface{}{userID}
	argIdx := 2

	if status != "" {
		where += " AND status = $" + itoa(argIdx)
		args = append(args, status)
		argIdx++
	}
	if startDate != "" {
		where += " AND created_at >= $" + itoa(argIdx)
		args = append(args, startDate)
		argIdx++
	}
	if endDate != "" {
		where += " AND created_at < $" + itoa(argIdx) + "::date + INTERVAL '1 day'"
		args = append(args, endDate)
		argIdx++
	}

	var total int
	_ = database.QueryRow(ctx, "SELECT COUNT(*) FROM orders WHERE "+where, args...).Scan(&total)

	offset := (page - 1) * pageSize
	queryArgs := append(args, pageSize, offset)
	rows, err := database.Query(ctx,
		`SELECT id::text, order_id, amount, COALESCE(currency, 'CNY') as currency, COALESCE(exchange_rate, 7.20) as exchange_rate, status, COALESCE(payment_method, ''), product_type, COALESCE(receipt_key, ''), created_at, updated_at
		 FROM orders WHERE `+where+`
		 ORDER BY created_at DESC LIMIT $`+itoa(argIdx)+` OFFSET $`+itoa(argIdx+1),
		queryArgs...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []RechargeRecord
	for rows.Next() {
		var r RechargeRecord
		var createdAt, updatedAt interface{}
		if err := rows.Scan(&r.ID, &r.OrderID, &r.Amount, &r.Currency, &r.ExchangeRate, &r.Status, &r.PaymentMethod, &r.ProductType, &r.ReceiptKey, &createdAt, &updatedAt); err == nil {
			if t, ok := createdAt.(time.Time); ok {
				r.CreatedAt = t
			}
			if t, ok := updatedAt.(time.Time); ok {
				r.UpdatedAt = t
			}
			items = append(items, r)
		}
	}

	totalPages := (total + pageSize - 1) / pageSize
	if totalPages < 1 {
		totalPages = 1
	}

	return &RechargeRecordsData{
		Items: items, Total: total, Page: page, PageSize: pageSize, TotalPages: totalPages,
	}, nil
}

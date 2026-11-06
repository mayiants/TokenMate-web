package services

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ai-proxy/ai-portal-go/internal/db"
	"go.uber.org/zap"
)

// ExchangeRateSource defines an interface for fetching exchange rates
type ExchangeRateSource interface {
	Name() string
	Fetch(ctx context.Context, from, to string) (float64, error)
}

// ExchangeService periodically fetches and stores exchange rates
type ExchangeService struct {
	db      *db.DB
	logger  *zap.Logger
	sources []ExchangeRateSource
	stopCh  chan struct{}
	client  *http.Client
}

// NewExchangeService creates a new exchange rate service
func NewExchangeService(db *db.DB, logger *zap.Logger) *ExchangeService {
	client := &http.Client{Timeout: 15 * time.Second}
	return &ExchangeService{
		db:     db,
		logger: logger,
		sources: []ExchangeRateSource{
			&ExchangerateAPI{client: client},
			&OpenERAPI{client: client},
		},
		stopCh: make(chan struct{}),
		client: client,
	}
}

// Start begins the periodic exchange rate update loop
func (s *ExchangeService) Start(ctx context.Context) {
	s.updateRates(ctx)

	ticker := time.NewTicker(8 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.updateRates(ctx)
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return
		}
	}
}

// Stop gracefully stops the service
func (s *ExchangeService) Stop() {
	close(s.stopCh)
}

// GetRate returns the latest exchange rate for a currency pair.
func (s *ExchangeService) GetRate(ctx context.Context, from, to string) (float64, error) {
	from = strings.ToUpper(from)
	to = strings.ToUpper(to)
	if from == to {
		return 1.0, nil
	}

	// 1. Try local cache first.
	var rate float64
	cacheErr := s.db.QueryRow(ctx,
		`SELECT rate FROM exchange_rates
		 WHERE from_currency = $1 AND to_currency = $2
		 ORDER BY fetched_at DESC LIMIT 1`,
		from, to,
	).Scan(&rate)
	if cacheErr == nil {
		return rate, nil
	}

	// 2. Live fetch and cache the result for next time.
	rate, err := s.fetchWithFallback(ctx, from, to)
	if err != nil {
		// Legacy fallback: for USD/CNY, use the usd_exchange_rate system setting if available.
		if from == "USD" && to == "CNY" {
			if legacyRate, legacyErr := s.legacyRateFromSettings(ctx); legacyErr == nil && legacyRate > 0 {
				return legacyRate, nil
			}
		}
		return 0, err
	}
	if rate <= 0 {
		return 0, fmt.Errorf("invalid exchange rate received")
	}
	if cacheErr := s.db.Exec(ctx,
		`INSERT INTO exchange_rates (from_currency, to_currency, rate, source, fetched_at)
		 VALUES ($1, $2, $3, $4, NOW())
		 ON CONFLICT (from_currency, to_currency) DO UPDATE
		 SET rate = EXCLUDED.rate, fetched_at = EXCLUDED.fetched_at`,
		from, to, rate, s.sources[0].Name()); cacheErr != nil {
		s.logger.Warn("Failed to cache exchange rate", zap.Error(cacheErr))
	}
	return rate, nil
}

// legacyRateFromSettings reads the legacy usd_exchange_rate system setting (stored in cents).
func (s *ExchangeService) legacyRateFromSettings(ctx context.Context) (float64, error) {
	var valueStr string
	err := s.db.QueryRow(ctx,
		`SELECT value FROM system_settings WHERE key = 'usd_exchange_rate'`,
	).Scan(&valueStr)
	if err != nil {
		return 0, err
	}
	valueStr = strings.TrimSpace(valueStr)
	if valueStr == "" {
		return 0, fmt.Errorf("empty usd_exchange_rate setting")
	}
	cents, err := strconv.ParseInt(valueStr, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid usd_exchange_rate value %q: %w", valueStr, err)
	}
	return float64(cents) / 100.0, nil
}

// GetRateOrDefault returns the latest exchange rate for a currency pair.
// If live sources fail, it returns the provided defaultRate instead of an error.
func (s *ExchangeService) GetRateOrDefault(ctx context.Context, from, to string, defaultRate float64) float64 {
	rate, err := s.GetRate(ctx, from, to)
	if err != nil {
		s.logger.Warn("Exchange rate lookup failed, using default", zap.String("from", from), zap.String("to", to), zap.Float64("default", defaultRate), zap.Error(err))
		if defaultRate <= 0 {
			return 1.0
		}
		return defaultRate
	}
	return rate
}

// updateRates fetches exchange rates for all currencies in use.
func (s *ExchangeService) updateRates(ctx context.Context) {
	// Collect all distinct display currencies from enterprises + user preferences.
	currencies := map[string]bool{}
	rows, err := s.db.Query(ctx,
		`SELECT DISTINCT display_currency FROM enterprises WHERE display_currency IS NOT NULL AND display_currency != 'USD' AND deleted_at IS NULL
		 UNION
		 SELECT DISTINCT preferred_currency FROM users WHERE preferred_currency IS NOT NULL AND preferred_currency != 'USD' AND deleted_at IS NULL`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var c string
			if rows.Scan(&c) == nil && c != "" {
				currencies[c] = true
			}
		}
	}
	// Always include CNY for legacy system_settings compatibility.
	currencies["CNY"] = true

	for cur := range currencies {
		rate, err := s.fetchWithFallback(ctx, "USD", cur)
		if err != nil {
			s.logger.Error("Failed to fetch exchange rate", zap.String("pair", "USD/"+cur), zap.Error(err))
			continue
		}

		// Update exchange_rates table
		if err := s.db.Exec(ctx,
			`INSERT INTO exchange_rates (from_currency, to_currency, rate, source, fetched_at)
			 VALUES ($1, $2, $3, $4, NOW())
			 ON CONFLICT (from_currency, to_currency) DO UPDATE SET rate = EXCLUDED.rate, fetched_at = EXCLUDED.fetched_at`,
			"USD", cur, rate, s.sources[0].Name()); err != nil {
			s.logger.Error("database operation failed", zap.Error(err))
		}

		// Legacy: keep system_settings usd_exchange_rate for CNY
		if cur == "CNY" {
			if err := s.db.Exec(ctx, `INSERT INTO system_settings (key, value, category, description)
				 VALUES ('usd_exchange_rate', $1, 'operation', 'USD to CNY exchange rate (cents)')
				 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()`, fmt.Sprintf("%.0f", rate*100)); err != nil {
				s.logger.Error("database operation failed", zap.Error(err))
			}
		}

		s.logger.Info("Exchange rate updated", zap.String("pair", "USD/"+cur), zap.Float64("rate", rate))
	}
}

func (s *ExchangeService) fetchWithFallback(ctx context.Context, from, to string) (float64, error) {
	for _, src := range s.sources {
		rate, err := src.Fetch(ctx, from, to)
		if err == nil && rate > 0 {
			return rate, nil
		}
		s.logger.Warn("Exchange source failed, trying next",
			zap.String("source", src.Name()),
			zap.Error(err))
	}
	return 0, fmt.Errorf("all exchange rate sources failed for %s/%s", from, to)
}

// ========== Built-in Exchange Rate Sources ==========

// ExchangerateAPI uses exchangerate-api.com (free tier)
type ExchangerateAPI struct{ client *http.Client }

func (e *ExchangerateAPI) Name() string { return "exchangerate-api.com" }

func (e *ExchangerateAPI) Fetch(ctx context.Context, from, to string) (float64, error) {
	url := fmt.Sprintf("https://api.exchangerate-api.com/v4/latest/%s", from)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return 0, err
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	var result struct {
		Rates map[string]float64 `json:"rates"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, err
	}
	rate, ok := result.Rates[to]
	if !ok {
		return 0, fmt.Errorf("currency %s not found", to)
	}
	return rate, nil
}

// OpenERAPI uses open.er-api.com (no rate limits)
type OpenERAPI struct{ client *http.Client }

func (e *OpenERAPI) Name() string { return "open.er-api.com" }

func (e *OpenERAPI) Fetch(ctx context.Context, from, to string) (float64, error) {
	url := fmt.Sprintf("https://open.er-api.com/v6/latest/%s", from)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return 0, err
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	var result struct {
		Rates map[string]float64 `json:"rates"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, err
	}
	rate, ok := result.Rates[to]
	if !ok {
		return 0, fmt.Errorf("currency %s not found", to)
	}
	return rate, nil
}

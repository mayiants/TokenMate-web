package query

import (
	"context"
	"time"

	"github.com/mayiants/ai-portal-go/internal/db"
)

// ProfileData holds user profile info.
type ProfileData struct {
	Name              string
	Email             string
	SecondEmail       string
	Phone             string
	Company           string
	Locale            string
	Theme             string
	Timezone          string
	PreferredCurrency string
	Balance           int64
	Role              string
	CreatedAt         time.Time
}

// GetProfileData fetches user profile info.
func GetProfileData(ctx context.Context, database *db.DB, userID string) (*ProfileData, error) {
	var p ProfileData
	err := database.QueryRow(ctx,
		`SELECT COALESCE(name, ''), email, COALESCE(second_email, ''), COALESCE(phone, ''),
		 COALESCE(company, ''), COALESCE(locale, ''),
		 COALESCE(theme, 'system'), COALESCE(timezone, 'UTC'), COALESCE(preferred_currency, 'USD'), COALESCE(balance, 0), role, created_at
		 FROM users WHERE id = $1::uuid`,
		userID,
	).Scan(&p.Name, &p.Email, &p.SecondEmail, &p.Phone, &p.Company,
		&p.Locale, &p.Theme, &p.Timezone, &p.PreferredCurrency, &p.Balance, &p.Role, &p.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

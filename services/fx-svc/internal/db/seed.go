package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// seedRate is a single currency-pair rate entry.
type seedRate struct{ from, to, rate string }

// defaultRates covers the main pairs used by the platform.
// Rates are approximate mid-market values — refresh via admin API in production.
var defaultRates = []seedRate{
	{"USD", "INR", "83.5000"},
	{"INR", "USD", "0.01198"},
	{"USD", "EUR", "0.9200"},
	{"EUR", "USD", "1.0870"},
	{"USD", "GBP", "0.7860"},
	{"GBP", "USD", "1.2722"},
	{"USD", "SGD", "1.3510"},
	{"SGD", "USD", "0.7402"},
	{"USD", "AED", "3.6730"},
	{"AED", "USD", "0.2722"},
	{"EUR", "INR", "90.7200"},
	{"INR", "EUR", "0.01102"},
	{"GBP", "INR", "106.100"},
	{"INR", "GBP", "0.00943"},
}

// SeedRates upserts all default rates with a 24h validity window.
// Called once on service startup — idempotent via ON CONFLICT DO UPDATE.
func SeedRates(ctx context.Context, pool *pgxpool.Pool) error {
	validUntil := time.Now().UTC().Add(365 * 24 * time.Hour)
	for _, r := range defaultRates {
		_, err := pool.Exec(ctx, `
			INSERT INTO fx.rates (from_cur, to_cur, rate, valid_until)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (from_cur, to_cur)
			DO UPDATE SET rate = EXCLUDED.rate, valid_until = EXCLUDED.valid_until`,
			r.from, r.to, r.rate, validUntil,
		)
		if err != nil {
			return err
		}
	}
	return nil
}

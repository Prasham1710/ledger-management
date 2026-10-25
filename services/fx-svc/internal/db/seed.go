package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// seedRate is a single currency-pair rate entry.
type seedRate struct{ from, to, rate string }

// defaultRates are development-only starting values.
//
// Only the forward direction is listed. The previous table also carried each
// inverse as an independently rounded constant — USD→INR 83.5 alongside
// INR→USD 0.01198, where 1/83.5 is 0.011976 — so a USD→INR→USD round trip
// created value out of nothing. Inverses are now derived at query time from the
// forward rate, which makes the pair exactly reciprocal by construction.
var defaultRates = []seedRate{
	{"USD", "INR", "83.50000000"},
	{"USD", "EUR", "0.92000000"},
	{"USD", "GBP", "0.78600000"},
	{"USD", "SGD", "1.35100000"},
	{"USD", "AED", "3.67300000"},
	{"USD", "JPY", "157.20000000"},
	{"EUR", "INR", "90.72000000"},
	{"GBP", "INR", "106.10000000"},
}

// seedValidity is how long seeded rates remain usable.
//
// Deliberately short. Rates are market data, and a stale rate prices real
// transfers wrongly. The previous seed set valid_until to a year ahead while its
// own comment claimed 24 hours, so the staleness check in GetRate could never
// fire and 2024-era constants would have priced transfers indefinitely.
const seedValidity = 24 * time.Hour

// SeedRates inserts the development rate table if a pair is absent.
//
// Existing rates are left untouched. The previous implementation used
// ON CONFLICT DO UPDATE and ran on every startup, so each restart — and with two
// replicas, each deployment — silently overwrote whatever live rates were in the
// database with these hardcoded constants.
//
// Call this only when seeding is explicitly enabled; see Config.SeedRates.
func SeedRates(ctx context.Context, pool *pgxpool.Pool) (inserted int, err error) {
	validUntil := time.Now().UTC().Add(seedValidity)

	for _, r := range defaultRates {
		tag, execErr := pool.Exec(ctx, `
			INSERT INTO fx.rates (from_cur, to_cur, rate, source, valid_until)
			VALUES ($1, $2, $3, 'seed', $4)
			ON CONFLICT (from_cur, to_cur) DO NOTHING`,
			r.from, r.to, r.rate, validUntil,
		)
		if execErr != nil {
			return inserted, fmt.Errorf("seed rate %s→%s: %w", r.from, r.to, execErr)
		}
		inserted += int(tag.RowsAffected())
	}
	return inserted, nil
}

// CountUsableRates reports how many rates are currently within their validity
// window, so startup can warn when the service cannot price anything.
func CountUsableRates(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM fx.rates WHERE valid_until > NOW()`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count usable rates: %w", err)
	}
	return n, nil
}

package config

import (
	"time"

	"github.com/redis/go-redis/v9"

	shared "github.com/prasham/ledger/internal/config"
)

// Config holds fx-svc runtime settings.
type Config struct {
	GRPCPort    int
	MetricsPort int
	PostgresDSN string
	Redis       *redis.Options

	OTLPEndpoint     string
	Version          string
	EnableReflection bool

	// QuoteTTL is how long a quote may be locked after being issued.
	QuoteTTL time.Duration

	// SeedRates inserts the development rate table on startup, for pairs that
	// have no rate yet.
	//
	// Off in production. Seeding previously ran unconditionally with
	// ON CONFLICT DO UPDATE, so every restart overwrote live market rates with
	// hardcoded constants.
	SeedRates bool

	// QuoteRetention is how long expired unlocked quotes are kept before being
	// purged.
	QuoteRetention time.Duration
	// QuotePurgeInterval is how often expired quotes are purged.
	QuotePurgeInterval time.Duration
}

// Load resolves configuration from the environment.
func Load() (Config, error) {
	redisOpts, err := shared.RedisOptions()
	if err != nil {
		return Config{}, err
	}
	return Config{
		GRPCPort:    shared.EnvInt("FX_GRPC_PORT", 50053),
		MetricsPort: shared.EnvInt("FX_METRICS_PORT", 9103),
		PostgresDSN: shared.PostgresDSN(),
		Redis:       redisOpts,

		OTLPEndpoint:     shared.OTLPEndpoint(),
		Version:          shared.Version(),
		EnableReflection: shared.EnvBool("GRPC_REFLECTION", !shared.IsProduction()),

		QuoteTTL: shared.EnvDuration("FX_QUOTE_TTL_SEC", 30*time.Second),
		// Default follows the environment: convenient locally, never in production.
		SeedRates: shared.EnvBool("FX_SEED_RATES", !shared.IsProduction()),

		QuoteRetention:     shared.EnvDuration("FX_QUOTE_RETENTION", time.Hour),
		QuotePurgeInterval: shared.EnvDuration("FX_QUOTE_PURGE_INTERVAL", 10*time.Minute),
	}, nil
}

package config

import (
	"time"

	"github.com/redis/go-redis/v9"

	shared "github.com/prasham/ledger/internal/config"
)

// Config holds transaction-svc runtime settings.
type Config struct {
	GRPCPort    string
	MetricsPort string
	DSN         string
	// Redis carries full connection settings so a rediss:// URL works.
	Redis       *redis.Options
	LedgerAddr  string
	AccountAddr string

	OTLPEndpoint     string
	Version          string
	EnableReflection bool

	// HoldTTL is how long the saga reserves source funds. It must comfortably
	// exceed the saga's own worst-case duration but stay short enough that an
	// abandoned saga's funds are reclaimed promptly.
	HoldTTL time.Duration
	// RecoveryInterval is how often abandoned in-flight sagas are swept.
	RecoveryInterval time.Duration
	// RecoveryStuckAfter is how long a transaction may stay PENDING before the
	// recovery worker treats it as abandoned.
	RecoveryStuckAfter time.Duration
	// RecoveryBatchSize bounds how many stuck sagas one pass resolves.
	RecoveryBatchSize int
}

// Load resolves configuration from the environment. Connection settings go
// through the shared resolver so both the Helm-injected names (POSTGRES_URL,
// REDIS_URL) and the local names (POSTGRES_HOST, REDIS_ADDR) work.
func Load() (Config, error) {
	redisOpts, err := shared.RedisOptions()
	if err != nil {
		return Config{}, err
	}
	return Config{
		GRPCPort:    shared.Env("TRANSACTION_GRPC_PORT", "50054"),
		MetricsPort: shared.Env("TRANSACTION_METRICS_PORT", "9104"),
		DSN:         shared.PostgresDSN(),
		Redis:       redisOpts,
		LedgerAddr:  shared.Env("LEDGER_GRPC_ADDR", "localhost:50051"),
		AccountAddr: shared.Env("ACCOUNT_GRPC_ADDR", "localhost:50052"),

		OTLPEndpoint:     shared.OTLPEndpoint(),
		Version:          shared.Version(),
		EnableReflection: shared.EnvBool("GRPC_REFLECTION", !shared.IsProduction()),

		HoldTTL:            shared.EnvDuration("PAYMENT_HOLD_TTL", time.Hour),
		RecoveryInterval:   shared.EnvDuration("SAGA_RECOVERY_INTERVAL", time.Minute),
		RecoveryStuckAfter: shared.EnvDuration("SAGA_RECOVERY_STUCK_AFTER", 5*time.Minute),
		RecoveryBatchSize:  shared.EnvInt("SAGA_RECOVERY_BATCH_SIZE", 50),
	}, nil
}

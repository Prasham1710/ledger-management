package config

import (
	"time"

	"github.com/redis/go-redis/v9"

	shared "github.com/prasham/ledger/internal/config"
	"github.com/prasham/ledger/services/payout-svc/internal/storage"
)

// Config holds payout-svc runtime settings.
type Config struct {
	GRPCPort    string
	MetricsPort string
	DSN         string
	Redis       *redis.Options
	LedgerAddr  string
	AccountAddr string
	Storage     storage.Config

	OTLPEndpoint     string
	Version          string
	EnableReflection bool

	// RecoveryInterval is how often abandoned payouts are swept.
	RecoveryInterval time.Duration
	// RecoveryStuckAfter is how long a payout may stay in-flight before the
	// recovery worker treats it as abandoned and refunds it.
	RecoveryStuckAfter time.Duration
	// RecoveryBatchSize bounds how many payouts one pass resolves.
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
	minioCfg := shared.MinIOConfig("settlement-files")
	// Allow the legacy bucket variable to keep working.
	bucket := shared.Env("MINIO_SETTLEMENT_BUCKET", minioCfg.Bucket)

	return Config{
		GRPCPort:    shared.Env("PAYOUT_GRPC_PORT", "50055"),
		MetricsPort: shared.Env("PAYOUT_METRICS_PORT", "9105"),
		DSN:         shared.PostgresDSN(),
		Redis:       redisOpts,
		LedgerAddr:  shared.Env("LEDGER_GRPC_ADDR", "localhost:50051"),
		AccountAddr: shared.Env("ACCOUNT_GRPC_ADDR", "localhost:50052"),
		Storage: storage.Config{
			Endpoint:  minioCfg.Endpoint,
			AccessKey: minioCfg.AccessKey,
			SecretKey: minioCfg.SecretKey,
			Bucket:    bucket,
			// Honoured rather than hardcoded false, so bank details can travel
			// over TLS.
			UseSSL: minioCfg.UseSSL,
			// Optional SSE-C key so settlement files holding beneficiary bank
			// details are encrypted at rest.
			EncryptionKey: shared.Env("SETTLEMENT_ENCRYPTION_KEY", ""),
		},

		OTLPEndpoint:     shared.OTLPEndpoint(),
		Version:          shared.Version(),
		EnableReflection: shared.EnvBool("GRPC_REFLECTION", !shared.IsProduction()),

		RecoveryInterval:   shared.EnvDuration("PAYOUT_RECOVERY_INTERVAL", time.Minute),
		RecoveryStuckAfter: shared.EnvDuration("PAYOUT_RECOVERY_STUCK_AFTER", 5*time.Minute),
		RecoveryBatchSize:  shared.EnvInt("PAYOUT_RECOVERY_BATCH_SIZE", 50),
	}, nil
}

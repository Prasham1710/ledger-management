package config

import (
	"time"

	"github.com/redis/go-redis/v9"

	shared "github.com/prasham/ledger/internal/config"
)

// Config holds account-svc runtime settings.
type Config struct {
	GRPCPort    int
	MetricsPort int
	PostgresDSN string
	// Redis carries full connection settings so a rediss:// URL with
	// credentials works, not just a bare host:port.
	Redis *redis.Options
	// OTLPEndpoint enables tracing when non-empty.
	OTLPEndpoint string
	// Version is the deployed build identifier.
	Version string
	// EnableReflection exposes the gRPC service surface for grpcurl.
	EnableReflection bool

	// HoldSweepInterval is how often expired holds are reclaimed.
	HoldSweepInterval time.Duration
	// HoldSweepBatchSize bounds how many holds one sweep pass reclaims.
	HoldSweepBatchSize int
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
		GRPCPort:           shared.EnvInt("ACCOUNT_GRPC_PORT", 50052),
		MetricsPort:        shared.EnvInt("ACCOUNT_METRICS_PORT", 9102),
		PostgresDSN:        shared.PostgresDSN(),
		Redis:              redisOpts,
		OTLPEndpoint:       shared.OTLPEndpoint(),
		Version:            shared.Version(),
		EnableReflection:   shared.EnvBool("GRPC_REFLECTION", !shared.IsProduction()),
		HoldSweepInterval:  shared.EnvDuration("HOLD_SWEEP_INTERVAL", time.Minute),
		HoldSweepBatchSize: shared.EnvInt("HOLD_SWEEP_BATCH_SIZE", 100),
	}, nil
}

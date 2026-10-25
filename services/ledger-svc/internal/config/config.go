package config

import (
	"github.com/redis/go-redis/v9"

	shared "github.com/prasham/ledger/internal/config"
)

// Config holds ledger-svc runtime settings.
type Config struct {
	GRPCPort    int
	MetricsPort int
	PostgresDSN string
	// Redis carries full connection settings so a rediss:// URL with
	// credentials works, not just a bare host:port.
	Redis *redis.Options
	// OTLPEndpoint enables tracing when non-empty.
	OTLPEndpoint string
	// Version is the deployed build identifier, attached to traces and metrics.
	Version string
	// EnableReflection exposes the gRPC service surface for grpcurl. Off by
	// default: it should not be reachable in production.
	EnableReflection bool
}

// Load resolves configuration from the environment. Connection settings go
// through the shared resolver so the names injected by Helm (POSTGRES_URL,
// REDIS_URL) and the names used locally (POSTGRES_HOST, REDIS_ADDR) both work.
//
// Returns an error rather than falling back to localhost on a malformed
// connection URL: silently connecting somewhere unintended is worse than
// refusing to start.
func Load() (Config, error) {
	redisOpts, err := shared.RedisOptions()
	if err != nil {
		return Config{}, err
	}
	return Config{
		GRPCPort:         shared.EnvInt("LEDGER_GRPC_PORT", 50051),
		MetricsPort:      shared.EnvInt("LEDGER_METRICS_PORT", 9101),
		PostgresDSN:      shared.PostgresDSN(),
		Redis:            redisOpts,
		OTLPEndpoint:     shared.OTLPEndpoint(),
		Version:          shared.Version(),
		EnableReflection: shared.EnvBool("GRPC_REFLECTION", !shared.IsProduction()),
	}, nil
}

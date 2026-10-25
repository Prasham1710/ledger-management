package config

import (
	"time"

	shared "github.com/prasham/ledger/internal/config"
)

// Config holds consumers runtime settings.
type Config struct {
	DatabaseURL  string
	KafkaBrokers []string
	KafkaGroupID string
	MetricsPort  string
	LogPretty    bool

	OTLPEndpoint string
	Version      string

	// RelayInterval is how often the outbox is polled.
	RelayInterval time.Duration
	// RelayBatchSize bounds how many rows one relay pass claims per schema.
	RelayBatchSize int
	// RelayLease is how long a claimed batch is reserved for this worker. It must
	// exceed the worst-case produce time.
	RelayLease time.Duration
	// PublishedRetention is how long published outbox rows are kept before purge.
	PublishedRetention time.Duration
	// PurgeInterval is how often published rows are purged.
	PurgeInterval time.Duration
}

// Load resolves configuration from the environment.
func Load() Config {
	return Config{
		DatabaseURL:  shared.PostgresDSN(),
		KafkaBrokers: shared.KafkaBrokers(),
		KafkaGroupID: shared.Env("KAFKA_GROUP_ID", "ledger-consumers"),
		MetricsPort:  shared.Env("CONSUMERS_METRICS_PORT", "9108"),
		LogPretty:    shared.Env("LOG_PRETTY", "") != "",

		OTLPEndpoint: shared.OTLPEndpoint(),
		Version:      shared.Version(),

		// RELAY_INTERVAL_SECONDS is still honoured; EnvDuration accepts a bare
		// integer as seconds as well as a Go duration.
		RelayInterval:      shared.EnvDuration("RELAY_INTERVAL", relayIntervalLegacy()),
		RelayBatchSize:     shared.EnvInt("RELAY_BATCH_SIZE", 100),
		RelayLease:         shared.EnvDuration("RELAY_LEASE", 30*time.Second),
		PublishedRetention: shared.EnvDuration("OUTBOX_PUBLISHED_RETENTION", 24*time.Hour),
		PurgeInterval:      shared.EnvDuration("OUTBOX_PURGE_INTERVAL", time.Hour),
	}
}

// relayIntervalLegacy preserves the original RELAY_INTERVAL_SECONDS variable so
// existing deployments keep working.
func relayIntervalLegacy() time.Duration {
	return shared.EnvDuration("RELAY_INTERVAL_SECONDS", time.Second)
}

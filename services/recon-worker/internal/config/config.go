package config

import (
	"time"

	shared "github.com/prasham/ledger/internal/config"
)

// Config holds recon-worker runtime settings.
type Config struct {
	MetricsPort string
	DSN         string
	MinIO       shared.MinIO
	Interval    time.Duration
	// MaxFindings bounds how many discrepancies one report carries, so systemic
	// drift cannot produce an unbounded query result and report.
	MaxFindings int

	OTLPEndpoint string
	Version      string
}

// Load resolves configuration from the environment.
func Load() Config {
	minioCfg := shared.MinIOConfig("recon-reports")
	// Allow the legacy bucket variable to keep working.
	minioCfg.Bucket = shared.Env("MINIO_RECON_BUCKET", minioCfg.Bucket)

	return Config{
		MetricsPort: shared.Env("RECON_METRICS_PORT", "9106"),
		DSN:         shared.PostgresDSN(),
		MinIO:       minioCfg,
		// RECON_INTERVAL_SECONDS still works: EnvDuration accepts a bare integer
		// as seconds as well as a Go duration string.
		Interval:    shared.EnvDuration("RECON_INTERVAL_SECONDS", time.Minute),
		MaxFindings: shared.EnvInt("RECON_MAX_FINDINGS", 500),

		OTLPEndpoint: shared.OTLPEndpoint(),
		Version:      shared.Version(),
	}
}

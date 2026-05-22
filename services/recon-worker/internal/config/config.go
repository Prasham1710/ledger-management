package config

import (
	"os"
	"time"
)

type Config struct {
	MetricsPort    string
	DSN            string
	MinIOEndpoint  string
	MinIOAccessKey string
	MinIOSecretKey string
	MinIOBucket    string
	Interval       time.Duration
}

func Load() Config {
	interval := 60 * time.Second
	if v := os.Getenv("RECON_INTERVAL_SECONDS"); v != "" {
		if d, err := time.ParseDuration(v + "s"); err == nil {
			interval = d
		}
	}
	return Config{
		MetricsPort:    env("RECON_METRICS_PORT", "9106"),
		DSN:            buildDSN(),
		MinIOEndpoint:  env("MINIO_ENDPOINT", "localhost:9000"),
		MinIOAccessKey: env("MINIO_ACCESS_KEY", "minioadmin"),
		MinIOSecretKey: env("MINIO_SECRET_KEY", "minioadmin"),
		MinIOBucket:    env("MINIO_RECON_BUCKET", "recon-reports"),
		Interval:       interval,
	}
}

func buildDSN() string {
	if v := os.Getenv("POSTGRES_DSN"); v != "" {
		return v
	}
	return "host=" + env("POSTGRES_HOST", "localhost") +
		" port=" + env("POSTGRES_PORT", "5432") +
		" user=" + env("POSTGRES_USER", "ledger") +
		" password=" + env("POSTGRES_PASSWORD", "ledger") +
		" dbname=" + env("POSTGRES_DB", "ledger") +
		" sslmode=disable"
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

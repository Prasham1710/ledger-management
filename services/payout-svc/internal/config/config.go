package config

import "os"

type Config struct {
	GRPCPort       string
	MetricsPort    string
	DSN            string
	RedisAddr      string
	LedgerAddr     string
	AccountAddr    string
	MinIOEndpoint  string
	MinIOAccessKey string
	MinIOSecretKey string
	MinIOBucket    string
}

func Load() Config {
	return Config{
		GRPCPort:       env("PAYOUT_GRPC_PORT", "50055"),
		MetricsPort:    env("PAYOUT_METRICS_PORT", "9105"),
		DSN:            buildDSN(),
		RedisAddr:      env("REDIS_ADDR", "localhost:6379"),
		LedgerAddr:     env("LEDGER_GRPC_ADDR", "localhost:50051"),
		AccountAddr:    env("ACCOUNT_GRPC_ADDR", "localhost:50052"),
		MinIOEndpoint:  env("MINIO_ENDPOINT", "localhost:9000"),
		MinIOAccessKey: env("MINIO_ACCESS_KEY", "minioadmin"),
		MinIOSecretKey: env("MINIO_SECRET_KEY", "minioadmin"),
		MinIOBucket:    env("MINIO_SETTLEMENT_BUCKET", "settlement-files"),
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

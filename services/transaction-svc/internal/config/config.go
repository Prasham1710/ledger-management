package config

import "os"

type Config struct {
	GRPCPort    string
	MetricsPort string
	DSN         string
	RedisAddr   string
	LedgerAddr  string
	AccountAddr string
}

func Load() Config {
	return Config{
		GRPCPort:    env("TRANSACTION_GRPC_PORT", "50054"),
		MetricsPort: env("TRANSACTION_METRICS_PORT", "9104"),
		DSN:         buildDSN(),
		RedisAddr:   env("REDIS_ADDR", "localhost:6379"),
		LedgerAddr:  env("LEDGER_GRPC_ADDR", "localhost:50051"),
		AccountAddr: env("ACCOUNT_GRPC_ADDR", "localhost:50052"),
	}
}

func buildDSN() string {
	if v := os.Getenv("POSTGRES_DSN"); v != "" {
		return v
	}
	host := env("POSTGRES_HOST", "localhost")
	port := env("POSTGRES_PORT", "5432")
	user := env("POSTGRES_USER", "ledger")
	pass := env("POSTGRES_PASSWORD", "ledger")
	db   := env("POSTGRES_DB", "ledger")
	return "host=" + host + " port=" + port + " user=" + user +
		" password=" + pass + " dbname=" + db + " sslmode=disable"
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

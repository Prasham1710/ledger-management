package config

import (
	"fmt"
	"os"
	"strconv"
)

type Config struct {
	GRPCPort     int
	MetricsPort  int
	PostgresDSN  string
	RedisAddr    string
	OTLPEndpoint string
	QuoteTTLSec  int // how long a quote is valid before it expires
}

func Load() Config {
	return Config{
		GRPCPort:     envInt("FX_GRPC_PORT", 50053),
		MetricsPort:  envInt("FX_METRICS_PORT", 9103),
		PostgresDSN:  buildDSN(),
		RedisAddr:    envStr("REDIS_ADDR", "localhost:6379"),
		OTLPEndpoint: envStr("OTEL_EXPORTER_OTLP_ENDPOINT", "http://localhost:4317"),
		QuoteTTLSec:  envInt("FX_QUOTE_TTL_SEC", 30),
	}
}

func buildDSN() string {
	host := envStr("POSTGRES_HOST", "localhost")
	port := envStr("POSTGRES_PORT", "5432")
	user := envStr("POSTGRES_USER", "ledger")
	pass := envStr("POSTGRES_PASSWORD", "ledger")
	db   := envStr("POSTGRES_DB", "ledger")
	return fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable", user, pass, host, port, db)
}

func envStr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

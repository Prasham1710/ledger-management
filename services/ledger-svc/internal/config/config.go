package config

import (
	"fmt"
	"os"
	"strconv"
)

type Config struct {
	GRPCPort    int
	MetricsPort int
	PostgresDSN string
	RedisAddr   string
	OTLPEndpoint string
}

func Load() Config {
	return Config{
		GRPCPort:     envInt("LEDGER_GRPC_PORT", 50051),
		MetricsPort:  envInt("LEDGER_METRICS_PORT", 9101),
		PostgresDSN:  buildDSN(),
		RedisAddr:    getEnv("REDIS_ADDR", "localhost:6379"),
		OTLPEndpoint: getEnv("OTEL_EXPORTER_OTLP_ENDPOINT", ""),
	}
}

func buildDSN() string {
	host := getEnv("POSTGRES_HOST", "localhost")
	port := getEnv("POSTGRES_PORT", "5432")
	user := getEnv("POSTGRES_USER", "ledger")
	pass := getEnv("POSTGRES_PASSWORD", "ledger")
	dbname := getEnv("POSTGRES_DB", "ledger")
	return fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		user, pass, host, port, dbname)
}

func getEnv(key, def string) string {
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

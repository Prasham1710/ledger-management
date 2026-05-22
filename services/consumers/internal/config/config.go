package config

import "os"

type Config struct {
	DatabaseURL     string
	KafkaBrokers    string
	KafkaGroupID    string
	MetricsPort     string
	RelayInterval   string
	LogPretty       bool
}

func Load() Config {
	return Config{
		DatabaseURL:   env("DATABASE_URL", "postgres://ledger:ledger@localhost:5432/ledger?sslmode=disable"),
		KafkaBrokers:  env("KAFKA_BROKERS", "localhost:19092"),
		KafkaGroupID:  env("KAFKA_GROUP_ID", "ledger-consumers"),
		MetricsPort:   env("CONSUMERS_METRICS_PORT", "9108"),
		RelayInterval: env("RELAY_INTERVAL_SECONDS", "5"),
		LogPretty:     os.Getenv("LOG_PRETTY") != "",
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

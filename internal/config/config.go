// Package config resolves environment configuration uniformly across services.
//
// It exists because each service previously read its own set of variable names.
// Five services read POSTGRES_HOST/PORT/USER/PASSWORD/DB, three also accepted
// POSTGRES_DSN, one read DATABASE_URL, and the Helm chart injected POSTGRES_URL
// and REDIS_URL — names no service read. The result was that every service
// deployed by the chart fell back to localhost and died on its startup ping.
//
// PostgresDSN and RedisOptions accept every name in use, in a documented
// precedence order, so a deployment cannot silently miss.
package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Env returns the value of key, or def when unset or empty.
func Env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// EnvInt returns key parsed as an int, or def when unset or unparseable.
func EnvInt(key string, def int) int {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// EnvBool returns key parsed as a bool, or def when unset or unparseable.
// Accepts the usual spellings: 1/0, t/f, true/false, yes/no, on/off.
func EnvBool(key string, def bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	switch v {
	case "1", "t", "true", "yes", "y", "on":
		return true
	case "0", "f", "false", "no", "n", "off":
		return false
	default:
		return def
	}
}

// EnvDuration returns key parsed as a Go duration, or def when unset or
// unparseable. A bare integer is interpreted as seconds for compatibility with
// the *_SECONDS variables already in use.
func EnvDuration(key string, def time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	if d, err := time.ParseDuration(v); err == nil {
		return d
	}
	if n, err := strconv.Atoi(v); err == nil {
		return time.Duration(n) * time.Second
	}
	return def
}

// EnvList returns key split on commas with empty entries removed.
func EnvList(key string, def []string) []string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return def
	}
	return out
}

// Version returns the deployed build identifier, for trace and metric
// attribution. Set SERVICE_VERSION from the image tag at deploy time.
func Version() string { return Env("SERVICE_VERSION", "dev") }

// PostgresDSN resolves a Postgres connection string, preferring a complete URL
// when one is supplied. Precedence, highest first:
//
//  1. DATABASE_URL   — read by consumers, set by the Helm chart
//  2. POSTGRES_URL   — set by the Helm chart secret
//  3. POSTGRES_DSN   — accepted by several services as an override
//  4. POSTGRES_HOST / PORT / USER / PASSWORD / DB components
//
// When the DSN is assembled from components, POSTGRES_SSLMODE controls TLS and
// defaults to "disable" for local development. Deployments handling real money
// must set it to "require" or stronger; the previous code hardcoded "disable"
// with no way to override it.
func PostgresDSN() string {
	for _, key := range []string{"DATABASE_URL", "POSTGRES_URL", "POSTGRES_DSN"} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
	}

	host := Env("POSTGRES_HOST", "localhost")
	port := Env("POSTGRES_PORT", "5432")
	user := Env("POSTGRES_USER", "ledger")
	pass := Env("POSTGRES_PASSWORD", "ledger")
	name := Env("POSTGRES_DB", "ledger")
	sslMode := Env("POSTGRES_SSLMODE", "disable")

	dsn := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(user, pass),
		Host:   host + ":" + port,
		Path:   "/" + name,
	}
	q := dsn.Query()
	q.Set("sslmode", sslMode)
	// search_path keeps unqualified table names resolving to the owning schema
	// even when the role default changes.
	if schema := Env("POSTGRES_SEARCH_PATH", ""); schema != "" {
		q.Set("search_path", schema)
	}
	dsn.RawQuery = q.Encode()
	return dsn.String()
}

// RedisOptions resolves Redis connection settings. Precedence, highest first:
//
//  1. REDIS_URL  — a full redis:// or rediss:// URL, set by the Helm chart
//  2. REDIS_ADDR — a bare host:port, used by every service's local config
//
// Returning *redis.Options rather than a bare address means credentials, a
// database index, and TLS from a rediss:// URL are all honoured. The previous
// code read only REDIS_ADDR, so the chart's REDIS_URL was ignored entirely and
// every service tried to reach localhost.
func RedisOptions() (*redis.Options, error) {
	if raw := strings.TrimSpace(os.Getenv("REDIS_URL")); raw != "" {
		opts, err := redis.ParseURL(raw)
		if err != nil {
			return nil, fmt.Errorf("config: parse REDIS_URL: %w", err)
		}
		return opts, nil
	}
	return &redis.Options{
		Addr:     Env("REDIS_ADDR", "localhost:6379"),
		Password: Env("REDIS_PASSWORD", ""),
		DB:       EnvInt("REDIS_DB", 0),
	}, nil
}

// OTLPEndpoint returns the OpenTelemetry collector endpoint, or "" when tracing
// should be disabled.
func OTLPEndpoint() string {
	return Env("OTEL_EXPORTER_OTLP_ENDPOINT", Env("OTLP_ENDPOINT", ""))
}

// KafkaBrokers returns the Kafka/Redpanda seed brokers.
func KafkaBrokers() []string {
	return EnvList("KAFKA_BROKERS", EnvList("REDPANDA_BROKERS", []string{"localhost:19092"}))
}

// MinIO holds object storage settings.
type MinIO struct {
	Endpoint  string
	AccessKey string
	SecretKey string
	UseSSL    bool
	Bucket    string
}

// MinIOConfig resolves object storage settings. UseSSL is honoured rather than
// hardcoded to false, so a deployment can actually encrypt object traffic.
func MinIOConfig(defaultBucket string) MinIO {
	return MinIO{
		Endpoint:  Env("MINIO_ENDPOINT", "localhost:9000"),
		AccessKey: Env("MINIO_ACCESS_KEY", Env("MINIO_ROOT_USER", "minioadmin")),
		SecretKey: Env("MINIO_SECRET_KEY", Env("MINIO_ROOT_PASSWORD", "minioadmin")),
		UseSSL:    EnvBool("MINIO_USE_SSL", false),
		Bucket:    Env("MINIO_BUCKET", defaultBucket),
	}
}

// IsProduction reports whether the service believes it is running in a
// production environment. Used to refuse insecure defaults.
func IsProduction() bool {
	switch strings.ToLower(Env("APP_ENV", "development")) {
	case "production", "prod":
		return true
	default:
		return false
	}
}

// Package logging configures a structured JSON zerolog logger for every service.
// All log lines carry at minimum: timestamp, level, service name.
package logging

import (
	"io"
	"os"
	"time"

	"github.com/rs/zerolog"
)

// Config holds logger bootstrap options.
type Config struct {
	// ServiceName is attached to every log line as the "service" field.
	ServiceName string
	// Level controls minimum log severity.
	Level zerolog.Level
	// Pretty enables human-readable console output; set false in production.
	Pretty bool
}

// New returns a zerolog.Logger configured for the given service.
func New(cfg Config) zerolog.Logger {
	zerolog.TimeFieldFormat = time.RFC3339Nano

	var w io.Writer = os.Stdout
	if cfg.Pretty {
		w = zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339}
	}

	return zerolog.New(w).
		Level(cfg.Level).
		With().
		Timestamp().
		Str("service", cfg.ServiceName).
		Logger()
}

// WithRequestID returns a child logger with the request_id field set.
func WithRequestID(log zerolog.Logger, requestID string) zerolog.Logger {
	return log.With().Str("request_id", requestID).Logger()
}

// WithTraceID returns a child logger with trace_id and span_id fields set.
// Call this inside HTTP/gRPC handlers after extracting span context.
func WithTraceID(log zerolog.Logger, traceID, spanID string) zerolog.Logger {
	return log.With().Str("trace_id", traceID).Str("span_id", spanID).Logger()
}

// WithAccountID returns a child logger with account_id attached.
func WithAccountID(log zerolog.Logger, accountID string) zerolog.Logger {
	return log.With().Str("account_id", accountID).Logger()
}

package logging_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/prasham/ledger/internal/logging"
)

func captureLog(f func(zerolog.Logger)) map[string]any {
	var buf bytes.Buffer
	log := zerolog.New(&buf).With().Str("service", "test-svc").Logger()
	f(log)
	var out map[string]any
	_ = json.Unmarshal(buf.Bytes(), &out)
	return out
}

func TestNew_ServiceFieldPresent(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	cfg := logging.Config{
		ServiceName: "ledger-svc",
		Level:       zerolog.InfoLevel,
		Pretty:      false,
	}
	log := logging.New(cfg)
	log = log.Output(&buf)
	log.Info().Msg("hello")

	var out map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &out))
	assert.Equal(t, "ledger-svc", out["service"])
	assert.Equal(t, "info", out["level"])
	assert.Equal(t, "hello", out["message"])
}

func TestWithRequestID(t *testing.T) {
	t.Parallel()

	out := captureLog(func(log zerolog.Logger) {
		l := logging.WithRequestID(log, "req-abc")
		l.Info().Msg("test")
	})
	assert.Equal(t, "req-abc", out["request_id"])
}

func TestWithTraceID(t *testing.T) {
	t.Parallel()

	out := captureLog(func(log zerolog.Logger) {
		l := logging.WithTraceID(log, "trace-111", "span-222")
		l.Info().Msg("test")
	})
	assert.Equal(t, "trace-111", out["trace_id"])
	assert.Equal(t, "span-222", out["span_id"])
}

func TestWithAccountID(t *testing.T) {
	t.Parallel()

	out := captureLog(func(log zerolog.Logger) {
		l := logging.WithAccountID(log, "acc-999")
		l.Info().Msg("test")
	})
	assert.Equal(t, "acc-999", out["account_id"])
}

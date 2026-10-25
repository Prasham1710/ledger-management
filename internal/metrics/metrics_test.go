package metrics_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/prasham/ledger/internal/metrics"
)

func TestHandler_ExposesGoRuntime(t *testing.T) {
	r := metrics.New()
	w := httptest.NewRecorder()
	r.Handler().ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", nil))
	assert.Equal(t, 200, w.Code)
	assert.True(t, strings.Contains(w.Body.String(), "go_goroutines"))
}

func TestNewCounterVec(t *testing.T) {
	r := metrics.New()
	c := r.NewCounterVec(prometheus.CounterOpts{
		Name: "test_requests_total",
		Help: "total requests",
	}, []string{"method"})
	require.NotNil(t, c)
	c.WithLabelValues("GET").Inc()
}

func TestNewHistogramVec(t *testing.T) {
	r := metrics.New()
	h := r.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "test_duration_seconds",
		Help:    "request duration",
		Buckets: prometheus.DefBuckets,
	}, []string{"route"})
	require.NotNil(t, h)
	h.WithLabelValues("/pay").Observe(0.05)
}

func TestNewGaugeVec(t *testing.T) {
	r := metrics.New()
	g := r.NewGaugeVec(prometheus.GaugeOpts{
		Name: "test_queue_depth",
		Help: "current queue depth",
	}, []string{"topic"})
	require.NotNil(t, g)
	g.WithLabelValues("payments.payment.completed").Set(42)
}

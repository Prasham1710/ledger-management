package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Registry is a scoped Prometheus registry pre-loaded with Go runtime and
// process collectors. Each service creates one and wires it to /metrics.
type Registry struct {
	reg *prometheus.Registry
}

// New returns a Registry with Go runtime and process metrics already registered.
func New() *Registry {
	r := prometheus.NewRegistry()
	r.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return &Registry{reg: r}
}

// Handler returns an HTTP handler suitable for mounting at /metrics.
func (r *Registry) Handler() http.Handler {
	return promhttp.HandlerFor(r.reg, promhttp.HandlerOpts{Registry: r.reg})
}

// MustRegister adds collectors to this registry. Panics on conflict.
func (r *Registry) MustRegister(cs ...prometheus.Collector) {
	r.reg.MustRegister(cs...)
}

// NewCounterVec creates, registers, and returns a CounterVec.
func (r *Registry) NewCounterVec(opts prometheus.CounterOpts, labels []string) *prometheus.CounterVec {
	c := prometheus.NewCounterVec(opts, labels)
	r.reg.MustRegister(c)
	return c
}

// NewHistogramVec creates, registers, and returns a HistogramVec.
func (r *Registry) NewHistogramVec(opts prometheus.HistogramOpts, labels []string) *prometheus.HistogramVec {
	h := prometheus.NewHistogramVec(opts, labels)
	r.reg.MustRegister(h)
	return h
}

// NewGaugeVec creates, registers, and returns a GaugeVec.
func (r *Registry) NewGaugeVec(opts prometheus.GaugeOpts, labels []string) *prometheus.GaugeVec {
	g := prometheus.NewGaugeVec(opts, labels)
	r.reg.MustRegister(g)
	return g
}

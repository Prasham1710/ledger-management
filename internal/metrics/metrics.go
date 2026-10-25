// Package metrics provides a scoped Prometheus registry and the metric sets each
// service registers.
//
// The metric definitions here exist because /metrics previously served only Go
// runtime and process collectors: the registry was created and wired up, but no
// service ever registered a single application metric, so nothing about RPCs,
// sagas, the outbox, or reconciliation was observable.
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

// NewCounter creates, registers, and returns an unlabelled Counter.
func (r *Registry) NewCounter(opts prometheus.CounterOpts) prometheus.Counter {
	c := prometheus.NewCounter(opts)
	r.reg.MustRegister(c)
	return c
}

// NewGauge creates, registers, and returns an unlabelled Gauge.
func (r *Registry) NewGauge(opts prometheus.GaugeOpts) prometheus.Gauge {
	g := prometheus.NewGauge(opts)
	r.reg.MustRegister(g)
	return g
}

// RPCMetrics is the standard set of gRPC server metrics, recorded by
// grpcmw.MetricsUnaryInterceptor. Every service registers these so dashboards
// and alerts can be written once against a uniform surface.
type RPCMetrics struct {
	Requests *prometheus.CounterVec
	Duration *prometheus.HistogramVec
	InFlight *prometheus.GaugeVec
}

// NewRPCMetrics registers the standard gRPC metrics on r.
func NewRPCMetrics(r *Registry) *RPCMetrics {
	return &RPCMetrics{
		Requests: r.NewCounterVec(prometheus.CounterOpts{
			Name: "grpc_server_requests_total",
			Help: "Total gRPC requests handled, by method and response code.",
		}, []string{"method", "code"}),
		Duration: r.NewHistogramVec(prometheus.HistogramOpts{
			Name: "grpc_server_request_duration_seconds",
			Help: "gRPC handler latency, by method and response code.",
			// Buckets span sub-millisecond cache hits to multi-second saga steps.
			Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		}, []string{"method", "code"}),
		InFlight: r.NewGaugeVec(prometheus.GaugeOpts{
			Name: "grpc_server_in_flight_requests",
			Help: "gRPC requests currently being handled, by method.",
		}, []string{"method"}),
	}
}

// SagaMetrics instruments multi-step money movement. Compensation counters are
// the ones that matter operationally: a rising compensation rate means money is
// moving and then being undone, and a rising failure rate on the compensating
// step itself means money is stuck.
type SagaMetrics struct {
	// StepsTotal counts saga steps by saga name, step name, and outcome.
	StepsTotal *prometheus.CounterVec
	// StepDuration measures per-step latency.
	StepDuration *prometheus.HistogramVec
	// CompensationsTotal counts compensating actions by saga, step, and outcome.
	CompensationsTotal *prometheus.CounterVec
	// StuckTotal counts sagas found abandoned mid-flight by the recovery worker.
	StuckTotal *prometheus.CounterVec
}

// NewSagaMetrics registers saga instrumentation on r.
func NewSagaMetrics(r *Registry) *SagaMetrics {
	return &SagaMetrics{
		StepsTotal: r.NewCounterVec(prometheus.CounterOpts{
			Name: "saga_steps_total",
			Help: "Saga steps executed, by saga, step, and outcome.",
		}, []string{"saga", "step", "outcome"}),
		StepDuration: r.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "saga_step_duration_seconds",
			Help:    "Saga step latency, by saga and step.",
			Buckets: []float64{.01, .05, .1, .25, .5, 1, 2.5, 5, 10, 30},
		}, []string{"saga", "step"}),
		CompensationsTotal: r.NewCounterVec(prometheus.CounterOpts{
			Name: "saga_compensations_total",
			Help: "Compensating actions attempted, by saga, step, and outcome. A failed compensation means money is stranded and needs manual intervention.",
		}, []string{"saga", "step", "outcome"}),
		StuckTotal: r.NewCounterVec(prometheus.CounterOpts{
			Name: "saga_stuck_total",
			Help: "Sagas found abandoned mid-flight by the recovery worker, by saga and recovery outcome.",
		}, []string{"saga", "outcome"}),
	}
}

// OutboxMetrics instruments the transactional outbox relay. Backlog is the
// leading indicator: if it grows without bound, events are not reaching Kafka.
type OutboxMetrics struct {
	PublishedTotal *prometheus.CounterVec
	FailuresTotal  *prometheus.CounterVec
	Backlog        *prometheus.GaugeVec
	OldestAgeSecs  *prometheus.GaugeVec
}

// NewOutboxMetrics registers outbox relay instrumentation on r.
func NewOutboxMetrics(r *Registry) *OutboxMetrics {
	return &OutboxMetrics{
		PublishedTotal: r.NewCounterVec(prometheus.CounterOpts{
			Name: "outbox_published_total",
			Help: "Outbox messages successfully published to Kafka, by schema and event type.",
		}, []string{"schema", "event_type"}),
		FailuresTotal: r.NewCounterVec(prometheus.CounterOpts{
			Name: "outbox_publish_failures_total",
			Help: "Outbox publish attempts that failed, by schema and reason. Rows are retained and retried.",
		}, []string{"schema", "reason"}),
		Backlog: r.NewGaugeVec(prometheus.GaugeOpts{
			Name: "outbox_backlog_messages",
			Help: "Unpublished outbox messages, by schema.",
		}, []string{"schema"}),
		OldestAgeSecs: r.NewGaugeVec(prometheus.GaugeOpts{
			Name: "outbox_oldest_unpublished_age_seconds",
			Help: "Age of the oldest unpublished outbox message, by schema. Alert on sustained growth.",
		}, []string{"schema"}),
	}
}

// ReconMetrics instruments the reconciliation worker. Drift is the platform's
// single most important correctness signal: a non-zero, non-transient drift
// count means recorded balances and the ledger disagree about real money.
type ReconMetrics struct {
	RunsTotal       *prometheus.CounterVec
	DriftAccounts   prometheus.Gauge
	DriftAbsTotal   prometheus.Gauge
	LastRunTimestmp prometheus.Gauge
	RunDuration     prometheus.Histogram
}

// NewReconMetrics registers reconciliation instrumentation on r.
func NewReconMetrics(r *Registry) *ReconMetrics {
	return &ReconMetrics{
		RunsTotal: r.NewCounterVec(prometheus.CounterOpts{
			Name: "recon_runs_total",
			Help: "Reconciliation runs, by outcome.",
		}, []string{"outcome"}),
		DriftAccounts: r.NewGauge(prometheus.GaugeOpts{
			Name: "recon_drift_accounts",
			Help: "Accounts whose recorded balance disagrees with the ledger projection. Should be 0.",
		}),
		DriftAbsTotal: r.NewGauge(prometheus.GaugeOpts{
			Name: "recon_drift_absolute_total",
			Help: "Sum of absolute drift across all accounts, in each account's own currency units.",
		}),
		LastRunTimestmp: r.NewGauge(prometheus.GaugeOpts{
			Name: "recon_last_run_timestamp_seconds",
			Help: "Unix timestamp of the last completed reconciliation run. Alert if this stops advancing.",
		}),
		RunDuration: r.NewHistogram(prometheus.HistogramOpts{
			Name:    "recon_run_duration_seconds",
			Help:    "Reconciliation run duration.",
			Buckets: []float64{.1, .5, 1, 5, 10, 30, 60, 300},
		}),
	}
}

// NewHistogram creates, registers, and returns an unlabelled Histogram.
func (r *Registry) NewHistogram(opts prometheus.HistogramOpts) prometheus.Histogram {
	h := prometheus.NewHistogram(opts)
	r.reg.MustRegister(h)
	return h
}

// ConsumerMetrics instruments Kafka consumption. Dropped messages must always
// be visible: silently swallowing a handler failure is how audit records were
// previously lost.
type ConsumerMetrics struct {
	ConsumedTotal  *prometheus.CounterVec
	HandlerFailure *prometheus.CounterVec
	DuplicateTotal *prometheus.CounterVec
	Lag            *prometheus.GaugeVec
}

// NewConsumerMetrics registers Kafka consumer instrumentation on r.
func NewConsumerMetrics(r *Registry) *ConsumerMetrics {
	return &ConsumerMetrics{
		ConsumedTotal: r.NewCounterVec(prometheus.CounterOpts{
			Name: "consumer_messages_total",
			Help: "Kafka messages processed, by topic and outcome.",
		}, []string{"topic", "outcome"}),
		HandlerFailure: r.NewCounterVec(prometheus.CounterOpts{
			Name: "consumer_handler_failures_total",
			Help: "Handler failures, by topic and reason. Offsets are not advanced past these.",
		}, []string{"topic", "reason"}),
		DuplicateTotal: r.NewCounterVec(prometheus.CounterOpts{
			Name: "consumer_duplicates_suppressed_total",
			Help: "Redelivered messages suppressed by the dedup key, by topic. Expected to be non-zero: delivery is at-least-once.",
		}, []string{"topic"}),
		Lag: r.NewGaugeVec(prometheus.GaugeOpts{
			Name: "consumer_group_lag_records",
			Help: "Consumer group lag, by topic and partition.",
		}, []string{"topic", "partition"}),
	}
}

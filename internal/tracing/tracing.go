// Package tracing bootstraps OpenTelemetry tracing for every platform service.
//
// Services call Init with a SpanProcessor of their choice. Production code
// passes a BatchSpanProcessor backed by an OTLP/gRPC exporter; tests pass
// a SimpleSpanProcessor backed by tracetest.NewInMemoryExporter.
//
// Keeping the exporter and processor choices outside this package avoids
// pulling gRPC and genproto into the shared internal module.
//
// Example (production):
//
//	exp, _ := otlptracegrpc.New(ctx, otlptracegrpc.WithEndpoint("tempo:4317"), ...)
//	proc := sdktrace.NewBatchSpanProcessor(exp)
//	shutdown, _ := tracing.Init(ctx, tracing.Config{...}, proc)
//	defer shutdown(ctx)
//
// Example (tests):
//
//	mem := tracetest.NewInMemoryExporter()
//	proc := sdktrace.NewSimpleSpanProcessor(mem)
//	shutdown, _ := tracing.Init(ctx, tracing.Config{...}, proc)
package tracing

import (
	"context"
	"fmt"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	"go.opentelemetry.io/otel/trace"
)

// Config holds OpenTelemetry bootstrap parameters.
type Config struct {
	// ServiceName identifies this service in Tempo (e.g. "ledger-svc").
	ServiceName string
	// ServiceVersion is the deployed image tag (e.g. "v1.2.3").
	ServiceVersion string
}

// Init configures the global TracerProvider with the given SpanProcessor.
// Returns a shutdown function that flushes all pending spans.
// Callers must invoke shutdown before the process exits.
func Init(ctx context.Context, cfg Config, processor sdktrace.SpanProcessor) (func(context.Context) error, error) {
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(cfg.ServiceName),
			semconv.ServiceVersion(cfg.ServiceVersion),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("tracing.Init: build resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(processor),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return tp.Shutdown, nil
}

// Setup is the one-call bootstrap services use at startup. It builds an
// OTLP/gRPC exporter for endpoint, wraps it in a BatchSpanProcessor, and
// installs the global TracerProvider.
//
// If endpoint is empty, tracing is disabled and the returned shutdown function
// is a no-op — so a service runs unchanged when no collector is configured.
//
// This exists because Init alone requires the caller to assemble an exporter and
// processor, and every service skipped that step: OTLP endpoints were read from
// the environment, Tempo and the collector were deployed, and not one span was
// ever produced. Prefer Setup unless you need a custom processor.
func Setup(ctx context.Context, cfg Config, endpoint string) (func(context.Context) error, error) {
	if endpoint == "" {
		return func(context.Context) error { return nil }, nil
	}

	opts := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(stripScheme(endpoint))}
	// Collectors in this platform are reached over the cluster-internal network
	// without TLS. Enable it explicitly by passing an https:// endpoint.
	if !strings.HasPrefix(endpoint, "https://") {
		opts = append(opts, otlptracegrpc.WithInsecure())
	}

	exp, err := otlptracegrpc.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("tracing.Setup: create OTLP exporter: %w", err)
	}
	return Init(ctx, cfg, sdktrace.NewBatchSpanProcessor(exp))
}

// stripScheme removes a URL scheme so callers may pass either "host:4317" or
// "http://host:4317"; the OTLP gRPC exporter wants the bare authority.
func stripScheme(endpoint string) string {
	for _, prefix := range []string{"http://", "https://", "grpc://"} {
		if after, ok := strings.CutPrefix(endpoint, prefix); ok {
			return after
		}
	}
	return endpoint
}

// Tracer returns a named tracer from the global provider.
// Pass the importing package path as name (e.g. "ledger-svc/internal/service").
func Tracer(name string) trace.Tracer {
	return otel.Tracer(name)
}

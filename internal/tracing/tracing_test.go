// Tests for the tracing package do NOT call t.Parallel():
// Init writes to a process-global TracerProvider; concurrent calls race on it.
package tracing_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/prasham/ledger/internal/tracing"
)

// syncProcessor wraps InMemoryExporter with a SimpleSpanProcessor so spans
// are exported synchronously — no batching, no timing dependencies.
func newSyncExporter() (*tracetest.InMemoryExporter, sdktrace.SpanProcessor) {
	exp := tracetest.NewInMemoryExporter()
	return exp, sdktrace.NewSimpleSpanProcessor(exp)
}

func TestInit_SpanReachesExporter(t *testing.T) {
	exp, proc := newSyncExporter()

	shutdown, err := tracing.Init(context.Background(), tracing.Config{
		ServiceName:    "test-svc",
		ServiceVersion: "v0.0.1",
	}, proc)
	require.NoError(t, err)
	t.Cleanup(func() { _ = shutdown(context.Background()) })

	_, span := tracing.Tracer("test-tracer").Start(context.Background(), "test-span")
	span.End()

	spans := exp.GetSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "test-span", spans[0].Name)
}

func TestInit_MultipleSpansAllExported(t *testing.T) {
	exp, proc := newSyncExporter()

	shutdown, err := tracing.Init(context.Background(), tracing.Config{
		ServiceName:    "flush-svc",
		ServiceVersion: "v0.0.0",
	}, proc)
	require.NoError(t, err)
	t.Cleanup(func() { _ = shutdown(context.Background()) })

	tr := tracing.Tracer("flush-tracer")
	for range 5 {
		_, s := tr.Start(context.Background(), "span")
		s.End()
	}

	assert.Len(t, exp.GetSpans(), 5)
}

func TestTracer_NoopBeforeInit(t *testing.T) {
	// otel's global no-op provider returns a non-nil tracer that does not panic.
	tr := tracing.Tracer("noop")
	require.NotNil(t, tr)

	ctx, span := tr.Start(context.Background(), "noop-span")
	span.End()
	assert.NotNil(t, ctx)
}

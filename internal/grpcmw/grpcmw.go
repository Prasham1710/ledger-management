// Package grpcmw provides the server and client interceptors every service in
// the platform installs.
//
// Services previously constructed grpc.NewServer() with no interceptors at all,
// which meant: a nil dereference in any handler killed the whole process and
// every in-flight request with it; no RPC was traced despite a fully deployed
// collector; no RPC latency or error was measured despite a scraped /metrics
// endpoint; and a slow database could pin a handler indefinitely because
// nothing imposed a deadline.
//
// Install with:
//
//	srv := grpc.NewServer(grpcmw.ServerOptions(deps)...)
//
// and dial peers with:
//
//	grpc.NewClient(addr, grpcmw.ClientOptions()...)
package grpcmw

import (
	"context"
	"errors"
	"runtime/debug"
	"time"

	"github.com/rs/zerolog"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/prasham/ledger/internal/metrics"
)

// DefaultTimeout bounds any RPC whose caller supplied no deadline.
const DefaultTimeout = 15 * time.Second

// Deps carries what the interceptors need from the hosting service.
type Deps struct {
	// Log receives one structured line per RPC, and panic reports.
	Log zerolog.Logger
	// Metrics records RPC counters and latency. May be nil to disable.
	Metrics *metrics.RPCMetrics
	// Timeout overrides DefaultTimeout when non-zero.
	Timeout time.Duration
}

// ServerOptions returns the full server interceptor chain plus tracing.
//
// Order matters. Outermost first:
//  1. tracing   — so every later stage is inside the span
//  2. recovery  — converts a panic into an error instead of a dead process
//  3. timeout   — imposes a deadline before the handler can block
//  4. metrics   — measures what the handler actually did
//  5. logging   — reports the final outcome, including recovered panics
func ServerOptions(deps Deps) []grpc.ServerOption {
	return []grpc.ServerOption{
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
		grpc.ChainUnaryInterceptor(
			RecoveryUnaryInterceptor(deps.Log),
			TimeoutUnaryInterceptor(deps.timeout()),
			MetricsUnaryInterceptor(deps.Metrics),
			LoggingUnaryInterceptor(deps.Log),
		),
	}
}

// ClientOptions returns dial options that propagate trace context to peers, so
// a saga's spans join up across services instead of appearing as unrelated
// fragments.
func ClientOptions() []grpc.DialOption {
	return []grpc.DialOption{
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	}
}

func (d Deps) timeout() time.Duration {
	if d.Timeout > 0 {
		return d.Timeout
	}
	return DefaultTimeout
}

// RecoveryUnaryInterceptor converts a panicking handler into an Internal error.
//
// Without this, a single nil dereference in one handler terminates the process
// and fails every concurrent request — in a payments service, potentially
// mid-saga. The stack trace is logged; the client is told nothing beyond
// "internal error".
func RecoveryUnaryInterceptor(log zerolog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if r := recover(); r != nil {
				log.Error().
					Str("method", info.FullMethod).
					Interface("panic", r).
					Bytes("stack", debug.Stack()).
					Msg("handler panicked — recovered")
				err = status.Error(codes.Internal, "internal error")
				resp = nil
			}
		}()
		return handler(ctx, req)
	}
}

// TimeoutUnaryInterceptor applies a default deadline to callers that sent none,
// so a stalled dependency cannot hold a handler and its database connection
// open indefinitely. An existing (earlier) client deadline is left untouched.
func TimeoutUnaryInterceptor(timeout time.Duration) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if _, ok := ctx.Deadline(); ok {
			return handler(ctx, req)
		}
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		return handler(ctx, req)
	}
}

// MetricsUnaryInterceptor records per-RPC counts and latency. A nil m disables
// recording, which keeps tests free of a metrics dependency.
func MetricsUnaryInterceptor(m *metrics.RPCMetrics) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if m == nil {
			return handler(ctx, req)
		}
		start := time.Now()
		m.InFlight.WithLabelValues(info.FullMethod).Inc()
		defer m.InFlight.WithLabelValues(info.FullMethod).Dec()

		resp, err := handler(ctx, req)

		code := status.Code(err)
		m.Requests.WithLabelValues(info.FullMethod, code.String()).Inc()
		m.Duration.WithLabelValues(info.FullMethod, code.String()).Observe(time.Since(start).Seconds())
		return resp, err
	}
}

// LoggingUnaryInterceptor emits one structured line per RPC. Failures above
// InvalidArgument log at error level; client-caused statuses log at debug so
// routine validation rejections do not drown real faults.
func LoggingUnaryInterceptor(log zerolog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)

		code := status.Code(err)
		event := log.Debug()
		if isServerFault(code) {
			event = log.Error()
		}
		event.
			Str("method", info.FullMethod).
			Str("code", code.String()).
			Dur("duration", time.Since(start))
		if err != nil {
			event = event.Err(err)
		}
		event.Msg("rpc")
		return resp, err
	}
}

// isServerFault reports whether code indicates a fault on our side rather than
// a malformed or conflicting request from the client.
func isServerFault(code codes.Code) bool {
	switch code {
	case codes.Internal, codes.Unknown, codes.DataLoss, codes.Unavailable:
		return true
	default:
		return false
	}
}

// IsRetryable reports whether err is a transient failure worth retrying.
// Sagas use it to distinguish "try again" from "this will never succeed".
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	switch status.Code(err) {
	case codes.Unavailable, codes.DeadlineExceeded, codes.ResourceExhausted, codes.Aborted:
		return true
	default:
		return false
	}
}

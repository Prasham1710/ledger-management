package grpcmw_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/prasham/ledger/internal/grpcmw"
)

func quiet() zerolog.Logger { return zerolog.New(io.Discard) }

func info(method string) *grpc.UnaryServerInfo {
	return &grpc.UnaryServerInfo{FullMethod: method}
}

// A panic in any handler previously killed the entire process, failing every
// concurrent request with it.
func TestRecovery_ConvertsPanicToInternalError(t *testing.T) {
	t.Parallel()

	interceptor := grpcmw.RecoveryUnaryInterceptor(quiet())

	resp, err := interceptor(context.Background(), nil, info("/test/Panic"),
		func(context.Context, any) (any, error) {
			var p *struct{ Field string }
			_ = p.Field // nil dereference, the realistic case
			return nil, nil
		})

	require.Error(t, err, "the panic must be converted to an error, not propagated")
	assert.Nil(t, resp)
	assert.Equal(t, codes.Internal, status.Code(err))
	// The client must not receive a stack trace or panic text.
	assert.Equal(t, "internal error", status.Convert(err).Message())
}

func TestRecovery_PassesThroughNormalResults(t *testing.T) {
	t.Parallel()

	interceptor := grpcmw.RecoveryUnaryInterceptor(quiet())

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		resp, err := interceptor(context.Background(), nil, info("/test/OK"),
			func(context.Context, any) (any, error) { return "value", nil })
		require.NoError(t, err)
		assert.Equal(t, "value", resp)
	})

	t.Run("error is not swallowed", func(t *testing.T) {
		t.Parallel()
		sentinel := status.Error(codes.NotFound, "nope")
		_, err := interceptor(context.Background(), nil, info("/test/Err"),
			func(context.Context, any) (any, error) { return nil, sentinel })
		require.Error(t, err)
		assert.Equal(t, codes.NotFound, status.Code(err))
	})
}

func TestTimeout_AppliesDeadlineWhenCallerSentNone(t *testing.T) {
	t.Parallel()

	interceptor := grpcmw.TimeoutUnaryInterceptor(50 * time.Millisecond)

	var sawDeadline bool
	_, err := interceptor(context.Background(), nil, info("/test/Slow"),
		func(ctx context.Context, _ any) (any, error) {
			_, sawDeadline = ctx.Deadline()
			return nil, nil
		})

	require.NoError(t, err)
	assert.True(t, sawDeadline,
		"a handler must never run without a deadline — a stalled dependency would pin it forever")
}

func TestTimeout_PreservesEarlierCallerDeadline(t *testing.T) {
	t.Parallel()

	callerDeadline := time.Now().Add(10 * time.Millisecond)
	ctx, cancel := context.WithDeadline(context.Background(), callerDeadline)
	defer cancel()

	// A generous server default must not extend a tighter client deadline.
	interceptor := grpcmw.TimeoutUnaryInterceptor(time.Hour)

	var got time.Time
	_, err := interceptor(ctx, nil, info("/test/Fast"),
		func(ctx context.Context, _ any) (any, error) {
			got, _ = ctx.Deadline()
			return nil, nil
		})

	require.NoError(t, err)
	assert.WithinDuration(t, callerDeadline, got, time.Millisecond,
		"the caller's deadline must win")
}

func TestTimeout_HandlerObservesCancellation(t *testing.T) {
	t.Parallel()

	interceptor := grpcmw.TimeoutUnaryInterceptor(20 * time.Millisecond)

	_, err := interceptor(context.Background(), nil, info("/test/Hang"),
		func(ctx context.Context, _ any) (any, error) {
			select {
			case <-ctx.Done():
				return nil, status.FromContextError(ctx.Err()).Err()
			case <-time.After(2 * time.Second):
				return "should not get here", nil
			}
		})

	require.Error(t, err)
	assert.Equal(t, codes.DeadlineExceeded, status.Code(err))
}

func TestIsRetryable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil is not retryable", err: nil, want: false},

		// Transient: the request may succeed later.
		{name: "unavailable", err: status.Error(codes.Unavailable, "down"), want: true},
		{name: "deadline exceeded", err: status.Error(codes.DeadlineExceeded, "slow"), want: true},
		{name: "resource exhausted", err: status.Error(codes.ResourceExhausted, "throttled"), want: true},
		{name: "aborted (idempotency in flight)", err: status.Error(codes.Aborted, "in flight"), want: true},
		{name: "context deadline", err: context.DeadlineExceeded, want: true},
		{name: "context canceled", err: context.Canceled, want: true},

		// Terminal: retrying will produce the same answer.
		{name: "invalid argument", err: status.Error(codes.InvalidArgument, "bad"), want: false},
		{name: "not found", err: status.Error(codes.NotFound, "missing"), want: false},
		{name: "already exists", err: status.Error(codes.AlreadyExists, "dup"), want: false},
		{name: "failed precondition", err: status.Error(codes.FailedPrecondition, "no funds"), want: false},
		{name: "plain error", err: errors.New("boom"), want: false},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, grpcmw.IsRetryable(tc.err))
		})
	}
}

func TestServerOptions_ReturnsUsableChain(t *testing.T) {
	t.Parallel()

	opts := grpcmw.ServerOptions(grpcmw.Deps{Log: quiet()})
	require.NotEmpty(t, opts)
	// Must be accepted by grpc.NewServer without panicking.
	srv := grpc.NewServer(opts...)
	require.NotNil(t, srv)
	srv.Stop()
}

func TestClientOptions_ReturnsTracingDialOption(t *testing.T) {
	t.Parallel()

	opts := grpcmw.ClientOptions()
	require.NotEmpty(t, opts, "clients must propagate trace context or sagas appear as disconnected spans")
}

package idemguard_test

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/prasham/ledger/internal/idemguard"
	"github.com/prasham/ledger/internal/idempotency"
)

func quietLogger() zerolog.Logger { return zerolog.New(io.Discard) }

func newEmpty() *wrapperspb.StringValue { return &wrapperspb.StringValue{} }

func TestDo_ExecutesOnceThenReplays(t *testing.T) {
	t.Parallel()

	store := idempotency.NewMemoryStore()
	ctx := context.Background()
	var calls atomic.Int64

	op := func(context.Context) (*wrapperspb.StringValue, error) {
		calls.Add(1)
		return wrapperspb.String("result-1"), nil
	}

	first, err := idemguard.Do(ctx, store, quietLogger(), "key", newEmpty, op)
	require.NoError(t, err)
	assert.Equal(t, "result-1", first.Value)
	assert.Equal(t, int64(1), calls.Load())

	// A retry must replay the recorded response, not re-run the operation.
	second, err := idemguard.Do(ctx, store, quietLogger(), "key", newEmpty, op)
	require.NoError(t, err)
	assert.Equal(t, "result-1", second.Value, "retry must replay the original response")
	assert.Equal(t, int64(1), calls.Load(), "operation must not run twice")
}

// This is the behaviour the previous implementation got backwards: a client
// retrying after a timeout received AlreadyExists and could not discover
// whether its money had moved.
func TestDo_ReplayReturnsSuccessNotConflict(t *testing.T) {
	t.Parallel()

	store := idempotency.NewMemoryStore()
	ctx := context.Background()
	op := func(context.Context) (*wrapperspb.StringValue, error) {
		return wrapperspb.String("txn-42"), nil
	}

	_, err := idemguard.Do(ctx, store, quietLogger(), "key", newEmpty, op)
	require.NoError(t, err)

	replay, err := idemguard.Do(ctx, store, quietLogger(), "key", newEmpty, op)
	require.NoError(t, err, "a replay is a success, not a conflict")
	assert.Equal(t, codes.OK, status.Code(err))
	assert.Equal(t, "txn-42", replay.Value)
}

func TestDo_FailsClosedWhenStoreUnavailable(t *testing.T) {
	t.Parallel()

	store := idempotency.NewMemoryStore()
	store.FailNextClaim(errors.New("redis unreachable"))

	var called atomic.Bool
	_, err := idemguard.Do(context.Background(), store, quietLogger(), "key", newEmpty,
		func(context.Context) (*wrapperspb.StringValue, error) {
			called.Store(true)
			return wrapperspb.String("should not happen"), nil
		})

	require.Error(t, err)
	assert.Equal(t, codes.Unavailable, status.Code(err),
		"a store outage must reject the request")
	assert.False(t, called.Load(),
		"the guarded operation must NOT run when dedup cannot be guaranteed")
}

func TestDo_ConcurrentCallersExecuteOnce(t *testing.T) {
	t.Parallel()

	store := idempotency.NewMemoryStore()
	ctx := context.Background()

	var executions atomic.Int64
	op := func(context.Context) (*wrapperspb.StringValue, error) {
		executions.Add(1)
		return wrapperspb.String("once"), nil
	}

	const callers = 32
	var wg sync.WaitGroup
	results := make([]*wrapperspb.StringValue, callers)
	codesSeen := make([]codes.Code, callers)
	start := make(chan struct{})

	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			resp, err := idemguard.Do(ctx, store, quietLogger(), "contended", newEmpty, op)
			results[idx], codesSeen[idx] = resp, status.Code(err)
		}(i)
	}
	close(start)
	wg.Wait()

	// The invariant that matters: the guarded operation ran exactly once, no
	// matter how the race was scheduled.
	assert.Equal(t, int64(1), executions.Load(),
		"the guarded operation must execute exactly once under contention")

	// Each caller either received the single real response or was told to retry.
	// Both are correct; what must never happen is a second execution or an
	// unrelated error.
	for i, c := range codesSeen {
		switch c {
		case codes.OK:
			require.NotNil(t, results[i])
			assert.Equal(t, "once", results[i].Value,
				"every successful caller sees the same single result")
		case codes.Aborted:
			// Winner still in flight — client retries.
		default:
			t.Fatalf("caller %d got unexpected code %s", i, c)
		}
	}
}

func TestDo_ReleasesClaimOnFailureSoRetryCanProceed(t *testing.T) {
	t.Parallel()

	store := idempotency.NewMemoryStore()
	ctx := context.Background()
	var calls atomic.Int64

	failing := func(context.Context) (*wrapperspb.StringValue, error) {
		calls.Add(1)
		return nil, status.Error(codes.Unavailable, "downstream down")
	}

	_, err := idemguard.Do(ctx, store, quietLogger(), "key", newEmpty, failing)
	require.Error(t, err)
	assert.Equal(t, codes.Unavailable, status.Code(err))

	// The claim must have been released: a transient failure is retryable.
	_, err = idemguard.Do(ctx, store, quietLogger(), "key", newEmpty, failing)
	require.Error(t, err)
	assert.Equal(t, int64(2), calls.Load(), "a failed operation must be retryable")
}

func TestDo_PropagatesOperationErrorUnchanged(t *testing.T) {
	t.Parallel()

	store := idempotency.NewMemoryStore()
	sentinel := status.Error(codes.FailedPrecondition, "insufficient balance")

	_, err := idemguard.Do(context.Background(), store, quietLogger(), "key", newEmpty,
		func(context.Context) (*wrapperspb.StringValue, error) { return nil, sentinel })

	require.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
	assert.Contains(t, err.Error(), "insufficient balance")
}

func TestFail_RecordsTerminalErrorForReplay(t *testing.T) {
	t.Parallel()

	store := idempotency.NewMemoryStore()
	ctx := context.Background()

	claim, err := store.Claim(ctx, "key")
	require.NoError(t, err)
	require.True(t, claim.Acquired)

	idemguard.Fail(ctx, store, quietLogger(), "key", codes.FailedPrecondition, "account suspended")

	// A retry must observe the same terminal error rather than re-executing an
	// operation that already had side effects.
	_, err = idemguard.Do(ctx, store, quietLogger(), "key", newEmpty,
		func(context.Context) (*wrapperspb.StringValue, error) {
			t.Fatal("operation must not re-run after a terminal failure was recorded")
			return nil, nil
		})
	require.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
	assert.Contains(t, err.Error(), "account suspended")
}

func TestDo_CommitFailureStillReturnsResponse(t *testing.T) {
	t.Parallel()

	// The operation committed to the database; a Redis failure afterwards must
	// not turn a successful money movement into a client-visible error.
	store := &commitFailingStore{MemoryStore: idempotency.NewMemoryStore()}

	resp, err := idemguard.Do(context.Background(), store, quietLogger(), "key", newEmpty,
		func(context.Context) (*wrapperspb.StringValue, error) {
			return wrapperspb.String("committed"), nil
		})

	require.NoError(t, err, "a post-commit cache failure must not fail the request")
	assert.Equal(t, "committed", resp.Value)
}

type commitFailingStore struct {
	*idempotency.MemoryStore
}

func (s *commitFailingStore) Commit(context.Context, string, idempotency.Entry) error {
	return errors.New("redis write failed")
}

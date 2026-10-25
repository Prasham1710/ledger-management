package idempotency_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/prasham/ledger/internal/idempotency"
)

func TestClaim_FirstCallerWins(t *testing.T) {
	t.Parallel()

	store := idempotency.NewMemoryStore()
	ctx := context.Background()

	first, err := store.Claim(ctx, "key-1")
	require.NoError(t, err)
	assert.True(t, first.Acquired, "first claim must be acquired")
	assert.Nil(t, first.Entry)

	second, err := store.Claim(ctx, "key-1")
	require.NoError(t, err)
	assert.False(t, second.Acquired, "second claim must not be acquired")
	assert.True(t, second.InFlight, "second claim must report the original as in flight")
	assert.Nil(t, second.Entry)
}

func TestClaim_ReplaysCommittedResponse(t *testing.T) {
	t.Parallel()

	store := idempotency.NewMemoryStore()
	ctx := context.Background()

	claim, err := store.Claim(ctx, "key-1")
	require.NoError(t, err)
	require.True(t, claim.Acquired)

	require.NoError(t, store.Commit(ctx, "key-1", idempotency.Entry{
		StatusCode: 0,
		Body:       []byte("original-response"),
	}))

	replay, err := store.Claim(ctx, "key-1")
	require.NoError(t, err)
	assert.False(t, replay.Acquired, "a committed key must never be re-acquired")
	assert.False(t, replay.InFlight)
	require.NotNil(t, replay.Entry)
	assert.Equal(t, []byte("original-response"), replay.Entry.Body)
	assert.False(t, replay.Entry.CreatedAt.IsZero(), "Commit must stamp CreatedAt")
}

// TestClaim_ExactlyOneWinnerUnderConcurrency is the property that matters: the
// pre-fix Get-then-Set implementation allowed every concurrent caller through.
func TestClaim_ExactlyOneWinnerUnderConcurrency(t *testing.T) {
	t.Parallel()

	store := idempotency.NewMemoryStore()
	ctx := context.Background()

	const goroutines = 64
	var acquired atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			claim, err := store.Claim(ctx, "contended")
			require.NoError(t, err)
			if claim.Acquired {
				acquired.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()

	assert.Equal(t, int64(1), acquired.Load(),
		"exactly one caller may execute the guarded operation")
}

func TestRelease_AllowsRetry(t *testing.T) {
	t.Parallel()

	store := idempotency.NewMemoryStore()
	ctx := context.Background()

	claim, err := store.Claim(ctx, "key-1")
	require.NoError(t, err)
	require.True(t, claim.Acquired)

	// Operation failed with no side effects — release so a retry can proceed.
	require.NoError(t, store.Release(ctx, "key-1"))

	retry, err := store.Claim(ctx, "key-1")
	require.NoError(t, err)
	assert.True(t, retry.Acquired, "a released key must be re-acquirable")
}

func TestClaim_PropagatesStoreErrors(t *testing.T) {
	t.Parallel()

	store := idempotency.NewMemoryStore()
	sentinel := errors.New("redis unreachable")
	store.FailNextClaim(sentinel)

	_, err := store.Claim(context.Background(), "key-1")
	require.Error(t, err, "store outages must surface so callers can fail closed")
	assert.ErrorIs(t, err, sentinel)
}

func TestCommit_OverwritesPendingClaim(t *testing.T) {
	t.Parallel()

	store := idempotency.NewMemoryStore()
	ctx := context.Background()

	_, err := store.Claim(ctx, "key-1")
	require.NoError(t, err)

	require.NoError(t, store.Commit(ctx, "key-1", idempotency.Entry{Body: []byte("v1")}))
	// A second Commit by the claim owner (e.g. terminal failure recorded after a
	// success path) must take effect — Commit is not NX.
	require.NoError(t, store.Commit(ctx, "key-1", idempotency.Entry{Body: []byte("v2")}))

	replay, err := store.Claim(ctx, "key-1")
	require.NoError(t, err)
	require.NotNil(t, replay.Entry)
	assert.Equal(t, []byte("v2"), replay.Entry.Body)
}

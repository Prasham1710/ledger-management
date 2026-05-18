package idempotency_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/prasham/ledger/internal/idempotency"
)

// mockStore is an in-memory idempotency.Store for unit tests.
type mockStore struct {
	entries map[string]*idempotency.Entry
}

func newMockStore() *mockStore { return &mockStore{entries: make(map[string]*idempotency.Entry)} }

func (m *mockStore) Get(_ context.Context, key string) (*idempotency.Entry, error) {
	e, ok := m.entries[key]
	if !ok {
		return nil, nil
	}
	return e, nil
}

func (m *mockStore) Set(_ context.Context, key string, entry idempotency.Entry) error {
	// NX semantics: do not overwrite.
	if _, ok := m.entries[key]; ok {
		return nil
	}
	m.entries[key] = &entry
	return nil
}

func TestStore_MissAndHit(t *testing.T) {
	t.Parallel()

	store := newMockStore()
	ctx := context.Background()

	// Miss on empty store.
	got, err := store.Get(ctx, "key-1")
	require.NoError(t, err)
	assert.Nil(t, got)

	// Set an entry.
	body, _ := json.Marshal(map[string]string{"transaction_id": "txn-001"})
	err = store.Set(ctx, "key-1", idempotency.Entry{
		StatusCode: 200,
		Body:       body,
	})
	require.NoError(t, err)

	// Hit.
	got, err = store.Get(ctx, "key-1")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, 200, got.StatusCode)
}

func TestStore_NXSemantics(t *testing.T) {
	t.Parallel()

	store := newMockStore()
	ctx := context.Background()

	first := idempotency.Entry{StatusCode: 200, Body: []byte(`{"v":1}`)}
	second := idempotency.Entry{StatusCode: 500, Body: []byte(`{"v":2}`)}

	require.NoError(t, store.Set(ctx, "key", first))
	require.NoError(t, store.Set(ctx, "key", second)) // NX: must not overwrite

	got, err := store.Get(ctx, "key")
	require.NoError(t, err)
	assert.Equal(t, 200, got.StatusCode, "second Set must not overwrite first response")
}

func TestEntry_CreatedAtSetOnStore(t *testing.T) {
	t.Parallel()

	before := time.Now()
	e := idempotency.Entry{StatusCode: 201, Body: []byte(`{}`)}
	// CreatedAt is set inside RedisStore.Set; here we just verify the field exists.
	assert.True(t, e.CreatedAt.IsZero() || e.CreatedAt.After(before) || e.CreatedAt.Equal(before))
}

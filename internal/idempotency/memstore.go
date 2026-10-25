package idempotency

import (
	"context"
	"sync"
	"time"
)

// MemoryStore is a process-local Store implementation. It is safe for
// concurrent use and is intended for tests and single-process development.
// It provides no cross-process guarantees — use RedisStore in deployment.
type MemoryStore struct {
	mu       sync.Mutex
	records  map[string]*record
	expires  map[string]time.Time
	ttl      time.Duration
	claimTTL time.Duration
	// failNext, when set, makes the next Claim return this error. Test hook for
	// verifying callers fail closed on store outages.
	failNext error
}

// NewMemoryStore returns an empty MemoryStore with the default TTLs.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		records:  make(map[string]*record),
		expires:  make(map[string]time.Time),
		ttl:      defaultTTL,
		claimTTL: defaultClaimTTL,
	}
}

// FailNextClaim makes the next call to Claim return err. Used by tests to
// assert that callers reject requests rather than proceeding unguarded.
func (m *MemoryStore) FailNextClaim(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failNext = err
}

// Claim reserves key if it is free.
func (m *MemoryStore) Claim(_ context.Context, key string) (Claim, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.failNext; err != nil {
		m.failNext = nil
		return Claim{}, err
	}

	m.evictLocked(key)

	rec, ok := m.records[key]
	if !ok {
		m.records[key] = &record{State: statePending}
		m.expires[key] = time.Now().Add(m.claimTTL)
		return Claim{Acquired: true}, nil
	}
	if rec.State == stateCommitted && rec.Entry != nil {
		cp := *rec.Entry
		return Claim{Entry: &cp}, nil
	}
	return Claim{InFlight: true}, nil
}

// Commit records the outcome for key.
func (m *MemoryStore) Commit(_ context.Context, key string, entry Entry) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	entry.CreatedAt = time.Now().UTC()
	m.records[key] = &record{State: stateCommitted, Entry: &entry}
	m.expires[key] = time.Now().Add(m.ttl)
	return nil
}

// Release drops the claim on key.
func (m *MemoryStore) Release(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.records, key)
	delete(m.expires, key)
	return nil
}

func (m *MemoryStore) evictLocked(key string) {
	if exp, ok := m.expires[key]; ok && time.Now().After(exp) {
		delete(m.records, key)
		delete(m.expires, key)
	}
}

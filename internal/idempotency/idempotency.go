// Package idempotency provides a Redis-backed store for idempotency keys.
//
// The store implements a two-phase claim/commit protocol so that concurrent
// requests carrying the same key cannot both execute the guarded operation:
//
//	claim, err := store.Claim(ctx, key)
//	if err != nil        { /* fail closed — do NOT execute */ }
//	if !claim.Acquired   { /* replay claim.Entry, or retry if still in flight */ }
//	... execute the operation ...
//	store.Commit(ctx, key, Entry{...})
//
// Claim is atomic (Redis SET NX): exactly one caller receives Acquired=true for
// a given key. Losers either replay the committed response or, if the winner is
// still working, learn the request is in flight and must be retried later.
//
// Callers MUST treat a Claim error as fatal for the request. Returning success
// on a store failure would silently disable deduplication, which for a ledger
// means double-spend.
package idempotency

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// defaultTTL is how long a committed response is replayable.
	defaultTTL = 24 * time.Hour
	// defaultClaimTTL bounds how long an in-flight claim blocks retries. If the
	// process holding the claim dies before committing, the key expires after
	// this long and the operation becomes retryable.
	defaultClaimTTL = 5 * time.Minute

	keyPrefix = "idem:"

	statePending   = "pending"
	stateCommitted = "committed"
)

// ErrInFlight is returned by Get when a claim exists but has not been
// committed yet — the original request is still executing.
var ErrInFlight = errors.New("idempotency: request already in flight")

// Entry is the recorded outcome of a guarded operation.
type Entry struct {
	// StatusCode is the status code of the original response. For gRPC callers
	// this is the integer value of a codes.Code; for HTTP, the HTTP status.
	StatusCode int `json:"status_code"`
	// Body is the serialized original response, replayed verbatim on retry.
	Body []byte `json:"body"`
	// CreatedAt is when the entry was committed (UTC).
	CreatedAt time.Time `json:"created_at"`
}

// Claim is the result of attempting to reserve an idempotency key.
type Claim struct {
	// Acquired is true if this caller won the race and must execute the
	// operation, then call Commit.
	Acquired bool
	// Entry is the previously committed response. Non-nil only when
	// Acquired is false and the original request has completed.
	Entry *Entry
	// InFlight is true when another caller holds the claim but has not
	// committed yet. Acquired is false and Entry is nil.
	InFlight bool
}

// Store guards operations against duplicate execution.
type Store interface {
	// Claim atomically reserves key. Exactly one concurrent caller receives
	// Acquired=true. An error means the store is unavailable and the caller
	// must fail the request rather than proceed unguarded.
	Claim(ctx context.Context, key string) (Claim, error)
	// Commit records the operation's outcome so later replays return it.
	Commit(ctx context.Context, key string, entry Entry) error
	// Release drops a claim so the operation can be retried. Called when the
	// guarded operation fails in a way that leaves no side effects.
	Release(ctx context.Context, key string) error
}

// record is the Redis-side representation of a claim.
type record struct {
	State string `json:"state"`
	Entry *Entry `json:"entry,omitempty"`
}

// RedisStore implements Store on Redis.
type RedisStore struct {
	client   *redis.Client
	ttl      time.Duration
	claimTTL time.Duration
}

// NewRedisStore returns a RedisStore with the default commit and claim TTLs.
func NewRedisStore(client *redis.Client) *RedisStore {
	return &RedisStore{client: client, ttl: defaultTTL, claimTTL: defaultClaimTTL}
}

// NewRedisStoreWithTTL returns a RedisStore with explicit TTLs. commitTTL
// bounds how long responses stay replayable; claimTTL bounds how long a crashed
// in-flight request blocks retries.
func NewRedisStoreWithTTL(client *redis.Client, commitTTL, claimTTL time.Duration) *RedisStore {
	if commitTTL <= 0 {
		commitTTL = defaultTTL
	}
	if claimTTL <= 0 {
		claimTTL = defaultClaimTTL
	}
	return &RedisStore{client: client, ttl: commitTTL, claimTTL: claimTTL}
}

// Claim atomically reserves key using SET NX.
func (s *RedisStore) Claim(ctx context.Context, key string) (Claim, error) {
	pending, err := json.Marshal(record{State: statePending})
	if err != nil {
		return Claim{}, fmt.Errorf("idempotency.Claim: marshal: %w", err)
	}

	won, err := s.client.SetNX(ctx, keyPrefix+key, pending, s.claimTTL).Result()
	if err != nil {
		return Claim{}, fmt.Errorf("idempotency.Claim: redis: %w", err)
	}
	if won {
		return Claim{Acquired: true}, nil
	}

	// Lost the race — report whether the winner has committed a response yet.
	rec, err := s.load(ctx, key)
	if err != nil {
		return Claim{}, err
	}
	if rec == nil {
		// The key expired between SetNX and the read. Treat as in-flight; the
		// caller retries and will win the next Claim.
		return Claim{InFlight: true}, nil
	}
	if rec.State == stateCommitted && rec.Entry != nil {
		return Claim{Entry: rec.Entry}, nil
	}
	return Claim{InFlight: true}, nil
}

// Commit records entry under key and extends the TTL to the replay window.
func (s *RedisStore) Commit(ctx context.Context, key string, entry Entry) error {
	entry.CreatedAt = time.Now().UTC()
	raw, err := json.Marshal(record{State: stateCommitted, Entry: &entry})
	if err != nil {
		return fmt.Errorf("idempotency.Commit: marshal: %w", err)
	}
	// Unconditional SET: the caller owns the claim, so overwriting the pending
	// marker with the committed response is correct.
	if err := s.client.Set(ctx, keyPrefix+key, raw, s.ttl).Err(); err != nil {
		return fmt.Errorf("idempotency.Commit: redis: %w", err)
	}
	return nil
}

// Release deletes the claim so the operation can be retried.
func (s *RedisStore) Release(ctx context.Context, key string) error {
	if err := s.client.Del(ctx, keyPrefix+key).Err(); err != nil {
		return fmt.Errorf("idempotency.Release: redis: %w", err)
	}
	return nil
}

func (s *RedisStore) load(ctx context.Context, key string) (*record, error) {
	raw, err := s.client.Get(ctx, keyPrefix+key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("idempotency: redis get: %w", err)
	}
	var rec record
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, fmt.Errorf("idempotency: unmarshal: %w", err)
	}
	return &rec, nil
}

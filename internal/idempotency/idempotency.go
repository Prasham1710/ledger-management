// Package idempotency provides a Redis-backed store for HTTP idempotency keys.
// A stored entry guarantees that replayed requests return the original response
// rather than executing the operation a second time.
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
	defaultTTL    = 24 * time.Hour
	keyPrefix     = "idem:"
)

// Entry is the value stored under an idempotency key.
type Entry struct {
	// StatusCode is the HTTP status code of the original response.
	StatusCode int `json:"status_code"`
	// Body is the raw JSON body of the original response.
	Body []byte `json:"body"`
	// CreatedAt is when the entry was first stored (UTC).
	CreatedAt time.Time `json:"created_at"`
}

// Store is the interface for idempotency key persistence.
type Store interface {
	// Get retrieves a cached response. Returns (nil, nil) on cache miss.
	Get(ctx context.Context, key string) (*Entry, error)
	// Set stores a response. A second Set on the same key is a no-op (NX semantics).
	Set(ctx context.Context, key string, entry Entry) error
}

// RedisStore implements Store using Redis SET NX with a TTL.
type RedisStore struct {
	client *redis.Client
	ttl    time.Duration
}

// NewRedisStore returns a RedisStore backed by client with a 24-hour TTL.
func NewRedisStore(client *redis.Client) *RedisStore {
	return &RedisStore{client: client, ttl: defaultTTL}
}

// Get returns the stored Entry for key, or (nil, nil) if the key does not exist.
func (s *RedisStore) Get(ctx context.Context, key string) (*Entry, error) {
	raw, err := s.client.Get(ctx, keyPrefix+key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("idempotency.Get: redis: %w", err)
	}

	var e Entry
	if err := json.Unmarshal(raw, &e); err != nil {
		return nil, fmt.Errorf("idempotency.Get: unmarshal: %w", err)
	}
	return &e, nil
}

// Set stores entry under key using SET NX so concurrent racing requests
// do not overwrite the first response. Silently succeeds if key already exists.
func (s *RedisStore) Set(ctx context.Context, key string, entry Entry) error {
	entry.CreatedAt = time.Now().UTC()
	raw, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("idempotency.Set: marshal: %w", err)
	}
	// SetNX: only write if key does not already exist.
	if err := s.client.SetNX(ctx, keyPrefix+key, raw, s.ttl).Err(); err != nil {
		return fmt.Errorf("idempotency.Set: redis: %w", err)
	}
	return nil
}

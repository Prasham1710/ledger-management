// Package idemguard wraps idempotency.Store with the gRPC-facing half of the
// claim/commit protocol: it replays recorded protobuf responses, maps store
// failures onto gRPC status codes, and releases claims for operations that
// failed without side effects.
//
// Services should route every mutating RPC through Do rather than calling the
// store directly — the hand-rolled Get-then-Set pattern it replaces was not
// atomic and silently permitted double execution.
package idemguard

import (
	"context"
	"errors"

	"github.com/rs/zerolog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/prasham/ledger/internal/idempotency"
)

// ErrStoreUnavailable is returned when the idempotency store cannot be reached.
// Callers must fail the request: executing unguarded risks double-spend.
var ErrStoreUnavailable = errors.New("idempotency store unavailable")

// Do executes op at most once per key.
//
// On the first call for key, op runs and its response is recorded. Later calls
// with the same key replay the recorded response without re-executing. Calls
// that arrive while op is still running receive codes.Aborted, signalling a
// safe client retry.
//
// If op returns an error, the claim is released so the caller may retry — op
// must therefore either fully succeed or leave no side effects. Operations with
// partial side effects must record their own terminal state instead (see the
// saga services, which persist state transitions before returning).
//
// newEmpty constructs a zero-valued response used to decode replays.
func Do[T proto.Message](
	ctx context.Context,
	store idempotency.Store,
	log zerolog.Logger,
	key string,
	newEmpty func() T,
	op func(context.Context) (T, error),
) (T, error) {
	var zero T

	claim, err := store.Claim(ctx, key)
	if err != nil {
		// Fail closed. Proceeding here would bypass deduplication entirely.
		log.Error().Err(err).Str("idem_key", key).Msg("idempotency claim failed — rejecting request")
		return zero, status.Error(codes.Unavailable, ErrStoreUnavailable.Error())
	}

	switch {
	case claim.Entry != nil:
		// Replay the original outcome verbatim.
		if code := codes.Code(claim.Entry.StatusCode); code != codes.OK { //nolint:gosec // bounded by Commit
			return zero, status.Error(code, string(claim.Entry.Body))
		}
		msg := newEmpty()
		if err := proto.Unmarshal(claim.Entry.Body, msg); err != nil {
			log.Error().Err(err).Str("idem_key", key).Msg("cannot decode recorded response")
			return zero, status.Error(codes.Internal, "recorded response is unreadable")
		}
		return msg, nil

	case claim.InFlight:
		return zero, status.Error(codes.Aborted,
			"a request with this idempotency_key is currently in flight — retry shortly")
	}

	// We own the claim: execute exactly once.
	resp, err := op(ctx)
	if err != nil {
		// Release so a retry can proceed. Use a detached context: op may have
		// failed precisely because ctx was cancelled.
		if relErr := store.Release(context.WithoutCancel(ctx), key); relErr != nil {
			log.Warn().Err(relErr).Str("idem_key", key).Msg("releasing idempotency claim failed")
		}
		return zero, err
	}

	body, marshalErr := proto.Marshal(resp)
	if marshalErr != nil {
		log.Error().Err(marshalErr).Str("idem_key", key).Msg("cannot encode response for replay")
		return resp, nil
	}

	// Commit on a detached context: the operation has already committed to the
	// database, so recording it must not be skipped because the client hung up.
	if err := store.Commit(context.WithoutCancel(ctx), key, idempotency.Entry{
		StatusCode: int(codes.OK),
		Body:       body,
	}); err != nil {
		// The operation succeeded but is no longer replay-protected by Redis.
		// Database-level uniqueness constraints remain the backstop.
		log.Error().Err(err).Str("idem_key", key).
			Msg("committing idempotency record failed — retries fall back to DB constraints")
	}
	return resp, nil
}

// Fail records a terminal failure against key so retries observe the same
// error instead of re-running an operation that already had side effects.
func Fail(ctx context.Context, store idempotency.Store, log zerolog.Logger, key string, code codes.Code, msg string) {
	if err := store.Commit(context.WithoutCancel(ctx), key, idempotency.Entry{
		StatusCode: int(code),
		Body:       []byte(msg),
	}); err != nil {
		log.Warn().Err(err).Str("idem_key", key).Msg("recording terminal failure failed")
	}
}

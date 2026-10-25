package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/prasham/ledger/services/gateway/internal/auth"
	"github.com/prasham/ledger/services/gateway/internal/middleware"
)

const (
	testSecret   = "test-secret-that-is-long-enough-for-hs256"
	upstreamSalt = "upstream-shared-secret-value"
)

func newManager(t *testing.T) *auth.Manager {
	t.Helper()
	mgr, err := auth.NewManager(auth.Config{
		Secret:        testSecret,
		TTL:           time.Hour,
		MaxSessionAge: 12 * time.Hour,
	})
	require.NoError(t, err)
	return mgr
}

// captured records what the protected handler actually received.
type captured struct {
	called  bool
	subject string
	role    string
	verifed string
}

func protectedChain(t *testing.T, mgr *auth.Manager, got *captured) http.Handler {
	t.Helper()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.called = true
		got.subject = r.Header.Get(middleware.HeaderAuthSubject)
		got.role = r.Header.Get(middleware.HeaderAuthRole)
		got.verifed = r.Header.Get(middleware.HeaderAuthVerified)
		w.WriteHeader(http.StatusOK)
	})
	return middleware.Auth(mgr, upstreamSalt)(next)
}

func TestAuth_RejectsMissingToken(t *testing.T) {
	t.Parallel()

	var got captured
	h := protectedChain(t, newManager(t), &got)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/accounts", nil))

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.False(t, got.called, "the protected handler must not run")
}

func TestAuth_RejectsInvalidToken(t *testing.T) {
	t.Parallel()

	var got captured
	h := protectedChain(t, newManager(t), &got)

	req := httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
	req.Header.Set("Authorization", "Bearer not-a-real-token")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.False(t, got.called)
}

func TestAuth_AttachesVerifiedIdentity(t *testing.T) {
	t.Parallel()

	mgr := newManager(t)
	token, err := mgr.Sign("alice", auth.RoleAdmin)
	require.NoError(t, err)

	var got captured
	h := protectedChain(t, mgr, &got)

	req := httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.True(t, got.called)
	assert.Equal(t, "alice", got.subject)
	assert.Equal(t, auth.RoleAdmin, got.role)
	assert.Equal(t, upstreamSalt, got.verifed,
		"upstreams need proof the request came through the gateway")
}

// The sharpest gateway bug: identity headers were only ever set, never stripped,
// and only for /api/ paths — so a client could supply its own and have it
// forwarded to an upstream that trusted it.
func TestAuth_StripsClientSuppliedIdentityHeaders(t *testing.T) {
	t.Parallel()

	t.Run("on an unauthenticated request", func(t *testing.T) {
		t.Parallel()
		var got captured
		h := protectedChain(t, newManager(t), &got)

		// A public path, where the old middleware returned early without touching
		// these headers.
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set(middleware.HeaderAuthSubject, "attacker")
		req.Header.Set(middleware.HeaderAuthRole, auth.RoleAdmin)
		req.Header.Set(middleware.HeaderAuthVerified, "forged")

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		require.True(t, got.called)
		assert.Empty(t, got.subject, "a spoofed subject must not reach upstreams")
		assert.Empty(t, got.role, "a spoofed role must not reach upstreams")
		assert.Empty(t, got.verifed, "a forged verification marker must not reach upstreams")
	})

	t.Run("overwritten on an authenticated request", func(t *testing.T) {
		t.Parallel()
		mgr := newManager(t)
		token, err := mgr.Sign("alice", auth.RoleViewer)
		require.NoError(t, err)

		var got captured
		h := protectedChain(t, mgr, &got)

		req := httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		// Attempt to escalate by supplying a higher role alongside a valid token.
		req.Header.Set(middleware.HeaderAuthRole, auth.RoleAdmin)
		req.Header.Set(middleware.HeaderAuthSubject, "root")

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		require.True(t, got.called)
		assert.Equal(t, "alice", got.subject, "identity comes from the token, not the header")
		assert.Equal(t, auth.RoleViewer, got.role, "the role must not be escalated by a header")
	})
}

func TestAuth_PublicPathsSkipVerification(t *testing.T) {
	t.Parallel()

	for _, path := range []string{"/", "/auth/login", "/auth/refresh", "/healthz", "/assets/app.js"} {
		var got captured
		h := protectedChain(t, newManager(t), &got)

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

		assert.Equal(t, http.StatusOK, rec.Code, "path %q should be public", path)
		assert.True(t, got.called, "path %q should reach the handler", path)
	}
}

func TestAuth_AllAPIPathsRequireAToken(t *testing.T) {
	t.Parallel()

	// Every money-moving route must be protected.
	for _, path := range []string{
		"/api/accounts",
		"/api/accounts/abc/credit",
		"/api/accounts/abc/debit",
		"/api/transactions",
		"/api/payouts",
		"/api/ledger/entries",
	} {
		var got captured
		h := protectedChain(t, newManager(t), &got)

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, nil))

		assert.Equal(t, http.StatusUnauthorized, rec.Code, "path %q must require a token", path)
		assert.False(t, got.called, "path %q must not reach the handler unauthenticated", path)
	}
}

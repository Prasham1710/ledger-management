package handler_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/prasham/ledger/services/ui-bff/internal/handler"
)

const sharedSecret = "gateway-upstream-shared-secret"

// newHandler builds a handler with authentication enforced. The gRPC clients are
// nil: every case below is rejected by the auth layer before any client is used,
// which is exactly what these tests assert.
func newHandler(t *testing.T) *handler.Handler {
	t.Helper()
	return handler.New(
		handler.Clients{},
		handler.Conns{},
		handler.Config{
			StaticDir:            t.TempDir(),
			UpstreamSharedSecret: sharedSecret,
			RequireAuth:          true,
		},
		zerolog.New(io.Discard),
	)
}

// gatewayRequest builds a request as the gateway would forward it.
func gatewayRequest(method, path, role, body string) *http.Request {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-Auth-Verified", sharedSecret)
	req.Header.Set("X-Auth-Subject", "alice")
	req.Header.Set("X-Auth-Role", role)
	return req
}

// mutatingRoutes are every endpoint that moves or creates money. Each was
// previously reachable with no credentials whatsoever on port 8090.
var mutatingRoutes = []struct {
	method string
	path   string
	body   string
}{
	{http.MethodPost, "/api/accounts", `{"idempotency_key":"k","owner_id":"o","currency":"INR"}`},
	{http.MethodPost, "/api/accounts/a1/credit", `{"idempotency_key":"k","amount":"1.00","currency":"INR","reference_id":"r"}`},
	{http.MethodPost, "/api/accounts/a1/debit", `{"idempotency_key":"k","amount":"1.00","currency":"INR","reference_id":"r"}`},
	{http.MethodPost, "/api/accounts/a1/holds", `{"idempotency_key":"k","amount":"1.00","currency":"INR","reference_id":"r"}`},
	{http.MethodDelete, "/api/accounts/a1/holds/h1", ""},
	{http.MethodPost, "/api/transactions", `{"idempotency_key":"k","source_account_id":"a","dest_account_id":"b","amount":"1.00","currency":"INR"}`},
	{http.MethodPost, "/api/payouts", `{"idempotency_key":"k","account_id":"a","amount":"1.00","currency":"INR","rail":"NACH"}`},
	{http.MethodPost, "/api/ledger/entries", `{"idempotency_key":"k","debit_account_id":"a","credit_account_id":"b","amount":"1.00","currency":"INR","reference_id":"r"}`},
}

var readRoutes = []string{
	"/api/accounts?owner_id=o",
	"/api/accounts/a1",
	"/api/ledger/a1/balance",
	"/api/ledger/a1/entries",
	"/api/transactions/t1",
	"/api/transactions/t1/trace",
	"/api/payouts/p1",
}

// The headline fix: this service had no authentication at all.
func TestMutatingRoutes_RejectUnauthenticatedRequests(t *testing.T) {
	t.Parallel()

	h := newHandler(t)

	for _, route := range mutatingRoutes {
		var req *http.Request
		if route.body == "" {
			req = httptest.NewRequest(route.method, route.path, nil)
		} else {
			req = httptest.NewRequest(route.method, route.path, strings.NewReader(route.body))
		}

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusUnauthorized, rec.Code,
			"%s %s must reject an unauthenticated request", route.method, route.path)
	}
}

func TestReadRoutes_RejectUnauthenticatedRequests(t *testing.T) {
	t.Parallel()

	h := newHandler(t)

	for _, path := range readRoutes {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		assert.Equal(t, http.StatusUnauthorized, rec.Code,
			"GET %s must reject an unauthenticated request", path)
	}
}

// Supplying the identity headers without the shared secret must not work, or the
// gateway's authentication could be bypassed by simply naming a role.
func TestForgedIdentityHeadersAreRejected(t *testing.T) {
	t.Parallel()

	h := newHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/api/accounts/a1/credit",
		strings.NewReader(`{"idempotency_key":"k","amount":"1000000.00","currency":"INR","reference_id":"r"}`))
	// An attacker's best guess: claim to be an admin.
	req.Header.Set("X-Auth-Subject", "attacker")
	req.Header.Set("X-Auth-Role", "admin")
	// No X-Auth-Verified, because only the gateway knows the secret.

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code,
		"identity headers without the gateway secret must not authenticate")
}

func TestWrongSharedSecretIsRejected(t *testing.T) {
	t.Parallel()

	h := newHandler(t)

	req := gatewayRequest(http.MethodPost, "/api/accounts/a1/credit", "admin",
		`{"idempotency_key":"k","amount":"1.00","currency":"INR","reference_id":"r"}`)
	req.Header.Set("X-Auth-Verified", "wrong-secret")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

// Direct balance adjustment creates or destroys money, so it is admin-only.
func TestBalanceAdjustment_RequiresAdmin(t *testing.T) {
	t.Parallel()

	adminOnly := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPost, "/api/accounts/a1/credit", `{"idempotency_key":"k","amount":"1.00","currency":"INR","reference_id":"r"}`},
		{http.MethodPost, "/api/accounts/a1/debit", `{"idempotency_key":"k","amount":"1.00","currency":"INR","reference_id":"r"}`},
		{http.MethodPost, "/api/ledger/entries", `{"idempotency_key":"k","debit_account_id":"a","credit_account_id":"b","amount":"1.00","currency":"INR","reference_id":"r"}`},
	}

	for _, route := range adminOnly {
		for _, role := range []string{"viewer", "operator"} {
			h := newHandler(t)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, gatewayRequest(route.method, route.path, role, route.body))

			assert.Equal(t, http.StatusForbidden, rec.Code,
				"%s %s must be forbidden for role %q", route.method, route.path, role)
		}
	}
}

// A viewer may read but must not move money.
func TestViewerCannotInitiateMoneyMovement(t *testing.T) {
	t.Parallel()

	sagaRoutes := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPost, "/api/transactions", `{"idempotency_key":"k","source_account_id":"a","dest_account_id":"b","amount":"1.00","currency":"INR"}`},
		{http.MethodPost, "/api/payouts", `{"idempotency_key":"k","account_id":"a","amount":"1.00","currency":"INR","rail":"NACH"}`},
		{http.MethodPost, "/api/accounts", `{"idempotency_key":"k","owner_id":"o","currency":"INR"}`},
	}

	for _, route := range sagaRoutes {
		h := newHandler(t)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, gatewayRequest(route.method, route.path, "viewer", route.body))

		assert.Equal(t, http.StatusForbidden, rec.Code,
			"%s %s must be forbidden for a viewer", route.method, route.path)
	}
}

func TestUnknownRoleIsRejected(t *testing.T) {
	t.Parallel()

	h := newHandler(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, gatewayRequest(http.MethodGet, "/api/accounts?owner_id=o", "superuser", ""))

	assert.Equal(t, http.StatusForbidden, rec.Code,
		"a role the authorization layer does not understand must be refused, not allowed")
}

func TestEmptySubjectIsRejected(t *testing.T) {
	t.Parallel()

	h := newHandler(t)
	req := gatewayRequest(http.MethodGet, "/api/accounts?owner_id=o", "admin", "")
	req.Header.Del("X-Auth-Subject")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestCORS_UnlistedOriginGetsNoApproval(t *testing.T) {
	t.Parallel()

	h := newHandler(t) // no AllowedOrigins configured

	// Unauthenticated on purpose: CORS headers are decided before authentication,
	// so this exercises the origin check without reaching an upstream client.
	req := httptest.NewRequest(http.MethodGet, "/api/accounts?owner_id=o", nil)
	req.Header.Set("Origin", "https://evil.example.com")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"),
		"the money API must not answer Access-Control-Allow-Origin to any origin")
}

func TestCORS_AllowsConfiguredOrigin(t *testing.T) {
	t.Parallel()

	h := handler.New(handler.Clients{}, handler.Conns{}, handler.Config{
		StaticDir:            t.TempDir(),
		UpstreamSharedSecret: sharedSecret,
		RequireAuth:          true,
		AllowedOrigins:       []string{"https://dashboard.example.com"},
	}, zerolog.New(io.Discard))

	req := httptest.NewRequest(http.MethodOptions, "/api/accounts", nil)
	req.Header.Set("Origin", "https://dashboard.example.com")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, "https://dashboard.example.com", rec.Header().Get("Access-Control-Allow-Origin"))
}

// Development mode must remain possible, but only when explicitly requested.
func TestAuthCanBeDisabledForDevelopment(t *testing.T) {
	t.Parallel()

	h := handler.New(handler.Clients{}, handler.Conns{}, handler.Config{
		StaticDir:   t.TempDir(),
		RequireAuth: false,
	}, zerolog.New(io.Discard))

	// With no clients wired, an authorised request panics on the nil client
	// rather than being rejected — proving the auth layer let it through.
	require.Panics(t, func() {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/accounts?owner_id=o", nil))
	}, "with auth disabled the request should reach the (nil) upstream client")
}

func TestNonGETToUnknownPathReturnsJSON(t *testing.T) {
	t.Parallel()

	h := newHandler(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/auth/login", nil))

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "application/json",
		"an API miss must not return the HTML shell")
}

package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/prasham/ledger/services/gateway/internal/middleware"
)

func corsHandler(origins ...string) http.Handler {
	return middleware.CORS(middleware.CORSConfig{AllowedOrigins: origins}, okHandler())
}

func TestCORS_SameOriginRequestsNeedNoHeaders(t *testing.T) {
	t.Parallel()

	// A request with no Origin is same-origin; CORS headers are irrelevant.
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()
	corsHandler("https://dashboard.example.com").ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
}

func TestCORS_AllowsListedOrigin(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req.Header.Set("Origin", "https://dashboard.example.com")
	rec := httptest.NewRecorder()
	corsHandler("https://dashboard.example.com").ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	// The specific origin is echoed, not "*".
	assert.Equal(t, "https://dashboard.example.com", rec.Header().Get("Access-Control-Allow-Origin"))
	// Responses vary by origin, so a shared cache must not reuse them across origins.
	assert.Contains(t, rec.Header().Values("Vary"), "Origin")
}

// Previously every response carried Access-Control-Allow-Origin: *, including the
// money-moving API — so any web page a user visited could call it with a token it
// had obtained.
func TestCORS_BlocksUnlistedOrigin(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	rec := httptest.NewRecorder()
	corsHandler("https://dashboard.example.com").ServeHTTP(rec, req)

	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"),
		"an unlisted origin must receive no approval, so the browser blocks the response")
}

func TestCORS_RejectsPreflightFromUnlistedOrigin(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodOptions, "/api/accounts", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	rec := httptest.NewRecorder()
	corsHandler("https://dashboard.example.com").ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
}

func TestCORS_PreflightFromAllowedOriginReturns204(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodOptions, "/api/accounts", nil)
	req.Header.Set("Origin", "https://dashboard.example.com")
	rec := httptest.NewRecorder()
	corsHandler("https://dashboard.example.com").ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, "https://dashboard.example.com", rec.Header().Get("Access-Control-Allow-Origin"))
	assert.Contains(t, rec.Header().Get("Access-Control-Allow-Methods"), http.MethodPost)
	assert.Contains(t, rec.Header().Get("Access-Control-Allow-Headers"), "Authorization")
}

func TestCORS_WildcardIsOptIn(t *testing.T) {
	t.Parallel()

	// "*" remains available for local development, but only when configured.
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	rec := httptest.NewRecorder()
	corsHandler("*").ServeHTTP(rec, req)

	assert.Equal(t, "*", rec.Header().Get("Access-Control-Allow-Origin"))
}

func TestCORS_EmptyConfigAllowsNoCrossOrigin(t *testing.T) {
	t.Parallel()

	// The default is same-origin only: the SPA is served from this origin.
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req.Header.Set("Origin", "https://anywhere.example.com")
	rec := httptest.NewRecorder()
	corsHandler().ServeHTTP(rec, req)

	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
}

func TestSecurityHeaders(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	middleware.SecurityHeaders(okHandler()).ServeHTTP(rec, req)

	h := rec.Header()
	assert.Equal(t, "nosniff", h.Get("X-Content-Type-Options"))
	assert.Equal(t, "DENY", h.Get("X-Frame-Options"))
	assert.Equal(t, "strict-origin-when-cross-origin", h.Get("Referrer-Policy"))

	csp := h.Get("Content-Security-Policy")
	assert.Contains(t, csp, "default-src 'self'")
	assert.Contains(t, csp, "frame-ancestors 'none'")
	// Scripts must not be allowed inline: the dashboard holds a bearer token in
	// browser storage, so an injected script could exfiltrate it.
	assert.NotContains(t, csp, "script-src 'self' 'unsafe-inline'")
}

package middleware_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/prasham/ledger/services/gateway/internal/middleware"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func requestFrom(peer string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
	req.RemoteAddr = peer
	return req
}

func TestRateLimit_AllowsWithinBurst(t *testing.T) {
	t.Parallel()

	rl := middleware.NewRateLimiter(middleware.RateLimiterConfig{
		RequestsPerMinute: 600,
		Burst:             5,
	})
	defer rl.Close()
	h := rl.Middleware(okHandler())

	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, requestFrom("10.0.0.1:1234"))
		assert.Equal(t, http.StatusOK, rec.Code, "request %d within burst should pass", i+1)
	}
}

func TestRateLimit_RejectsBeyondBurst(t *testing.T) {
	t.Parallel()

	rl := middleware.NewRateLimiter(middleware.RateLimiterConfig{
		RequestsPerMinute: 60, // 1/s sustained
		Burst:             2,
	})
	defer rl.Close()
	h := rl.Middleware(okHandler())

	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, requestFrom("10.0.0.2:1234"))
		require.Equal(t, http.StatusOK, rec.Code)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, requestFrom("10.0.0.2:1234"))
	assert.Equal(t, http.StatusTooManyRequests, rec.Code)
	assert.Equal(t, "60", rec.Header().Get("Retry-After"))
}

func TestRateLimit_IsPerClient(t *testing.T) {
	t.Parallel()

	rl := middleware.NewRateLimiter(middleware.RateLimiterConfig{
		RequestsPerMinute: 60,
		Burst:             1,
	})
	defer rl.Close()
	h := rl.Middleware(okHandler())

	// Exhaust one client.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, requestFrom("10.0.0.3:1111"))
	require.Equal(t, http.StatusOK, rec.Code)

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, requestFrom("10.0.0.3:1111"))
	require.Equal(t, http.StatusTooManyRequests, rec.Code)

	// A different client still has its own allowance.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, requestFrom("10.0.0.4:2222"))
	assert.Equal(t, http.StatusOK, rec.Code)
}

// The bypass: X-Forwarded-For was trusted unconditionally, so rotating the header
// produced a fresh bucket per request — and since the limiter also guards
// /auth/login, credential brute-forcing was effectively unlimited.
func TestRateLimit_IgnoresForgedForwardedForByDefault(t *testing.T) {
	t.Parallel()

	rl := middleware.NewRateLimiter(middleware.RateLimiterConfig{
		RequestsPerMinute: 60,
		Burst:             2,
		// TrustProxyHeaders defaults to false.
	})
	defer rl.Close()
	h := rl.Middleware(okHandler())

	// Same real peer, a different forged X-Forwarded-For each time.
	var allowed, blocked int
	for i := 0; i < 10; i++ {
		req := requestFrom("10.0.0.5:1234")
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("203.0.113.%d", i))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code == http.StatusOK {
			allowed++
		} else {
			blocked++
		}
	}

	assert.Equal(t, 2, allowed, "only the burst should be allowed regardless of the forged header")
	assert.Equal(t, 8, blocked, "rotating X-Forwarded-For must not create new buckets")
}

func TestRateLimit_HonoursForwardedForFromATrustedProxy(t *testing.T) {
	t.Parallel()

	rl := middleware.NewRateLimiter(middleware.RateLimiterConfig{
		RequestsPerMinute: 60,
		Burst:             1,
		TrustProxyHeaders: true,
		TrustedProxyCIDRs: []string{"10.0.0.0/8"},
	})
	defer rl.Close()

	// From a trusted proxy, the forwarded client address identifies the client.
	req := requestFrom("10.0.0.9:1234")
	req.Header.Set("X-Forwarded-For", "203.0.113.7")
	assert.Equal(t, "203.0.113.7", rl.ClientKey(req))

	// From an untrusted peer, the header is ignored.
	req = requestFrom("198.51.100.4:1234")
	req.Header.Set("X-Forwarded-For", "203.0.113.7")
	assert.Equal(t, "198.51.100.4", rl.ClientKey(req),
		"an untrusted peer must not choose its own bucket")
}

func TestRateLimit_UsesRightmostUntrustedForwardedEntry(t *testing.T) {
	t.Parallel()

	rl := middleware.NewRateLimiter(middleware.RateLimiterConfig{
		TrustProxyHeaders: true,
		TrustedProxyCIDRs: []string{"10.0.0.0/8"},
	})
	defer rl.Close()

	// A client prepending its own forged entries cannot displace the real one:
	// entries to the left of the right-most untrusted address are forgeable.
	req := requestFrom("10.0.0.9:1234")
	req.Header.Set("X-Forwarded-For", "1.2.3.4, 5.6.7.8, 203.0.113.7, 10.0.0.9")
	assert.Equal(t, "203.0.113.7", rl.ClientKey(req))
}

// RATE_LIMIT_RPM below 10 previously produced burst == 0, and a zero-burst
// limiter refuses every request — a total lockout from a plausible config value.
func TestRateLimit_LowRPMDoesNotLockEveryoneOut(t *testing.T) {
	t.Parallel()

	for _, rpm := range []int{1, 5, 9} {
		rl := middleware.NewRateLimiter(middleware.RateLimiterConfig{RequestsPerMinute: rpm})
		h := rl.Middleware(okHandler())

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, requestFrom("10.0.0.6:1234"))
		assert.Equal(t, http.StatusOK, rec.Code,
			"rpm=%d must still allow at least one request", rpm)
		rl.Close()
	}
}

func TestRateLimit_ZeroConfigUsesSaneDefaults(t *testing.T) {
	t.Parallel()

	rl := middleware.NewRateLimiter(middleware.RateLimiterConfig{})
	defer rl.Close()
	h := rl.Middleware(okHandler())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, requestFrom("10.0.0.7:1234"))
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestRateLimit_HandlesRemoteAddrWithoutPort(t *testing.T) {
	t.Parallel()

	rl := middleware.NewRateLimiter(middleware.RateLimiterConfig{RequestsPerMinute: 600, Burst: 2})
	defer rl.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
	req.RemoteAddr = "10.0.0.8" // no port
	assert.Equal(t, "10.0.0.8", rl.ClientKey(req))
}

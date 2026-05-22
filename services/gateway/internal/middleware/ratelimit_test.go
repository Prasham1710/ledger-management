package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prasham/ledger/services/gateway/internal/middleware"
)

func TestRateLimiter_AllowsUnderLimit(t *testing.T) {
	rl := middleware.NewRateLimiter(600) // 10 req/sec burst=60
	handler := rl.Middleware(http.HandlerFunc(okHandler))

	for i := 0; i < 10; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req.RemoteAddr = "10.0.0.1:1234"
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("request %d: got %d, want 200", i+1, rr.Code)
		}
	}
}

func TestRateLimiter_Blocks_WhenExceeded(t *testing.T) {
	// 1 req/min → burst=0 (capped at 1 by implementation)
	rl := middleware.NewRateLimiter(1)
	handler := rl.Middleware(http.HandlerFunc(okHandler))

	blocked := false
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "10.0.0.2:9999"
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code == http.StatusTooManyRequests {
			blocked = true
			break
		}
	}
	if !blocked {
		t.Error("expected at least one 429 response")
	}
}

func TestRateLimiter_DifferentIPs_IndependentLimits(t *testing.T) {
	rl := middleware.NewRateLimiter(600)
	handler := rl.Middleware(http.HandlerFunc(okHandler))

	for _, ip := range []string{"10.0.0.3:1", "10.0.0.4:1", "10.0.0.5:1"} {
		req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
		req.RemoteAddr = ip
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("IP %s: got %d, want 200", ip, rr.Code)
		}
	}
}

func TestRateLimiter_RetryAfterHeader(t *testing.T) {
	rl := middleware.NewRateLimiter(1)
	handler := rl.Middleware(http.HandlerFunc(okHandler))

	var lastCode int
	for i := 0; i < 20; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "10.0.0.6:1"
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		lastCode = rr.Code
		if rr.Code == http.StatusTooManyRequests {
			if rr.Header().Get("Retry-After") == "" {
				t.Error("429 response missing Retry-After header")
			}
			return
		}
	}
	t.Errorf("never got 429; last status=%d", lastCode)
}

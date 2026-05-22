package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prasham/ledger/services/gateway/internal/middleware"
)

func TestCORS_SetsHeaders(t *testing.T) {
	handler := middleware.CORS(http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Allow-Origin: got %q, want *", got)
	}
}

func TestCORS_Preflight_Returns204(t *testing.T) {
	handler := middleware.CORS(http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodOptions, "/api/accounts", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Errorf("preflight: got %d, want 204", rr.Code)
	}
}

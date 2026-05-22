package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prasham/ledger/services/gateway/internal/auth"
	"github.com/prasham/ledger/services/gateway/internal/middleware"
)

func okHandler(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }

func makeToken(t *testing.T, secret string, ttl time.Duration, subject, role string) string {
	t.Helper()
	mgr := auth.NewManager(secret, ttl)
	tok, err := mgr.Sign(subject, role)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return tok
}

func TestAuth_NonAPIPath_NoTokenRequired(t *testing.T) {
	mgr := auth.NewManager("secret", time.Hour)
	handler := middleware.Auth(mgr)(http.HandlerFunc(okHandler))

	paths := []string{"/", "/auth/login", "/assets/index.js", "/healthz"}
	for _, path := range paths {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("path %q: got %d, want 200", path, rr.Code)
		}
	}
}

func TestAuth_APIPath_MissingToken_Returns401(t *testing.T) {
	mgr := auth.NewManager("secret", time.Hour)
	handler := middleware.Auth(mgr)(http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("got %d, want 401", rr.Code)
	}
}

func TestAuth_APIPath_ValidToken_Returns200(t *testing.T) {
	mgr := auth.NewManager("secret", time.Hour)
	tok := makeToken(t, "secret", time.Hour, "alice", "admin")
	handler := middleware.Auth(mgr)(http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("got %d, want 200", rr.Code)
	}
}

func TestAuth_APIPath_ExpiredToken_Returns401(t *testing.T) {
	mgr := auth.NewManager("secret", time.Hour)
	tok := makeToken(t, "secret", -time.Second, "alice", "admin")
	handler := middleware.Auth(mgr)(http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("got %d, want 401", rr.Code)
	}
}

func TestAuth_APIPath_WrongSecret_Returns401(t *testing.T) {
	tok := makeToken(t, "secret-a", time.Hour, "alice", "admin")
	mgr := auth.NewManager("secret-b", time.Hour) // different secret
	handler := middleware.Auth(mgr)(http.HandlerFunc(okHandler))

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("got %d, want 401", rr.Code)
	}
}

func TestAuth_HeadersForwarded(t *testing.T) {
	mgr := auth.NewManager("secret", time.Hour)
	tok := makeToken(t, "secret", time.Hour, "alice", "admin")

	var gotSubject, gotRole string
	capture := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		gotSubject = r.Header.Get("X-Auth-Subject")
		gotRole = r.Header.Get("X-Auth-Role")
	})
	handler := middleware.Auth(mgr)(capture)

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if gotSubject != "alice" {
		t.Errorf("X-Auth-Subject: got %q, want %q", gotSubject, "alice")
	}
	if gotRole != "admin" {
		t.Errorf("X-Auth-Role: got %q, want %q", gotRole, "admin")
	}
}

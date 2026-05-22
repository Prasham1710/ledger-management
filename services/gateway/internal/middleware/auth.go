package middleware

import (
	"net/http"
	"strings"

	"github.com/prasham/ledger/services/gateway/internal/auth"
)

func Auth(mgr *auth.Manager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Only /api/* routes require a JWT; everything else is public
			// (static assets, /auth/*, /healthz are all open).
			if !strings.HasPrefix(r.URL.Path, "/api/") {
				next.ServeHTTP(w, r)
				return
			}

			bearer := r.Header.Get("Authorization")
			if !strings.HasPrefix(bearer, "Bearer ") {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing token"})
				return
			}
			claims, err := mgr.Verify(strings.TrimPrefix(bearer, "Bearer "))
			if err != nil {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token"})
				return
			}
			// forward subject as internal header so upstream services can log it
			r.Header.Set("X-Auth-Subject", claims.Subject)
			r.Header.Set("X-Auth-Role", claims.Role)
			next.ServeHTTP(w, r)
		})
	}
}

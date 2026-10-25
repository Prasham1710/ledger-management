package middleware

import (
	"net/http"
	"strings"

	"github.com/prasham/ledger/services/gateway/internal/auth"
	"github.com/prasham/ledger/services/gateway/internal/handler"
)

// Internal identity headers, set from verified token claims and consumed by
// upstream services.
const (
	HeaderAuthSubject = "X-Auth-Subject"
	HeaderAuthRole    = "X-Auth-Role"
	// HeaderAuthVerified proves the request passed through this gateway. Upstreams
	// reject money-moving requests that lack it, so reaching ui-bff directly
	// cannot bypass authentication.
	HeaderAuthVerified = "X-Auth-Verified"
)

// publicPrefixes are paths served without a token: the login endpoints, probes,
// and static SPA assets.
var publicPrefixes = []string{
	"/auth/login",
	"/auth/refresh",
	"/healthz",
	"/readyz",
	"/assets/",
}

// Auth verifies bearer tokens for protected paths and attaches verified identity.
//
// sharedSecret is stamped into X-Auth-Verified so upstream services can
// distinguish a gateway-authenticated request from a direct one.
func Auth(mgr *auth.Manager, sharedSecret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Strip inbound identity headers unconditionally, on every path.
			//
			// The previous middleware only *set* these headers, and only for /api/
			// paths. A client could therefore send its own X-Auth-Role on any other
			// route and have it forwarded verbatim to an upstream that trusted it.
			// Deleting them first means a spoofed value can never survive, whether
			// or not this request is authenticated.
			r.Header.Del(HeaderAuthSubject)
			r.Header.Del(HeaderAuthRole)
			r.Header.Del(HeaderAuthVerified)

			if isPublic(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}

			raw, ok := handler.BearerToken(r)
			if !ok {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing token"})
				return
			}

			claims, err := mgr.Verify(raw)
			if err != nil {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token"})
				return
			}

			// Attach verified identity for upstream authorization.
			r.Header.Set(HeaderAuthSubject, claims.Subject)
			r.Header.Set(HeaderAuthRole, claims.Role)
			r.Header.Set(HeaderAuthVerified, sharedSecret)

			next.ServeHTTP(w, r)
		})
	}
}

// isPublic reports whether path is served without authentication.
//
// Everything under /api/ requires a token unless it appears in publicPrefixes.
// The SPA shell and its assets are public; the API it calls is not.
func isPublic(path string) bool {
	for _, prefix := range publicPrefixes {
		if path == prefix || strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return !strings.HasPrefix(path, "/api/")
}

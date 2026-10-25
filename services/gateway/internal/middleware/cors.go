package middleware

import (
	"net/http"
	"strings"
)

// CORSConfig configures cross-origin access.
type CORSConfig struct {
	// AllowedOrigins is the explicit allow-list. An entry of "*" permits any
	// origin, which is only appropriate for local development.
	AllowedOrigins []string
}

// CORS restricts cross-origin requests to an explicit allow-list.
//
// The previous implementation sent `Access-Control-Allow-Origin: *` on every
// response, including those of the money-moving API. That let any web page a
// user visited call this API with a token it had obtained, and made the browser's
// origin check no protection at all. An allow-list means only origins the
// operator names can reach the API from a browser.
//
// The response also varies by request origin, so `Vary: Origin` is required or a
// shared cache may serve one origin's CORS headers to another.
func CORS(cfg CORSConfig, next http.Handler) http.Handler {
	allowAll := false
	allowed := make(map[string]bool, len(cfg.AllowedOrigins))
	for _, origin := range cfg.AllowedOrigins {
		origin = strings.TrimSpace(origin)
		if origin == "*" {
			allowAll = true
			continue
		}
		if origin != "" {
			allowed[strings.ToLower(origin)] = true
		}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")

		// Same-origin requests carry no Origin header and need no CORS headers.
		if origin != "" {
			w.Header().Add("Vary", "Origin")

			switch {
			case allowAll:
				w.Header().Set("Access-Control-Allow-Origin", "*")
			case allowed[strings.ToLower(origin)]:
				// Echo the specific origin rather than "*", which is required when
				// credentials may be involved and avoids caching ambiguity.
				w.Header().Set("Access-Control-Allow-Origin", origin)
			default:
				// Not allowed: omit the header entirely so the browser blocks the
				// response. A preflight is answered without approval.
				if r.Method == http.MethodOptions {
					w.WriteHeader(http.StatusForbidden)
					return
				}
			}
		}

		if origin != "" && (allowAll || allowed[strings.ToLower(origin)]) {
			w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization,Content-Type,X-Request-ID")
			// Cache preflights so browsers do not re-ask on every call.
			w.Header().Set("Access-Control-Max-Age", "600")
		}

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// SecurityHeaders sets defensive response headers.
//
// These were absent entirely. The dashboard holds a bearer token in browser
// storage, so limiting what scripts can run and what can embed the page reduces
// the blast radius of any injected content.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		// Do not let the browser second-guess declared content types.
		h.Set("X-Content-Type-Options", "nosniff")
		// Disallow framing, which blocks clickjacking of the dashboard.
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		// Restrict script and connection sources. 'unsafe-inline' is required for
		// styles because the bundled SPA injects them; scripts are not exempted.
		h.Set("Content-Security-Policy",
			"default-src 'self'; "+
				"script-src 'self'; "+
				"style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' data:; "+
				"connect-src 'self'; "+
				"frame-ancestors 'none'; "+
				"base-uri 'self'; "+
				"form-action 'self'")
		h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")

		next.ServeHTTP(w, r)
	})
}

package middleware

import (
	"net/http"

	"github.com/rs/zerolog/log"
)

const headerRequestID = "X-Request-ID"

// maxRequestIDLen bounds an inbound request id. It is echoed into responses and
// into every log line for the request, so an unbounded client-supplied value is
// a log-injection and log-volume vector.
const maxRequestIDLen = 64

// RequestID propagates or generates a request identifier and binds it to the
// request's logger.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := sanitizeRequestID(r.Header.Get(headerRequestID))
		if id == "" {
			id = genID()
		}
		// Normalise the header so upstreams see the sanitized value, not the raw
		// one the client sent.
		r.Header.Set(headerRequestID, id)
		w.Header().Set(headerRequestID, id)
		r = r.WithContext(log.With().Str("request_id", id).Logger().WithContext(r.Context()))
		next.ServeHTTP(w, r)
	})
}

// sanitizeRequestID keeps only characters safe to embed in logs, and bounds the
// length. Returns "" when nothing usable remains, so a fresh id is generated.
func sanitizeRequestID(raw string) string {
	if raw == "" {
		return ""
	}
	if len(raw) > maxRequestIDLen {
		raw = raw[:maxRequestIDLen]
	}
	out := make([]byte, 0, len(raw))
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
			out = append(out, c)
		case c == '-', c == '_', c == '.':
			out = append(out, c)
		default:
			// Drop anything else, notably newlines and control characters, which
			// could otherwise forge additional log entries.
		}
	}
	return string(out)
}

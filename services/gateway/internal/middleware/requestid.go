package middleware

import (
	"net/http"

	"github.com/rs/zerolog/log"
)

const headerRequestID = "X-Request-ID"

func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(headerRequestID)
		if id == "" {
			id = genID()
		}
		w.Header().Set(headerRequestID, id)
		r = r.WithContext(log.With().Str("request_id", id).Logger().WithContext(r.Context()))
		next.ServeHTTP(w, r)
	})
}

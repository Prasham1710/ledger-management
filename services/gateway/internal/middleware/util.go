package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
)

// genID returns a random request identifier.
//
// It uses crypto/rand rather than math/rand. The previous implementation used
// math/rand/v2, while .golangci.yml excluded the gosec weak-randomness check with
// the comment "we use crypto/rand for IDs, not math/rand" — the opposite of what
// the code did. A predictable request id lets an observer correlate or forge log
// entries, so the comment's claim is now true.
func genID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand does not fail in practice; if it ever does, an empty id is
		// preferable to a predictable one.
		return ""
	}
	return hex.EncodeToString(buf)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

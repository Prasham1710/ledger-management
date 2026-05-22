package middleware

import (
	"encoding/json"
	"math/rand/v2"
	"net/http"
	"strconv"
)

func genID() string {
	return strconv.FormatUint(rand.Uint64(), 36)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

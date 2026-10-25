package handler

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/prasham/ledger/services/gateway/internal/auth"
)

// maxLoginBodyBytes bounds the login request body. Without a limit the JSON
// decoder reads whatever a client sends.
const maxLoginBodyBytes = 4 << 10 // 4 KiB

// AuthHandler issues and refreshes access tokens.
type AuthHandler struct {
	mgr      *auth.Manager
	username string
	password string
	log      zerolog.Logger
}

func NewAuthHandler(mgr *auth.Manager, username, password string, log zerolog.Logger) *AuthHandler {
	return &AuthHandler{mgr: mgr, username: username, password: password, log: log}
}

// Login exchanges credentials for an access token.
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}

	if !h.credentialsMatch(req.Username, req.Password) {
		// One generic message for a bad username and a bad password alike, so the
		// response cannot be used to enumerate valid usernames.
		h.log.Warn().Str("remote_addr", r.RemoteAddr).Msg("failed login attempt")
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
		return
	}

	token, err := h.mgr.Sign(req.Username, auth.RoleAdmin)
	if err != nil {
		h.log.Error().Err(err).Msg("signing token failed")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "token generation failed"})
		return
	}

	h.log.Info().Str("subject", req.Username).Msg("login succeeded")
	h.writeToken(w, token)
}

// credentialsMatch compares credentials in constant time.
//
// The previous implementation used `!=` on both fields, which short-circuits and
// compares byte by byte, leaking timing information about how much of the
// password was correct. Both comparisons run unconditionally here, so neither the
// result nor the duration depends on where the first difference lies.
func (h *AuthHandler) credentialsMatch(username, password string) bool {
	userMatch := subtle.ConstantTimeCompare([]byte(username), []byte(h.username)) == 1
	passMatch := subtle.ConstantTimeCompare([]byte(password), []byte(h.password)) == 1
	return userMatch && passMatch
}

// Refresh re-issues a token for an already-valid one.
func (h *AuthHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	raw, ok := BearerToken(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing token"})
		return
	}

	claims, err := h.mgr.Verify(raw)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token"})
		return
	}

	// Refresh cannot extend a session indefinitely: the manager enforces an
	// absolute lifetime measured from the original login, so a leaked token
	// cannot be rolled forward forever.
	token, err := h.mgr.Refresh(claims)
	if err != nil {
		if errors.Is(err, auth.ErrSessionExpired) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{
				"error": "session lifetime exceeded — sign in again",
			})
			return
		}
		h.log.Error().Err(err).Msg("refreshing token failed")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "token generation failed"})
		return
	}

	h.writeToken(w, token)
}

// writeToken reports the token with its real lifetime.
//
// expires_in comes from the manager rather than a literal: the previous handler
// always reported 3600 regardless of the configured TTL, so a client's refresh
// timing silently diverged from reality whenever the TTL changed.
func (h *AuthHandler) writeToken(w http.ResponseWriter, token string) {
	writeJSON(w, http.StatusOK, map[string]any{
		"token":      token,
		"token_type": "Bearer",
		"expires_in": int(h.mgr.TTL() / time.Second),
	})
}

// BearerToken extracts a bearer token from the Authorization header.
//
// It validates the scheme rather than slicing at a fixed offset: the previous
// code did bearer[7:] after only checking the header was at least 8 characters,
// so any 8-character value was treated as a token.
func BearerToken(r *http.Request) (string, bool) {
	header := r.Header.Get("Authorization")
	if header == "" {
		return "", false
	}
	const prefix = "Bearer "
	// Scheme comparison is case-insensitive per RFC 7235.
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", false
	}
	token := strings.TrimSpace(header[len(prefix):])
	if token == "" {
		return "", false
	}
	return token, true
}

// decodeJSON reads a bounded, strict JSON body.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxLoginBodyBytes)
	dec := json.NewDecoder(r.Body)
	// Reject unknown fields so a client-side typo is reported rather than
	// silently ignored.
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err //nolint:wrapcheck // caller maps this to a 400
	}
	// Exactly one JSON value is expected.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("body must contain a single JSON object")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

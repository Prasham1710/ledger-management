package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Roles recognised by the platform.
const (
	// RoleAdmin may read and mutate anything, including moving money between
	// arbitrary accounts.
	RoleAdmin = "admin"
	// RoleOperator may read anything and initiate payments, but may not adjust
	// balances directly.
	RoleOperator = "operator"
	// RoleViewer is read-only.
	RoleViewer = "viewer"
)

// issuer identifies tokens minted by this gateway.
const issuer = "ledger-gateway"

// Errors returned by the manager.
var (
	// ErrSessionExpired means the absolute session lifetime has elapsed and the
	// holder must authenticate again.
	ErrSessionExpired = errors.New("session lifetime exceeded")
	// ErrWeakSecret means the signing secret is too short or is a known default.
	ErrWeakSecret = errors.New("jwt secret is unsuitable")
)

// minSecretLen is the shortest HMAC secret accepted. HS256 keys shorter than the
// hash output are brute-forceable.
const minSecretLen = 32

// knownDefaults are placeholder secrets that shipped in configuration files. A
// deployment that still uses one can be signed into by anyone who has read the
// repository, so they are rejected outright.
var knownDefaults = map[string]bool{
	"change-me-in-production":                    true,
	"super-secret-ledger-jwt-key-change-in-prod": true,
	"secret":   true,
	"changeme": true,
}

// Claims is the token payload.
type Claims struct {
	Role string `json:"role"`
	// SessionStart is when the holder originally authenticated. Refresh carries
	// it forward unchanged, which is what bounds the total session lifetime:
	// without it, a leaked token could be refreshed indefinitely.
	SessionStart int64 `json:"sid_iat"`
	jwt.RegisteredClaims
}

// Manager signs and verifies access tokens.
type Manager struct {
	secret []byte
	ttl    time.Duration
	// maxSessionAge bounds how long a session may be extended by refreshing.
	maxSessionAge time.Duration
}

// Config configures the token manager.
type Config struct {
	Secret string
	// TTL is the lifetime of an individual token.
	TTL time.Duration
	// MaxSessionAge bounds the total session, across refreshes. Zero applies a
	// 12-hour default.
	MaxSessionAge time.Duration
	// AllowWeakSecret skips secret validation. Intended for local development
	// only; NewManager refuses weak secrets otherwise.
	AllowWeakSecret bool
}

// NewManager builds a Manager, rejecting unsuitable secrets.
//
// Validation exists because the signing secret had a compiled-in default of
// "change-me-in-production" and the committed Helm values shipped another
// placeholder. Either lets anyone who has read the repository mint an admin
// token, so an unsuitable secret must prevent startup rather than warn.
func NewManager(cfg Config) (*Manager, error) {
	if cfg.TTL <= 0 {
		cfg.TTL = time.Hour
	}
	if cfg.MaxSessionAge <= 0 {
		cfg.MaxSessionAge = 12 * time.Hour
	}
	if cfg.MaxSessionAge < cfg.TTL {
		return nil, fmt.Errorf("auth: max session age (%s) must be at least the token TTL (%s)",
			cfg.MaxSessionAge, cfg.TTL)
	}

	if !cfg.AllowWeakSecret {
		if err := validateSecret(cfg.Secret); err != nil {
			return nil, err
		}
	}

	return &Manager{
		secret:        []byte(cfg.Secret),
		ttl:           cfg.TTL,
		maxSessionAge: cfg.MaxSessionAge,
	}, nil
}

func validateSecret(secret string) error {
	if len(secret) < minSecretLen {
		return fmt.Errorf("%w: must be at least %d characters, got %d",
			ErrWeakSecret, minSecretLen, len(secret))
	}
	if knownDefaults[secret] {
		return fmt.Errorf("%w: this is a placeholder value published in the repository", ErrWeakSecret)
	}
	return nil
}

// TTL reports the individual token lifetime, so handlers can report expires_in
// accurately instead of hardcoding it.
func (m *Manager) TTL() time.Duration { return m.ttl }

// Sign issues a token for a new session.
func (m *Manager) Sign(subject, role string) (string, error) {
	if err := ValidateRole(role); err != nil {
		return "", err
	}
	now := time.Now()
	return m.sign(subject, role, now, now)
}

// Refresh re-issues a token, preserving the original session start so the
// absolute lifetime still applies.
func (m *Manager) Refresh(claims *Claims) (string, error) {
	sessionStart := time.Unix(claims.SessionStart, 0)
	if claims.SessionStart == 0 {
		// A token minted before session tracking existed. Treat its issue time as
		// the session start rather than granting an unbounded session.
		if claims.IssuedAt != nil {
			sessionStart = claims.IssuedAt.Time
		} else {
			sessionStart = time.Now()
		}
	}

	if age := time.Since(sessionStart); age > m.maxSessionAge {
		return "", fmt.Errorf("%w: session began %s ago, limit is %s",
			ErrSessionExpired, age.Truncate(time.Second), m.maxSessionAge)
	}

	return m.sign(claims.Subject, claims.Role, time.Now(), sessionStart)
}

func (m *Manager) sign(subject, role string, now, sessionStart time.Time) (string, error) {
	claims := Claims{
		Role:         role,
		SessionStart: sessionStart.Unix(),
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   subject,
			Issuer:    issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(m.ttl)),
		},
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.secret)
	if err != nil {
		return "", fmt.Errorf("auth: sign token: %w", err)
	}
	return signed, nil
}

// Verify parses and validates a token.
func (m *Manager) Verify(tokenStr string) (*Claims, error) {
	tok, err := jwt.ParseWithClaims(tokenStr, &Claims{},
		func(t *jwt.Token) (any, error) {
			// Pin the algorithm. Accepting whatever the token declares is how
			// algorithm-confusion attacks work.
			if t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
				return nil, fmt.Errorf("unexpected signing method %q", t.Method.Alg())
			}
			return m.secret, nil
		},
		// Require the claims that make a token unambiguous.
		jwt.WithIssuer(issuer),
		jwt.WithExpirationRequired(),
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
	)
	if err != nil {
		return nil, fmt.Errorf("auth: verify token: %w", err)
	}

	claims, ok := tok.Claims.(*Claims)
	if !ok || !tok.Valid {
		return nil, errors.New("auth: invalid token")
	}
	if claims.Subject == "" {
		return nil, errors.New("auth: token has no subject")
	}
	if err := ValidateRole(claims.Role); err != nil {
		return nil, err
	}

	// Enforce the absolute session lifetime on every request, not only on
	// refresh, so a token issued just before the limit cannot outlive it.
	if claims.SessionStart != 0 {
		if age := time.Since(time.Unix(claims.SessionStart, 0)); age > m.maxSessionAge {
			return nil, fmt.Errorf("%w: session began %s ago", ErrSessionExpired, age.Truncate(time.Second))
		}
	}

	return claims, nil
}

// ValidateRole rejects unknown roles, so a token cannot carry a role the
// authorization layer does not understand.
func ValidateRole(role string) error {
	switch role {
	case RoleAdmin, RoleOperator, RoleViewer:
		return nil
	default:
		return fmt.Errorf("auth: unknown role %q", role)
	}
}

// CanMutate reports whether role may change state.
func CanMutate(role string) bool {
	return role == RoleAdmin || role == RoleOperator
}

// CanAdjustBalances reports whether role may credit or debit an account
// directly, bypassing the payment saga. This is the most privileged operation
// the platform exposes: it creates or destroys money.
func CanAdjustBalances(role string) bool {
	return role == RoleAdmin
}

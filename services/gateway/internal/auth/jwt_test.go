package auth_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/prasham/ledger/services/gateway/internal/auth"
)

// testSecret is long enough to satisfy the minimum and is not a known default.
const testSecret = "test-secret-that-is-long-enough-for-hs256"

func newManager(t *testing.T, ttl, maxAge time.Duration) *auth.Manager {
	t.Helper()
	mgr, err := auth.NewManager(auth.Config{
		Secret:        testSecret,
		TTL:           ttl,
		MaxSessionAge: maxAge,
	})
	require.NoError(t, err)
	return mgr
}

func TestSignAndVerify(t *testing.T) {
	t.Parallel()

	mgr := newManager(t, time.Hour, 12*time.Hour)

	token, err := mgr.Sign("alice", auth.RoleAdmin)
	require.NoError(t, err)
	require.NotEmpty(t, token)

	claims, err := mgr.Verify(token)
	require.NoError(t, err)
	assert.Equal(t, "alice", claims.Subject)
	assert.Equal(t, auth.RoleAdmin, claims.Role)
	assert.NotZero(t, claims.SessionStart, "session start must be recorded to bound refresh")
}

func TestVerify_RejectsExpiredToken(t *testing.T) {
	t.Parallel()

	// A 1ms TTL expires before Verify runs. A negative TTL cannot be used here:
	// NewManager normalises a non-positive TTL to its default rather than
	// producing a manager that only ever mints dead tokens.
	mgr := newManager(t, time.Millisecond, time.Hour)
	token, err := mgr.Sign("alice", auth.RoleAdmin)
	require.NoError(t, err)

	time.Sleep(20 * time.Millisecond)

	_, err = mgr.Verify(token)
	require.Error(t, err, "an expired token must not verify")
}

func TestVerify_RejectsTokenSignedWithAnotherSecret(t *testing.T) {
	t.Parallel()

	signer := newManager(t, time.Hour, time.Hour)
	token, err := signer.Sign("alice", auth.RoleAdmin)
	require.NoError(t, err)

	other, err := auth.NewManager(auth.Config{
		Secret:        "a-completely-different-secret-of-sufficient-length",
		TTL:           time.Hour,
		MaxSessionAge: time.Hour,
	})
	require.NoError(t, err)

	_, err = other.Verify(token)
	require.Error(t, err, "a token signed with a different secret must not verify")
}

func TestVerify_RejectsGarbage(t *testing.T) {
	t.Parallel()

	mgr := newManager(t, time.Hour, time.Hour)

	for _, token := range []string{"", "not.a.token", "a.b.c", "Bearer something"} {
		_, err := mgr.Verify(token)
		assert.Error(t, err, "token %q must not verify", token)
	}
}

// A signing secret that is a published placeholder lets anyone who has read the
// repository mint an admin token, so it must prevent startup.
func TestNewManager_RejectsWeakSecrets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		secret string
	}{
		{name: "empty", secret: ""},
		{name: "too short", secret: "short"},
		{name: "compiled-in default", secret: "change-me-in-production"},
		{name: "committed helm placeholder", secret: "super-secret-ledger-jwt-key-change-in-prod"},
		{name: "obvious placeholder", secret: "changeme"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := auth.NewManager(auth.Config{
				Secret:        tc.secret,
				TTL:           time.Hour,
				MaxSessionAge: time.Hour,
			})
			require.Error(t, err)
			assert.ErrorIs(t, err, auth.ErrWeakSecret)
		})
	}
}

func TestNewManager_AllowWeakSecretEscapeHatch(t *testing.T) {
	t.Parallel()

	// Local development may opt out explicitly; the default must not.
	mgr, err := auth.NewManager(auth.Config{
		Secret:          "short",
		TTL:             time.Hour,
		MaxSessionAge:   time.Hour,
		AllowWeakSecret: true,
	})
	require.NoError(t, err)
	require.NotNil(t, mgr)
}

func TestNewManager_RejectsSessionShorterThanTTL(t *testing.T) {
	t.Parallel()

	_, err := auth.NewManager(auth.Config{
		Secret:        testSecret,
		TTL:           time.Hour,
		MaxSessionAge: time.Minute,
	})
	require.Error(t, err, "a session shorter than one token's life is a misconfiguration")
}

func TestRefresh_PreservesSessionStart(t *testing.T) {
	t.Parallel()

	mgr := newManager(t, time.Hour, 12*time.Hour)

	token, err := mgr.Sign("alice", auth.RoleOperator)
	require.NoError(t, err)
	original, err := mgr.Verify(token)
	require.NoError(t, err)

	refreshed, err := mgr.Refresh(original)
	require.NoError(t, err)

	claims, err := mgr.Verify(refreshed)
	require.NoError(t, err)
	assert.Equal(t, original.SessionStart, claims.SessionStart,
		"refresh must carry the original session start forward, or the session becomes unbounded")
	assert.Equal(t, auth.RoleOperator, claims.Role, "refresh must not escalate the role")
	assert.Equal(t, "alice", claims.Subject)
}

// Without an absolute lifetime, a leaked token could be refreshed forever.
func TestRefresh_RejectsSessionPastAbsoluteLifetime(t *testing.T) {
	t.Parallel()

	mgr := newManager(t, time.Hour, time.Hour)

	claims := &auth.Claims{
		Role: auth.RoleAdmin,
		// A session that began well beyond the limit.
		SessionStart: time.Now().Add(-25 * time.Hour).Unix(),
	}
	claims.Subject = "alice"

	_, err := mgr.Refresh(claims)
	require.Error(t, err)
	assert.ErrorIs(t, err, auth.ErrSessionExpired)
}

func TestSign_RejectsUnknownRole(t *testing.T) {
	t.Parallel()

	mgr := newManager(t, time.Hour, time.Hour)

	_, err := mgr.Sign("alice", "superuser")
	require.Error(t, err, "an unrecognised role must not be signed")
}

func TestTTL_IsReported(t *testing.T) {
	t.Parallel()

	mgr := newManager(t, 42*time.Minute, time.Hour)
	// Handlers report expires_in from this rather than a hardcoded literal.
	assert.Equal(t, 42*time.Minute, mgr.TTL())
}

func TestRolePermissions(t *testing.T) {
	t.Parallel()

	t.Run("mutation", func(t *testing.T) {
		t.Parallel()
		assert.True(t, auth.CanMutate(auth.RoleAdmin))
		assert.True(t, auth.CanMutate(auth.RoleOperator))
		assert.False(t, auth.CanMutate(auth.RoleViewer))
	})

	t.Run("direct balance adjustment is admin-only", func(t *testing.T) {
		t.Parallel()
		// Crediting an account outside the payment saga creates money, so it is
		// the most privileged operation the platform exposes.
		assert.True(t, auth.CanAdjustBalances(auth.RoleAdmin))
		assert.False(t, auth.CanAdjustBalances(auth.RoleOperator))
		assert.False(t, auth.CanAdjustBalances(auth.RoleViewer))
	})
}

func TestValidateRole(t *testing.T) {
	t.Parallel()

	for _, role := range []string{auth.RoleAdmin, auth.RoleOperator, auth.RoleViewer} {
		assert.NoError(t, auth.ValidateRole(role))
	}
	for _, role := range []string{"", "root", "ADMIN", "superuser"} {
		assert.Error(t, auth.ValidateRole(role), "role %q must be rejected", role)
	}
}

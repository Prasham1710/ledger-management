// Package handler serves the dashboard's HTTP API by fanning out to the gRPC
// services.
package handler

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// Identity headers set by the gateway from verified token claims.
const (
	HeaderAuthSubject  = "X-Auth-Subject"
	HeaderAuthRole     = "X-Auth-Role"
	HeaderAuthVerified = "X-Auth-Verified"
)

// Roles, mirroring the gateway's definitions.
const (
	RoleAdmin    = "admin"
	RoleOperator = "operator"
	RoleViewer   = "viewer"
)

// Identity is the authenticated caller.
type Identity struct {
	Subject string
	Role    string
}

// CanMutate reports whether the caller may change state.
func (i Identity) CanMutate() bool {
	return i.Role == RoleAdmin || i.Role == RoleOperator
}

// CanAdjustBalances reports whether the caller may credit or debit an account
// directly, outside the payment saga. That creates or destroys money, so it is
// restricted to admins.
func (i Identity) CanAdjustBalances() bool {
	return i.Role == RoleAdmin
}

// authenticator verifies that a request arrived through the gateway and carries
// verified identity.
//
// This exists because ui-bff had no authentication whatsoever. It served
// POST /api/accounts/{id}/credit, /debit, /api/transactions, /api/payouts and
// /api/ledger/entries on port 8090 with no token check, did not read the
// X-Auth-* headers the gateway set, and answered `Access-Control-Allow-Origin: *`.
// Authentication lived only at the edge, so anything able to reach the service
// port — any pod in the cluster, given the Helm Service — could mint balance.
//
// Defence in depth: the gateway still authenticates, and this layer independently
// refuses requests that did not come through it.
type authenticator struct {
	// sharedSecret must match the gateway's GATEWAY_UPSTREAM_SECRET.
	sharedSecret string
	// required, when false, permits unauthenticated requests. Intended only for
	// local single-process development; Handler logs loudly when it is off.
	required bool
}

// authenticate extracts the verified identity, or reports why it could not.
func (a authenticator) authenticate(r *http.Request) (Identity, error) {
	verified := r.Header.Get(HeaderAuthVerified)
	subject := strings.TrimSpace(r.Header.Get(HeaderAuthSubject))
	role := strings.TrimSpace(r.Header.Get(HeaderAuthRole))

	if !a.required {
		// Development mode: accept whatever is present, defaulting to admin so
		// the dashboard remains usable without a gateway.
		if role == "" {
			role = RoleAdmin
		}
		if subject == "" {
			subject = "dev"
		}
		return Identity{Subject: subject, Role: role}, nil
	}

	// Constant-time comparison: this value gates every mutating endpoint.
	if subtle.ConstantTimeCompare([]byte(verified), []byte(a.sharedSecret)) != 1 {
		return Identity{}, errUnauthenticated
	}
	if subject == "" || role == "" {
		return Identity{}, errUnauthenticated
	}
	switch role {
	case RoleAdmin, RoleOperator, RoleViewer:
	default:
		return Identity{}, errForbidden
	}

	return Identity{Subject: subject, Role: role}, nil
}

package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/rs/zerolog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	accountv1 "github.com/prasham/ledger/gen/account/v1"
	fxv1 "github.com/prasham/ledger/gen/fx/v1"
	ledgerv1 "github.com/prasham/ledger/gen/ledger/v1"
	payoutv1 "github.com/prasham/ledger/gen/payout/v1"
	txnv1 "github.com/prasham/ledger/gen/transaction/v1"
)

// maxRequestBodyBytes bounds every JSON request body. Without a limit the
// decoder reads whatever a client sends.
const maxRequestBodyBytes = 64 << 10 // 64 KiB

// Sentinel errors for the authorization layer.
var (
	errUnauthenticated = errors.New("unauthenticated")
	errForbidden       = errors.New("forbidden")
)

var pj = protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: false}

// Clients bundles the gRPC clients the dashboard fans out to.
type Clients struct {
	Ledger  ledgerv1.LedgerServiceClient
	Account accountv1.AccountServiceClient
	FX      fxv1.FXServiceClient
	Txn     txnv1.TransactionServiceClient
	Payout  payoutv1.PayoutServiceClient
}

// Conns are the underlying connections, used for health reporting. Inspecting
// the connections this process already holds is both accurate and free, unlike
// probing each service's metrics port over HTTP.
type Conns struct {
	Ledger  grpcConn
	Account grpcConn
	FX      grpcConn
	Txn     grpcConn
	Payout  grpcConn
}

// Config configures the handler.
type Config struct {
	StaticDir string
	// UpstreamSharedSecret must match the gateway's GATEWAY_UPSTREAM_SECRET.
	UpstreamSharedSecret string
	// RequireAuth enforces gateway authentication. Disable only for local
	// single-process development.
	RequireAuth bool
	// AllowedOrigins restricts cross-origin browser access.
	AllowedOrigins []string
}

type Handler struct {
	mux      *http.ServeMux
	clients  Clients
	conns    Conns
	auth     authenticator
	static   string
	origins  map[string]bool
	allowAll bool
	log      zerolog.Logger
}

func New(clients Clients, conns Conns, cfg Config, log zerolog.Logger) *Handler {
	h := &Handler{
		mux:     http.NewServeMux(),
		clients: clients,
		conns:   conns,
		auth: authenticator{
			sharedSecret: cfg.UpstreamSharedSecret,
			required:     cfg.RequireAuth,
		},
		static:  cfg.StaticDir,
		origins: make(map[string]bool, len(cfg.AllowedOrigins)),
		log:     log,
	}
	for _, origin := range cfg.AllowedOrigins {
		origin = strings.TrimSpace(origin)
		if origin == "*" {
			h.allowAll = true
			continue
		}
		if origin != "" {
			h.origins[strings.ToLower(origin)] = true
		}
	}
	h.routes()
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Cross-origin access is restricted to a configured allow-list. The previous
	// handler answered `Access-Control-Allow-Origin: *` on every response,
	// including the money-moving endpoints.
	if origin := r.Header.Get("Origin"); origin != "" {
		w.Header().Add("Vary", "Origin")
		permitted := h.allowAll || h.origins[strings.ToLower(origin)]
		switch {
		case h.allowAll:
			w.Header().Set("Access-Control-Allow-Origin", "*")
		case permitted:
			w.Header().Set("Access-Control-Allow-Origin", origin)
		case r.Method == http.MethodOptions:
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if permitted {
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Request-ID")
		}
	}

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	w.Header().Set("X-Content-Type-Options", "nosniff")
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) routes() {
	h.mux.HandleFunc("GET /api/health", h.health)

	// Reads require any authenticated role.
	h.mux.HandleFunc("GET /api/accounts", h.guard(RoleViewer, h.listAccounts))
	h.mux.HandleFunc("GET /api/accounts/{id}", h.guard(RoleViewer, h.getAccount))
	h.mux.HandleFunc("GET /api/ledger/{account_id}/balance", h.guard(RoleViewer, h.getBalance))
	h.mux.HandleFunc("GET /api/ledger/{account_id}/entries", h.guard(RoleViewer, h.listEntries))
	h.mux.HandleFunc("GET /api/transactions/{id}", h.guard(RoleViewer, h.getTransaction))
	h.mux.HandleFunc("GET /api/transactions/{id}/trace", h.guard(RoleViewer, h.getTransactionTrace))
	h.mux.HandleFunc("GET /api/payouts/{id}", h.guard(RoleViewer, h.getPayout))

	// Money movement through a saga requires operator or above.
	h.mux.HandleFunc("POST /api/accounts", h.guard(RoleOperator, h.createAccount))
	h.mux.HandleFunc("POST /api/accounts/{id}/holds", h.guard(RoleOperator, h.placeHold))
	h.mux.HandleFunc("DELETE /api/accounts/{id}/holds/{hold_id}", h.guard(RoleOperator, h.releaseHold))
	h.mux.HandleFunc("POST /api/transactions", h.guard(RoleOperator, h.initiatePayment))
	h.mux.HandleFunc("POST /api/payouts", h.guard(RoleOperator, h.initiatePayout))
	h.mux.HandleFunc("POST /api/fx/quote", h.guard(RoleOperator, h.getQuote))
	h.mux.HandleFunc("POST /api/fx/quote/{id}/lock", h.guard(RoleOperator, h.lockQuote))

	// Direct balance adjustment and raw ledger posting create or destroy money
	// without a saga, so they are admin-only.
	h.mux.HandleFunc("POST /api/accounts/{id}/credit", h.guard(RoleAdmin, h.creditAccount))
	h.mux.HandleFunc("POST /api/accounts/{id}/debit", h.guard(RoleAdmin, h.debitAccount))
	h.mux.HandleFunc("POST /api/ledger/entries", h.guard(RoleAdmin, h.postEntry))

	fs := http.FileServer(http.Dir(h.static))
	h.mux.Handle("/assets/", fs)
	h.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Non-GET requests to unknown paths are API misses — return JSON, not HTML.
		if r.Method != http.MethodGet {
			writeError(w, http.StatusNotFound, "not found — use the gateway on :8080")
			return
		}
		http.ServeFile(w, r, h.static+"/index.html")
	})
}

// authedHandler is a handler with an authenticated identity available.
type authedHandler func(http.ResponseWriter, *http.Request, Identity)

// guard authenticates the request and enforces a minimum role.
func (h *Handler) guard(minRole string, next authedHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, err := h.auth.authenticate(r)
		if err != nil {
			if errors.Is(err, errForbidden) {
				writeError(w, http.StatusForbidden, "role not permitted")
				return
			}
			h.log.Warn().
				Str("path", r.URL.Path).
				Str("remote_addr", r.RemoteAddr).
				Msg("rejected unauthenticated request — requests must arrive through the gateway")
			writeError(w, http.StatusUnauthorized,
				"unauthenticated — requests must be made through the gateway")
			return
		}

		if !roleAtLeast(identity.Role, minRole) {
			h.log.Warn().
				Str("subject", identity.Subject).
				Str("role", identity.Role).
				Str("required", minRole).
				Str("path", r.URL.Path).
				Msg("rejected request: insufficient role")
			writeError(w, http.StatusForbidden, "your role does not permit this operation")
			return
		}

		next(w, r, identity)
	}
}

// roleRank orders roles by privilege so a minimum can be expressed.
var roleRank = map[string]int{
	RoleViewer:   1,
	RoleOperator: 2,
	RoleAdmin:    3,
}

func roleAtLeast(have, want string) bool {
	haveRank, ok := roleRank[have]
	if !ok {
		return false
	}
	wantRank, ok := roleRank[want]
	if !ok {
		// Unknown requirement: refuse rather than allow.
		return false
	}
	return haveRank >= wantRank
}

// ── response helpers ──────────────────────────────────────────────────────────

func writeProto(w http.ResponseWriter, code int, m proto.Message) {
	b, err := pj.Marshal(m)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not encode response")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write(b)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// writeGRPCError translates an upstream gRPC error into an HTTP response,
// logging the detail and returning only what is safe to expose.
//
// The previous handlers passed err.Error() straight to the client, so Postgres
// constraint text, schema names, and internal SQL reached the browser. Client
// errors keep their message because it is actionable; server faults are reduced
// to a generic string and recorded server-side.
func (h *Handler) writeGRPCError(w http.ResponseWriter, r *http.Request, err error) {
	st := status.Convert(err)
	httpCode := grpcToHTTP(st.Code())

	if httpCode >= http.StatusInternalServerError {
		h.log.Error().
			Err(err).
			Str("path", r.URL.Path).
			Str("grpc_code", st.Code().String()).
			Msg("upstream call failed")
		writeError(w, httpCode, "upstream service error")
		return
	}

	// A 4xx message describes what the caller did wrong and is safe to return.
	h.log.Debug().
		Str("path", r.URL.Path).
		Str("grpc_code", st.Code().String()).
		Msg("upstream rejected request")
	writeError(w, httpCode, st.Message())
}

func grpcToHTTP(code codes.Code) int {
	switch code {
	case codes.OK:
		return http.StatusOK
	case codes.InvalidArgument, codes.OutOfRange:
		return http.StatusBadRequest
	case codes.NotFound:
		return http.StatusNotFound
	case codes.AlreadyExists:
		return http.StatusConflict
	case codes.FailedPrecondition:
		return http.StatusUnprocessableEntity
	case codes.Unauthenticated:
		return http.StatusUnauthorized
	case codes.PermissionDenied:
		return http.StatusForbidden
	case codes.ResourceExhausted:
		return http.StatusTooManyRequests
	case codes.Unavailable:
		return http.StatusServiceUnavailable
	case codes.DeadlineExceeded:
		return http.StatusGatewayTimeout
	case codes.Aborted:
		// The idempotency guard reports an in-flight duplicate this way; the
		// client should retry shortly.
		return http.StatusConflict
	case codes.Unimplemented:
		return http.StatusNotImplemented
	default:
		return http.StatusInternalServerError
	}
}

// decodeJSON reads a bounded, strict JSON body.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

package handler

import (
	"context"
	"net/http"
	"time"

	"google.golang.org/grpc/connectivity"
)

// healthCheckTimeout bounds the whole aggregate health check.
const healthCheckTimeout = 3 * time.Second

// svcStatus is one dependency's health.
type svcStatus struct {
	Name    string `json:"name"`
	Healthy bool   `json:"healthy"`
	State   string `json:"state"`
	Latency string `json:"latency"`
}

// health reports the reachability of each upstream gRPC service.
//
// It inspects the gRPC connections this process already holds rather than
// fetching hardcoded http://localhost:910x/healthz URLs. Those URLs only
// resolved when every service happened to run on the same host as ui-bff, so the
// dashboard's health panel showed everything down under Kubernetes or Compose —
// and it probed the unauthenticated metrics ports to do it.
func (h *Handler) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), healthCheckTimeout)
	defer cancel()

	checks := []struct {
		name string
		conn grpcConn
	}{
		{"ledger-svc", h.conns.Ledger},
		{"account-svc", h.conns.Account},
		{"fx-svc", h.conns.FX},
		{"transaction-svc", h.conns.Txn},
		{"payout-svc", h.conns.Payout},
	}

	services := make([]svcStatus, 0, len(checks))
	allOK := true

	for _, c := range checks {
		status := checkConn(ctx, c.name, c.conn)
		if !status.Healthy {
			allOK = false
		}
		services = append(services, status)
	}

	code := http.StatusOK
	if !allOK {
		code = http.StatusServiceUnavailable
	}
	writeJSON(w, code, map[string]any{"services": services, "healthy": allOK})
}

// grpcConn is the part of *grpc.ClientConn health checking needs.
type grpcConn interface {
	GetState() connectivity.State
	Connect()
}

func checkConn(ctx context.Context, name string, conn grpcConn) svcStatus {
	if conn == nil {
		return svcStatus{Name: name, Healthy: false, State: "not configured", Latency: "0s"}
	}

	start := time.Now()
	state := conn.GetState()

	// Idle connections are lazy: nudge them and wait briefly so a healthy but
	// unused service is not reported as down.
	if state == connectivity.Idle {
		conn.Connect()
		waitCtx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		for state == connectivity.Idle || state == connectivity.Connecting {
			if waitCtx.Err() != nil {
				break
			}
			time.Sleep(20 * time.Millisecond)
			state = conn.GetState()
		}
	}

	return svcStatus{
		Name:    name,
		Healthy: state == connectivity.Ready,
		State:   state.String(),
		Latency: time.Since(start).Round(time.Millisecond).String(),
	}
}

package handler

import (
	"net/http"
	"time"
)

type svcStatus struct {
	Name    string `json:"name"`
	Healthy bool   `json:"healthy"`
	Latency string `json:"latency"`
}

func (h *Handler) health(w http.ResponseWriter, r *http.Request) {
	client := &http.Client{Timeout: 2 * time.Second}
	services := []svcStatus{
		checkHTTP(client, "ledger-svc",      "http://localhost:9101/healthz"),
		checkHTTP(client, "account-svc",     "http://localhost:9102/healthz"),
		checkHTTP(client, "fx-svc",          "http://localhost:9103/healthz"),
		checkHTTP(client, "transaction-svc", "http://localhost:9104/healthz"),
		checkHTTP(client, "payout-svc",      "http://localhost:9105/healthz"),
		checkHTTP(client, "recon-worker",    "http://localhost:9106/healthz"),
		checkHTTP(client, "consumers",       "http://localhost:9108/healthz"),
	}
	allOK := true
	for _, s := range services {
		if !s.Healthy {
			allOK = false
		}
	}
	code := http.StatusOK
	if !allOK {
		code = http.StatusServiceUnavailable
	}
	writeJSON(w, code, map[string]any{"services": services, "healthy": allOK})
}

func checkHTTP(client *http.Client, name, url string) svcStatus {
	start := time.Now()
	resp, err := client.Get(url)
	lat := time.Since(start).Round(time.Millisecond).String()
	if err != nil || resp.StatusCode != http.StatusOK {
		return svcStatus{Name: name, Healthy: false, Latency: lat}
	}
	return svcStatus{Name: name, Healthy: true, Latency: lat}
}

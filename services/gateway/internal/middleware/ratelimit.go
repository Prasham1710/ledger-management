package middleware

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Rate limiter tuning.
const (
	// minBurst keeps a limiter usable at low request rates. The previous code
	// computed burst as rpm/10 with no floor, so RATE_LIMIT_RPM below 10 yielded a
	// burst of zero and Allow() refused every request — a total lockout from a
	// plausible configuration value.
	minBurst = 1
	// idleEviction is how long an unseen client's limiter is retained.
	idleEviction = 10 * time.Minute
	// cleanupInterval is how often idle limiters are swept.
	cleanupInterval = 5 * time.Minute
	// maxTrackedClients caps the limiter map. Without a bound, a client rotating
	// source addresses grows it indefinitely — the eviction sweep runs every few
	// minutes, far slower than addresses can be cycled.
	maxTrackedClients = 100_000
)

type ipLimiter struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// RateLimiter applies a per-client token bucket.
type RateLimiter struct {
	mu      sync.Mutex
	clients map[string]*ipLimiter
	rps     rate.Limit
	burst   int

	// trustProxyHeaders controls whether X-Forwarded-For is honoured.
	//
	// This must stay false unless the gateway genuinely sits behind a proxy that
	// overwrites the header. When it was trusted unconditionally, any client could
	// send an arbitrary X-Forwarded-For and get a fresh bucket per request, making
	// the limit trivially bypassable — including on /auth/login, so credential
	// brute-forcing was effectively unlimited.
	trustProxyHeaders bool
	// trustedProxies are the CIDRs whose X-Forwarded-For may be believed.
	trustedProxies []*net.IPNet

	stop chan struct{}
}

// RateLimiterConfig configures the limiter.
type RateLimiterConfig struct {
	// RequestsPerMinute is the sustained per-client allowance.
	RequestsPerMinute int
	// Burst is the bucket depth. Zero derives a value from RequestsPerMinute with
	// a floor of minBurst.
	Burst int
	// TrustProxyHeaders enables X-Forwarded-For parsing. Only enable it when a
	// trusted proxy sets that header.
	TrustProxyHeaders bool
	// TrustedProxyCIDRs restricts which peers' X-Forwarded-For is honoured.
	// Ignored unless TrustProxyHeaders is set.
	TrustedProxyCIDRs []string
}

// NewRateLimiter builds a limiter from cfg.
func NewRateLimiter(cfg RateLimiterConfig) *RateLimiter {
	rpm := cfg.RequestsPerMinute
	if rpm <= 0 {
		rpm = 300
	}

	burst := cfg.Burst
	if burst <= 0 {
		burst = rpm / 10
	}
	if burst < minBurst {
		burst = minBurst
	}

	rl := &RateLimiter{
		clients:           make(map[string]*ipLimiter),
		rps:               rate.Limit(float64(rpm) / 60.0),
		burst:             burst,
		trustProxyHeaders: cfg.TrustProxyHeaders,
		stop:              make(chan struct{}),
	}

	if cfg.TrustProxyHeaders {
		for _, cidr := range cfg.TrustedProxyCIDRs {
			if _, network, err := net.ParseCIDR(strings.TrimSpace(cidr)); err == nil {
				rl.trustedProxies = append(rl.trustedProxies, network)
			}
		}
	}

	go rl.cleanup()
	return rl
}

// Close stops the background sweeper.
func (rl *RateLimiter) Close() { close(rl.stop) }

func (rl *RateLimiter) get(key string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	if entry, ok := rl.clients[key]; ok {
		entry.lastSeen = time.Now()
		return entry.limiter
	}

	// Bound the map: when full, evict the least recently seen entry rather than
	// letting address rotation grow it without limit.
	if len(rl.clients) >= maxTrackedClients {
		rl.evictOldestLocked()
	}

	entry := &ipLimiter{limiter: rate.NewLimiter(rl.rps, rl.burst), lastSeen: time.Now()}
	rl.clients[key] = entry
	return entry.limiter
}

func (rl *RateLimiter) evictOldestLocked() {
	var oldestKey string
	var oldestSeen time.Time
	for k, v := range rl.clients {
		if oldestKey == "" || v.lastSeen.Before(oldestSeen) {
			oldestKey, oldestSeen = k, v.lastSeen
		}
	}
	if oldestKey != "" {
		delete(rl.clients, oldestKey)
	}
}

func (rl *RateLimiter) cleanup() {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-rl.stop:
			return
		case <-ticker.C:
			rl.mu.Lock()
			for key, entry := range rl.clients {
				if time.Since(entry.lastSeen) > idleEviction {
					delete(rl.clients, key)
				}
			}
			rl.mu.Unlock()
		}
	}
}

// Middleware enforces the per-client limit.
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !rl.get(rl.clientKey(r)).Allow() {
			w.Header().Set("Retry-After", "60")
			writeJSON(w, http.StatusTooManyRequests, map[string]string{
				"error": "rate limit exceeded",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ClientKey exposes client identification for tests.
func (rl *RateLimiter) ClientKey(r *http.Request) string { return rl.clientKey(r) }

// clientKey identifies the client for rate limiting.
//
// It uses the TCP peer address unless proxy headers are explicitly trusted AND
// the peer is a configured trusted proxy. A client-supplied header must never
// determine its own bucket.
func (rl *RateLimiter) clientKey(r *http.Request) string {
	peerIP := peerAddr(r)

	if !rl.trustProxyHeaders || !rl.isTrustedProxy(peerIP) {
		// Either headers are not trusted at all, or this peer is not authorised
		// to set them.
		return peerIP
	}

	forwarded := r.Header.Get("X-Forwarded-For")
	if forwarded == "" {
		return peerIP
	}
	// Take the right-most entry that is not itself a trusted proxy: entries to
	// its left are client-supplied and therefore forgeable.
	parts := strings.Split(forwarded, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		candidate := strings.TrimSpace(parts[i])
		if candidate == "" {
			continue
		}
		if ip := net.ParseIP(candidate); ip != nil && !rl.isTrustedProxy(candidate) {
			return candidate
		}
	}
	return peerIP
}

func (rl *RateLimiter) isTrustedProxy(addr string) bool {
	ip := net.ParseIP(addr)
	if ip == nil {
		return false
	}
	for _, network := range rl.trustedProxies {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// peerAddr returns the TCP peer IP, which a client cannot forge.
func peerAddr(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// RemoteAddr without a port (possible in tests).
		return r.RemoteAddr
	}
	return host
}

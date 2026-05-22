package proxy

import (
	"net/http"
	"net/http/httputil"
	"net/url"
)

// New returns a reverse proxy that forwards all requests to upstream.
// The Authorization header is stripped so backend services receive only
// internal X-Auth-* headers set by the auth middleware.
func New(upstream string) (http.Handler, error) {
	target, err := url.Parse(upstream)
	if err != nil {
		return nil, err
	}
	rp := httputil.NewSingleHostReverseProxy(target)
	orig := rp.Director
	rp.Director = func(r *http.Request) {
		orig(r)
		r.Header.Del("Authorization") // replaced by X-Auth-* headers
		r.Header.Set("X-Forwarded-Host", r.Host)
		r.Host = target.Host
	}
	return rp, nil
}

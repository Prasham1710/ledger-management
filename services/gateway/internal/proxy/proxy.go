package proxy

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
)

// New returns a reverse proxy that forwards requests to upstream.
//
// The Authorization header is stripped: upstream services authenticate on the
// internal X-Auth-* headers the gateway sets from verified token claims, and
// forwarding the raw bearer token would give every upstream a credential it has
// no reason to hold.
//
// Uses Rewrite rather than the deprecated Director. Besides being the supported
// API since Go 1.20, Rewrite's SetXForwarded replaces any inbound X-Forwarded-*
// headers instead of appending to them, so a client cannot inject a forged
// forwarding chain that an upstream might trust.
func New(upstream string) (http.Handler, error) {
	target, err := url.Parse(upstream)
	if err != nil {
		return nil, fmt.Errorf("proxy: parse upstream URL %q: %w", upstream, err)
	}
	if target.Scheme == "" || target.Host == "" {
		return nil, fmt.Errorf("proxy: upstream URL %q must include a scheme and host", upstream)
	}

	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			// Overwrite X-Forwarded-For/Proto/Host from the real connection.
			pr.SetXForwarded()
			// Replaced by the X-Auth-* headers set in the auth middleware.
			pr.Out.Header.Del("Authorization")
			// Preserve the client-facing host so upstream redirects stay correct.
			pr.Out.Host = target.Host
		},
	}, nil
}

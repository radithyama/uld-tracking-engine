package httpx

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
)

// trustedProxies holds the networks whose X-Real-IP header is believed.
// Empty (the default) means the TCP peer is always the client.
var trustedProxies atomic.Pointer[[]netip.Prefix]

// SetTrustedProxies configures which peers may report the client address in
// X-Real-IP (e.g. the Nginx host, reached through Docker's bridge gateway).
// Entries are CIDRs or bare IPs. Call once at startup.
func SetTrustedProxies(entries []string) error {
	var out []netip.Prefix
	for _, e := range entries {
		p, err := netip.ParsePrefix(e)
		if err != nil {
			a, aerr := netip.ParseAddr(e)
			if aerr != nil {
				return fmt.Errorf("trusted proxy %q: not an IP or CIDR", e)
			}
			p = netip.PrefixFrom(a, a.BitLen())
		}
		out = append(out, p.Masked())
	}
	trustedProxies.Store(&out)
	return nil
}

// ClientIP returns the address of the client that made the request.
//
// X-Real-IP is used only when the TCP peer is a configured trusted proxy.
// Nginx sets it from $remote_addr, overwriting anything the client sent, so
// it can't be spoofed through the proxy. X-Forwarded-For is never used: it is
// client-controlled, and an attacker could use it to evade per-source limits
// and brute-force detection, or to frame another IP in the SIEM.
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	peer = peer.Unmap()
	if ps := trustedProxies.Load(); ps != nil && slices.ContainsFunc(*ps, func(p netip.Prefix) bool { return p.Contains(peer) }) {
		if real, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get("X-Real-IP"))); err == nil {
			return real.Unmap().String()
		}
	}
	return peer.String()
}

// NormalizeOrigins validates an allow-list of browser origins. Wildcards are
// rejected: every allowed origin must be an exact scheme://host[:port].
func NormalizeOrigins(origins []string) ([]string, error) {
	out := make([]string, 0, len(origins))
	for _, o := range origins {
		u, err := url.Parse(o)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" ||
			u.Path != "" || u.RawQuery != "" || u.User != nil || strings.Contains(o, "*") {
			return nil, fmt.Errorf("allowed origin %q must be an exact scheme://host[:port]", o)
		}
		out = append(out, strings.ToLower(u.Scheme+"://"+u.Host))
	}
	return out, nil
}

// OriginAllowed reports whether origin exactly matches an allowed origin.
func OriginAllowed(allowed []string, origin string) bool {
	return origin != "" && slices.Contains(allowed, strings.ToLower(origin))
}

// CORS grants read access to the listed origins only (exact match, no
// wildcard, no credentials). Preflights from any other origin get a 403, and
// simple requests from them get no CORS headers, so the browser withholds the
// response.
func CORS(origins []string, methods ...string) func(http.Handler) http.Handler {
	if len(methods) == 0 {
		methods = []string{http.MethodGet}
	}
	allowMethods := strings.Join(methods, ", ")
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			o := r.Header.Get("Origin")
			w.Header().Add("Vary", "Origin")
			allowed := OriginAllowed(origins, o)
			if allowed {
				w.Header().Set("Access-Control-Allow-Origin", o)
				w.Header().Set("Access-Control-Allow-Methods", allowMethods)
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
				w.Header().Set("Access-Control-Max-Age", "600")
			}
			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				if !allowed {
					WriteError(w, http.StatusForbidden, "origin not allowed", nil)
					return
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// SecurityHeaders sets defensive headers suited to JSON APIs: nothing may be
// framed, sniffed, cached or rendered as a document.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		h.Set("Cross-Origin-Resource-Policy", "same-site")
		next.ServeHTTP(w, r)
	})
}

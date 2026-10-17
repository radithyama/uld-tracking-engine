// Package authz verifies the short-lived role tokens that the Next.js server
// (backend-for-frontend) mints for signed-in users, and enforces roles on the
// realtime service's privileged endpoints.
//
// Tokens are compact JWS (HS256) with a shared secret, a fixed issuer and
// audience, and a short, bounded lifetime. Only HS256 is accepted, so
// "alg": "none" and algorithm confusion attacks are rejected outright.
package authz

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"
)

const (
	RoleGroundHandler   = "ground_handler"
	RoleLoadmaster      = "loadmaster"
	RoleSecurityAuditor = "security_auditor"

	// Issuer is the Next.js backend-for-frontend, the only token minter.
	Issuer = "uldpulse-bff"
	// Audience is for API tokens, sent as "Authorization: Bearer".
	Audience = "uld-realtime"
	// TicketAudience is for WebSocket tickets, sent as ?ticket= because a
	// browser can't set headers on a WebSocket upgrade. A distinct audience
	// means a ticket (which may end up in a proxy access log) can never be
	// used as an API token, and vice versa.
	TicketAudience = "uld-ws"

	skew              = 30 * time.Second
	maxTokenLifetime  = 10 * time.Minute
	maxTicketLifetime = 2 * time.Minute
)

var Roles = []string{RoleGroundHandler, RoleLoadmaster, RoleSecurityAuditor}

type Claims struct {
	Iss  string `json:"iss"`
	Sub  string `json:"sub"`  // stable user id (e-mail or demo id)
	Name string `json:"name"` // display name for audit trails
	Role string `json:"role"`
	Aud  string `json:"aud"`
	Jti  string `json:"jti,omitempty"` // unique id; required on tickets (single use)
	Iat  int64  `json:"iat"`
	Exp  int64  `json:"exp"`
}

var (
	ErrMalformed = errors.New("malformed token")
	ErrSignature = errors.New("bad signature")
	ErrExpired   = errors.New("token expired")
	ErrClaims    = errors.New("invalid claims")
	ErrReplayed  = errors.New("ticket already used")
)

var b64 = base64.RawURLEncoding

// Sign creates a token (used by tests and tooling; production tokens are
// minted by the Next.js server with the same format).
func Sign(c Claims, secret []byte) string {
	h := b64.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	body, _ := json.Marshal(c)
	signing := h + "." + b64.EncodeToString(body)
	return signing + "." + b64.EncodeToString(mac(signing, secret))
}

func mac(s string, secret []byte) []byte {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(s))
	return m.Sum(nil)
}

// Verify checks an API token: signature, algorithm, issuer, audience,
// lifetime and role.
func Verify(token string, secret []byte, now time.Time) (Claims, error) {
	return verify(token, secret, Audience, maxTokenLifetime, now)
}

// VerifyTicket checks a WebSocket ticket. Single use is enforced separately
// (see RequireTicket), since it needs shared state.
func VerifyTicket(token string, secret []byte, now time.Time) (Claims, error) {
	c, err := verify(token, secret, TicketAudience, maxTicketLifetime, now)
	if err == nil && (len(c.Jti) < 16 || len(c.Jti) > 64) {
		return c, ErrClaims
	}
	return c, err
}

func verify(token string, secret []byte, aud string, maxLifetime time.Duration, now time.Time) (Claims, error) {
	var c Claims
	parts := strings.Split(token, ".")
	if len(parts) != 3 || len(token) > 4096 {
		return c, ErrMalformed
	}
	hdrJSON, err := b64.DecodeString(parts[0])
	if err != nil {
		return c, ErrMalformed
	}
	var hdr struct {
		Alg string `json:"alg"`
	}
	if json.Unmarshal(hdrJSON, &hdr) != nil || hdr.Alg != "HS256" {
		return c, ErrMalformed
	}
	sig, err := b64.DecodeString(parts[2])
	if err != nil || !hmac.Equal(sig, mac(parts[0]+"."+parts[1], secret)) {
		return c, ErrSignature
	}
	body, err := b64.DecodeString(parts[1])
	if err != nil || json.Unmarshal(body, &c) != nil {
		return c, ErrMalformed
	}
	iat, exp := time.Unix(c.Iat, 0), time.Unix(c.Exp, 0)
	switch {
	case c.Exp == 0 || now.After(exp.Add(skew)):
		return c, ErrExpired
	case c.Iat == 0 || iat.After(now.Add(skew)) || exp.Sub(iat) > maxLifetime:
		// A long-lived token would turn a single leak into lasting access.
		return c, ErrClaims
	case c.Iss != Issuer, c.Aud != aud, c.Sub == "", !slices.Contains(Roles, c.Role):
		return c, ErrClaims
	}
	return c, nil
}

type ctxKey struct{}

// FromContext returns the verified claims set by Require or RequireTicket.
func FromContext(ctx context.Context) (Claims, bool) {
	c, ok := ctx.Value(ctxKey{}).(Claims)
	return c, ok
}

// Require authenticates an API request ("Authorization: Bearer" only; tokens
// in URLs leak into logs) and allows it only for the listed roles. An empty
// secret disables the endpoints entirely.
func Require(secret []byte, roles ...string) func(http.Handler) http.Handler {
	return guard(secret, roles, func(r *http.Request) (Claims, error) {
		scheme, tok, ok := strings.Cut(r.Header.Get("Authorization"), " ")
		if !ok || !strings.EqualFold(scheme, "Bearer") {
			return Claims{}, ErrMalformed
		}
		return Verify(strings.TrimSpace(tok), secret, time.Now())
	})
}

// TicketStore records used ticket ids so each ticket opens one connection.
type TicketStore interface {
	// Claim records jti until ttl elapses and reports whether it was unused.
	Claim(ctx context.Context, jti string, ttl time.Duration) (bool, error)
}

// RequireTicket authenticates a WebSocket upgrade with a single-use ticket
// in ?ticket= and allows it only for the listed roles. If the store is
// unavailable the upgrade is refused (fail closed).
func RequireTicket(secret []byte, store TicketStore, roles ...string) func(http.Handler) http.Handler {
	return guard(secret, roles, func(r *http.Request) (Claims, error) {
		c, err := VerifyTicket(r.URL.Query().Get("ticket"), secret, time.Now())
		if err != nil {
			return c, err
		}
		// Remember the id past the ticket's own expiry (plus clock skew).
		ttl := time.Until(time.Unix(c.Exp, 0)) + 2*skew
		fresh, err := store.Claim(r.Context(), c.Jti, ttl)
		if err != nil || !fresh {
			return c, ErrReplayed
		}
		return c, nil
	})
}

func guard(secret []byte, roles []string, authenticate func(*http.Request) (Claims, error)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if len(secret) == 0 {
				writeErr(w, http.StatusServiceUnavailable, "internal API disabled: INTERNAL_API_SECRET not set")
				return
			}
			c, err := authenticate(r)
			if err != nil {
				writeErr(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			if !slices.Contains(roles, c.Role) {
				writeErr(w, http.StatusForbidden, "forbidden")
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, c)))
		})
	}
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":"` + msg + `"}`))
}

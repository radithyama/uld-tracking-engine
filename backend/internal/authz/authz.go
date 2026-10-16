// Package authz verifies the short-lived role tokens that the Next.js server
// (backend-for-frontend) mints for signed-in users, and enforces roles on the
// realtime service's privileged endpoints.
//
// Tokens are compact JWS (HS256) with a shared secret, a fixed audience and a
// short expiry. Only HS256 is accepted, so "alg": "none" and algorithm
// confusion attacks are rejected outright.
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

	Audience = "uld-realtime"
	skew     = 30 * time.Second
)

var Roles = []string{RoleGroundHandler, RoleLoadmaster, RoleSecurityAuditor}

type Claims struct {
	Sub  string `json:"sub"`  // stable user id (e-mail or demo id)
	Name string `json:"name"` // display name for audit trails
	Role string `json:"role"`
	Aud  string `json:"aud"`
	Iat  int64  `json:"iat"`
	Exp  int64  `json:"exp"`
}

var (
	ErrMalformed = errors.New("malformed token")
	ErrSignature = errors.New("bad signature")
	ErrExpired   = errors.New("token expired")
	ErrClaims    = errors.New("invalid claims")
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

// Verify checks signature, algorithm, audience, expiry and role.
func Verify(token string, secret []byte, now time.Time) (Claims, error) {
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
	switch {
	case c.Exp == 0 || now.After(time.Unix(c.Exp, 0).Add(skew)):
		return c, ErrExpired
	case c.Aud != Audience, c.Sub == "", !slices.Contains(Roles, c.Role):
		return c, ErrClaims
	}
	return c, nil
}

type ctxKey struct{}

// FromContext returns the verified claims set by Require.
func FromContext(ctx context.Context) (Claims, bool) {
	c, ok := ctx.Value(ctxKey{}).(Claims)
	return c, ok
}

// Require authenticates the request (Authorization: Bearer, or ?token= for
// WebSocket upgrades, which can't set headers from a browser) and allows it
// only for the listed roles. An empty secret disables the endpoints entirely.
func Require(secret []byte, roles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if len(secret) == 0 {
				http.Error(w, `{"error":"internal API disabled: INTERNAL_API_SECRET not set"}`, http.StatusServiceUnavailable)
				return
			}
			tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if tok == "" || tok == r.Header.Get("Authorization") {
				tok = r.URL.Query().Get("token")
			}
			c, err := Verify(tok, secret, time.Now())
			if err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
				return
			}
			if !slices.Contains(roles, c.Role) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":"forbidden for role ` + c.Role + `"}`))
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, c)))
		})
	}
}

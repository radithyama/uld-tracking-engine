package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/radithya/uld-engine/internal/httpx"
	"github.com/radithya/uld-engine/internal/secevent"
)

// apiKey is one entry of the keys file. Only the SHA-256 of the key is
// stored, so the file (and a leaked copy of it) never contains secrets.
type apiKey struct {
	ID        string   `json:"id"`
	Principal string   `json:"principal"`
	Scopes    []string `json:"scopes"`
	SHA256    string   `json:"sha256"`
	Revoked   bool     `json:"revoked"`
}

type principal struct {
	KeyID string
	Name  string
}

type ctxKey struct{}

// Bearer tokens: 20–256 chars of RFC 6750 b64token alphabet.
var tokenPattern = regexp.MustCompile(`^[A-Za-z0-9\-._~+/]+=*$`)

const (
	minTokenLen = 20
	maxTokenLen = 256
)

type Authenticator struct {
	log        *slog.Logger
	byHash     map[string]apiKey
	brute      *bruteForceTracker
	logSuccess bool
}

func loadKeys(path string) (map[string]apiKey, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var file struct {
		Keys []apiKey `json:"keys"`
	}
	if err := json.Unmarshal(b, &file); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	m := make(map[string]apiKey, len(file.Keys))
	for _, k := range file.Keys {
		h := strings.ToLower(k.SHA256)
		if len(h) != 64 || k.ID == "" || k.Principal == "" {
			return nil, fmt.Errorf("key %q: id, principal and 64-hex sha256 are required", k.ID)
		}
		m[h] = k
	}
	return m, nil
}

// fingerprint lets analysts correlate the same bad key across requests and
// sources without the key itself ever reaching the logs.
func fingerprint(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])[:16]
}

// Require authenticates the request and checks that the key holds scope.
func (a *Authenticator) Require(scope string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, reason, ok := extractToken(r)
			if !ok {
				rule := secevent.AuthMalformedHeader
				if reason == "missing_credentials" {
					rule = secevent.AuthMissingCredentials
				}
				a.fail(w, r, rule, reason, http.StatusUnauthorized, "", nil)
				return
			}

			sum := sha256.Sum256([]byte(token))
			key, found := a.byHash[hex.EncodeToString(sum[:])]
			switch {
			case !found:
				a.fail(w, r, secevent.AuthInvalidKey, "unknown_api_key", http.StatusUnauthorized, token, nil)
				return
			case key.Revoked:
				a.fail(w, r, secevent.AuthRevokedKey, "revoked_api_key", http.StatusUnauthorized, token, &key)
				return
			case !slices.Contains(key.Scopes, scope):
				secevent.Emit(r.Context(), a.log, secevent.AuthzInsufficientScope, "failure", "missing_scope",
					append(requestAttrs(r, nil),
						slog.Group("user", slog.String("name", key.Principal)),
						slog.Group("api_key", slog.String("id", key.ID), slog.String("fingerprint", fingerprint(token)),
							slog.Any("scopes", key.Scopes), slog.String("required_scope", scope)))...)
				httpx.WriteError(w, http.StatusForbidden, "forbidden", nil)
				return
			}

			if a.logSuccess {
				secevent.Emit(r.Context(), a.log, secevent.AuthSuccess, "success", "valid_api_key",
					append(requestAttrs(r, nil),
						slog.Group("user", slog.String("name", key.Principal)),
						slog.Group("api_key", slog.String("id", key.ID)))...)
			}
			ctx := context.WithValue(r.Context(), ctxKey{}, principal{KeyID: key.ID, Name: key.Principal})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func principalFrom(ctx context.Context) principal {
	p, _ := ctx.Value(ctxKey{}).(principal)
	return p
}

// extractToken accepts exactly one of "Authorization: Bearer <token>" or
// "X-API-Key: <token>" and classifies everything else.
func extractToken(r *http.Request) (token, reason string, ok bool) {
	authz := r.Header.Values("Authorization")
	xkey := r.Header.Values("X-Api-Key")
	switch {
	case len(authz) == 0 && len(xkey) == 0:
		return "", "missing_credentials", false
	case len(authz)+len(xkey) > 1:
		// Several credentials is a classic header-smuggling / confusion probe.
		return "", "multiple_credentials", false
	case len(xkey) == 1:
		token = strings.TrimSpace(xkey[0])
	default:
		scheme, rest, found := strings.Cut(strings.TrimSpace(authz[0]), " ")
		if !found || !strings.EqualFold(scheme, "Bearer") {
			return "", "unsupported_auth_scheme", false
		}
		token = strings.TrimSpace(rest)
	}
	switch {
	case token == "":
		return "", "empty_token", false
	case len(token) < minTokenLen || len(token) > maxTokenLen:
		return "", "invalid_token_length", false
	case !tokenPattern.MatchString(token):
		return "", "invalid_token_charset", false
	}
	return token, "", true
}

// requestAttrs returns the ECS source/http/url/user_agent groups. extra is
// appended inside http.request so each top-level object appears exactly once
// per line (duplicate JSON keys are dropped or rejected by strict parsers).
func requestAttrs(r *http.Request, extra []any) []slog.Attr {
	req := append([]any{
		slog.String("method", r.Method),
		slog.String("id", middleware.GetReqID(r.Context())),
	}, extra...)
	return []slog.Attr{
		slog.Group("source", slog.String("ip", httpx.ClientIP(r))),
		slog.Group("http", slog.Group("request", req...)),
		slog.Group("url", slog.String("path", secevent.Clean(r.URL.Path, 256))),
		slog.Group("user_agent", slog.String("original", secevent.Clean(r.UserAgent(), 256))),
	}
}

// fail emits the auth-failure event (and, past the threshold, a brute-force
// alert) and writes the 401. token may be empty; key is set when a known
// key was presented (e.g. revoked).
func (a *Authenticator) fail(w http.ResponseWriter, r *http.Request, rule secevent.Rule, reason string, status int, token string, key *apiKey) {
	// Record suspicious header shapes without echoing secrets: the scheme
	// only, never the credential.
	var reqExtra []any
	if v := r.Header.Get("Authorization"); v != "" {
		scheme, _, _ := strings.Cut(strings.TrimSpace(v), " ")
		reqExtra = append(reqExtra, slog.String("auth_scheme", secevent.Clean(scheme, 32)))
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		reqExtra = append(reqExtra, slog.String("x_forwarded_for", secevent.Clean(xff, 128)))
	}
	attrs := requestAttrs(r, reqExtra)

	var keyAttrs []any
	if key != nil {
		keyAttrs = append(keyAttrs, slog.String("id", key.ID))
		attrs = append(attrs, slog.Group("user", slog.String("name", key.Principal)))
	}
	if token != "" {
		keyAttrs = append(keyAttrs, slog.String("fingerprint", fingerprint(token)))
	}
	if len(keyAttrs) > 0 {
		attrs = append(attrs, slog.Group("api_key", keyAttrs...))
	}
	secevent.Emit(r.Context(), a.log, rule, "failure", reason, attrs...)

	ip := httpx.ClientIP(r)
	if n, trip := a.brute.fail(ip); trip {
		secevent.Emit(r.Context(), a.log, secevent.AuthBruteForce, "failure", "failure_threshold_exceeded",
			slog.Group("source", slog.String("ip", ip)),
			slog.Int("auth_failures", n),
			slog.Float64("window_seconds", a.brute.window.Seconds()))
	}

	w.Header().Set("WWW-Authenticate", `Bearer realm="uld-ingest"`)
	httpx.WriteError(w, status, "unauthorized", nil)
}

// bruteForceTracker counts auth failures per source IP in a fixed window and
// trips once per window when the threshold is crossed. It is a first-line
// local signal; cross-host correlation is the SIEM's job.
type bruteForceTracker struct {
	mu        sync.Mutex
	window    time.Duration
	threshold int
	m         map[string]*bfEntry
}

type bfEntry struct {
	start   time.Time
	count   int
	alerted bool
}

func newBruteForceTracker(window time.Duration, threshold int) *bruteForceTracker {
	return &bruteForceTracker{window: window, threshold: threshold, m: map[string]*bfEntry{}}
}

func (b *bruteForceTracker) fail(ip string) (count int, trip bool) {
	now := time.Now()
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.m) > 10000 { // bound memory under a distributed attack
		for k, e := range b.m {
			if now.Sub(e.start) > b.window {
				delete(b.m, k)
			}
		}
	}
	e := b.m[ip]
	if e == nil || now.Sub(e.start) > b.window {
		e = &bfEntry{start: now}
		b.m[ip] = e
	}
	e.count++
	if e.count >= b.threshold && !e.alerted {
		e.alerted = true
		return e.count, true
	}
	return e.count, false
}

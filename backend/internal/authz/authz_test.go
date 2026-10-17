package authz

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

var secret = []byte("test-secret-test-secret-test-secret")

func valid(role string) Claims {
	now := time.Now()
	return Claims{Iss: Issuer, Sub: "demo-lm", Name: "Demo", Role: role, Aud: Audience, Iat: now.Unix(), Exp: now.Add(time.Minute).Unix()}
}

func ticket(role, jti string) Claims {
	c := valid(role)
	c.Aud, c.Jti = TicketAudience, jti
	return c
}

func TestVerify(t *testing.T) {
	now := time.Now()
	good := Sign(valid(RoleLoadmaster), secret)
	if c, err := Verify(good, secret, now); err != nil || c.Role != RoleLoadmaster {
		t.Fatalf("valid token rejected: %v", err)
	}

	expired := valid(RoleLoadmaster)
	expired.Exp = now.Add(-time.Hour).Unix()
	wrongAud := valid(RoleLoadmaster)
	wrongAud.Aud = "other"
	badRole := valid("admin")
	wrongIss := valid(RoleLoadmaster)
	wrongIss.Iss = "attacker"
	longLived := valid(RoleLoadmaster)
	longLived.Exp = now.Add(24 * time.Hour).Unix()
	noIat := valid(RoleLoadmaster)
	noIat.Iat = 0

	// alg=none with the original payload must never verify.
	parts := strings.Split(good, ".")
	none := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`)) + "." + parts[1] + "."

	for name, tc := range map[string]struct {
		tok  string
		want error
	}{
		"wrong secret":        {Sign(valid(RoleLoadmaster), []byte("other")), ErrSignature},
		"tampered":            {parts[0] + "." + base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"x","role":"security_auditor","aud":"uld-realtime","exp":9999999999}`)) + "." + parts[2], ErrSignature},
		"alg none":            {none, ErrMalformed},
		"expired":             {Sign(expired, secret), ErrExpired},
		"audience":            {Sign(wrongAud, secret), ErrClaims},
		"unknown role":        {Sign(badRole, secret), ErrClaims},
		"issuer":              {Sign(wrongIss, secret), ErrClaims},
		"long-lived":          {Sign(longLived, secret), ErrClaims},
		"no iat":              {Sign(noIat, secret), ErrClaims},
		"ticket as API token": {Sign(ticket(RoleLoadmaster, "0123456789abcdef0123"), secret), ErrClaims},
		"garbage":             {"a.b", ErrMalformed},
	} {
		if _, err := Verify(tc.tok, secret, now); err != tc.want {
			t.Errorf("%s: got %v, want %v", name, err, tc.want)
		}
	}
}

func TestRequire(t *testing.T) {
	h := Require(secret, RoleSecurityAuditor)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, _ := FromContext(r.Context())
		_, _ = w.Write([]byte(c.Sub))
	}))
	do := func(tok string, query bool) int {
		req := httptest.NewRequest("GET", "/x", nil)
		if query {
			req = httptest.NewRequest("GET", "/x?token="+tok, nil)
		} else if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if c := do("", false); c != 401 {
		t.Errorf("no token: %d", c)
	}
	if c := do(Sign(valid(RoleLoadmaster), secret), false); c != 403 {
		t.Errorf("wrong role: %d", c)
	}
	if c := do(Sign(valid(RoleSecurityAuditor), secret), false); c != 200 {
		t.Errorf("auditor header: %d", c)
	}
	// Tokens in URLs leak into proxy logs, so API tokens are header-only.
	if c := do(Sign(valid(RoleSecurityAuditor), secret), true); c != 401 {
		t.Errorf("auditor token in query: %d, want 401", c)
	}
	disabled := Require(nil, RoleLoadmaster)(http.NotFoundHandler())
	rec := httptest.NewRecorder()
	disabled.ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))
	if rec.Code != 503 {
		t.Errorf("empty secret should disable endpoint, got %d", rec.Code)
	}
}

type memStore struct {
	mu   sync.Mutex
	seen map[string]bool
	err  error
}

func (m *memStore) Claim(_ context.Context, jti string, _ time.Duration) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return false, m.err
	}
	if m.seen[jti] {
		return false, nil
	}
	m.seen[jti] = true
	return true, nil
}

func TestRequireTicket(t *testing.T) {
	store := &memStore{seen: map[string]bool{}}
	h := RequireTicket(secret, store, RoleSecurityAuditor)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	do := func(q string) int {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/ws?"+q, nil))
		return rec.Code
	}
	good := Sign(ticket(RoleSecurityAuditor, "aaaaaaaaaaaaaaaaaaaa"), secret)
	if c := do("ticket=" + good); c != 200 {
		t.Fatalf("fresh ticket: %d", c)
	}
	if c := do("ticket=" + good); c != 401 {
		t.Errorf("replayed ticket: %d, want 401", c)
	}
	if c := do("ticket=" + Sign(ticket(RoleLoadmaster, "bbbbbbbbbbbbbbbbbbbb"), secret)); c != 403 {
		t.Errorf("wrong role: %d, want 403", c)
	}
	if c := do("ticket=" + Sign(valid(RoleSecurityAuditor), secret)); c != 401 {
		t.Errorf("API token as ticket: %d, want 401", c)
	}
	if c := do("ticket=" + Sign(ticket(RoleSecurityAuditor, "short"), secret)); c != 401 {
		t.Errorf("ticket without proper jti: %d, want 401", c)
	}
	long := ticket(RoleSecurityAuditor, "cccccccccccccccccccc")
	long.Exp = time.Now().Add(10 * time.Minute).Unix()
	if c := do("ticket=" + Sign(long, secret)); c != 401 {
		t.Errorf("long-lived ticket: %d, want 401", c)
	}
	store.err = errors.New("redis down")
	if c := do("ticket=" + Sign(ticket(RoleSecurityAuditor, "dddddddddddddddddddd"), secret)); c != 401 {
		t.Errorf("store unavailable must fail closed: %d", c)
	}
}

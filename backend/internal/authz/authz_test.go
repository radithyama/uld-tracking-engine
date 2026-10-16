package authz

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var secret = []byte("test-secret-test-secret-test-secret")

func valid(role string) Claims {
	now := time.Now()
	return Claims{Sub: "demo-lm", Name: "Demo", Role: role, Aud: Audience, Iat: now.Unix(), Exp: now.Add(time.Minute).Unix()}
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

	// alg=none with the original payload must never verify.
	parts := strings.Split(good, ".")
	none := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`)) + "." + parts[1] + "."

	for name, tc := range map[string]struct {
		tok  string
		want error
	}{
		"wrong secret": {Sign(valid(RoleLoadmaster), []byte("other")), ErrSignature},
		"tampered":     {parts[0] + "." + base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"x","role":"security_auditor","aud":"uld-realtime","exp":9999999999}`)) + "." + parts[2], ErrSignature},
		"alg none":     {none, ErrMalformed},
		"expired":      {Sign(expired, secret), ErrExpired},
		"audience":     {Sign(wrongAud, secret), ErrClaims},
		"unknown role": {Sign(badRole, secret), ErrClaims},
		"garbage":      {"a.b", ErrMalformed},
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
	if c := do(Sign(valid(RoleSecurityAuditor), secret), true); c != 200 {
		t.Errorf("auditor query: %d", c)
	}
	disabled := Require(nil, RoleLoadmaster)(http.NotFoundHandler())
	rec := httptest.NewRecorder()
	disabled.ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))
	if rec.Code != 503 {
		t.Errorf("empty secret should disable endpoint, got %d", rec.Code)
	}
}

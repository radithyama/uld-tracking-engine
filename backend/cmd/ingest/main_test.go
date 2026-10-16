package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/segmentio/kafka-go"

	"github.com/radithya/uld-engine/internal/logx"
	"github.com/radithya/uld-engine/internal/spool"
)

type fakeProducer struct {
	mu   sync.Mutex
	msgs []kafka.Message
	err  error
}

func (f *fakeProducer) WriteMessages(_ context.Context, msgs ...kafka.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.msgs = append(f.msgs, msgs...)
	return nil
}

func (f *fakeProducer) set(err error) { f.mu.Lock(); f.err = err; f.mu.Unlock() }
func (f *fakeProducer) count() int    { f.mu.Lock(); defer f.mu.Unlock(); return len(f.msgs) }

const (
	writeKey   = "dev-cgk-scanner-fleet-key-7f3a9c2e"
	readKey    = "dev-ops-dashboard-readonly-key-9e21"
	revokedKey = "dev-retired-handheld-key-0c55aa31"
	okScan     = `{"uld_id":"AKE10001GA","scanner_id":"SCN-1","scan_type":"RFID","weight_kg":500,"location_id":"RCV-01"}`
)

// lockedBuffer is a goroutine-safe log sink (the replayer logs concurrently).
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}
func (b *lockedBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return bytes.Clone(b.buf.Bytes())
}
func (b *lockedBuffer) String() string { return string(b.Bytes()) }
func (b *lockedBuffer) Reset()         { b.mu.Lock(); b.buf.Reset(); b.mu.Unlock() }

type harness struct {
	h    http.Handler
	prod *fakeProducer
	pub  *Publisher
	logs *lockedBuffer
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	logs := &lockedBuffer{}
	log := logx.NewWithWriter(logs, "ingest-test", "debug")
	keys, err := loadKeys("../../config/api-keys.dev.json")
	if err != nil {
		t.Fatal(err)
	}
	sp, err := spool.Open(t.TempDir(), 1<<20, 100)
	if err != nil {
		t.Fatal(err)
	}
	prod := &fakeProducer{}
	pub := NewPublisher(log, prod, sp, 100*time.Millisecond)
	s := &server{log: log, pub: pub}
	auth := &Authenticator{log: log, byHash: keys, brute: newBruteForceTracker(time.Minute, 5)}
	r := chi.NewRouter()
	r.With(auth.Require("scans:write")).Post("/v1/scans", s.handleScans)
	return &harness{h: r, prod: prod, pub: pub, logs: logs}
}

func (h *harness) post(body string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/scans", strings.NewReader(body))
	req.RemoteAddr = "203.0.113.9:4711"
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.h.ServeHTTP(rec, req)
	return rec
}

func bearer(k string) map[string]string { return map[string]string{"Authorization": "Bearer " + k} }

// securityEvents parses the JSON log stream and returns rule IDs of
// uld.security events, asserting every line is valid JSON.
func securityEvents(t *testing.T, logs *lockedBuffer) []string {
	t.Helper()
	var ids []string
	for _, line := range bytes.Split(bytes.TrimSpace(logs.Bytes()), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			t.Fatalf("log line is not strict JSON: %s", line)
		}
		if dup := duplicateKey(line); dup != "" {
			t.Fatalf("duplicate JSON key %q in log line: %s", dup, line)
		}
		ev, _ := m["event"].(map[string]any)
		if ev != nil && ev["dataset"] == "uld.security" {
			ids = append(ids, m["rule"].(map[string]any)["id"].(string))
		}
	}
	return ids
}

func TestAcceptedDirectToKafka(t *testing.T) {
	h := newHarness(t)
	rec := h.post(okScan, bearer(writeKey))
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), `"durability":"kafka"`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if h.prod.count() != 1 || string(h.prod.msgs[0].Key) != "AKE10001GA" {
		t.Fatalf("unexpected messages: %+v", h.prod.msgs)
	}
}

func TestAuthFailuresEmitSecurityEvents(t *testing.T) {
	h := newHarness(t)
	cases := []struct {
		name   string
		hdr    map[string]string
		status int
		rule   string
	}{
		{"missing", nil, 401, "ULD-AUTH-001"},
		{"basic scheme", map[string]string{"Authorization": "Basic dXNlcjpwYXNz"}, 401, "ULD-AUTH-002"},
		{"empty bearer", map[string]string{"Authorization": "Bearer "}, 401, "ULD-AUTH-002"},
		{"bad charset", bearer("<script>alert(1)</script>-padpadpad"), 401, "ULD-AUTH-002"},
		{"unknown key", bearer("this-key-does-not-exist-123456"), 401, "ULD-AUTH-003"},
		{"revoked key", bearer(revokedKey), 401, "ULD-AUTH-005"},
		{"read-only key", bearer(readKey), 403, "ULD-AUTHZ-001"},
	}
	for _, c := range cases {
		h.logs.Reset()
		rec := h.post(okScan, c.hdr)
		if rec.Code != c.status {
			t.Errorf("%s: status %d, want %d", c.name, rec.Code, c.status)
		}
		if ids := securityEvents(t, h.logs); len(ids) == 0 || ids[0] != c.rule {
			t.Errorf("%s: events %v, want %s", c.name, ids, c.rule)
		}
		if strings.Contains(h.logs.String(), "this-key-does-not-exist") || strings.Contains(h.logs.String(), revokedKey) {
			t.Errorf("%s: raw credential leaked into logs", c.name)
		}
	}
	if h.prod.count() != 0 {
		t.Fatal("unauthenticated requests must not publish")
	}
}

func TestBruteForceAlertFiresOncePerWindow(t *testing.T) {
	h := newHarness(t)
	for range 12 {
		h.post(okScan, bearer("wrong-key-wrong-key-wrong-key"))
	}
	n := 0
	for _, id := range securityEvents(t, h.logs) {
		if id == "ULD-AUTH-004" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("brute-force alerts = %d, want 1", n)
	}
}

func TestBatchIsAllOrNothing(t *testing.T) {
	h := newHarness(t)
	rec := h.post(`[`+okScan+`, {"uld_id":"bad"}]`, bearer(writeKey))
	if rec.Code != http.StatusUnprocessableEntity || h.prod.count() != 0 {
		t.Fatalf("status %d, published %d", rec.Code, h.prod.count())
	}
	if ids := securityEvents(t, h.logs); len(ids) != 1 || ids[0] != "ULD-INPUT-001" {
		t.Fatalf("events %v", ids)
	}
}

// Kafka down: requests are still accepted (spooled, durable); when Kafka
// returns, the replayer drains everything in order and direct mode resumes.
func TestDegradesToSpoolAndRecovers(t *testing.T) {
	h := newHarness(t)
	h.prod.set(errors.New("broker unreachable"))

	for i := range 5 {
		rec := h.post(okScan, bearer(writeKey))
		if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), `"durability":"spool"`) {
			t.Fatalf("req %d: %d %s", i, rec.Code, rec.Body)
		}
	}
	if h.pub.spool.Pending() != 5 || !h.pub.degraded.Load() {
		t.Fatalf("pending=%d degraded=%v", h.pub.spool.Pending(), h.pub.degraded.Load())
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go h.pub.RunReplayer(ctx)
	h.prod.set(nil)

	deadline := time.Now().Add(5 * time.Second)
	for h.pub.degraded.Load() {
		if time.Now().After(deadline) {
			t.Fatalf("did not recover; pending=%d", h.pub.spool.Pending())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if h.prod.count() != 5 {
		t.Fatalf("replayed %d, want 5", h.prod.count())
	}
	if rec := h.post(okScan, bearer(writeKey)); !strings.Contains(rec.Body.String(), `"durability":"kafka"`) {
		t.Fatalf("direct mode not restored: %s", rec.Body)
	}
	ids := strings.Join(securityEvents(t, h.logs), ",")
	if !strings.Contains(ids, "ULD-AVAIL-001") || !strings.Contains(ids, "ULD-AVAIL-002") {
		t.Fatalf("missing availability events: %s", ids)
	}
}

func TestUnknownFieldsRejected(t *testing.T) {
	h := newHarness(t)
	if rec := h.post(`{"uld_id":"AKE10001GA","bogus":1}`, bearer(writeKey)); rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d", rec.Code)
	}
}

// duplicateKey walks a JSON document and reports the first object key that
// appears twice in the same object ("" if none).
func duplicateKey(doc []byte) string {
	dec := json.NewDecoder(bytes.NewReader(doc))
	var walk func() string
	walk = func() string {
		tok, err := dec.Token()
		if err != nil {
			return ""
		}
		switch tok {
		case json.Delim('{'):
			seen := map[string]bool{}
			for dec.More() {
				k, _ := dec.Token()
				key := k.(string)
				if seen[key] {
					return key
				}
				seen[key] = true
				if d := walk(); d != "" {
					return d
				}
			}
			_, _ = dec.Token()
		case json.Delim('['):
			for dec.More() {
				if d := walk(); d != "" {
					return d
				}
			}
			_, _ = dec.Token()
		}
		return ""
	}
	return walk()
}

package realtime

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/radithya/uld-engine/internal/ratelimit"
	"github.com/radithya/uld-engine/internal/secevent"
)

func testAdmission(perIP, total int) *Admission {
	return &Admission{
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Origins: []string{"https://uldpulse.example"},
		Upgrade: ratelimit.NewKeyed(1000, 1000, 100),
		Conns:   ratelimit.NewConns(perIP, total),
		Report:  secevent.NewThrottle(time.Minute),
	}
}

func TestPrecheckOrigin(t *testing.T) {
	a := testAdmission(5, 5)
	h := a.Precheck(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	for origin, want := range map[string]int{
		"https://uldpulse.example": 200,
		"https://evil.example":     403, // cross-site WebSocket hijacking attempt
		"null":                     403, // sandboxed iframe / file:// page
		"":                         200, // non-browser client (still needs a ticket)
	} {
		req := httptest.NewRequest("GET", "/ws", nil)
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Errorf("origin %q: got %d, want %d", origin, rec.Code, want)
		}
	}
}

func TestPrecheckUpgradeRate(t *testing.T) {
	a := testAdmission(5, 5)
	a.Upgrade = ratelimit.NewKeyed(0.001, 2, 100)
	h := a.Precheck(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	codes := []int{}
	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/ws", nil))
		codes = append(codes, rec.Code)
	}
	if codes[0] != 200 || codes[1] != 200 || codes[2] != 429 {
		t.Fatalf("codes = %v, want [200 200 429]", codes)
	}
}

func TestAcquirePerIP(t *testing.T) {
	a := testAdmission(1, 10)
	rel, ok := a.Acquire(httptest.NewRecorder(), httptest.NewRequest("GET", "/ws", nil))
	if !ok {
		t.Fatal("first connection refused")
	}
	rec := httptest.NewRecorder()
	if _, ok := a.Acquire(rec, httptest.NewRequest("GET", "/ws", nil)); ok || rec.Code != 503 {
		t.Fatalf("second connection from same IP: ok=%v code=%d", ok, rec.Code)
	}
	rel()
	if _, ok := a.Acquire(httptest.NewRecorder(), httptest.NewRequest("GET", "/ws", nil)); !ok {
		t.Fatal("slot not released")
	}
}

// Many simultaneous callers share a handful of fetches, and no caller gets a
// result from a fetch that started before it called.
func TestCoalesceSnapshots(t *testing.T) {
	var fetches atomic.Int64
	var mu sync.Mutex
	var starts []time.Time
	snap := CoalesceSnapshots(func(context.Context) ([]byte, error) {
		fetches.Add(1)
		start := time.Now()
		mu.Lock()
		starts = append(starts, start)
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		return []byte(start.Format(time.RFC3339Nano)), nil
	})

	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			called := time.Now()
			b, err := snap(context.Background())
			if err != nil {
				t.Error(err)
				return
			}
			got, _ := time.Parse(time.RFC3339Nano, string(b))
			if got.Before(called.Add(-time.Millisecond)) {
				t.Errorf("caller at %v got snapshot started at %v", called, got)
			}
		}()
	}
	wg.Wait()
	if n := fetches.Load(); n > 10 {
		t.Fatalf("%d fetches for 200 concurrent callers; expected coalescing", n)
	}
}

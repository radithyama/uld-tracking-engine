// Command simulator emulates the warehouse scanners at every station in the
// network. It walks the seeded ULD fleet through receiving, build-up,
// staging and ramp, follows ULDs onto their flights (flown by the processor's
// network clock) and posts scan batches to the ingest API as an
// at-least-once client. Occasionally it over-packs a ULD to exercise weight
// alerts.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/radithya/uld-engine/internal/config"
	"github.com/radithya/uld-engine/internal/httpx"
	"github.com/radithya/uld-engine/internal/logx"
	"github.com/radithya/uld-engine/internal/model"
)

// stats are read by the control API while the generator/sender goroutines
// update them.
type stats struct {
	generated atomic.Int64
	accepted  atomic.Int64
	rejected  atomic.Int64
	retries   atomic.Int64
	spooled   atomic.Int64 // accepted while ingest was in degraded (spool) mode
	inFlight  atomic.Int64
	paused    atomic.Bool
	rate      atomic.Int64 // scans per second, adjustable at runtime

	mu   sync.Mutex
	sent []string // event ids acknowledged with 202, in order (the ledger)
}

func main() {
	httpx.HandleHealthcheckCommand()

	log := logx.New("simulator", config.String("LOG_LEVEL", "info"))
	ctx, stop := logx.SignalContext()
	defer stop()

	url := config.String("INGEST_URL", "http://localhost:8080") + "/v1/scans"
	apiKey := config.String("SIM_API_KEY", "")
	const tick = 250 * time.Millisecond
	runID := uuid.NewString()[:8]
	snapshotURL := config.String("REALTIME_URL", "http://localhost:8081") + "/api/snapshot"

	// Simulated scanner behaviour only; nothing here is security-sensitive.
	rng := rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), 42)) // #nosec G404
	fleet := seedFleet(rng)
	client := &http.Client{Timeout: 10 * time.Second}
	st := &stats{}
	st.rate.Store(int64(min(max(config.Int("SIM_RATE", 30), 1), maxRate)))
	w := &world{}

	// Generator -> queue -> single sender preserves scan order end to end.
	// The queue models a scanner's local buffer: generation continues while
	// ingest is unreachable, and nothing is dropped unless the buffer fills.
	queue := make(chan []model.ScanEvent, config.Int("SIM_BUFFER_BATCHES", 200_000))

	var wg sync.WaitGroup
	wg.Add(4)
	go func() { defer wg.Done(); w.sync(ctx, log, snapshotURL, 2*time.Second) }()
	go func() { defer wg.Done(); generate(ctx, log, st, queue, fleet, w, rng, tick) }()
	go func() { defer wg.Done(); send(ctx, log, st, client, url, apiKey, queue) }()
	go func() {
		defer wg.Done()
		srv := &http.Server{Addr: config.String("SIM_CONTROL_ADDR", ":8090"), Handler: controlAPI(st, queue, runID),
			ReadHeaderTimeout: 5 * time.Second}
		if err := httpx.Serve(ctx, log, srv); err != nil {
			// Fail fast: a second simulator that can't bind its control port
			// would otherwise keep generating conflicting scans unnoticed.
			log.Error("control api failed; shutting down", "err", err)
			stop()
		}
	}()

	log.Info("simulator started", "url", url, "rate_per_sec", st.rate.Load(), "run_id", runID,
		"authenticated", apiKey != "", "snapshot", snapshotURL)
	wg.Wait()
	log.Info("simulator stopped", "generated", st.generated.Load(), "accepted", st.accepted.Load(),
		"unsent", len(queue)+int(st.inFlight.Load()))
}

const maxRate = 200

func generate(ctx context.Context, log *slog.Logger, st *stats, queue chan<- []model.ScanEvent,
	fleet []*simULD, w *world, rng *rand.Rand, tick time.Duration) {
	t := time.NewTicker(tick)
	defer t.Stop()
	carry := 0.0
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if st.paused.Load() || !w.ready() {
			continue
		}
		// Fractional carry keeps low rates (e.g. 5/s at 4 ticks/s) accurate.
		carry += float64(st.rate.Load()) * tick.Seconds()
		n := int(carry)
		carry -= float64(n)
		batch := make([]model.ScanEvent, 0, n)
		for attempts := 0; len(batch) < n && attempts < n*5; attempts++ {
			u := fleet[rng.IntN(len(fleet))]
			s, known := w.get(u.id)
			if !u.reconcile(s, known, rng) {
				continue
			}
			ev := u.step(rng)
			ev.EventID = uuid.NewString() // client-assigned: retries are idempotent
			batch = append(batch, ev)
		}
		if len(batch) == 0 {
			continue
		}
		select {
		case queue <- batch:
			st.generated.Add(int64(len(batch)))
		default:
			log.Error("scanner buffer full; scans dropped at source", "count", len(batch))
		}
	}
}

// send posts batches strictly in order. A batch is retried with exponential
// backoff (same event ids) until ingest returns 202; only a non-retryable
// 4xx drops it.
func send(ctx context.Context, log *slog.Logger, st *stats, c *http.Client, url, apiKey string, queue <-chan []model.ScanEvent) {
	const minBackoff, maxBackoff = 250 * time.Millisecond, 10 * time.Second
	for {
		var batch []model.ScanEvent
		select {
		case <-ctx.Done():
			return
		case batch = <-queue:
		}
		st.inFlight.Store(int64(len(batch)))
		backoff := minBackoff
		for attempt := 1; ; attempt++ {
			durability, retryable, err := post(ctx, c, url, apiKey, batch)
			if err == nil {
				st.accepted.Add(int64(len(batch)))
				if durability == "spool" {
					st.spooled.Add(int64(len(batch)))
				}
				st.mu.Lock()
				for _, ev := range batch {
					st.sent = append(st.sent, ev.EventID)
				}
				st.mu.Unlock()
				break
			}
			if ctx.Err() != nil {
				return
			}
			if !retryable {
				st.rejected.Add(int64(len(batch)))
				log.Error("batch rejected permanently", "err", err, "count", len(batch))
				break
			}
			st.retries.Add(1)
			if attempt == 1 || attempt%10 == 0 {
				log.Warn("ingest unavailable; retrying batch", "err", err, "attempt", attempt, "backoff", backoff.String())
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, maxBackoff)
		}
		st.inFlight.Store(0)
	}
}

func post(ctx context.Context, c *http.Client, url, apiKey string, batch []model.ScanEvent) (durability string, retryable bool, err error) {
	body, err := json.Marshal(batch)
	if err != nil {
		return "", false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "uld-scanner-simulator/1.0")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := c.Do(req)
	if err != nil {
		return "", true, err // network error / timeout
	}
	defer resp.Body.Close()
	var out struct {
		Durability string `json:"durability"`
		Error      string `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	switch {
	case resp.StatusCode == http.StatusAccepted:
		return out.Durability, false, nil
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode >= 500:
		return "", true, fmt.Errorf("ingest returned %s: %s", resp.Status, out.Error)
	default:
		return "", false, fmt.Errorf("ingest returned %s: %s", resp.Status, out.Error)
	}
}

// controlAPI lets the chaos scripts observe and steer the simulator:
//
//	GET  /stats   counters (generated / accepted / queued …)
//	GET  /sent    newline-separated event ids acknowledged by ingest (the ledger)
//	POST /pause   stop generating new scans (queue keeps draining)
//	POST /resume
//	POST /rate    {"rate": n}  set scans per second (1-200)
func controlAPI(st *stats, queue chan []model.ScanEvent, runID string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok"})
	})
	mux.HandleFunc("GET /stats", func(w http.ResponseWriter, _ *http.Request) {
		// len(queue) counts batches; approximate scans from the configured rate.
		queuedBatches := len(queue)
		rate := int(st.rate.Load())
		queuedScans := queuedBatches*max(rate/4, 1) + int(st.inFlight.Load())
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"run_id":         runID,
			"rate_per_sec":   rate,
			"paused":         st.paused.Load(),
			"generated":      st.generated.Load(),
			"accepted":       st.accepted.Load(),
			"accepted_spool": st.spooled.Load(),
			"rejected":       st.rejected.Load(),
			"retries":        st.retries.Load(),
			"queued_batches": queuedBatches,
			"unsent":         queuedScans,
			"drained":        queuedBatches == 0 && st.inFlight.Load() == 0,
		})
	})
	mux.HandleFunc("GET /sent", func(w http.ResponseWriter, _ *http.Request) {
		st.mu.Lock()
		ids := slices.Clone(st.sent)
		st.mu.Unlock()
		w.Header().Set("Content-Type", "text/plain")
		for _, id := range ids {
			_, _ = io.WriteString(w, id+"\n")
		}
	})
	mux.HandleFunc("POST /pause", func(w http.ResponseWriter, _ *http.Request) {
		st.paused.Store(true)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"paused": true})
	})
	mux.HandleFunc("POST /resume", func(w http.ResponseWriter, _ *http.Request) {
		st.paused.Store(false)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"paused": false})
	})
	mux.HandleFunc("POST /rate", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Rate int `json:"rate"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req); err != nil || req.Rate < 1 || req.Rate > maxRate {
			httpx.WriteError(w, http.StatusBadRequest, fmt.Sprintf("rate must be 1-%d", maxRate), nil)
			return
		}
		st.rate.Store(int64(req.Rate))
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"rate_per_sec": req.Rate})
	})
	return mux
}

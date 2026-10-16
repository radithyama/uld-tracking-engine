// Command simulator emulates warehouse scanners: it walks the seeded ULD fleet
// through Receiving -> Build-up -> Staging -> Ramp and posts scan batches to
// the ingest API. Occasionally it overloads a ULD to exercise weight alerts.
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

type simULD struct {
	id       string
	tare     float64
	maxGross float64
	weight   float64
	target   float64 // planned gross weight for this build-up cycle
	flight   string
	stage    int // index into stages
}

var stages = []struct {
	zone      string
	scanner   string
	scanTypes []model.ScanType
}{
	{"RCV", "SCN-RCV", []model.ScanType{model.ScanBarcode}},
	{"BUP", "SCN-BUP", []model.ScanType{model.ScanRFID, model.ScanBarcode}},
	{"STA", "SCN-STG", []model.ScanType{model.ScanRFID}},
	{"STB", "SCN-STG", []model.ScanType{model.ScanRFID}},
	{"RMP", "SCN-RMP", []model.ScanType{model.ScanRFID}},
}

// Mirrors backend/migrations/002_seed.sql.
func seedFleet(r *rand.Rand) []*simULD {
	var fleet []*simULD
	for n := 1; n <= 12; n++ {
		flight := "QZ7510"
		if n%2 == 0 {
			flight = "GA716"
		}
		fleet = append(fleet, &simULD{id: fmt.Sprintf("AKE%dGA", 10000+n), tare: 82, maxGross: 1588, weight: 82, flight: flight})
	}
	for n := 1; n <= 8; n++ {
		fleet = append(fleet, &simULD{id: fmt.Sprintf("PMC%dGA", 20000+n), tare: 110, maxGross: 6804, weight: 110, flight: "GA880", stage: 1})
	}
	for _, u := range fleet {
		u.target = plannedFill(r, u.maxGross)
	}
	return fleet
}

// step advances one ULD's simulated life and returns the scan it produces.
func (u *simULD) step(r *rand.Rand) model.ScanEvent {
	const (
		rcv, bup, rmp = 0, 1, 4
	)
	switch {
	case u.stage == bup && u.weight < u.target && r.Float64() < 0.7:
		// Still in build-up: more cargo is loaded until the planned fill.
		u.weight = min(u.weight+r.Float64()*u.maxGross*0.2, u.target)
		// ~4% chance a build-up crew over-packs the ULD.
		if r.Float64() < 0.04 {
			u.weight = u.maxGross * (1.02 + r.Float64()*0.08)
		}
	case u.stage == rmp && r.Float64() < 0.5:
		// Flight departed: ULD returns empty to receiving with a new plan.
		u.stage, u.weight, u.target = rcv, u.tare, plannedFill(r, u.maxGross)
	case u.weight > u.maxGross && u.stage < rmp && r.Float64() < 0.3:
		// Ops caught the overweight ULD and rebuilt it — only possible before
		// loading. (A weight change on a LOADED ULD is what ULD-INT-003 flags.)
		u.weight = u.target
	case r.Float64() < 0.55:
		switch u.stage {
		case bup:
			u.stage = 2 + r.IntN(2) // Staging A or B
		case 2, 3:
			u.stage = rmp
		case rcv:
			u.stage = bup
		}
	}

	st := stages[u.stage]
	return model.ScanEvent{
		ULDID:        u.id,
		ScannerID:    fmt.Sprintf("%s-%02d", st.scanner, r.IntN(4)+1),
		ScanType:     st.scanTypes[r.IntN(len(st.scanTypes))],
		WeightKg:     float64(int(u.weight*10)) / 10,
		LocationID:   fmt.Sprintf("%s-%02d", st.zone, r.IntN(6)+1),
		FlightNumber: u.flight,
		Timestamp:    time.Now().UTC(),
	}
}

func plannedFill(r *rand.Rand, maxGross float64) float64 {
	return maxGross * (0.55 + r.Float64()*0.42)
}

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
	rate := max(config.Int("SIM_RATE", 30), 1) // events per second
	const tick = 250 * time.Millisecond
	perTick := max(rate*int(tick)/int(time.Second), 1)
	runID := uuid.NewString()[:8]

	rng := rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), 42))
	fleet := seedFleet(rng)
	client := &http.Client{Timeout: 10 * time.Second}
	st := &stats{}

	// Generator -> queue -> single sender preserves scan order end to end.
	// The queue models a scanner's local buffer: generation continues while
	// ingest is unreachable, and nothing is dropped unless the buffer fills.
	queue := make(chan []model.ScanEvent, config.Int("SIM_BUFFER_BATCHES", 200_000))

	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); generate(ctx, log, st, queue, fleet, rng, tick, perTick) }()
	go func() { defer wg.Done(); send(ctx, log, st, client, url, apiKey, queue) }()
	go func() {
		defer wg.Done()
		srv := &http.Server{Addr: config.String("SIM_CONTROL_ADDR", ":8090"), Handler: controlAPI(st, queue, runID, rate),
			ReadHeaderTimeout: 5 * time.Second}
		if err := httpx.Serve(ctx, log, srv); err != nil {
			log.Error("control api", "err", err)
		}
	}()

	log.Info("simulator started", "url", url, "rate_per_sec", rate, "run_id", runID, "authenticated", apiKey != "")
	wg.Wait()
	log.Info("simulator stopped", "generated", st.generated.Load(), "accepted", st.accepted.Load(),
		"unsent", len(queue)+int(st.inFlight.Load()))
}

func generate(ctx context.Context, log *slog.Logger, st *stats, queue chan<- []model.ScanEvent,
	fleet []*simULD, rng *rand.Rand, tick time.Duration, perTick int) {
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if st.paused.Load() {
			continue
		}
		batch := make([]model.ScanEvent, 0, perTick)
		for range perTick {
			ev := fleet[rng.IntN(len(fleet))].step(rng)
			ev.EventID = uuid.NewString() // client-assigned: retries are idempotent
			batch = append(batch, ev)
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
func controlAPI(st *stats, queue chan []model.ScanEvent, runID string, rate int) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok"})
	})
	mux.HandleFunc("GET /stats", func(w http.ResponseWriter, _ *http.Request) {
		// len(queue) counts batches; approximate scans from the configured rate.
		queuedBatches := len(queue)
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
	return mux
}

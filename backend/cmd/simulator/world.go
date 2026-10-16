package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/radithya/uld-engine/internal/model"
)

// The simulator models the scanners at every station. Flights themselves are
// flown by the processor's network clock; the simulator follows the server's
// view of each ULD (synced from the realtime snapshot) so it never scans a
// ULD that is airborne or at a different station.

const hub = "CGK"

type stage struct {
	zone      string
	scanner   string
	scanTypes []model.ScanType
	builds    bool // cargo is built up (weight grows) in this stage
	loaded    bool // ramp: ULD is on its flight, waiting for departure
}

var hubStages = []stage{
	{zone: "RCV", scanner: "SCN-RCV", scanTypes: []model.ScanType{model.ScanBarcode}},
	{zone: "BUP", scanner: "SCN-BUP", scanTypes: []model.ScanType{model.ScanRFID, model.ScanBarcode}, builds: true},
	{zone: "STA", scanner: "SCN-STG", scanTypes: []model.ScanType{model.ScanRFID}},
	{zone: "STB", scanner: "SCN-STG", scanTypes: []model.ScanType{model.ScanRFID}},
	{zone: "RMP", scanner: "SCN-RMP", scanTypes: []model.ScanType{model.ScanRFID}, loaded: true},
}

var outstationStages = []stage{
	{zone: "RCV", scanner: "RCV", scanTypes: []model.ScanType{model.ScanBarcode}},
	{zone: "STA", scanner: "STG", scanTypes: []model.ScanType{model.ScanRFID}, builds: true},
	{zone: "RMP", scanner: "RMP", scanTypes: []model.ScanType{model.ScanRFID}, loaded: true},
}

// Outbound flight per home route, and the return flight from each outstation.
// Mirrors routes in backend/migrations/004.
var returnFlight = map[string]string{"SIN": "QZ7511", "SYD": "GA717", "MEL": "GA723", "NRT": "GA881"}

type simULD struct {
	id       string
	tare     float64
	maxGross float64
	weight   float64
	target   float64 // planned gross weight for this build-up cycle
	outbound string  // hub departure this ULD rotates on
	station  string
	stage    int
	synced   bool // adopted the server's state at least once
}

// The first 12 AKE / 8 PMC match backend/migrations/002_seed.sql; the rest
// are auto-registered by the processor on their first scan. 8 containers per
// LD3 route and 12 pallets keep build-up activity going while other ULDs
// wait on the ramp or fly.
func seedFleet(r *rand.Rand) []*simULD {
	var fleet []*simULD
	for n := 1; n <= 24; n++ {
		flight := map[int]string{0: "QZ7510", 1: "GA716", 2: "GA722"}[(n-1)%3]
		fleet = append(fleet, &simULD{id: fmt.Sprintf("AKE%dGA", 10000+n), tare: 82, maxGross: 1588, weight: 82, outbound: flight, station: hub})
	}
	for n := 1; n <= 12; n++ {
		fleet = append(fleet, &simULD{id: fmt.Sprintf("PMC%dGA", 20000+n), tare: 110, maxGross: 6804, weight: 110, outbound: "GA880", station: hub})
	}
	for _, u := range fleet {
		u.target = plannedFill(r, u.maxGross)
	}
	return fleet
}

func plannedFill(r *rand.Rand, maxGross float64) float64 {
	return maxGross * (0.55 + r.Float64()*0.42)
}

func stagesFor(station string) []stage {
	if station == hub {
		return hubStages
	}
	return outstationStages
}

func (u *simULD) flight() string {
	if u.station == hub {
		return u.outbound
	}
	return returnFlight[u.station]
}

func locationID(station, zone string, n int) string {
	if station == hub {
		return fmt.Sprintf("%s-%02d", zone, n)
	}
	return fmt.Sprintf("%s-%s-%02d", station, zone, n)
}

// serverULD is the subset of the realtime snapshot the simulator needs.
type serverULD struct {
	Status     string  `json:"status"`
	Station    string  `json:"station"`
	Zone       string  `json:"zone"`
	WeightKg   float64 `json:"weight_kg"`
	LocationID string  `json:"location_id"`
}

// world holds the latest server view, refreshed in the background.
type world struct {
	mu    sync.RWMutex
	state map[string]serverULD
	at    time.Time
}

func (w *world) get(id string) (serverULD, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	s, ok := w.state[id]
	return s, ok && !w.at.IsZero()
}

func (w *world) ready() bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return !w.at.IsZero()
}

func (w *world) sync(ctx context.Context, log *slog.Logger, url string, every time.Duration) {
	c := &http.Client{Timeout: 5 * time.Second}
	t := time.NewTicker(every)
	defer t.Stop()
	failures := 0
	for {
		if err := w.fetch(ctx, c, url); err != nil {
			failures++
			if failures == 1 || failures%30 == 0 {
				log.Warn("snapshot sync failed; holding scans for unknown ULDs", "err", err)
			}
		} else {
			failures = 0
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (w *world) fetch(ctx context.Context, c *http.Client, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var env struct {
		Data struct {
			ULDs []struct {
				ID string `json:"id"`
				serverULD
			} `json:"ulds"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return err
	}
	m := make(map[string]serverULD, len(env.Data.ULDs))
	for _, u := range env.Data.ULDs {
		m[u.ID] = u.serverULD
	}
	w.mu.Lock()
	w.state, w.at = m, time.Now()
	w.mu.Unlock()
	return nil
}

// reconcile aligns the simulator's model of u with the server's. It returns
// false if u must not be scanned right now (airborne, waiting on the ramp,
// or unknown to the server yet).
func (u *simULD) reconcile(s serverULD, known bool, r *rand.Rand) bool {
	if !known {
		return u.station == hub && u.stage == 0 // first ever scan of a new ULD
	}
	if s.Status == string(model.StatusInFlight) || s.Station == "" {
		return false
	}
	if !u.synced {
		// First sight after (re)start: adopt the server's station, stage and
		// weight so a simulator restart never walks ULDs backwards.
		u.synced, u.station, u.stage, u.weight = true, s.Station, 0, s.WeightKg
		for i, st := range stagesFor(s.Station) {
			if st.zone == s.Zone {
				u.stage = i
			}
		}
		return s.Status != string(model.StatusLoaded)
	}
	if s.Station != u.station {
		// Landed somewhere new: start over in that station's receiving area.
		u.station, u.stage, u.weight = s.Station, 0, s.WeightKg
		u.target = plannedFill(r, u.maxGross)
		return true
	}
	// Loaded ULDs wait for the network clock to depart their flight. Never
	// re-scanning them avoids racing a departure, and means the simulator
	// can't trip the loaded-weight-change (tamper) detection by itself.
	if s.Status == string(model.StatusLoaded) {
		return false
	}
	// Server moved it back (e.g. arrival at the hub): follow its zone.
	for i, st := range stagesFor(u.station) {
		if st.zone == s.Zone && i < u.stage {
			u.stage = i
		}
	}
	return true
}

// step advances one ULD and returns the scan it produces.
func (u *simULD) step(r *rand.Rand) model.ScanEvent {
	stages := stagesFor(u.station)
	cur := stages[u.stage]
	switch {
	case u.stage == 0 && u.weight > u.tare*1.05:
		// Breakdown on receipt: arriving cargo is unloaded, the ULD is empty.
		u.weight = u.tare
	case cur.builds && u.weight < u.target && r.Float64() < 0.7:
		u.weight = min(u.weight+r.Float64()*u.maxGross*0.2, u.target)
		if r.Float64() < 0.04 { // a crew over-packs it now and then
			u.weight = u.maxGross * (1.02 + r.Float64()*0.08)
		}
	case u.weight > u.maxGross && !cur.loaded && r.Float64() < 0.3:
		u.weight = u.target // caught and rebuilt before loading
	case r.Float64() < 0.55 && !cur.loaded:
		next := u.stage + 1
		if u.station == hub && cur.zone == "BUP" {
			next = 2 + r.IntN(2) // staging A or B
		} else if u.station == hub && (cur.zone == "STA" || cur.zone == "STB") {
			next = len(stages) - 1
		}
		u.stage = min(next, len(stages)-1)
	}
	st := stages[u.stage]
	scanner := st.scanner
	if u.station != hub {
		scanner = "SCN-" + u.station + "-" + st.scanner
	}
	return model.ScanEvent{
		ULDID:        u.id,
		ScannerID:    fmt.Sprintf("%s-%02d", strings.TrimSuffix(scanner, "-"), r.IntN(4)+1),
		ScanType:     st.scanTypes[r.IntN(len(st.scanTypes))],
		WeightKg:     float64(int(u.weight*10)) / 10,
		LocationID:   locationID(u.station, st.zone, r.IntN(map[bool]int{true: 6, false: 3}[u.station == hub])+1),
		FlightNumber: u.flight(),
		Timestamp:    time.Now().UTC(),
	}
}

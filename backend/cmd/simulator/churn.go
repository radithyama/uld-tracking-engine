package main

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/radithya/uld-engine/internal/model"
)

// The traffic generator is an on/off demo switch for extra warehouse
// activity. The regular fleet follows real flight rotations, so it goes quiet
// whenever every ULD is loaded or airborne; the generator fills those gaps.
//
// It cycles a pool of sandbox ULDs (ids ending in "SB") through each
// station's warehouse: receiving → build-up → staging → back to receiving.
// They never reach the ramp or a flight, stay at one station and keep
// realistic weights, so they can't disturb load plans or trip the security
// detections. The processor purges sandbox ULDs a while after their last
// scan (SANDBOX_ULD_TTL), so switching the generator off cleans up by itself.
// It also switches itself off after a time limit, so a forgotten toggle on
// a public demo doesn't run forever.

// generator is the shared on/off state, read by the control API.
type generator struct {
	enabled atomic.Bool
	until   atomic.Int64 // unix nanos when it switches itself off
	rate    int          // scans per second while enabled
	maxRun  time.Duration
}

func (g *generator) on(now time.Time) bool {
	return g.enabled.Load() && now.UnixNano() < g.until.Load()
}

func (g *generator) set(enabled bool, now time.Time) {
	if enabled {
		g.until.Store(now.Add(g.maxRun).UnixNano())
	}
	g.enabled.Store(enabled)
}

func (g *generator) state(now time.Time) map[string]any {
	on := g.on(now)
	s := map[string]any{"enabled": on, "rate_per_sec": g.rate, "max_run_seconds": int(g.maxRun.Seconds())}
	if on {
		s["until"] = time.Unix(0, g.until.Load()).UTC()
		s["remaining_seconds"] = int(time.Until(time.Unix(0, g.until.Load())).Seconds())
	}
	return s
}

type churnULD struct {
	id       string
	station  string
	zones    []string // warehouse zones it cycles through (never the ramp)
	stage    int
	tare     float64
	maxGross float64
	weight   float64
}

// churnPool: more containers at the hub, a few at each outstation.
func churnPool(r *rand.Rand) []*churnULD {
	var pool []*churnULD
	add := func(station string, n int, zones []string) {
		for i := 0; i < n; i++ {
			seq := 70001 + len(pool)
			u := &churnULD{station: station, zones: zones, stage: r.IntN(len(zones))}
			if i%3 == 2 {
				u.id, u.tare, u.maxGross = fmt.Sprintf("PMC%dSB", seq), 110, 6804
			} else {
				u.id, u.tare, u.maxGross = fmt.Sprintf("AKE%dSB", seq), 82, 1588
			}
			u.weight = u.tare + r.Float64()*(u.maxGross*0.5)
			pool = append(pool, u)
		}
	}
	add(hub, 12, []string{"RCV", "BUP", "STA", "STB"})
	for _, s := range []string{"SIN", "SYD", "MEL", "NRT", "DPS"} {
		add(s, 3, []string{"RCV", "STA"})
	}
	return pool
}

func (u *churnULD) step(r *rand.Rand) model.ScanEvent {
	// Mostly move to the next zone; sometimes a re-scan in place.
	if r.Float64() < 0.7 {
		u.stage = (u.stage + 1) % len(u.zones)
	}
	zone := u.zones[u.stage]
	switch {
	case u.stage == 0:
		// New shipment received: a fresh, partly filled container.
		u.weight = u.tare + r.Float64()*u.maxGross*0.4
	case zone == "BUP" || (u.station != hub && zone == "STA"):
		// Built up, but always within 95% of the rating (no weight alerts).
		u.weight = min(u.weight+r.Float64()*u.maxGross*0.3, u.maxGross*0.95)
	}
	slots := 3
	scanner := "SCN-" + u.station + "-" + zone
	if u.station == hub {
		slots, scanner = 6, "SCN-"+zone
	}
	scanType := model.ScanRFID
	if zone == "RCV" {
		scanType = model.ScanBarcode
	}
	return model.ScanEvent{
		EventID:    uuid.NewString(),
		ULDID:      u.id,
		ScannerID:  fmt.Sprintf("%s-%02d", scanner, r.IntN(4)+1),
		ScanType:   scanType,
		WeightKg:   float64(int(u.weight*10)) / 10,
		LocationID: locationID(u.station, zone, r.IntN(slots)+1),
		Timestamp:  time.Now().UTC(),
	}
}

// churn feeds generator scans into the same queue as the regular fleet.
func churn(ctx context.Context, log *slog.Logger, st *stats, g *generator, queue chan<- []model.ScanEvent, r *rand.Rand, tick time.Duration) {
	pool := churnPool(r)
	t := time.NewTicker(tick)
	defer t.Stop()
	carry := 0.0
	was := false
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			on := g.on(now)
			if on != was {
				log.Info("traffic generator", "enabled", on)
				was = on
			}
			if !on {
				g.enabled.Store(false) // time limit reached
				carry = 0
				continue
			}
			carry += float64(g.rate) * tick.Seconds()
			n := int(carry)
			carry -= float64(n)
			if n == 0 {
				continue
			}
			batch := make([]model.ScanEvent, 0, n)
			for i := 0; i < n; i++ {
				batch = append(batch, pool[r.IntN(len(pool))].step(r))
			}
			select {
			case queue <- batch:
				st.generated.Add(int64(len(batch)))
			default:
				log.Warn("scanner buffer full; generator scans dropped", "count", len(batch))
			}
		}
	}
}

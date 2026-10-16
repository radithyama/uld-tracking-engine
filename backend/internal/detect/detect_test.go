package detect

import (
	"testing"
	"time"

	"github.com/radithya/uld-engine/internal/model"
)

var (
	cgk = Fix{Station: "CGK", LocationID: "STA-01", Lat: -6.1256, Lon: 106.6559}
	mel = Fix{Station: "MEL", LocationID: "MEL-RCV-01", Lat: -37.6690, Lon: 144.8410}
	t0  = time.Date(2026, 10, 15, 8, 0, 0, 0, time.UTC)
)

func input(weight float64) Input {
	return Input{
		Event:   &model.ScanEvent{ULDID: "AKE10001GA", EventID: "e", WeightKg: weight},
		Cur:     cgk,
		TareKg:  82,
		LimitKg: 1500,
	}
}

func rules(fs []Finding) map[string]bool {
	m := map[string]bool{}
	for _, f := range fs {
		m[f.Rule.ID] = true
	}
	return m
}

func TestHaversine(t *testing.T) {
	// CGK–MEL great-circle distance is ~5,210 km.
	if d := HaversineKm(cgk.Lat, cgk.Lon, mel.Lat, mel.Lon); d < 5150 || d > 5260 {
		t.Fatalf("CGK-MEL = %.0f km", d)
	}
}

func TestImpossibleTravel(t *testing.T) {
	cfg := DefaultConfig()
	for _, c := range []struct {
		name string
		dt   time.Duration
		want bool
	}{
		{"10 minutes CGK->MEL", 10 * time.Minute, true},
		{"4 hours CGK->MEL (~1300 km/h)", 4 * time.Hour, true},
		{"7 hours CGK->MEL (feasible)", 7 * time.Hour, false},
		{"reverse-ordered scans", -10 * time.Minute, true},
	} {
		in := input(500)
		prev := cgk
		prev.At = t0
		in.Prev = &prev
		in.Cur = mel
		in.Cur.At = t0.Add(c.dt)
		if got := rules(Evaluate(cfg, in))["ULD-GEO-001"]; got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
	// Same station never triggers, however fast.
	in := input(500)
	prev := cgk
	prev.At = t0
	in.Prev, in.Cur.At = &prev, t0.Add(time.Second)
	if len(Evaluate(cfg, in)) != 0 {
		t.Fatal("same-station scan flagged")
	}
}

func TestWeightIntegrity(t *testing.T) {
	cfg := DefaultConfig()
	if r := rules(Evaluate(cfg, input(1600))); r["ULD-INT-001"] {
		t.Fatal("ordinary overload (7%) must stay an ops alert, not a security finding")
	}
	if r := rules(Evaluate(cfg, input(1900))); !r["ULD-INT-001"] {
		t.Fatal("27% over limit should be implausible")
	}
	if r := rules(Evaluate(cfg, input(40))); !r["ULD-INT-002"] {
		t.Fatal("below tare not flagged")
	}
}

func TestLoadedWeightChange(t *testing.T) {
	cfg := DefaultConfig()
	mk := func(prevW, newW float64, stale bool) map[string]bool {
		in := input(newW)
		prev := cgk
		prev.At = t0
		in.Prev, in.Cur.At = &prev, t0.Add(time.Minute)
		in.PrevStatus, in.NewStatus, in.PrevWeight, in.Stale = model.StatusLoaded, model.StatusLoaded, prevW, stale
		return rules(Evaluate(cfg, in))
	}
	if mk(1000, 1040, false)["ULD-INT-003"] {
		t.Fatal("sensor noise within tolerance flagged")
	}
	if !mk(1000, 1400, false)["ULD-INT-003"] {
		t.Fatal("+400 kg on a loaded ULD not flagged")
	}
	if mk(1000, 1400, true)["ULD-INT-003"] {
		t.Fatal("stale events must not be compared against newer state")
	}
}

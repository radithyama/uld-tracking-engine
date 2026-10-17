package main

import (
	"math/rand/v2"
	"strings"
	"testing"
	"time"
)

// Generator scans must always be valid, stay off the ramp and flights, keep
// each ULD at one station, and stay within weight limits (no false alerts).
func TestChurnScansAreSafe(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	pool := churnPool(r)
	home := map[string]string{}
	for i := 0; i < 5000; i++ {
		u := pool[r.IntN(len(pool))]
		ev := u.step(r)
		now := time.Now()
		ev.Normalize(now)
		if err := ev.Validate(now); err != nil {
			t.Fatalf("invalid scan %+v: %v", ev, err)
		}
		if !strings.HasSuffix(ev.ULDID, "SB") {
			t.Fatalf("%s is not a purgeable sandbox id", ev.ULDID)
		}
		if strings.Contains(ev.LocationID, "RMP") || ev.FlightNumber != "" {
			t.Fatalf("scan touches the ramp or a flight: %+v", ev)
		}
		if ev.WeightKg < u.tare || ev.WeightKg > u.maxGross*0.95 {
			t.Fatalf("weight %.1f outside [%v, %v]", ev.WeightKg, u.tare, u.maxGross*0.95)
		}
		station := "CGK"
		if parts := strings.Split(ev.LocationID, "-"); len(parts) == 3 {
			station = parts[0]
		}
		if prev, ok := home[ev.ULDID]; ok && prev != station {
			t.Fatalf("%s moved from %s to %s", ev.ULDID, prev, station)
		}
		home[ev.ULDID] = station
	}
}

func TestGeneratorAutoOff(t *testing.T) {
	g := &generator{rate: 5, maxRun: time.Minute}
	now := time.Now()
	g.set(true, now)
	if !g.on(now.Add(30 * time.Second)) {
		t.Fatal("generator off before its time limit")
	}
	if g.on(now.Add(61 * time.Second)) {
		t.Fatal("generator still on after its time limit")
	}
	g.set(false, now)
	if g.on(now) {
		t.Fatal("generator on after being switched off")
	}
}

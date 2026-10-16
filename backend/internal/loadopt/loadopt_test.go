package loadopt

import (
	"fmt"
	"math"
	"math/rand/v2"
	"reflect"
	"testing"
)

// A330-like lower deck: FWD 11L-15R, AFT 31L-34R, bulk 51 (no ULDs).
func a330() (Aircraft, []Position) {
	ac := Aircraft{Registration: "PK-GPA", Type: "A330-300", OEWKg: 124000, OEWArmM: 27.64,
		LEMACM: 25.6, MACM: 7.27, CGFwdPct: 14, CGAftPct: 38, CGTargetPct: 30, MaxPayloadKg: 18000}
	var ps []Position
	seq := 0
	for _, r := range []struct {
		row  int
		hold string
		arm  float64
	}{{11, "FWD", 12}, {12, "FWD", 13.6}, {13, "FWD", 15.2}, {14, "FWD", 16.8}, {15, "FWD", 18.4},
		{31, "AFT", 37}, {32, "AFT", 38.6}, {33, "AFT", 40.2}, {34, "AFT", 41.8}} {
		for _, side := range []struct {
			s   string
			lat int
		}{{"L", -1}, {"R", 1}} {
			seq++
			ps = append(ps, Position{Code: fmt.Sprintf("%d%s", r.row, side.s), Deck: "LOWER", Hold: r.hold,
				ArmM: r.arm, Lateral: side.lat, MaxKg: 1588, ULDTypes: []string{"AKE", "AKH"}, Seq: seq})
		}
	}
	ps = append(ps, Position{Code: "51", Deck: "LOWER", Hold: "BULK", ArmM: 45, MaxKg: 3468, Seq: 99})
	return ac, ps
}

func ulds(weights ...float64) []ULD {
	var out []ULD
	for i, w := range weights {
		out = append(out, ULD{ID: fmt.Sprintf("AKE%05dGA", i+1), Type: "AKE", WeightKg: w, Status: "STAGED"})
	}
	return out
}

func posOf(p Plan) map[string]string {
	m := map[string]string{}
	for _, a := range p.Assignments {
		m[a.ULDID] = a.Position
	}
	return m
}

func TestOptimizeImprovesCGOverNaivePlan(t *testing.T) {
	ac, ps := a330()
	us := ulds(1500, 1450, 1300, 1200, 1100, 900, 800, 700)
	// Naive plan: fill the forward hold front to back.
	var naive []Assignment
	for i, u := range us {
		naive = append(naive, Assignment{ULDID: u.ID, Position: ps[i].Code})
	}
	before := Evaluate(ac, ps, us, naive)
	after := Optimize(ac, ps, us)

	if len(after.Assignments) != len(us) || len(after.Unassigned) != 0 {
		t.Fatalf("expected all ULDs placed: %+v", after)
	}
	db, da := math.Abs(before.Balance.CGPctMAC-ac.CGTargetPct), math.Abs(after.Balance.CGPctMAC-ac.CGTargetPct)
	if da >= db {
		t.Fatalf("CG error not reduced: before %.2f%%MAC (Δ%.2f) after %.2f%%MAC (Δ%.2f)",
			before.Balance.CGPctMAC, db, after.Balance.CGPctMAC, da)
	}
	if !after.Balance.WithinEnvelope {
		t.Fatalf("CG %.2f outside envelope", after.Balance.CGPctMAC)
	}
	if math.Abs(after.Balance.LateralImbalanceKg) > 200 {
		t.Fatalf("lateral imbalance %.0f kg too high", after.Balance.LateralImbalanceKg)
	}
	t.Logf("naive CG %.2f%%MAC -> optimized %.2f%%MAC (target %.0f), lateral %.0f kg, %d iterations",
		before.Balance.CGPctMAC, after.Balance.CGPctMAC, ac.CGTargetPct, after.Balance.LateralImbalanceKg, after.Iterations)
}

func TestHardConstraints(t *testing.T) {
	ac, ps := a330()
	us := append(ulds(1000, 1700), ULD{ID: "PMC20001GA", Type: "PMC", WeightKg: 3000})
	p := Optimize(ac, ps, us)
	got := posOf(p)
	if _, ok := got["AKE00002GA"]; ok {
		t.Fatal("1700 kg AKE placed although every position is limited to 1588 kg")
	}
	if _, ok := got["PMC20001GA"]; ok {
		t.Fatal("PMC placed on an LD3-only deck")
	}
	if got["AKE00001GA"] == "51" {
		t.Fatal("ULD placed in bulk compartment")
	}
	codes := map[string]string{}
	for _, u := range p.Unassigned {
		codes[u.ULDID] = u.Code
	}
	if codes["AKE00002GA"] != "OVER_POSITION_LIMIT" || codes["PMC20001GA"] != "NO_COMPATIBLE_POSITION" {
		t.Fatalf("unexpected unassigned reasons: %+v", p.Unassigned)
	}
	for _, a := range p.Assignments {
		for _, pos := range ps {
			if pos.Code == a.Position && !Fits(pos, ULD{Type: "AKE", WeightKg: 1000}) {
				t.Fatalf("infeasible assignment %+v", a)
			}
		}
	}
}

func TestLockedULDsStay(t *testing.T) {
	ac, ps := a330()
	us := ulds(1500, 900, 800)
	us[0].Locked, us[0].Current, us[0].Status = true, "11L", "LOADED"
	p := Optimize(ac, ps, us)
	if posOf(p)["AKE00001GA"] != "11L" {
		t.Fatalf("locked ULD moved: %+v", p.Assignments)
	}
}

func TestDeterministicRegardlessOfInputOrder(t *testing.T) {
	ac, ps := a330()
	us := ulds(1500, 1450, 1300, 1200, 1100, 900, 800, 700, 650, 600)
	ref := Optimize(ac, ps, us)
	r := rand.New(rand.NewPCG(1, 2))
	for range 20 {
		sh := append([]ULD(nil), us...)
		r.Shuffle(len(sh), func(i, j int) { sh[i], sh[j] = sh[j], sh[i] })
		shp := append([]Position(nil), ps...)
		r.Shuffle(len(shp), func(i, j int) { shp[i], shp[j] = shp[j], shp[i] })
		if got := Optimize(ac, shp, sh); !reflect.DeepEqual(got.Assignments, ref.Assignments) {
			t.Fatalf("non-deterministic plan:\n%v\n%v", got.Assignments, ref.Assignments)
		}
	}
}

func TestEvaluateFlagsManualOverride(t *testing.T) {
	ac, ps := a330()
	us := ulds(1700, 900)
	p := Evaluate(ac, ps, us, []Assignment{{"AKE00001GA", "11L"}, {"AKE00002GA", "11L"}})
	codes := []string{}
	for _, i := range p.Issues {
		codes = append(codes, i.Code)
	}
	if fmt.Sprint(codes) != "[OVER_POSITION_LIMIT POSITION_TAKEN]" {
		t.Fatalf("issues = %v", codes)
	}
}

func TestPayloadExceeded(t *testing.T) {
	ac, ps := a330()
	ac.MaxPayloadKg = 2000
	p := Optimize(ac, ps, ulds(1200, 1100))
	found := false
	for _, i := range p.Issues {
		found = found || i.Code == "PAYLOAD_EXCEEDED"
	}
	if !found {
		t.Fatalf("expected PAYLOAD_EXCEEDED, got %+v", p.Issues)
	}
}

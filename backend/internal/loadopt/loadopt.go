// Package loadopt assigns ULDs to aircraft cargo positions.
//
// The optimizer keeps every hard constraint, namely ULD type/contour
// compatibility, the per-position structural limit, and keeping already
// loaded ULDs where they are. Within those constraints it minimises:
//
//	cost = |CG(%MAC) − target|  +  LateralWeight × |ΣL − ΣR| / Σcargo  (+ envelope penalty)
//
// where CG is the zero-fuel-weight centre of gravity (operating empty weight
// plus cargo) expressed as a percentage of the mean aerodynamic chord.
//
// Algorithm: heavy-first greedy placement (heavy ULDs have the most leverage
// on CG, so they get first pick of arms), followed by steepest-descent local
// search over single moves and pairwise swaps until no move improves the
// cost. Problem sizes are tiny (≤ ~30 positions), so this converges in
// microseconds and is fully deterministic (ties break on position sequence).
//
// The balance model is deliberately simplified (no fuel, no index units, no
// trim sheet); it is a planning aid for a demo, not certified W&B software.
package loadopt

import (
	"fmt"
	"math"
	"slices"
	"sort"
)

const (
	LateralWeight   = 5.0   // cost of a 100% lateral imbalance, in %MAC-equivalents
	EnvelopePenalty = 100.0 // added when CG falls outside the envelope
	maxIterations   = 500
)

type Aircraft struct {
	Registration string  `json:"registration"`
	Type         string  `json:"type"`
	OEWKg        float64 `json:"oew_kg"`
	OEWArmM      float64 `json:"oew_arm_m"`
	LEMACM       float64 `json:"lemac_m"`
	MACM         float64 `json:"mac_m"`
	CGFwdPct     float64 `json:"cg_fwd_pct"`
	CGAftPct     float64 `json:"cg_aft_pct"`
	CGTargetPct  float64 `json:"cg_target_pct"`
	MaxPayloadKg float64 `json:"max_payload_kg"`
}

type Position struct {
	Code     string   `json:"code"`
	Deck     string   `json:"deck"`
	Hold     string   `json:"hold"`
	ArmM     float64  `json:"arm_m"`
	Lateral  int      `json:"lateral"` // -1 left, 0 centre, +1 right
	MaxKg    float64  `json:"max_weight_kg"`
	ULDTypes []string `json:"uld_types"` // empty = bulk compartment, takes no ULDs
	Seq      int      `json:"seq"`
}

type ULD struct {
	ID       string  `json:"id"`
	Type     string  `json:"uld_type"`
	WeightKg float64 `json:"weight_kg"`
	Status   string  `json:"status"`
	// Locked ULDs are physically loaded at Current and are never moved.
	Locked  bool   `json:"locked"`
	Current string `json:"current_position,omitempty"`
}

type Assignment struct {
	ULDID    string `json:"uld_id"`
	Position string `json:"position"`
}

type Issue struct {
	Code     string `json:"code"` // NO_COMPATIBLE_POSITION | OVER_POSITION_LIMIT | NO_FREE_POSITION | PAYLOAD_EXCEEDED | LOCKED_OVER_LIMIT | CG_OUT_OF_ENVELOPE
	ULDID    string `json:"uld_id,omitempty"`
	Position string `json:"position,omitempty"`
	Message  string `json:"message"`
}

type Balance struct {
	CargoKg            float64            `json:"cargo_kg"`
	ZFWKg              float64            `json:"zfw_kg"`
	CGArmM             float64            `json:"cg_arm_m"`
	CGPctMAC           float64            `json:"cg_pct_mac"`
	TargetPctMAC       float64            `json:"target_pct_mac"`
	FwdLimitPctMAC     float64            `json:"fwd_limit_pct_mac"`
	AftLimitPctMAC     float64            `json:"aft_limit_pct_mac"`
	WithinEnvelope     bool               `json:"within_envelope"`
	LateralImbalanceKg float64            `json:"lateral_imbalance_kg"` // ΣR − ΣL
	HoldKg             map[string]float64 `json:"hold_kg"`
}

type Plan struct {
	Assignments []Assignment `json:"assignments"`
	Unassigned  []Issue      `json:"unassigned"`
	Issues      []Issue      `json:"issues"`
	Balance     Balance      `json:"balance"`
	Cost        float64      `json:"cost"`
	Iterations  int          `json:"iterations"`
}

// Fits reports whether ULD u may occupy position p.
func Fits(p Position, u ULD) bool {
	return slices.Contains(p.ULDTypes, u.Type) && u.WeightKg <= p.MaxKg
}

type state struct {
	ac    Aircraft
	pos   []Position        // sorted by Seq
	byPos map[string]int    // position code -> index into pos
	ulds  map[string]ULD    // by id
	at    map[string]string // uld -> position
	occ   map[string]string // position -> uld
}

func newState(ac Aircraft, positions []Position, ulds []ULD) *state {
	s := &state{ac: ac, byPos: map[string]int{}, ulds: map[string]ULD{}, at: map[string]string{}, occ: map[string]string{}}
	s.pos = slices.Clone(positions)
	sort.SliceStable(s.pos, func(i, j int) bool { return s.pos[i].Seq < s.pos[j].Seq })
	for i, p := range s.pos {
		s.byPos[p.Code] = i
	}
	for _, u := range ulds {
		s.ulds[u.ID] = u
	}
	return s
}

func (s *state) place(uld, pos string) { s.at[uld], s.occ[pos] = pos, uld }
func (s *state) remove(uld string) {
	if p, ok := s.at[uld]; ok {
		delete(s.occ, p)
		delete(s.at, uld)
	}
}

func (s *state) balance() Balance {
	b := Balance{
		TargetPctMAC: s.ac.CGTargetPct, FwdLimitPctMAC: s.ac.CGFwdPct, AftLimitPctMAC: s.ac.CGAftPct,
		HoldKg: map[string]float64{},
	}
	moment := s.ac.OEWKg * s.ac.OEWArmM
	var left, right float64
	for _, p := range s.pos { // ordered iteration keeps float sums deterministic
		uld := s.occ[p.Code]
		if uld == "" {
			continue
		}
		u := s.ulds[uld]
		b.CargoKg += u.WeightKg
		moment += u.WeightKg * p.ArmM
		b.HoldKg[p.Hold] += u.WeightKg
		switch p.Lateral {
		case -1:
			left += u.WeightKg
		case 1:
			right += u.WeightKg
		}
	}
	b.ZFWKg = s.ac.OEWKg + b.CargoKg
	if b.ZFWKg > 0 {
		b.CGArmM = moment / b.ZFWKg
	}
	if s.ac.MACM > 0 {
		b.CGPctMAC = (b.CGArmM - s.ac.LEMACM) / s.ac.MACM * 100
	}
	b.WithinEnvelope = b.CGPctMAC >= s.ac.CGFwdPct && b.CGPctMAC <= s.ac.CGAftPct
	b.LateralImbalanceKg = right - left
	return b
}

func (s *state) cost() float64 {
	b := s.balance()
	c := math.Abs(b.CGPctMAC - s.ac.CGTargetPct)
	if b.CargoKg > 0 {
		c += LateralWeight * math.Abs(b.LateralImbalanceKg) / b.CargoKg
	}
	if !b.WithinEnvelope {
		c += EnvelopePenalty + math.Min(math.Abs(b.CGPctMAC-s.ac.CGFwdPct), math.Abs(b.CGPctMAC-s.ac.CGAftPct))
	}
	return c
}

// Evaluate scores an explicit assignment (e.g. the current plan or a manual
// override) without changing it. Assignments to unknown or incompatible
// positions are reported as issues and excluded from the balance.
func Evaluate(ac Aircraft, positions []Position, ulds []ULD, assignments []Assignment) Plan {
	s := newState(ac, positions, ulds)
	var issues []Issue
	for _, a := range assignments {
		u, okU := s.ulds[a.ULDID]
		i, okP := s.byPos[a.Position]
		switch {
		case !okU || !okP:
			issues = append(issues, Issue{Code: "UNKNOWN", ULDID: a.ULDID, Position: a.Position, Message: "unknown ULD or position"})
		case s.occ[a.Position] != "":
			issues = append(issues, Issue{Code: "POSITION_TAKEN", ULDID: a.ULDID, Position: a.Position, Message: "position already occupied"})
		case !slices.Contains(s.pos[i].ULDTypes, u.Type):
			issues = append(issues, Issue{Code: "NO_COMPATIBLE_POSITION", ULDID: a.ULDID, Position: a.Position,
				Message: fmt.Sprintf("%s cannot take a %s", a.Position, u.Type)})
		default:
			if u.WeightKg > s.pos[i].MaxKg {
				issues = append(issues, Issue{Code: "OVER_POSITION_LIMIT", ULDID: a.ULDID, Position: a.Position,
					Message: fmt.Sprintf("%.0f kg exceeds %s limit of %.0f kg", u.WeightKg, a.Position, s.pos[i].MaxKg)})
			}
			s.place(a.ULDID, a.Position)
		}
	}
	return s.plan(issues, nil, 0)
}

// Optimize computes a load plan for ulds.
func Optimize(ac Aircraft, positions []Position, ulds []ULD) Plan {
	s := newState(ac, positions, ulds)
	var issues, unassigned []Issue

	// 1. Locked ULDs stay where they are.
	var movable []ULD
	for _, u := range ulds {
		if u.Locked && u.Current != "" {
			if i, ok := s.byPos[u.Current]; ok && s.occ[u.Current] == "" {
				s.place(u.ID, u.Current)
				if u.WeightKg > s.pos[i].MaxKg {
					issues = append(issues, Issue{Code: "LOCKED_OVER_LIMIT", ULDID: u.ID, Position: u.Current,
						Message: fmt.Sprintf("loaded ULD %.0f kg exceeds %s limit of %.0f kg; offload and rebuild", u.WeightKg, u.Current, s.pos[i].MaxKg)})
				}
				continue
			}
		}
		movable = append(movable, u)
	}

	// 2. Greedy: heaviest first, each to the feasible slot with lowest cost.
	sort.SliceStable(movable, func(i, j int) bool {
		if movable[i].WeightKg != movable[j].WeightKg {
			return movable[i].WeightKg > movable[j].WeightKg
		}
		return movable[i].ID < movable[j].ID
	})
	for _, u := range movable {
		best, bestCost := "", math.Inf(1)
		typeOK, free := false, false
		for _, p := range s.pos {
			if !slices.Contains(p.ULDTypes, u.Type) {
				continue
			}
			typeOK = true
			if s.occ[p.Code] != "" {
				continue
			}
			free = true
			if u.WeightKg > p.MaxKg {
				continue
			}
			s.place(u.ID, p.Code)
			if c := s.cost(); c < bestCost-1e-9 {
				best, bestCost = p.Code, c
			}
			s.remove(u.ID)
		}
		switch {
		case best != "":
			s.place(u.ID, best)
		case !typeOK:
			unassigned = append(unassigned, Issue{Code: "NO_COMPATIBLE_POSITION", ULDID: u.ID,
				Message: fmt.Sprintf("%s has no position that accepts a %s", ac.Type, u.Type)})
		case !free:
			unassigned = append(unassigned, Issue{Code: "NO_FREE_POSITION", ULDID: u.ID,
				Message: "all compatible positions are taken"})
		default:
			unassigned = append(unassigned, Issue{Code: "OVER_POSITION_LIMIT", ULDID: u.ID,
				Message: fmt.Sprintf("%.0f kg exceeds every free compatible position's structural limit; rebuild the ULD", u.WeightKg)})
		}
	}

	// 3. Local search: best improving move or swap per pass.
	movableIDs := map[string]bool{}
	for _, u := range movable {
		movableIDs[u.ID] = true
	}
	iter := 0
	for ; iter < maxIterations; iter++ {
		cur := s.cost()
		bestCost := cur
		var apply func()
		for _, u := range movable {
			from, ok := s.at[u.ID]
			if !ok {
				continue
			}
			for _, p := range s.pos {
				if p.Code == from || !Fits(p, u) {
					continue
				}
				other := s.occ[p.Code]
				if other == "" { // move into a free slot
					s.remove(u.ID)
					s.place(u.ID, p.Code)
					if c := s.cost(); c < bestCost-1e-9 {
						bestCost = c
						uid, to := u.ID, p.Code
						apply = func() { s.remove(uid); s.place(uid, to) }
					}
					s.remove(u.ID)
					s.place(u.ID, from)
					continue
				}
				v := s.ulds[other]
				if !movableIDs[other] || !Fits(s.pos[s.byPos[from]], v) {
					continue
				}
				s.remove(u.ID)
				s.remove(other)
				s.place(u.ID, p.Code)
				s.place(other, from)
				if c := s.cost(); c < bestCost-1e-9 {
					bestCost = c
					a, b, pa, pb := u.ID, other, p.Code, from
					apply = func() { s.remove(a); s.remove(b); s.place(a, pa); s.place(b, pb) }
				}
				s.remove(u.ID)
				s.remove(other)
				s.place(u.ID, from)
				s.place(other, p.Code)
			}
		}
		if apply == nil {
			break
		}
		apply()
	}
	return s.plan(issues, unassigned, iter)
}

func (s *state) plan(issues, unassigned []Issue, iter int) Plan {
	p := Plan{Balance: s.balance(), Cost: s.cost(), Iterations: iter, Issues: issues, Unassigned: unassigned}
	for _, pos := range s.pos {
		if uld := s.occ[pos.Code]; uld != "" {
			p.Assignments = append(p.Assignments, Assignment{ULDID: uld, Position: pos.Code})
		}
	}
	if s.ac.MaxPayloadKg > 0 && p.Balance.CargoKg > s.ac.MaxPayloadKg {
		p.Issues = append(p.Issues, Issue{Code: "PAYLOAD_EXCEEDED",
			Message: fmt.Sprintf("planned cargo %.0f kg exceeds max payload %.0f kg", p.Balance.CargoKg, s.ac.MaxPayloadKg)})
	}
	if len(p.Assignments) > 0 && !p.Balance.WithinEnvelope {
		p.Issues = append(p.Issues, Issue{Code: "CG_OUT_OF_ENVELOPE",
			Message: fmt.Sprintf("CG %.1f%% MAC outside %.1f–%.1f%%", p.Balance.CGPctMAC, s.ac.CGFwdPct, s.ac.CGAftPct)})
	}
	if p.Assignments == nil {
		p.Assignments = []Assignment{}
	}
	if p.Unassigned == nil {
		p.Unassigned = []Issue{}
	}
	if p.Issues == nil {
		p.Issues = []Issue{}
	}
	return p
}

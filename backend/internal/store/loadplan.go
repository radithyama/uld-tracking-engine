package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/radithya/uld-engine/internal/loadopt"
)

var ErrNotFound = errors.New("not found")

// FlightPlanContext is everything needed to view or optimize one flight's load.
type FlightPlanContext struct {
	FlightID     int64              `json:"flight_id"`
	FlightNumber string             `json:"flight_number"`
	Status       string             `json:"status"`
	Origin       string             `json:"origin"`
	Destination  string             `json:"destination"`
	Departure    time.Time          `json:"departure"`
	Aircraft     loadopt.Aircraft   `json:"aircraft"`
	Positions    []loadopt.Position `json:"positions"`
	// ULDs booked on the flight. Only STAGED and LOADED ULDs are plannable;
	// the rest are listed so the UI can show what is still being built up.
	ULDs    []loadopt.ULD        `json:"ulds"`
	Current []loadopt.Assignment `json:"current"`
}

// Plannable reports whether the flight's load can still be changed.
func (c FlightPlanContext) Plannable() bool { return c.Status == "SCHEDULED" }

// ReadyULDs returns the ULDs the optimizer may place.
func (c FlightPlanContext) ReadyULDs() []loadopt.ULD {
	var out []loadopt.ULD
	for _, u := range c.ULDs {
		if u.Status == "STAGED" || u.Status == "LOADED" || u.Status == "IN_FLIGHT" {
			out = append(out, u)
		}
	}
	return out
}

func (p *Postgres) FlightPlanContext(ctx context.Context, flightID int64) (FlightPlanContext, error) {
	var c FlightPlanContext
	var aircraftID int64
	err := p.pool.QueryRow(ctx, `
		SELECT f.id, f.flight_number, f.status, f.origin, f.destination, f.scheduled_departure, a.id,
		       a.registration, a.aircraft_type, COALESCE(a.oew_kg, 0)::float8, COALESCE(a.oew_arm_m, 0)::float8,
		       COALESCE(a.lemac_m, 0)::float8, COALESCE(a.mac_m, 0)::float8, COALESCE(a.cg_fwd_pct, 0)::float8,
		       COALESCE(a.cg_aft_pct, 0)::float8, COALESCE(a.cg_target_pct, 0)::float8, a.max_payload_kg::float8
		FROM flights f JOIN aircrafts a ON a.id = f.aircraft_id WHERE f.id = $1`, flightID,
	).Scan(&c.FlightID, &c.FlightNumber, &c.Status, &c.Origin, &c.Destination, &c.Departure, &aircraftID,
		&c.Aircraft.Registration, &c.Aircraft.Type, &c.Aircraft.OEWKg, &c.Aircraft.OEWArmM, &c.Aircraft.LEMACM,
		&c.Aircraft.MACM, &c.Aircraft.CGFwdPct, &c.Aircraft.CGAftPct, &c.Aircraft.CGTargetPct, &c.Aircraft.MaxPayloadKg)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrNotFound
	}
	if err != nil {
		return c, err
	}

	rows, err := p.pool.Query(ctx, `
		SELECT code, deck, hold, arm_m::float8, side, max_weight_kg::float8, uld_types, seq
		FROM aircraft_positions WHERE aircraft_id = $1 ORDER BY seq`, aircraftID)
	if err != nil {
		return c, err
	}
	c.Positions, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (loadopt.Position, error) {
		var ps loadopt.Position
		err := r.Scan(&ps.Code, &ps.Deck, &ps.Hold, &ps.ArmM, &ps.Lateral, &ps.MaxKg, &ps.ULDTypes, &ps.Seq)
		return ps, err
	})
	if err != nil {
		return c, err
	}

	rows, err = p.pool.Query(ctx, `
		SELECT u.id, u.uld_type, u.current_weight_kg::float8, u.status::text, COALESCE(lp.position_code, '')
		FROM ulds u LEFT JOIN load_plans lp ON lp.flight_id = u.flight_id AND lp.uld_id = u.id
		WHERE u.flight_id = $1 ORDER BY u.id`, flightID)
	if err != nil {
		return c, err
	}
	c.ULDs, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (loadopt.ULD, error) {
		var u loadopt.ULD
		err := r.Scan(&u.ID, &u.Type, &u.WeightKg, &u.Status, &u.Current)
		// A ULD that is physically loaded (or airborne) at a planned position
		// can't be moved by re-planning.
		u.Locked = u.Current != "" && (u.Status == "LOADED" || u.Status == "IN_FLIGHT")
		return u, err
	})
	if err != nil {
		return c, err
	}
	c.Current = []loadopt.Assignment{}
	for _, u := range c.ULDs {
		if u.Current != "" {
			c.Current = append(c.Current, loadopt.Assignment{ULDID: u.ID, Position: u.Current})
		}
	}
	return c, nil
}

// SaveLoadPlan replaces the flight's plan with assignments.
func (p *Postgres) SaveLoadPlan(ctx context.Context, flightID int64, assignments []loadopt.Assignment, actor string) error {
	return pgx.BeginFunc(ctx, p.pool, func(tx pgx.Tx) error {
		// Lock the flight row so concurrent plan edits serialise.
		var status string
		if err := tx.QueryRow(ctx, `SELECT status FROM flights WHERE id = $1 FOR UPDATE`, flightID).Scan(&status); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if status != "SCHEDULED" {
			return ErrNotPlannable
		}
		if _, err := tx.Exec(ctx, `DELETE FROM load_plans WHERE flight_id = $1`, flightID); err != nil {
			return err
		}
		for _, a := range assignments {
			if _, err := tx.Exec(ctx, `INSERT INTO load_plans (flight_id, position_code, uld_id, planned_by)
				VALUES ($1, $2, $3, $4)`, flightID, a.Position, a.ULDID, actor); err != nil {
				return err
			}
		}
		return nil
	})
}

var ErrNotPlannable = errors.New("flight has departed; its load plan is frozen")

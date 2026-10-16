package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// NetworkChanges lists what one network-clock tick touched, so the caller
// can re-project exactly those rows into Redis.
type NetworkChanges struct {
	ULDs       []string // state changed (departed, arrived, rebooked)
	Flights    []int64  // created or changed
	DeletedULD []string // sandbox ULDs purged
	Departed   []string // flight numbers, for logging
	Arrived    []string
	Rebooked   int
}

func (c NetworkChanges) Empty() bool {
	return len(c.ULDs)+len(c.Flights)+len(c.DeletedULD) == 0
}

// Scaled converts real-world minutes into demo wall-clock time.
func Scaled(minutes int, scale float64) time.Duration {
	if scale <= 0 {
		scale = 1
	}
	return time.Duration(float64(minutes) * float64(time.Minute) / scale)
}

type route struct {
	flightNumber, origin, destination, returnFlight string
	aircraftID                                      int64
	block, turnaround                               int
}

const networkLockKey = 7140002

// NetworkTick advances the flight network to now. It returns leader=false
// without doing anything if another processor holds the network lock.
//
// Rotation model: each aircraft flies an outbound/return pair. When a flight
// of route R departs, R's next departure is scheduled one full cycle later
// (blockR + turnR + blockP + turnP) and the paired route P is scheduled to
// leave R's destination one turnaround after R lands. Every flight number
// therefore always has a SCHEDULED instance that scanners can book onto.
func (p *Postgres) NetworkTick(ctx context.Context, now time.Time, scale float64, sandboxTTL time.Duration) (NetworkChanges, bool, error) {
	var ch NetworkChanges
	leader := false
	err := pgx.BeginFunc(ctx, p.pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock($1)`, networkLockKey).Scan(&leader); err != nil || !leader {
			return err
		}

		routes := map[string]route{}
		rows, err := tx.Query(ctx, `SELECT flight_number, origin, destination, return_flight, aircraft_id,
			block_minutes, turnaround_minutes FROM routes WHERE active ORDER BY (origin <> 'CGK'), flight_number`)
		if err != nil {
			return err
		}
		var order []string
		for rows.Next() {
			var r route
			if err := rows.Scan(&r.flightNumber, &r.origin, &r.destination, &r.returnFlight, &r.aircraftID,
				&r.block, &r.turnaround); err != nil {
				return err
			}
			routes[r.flightNumber] = r
			order = append(order, r.flightNumber)
		}
		if err := rows.Err(); err != nil {
			return err
		}

		// 1. Departures.
		type departed struct {
			id    int64
			route string
			dep   time.Time
		}
		var deps []departed
		rows, err = tx.Query(ctx, `
			UPDATE flights f SET status = 'DEPARTED', departed_at = $1,
			       block_minutes = COALESCE(f.block_minutes, r.block_minutes),
			       estimated_arrival = $1 + make_interval(secs => COALESCE(f.block_minutes, r.block_minutes) * 60.0 / $2)
			FROM routes r
			WHERE r.flight_number = f.flight_number AND f.status = 'SCHEDULED' AND f.scheduled_departure <= $1
			RETURNING f.id, f.flight_number, f.scheduled_departure`, now, scale)
		if err != nil {
			return err
		}
		for rows.Next() {
			var d departed
			if err := rows.Scan(&d.id, &d.route, &d.dep); err != nil {
				return err
			}
			deps = append(deps, d)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		leftovers := map[string][]string{} // route -> ULDs that missed the flight
		for _, d := range deps {
			ch.Flights = append(ch.Flights, d.id)
			ch.Departed = append(ch.Departed, d.route)
			ids, err := collectStrings(tx.Query(ctx, `
				UPDATE ulds SET status = 'IN_FLIGHT', location_id = NULL, version = version + 1, updated_at = now()
				WHERE flight_id = $1 AND status = 'LOADED' RETURNING id`, d.id))
			if err != nil {
				return err
			}
			ch.ULDs = append(ch.ULDs, ids...)
			missed, err := collectStrings(tx.Query(ctx, `
				SELECT id FROM ulds WHERE flight_id = $1 AND status <> 'IN_FLIGHT' FOR UPDATE`, d.id))
			if err != nil {
				return err
			}
			leftovers[d.route] = append(leftovers[d.route], missed...)
		}

		// 2. Arrivals: ULDs are delivered to the destination's receiving area.
		rows, err = tx.Query(ctx, `
			UPDATE flights SET status = 'ARRIVED', arrived_at = $1
			WHERE status = 'DEPARTED' AND estimated_arrival <= $1
			RETURNING id, flight_number, destination`, now)
		if err != nil {
			return err
		}
		type arrived struct {
			id   int64
			fn   string
			dest string
		}
		var arrs []arrived
		for rows.Next() {
			var a arrived
			if err := rows.Scan(&a.id, &a.fn, &a.dest); err != nil {
				return err
			}
			arrs = append(arrs, a)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		for _, a := range arrs {
			ch.Flights = append(ch.Flights, a.id)
			ch.Arrived = append(ch.Arrived, a.fn)
			ids, err := collectStrings(tx.Query(ctx, `
				UPDATE ulds SET status = 'IN_TRANSIT', flight_id = NULL, version = version + 1, updated_at = now(),
				       location_id = (SELECT id FROM warehouse_locations
				                      WHERE station = $2 AND zone = 'RCV' ORDER BY grid_y, grid_x LIMIT 1)
				WHERE flight_id = $1 AND status = 'IN_FLIGHT' RETURNING id`, a.id, a.dest))
			if err != nil {
				return err
			}
			ch.ULDs = append(ch.ULDs, ids...)
		}

		// 3. Keep one SCHEDULED instance per route.
		for _, fn := range order {
			r := routes[fn]
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM flights WHERE flight_number = $1 AND status = 'SCHEDULED')`,
				fn).Scan(&exists); err != nil {
				return err
			}
			if exists {
				continue
			}
			dep, err := nextDeparture(ctx, tx, r, routes, now, scale)
			if err != nil {
				return err
			}
			var id int64
			err = tx.QueryRow(ctx, `
				INSERT INTO flights (flight_number, aircraft_id, origin, destination, scheduled_departure, block_minutes, status)
				VALUES ($1, $2, $3, $4, $5, $6, 'SCHEDULED')
				ON CONFLICT (flight_number, scheduled_departure) DO NOTHING
				RETURNING id`, fn, r.aircraftID, r.origin, r.destination, dep, r.block).Scan(&id)
			switch {
			case errors.Is(err, pgx.ErrNoRows):
				continue // same slot already used by a past instance; retried next tick
			case err != nil:
				return err
			}
			ch.Flights = append(ch.Flights, id)
		}

		// 4. Rebook ULDs that missed their departure onto the next rotation.
		for fn, ids := range leftovers {
			if len(ids) == 0 {
				continue
			}
			var next int64
			if err := tx.QueryRow(ctx, `SELECT id FROM flights WHERE flight_number = $1 AND status = 'SCHEDULED'
				ORDER BY scheduled_departure LIMIT 1`, fn).Scan(&next); err != nil {
				return fmt.Errorf("rebook %s: %w", fn, err)
			}
			if _, err := tx.Exec(ctx, `DELETE FROM load_plans WHERE uld_id = ANY($1)`, ids); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE ulds SET flight_id = $1, version = version + 1, updated_at = now()
				WHERE id = ANY($2)`, next, ids); err != nil {
				return err
			}
			ch.ULDs = append(ch.ULDs, ids...)
			ch.Flights = append(ch.Flights, next)
			ch.Rebooked += len(ids)
		}

		// 5. Purge sandbox ULDs (ids ending in "SB") after their TTL.
		if sandboxTTL > 0 {
			ids, err := collectStrings(tx.Query(ctx, `
				DELETE FROM ulds WHERE id LIKE '%SB' AND COALESCE(last_scan_at, created_at) < $1 RETURNING id`,
				now.Add(-sandboxTTL)))
			if err != nil {
				return err
			}
			ch.DeletedULD = ids
		}
		return nil
	})
	return ch, leader, err
}

// nextDeparture computes when route r should next leave.
func nextDeparture(ctx context.Context, tx pgx.Tx, r route, routes map[string]route, now time.Time, scale float64) (time.Time, error) {
	pair, hasPair := routes[r.returnFlight]
	// Most recent departure of this route -> one full aircraft cycle later.
	var lastDep *time.Time
	if err := tx.QueryRow(ctx, `SELECT max(departed_at) FROM flights WHERE flight_number = $1 AND status IN ('DEPARTED', 'ARRIVED')`,
		r.flightNumber).Scan(&lastDep); err != nil {
		return time.Time{}, err
	}
	if lastDep != nil && hasPair {
		cycle := Scaled(r.block+r.turnaround+pair.block+pair.turnaround, scale)
		if dep := lastDep.Add(cycle); dep.After(now) {
			return dep, nil
		}
	}
	// Otherwise leave one turnaround after the paired flight lands here.
	if hasPair {
		var pairDep, pairETA *time.Time
		err := tx.QueryRow(ctx, `SELECT scheduled_departure, estimated_arrival FROM flights
			WHERE flight_number = $1 AND status IN ('SCHEDULED', 'DEPARTED')
			ORDER BY (status = 'DEPARTED') DESC, scheduled_departure LIMIT 1`, r.returnFlight).Scan(&pairDep, &pairETA)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return time.Time{}, err
		}
		if pairDep != nil {
			land := pairDep.Add(Scaled(pair.block, scale))
			if pairETA != nil {
				land = *pairETA
			}
			if dep := land.Add(Scaled(pair.turnaround, scale)); dep.After(now) {
				return dep, nil
			}
		}
	}
	// Bootstrap: a hub departure a few minutes from now.
	return now.Add(Scaled(r.turnaround, scale)).Truncate(time.Second), nil
}

func collectStrings(rows pgx.Rows, err error) ([]string, error) {
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

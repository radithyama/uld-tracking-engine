// Package store holds the PostgreSQL (system of record) and Redis (live
// projection) adapters.
package store

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/radithya/uld-engine/internal/detect"
	"github.com/radithya/uld-engine/internal/model"
)

// ErrPermanent marks failures that will never succeed on retry (bad reference
// data, constraint violations). The processor routes these to the DLQ.
var ErrPermanent = errors.New("permanent failure")

func permanent(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrPermanent, fmt.Sprintf(format, args...))
}

type Postgres struct {
	pool *pgxpool.Pool
}

func NewPostgres(ctx context.Context, url string, maxConns int32) (*Postgres, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	if maxConns > 0 {
		cfg.MaxConns = maxConns
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &Postgres{pool: pool}, nil
}

func (p *Postgres) Close() { p.pool.Close() }

// ApplyResult describes the outcome of applying a scan.
type ApplyResult struct {
	// Applied is false for duplicates and out-of-order (stale) events.
	Applied   bool
	Duplicate bool
	Stale     bool
	// FlightIDs whose aggregate load may have changed (old and new assignment).
	FlightIDs []int64
	// Security detections that fired. Empty for duplicates, so Kafka
	// redelivery never raises the same SIEM alert twice.
	Findings []detect.Finding
}

// Default ratings for ULDs first seen via a scan (auto-registration).
var uldTypeDefaults = map[string]struct{ tare, maxGross float64 }{
	"AKE": {82, 1588},
	"AKH": {75, 1134},
	"PMC": {110, 6804},
	"PAG": {105, 6033},
	"PLA": {95, 3175},
}

// ApplyScan records the event and updates the ULD's current state inside one
// transaction. It is idempotent on event_id and ignores events older than the
// ULD's last applied scan, so Kafka redelivery and producer retries are safe.
// Security detections are evaluated against the locked prior state and
// persisted on the event row in the same transaction.
func (p *Postgres) ApplyScan(ctx context.Context, ev *model.ScanEvent, partition int, offset int64, cfg detect.Config) (ApplyResult, error) {
	var res ApplyResult

	err := pgx.BeginFunc(ctx, p.pool, func(tx pgx.Tx) error {
		// 1. Reference data lookups. An unknown location can never succeed.
		cur := detect.Fix{LocationID: ev.LocationID, At: ev.Timestamp}
		var impliedStatus model.ULDStatus
		err := tx.QueryRow(ctx, `
			SELECT l.implied_status, l.station, a.lat, a.lon
			FROM warehouse_locations l JOIN airports a ON a.iata = l.station
			WHERE l.id = $1`, ev.LocationID,
		).Scan(&impliedStatus, &cur.Station, &cur.Lat, &cur.Lon)
		if errors.Is(err, pgx.ErrNoRows) {
			return permanent("unknown location_id %q", ev.LocationID)
		}
		if err != nil {
			return err
		}

		var newFlightID *int64
		if ev.FlightNumber != "" {
			// Pick the departure closest to the scan time for this flight number.
			var id int64
			err := tx.QueryRow(ctx, `
				SELECT id FROM flights
				WHERE flight_number = $1
				ORDER BY abs(extract(epoch FROM scheduled_departure - $2::timestamptz))
				LIMIT 1`, ev.FlightNumber, ev.Timestamp,
			).Scan(&id)
			switch {
			case errors.Is(err, pgx.ErrNoRows):
				return permanent("unknown flight_number %q", ev.FlightNumber)
			case err != nil:
				return err
			}
			newFlightID = &id
		}

		// 2. Auto-register ULDs we have never seen, then lock the row so
		// concurrent writers for the same ULD serialise.
		typ := ev.ULDID[:3]
		def, ok := uldTypeDefaults[typ]
		if !ok {
			def = uldTypeDefaults["AKE"]
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO ulds (id, uld_type, tare_weight_kg, max_gross_weight_kg)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (id) DO NOTHING`, ev.ULDID, typ, def.tare, def.maxGross); err != nil {
			return err
		}

		var (
			oldFlightID          *int64
			lastScanAt           *time.Time
			prevWeight, tare, mg float64
			prevStatus           string
			prevLoc, prevStation *string
			prevLat, prevLon     *float64
		)
		if err := tx.QueryRow(ctx, `
			SELECT u.flight_id, u.last_scan_at, u.current_weight_kg::float8, u.status::text,
			       u.tare_weight_kg::float8, u.max_gross_weight_kg::float8,
			       u.location_id, l.station, a.lat, a.lon
			FROM ulds u
			LEFT JOIN warehouse_locations l ON l.id = u.location_id
			LEFT JOIN airports a ON a.iata = l.station
			WHERE u.id = $1
			FOR UPDATE OF u`, ev.ULDID,
		).Scan(&oldFlightID, &lastScanAt, &prevWeight, &prevStatus, &tare, &mg,
			&prevLoc, &prevStation, &prevLat, &prevLon); err != nil {
			return err
		}

		// 3. Effective weight limit for the flight the ULD will be on.
		in := detect.Input{
			Event: ev, Cur: cur, PrevWeight: prevWeight, PrevStatus: model.ULDStatus(prevStatus),
			TareKg: tare, LimitKg: mg,
		}
		if fid := cmp.Or(newFlightID, oldFlightID); fid != nil {
			var acLimit float64
			if err := tx.QueryRow(ctx, `
				SELECT f.flight_number, a.registration, a.max_uld_weight_kg::float8
				FROM flights f JOIN aircrafts a ON a.id = f.aircraft_id WHERE f.id = $1`, *fid,
			).Scan(&in.Flight, &in.Aircraft, &acLimit); err != nil {
				return err
			}
			in.LimitKg = min(mg, acLimit)
		}
		// Seeded positions have no scan time; only real sightings count for travel.
		if lastScanAt != nil && prevStation != nil && prevLat != nil && prevLon != nil {
			in.Prev = &detect.Fix{Station: *prevStation, LocationID: *prevLoc, Lat: *prevLat, Lon: *prevLon, At: *lastScanAt}
		}

		// 4. Out-of-order guard: keep the event in the log but don't let it
		// rewind the current state.
		in.Stale = lastScanAt != nil && ev.Timestamp.Before(*lastScanAt)
		in.NewStatus = cmp.Or(ev.Status, impliedStatus)
		findings := detect.Evaluate(cfg, in)
		flags := make([]string, len(findings))
		for i, f := range findings {
			flags[i] = f.Flag()
		}

		// 5. Idempotency: the event log's primary key is the event id.
		tag, err := tx.Exec(ctx, `
			INSERT INTO uld_scan_events
			  (event_id, uld_id, scanner_id, scan_type, weight_kg, location_id,
			   flight_number, status, scanned_at, kafka_partition, kafka_offset, security_flags)
			VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''), NULLIF($8, '')::uld_status, $9, $10, $11, $12)
			ON CONFLICT (event_id) DO NOTHING`,
			ev.EventID, ev.ULDID, ev.ScannerID, string(ev.ScanType), ev.WeightKg, ev.LocationID,
			ev.FlightNumber, string(ev.Status), ev.Timestamp, partition, offset, flags)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			res.Duplicate = true
			return nil
		}
		res.Findings = findings

		if in.Stale {
			res.Stale = true
			return nil
		}

		if _, err := tx.Exec(ctx, `
			UPDATE ulds SET
			    current_weight_kg = $2,
			    location_id       = $3,
			    flight_id         = COALESCE($4, flight_id),
			    status            = $5,
			    last_scanner_id   = $6,
			    last_scan_at      = $7,
			    version           = version + 1,
			    updated_at        = now()
			WHERE id = $1`,
			ev.ULDID, ev.WeightKg, ev.LocationID, newFlightID, string(in.NewStatus), ev.ScannerID, ev.Timestamp,
		); err != nil {
			return err
		}

		res.Applied = true
		for _, id := range []*int64{oldFlightID, newFlightID} {
			if id != nil && !slices.Contains(res.FlightIDs, *id) {
				res.FlightIDs = append(res.FlightIDs, *id)
			}
		}
		return nil
	})
	if err != nil {
		return ApplyResult{}, classify(err)
	}
	return res, nil
}

// classify converts Postgres data/constraint errors into ErrPermanent.
func classify(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		// Class 22 = data exception, class 23 = integrity constraint violation.
		if strings.HasPrefix(pgErr.Code, "22") || strings.HasPrefix(pgErr.Code, "23") {
			return fmt.Errorf("%w: %s", ErrPermanent, pgErr.Message)
		}
	}
	return err
}

const uldStateSQL = `
SELECT u.id, u.uld_type,
       u.current_weight_kg::float8,
       u.max_gross_weight_kg::float8,
       LEAST(u.max_gross_weight_kg, COALESCE(a.max_uld_weight_kg, u.max_gross_weight_kg))::float8,
       COALESCE(u.location_id, ''), COALESCE(l.zone, ''), u.flight_id, COALESCE(f.flight_number, ''),
       u.status::text, COALESCE(u.last_scanner_id, ''), u.last_scan_at, u.version, u.updated_at
FROM ulds u
LEFT JOIN warehouse_locations l ON l.id = u.location_id
LEFT JOIN flights   f ON f.id = u.flight_id
LEFT JOIN aircrafts a ON a.id = f.aircraft_id`

func scanULDState(row pgx.Row) (model.ULDState, error) {
	var s model.ULDState
	var status string
	err := row.Scan(&s.ID, &s.ULDType, &s.WeightKg, &s.MaxGrossWeightKg, &s.WeightLimitKg,
		&s.LocationID, &s.Zone, &s.FlightID, &s.FlightNumber, &status, &s.LastScannerID, &s.LastScanAt,
		&s.Version, &s.UpdatedAt)
	s.Status = model.ULDStatus(status)
	s.Overweight = s.WeightKg > s.WeightLimitKg
	return s, err
}

func (p *Postgres) ULDState(ctx context.Context, id string) (model.ULDState, error) {
	return scanULDState(p.pool.QueryRow(ctx, uldStateSQL+` WHERE u.id = $1`, id))
}

func (p *Postgres) AllULDStates(ctx context.Context) ([]model.ULDState, error) {
	rows, err := p.pool.Query(ctx, uldStateSQL+` ORDER BY u.id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (model.ULDState, error) { return scanULDState(r) })
}

const flightLoadSQL = `
SELECT f.id, f.flight_number, a.registration, a.aircraft_type, f.origin, f.destination,
       f.scheduled_departure, a.max_payload_kg::float8, a.max_uld_weight_kg::float8, a.uld_positions,
       COALESCE(SUM(u.current_weight_kg), 0)::float8,
       COUNT(u.id)::int,
       COUNT(u.id) FILTER (WHERE u.current_weight_kg > LEAST(u.max_gross_weight_kg, a.max_uld_weight_kg))::int,
       (extract(epoch FROM clock_timestamp()) * 1000000)::bigint
FROM flights f
JOIN aircrafts a ON a.id = f.aircraft_id
LEFT JOIN ulds u ON u.flight_id = f.id
WHERE $1 OR f.id = ANY($2)
GROUP BY f.id, a.id
ORDER BY f.scheduled_departure`

// FlightLoads returns aggregates for the given flights, or all flights if ids is empty.
func (p *Postgres) FlightLoads(ctx context.Context, ids []int64) ([]model.FlightLoad, error) {
	if ids == nil {
		ids = []int64{}
	}
	rows, err := p.pool.Query(ctx, flightLoadSQL, len(ids) == 0, ids)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (model.FlightLoad, error) {
		var f model.FlightLoad
		err := r.Scan(&f.FlightID, &f.FlightNumber, &f.Aircraft, &f.AircraftType, &f.Origin, &f.Destination,
			&f.Departure, &f.MaxPayloadKg, &f.MaxULDWeightKg, &f.ULDPositions,
			&f.TotalWeightKg, &f.ULDCount, &f.OverweightULDs, &f.Version)
		f.OverPayload = f.TotalWeightKg > f.MaxPayloadKg
		return f, err
	})
}

// Locations returns the scan points of one station (the dashboard's floor plan).
func (p *Postgres) Locations(ctx context.Context, station string) ([]model.Location, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT id, zone, label, grid_x, grid_y, implied_status::text
		FROM warehouse_locations WHERE station = $1 ORDER BY grid_y, grid_x`, station)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (model.Location, error) {
		var l model.Location
		var st string
		err := r.Scan(&l.ID, &l.Zone, &l.Label, &l.GridX, &l.GridY, &st)
		l.ImpliedStatus = model.ULDStatus(st)
		return l, err
	})
}

// Package model defines the wire and domain types shared by all services.
package model

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

type ScanType string

const (
	ScanRFID    ScanType = "RFID"
	ScanBarcode ScanType = "BARCODE"
)

type ULDStatus string

const (
	StatusInTransit ULDStatus = "IN_TRANSIT"
	StatusStaged    ULDStatus = "STAGED"
	StatusLoaded    ULDStatus = "LOADED"
	// Set by the network clock when the ULD's flight departs; never sent by
	// scanners.
	StatusInFlight ULDStatus = "IN_FLIGHT"
)

// Valid reports whether a scanner may report this status.
func (s ULDStatus) Valid() bool {
	switch s {
	case StatusInTransit, StatusStaged, StatusLoaded:
		return true
	}
	return false
}

// ScanEvent is a single telemetry reading from a warehouse scanner. It is the
// payload of every message on the telemetry topic.
type ScanEvent struct {
	EventID      string    `json:"event_id"`
	ULDID        string    `json:"uld_id"`
	ScannerID    string    `json:"scanner_id"`
	ScanType     ScanType  `json:"scan_type"`
	WeightKg     float64   `json:"weight_kg"`
	LocationID   string    `json:"location_id"`
	FlightNumber string    `json:"flight_number,omitempty"`
	Status       ULDStatus `json:"status,omitempty"` // optional; otherwise implied by location
	Timestamp    time.Time `json:"timestamp"`
}

// Server-side bounds on every field, enforced before anything reaches Kafka.
// Free-text fields end up in Postgres, logs and the dashboard, so they are
// restricted to short, plain identifiers.
var (
	// IATA ULD identifier: 3-letter type, 4-5 digit serial, 2-char owner code.
	uldIDPattern = regexp.MustCompile(`^[A-Z]{3}[0-9]{4,5}[A-Z0-9]{2}$`)
	// Warehouse positions: STA-06, SYD-RCV-01.
	locationPattern = regexp.MustCompile(`^[A-Z0-9]{2,4}(-[A-Z0-9]{1,4}){1,3}$`)
	// IATA flight designator: 2-char airline code, 1-4 digits, optional suffix.
	flightPattern = regexp.MustCompile(`^[A-Z0-9]{2}[0-9]{1,4}[A-Z]?$`)
	// Device ids: SCN-RCV-01, WEB-LOADMASTER.
	scannerPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$`)
)

const (
	maxWeightKg  = 20000 // well above any real ULD; guards against sensor garbage
	maxClockSkew = 5 * time.Minute
	// MaxEventAge bounds how far in the past a scan may claim to be. Together
	// with ingest's event-id replay window (same length) it leaves no gap in
	// which an old, captured event could be replayed unnoticed.
	MaxEventAge = 24 * time.Hour
)

// Normalize canonicalises casing and fills in server-side defaults.
func (e *ScanEvent) Normalize(now time.Time) {
	e.ULDID = strings.ToUpper(strings.TrimSpace(e.ULDID))
	e.ScannerID = strings.TrimSpace(e.ScannerID)
	e.ScanType = ScanType(strings.ToUpper(strings.TrimSpace(string(e.ScanType))))
	e.LocationID = strings.ToUpper(strings.TrimSpace(e.LocationID))
	e.FlightNumber = strings.ToUpper(strings.TrimSpace(e.FlightNumber))
	e.Status = ULDStatus(strings.ToUpper(strings.TrimSpace(string(e.Status))))
	if e.EventID == "" {
		e.EventID = uuid.NewString()
	}
	if e.Timestamp.IsZero() {
		e.Timestamp = now
	}
	e.Timestamp = e.Timestamp.UTC()
}

// Validate reports every problem with the event, not just the first.
func (e *ScanEvent) Validate(now time.Time) error {
	var errs []error
	if _, err := uuid.Parse(e.EventID); err != nil {
		errs = append(errs, errors.New("event_id must be a UUID"))
	}
	if !uldIDPattern.MatchString(e.ULDID) {
		errs = append(errs, fmt.Errorf("uld_id %q is not a valid IATA ULD id", Truncate(e.ULDID, 16)))
	}
	if !scannerPattern.MatchString(e.ScannerID) {
		errs = append(errs, errors.New("scanner_id must be 1-64 characters of A-Z, 0-9, '.', '_', ':' or '-'"))
	}
	if e.ScanType != ScanRFID && e.ScanType != ScanBarcode {
		errs = append(errs, errors.New("scan_type must be RFID or BARCODE"))
	}
	if e.WeightKg < 0 || e.WeightKg > maxWeightKg {
		errs = append(errs, fmt.Errorf("weight_kg must be between 0 and %d", maxWeightKg))
	}
	if !locationPattern.MatchString(e.LocationID) {
		errs = append(errs, fmt.Errorf("location_id %q is not a valid location id", Truncate(e.LocationID, 32)))
	}
	if e.FlightNumber != "" && !flightPattern.MatchString(e.FlightNumber) {
		errs = append(errs, fmt.Errorf("flight_number %q is not an IATA flight designator", Truncate(e.FlightNumber, 16)))
	}
	if e.Status != "" && !e.Status.Valid() {
		errs = append(errs, errors.New("status must be IN_TRANSIT, STAGED or LOADED"))
	}
	if e.Timestamp.After(now.Add(maxClockSkew)) {
		errs = append(errs, errors.New("timestamp is in the future"))
	}
	if e.Timestamp.Before(now.Add(-MaxEventAge)) {
		errs = append(errs, fmt.Errorf("timestamp is older than %s", MaxEventAge))
	}
	return errors.Join(errs...)
}

// Truncate caps attacker-controlled values echoed back in error messages.
func Truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// ULDState is the live, denormalised view of one ULD pushed to the dashboard.
type ULDState struct {
	ID               string  `json:"id"`
	ULDType          string  `json:"uld_type"`
	WeightKg         float64 `json:"weight_kg"`
	MaxGrossWeightKg float64 `json:"max_gross_weight_kg"`
	// Effective limit: min(ULD rating, aircraft per-position limit).
	WeightLimitKg float64    `json:"weight_limit_kg"`
	Overweight    bool       `json:"overweight"`
	LocationID    string     `json:"location_id"`
	Zone          string     `json:"zone"`
	Station       string     `json:"station"` // IATA of the current location; empty while in flight
	FlightID      *int64     `json:"flight_id"`
	FlightNumber  string     `json:"flight_number"`
	FlightStatus  string     `json:"flight_status"`
	Origin        string     `json:"origin"`
	Destination   string     `json:"destination"`
	ETA           *time.Time `json:"eta"`
	Status        ULDStatus  `json:"status"`
	LastScannerID string     `json:"last_scanner_id"`
	LastScanAt    *time.Time `json:"last_scan_at"`
	Version       int64      `json:"version"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// FlightLoad aggregates the weight of every ULD assigned to a flight.
type FlightLoad struct {
	FlightID       int64      `json:"id"`
	FlightNumber   string     `json:"flight_number"`
	Status         string     `json:"status"` // SCHEDULED | DEPARTED | ARRIVED | CANCELLED
	Aircraft       string     `json:"aircraft"`
	AircraftType   string     `json:"aircraft_type"`
	Origin         string     `json:"origin"`
	Destination    string     `json:"destination"`
	Departure      time.Time  `json:"departure"`
	DepartedAt     *time.Time `json:"departed_at"`
	ETA            *time.Time `json:"eta"`
	ArrivedAt      *time.Time `json:"arrived_at"`
	MaxPayloadKg   float64    `json:"max_payload_kg"`
	MaxULDWeightKg float64    `json:"max_uld_weight_kg"`
	ULDPositions   int        `json:"uld_positions"`
	TotalWeightKg  float64    `json:"total_weight_kg"`
	ULDCount       int        `json:"uld_count"`
	OverweightULDs int        `json:"overweight_ulds"`
	OverPayload    bool       `json:"over_payload"`
	// Microsecond timestamp of when the aggregate was computed; used as a
	// version so a late, older aggregate can never overwrite a newer one.
	Version int64 `json:"version"`
}

type Location struct {
	ID            string    `json:"id"`
	Station       string    `json:"station"`
	Zone          string    `json:"zone"`
	Label         string    `json:"label"`
	GridX         int       `json:"x"`
	GridY         int       `json:"y"`
	ImpliedStatus ULDStatus `json:"implied_status"`
}

// Station is an airport in the network.
type Station struct {
	IATA    string  `json:"iata"`
	Name    string  `json:"name"`
	Country string  `json:"country"`
	Lat     float64 `json:"lat"`
	Lon     float64 `json:"lon"`
}

// Message types sent over the WebSocket / Redis pub-sub channel.
const (
	MsgSnapshot     = "snapshot"
	MsgULDUpdate    = "uld_update"
	MsgULDRemoved   = "uld_removed" // {id}
	MsgFlightUpdate = "flight_update"
	MsgFlightRemove = "flight_removed"  // {id}
	MsgLoadPlan     = "loadplan_update" // {flight_id}

	// Security stream (/ws/security, auditors only).
	MsgSecurityEvent       = "security_event"
	MsgSecurityAlertUpdate = "security_alert_update"
)

type Envelope struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

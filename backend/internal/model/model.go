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
)

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

// IATA ULD identifier: 3-letter type, 4-5 digit serial, 2-char owner code.
var uldIDPattern = regexp.MustCompile(`^[A-Z]{3}[0-9]{4,5}[A-Z0-9]{2}$`)

const (
	maxWeightKg  = 20000 // well above any real ULD; guards against sensor garbage
	maxClockSkew = 5 * time.Minute
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
		errs = append(errs, fmt.Errorf("uld_id %q is not a valid IATA ULD id", e.ULDID))
	}
	if e.ScannerID == "" {
		errs = append(errs, errors.New("scanner_id is required"))
	}
	if e.ScanType != ScanRFID && e.ScanType != ScanBarcode {
		errs = append(errs, errors.New("scan_type must be RFID or BARCODE"))
	}
	if e.WeightKg < 0 || e.WeightKg > maxWeightKg {
		errs = append(errs, fmt.Errorf("weight_kg must be between 0 and %d", maxWeightKg))
	}
	if e.LocationID == "" {
		errs = append(errs, errors.New("location_id is required"))
	}
	if e.Status != "" && !e.Status.Valid() {
		errs = append(errs, errors.New("status must be IN_TRANSIT, STAGED or LOADED"))
	}
	if e.Timestamp.After(now.Add(maxClockSkew)) {
		errs = append(errs, errors.New("timestamp is in the future"))
	}
	return errors.Join(errs...)
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
	FlightID      *int64     `json:"-"`
	FlightNumber  string     `json:"flight_number"`
	Status        ULDStatus  `json:"status"`
	LastScannerID string     `json:"last_scanner_id"`
	LastScanAt    *time.Time `json:"last_scan_at"`
	Version       int64      `json:"version"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// FlightLoad aggregates the weight of every ULD assigned to a flight.
type FlightLoad struct {
	FlightID       int64     `json:"-"`
	FlightNumber   string    `json:"flight_number"`
	Aircraft       string    `json:"aircraft"`
	AircraftType   string    `json:"aircraft_type"`
	Origin         string    `json:"origin"`
	Destination    string    `json:"destination"`
	Departure      time.Time `json:"departure"`
	MaxPayloadKg   float64   `json:"max_payload_kg"`
	MaxULDWeightKg float64   `json:"max_uld_weight_kg"`
	ULDPositions   int       `json:"uld_positions"`
	TotalWeightKg  float64   `json:"total_weight_kg"`
	ULDCount       int       `json:"uld_count"`
	OverweightULDs int       `json:"overweight_ulds"`
	OverPayload    bool      `json:"over_payload"`
	// Microsecond timestamp of when the aggregate was computed; used as a
	// version so a late, older aggregate can never overwrite a newer one.
	Version int64 `json:"version"`
}

type Location struct {
	ID            string    `json:"id"`
	Zone          string    `json:"zone"`
	Label         string    `json:"label"`
	GridX         int       `json:"x"`
	GridY         int       `json:"y"`
	ImpliedStatus ULDStatus `json:"implied_status"`
}

// Message types sent over the WebSocket / Redis pub-sub channel.
const (
	MsgSnapshot     = "snapshot"
	MsgULDUpdate    = "uld_update"
	MsgFlightUpdate = "flight_update"
)

type Envelope struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

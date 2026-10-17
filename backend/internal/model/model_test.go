package model

import (
	"strings"
	"testing"
	"time"
)

func validEvent() ScanEvent {
	return ScanEvent{
		ULDID: " ake12345ga ", ScannerID: "SCN-01", ScanType: "rfid",
		WeightKg: 812.5, LocationID: "sta-01", FlightNumber: "ga880",
	}
}

func TestNormalizeAndValidate(t *testing.T) {
	now := time.Now()
	ev := validEvent()
	ev.Normalize(now)
	if err := ev.Validate(now); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ev.ULDID != "AKE12345GA" || ev.LocationID != "STA-01" || ev.FlightNumber != "GA880" || ev.ScanType != ScanRFID {
		t.Fatalf("not normalized: %+v", ev)
	}
	if ev.EventID == "" || ev.Timestamp.IsZero() {
		t.Fatal("defaults not filled")
	}
}

func TestValidateRejects(t *testing.T) {
	now := time.Now()
	cases := map[string]func(*ScanEvent){
		"bad uld id":    func(e *ScanEvent) { e.ULDID = "X1" },
		"bad scan type": func(e *ScanEvent) { e.ScanType = "NFC" },
		"neg weight":    func(e *ScanEvent) { e.WeightKg = -1 },
		"huge weight":   func(e *ScanEvent) { e.WeightKg = 1e6 },
		"no location":   func(e *ScanEvent) { e.LocationID = "" },
		"bad status":    func(e *ScanEvent) { e.Status = "LOST" },
		"future":        func(e *ScanEvent) { e.Timestamp = now.Add(time.Hour) },
		"bad event id":  func(e *ScanEvent) { e.EventID = "nope" },
		"too old":       func(e *ScanEvent) { e.Timestamp = now.Add(-25 * time.Hour) },
		"no scanner":    func(e *ScanEvent) { e.ScannerID = "" },
		"long scanner":  func(e *ScanEvent) { e.ScannerID = strings.Repeat("A", 65) },
		"scanner html":  func(e *ScanEvent) { e.ScannerID = "<script>alert(1)</script>" },
		"location sqli": func(e *ScanEvent) { e.LocationID = "STA-01' OR '1'='1" },
		"bad flight":    func(e *ScanEvent) { e.FlightNumber = "GA880; DROP TABLE flights" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			ev := validEvent()
			ev.Normalize(now)
			mutate(&ev)
			if err := ev.Validate(now); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestValidateAcceptsRealIDs(t *testing.T) {
	now := time.Now()
	for _, loc := range []string{"STA-06", "SYD-RCV-01", "CGK-BUP-02"} {
		for _, scanner := range []string{"SCN-RCV-01", "WEB-LOADMASTER", "ROGUE-01"} {
			ev := validEvent()
			ev.LocationID, ev.ScannerID, ev.FlightNumber = loc, scanner, "QZ7510"
			ev.Normalize(now)
			if err := ev.Validate(now); err != nil {
				t.Errorf("%s/%s rejected: %v", loc, scanner, err)
			}
		}
	}
}

func TestValidateReportsAllErrors(t *testing.T) {
	ev := ScanEvent{EventID: "x"}
	err := ev.Validate(time.Now())
	if err == nil || strings.Count(err.Error(), "\n") < 4 {
		t.Fatalf("expected multiple joined errors, got %v", err)
	}
}

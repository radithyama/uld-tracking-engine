// Package detect holds the behavioural security detections run against each
// scan. Functions are pure so they can be unit tested without infrastructure.
package detect

import (
	"log/slog"
	"math"
	"time"

	"github.com/radithya/uld-engine/internal/model"
	"github.com/radithya/uld-engine/internal/secevent"
)

type Config struct {
	// Implied ground speed above which travel between two stations is
	// impossible. Wide-body cruise is ~900 km/h; 1000 km/h leaves headroom
	// for tailwinds and clock skew while still catching real anomalies.
	MaxSpeedKmh float64
	// Stations closer than this are treated as the same metro area.
	MinDistanceKm float64
	// Weight above limit*(1+SuspiciousMargin) is treated as implausible
	// (sensor tampering / spoofed scan) rather than an ops overload.
	SuspiciousMargin float64
	// A LOADED ULD's weight may drift by sensor noise but not by more than
	// max(LoadedDeltaKg, LoadedDeltaPct*previous weight).
	LoadedDeltaKg  float64
	LoadedDeltaPct float64
}

func DefaultConfig() Config {
	return Config{MaxSpeedKmh: 1000, MinDistanceKm: 50, SuspiciousMargin: 0.25, LoadedDeltaKg: 50, LoadedDeltaPct: 0.10}
}

// Fix is a ULD sighting at a known station.
type Fix struct {
	Station    string
	LocationID string
	Lat, Lon   float64
	At         time.Time
}

// Input is everything the detections need about one scan, gathered inside
// the processor's transaction.
type Input struct {
	Event      *model.ScanEvent
	Prev       *Fix // nil if the ULD has never been scanned
	Cur        Fix
	PrevWeight float64
	PrevStatus model.ULDStatus
	NewStatus  model.ULDStatus
	TareKg     float64
	LimitKg    float64 // effective limit: min(ULD rating, aircraft per-position)
	Flight     string
	Aircraft   string
	Stale      bool // event older than current state (out of order)
}

// Finding is one triggered detection.
type Finding struct {
	Rule   secevent.Rule
	Reason string
	Attrs  []slog.Attr
}

// Flag is the short label stored on the scan row (uld_scan_events.security_flags).
func (f Finding) Flag() string { return f.Rule.ID }

func Evaluate(cfg Config, in Input) []Finding {
	var out []Finding
	if f, ok := impossibleTravel(cfg, in); ok {
		out = append(out, f)
	}
	if f, ok := weightImplausible(cfg, in); ok {
		out = append(out, f)
	}
	if f, ok := belowTare(in); ok {
		out = append(out, f)
	}
	if !in.Stale {
		if f, ok := loadedWeightChange(cfg, in); ok {
			out = append(out, f)
		}
	}
	return out
}

func uldAttrs(in Input) slog.Attr {
	return slog.Group("uld",
		slog.String("id", in.Event.ULDID),
		slog.String("flight", in.Flight),
		slog.String("aircraft", in.Aircraft),
		slog.String("scanner_id", secevent.Clean(in.Event.ScannerID, 64)),
		slog.String("event_id", in.Event.EventID),
		slog.String("location_id", in.Event.LocationID),
	)
}

func impossibleTravel(cfg Config, in Input) (Finding, bool) {
	if in.Prev == nil || in.Prev.Station == in.Cur.Station {
		return Finding{}, false
	}
	dist := HaversineKm(in.Prev.Lat, in.Prev.Lon, in.Cur.Lat, in.Cur.Lon)
	if dist < cfg.MinDistanceKm {
		return Finding{}, false
	}
	dt := in.Cur.At.Sub(in.Prev.At)
	if dt < 0 {
		dt = -dt
	}
	hours := math.Max(dt.Hours(), 1.0/3600) // floor at 1s to avoid div-by-zero
	speed := dist / hours
	if speed <= cfg.MaxSpeedKmh {
		return Finding{}, false
	}
	minFeasible := time.Duration(dist / cfg.MaxSpeedKmh * float64(time.Hour))
	return Finding{
		Rule:   secevent.GeoImpossibleTravel,
		Reason: "implied_speed_exceeds_threshold",
		Attrs: []slog.Attr{
			uldAttrs(in),
			slog.Group("geo",
				slog.Group("previous",
					slog.String("station", in.Prev.Station),
					slog.String("location_id", in.Prev.LocationID),
					slog.Time("scanned_at", in.Prev.At.UTC()),
					slog.Group("location", slog.Float64("lat", in.Prev.Lat), slog.Float64("lon", in.Prev.Lon)),
				),
				slog.Group("current",
					slog.String("station", in.Cur.Station),
					slog.String("location_id", in.Cur.LocationID),
					slog.Time("scanned_at", in.Cur.At.UTC()),
					slog.Group("location", slog.Float64("lat", in.Cur.Lat), slog.Float64("lon", in.Cur.Lon)),
				),
				slog.Float64("distance_km", round1(dist)),
				slog.Float64("elapsed_seconds", round1(dt.Seconds())),
				slog.Float64("implied_speed_kmh", round1(speed)),
				slog.Float64("threshold_kmh", cfg.MaxSpeedKmh),
				slog.Float64("min_feasible_seconds", round1(minFeasible.Seconds())),
			),
		},
	}, true
}

func weightImplausible(cfg Config, in Input) (Finding, bool) {
	if in.LimitKg <= 0 {
		return Finding{}, false
	}
	threshold := in.LimitKg * (1 + cfg.SuspiciousMargin)
	if in.Event.WeightKg <= threshold {
		return Finding{}, false
	}
	return Finding{
		Rule:   secevent.IntegrityWeightImplausible,
		Reason: "weight_exceeds_limit_by_suspicious_margin",
		Attrs: []slog.Attr{
			uldAttrs(in),
			slog.Group("weight",
				slog.Float64("reported_kg", in.Event.WeightKg),
				slog.Float64("limit_kg", in.LimitKg),
				slog.Float64("threshold_kg", round1(threshold)),
				slog.Float64("excess_pct", round1((in.Event.WeightKg/in.LimitKg-1)*100)),
				slog.Float64("previous_kg", in.PrevWeight),
			),
		},
	}, true
}

func belowTare(in Input) (Finding, bool) {
	// 10% tolerance for scale calibration.
	if in.TareKg <= 0 || in.Event.WeightKg >= in.TareKg*0.9 {
		return Finding{}, false
	}
	return Finding{
		Rule:   secevent.IntegrityBelowTare,
		Reason: "gross_weight_below_tare",
		Attrs: []slog.Attr{
			uldAttrs(in),
			slog.Group("weight",
				slog.Float64("reported_kg", in.Event.WeightKg),
				slog.Float64("tare_kg", in.TareKg),
				slog.Float64("previous_kg", in.PrevWeight),
			),
		},
	}, true
}

func loadedWeightChange(cfg Config, in Input) (Finding, bool) {
	if in.PrevStatus != model.StatusLoaded || in.NewStatus != model.StatusLoaded || in.Prev == nil {
		return Finding{}, false
	}
	delta := in.Event.WeightKg - in.PrevWeight
	allowed := math.Max(cfg.LoadedDeltaKg, cfg.LoadedDeltaPct*in.PrevWeight)
	if math.Abs(delta) <= allowed {
		return Finding{}, false
	}
	return Finding{
		Rule:   secevent.IntegrityLoadedWeightChange,
		Reason: "weight_changed_after_loading",
		Attrs: []slog.Attr{
			uldAttrs(in),
			slog.Group("weight",
				slog.Float64("reported_kg", in.Event.WeightKg),
				slog.Float64("previous_kg", in.PrevWeight),
				slog.Float64("delta_kg", round1(delta)),
				slog.Float64("allowed_delta_kg", round1(allowed)),
			),
		},
	}, true
}

// HaversineKm is the great-circle distance between two points.
func HaversineKm(lat1, lon1, lat2, lon2 float64) float64 {
	const r = 6371.0088
	rad := math.Pi / 180
	dLat := (lat2 - lat1) * rad
	dLon := (lon2 - lon1) * rad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * r * math.Asin(math.Sqrt(a))
}

func round1(f float64) float64 { return math.Round(f*10) / 10 }

// Package secevent emits security telemetry for SIEM ingestion.
//
// Each event is one JSON log line in the uld.security dataset, using Elastic
// Common Schema fields so that Security Onion / Elastic can index, correlate
// and alert on it without custom parsing:
//
//	event.kind / category / type / action / outcome / severity / dataset / reason
//	rule.id / rule.name / rule.ruleset
//	source.ip, user.name, uld.*, geo.*, kafka.* … (event specific)
//
// Filter in the SIEM with: event.dataset:"uld.security".
package secevent

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"unicode"
)

const (
	Dataset = "uld.security"
	Ruleset = "uld-engine"
	Module  = "uld-engine"
)

// Severity follows the ECS 0–100 convention (Security Onion maps
// 21=low, 47=medium, 73=high, 99=critical).
const (
	SevLow      = 21
	SevMedium   = 47
	SevHigh     = 73
	SevCritical = 99
)

type Rule struct {
	ID       string
	Name     string
	Action   string   // event.action
	Kind     string   // event.kind: "event" or "alert"
	Category []string // event.category (ECS allowed values)
	Type     []string // event.type (ECS allowed values)
	Severity int
	Level    slog.Level
}

// Rule catalogue. IDs are stable so SIEM correlation rules can key on them.
var (
	// --- Authentication / authorization (ingest) ---
	// Success events are opt-in (AUTH_LOG_SUCCESS) for volume reasons, but
	// they are what lets a SIEM correlate "N failures then a success".
	AuthSuccess = Rule{"ULD-AUTH-000", "API key authenticated", "auth.success",
		"event", []string{"authentication"}, []string{"start", "allowed"}, 0, slog.LevelInfo}
	AuthMissingCredentials = Rule{"ULD-AUTH-001", "API request without credentials", "auth.missing_credentials",
		"event", []string{"authentication"}, []string{"start"}, SevLow, slog.LevelWarn}
	AuthMalformedHeader = Rule{"ULD-AUTH-002", "Malformed authentication header", "auth.malformed_header",
		"event", []string{"authentication"}, []string{"start"}, SevMedium, slog.LevelWarn}
	AuthInvalidKey = Rule{"ULD-AUTH-003", "Invalid API key", "auth.invalid_api_key",
		"event", []string{"authentication"}, []string{"start"}, SevMedium, slog.LevelWarn}
	AuthBruteForce = Rule{"ULD-AUTH-004", "Repeated authentication failures from one source", "auth.brute_force_suspected",
		"alert", []string{"authentication", "intrusion_detection"}, []string{"indicator"}, SevHigh, slog.LevelError}
	AuthRevokedKey = Rule{"ULD-AUTH-005", "Revoked API key presented", "auth.revoked_api_key",
		"alert", []string{"authentication"}, []string{"indicator"}, SevHigh, slog.LevelError}
	AuthzInsufficientScope = Rule{"ULD-AUTHZ-001", "API key lacks required scope", "authz.insufficient_scope",
		"event", []string{"iam"}, []string{"denied"}, SevMedium, slog.LevelWarn}

	// --- Input handling (ingest) ---
	InputRejected = Rule{"ULD-INPUT-001", "Telemetry payload rejected by validation", "input.validation_failed",
		"event", []string{"web"}, []string{"error"}, SevLow, slog.LevelWarn}

	// --- Behavioural detections (processor) ---
	GeoImpossibleTravel = Rule{"ULD-GEO-001", "ULD impossible travel between stations", "geo.impossible_travel",
		"alert", []string{"intrusion_detection"}, []string{"indicator"}, SevHigh, slog.LevelError}
	IntegrityWeightImplausible = Rule{"ULD-INT-001", "ULD weight exceeds design limit by suspicious margin", "integrity.weight_implausible",
		"alert", []string{"intrusion_detection"}, []string{"indicator"}, SevHigh, slog.LevelError}
	IntegrityBelowTare = Rule{"ULD-INT-002", "ULD gross weight below empty (tare) weight", "integrity.weight_below_tare",
		"alert", []string{"intrusion_detection"}, []string{"indicator"}, SevMedium, slog.LevelWarn}
	IntegrityLoadedWeightChange = Rule{"ULD-INT-003", "Weight of a loaded ULD changed", "integrity.loaded_weight_changed",
		"alert", []string{"intrusion_detection"}, []string{"indicator"}, SevHigh, slog.LevelError}

	// --- Pipeline integrity (processor) ---
	DLQRouted = Rule{"ULD-DLQ-001", "Telemetry message routed to dead-letter queue", "pipeline.dlq_routed",
		"event", []string{"intrusion_detection"}, []string{"info"}, SevMedium, slog.LevelWarn}

	// --- Availability (both) ---
	IngestDegraded = Rule{"ULD-AVAIL-001", "Ingest lost Kafka; buffering telemetry to disk spool", "availability.ingest_degraded",
		"event", []string{"network"}, []string{"connection"}, SevMedium, slog.LevelWarn}
	IngestRecovered = Rule{"ULD-AVAIL-002", "Ingest spool drained; direct Kafka publishing restored", "availability.ingest_recovered",
		"event", []string{"network"}, []string{"connection"}, SevLow, slog.LevelInfo}
	IngestSpoolFull = Rule{"ULD-AVAIL-003", "Ingest disk spool full; rejecting telemetry", "availability.spool_full",
		"alert", []string{"network"}, []string{"denied"}, SevCritical, slog.LevelError}
	ProcessorKafkaDown = Rule{"ULD-AVAIL-004", "Stream processor lost Kafka connectivity", "availability.processor_kafka_down",
		"event", []string{"network"}, []string{"connection"}, SevMedium, slog.LevelWarn}
	ProcessorKafkaUp = Rule{"ULD-AVAIL-005", "Stream processor Kafka connectivity restored", "availability.processor_kafka_restored",
		"event", []string{"network"}, []string{"connection"}, SevLow, slog.LevelInfo}
)

var counts sync.Map // rule ID -> *atomic.Int64

// Emit writes one security event. outcome is the ECS event.outcome
// ("success", "failure" or "unknown"); reason is a short machine-friendly
// explanation (event.reason).
func Emit(ctx context.Context, log *slog.Logger, r Rule, outcome, reason string, attrs ...slog.Attr) {
	c, _ := counts.LoadOrStore(r.ID, new(atomic.Int64))
	c.(*atomic.Int64).Add(1)

	all := make([]slog.Attr, 0, len(attrs)+2)
	all = append(all,
		slog.Group("event",
			slog.String("kind", r.Kind),
			slog.Any("category", r.Category),
			slog.Any("type", r.Type),
			slog.String("action", r.Action),
			slog.String("outcome", outcome),
			slog.Int("severity", r.Severity),
			slog.String("dataset", Dataset),
			slog.String("module", Module),
			slog.String("reason", reason),
		),
		slog.Group("rule", slog.String("id", r.ID), slog.String("name", r.Name), slog.String("ruleset", Ruleset)),
	)
	all = append(all, attrs...)
	log.LogAttrs(ctx, r.Level, r.Name, all...)
}

// Counts returns how many events of each rule have been emitted by this
// process (exposed on the /metrics endpoints for test verification).
func Counts() map[string]int64 {
	out := map[string]int64{}
	counts.Range(func(k, v any) bool {
		out[k.(string)] = v.(*atomic.Int64).Load()
		return true
	})
	return out
}

// SortedIDs is a helper for stable output.
func SortedIDs(m map[string]int64) []string {
	ids := make([]string, 0, len(m))
	for k := range m {
		ids = append(ids, k)
	}
	sort.Strings(ids)
	return ids
}

// Clean makes an attacker-controlled string safe and bounded for logging:
// control characters are replaced and length is capped. (The JSON encoder
// already escapes quotes, so this is about log size and analyst readability.)
func Clean(s string, max int) string {
	if len(s) > max {
		s = s[:max] + "…"
	}
	if strings.IndexFunc(s, unicode.IsControl) < 0 {
		return s
	}
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return '�'
		}
		return r
	}, s)
}

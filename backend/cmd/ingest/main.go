// Command ingest is the Telemetry Ingestion API: it validates scanner events
// and publishes them to Kafka, keyed by ULD id.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/segmentio/kafka-go"

	"github.com/radithya/uld-engine/internal/config"
	"github.com/radithya/uld-engine/internal/httpx"
	"github.com/radithya/uld-engine/internal/kafkax"
	"github.com/radithya/uld-engine/internal/logx"
	"github.com/radithya/uld-engine/internal/model"
	"github.com/radithya/uld-engine/internal/secevent"
	"github.com/radithya/uld-engine/internal/spool"
)

const (
	maxBodyBytes = 1 << 20 // 1 MiB
	maxBatch     = 500
)

type server struct {
	log      *slog.Logger
	pub      *Publisher
	accepted atomic.Int64
	rejected atomic.Int64
}

func main() {
	httpx.HandleHealthcheckCommand() // `ingest healthcheck <url>` for Docker HEALTHCHECK

	cfg := config.LoadCommon()
	log := logx.New("ingest", cfg.LogLevel)
	ctx, stop := logx.SignalContext()
	defer stop()

	keys, err := loadKeys(config.String("API_KEYS_FILE", "config/api-keys.dev.json"))
	if err != nil {
		logx.Fatal(log, "load api keys", err)
	}
	auth := &Authenticator{
		log:    log,
		byHash: keys,
		brute: newBruteForceTracker(
			config.Duration("AUTH_BRUTE_FORCE_WINDOW", time.Minute),
			config.Int("AUTH_BRUTE_FORCE_THRESHOLD", 10)),
		logSuccess: config.String("AUTH_LOG_SUCCESS", "false") == "true",
	}

	sp, err := spool.Open(
		config.String("SPOOL_DIR", "/var/lib/uld/spool"),
		int64(config.Int("SPOOL_MAX_MB", 512))<<20,
		config.Int("SPOOL_SEGMENT_RECORDS", 2000))
	if err != nil {
		logx.Fatal(log, "open spool", err)
	}
	defer sp.Close()

	writer := kafkax.NewWriter(cfg.KafkaBrokers, cfg.KafkaTopic)
	writer.ErrorLogger = kafka.LoggerFunc(logx.Sampled(log, slog.LevelWarn, "kafka-writer", 5*time.Second))
	defer func() {
		// Close flushes any buffered batches before exit.
		if err := writer.Close(); err != nil {
			log.Error("kafka writer close", "err", err)
		}
	}()

	pub := NewPublisher(log, writer, sp, config.Duration("KAFKA_PUBLISH_TIMEOUT", 3*time.Second))
	if sp.Pending() > 0 {
		// Telemetry spooled before a restart: replay it before going direct.
		pub.enterDegraded(ctx, nil)
		log.Info("recovered spooled telemetry from disk", "records", sp.Pending())
	}
	replayDone := make(chan struct{})
	go func() { defer close(replayDone); pub.RunReplayer(ctx) }()

	s := &server{log: log, pub: pub}

	r := chi.NewRouter()
	// No middleware.RealIP: X-Forwarded-For is client-controlled (see httpx.ClientIP).
	r.Use(middleware.RequestID, httpx.Logger(log), httpx.Recoverer(log))
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok"})
	})
	// Ready while accepting telemetry, including in degraded (spooling) mode.
	r.Get("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		mode := "direct"
		if pub.degraded.Load() {
			mode = "degraded_spooling"
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"status": "ready", "mode": mode,
			"spool_pending": sp.Pending(), "spool_bytes": sp.Bytes(),
		})
	})
	r.Get("/metrics", func(w http.ResponseWriter, _ *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"accepted":             s.accepted.Load(),
			"rejected":             s.rejected.Load(),
			"published_direct":     pub.direct.Load(),
			"spooled":              pub.spooled.Load(),
			"replayed":             pub.replayed.Load(),
			"spool_corrupt":        pub.corrupt.Load(),
			"spool_pending":        sp.Pending(),
			"degraded":             pub.degraded.Load(),
			"security_event_count": secevent.Counts(),
		})
	})
	r.With(auth.Require("scans:write")).Post("/v1/scans", s.handleScans)

	srv := &http.Server{
		Addr:              config.String("HTTP_ADDR", ":8080"),
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	if err := httpx.Serve(ctx, log, srv); err != nil {
		logx.Fatal(log, "http server", err)
	}
	stop()
	<-replayDone
}

func (s *server) inputRejected(r *http.Request, reason string, invalid int, detail string) {
	p := principalFrom(r.Context())
	secevent.Emit(r.Context(), s.log, secevent.InputRejected, "failure", reason,
		append(requestAttrs(r, nil),
			slog.Group("user", slog.String("name", p.Name)),
			slog.Group("api_key", slog.String("id", p.KeyID)),
			slog.Int("invalid_items", invalid),
			slog.String("error.message", secevent.Clean(detail, 256)))...)
}

type fieldError struct {
	Index int    `json:"index"`
	Error string `json:"error"`
}

// handleScans accepts a single scan object or a JSON array of scans.
// The whole request is validated before anything is published, so a batch is
// accepted or rejected as a unit.
func (s *server) handleScans(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		s.rejected.Add(1)
		s.inputRejected(r, "body_too_large_or_unreadable", 0, err.Error())
		httpx.WriteError(w, http.StatusRequestEntityTooLarge, "body too large or unreadable", nil)
		return
	}

	events, err := decodeScans(body)
	if err != nil {
		s.rejected.Add(1)
		s.inputRejected(r, "malformed_json", 0, err.Error())
		httpx.WriteError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}

	now := time.Now().UTC()
	var problems []fieldError
	msgs := make([]kafka.Message, 0, len(events))
	ids := make([]string, 0, len(events))
	for i := range events {
		ev := &events[i]
		ev.Normalize(now)
		if err := ev.Validate(now); err != nil {
			problems = append(problems, fieldError{Index: i, Error: err.Error()})
			continue
		}
		payload, err := json.Marshal(ev)
		if err != nil {
			problems = append(problems, fieldError{Index: i, Error: err.Error()})
			continue
		}
		msgs = append(msgs, kafka.Message{
			Key:   []byte(ev.ULDID),
			Value: payload,
			Time:  ev.Timestamp,
			Headers: []kafka.Header{
				{Key: "event_id", Value: []byte(ev.EventID)},
				{Key: "content-type", Value: []byte("application/json")},
			},
		})
		ids = append(ids, ev.EventID)
	}
	if len(problems) > 0 {
		s.rejected.Add(int64(len(events)))
		s.inputRejected(r, "schema_validation_failed", len(problems), problems[0].Error)
		httpx.WriteError(w, http.StatusUnprocessableEntity, "validation failed", problems)
		return
	}

	durability, err := s.pub.Publish(r.Context(), msgs)
	if err != nil {
		// Only reached if Kafka AND the local spool are unavailable/full.
		s.log.Error("telemetry not accepted", "err", err, "count", len(msgs))
		w.Header().Set("Retry-After", "5")
		httpx.WriteError(w, http.StatusServiceUnavailable, "telemetry buffer unavailable, retry with the same event_id", nil)
		return
	}

	s.accepted.Add(int64(len(msgs)))
	httpx.WriteJSON(w, http.StatusAccepted, map[string]any{
		"accepted": len(msgs), "event_ids": ids, "durability": durability,
	})
}

func decodeScans(body []byte) ([]model.ScanEvent, error) {
	trimmed := bytes.TrimLeft(body, " \t\r\n")
	if len(trimmed) == 0 {
		return nil, errors.New("empty body")
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()

	var events []model.ScanEvent
	if trimmed[0] == '[' {
		if err := dec.Decode(&events); err != nil {
			return nil, fmt.Errorf("invalid JSON array: %w", err)
		}
	} else {
		var ev model.ScanEvent
		if err := dec.Decode(&ev); err != nil {
			return nil, fmt.Errorf("invalid JSON object: %w", err)
		}
		events = append(events, ev)
	}
	if len(events) == 0 {
		return nil, errors.New("no events")
	}
	if len(events) > maxBatch {
		return nil, fmt.Errorf("batch too large: %d > %d", len(events), maxBatch)
	}
	return events, nil
}

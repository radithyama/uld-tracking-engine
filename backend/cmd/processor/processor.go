package main

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"

	"github.com/radithya/uld-engine/internal/detect"
	"github.com/radithya/uld-engine/internal/kafkax"
	"github.com/radithya/uld-engine/internal/logx"
	"github.com/radithya/uld-engine/internal/model"
	"github.com/radithya/uld-engine/internal/secevent"
	"github.com/radithya/uld-engine/internal/store"
)

// Processor is shared by all worker goroutines. Every field is either
// immutable after construction, internally synchronised (pgxpool, go-redis,
// kafka.Writer), or atomic.
type Processor struct {
	log    *slog.Logger
	pg     *store.Postgres
	cache  *store.Redis
	dlq    *kafka.Writer
	detect detect.Config

	applied    atomic.Int64
	duplicates atomic.Int64
	stale      atomic.Int64
	deadLetter atomic.Int64
	findings   atomic.Int64

	// Kafka connectivity, shared by all workers so the SIEM gets exactly one
	// "down" and one "restored" event per outage rather than one per worker.
	fetchErrors     atomic.Int64
	kafkaDown       atomic.Bool
	kafkaDownSince  atomic.Int64 // unix nanos
	troubleSince    atomic.Int64 // unix nanos of first error since last good fetch; 0 = healthy
	lastFetchErrMsg atomic.Value // string
}

// Namespace for deriving deterministic event ids for messages that arrive
// without one (e.g. from a producer other than the ingest API).
var eventNamespace = uuid.MustParse("6f1d0b8e-5f0c-4a5e-9a52-9a0c1b7e2d11")

// handle processes one message to completion. It returns an error only when
// ctx is cancelled; every other failure is retried or dead-lettered.
func (p *Processor) handle(ctx context.Context, m kafka.Message) error {
	var ev model.ScanEvent
	if err := json.Unmarshal(m.Value, &ev); err != nil {
		return p.sendToDLQ(ctx, m, &ev, "decode_error", err)
	}
	if ev.EventID == "" {
		ev.EventID = uuid.NewSHA1(eventNamespace,
			[]byte(fmt.Sprintf("%s/%d/%d", m.Topic, m.Partition, m.Offset))).String()
	}
	// Re-validate: the topic is a trust boundary, not only the HTTP API.
	now := time.Now().UTC()
	ev.Normalize(now)
	if err := ev.Validate(now); err != nil {
		return p.sendToDLQ(ctx, m, &ev, "schema_validation_failed", err)
	}

	var res store.ApplyResult
	err := retry(ctx, p.log, "apply scan", func(ctx context.Context) error {
		var err error
		res, err = p.pg.ApplyScan(ctx, &ev, m.Partition, m.Offset, p.detect)
		return err
	})
	switch {
	case errors.Is(err, store.ErrPermanent):
		return p.sendToDLQ(ctx, m, &ev, "reference_data_or_constraint", err)
	case err != nil:
		return err
	}

	// Emitted only after the transaction committed, and never for duplicates,
	// so each finding maps 1:1 to a flagged row in uld_scan_events.
	for _, f := range res.Findings {
		p.findings.Add(1)
		secevent.Emit(ctx, p.log, f.Rule, "unknown", f.Reason, append(f.Attrs,
			slog.Group("kafka", slog.Int("partition", m.Partition), slog.Int64("offset", m.Offset)))...)
	}

	switch {
	case res.Stale:
		p.stale.Add(1)
		return nil // current state unchanged; nothing to project
	case res.Duplicate:
		// Still re-project: a previous attempt may have committed to Postgres
		// and then crashed before it reached Redis.
		p.duplicates.Add(1)
	default:
		p.applied.Add(1)
	}

	return retry(ctx, p.log, "project to redis", func(ctx context.Context) error {
		return p.project(ctx, ev.ULDID, res.FlightIDs)
	})
}

// project pushes the authoritative Postgres state for a ULD (and any flights
// whose load it affects) into Redis, which in turn fans out to WebSockets.
func (p *Processor) project(ctx context.Context, uldID string, flightIDs []int64) error {
	st, err := p.pg.ULDState(ctx, uldID)
	if err != nil {
		return err
	}
	if err := p.cache.PutULD(ctx, st); err != nil {
		return err
	}
	ids := append([]int64(nil), flightIDs...)
	if st.FlightID != nil && !slices.Contains(ids, *st.FlightID) {
		ids = append(ids, *st.FlightID)
	}
	if len(ids) == 0 {
		return nil
	}
	loads, err := p.pg.FlightLoads(ctx, ids)
	if err != nil {
		return err
	}
	for _, fl := range loads {
		if err := p.cache.PutFlight(ctx, fl); err != nil {
			return err
		}
	}
	if st.Overweight {
		p.log.Warn("ULD over weight limit", "uld", st.ID, "weight_kg", st.WeightKg,
			"limit_kg", st.WeightLimitKg, "flight", st.FlightNumber)
	}
	return nil
}

// sendToDLQ parks a message that can never be processed. ev may be partially
// populated (or empty) depending on how far decoding got.
func (p *Processor) sendToDLQ(ctx context.Context, m kafka.Message, ev *model.ScanEvent, reason string, cause error) error {
	sum := sha256.Sum256(m.Value)
	digest := hex.EncodeToString(sum[:])
	source := m.Topic + "/" + strconv.Itoa(m.Partition) + "/" + strconv.FormatInt(m.Offset, 10)
	dl := kafka.Message{
		Key:   m.Key,
		Value: m.Value,
		Headers: append(append([]kafka.Header(nil), m.Headers...),
			kafka.Header{Key: "dlq.reason", Value: []byte(reason)},
			kafka.Header{Key: "dlq.error", Value: []byte(cause.Error())},
			kafka.Header{Key: "dlq.source", Value: []byte(source)},
			kafka.Header{Key: "dlq.sha256", Value: []byte(digest)},
		),
	}
	// The offset is only committed after the DLQ write succeeds, so a poison
	// message is never silently lost.
	err := retry(ctx, p.log, "dlq publish", func(ctx context.Context) error {
		return p.dlq.WriteMessages(ctx, dl)
	})
	if err != nil {
		return err
	}
	p.deadLetter.Add(1)
	// The raw payload is attacker-controlled and unbounded, so the SIEM gets
	// its hash and size (enough to correlate and to fetch it from the DLQ),
	// never the payload itself.
	secevent.Emit(ctx, p.log, secevent.DLQRouted, "failure", reason,
		slog.String("error.message", secevent.Clean(cause.Error(), 512)),
		slog.Group("kafka",
			slog.String("topic", m.Topic), slog.Int("partition", m.Partition), slog.Int64("offset", m.Offset),
			slog.String("key", secevent.Clean(string(m.Key), 64)),
			slog.String("dlq_topic", p.dlq.Topic),
		),
		slog.Group("file", slog.Int("size", len(m.Value)), slog.Group("hash", slog.String("sha256", digest))),
		slog.Group("uld",
			slog.String("id", secevent.Clean(ev.ULDID, 64)),
			slog.String("event_id", secevent.Clean(cmp.Or(ev.EventID, kafkax.Header(m, "event_id")), 64)),
			slog.String("scanner_id", secevent.Clean(ev.ScannerID, 64)),
		),
	)
	return nil
}

// Kafka connectivity tracking. kafka-go retries internally and reports most
// broker trouble only through its ErrorLogger (FetchMessage just blocks), so
// both that logger and FetchMessage errors feed kafkaTrouble. A 5s
// hysteresis keeps routine rebalance chatter from raising a false "down".
const kafkaDownAfter = 5 * time.Second

func (p *Processor) kafkaTrouble(ctx context.Context, msg string) {
	p.fetchErrors.Add(1)
	p.lastFetchErrMsg.Store(msg)
	now := time.Now().UnixNano()
	p.troubleSince.CompareAndSwap(0, now)
	since := p.troubleSince.Load()
	if since == 0 || time.Duration(now-since) < kafkaDownAfter {
		return
	}
	if p.kafkaDown.CompareAndSwap(false, true) {
		p.kafkaDownSince.Store(since)
		secevent.Emit(ctx, p.log, secevent.ProcessorKafkaDown, "failure", "broker_unreachable",
			slog.String("error.message", secevent.Clean(msg, 512)))
	}
}

func (p *Processor) fetchOK(ctx context.Context) {
	p.troubleSince.Store(0)
	if p.kafkaDown.CompareAndSwap(true, false) {
		down := time.Since(time.Unix(0, p.kafkaDownSince.Load()))
		secevent.Emit(ctx, p.log, secevent.ProcessorKafkaUp, "success", "fetch_succeeded",
			slog.Float64("outage_seconds", down.Round(time.Millisecond).Seconds()))
	}
}

// kafkaErrorLogger feeds kafka-go's internal errors into connectivity
// tracking and a sampled log line.
func (p *Processor) kafkaErrorLogger(component string) kafka.Logger {
	sampled := logx.Sampled(p.log, slog.LevelWarn, component, 5*time.Second)
	return kafka.LoggerFunc(func(format string, args ...any) {
		p.kafkaTrouble(context.Background(), fmt.Sprintf(format, args...))
		sampled(format, args...)
	})
}

func (p *Processor) warmCache(ctx context.Context) error {
	states, err := p.pg.AllULDStates(ctx)
	if err != nil {
		return err
	}
	for _, s := range states {
		if err := p.cache.PutULD(ctx, s); err != nil {
			return err
		}
	}
	loads, err := p.pg.FlightLoads(ctx, nil)
	if err != nil {
		return err
	}
	// Full rebuild: v1 keyed flights by number, v2 by id.
	if err := p.cache.ResetFlights(ctx); err != nil {
		return err
	}
	for _, f := range loads {
		if err := p.cache.PutFlight(ctx, f); err != nil {
			return err
		}
	}
	p.log.Info("redis projection warmed", "ulds", len(states), "flights", len(loads))
	return nil
}

func (p *Processor) reportStats(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.log.Info("stats",
				"applied", p.applied.Load(), "duplicates", p.duplicates.Load(),
				"stale", p.stale.Load(), "dead_lettered", p.deadLetter.Load())
		}
	}
}

// retry runs fn with capped exponential backoff until it succeeds, returns a
// permanent error, or ctx is cancelled.
func retry(ctx context.Context, log *slog.Logger, op string, fn func(context.Context) error) error {
	backoff := 100 * time.Millisecond
	for attempt := 1; ; attempt++ {
		err := fn(ctx)
		if err == nil || errors.Is(err, store.ErrPermanent) {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		log.Warn("transient failure, retrying", "op", op, "attempt", attempt, "backoff", backoff, "err", err)
		if !sleepCtx(ctx, backoff) {
			return ctx.Err()
		}
		backoff = min(backoff*2, 5*time.Second)
	}
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

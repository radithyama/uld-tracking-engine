package main

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/segmentio/kafka-go"

	"github.com/radithya/uld-engine/internal/secevent"
	"github.com/radithya/uld-engine/internal/spool"
)

type producer interface {
	WriteMessages(ctx context.Context, msgs ...kafka.Message) error
}

// Durability says where an accepted batch was made durable.
type Durability string

const (
	DurableKafka Durability = "kafka"
	DurableSpool Durability = "spool"
)

// Publisher implements graceful degradation for the ingest path:
//
//   - Direct mode (spool empty): publish synchronously to Kafka with a short
//     timeout. On failure, fall back to the spool for that batch.
//   - Degraded mode (spool non-empty): every new batch goes to the spool, so
//     requests stop waiting on a dead broker and ordering stays FIFO. A
//     single replayer goroutine drains the spool to Kafka with exponential
//     backoff; when it is empty, direct mode resumes.
//
// Either way the client's 202 means "durable": acked by Kafka (acks=all) or
// fsynced to the spool volume.
type Publisher struct {
	log           *slog.Logger
	w             producer
	spool         *spool.Spool
	directTimeout time.Duration
	wake          chan struct{}

	degraded      atomic.Bool
	degradedSince atomic.Int64

	direct   atomic.Int64
	spooled  atomic.Int64
	replayed atomic.Int64
	corrupt  atomic.Int64
}

func NewPublisher(log *slog.Logger, w producer, sp *spool.Spool, directTimeout time.Duration) *Publisher {
	return &Publisher{log: log, w: w, spool: sp, directTimeout: directTimeout, wake: make(chan struct{}, 1)}
}

func (p *Publisher) Publish(ctx context.Context, msgs []kafka.Message) (Durability, error) {
	if p.spool.Pending() == 0 {
		// Detached from the request context: a client disconnecting must not
		// be mistaken for a broker failure (and flip us into degraded mode).
		wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), p.directTimeout)
		err := p.w.WriteMessages(wctx, msgs...)
		cancel()
		if err == nil {
			p.direct.Add(int64(len(msgs)))
			return DurableKafka, nil
		}
		// A timed-out write may still complete inside the client, so the same
		// event can reach Kafka twice (direct + replay). The processor's
		// event_id idempotency absorbs it.
		p.enterDegraded(ctx, err)
	}

	recs := make([]spool.Record, len(msgs))
	for i, m := range msgs {
		recs[i] = spool.Record{Key: string(m.Key), Value: m.Value, EventID: headerValue(m, "event_id"), Time: m.Time}
	}
	if err := p.spool.Append(recs); err != nil {
		if errors.Is(err, spool.ErrFull) {
			secevent.Emit(ctx, p.log, secevent.IngestSpoolFull, "failure", "spool_capacity_exceeded",
				slog.Int64("spool.pending", p.spool.Pending()), slog.Int64("spool.bytes", p.spool.Bytes()))
		}
		return "", err
	}
	p.spooled.Add(int64(len(recs)))
	p.enterDegraded(ctx, nil)
	select {
	case p.wake <- struct{}{}:
	default:
	}
	return DurableSpool, nil
}

func (p *Publisher) enterDegraded(ctx context.Context, cause error) {
	if !p.degraded.CompareAndSwap(false, true) {
		return
	}
	p.degradedSince.Store(time.Now().UnixNano())
	reason, msg := "spool_backlog", ""
	if cause != nil {
		reason, msg = "kafka_publish_failed", cause.Error()
	}
	secevent.Emit(ctx, p.log, secevent.IngestDegraded, "failure", reason,
		slog.String("error.message", secevent.Clean(msg, 512)),
		slog.Int64("spool.pending", p.spool.Pending()))
}

// RunReplayer drains the spool to Kafka until ctx is cancelled.
func (p *Publisher) RunReplayer(ctx context.Context) {
	// Capped at 10s so recovery is noticed within seconds of the broker
	// returning; a probe every 10s during an outage is negligible load.
	const minBackoff, maxBackoff = 500 * time.Millisecond, 10 * time.Second
	backoff := minBackoff
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	attempt := 0

	for {
		if p.spool.Pending() == 0 {
			if p.degraded.CompareAndSwap(true, false) {
				down := time.Since(time.Unix(0, p.degradedSince.Load()))
				secevent.Emit(ctx, p.log, secevent.IngestRecovered, "success", "spool_drained",
					slog.Float64("outage_seconds", down.Round(time.Millisecond).Seconds()),
					slog.Int64("spool.replayed_total", p.replayed.Load()))
			}
			select {
			case <-ctx.Done():
				return
			case <-p.wake:
			case <-tick.C:
			}
			continue
		}

		name, recs, corrupt, err := p.spool.Oldest()
		if errors.Is(err, spool.ErrEmpty) {
			continue
		}
		if err != nil {
			p.log.Error("spool read failed", "err", err)
			if !sleepCtx(ctx, maxBackoff) {
				return
			}
			continue
		}
		if corrupt > 0 {
			p.corrupt.Add(int64(corrupt))
			p.log.Warn("skipped torn spool records (never acknowledged to clients)", "segment", name, "count", corrupt)
		}

		msgs := make([]kafka.Message, len(recs))
		for i, r := range recs {
			msgs[i] = kafka.Message{
				Key: []byte(r.Key), Value: r.Value, Time: r.Time,
				Headers: []kafka.Header{
					{Key: "event_id", Value: []byte(r.EventID)},
					{Key: "content-type", Value: []byte("application/json")},
					{Key: "ingest.replayed", Value: []byte("true")},
				},
			}
		}

		wctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err = p.w.WriteMessages(wctx, msgs...)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			attempt++
			// Log the first failure and then every 10th so a long outage
			// stays readable; the degraded secevent already alerted.
			if attempt == 1 || attempt%10 == 0 {
				p.log.Warn("spool replay failed; backing off", "err", err, "attempt", attempt,
					"backoff", backoff.String(), "spool_pending", p.spool.Pending())
			}
			if !sleepCtx(ctx, backoff) {
				return
			}
			backoff = min(backoff*2, maxBackoff)
			continue
		}
		if err := p.spool.Ack(name); err != nil {
			p.log.Error("spool ack failed", "err", err, "segment", name)
		}
		p.replayed.Add(int64(len(recs)))
		p.log.Info("spool segment replayed", "segment", name, "records", len(recs), "spool_pending", p.spool.Pending())
		attempt, backoff = 0, minBackoff
	}
}

func headerValue(m kafka.Message, key string) string {
	for _, h := range m.Headers {
		if h.Key == key {
			return string(h.Value)
		}
	}
	return ""
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

// Command processor is the Kafka stream processor: it consumes scan events,
// persists them to PostgreSQL, projects live state into Redis and runs the
// security detections.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/segmentio/kafka-go"

	"github.com/radithya/uld-engine/internal/config"
	"github.com/radithya/uld-engine/internal/detect"
	"github.com/radithya/uld-engine/internal/httpx"
	"github.com/radithya/uld-engine/internal/kafkax"
	"github.com/radithya/uld-engine/internal/logx"
	"github.com/radithya/uld-engine/internal/secevent"
	"github.com/radithya/uld-engine/internal/store"
)

func main() {
	httpx.HandleHealthcheckCommand() // `processor healthcheck <url>` for Docker HEALTHCHECK

	cfg := config.LoadCommon()
	log := logx.New("processor", cfg.LogLevel)
	ctx, stop := logx.SignalContext()
	defer stop()

	workers := config.Int("PROCESSOR_WORKERS", 6)
	det := detect.DefaultConfig()
	det.MaxSpeedKmh = configFloat("DETECT_MAX_SPEED_KMH", det.MaxSpeedKmh)
	det.SuspiciousMargin = configFloat("DETECT_WEIGHT_SUSPICIOUS_MARGIN", det.SuspiciousMargin)

	startCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	pg, err := store.NewPostgres(startCtx, cfg.DatabaseURL, int32(workers*2+2))
	if err != nil {
		cancel()
		logx.Fatal(log, "postgres connect", err)
	}
	defer pg.Close()
	cache, err := store.NewRedis(startCtx, cfg.RedisAddr)
	if err != nil {
		cancel()
		logx.Fatal(log, "redis connect", err)
	}
	defer cache.Close()

	p := &Processor{log: log, pg: pg, cache: cache, detect: det}

	// Security events also feed the dashboard's live security stream.
	secPub := store.NewSecurityPublisher(cache, log)
	secevent.SetSink("processor", secPub.Submit)
	p.dlq = kafkax.NewWriter(cfg.KafkaBrokers, cfg.KafkaDLQTopic)
	p.dlq.ErrorLogger = kafka.LoggerFunc(logx.Sampled(log, slog.LevelWarn, "kafka-dlq-writer", 5*time.Second))
	defer p.dlq.Close()

	// Rebuild the Redis projection from Postgres so the dashboard has full
	// state even after a Redis restart. Version guards make this safe to run
	// concurrently with live updates from other processor instances.
	if err := p.warmCache(startCtx); err != nil {
		log.Warn("cache warm-up failed; continuing", "err", err)
	}
	cancel()

	// One kafka-go Reader per worker; each is a separate consumer-group member.
	// Kafka assigns every partition to exactly one member, so each partition
	// (and therefore each ULD, since we key by ULD id) is processed strictly in
	// order by a single goroutine, while different partitions run in parallel.
	readerLog := p.kafkaErrorLogger("kafka-reader")
	readers := make([]*kafka.Reader, workers)
	var wg sync.WaitGroup
	for i := range workers {
		readers[i] = kafkax.NewGroupReader(cfg.KafkaBrokers, cfg.KafkaTopic, cfg.KafkaGroupID, readerLog)
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.runWorker(ctx, i, readers[i])
		}()
	}

	wg.Add(4)
	go func() {
		defer wg.Done()
		p.reportStats(ctx, 15*time.Second)
	}()
	go func() { defer wg.Done(); secPub.Run(ctx) }()
	go func() {
		defer wg.Done()
		p.runNetworkClock(ctx, config.Duration("NETWORK_TICK", 5*time.Second),
			configFloat("NETWORK_TIME_SCALE", 10), config.Duration("SANDBOX_ULD_TTL", 15*time.Minute))
	}()
	go func() {
		defer wg.Done()
		srv := &http.Server{
			Addr:              config.String("METRICS_ADDR", ":9090"),
			Handler:           p.routes(readers),
			ReadHeaderTimeout: 5 * time.Second,
		}
		if err := httpx.Serve(ctx, log, srv); err != nil {
			// Fail fast rather than run without health checks.
			log.Error("metrics server failed; shutting down", "err", err)
			stop()
		}
	}()

	log.Info("processor started", "workers", workers, "topic", cfg.KafkaTopic, "group", cfg.KafkaGroupID,
		"detect_max_speed_kmh", det.MaxSpeedKmh, "detect_weight_margin", det.SuspiciousMargin)
	<-ctx.Done()
	log.Info("shutting down; draining workers")
	wg.Wait()
	log.Info("processor stopped")
}

func configFloat(key string, def float64) float64 {
	if v, err := strconv.ParseFloat(os.Getenv(key), 64); err == nil {
		return v
	}
	return def
}

// routes: /healthz is liveness (process is up), /readyz reflects Kafka
// connectivity, /metrics exposes counters used by the chaos verifier.
func (p *Processor) routes(readers []*kafka.Reader) http.Handler {
	r := chi.NewRouter()
	r.Use(httpx.Recoverer(p.log))
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok"})
	})
	r.Get("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if p.kafkaDown.Load() {
			httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "kafka_unavailable"})
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"status": "ready"})
	})
	r.Get("/metrics", func(w http.ResponseWriter, _ *http.Request) {
		var lag int64
		for _, rd := range readers {
			if l := rd.Stats().Lag; l > 0 {
				lag += l
			}
		}
		lastErr, _ := p.lastFetchErrMsg.Load().(string)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"applied":              p.applied.Load(),
			"duplicates_absorbed":  p.duplicates.Load(),
			"stale_ignored":        p.stale.Load(),
			"dead_lettered":        p.deadLetter.Load(),
			"security_findings":    p.findings.Load(),
			"kafka_down":           p.kafkaDown.Load(),
			"kafka_fetch_errors":   p.fetchErrors.Load(),
			"kafka_last_error":     lastErr,
			"approx_consumer_lag":  lag,
			"security_event_count": secevent.Counts(),
		})
	})
	return r
}

func (p *Processor) runWorker(ctx context.Context, id int, r *kafka.Reader) {
	log := p.log.With("worker", id)
	defer func() {
		// Close commits any pending offsets and leaves the group cleanly so
		// partitions are rebalanced immediately rather than after session timeout.
		if err := r.Close(); err != nil {
			log.Error("reader close", "err", err)
		}
	}()

	const minBackoff, maxBackoff = 250 * time.Millisecond, 15 * time.Second
	backoff := minBackoff
	for {
		m, err := r.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			// Broker down or paused: kafka-go reconnects internally; we back
			// off so a long outage doesn't spin, and report connectivity once.
			p.kafkaTrouble(ctx, err.Error())
			log.Debug("fetch failed", "err", err, "backoff", backoff)
			if !sleepCtx(ctx, backoff) {
				return
			}
			backoff = min(backoff*2, maxBackoff)
			continue
		}
		backoff = minBackoff
		p.fetchOK(ctx)

		// handle only returns an error if ctx was cancelled mid-flight; the
		// message is then left uncommitted and will be redelivered.
		if err := p.handle(ctx, m); err != nil {
			return
		}

		// Commit with a detached context so a shutdown signal arriving right
		// now doesn't drop the offset of work we already finished. If the
		// commit itself is lost (broker down), the message is redelivered
		// later and absorbed by event_id idempotency.
		commitCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		if err := r.CommitMessages(commitCtx, m); err != nil && !errors.Is(err, context.Canceled) {
			log.Warn("commit failed; message will be redelivered", "err", err, "partition", m.Partition, "offset", m.Offset)
		}
		cancel()
	}
}

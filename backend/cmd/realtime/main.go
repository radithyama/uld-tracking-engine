// Command realtime is the WebSocket server. It subscribes to the Redis
// pub/sub channel the processor publishes to and fans every message out to
// connected dashboards. Instances are stateless and scale horizontally: each
// one holds its own Redis subscription.
package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"

	"github.com/radithya/uld-engine/internal/authz"
	"github.com/radithya/uld-engine/internal/config"
	"github.com/radithya/uld-engine/internal/httpx"
	"github.com/radithya/uld-engine/internal/logx"
	"github.com/radithya/uld-engine/internal/model"
	"github.com/radithya/uld-engine/internal/realtime"
	"github.com/radithya/uld-engine/internal/secevent"
	"github.com/radithya/uld-engine/internal/store"
)

type snapshotData struct {
	ULDs      []json.RawMessage `json:"ulds"`
	Flights   []json.RawMessage `json:"flights"`
	Locations []model.Location  `json:"locations"`
	Stations  []model.Station   `json:"stations"`
	TimeScale float64           `json:"time_scale"` // network clock acceleration
}

func main() {
	httpx.HandleHealthcheckCommand() // `realtime healthcheck <url>` for Docker HEALTHCHECK

	cfg := config.LoadCommon()
	log := logx.New("realtime", cfg.LogLevel)
	ctx, stop := logx.SignalContext()
	defer stop()

	origins := config.List("ALLOWED_ORIGINS", "http://localhost:3000")

	startCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	pg, err := store.NewPostgres(startCtx, cfg.DatabaseURL, 4)
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

	// Network layout is static reference data: load once.
	locations, err := pg.Locations(startCtx)
	if err != nil {
		cancel()
		logx.Fatal(log, "load locations", err)
	}
	stations, err := pg.Stations(startCtx)
	cancel()
	if err != nil {
		logx.Fatal(log, "load stations", err)
	}
	timeScale, _ := strconv.ParseFloat(config.String("NETWORK_TIME_SCALE", "10"), 64)
	secret := []byte(config.String("INTERNAL_API_SECRET", ""))
	if len(secret) == 0 {
		log.Warn("INTERNAL_API_SECRET not set: role-gated APIs and /ws/security are disabled")
	}

	hub := realtime.NewHub(log)
	secHub := realtime.NewHub(log)

	snapshot := func(ctx context.Context) ([]byte, error) {
		ulds, flights, err := cache.Snapshot(ctx)
		if err != nil {
			return nil, err
		}
		return json.Marshal(model.Envelope{Type: model.MsgSnapshot, Data: snapshotData{
			ULDs: ulds, Flights: flights, Locations: locations, Stations: stations, TimeScale: timeScale,
		}})
	}
	securitySnapshot := func(ctx context.Context) ([]byte, error) {
		alerts, err := pg.ListSecurityAlerts(ctx, "", 150)
		if err != nil {
			return nil, err
		}
		return json.Marshal(model.Envelope{Type: model.MsgSnapshot, Data: map[string]any{"alerts": alerts}})
	}

	host, _ := os.Hostname()
	var wg sync.WaitGroup
	wg.Add(5)
	go func() { defer wg.Done(); hub.Run(ctx) }()
	go func() { defer wg.Done(); secHub.Run(ctx) }()
	go func() { defer wg.Done(); relay(ctx, log, cache.Client(), store.ChannelEvents, hub) }()
	go func() { defer wg.Done(); relay(ctx, log, cache.Client(), store.SecurityChannel, secHub) }()
	go func() {
		defer wg.Done()
		// Persist security events from the stream (exactly one realtime
		// instance handles each), then fan out to every instance.
		cache.ConsumeSecurity(ctx, "realtime-"+host, log, func(rec secevent.Record) error {
			alert, inserted, err := pg.InsertSecurityAlert(ctx, rec)
			if err != nil || !inserted {
				return err
			}
			return cache.PublishSecurity(ctx, model.MsgSecurityEvent, alert)
		})
	}()

	r := chi.NewRouter()
	r.Use(middleware.RequestID, httpx.Logger(log), httpx.Recoverer(log))
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok", "clients": hub.ClientCount()})
	})
	r.With(httpx.CORS(origins)).Get("/api/snapshot", func(w http.ResponseWriter, r *http.Request) {
		b, err := snapshot(r.Context())
		if err != nil {
			httpx.WriteError(w, http.StatusServiceUnavailable, "snapshot unavailable", nil)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(b)
	})
	upgrader := websocket.Upgrader{
		ReadBufferSize:    1024,
		WriteBufferSize:   4096,
		EnableCompression: true,
		CheckOrigin: func(r *http.Request) bool {
			o := r.Header.Get("Origin")
			return o == "" || httpx.OriginAllowed(origins, o)
		},
	}
	// Live operations feed: public (equivalent to a departures board).
	r.Handle("/ws", &realtime.Handler{Hub: hub, Log: log, Snapshot: snapshot, Upgrader: upgrader})
	// Security event stream: security auditors only (?token= minted by the BFF).
	r.With(authz.Require(secret, authz.RoleSecurityAuditor)).Handle("/ws/security",
		&realtime.Handler{Hub: secHub, Log: log, Snapshot: securitySnapshot, Upgrader: upgrader})
	r.Mount("/api/v1", (&api{log: log, pg: pg, cache: cache}).routes(secret))

	// No WriteTimeout: WebSocket connections are long-lived and manage their
	// own deadlines.
	srv := &http.Server{
		Addr:              config.String("HTTP_ADDR", ":8081"),
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	if err := httpx.Serve(ctx, log, srv); err != nil {
		log.Error("http server", "err", err)
		stop()
	}
	wg.Wait()
}

// relay forwards every Redis pub/sub message to the hub. go-redis
// transparently reconnects and resubscribes; while disconnected, clients may
// miss updates, which is why each update carries complete state rather than a
// delta, and why clients resync from a snapshot on reconnect.
func relay(ctx context.Context, log *slog.Logger, rdb *redis.Client, channel string, hub *realtime.Hub) {
	sub := rdb.Subscribe(ctx, channel)
	defer sub.Close()

	ch := sub.Channel(redis.WithChannelSize(1024))
	log.Info("subscribed to redis channel", "channel", channel)
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			if err := hub.Broadcast([]byte(msg.Payload)); err != nil {
				log.Error("broadcast", "err", err)
			}
		}
	}
}

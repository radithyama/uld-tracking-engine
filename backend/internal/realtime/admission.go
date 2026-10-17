package realtime

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/radithya/uld-engine/internal/httpx"
	"github.com/radithya/uld-engine/internal/ratelimit"
	"github.com/radithya/uld-engine/internal/secevent"
)

// Admission decides whether a WebSocket upgrade may proceed. It runs before
// the upgrade, so refused connections cost one small HTTP response.
type Admission struct {
	Log     *slog.Logger
	Origins []string           // exact allowed browser origins
	Upgrade *ratelimit.Keyed   // upgrade attempts per client IP
	Conns   *ratelimit.Conns   // concurrent sockets per client IP and in total
	Report  *secevent.Throttle // one SIEM event per source per window
}

func (a *Admission) emit(r *http.Request, rule secevent.Rule, reason, ip string, attrs ...slog.Attr) {
	if !a.Report.Allow(rule.ID + "|" + ip) {
		return
	}
	attrs = append(attrs,
		slog.Group("source", slog.String("ip", ip)),
		slog.Group("url", slog.String("path", secevent.Clean(r.URL.Path, 128))),
		slog.Group("user_agent", slog.String("original", secevent.Clean(r.UserAgent(), 256))))
	secevent.Emit(r.Context(), a.Log, rule, "failure", reason, attrs...)
}

// Precheck is middleware for the cheap checks (origin and upgrade rate). It
// runs before authentication, so a flood never reaches the ticket store and
// a refused upgrade never burns a single-use ticket.
func (a *Admission) Precheck(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.precheck(w, r, httpx.ClientIP(r)) {
			next.ServeHTTP(w, r)
		}
	})
}

func (a *Admission) precheck(w http.ResponseWriter, r *http.Request, ip string) bool {
	// Cross-site WebSocket hijacking: browsers always send Origin on a
	// WebSocket upgrade and page scripts can't forge it, so an exact
	// allow-list stops other sites from opening sockets with a visitor's
	// browser. (Non-browser clients omit Origin; they still need a ticket.)
	if o := r.Header.Get("Origin"); o != "" && !httpx.OriginAllowed(a.Origins, o) {
		a.emit(r, secevent.WSOriginRejected, "origin_not_allowed", ip, slog.String("http.request.origin", secevent.Clean(o, 128)))
		httpx.WriteError(w, http.StatusForbidden, "origin not allowed", nil)
		return false
	}
	if !a.Upgrade.Allow(ip) {
		a.emit(r, secevent.WSConnLimited, "upgrade_rate", ip)
		w.Header().Set("Retry-After", "5")
		httpx.WriteError(w, http.StatusTooManyRequests, "too many connection attempts", nil)
		return false
	}
	return true
}

// Acquire reserves a connection slot for the request's client. On success it
// returns a release func to call when the connection ends; otherwise it has
// already written the HTTP error.
func (a *Admission) Acquire(w http.ResponseWriter, r *http.Request) (release func(), ok bool) {
	ip := httpx.ClientIP(r)
	release, reason := a.Conns.Acquire(ip)
	if release == nil {
		a.emit(r, secevent.WSConnLimited, "concurrent_"+reason, ip, slog.Int("ws.active_connections", a.Conns.Active()))
		w.Header().Set("Retry-After", strconv.Itoa(30))
		httpx.WriteError(w, http.StatusServiceUnavailable, "connection limit reached", nil)
		return nil, false
	}
	return release, true
}

// CoalesceSnapshots wraps fn so a burst of new connections shares a few
// snapshot queries instead of running one each (otherwise a connection flood
// is amplified into Redis load). At most one fetch runs at a time; callers
// that arrive while it runs share the next one.
//
// A caller never reuses a fetch that started before it called. Handler
// registers the client before asking for the snapshot, so the fetch it gets
// reflects every update published before registration, and every later
// update is already queued for the client: there is never a gap.
func CoalesceSnapshots(fn SnapshotFunc) SnapshotFunc {
	type call struct {
		done chan struct{}
		b    []byte
		err  error
	}
	var (
		mu            sync.Mutex
		running, next *call
	)
	run := func(c *call) {
		for c != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			c.b, c.err = fn(ctx)
			cancel()
			close(c.done)
			mu.Lock()
			running, next = next, nil
			c = running
			mu.Unlock()
		}
	}
	return func(ctx context.Context) ([]byte, error) {
		mu.Lock()
		var c *call
		switch {
		case running == nil:
			c = &call{done: make(chan struct{})}
			running = c
			mu.Unlock()
			go run(c)
		case next == nil:
			c = &call{done: make(chan struct{})}
			next = c
			mu.Unlock()
		default:
			c = next
			mu.Unlock()
		}
		select {
		case <-c.done:
			return c.b, c.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

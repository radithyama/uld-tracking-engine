package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/radithya/uld-engine/internal/httpx"
	"github.com/radithya/uld-engine/internal/model"
	"github.com/radithya/uld-engine/internal/ratelimit"
	"github.com/radithya/uld-engine/internal/secevent"
	"github.com/radithya/uld-engine/internal/store"
)

// limits throttles requests per source IP (before authentication, so floods
// and brute force are cheap to refuse) and per API key (after it, so one
// compromised or misbehaving scanner can't starve the rest).
type limits struct {
	log    *slog.Logger
	perIP  *ratelimit.Keyed
	perKey *ratelimit.Keyed
	brute  *bruteForceTracker
	report *secevent.Throttle
}

func (l *limits) refuse(w http.ResponseWriter, r *http.Request, scope, key string, retryAfter time.Duration) {
	if l.report.Allow(scope + "|" + key) {
		secevent.Emit(r.Context(), l.log, secevent.RateLimited, "failure", scope,
			append(requestAttrs(r, nil), slog.String("rate_limit.key", secevent.Clean(key, 64)))...)
	}
	w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())+1))
	httpx.WriteError(w, http.StatusTooManyRequests, "rate limit exceeded", nil)
}

// PerIP runs before authentication. A source that has crossed the
// brute-force threshold is refused for the rest of the window without its
// credentials even being checked.
func (l *limits) PerIP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := httpx.ClientIP(r)
		if l.brute.blocked(ip) {
			l.refuse(w, r, "auth_failures", ip, l.brute.window)
			return
		}
		if !l.perIP.Allow(ip) {
			l.refuse(w, r, "per_ip", ip, time.Second)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// PerKey runs after authentication.
func (l *limits) PerKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id := principalFrom(r.Context()).KeyID; !l.perKey.Allow(id) {
			l.refuse(w, r, "per_api_key", id, time.Second)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// replayGuard makes event_id an idempotency key bound to a SHA-256 digest of
// the event's canonical payload, for a window as long as model.MaxEventAge:
//
//   - same id, same payload: an honest retry. Acknowledged, not republished.
//   - same id, different payload: someone is replaying a captured event with
//     altered contents (e.g. a new location or weight). Rejected and alerted.
//   - older than the window: rejected by validation (model.MaxEventAge).
//
// The processor also deduplicates on event_id (the first write wins), so
// this guard is about refusing tampered replays at the edge, keeping them out
// of Kafka, and telling the SOC. If Redis is unavailable it fails open and
// the processor's deduplication remains the backstop.
type replayGuard struct {
	log    *slog.Logger
	rdb    *store.Redis
	window time.Duration
	// After a Redis error, skip checks briefly so an outage doesn't add a
	// timeout to every request.
	pausedUntil atomic.Int64
}

// digest hashes the normalized event. A timestamp the server filled in is
// excluded, since a retry of a request without one gets a new default.
func digest(ev model.ScanEvent, clientTimestamp bool) string {
	if !clientTimestamp {
		ev.Timestamp = time.Time{}
	}
	b, _ := json.Marshal(ev)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

type replayVerdict struct {
	duplicate []bool // honest retries: skip
	altered   []int  // indexes replayed with a different payload
}

func (g *replayGuard) check(ctx context.Context, ids, digests []string) replayVerdict {
	v := replayVerdict{duplicate: make([]bool, len(ids))}
	if g == nil || time.Now().UnixNano() < g.pausedUntil.Load() {
		return v
	}
	seen, err := g.rdb.EventDigests(ctx, ids)
	if err != nil {
		g.pausedUntil.Store(time.Now().Add(10 * time.Second).UnixNano())
		g.log.Warn("replay check unavailable for 10s; relying on processor dedup", "err", err)
		return v
	}
	for i, d := range seen {
		switch {
		case d == "":
		case d == digests[i]:
			v.duplicate[i] = true
		default:
			v.altered = append(v.altered, i)
		}
	}
	return v
}

func (g *replayGuard) mark(ctx context.Context, ids, digests []string) {
	if g == nil || len(ids) == 0 || time.Now().UnixNano() < g.pausedUntil.Load() {
		return
	}
	if err := g.rdb.MarkEvents(context.WithoutCancel(ctx), ids, digests, g.window); err != nil {
		g.log.Warn("could not record accepted event ids", "err", err)
	}
}

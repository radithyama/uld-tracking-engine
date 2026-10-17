// Package ratelimit provides per-key request rate limits and concurrent
// connection limits with bounded memory, for protecting public endpoints
// from brute force and resource exhaustion.
package ratelimit

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Keyed is a set of token buckets, one per key (client IP, API key id, ...).
//
// Memory is bounded: idle buckets are swept, and when maxKeys distinct keys
// are active at once, new keys are refused rather than tracked. That sheds
// load under a wide distributed flood instead of exhausting memory.
type Keyed struct {
	mu      sync.Mutex
	limit   rate.Limit
	burst   int
	maxKeys int
	idle    time.Duration
	m       map[string]*entry
	sweptAt time.Time
}

type entry struct {
	lim  *rate.Limiter
	seen time.Time
}

// NewKeyed allows perSecond sustained requests per key with the given burst.
func NewKeyed(perSecond float64, burst, maxKeys int) *Keyed {
	return &Keyed{
		limit:   rate.Limit(perSecond),
		burst:   burst,
		maxKeys: maxKeys,
		idle:    10 * time.Minute,
		m:       make(map[string]*entry),
	}
}

// Allow reports whether a request for key may proceed now.
func (k *Keyed) Allow(key string) bool {
	now := time.Now()
	k.mu.Lock()
	defer k.mu.Unlock()
	e := k.m[key]
	if e == nil {
		if len(k.m) >= k.maxKeys || now.Sub(k.sweptAt) > time.Minute {
			k.sweepLocked(now)
		}
		if len(k.m) >= k.maxKeys {
			return false
		}
		e = &entry{lim: rate.NewLimiter(k.limit, k.burst)}
		k.m[key] = e
	}
	e.seen = now
	return e.lim.AllowN(now, 1)
}

func (k *Keyed) sweepLocked(now time.Time) {
	k.sweptAt = now
	for key, e := range k.m {
		// A bucket that has refilled completely carries no state worth keeping.
		if now.Sub(e.seen) > k.idle || e.lim.TokensAt(now) >= float64(k.burst) {
			delete(k.m, key)
		}
	}
}

// Conns caps concurrent connections per key and in total.
type Conns struct {
	mu     sync.Mutex
	perKey int
	total  int
	m      map[string]int
	n      int
}

func NewConns(perKey, total int) *Conns {
	return &Conns{perKey: perKey, total: total, m: make(map[string]int)}
}

// Acquire reserves a connection slot for key. On success it returns a
// release function that must be called exactly once when the connection
// ends; reason is "per_key" or "total" when the slot is refused.
func (c *Conns) Acquire(key string) (release func(), reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case c.n >= c.total:
		return nil, "total"
	case c.m[key] >= c.perKey:
		return nil, "per_key"
	}
	c.m[key]++
	c.n++
	var once sync.Once
	return func() {
		once.Do(func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			c.n--
			if c.m[key]--; c.m[key] <= 0 {
				delete(c.m, key)
			}
		})
	}, ""
}

// Active returns the number of open connections.
func (c *Conns) Active() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

package secevent

import (
	"sync"
	"time"
)

// Throttle limits how often an event fires per key (e.g. rule + source IP),
// so a flood produces one event per window instead of one per request and
// can't itself flood the SIEM or the live security stream.
type Throttle struct {
	mu    sync.Mutex
	every time.Duration
	last  map[string]time.Time
}

func NewThrottle(every time.Duration) *Throttle {
	return &Throttle{every: every, last: make(map[string]time.Time)}
}

// Allow reports whether an event for key may be emitted now.
func (t *Throttle) Allow(key string) bool {
	now := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()
	if prev, ok := t.last[key]; ok && now.Sub(prev) < t.every {
		return false
	}
	if len(t.last) >= 10000 { // bound memory under a distributed flood
		for k, at := range t.last {
			if now.Sub(at) >= t.every {
				delete(t.last, k)
			}
		}
		if len(t.last) >= 10000 {
			return false
		}
	}
	t.last[key] = now
	return true
}

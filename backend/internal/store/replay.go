package store

import (
	"context"
	"time"
)

const (
	keyTicketPrefix = "uld:ws:ticket:"  // used WebSocket ticket ids
	keyEventPrefix  = "uld:ingest:evt:" // event ids accepted by ingest
)

// claimOnce atomically records key for ttl and reports whether it was new.
func (r *Redis) claimOnce(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return r.rdb.SetNX(ctx, key, 1, ttl).Result()
}

// TicketStore makes WebSocket tickets single-use across every realtime
// instance (it implements authz.TicketStore).
type TicketStore struct{ r *Redis }

func (r *Redis) Tickets() TicketStore { return TicketStore{r} }

func (t TicketStore) Claim(ctx context.Context, jti string, ttl time.Duration) (bool, error) {
	return t.r.claimOnce(ctx, keyTicketPrefix+jti, ttl)
}

// EventDigests returns the payload digest recorded for each event id, or ""
// for ids not seen within the replay window.
func (r *Redis) EventDigests(ctx context.Context, eventIDs []string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	keys := make([]string, len(eventIDs))
	for i, id := range eventIDs {
		keys[i] = keyEventPrefix + id
	}
	vals, err := r.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}
	out := make([]string, len(vals))
	for i, v := range vals {
		out[i], _ = v.(string)
	}
	return out, nil
}

// MarkEvents records the payload digest of each accepted event for the
// replay window. Ingest calls it only after a successful publish, so a
// client retrying a failed request is never mistaken for a replay. The
// first digest recorded for an id wins.
func (r *Redis) MarkEvents(ctx context.Context, eventIDs, digests []string, window time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	pipe := r.rdb.Pipeline()
	for i, id := range eventIDs {
		pipe.SetNX(ctx, keyEventPrefix+id, digests[i], window)
	}
	_, err := pipe.Exec(ctx)
	return err
}

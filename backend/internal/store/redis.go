package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/redis/go-redis/v9"

	"github.com/radithya/uld-engine/internal/model"
)

const (
	KeyULDLive    = "uld:live"    // hash: uld id        -> ULDState JSON
	KeyFlightLive = "flight:live" // hash: flight number -> FlightLoad JSON
	ChannelEvents = "uld:events"  // pub/sub: Envelope JSON
)

// upsertAndPublish atomically (1) writes the hash field only if the incoming
// version is >= the stored one and (2) publishes the change. Doing both in one
// script guarantees subscribers never see a message whose state is not yet in
// the hash, and that a slow writer can never regress newer state.
//
// KEYS[1] hash key, KEYS[2] channel
// ARGV[1] field, ARGV[2] state JSON, ARGV[3] version, ARGV[4] envelope JSON
var upsertAndPublish = redis.NewScript(`
local cur = redis.call('HGET', KEYS[1], ARGV[1])
if cur then
  local ok, decoded = pcall(cjson.decode, cur)
  if ok and decoded.version and tonumber(decoded.version) > tonumber(ARGV[3]) then
    return 0
  end
end
redis.call('HSET', KEYS[1], ARGV[1], ARGV[2])
redis.call('PUBLISH', KEYS[2], ARGV[4])
return 1
`)

type Redis struct {
	rdb *redis.Client
}

func NewRedis(ctx context.Context, addr string) (*Redis, error) {
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		return nil, err
	}
	return &Redis{rdb: rdb}, nil
}

func (r *Redis) Client() *redis.Client { return r.rdb }
func (r *Redis) Close() error          { return r.rdb.Close() }

func (r *Redis) put(ctx context.Context, key, field string, version int64, msgType string, state any) error {
	stateJSON, err := json.Marshal(state)
	if err != nil {
		return err
	}
	envJSON, err := json.Marshal(model.Envelope{Type: msgType, Data: json.RawMessage(stateJSON)})
	if err != nil {
		return err
	}
	return upsertAndPublish.Run(ctx, r.rdb,
		[]string{key, ChannelEvents}, field, stateJSON, version, envJSON).Err()
}

func (r *Redis) PutULD(ctx context.Context, s model.ULDState) error {
	return r.put(ctx, KeyULDLive, s.ID, s.Version, model.MsgULDUpdate, s)
}

func (r *Redis) PutFlight(ctx context.Context, f model.FlightLoad) error {
	return r.put(ctx, KeyFlightLive, f.FlightNumber, f.Version, model.MsgFlightUpdate, f)
}

// Snapshot returns the raw JSON of every live ULD and flight, without
// decoding, so the realtime server can splice it straight into a message.
func (r *Redis) Snapshot(ctx context.Context) (ulds, flights []json.RawMessage, err error) {
	pipe := r.rdb.Pipeline()
	uCmd := pipe.HVals(ctx, KeyULDLive)
	fCmd := pipe.HVals(ctx, KeyFlightLive)
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, nil, fmt.Errorf("redis snapshot: %w", err)
	}
	return toRaw(uCmd.Val()), toRaw(fCmd.Val()), nil
}

func toRaw(vals []string) []json.RawMessage {
	out := make([]json.RawMessage, len(vals))
	for i, v := range vals {
		out[i] = json.RawMessage(v)
	}
	return out
}

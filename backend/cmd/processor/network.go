package main

import (
	"context"
	"slices"
	"time"

	"github.com/radithya/uld-engine/internal/store"
)

// runNetworkClock advances the flight network (departures, arrivals,
// rotations, rebooking, sandbox purge) and projects every change into Redis.
// Safe to run in every processor replica: a Postgres advisory lock elects one
// leader per tick.
func (p *Processor) runNetworkClock(ctx context.Context, every time.Duration, scale float64, sandboxTTL time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	lastSweep := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		ch, leader, err := p.pg.NetworkTick(ctx, time.Now().UTC(), scale, sandboxTTL)
		if err != nil {
			if ctx.Err() == nil {
				p.log.Error("network tick failed", "err", err)
			}
			continue
		}
		if !leader {
			continue
		}
		if len(ch.Departed)+len(ch.Arrived)+ch.Rebooked+len(ch.DeletedULD) > 0 {
			p.log.Info("network clock", "departed", ch.Departed, "arrived", ch.Arrived,
				"ulds_moved", len(ch.ULDs), "rebooked", ch.Rebooked, "sandbox_purged", len(ch.DeletedULD))
		}
		if err := p.projectNetwork(ctx, ch); err != nil && ctx.Err() == nil {
			p.log.Warn("network projection failed; next warm-up will repair", "err", err)
		}
		if time.Since(lastSweep) > 30*time.Second {
			lastSweep = time.Now()
			if err := p.sweepFlights(ctx); err != nil && ctx.Err() == nil {
				p.log.Warn("flight sweep failed", "err", err)
			}
		}
	}
}

func (p *Processor) projectNetwork(ctx context.Context, ch store.NetworkChanges) error {
	slices.Sort(ch.ULDs)
	for _, id := range slices.Compact(ch.ULDs) {
		st, err := p.pg.ULDState(ctx, id)
		if err != nil {
			return err
		}
		if err := p.cache.PutULD(ctx, st); err != nil {
			return err
		}
	}
	for _, id := range ch.DeletedULD {
		if err := p.cache.RemoveULD(ctx, id); err != nil {
			return err
		}
	}
	if len(ch.Flights) > 0 {
		loads, err := p.pg.FlightLoads(ctx, ch.Flights)
		if err != nil {
			return err
		}
		for _, f := range loads {
			if err := p.cache.PutFlight(ctx, f); err != nil {
				return err
			}
		}
	}
	return nil
}

// sweepFlights drops flights that arrived long ago from the live projection
// (they stay in Postgres as history).
func (p *Processor) sweepFlights(ctx context.Context) error {
	active, err := p.pg.FlightLoads(ctx, nil)
	if err != nil {
		return err
	}
	keep := map[int64]bool{}
	for _, f := range active {
		keep[f.FlightID] = true
	}
	ids, err := p.cache.FlightIDs(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if !keep[id] {
			if err := p.cache.RemoveFlight(ctx, id); err != nil {
				return err
			}
		}
	}
	return nil
}

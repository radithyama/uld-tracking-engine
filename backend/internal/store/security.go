package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"

	"github.com/radithya/uld-engine/internal/model"
	"github.com/radithya/uld-engine/internal/secevent"
)

const (
	// SecurityStream durably buffers security events between the emitting
	// services (ingest, processor) and realtime, which persists them.
	SecurityStream = "uld:security:stream"
	// SecurityChannel fans persisted events and status changes out to every
	// realtime instance's /ws/security clients.
	SecurityChannel = "uld:security"
	securityGroup   = "realtime"
	streamMaxLen    = 10000
)

// ---------------------------------------------------------------- producer

// SecurityPublisher forwards secevent records to the Redis stream from a
// background goroutine, so emitting a security event never blocks a request
// or a Kafka worker. If Redis is down the buffer fills and records are
// dropped (and counted); stdout logs remain the system of record for the SIEM.
type SecurityPublisher struct {
	rdb     *redis.Client
	log     *slog.Logger
	ch      chan secevent.Record
	Dropped atomic.Int64
}

func NewSecurityPublisher(r *Redis, log *slog.Logger) *SecurityPublisher {
	return &SecurityPublisher{rdb: r.rdb, log: log, ch: make(chan secevent.Record, 2048)}
}

func (p *SecurityPublisher) Submit(rec secevent.Record) {
	select {
	case p.ch <- rec:
	default:
		p.Dropped.Add(1)
	}
}

func (p *SecurityPublisher) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case rec := <-p.ch:
			b, err := json.Marshal(rec)
			if err != nil {
				continue
			}
			for attempt := 0; ; attempt++ {
				err = p.rdb.XAdd(ctx, &redis.XAddArgs{
					Stream: SecurityStream, MaxLen: streamMaxLen, Approx: true,
					Values: map[string]any{"data": b},
				}).Err()
				if err == nil || ctx.Err() != nil || attempt >= 3 {
					break
				}
				time.Sleep(time.Duration(200*(attempt+1)) * time.Millisecond)
			}
			if err != nil && ctx.Err() == nil {
				p.Dropped.Add(1)
				p.log.Warn("security event not streamed", "err", err, "rule", rec.RuleID)
			}
		}
	}
}

// ---------------------------------------------------------------- consumer

// ConsumeSecurity reads the stream as a member of the shared consumer group
// and calls handle for each record; a record is acknowledged only after
// handle succeeds. Pending records left by a crash are re-processed first.
func (r *Redis) ConsumeSecurity(ctx context.Context, consumer string, log *slog.Logger, handle func(secevent.Record) error) {
	if err := r.rdb.XGroupCreateMkStream(ctx, SecurityStream, securityGroup, "0").Err(); err != nil &&
		!strings.Contains(err.Error(), "BUSYGROUP") {
		log.Error("create security consumer group", "err", err)
	}
	start := "0" // own pending entries first
	for ctx.Err() == nil {
		res, err := r.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group: securityGroup, Consumer: consumer, Streams: []string{SecurityStream, start},
			Count: 100, Block: 5 * time.Second,
		}).Result()
		if errors.Is(err, redis.Nil) {
			start = ">"
			continue
		}
		if err != nil {
			if ctx.Err() == nil {
				log.Warn("security stream read failed", "err", err)
				time.Sleep(2 * time.Second)
			}
			continue
		}
		n := 0
		failed := false
		for _, s := range res {
			for _, msg := range s.Messages {
				n++
				var rec secevent.Record
				raw, _ := msg.Values["data"].(string)
				if err := json.Unmarshal([]byte(raw), &rec); err != nil || rec.ID == "" {
					log.Warn("skipping malformed security record", "id", msg.ID)
					_ = r.rdb.XAck(ctx, SecurityStream, securityGroup, msg.ID).Err()
					continue
				}
				if err := handle(rec); err != nil {
					log.Warn("security record not persisted; will retry", "err", err)
					failed = true
					break
				}
				_ = r.rdb.XAck(ctx, SecurityStream, securityGroup, msg.ID).Err()
			}
		}
		switch {
		case failed:
			start = "0"
			time.Sleep(2 * time.Second)
		case n == 0 && start == "0":
			start = ">"
		}
	}
}

// PublishSecurity fans a message out to every realtime instance.
func (r *Redis) PublishSecurity(ctx context.Context, msgType string, data any) error {
	b, err := json.Marshal(model.Envelope{Type: msgType, Data: data})
	if err != nil {
		return err
	}
	return r.rdb.Publish(ctx, SecurityChannel, b).Err()
}

// ---------------------------------------------------------------- postgres

type SecurityAlert struct {
	ID         string          `json:"id"`
	OccurredAt time.Time       `json:"occurred_at"`
	RuleID     string          `json:"rule_id"`
	RuleName   string          `json:"rule_name"`
	Action     string          `json:"action"`
	Kind       string          `json:"kind"`
	Severity   int             `json:"severity"`
	Service    string          `json:"service"`
	Reason     string          `json:"reason"`
	SourceIP   *string         `json:"source_ip"`
	Principal  *string         `json:"principal"`
	ULDID      *string         `json:"uld_id"`
	Details    json.RawMessage `json:"details"`
	Status     string          `json:"status"`
	AckedBy    *string         `json:"acked_by"`
	AckedAt    *time.Time      `json:"acked_at"`
	ResolvedBy *string         `json:"resolved_by"`
	ResolvedAt *time.Time      `json:"resolved_at"`
	Note       *string         `json:"note"`
}

const alertCols = `id::text, occurred_at, rule_id, rule_name, action, kind, severity, service, reason,
	source_ip, principal, uld_id, details, status, acked_by, acked_at, resolved_by, resolved_at, note`

func scanAlert(row pgx.Row) (SecurityAlert, error) {
	var a SecurityAlert
	err := row.Scan(&a.ID, &a.OccurredAt, &a.RuleID, &a.RuleName, &a.Action, &a.Kind, &a.Severity, &a.Service,
		&a.Reason, &a.SourceIP, &a.Principal, &a.ULDID, &a.Details, &a.Status, &a.AckedBy, &a.AckedAt,
		&a.ResolvedBy, &a.ResolvedAt, &a.Note)
	return a, err
}

func dig(m map[string]any, path ...string) *string {
	var cur any = m
	for _, p := range path {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = mm[p]
	}
	if s, ok := cur.(string); ok && s != "" {
		return &s
	}
	return nil
}

// InsertSecurityAlert stores a record; inserted is false for redeliveries.
func (p *Postgres) InsertSecurityAlert(ctx context.Context, rec secevent.Record) (SecurityAlert, bool, error) {
	details, _ := json.Marshal(rec.Details)
	a, err := scanAlert(p.pool.QueryRow(ctx, `
		INSERT INTO security_alerts (id, occurred_at, rule_id, rule_name, action, kind, severity, service, reason,
		                             source_ip, principal, uld_id, details)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT (id) DO NOTHING
		RETURNING `+alertCols,
		rec.ID, rec.Time, rec.RuleID, rec.RuleName, rec.Action, rec.Kind, rec.Severity, rec.Service, rec.Reason,
		dig(rec.Details, "source", "ip"), dig(rec.Details, "user", "name"), dig(rec.Details, "uld", "id"), details))
	if errors.Is(err, pgx.ErrNoRows) {
		return SecurityAlert{}, false, nil
	}
	return a, err == nil, err
}

func (p *Postgres) ListSecurityAlerts(ctx context.Context, status string, limit int) ([]SecurityAlert, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := p.pool.Query(ctx, `SELECT `+alertCols+` FROM security_alerts
		WHERE $1 = '' OR status = $1 ORDER BY occurred_at DESC LIMIT $2`, status, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (SecurityAlert, error) { return scanAlert(r) })
}

var ErrInvalidTransition = errors.New("invalid status transition")

// TransitionAlert moves an alert OPEN -> ACKNOWLEDGED or (OPEN|ACKNOWLEDGED) -> RESOLVED.
func (p *Postgres) TransitionAlert(ctx context.Context, id, to, actor, note string) (SecurityAlert, error) {
	var q string
	switch to {
	case "ACKNOWLEDGED":
		q = `UPDATE security_alerts SET status = 'ACKNOWLEDGED', acked_by = $2, acked_at = now(),
		       note = COALESCE(NULLIF($3, ''), note)
		     WHERE id = $1::uuid AND status = 'OPEN' RETURNING ` + alertCols
	case "RESOLVED":
		q = `UPDATE security_alerts SET status = 'RESOLVED', resolved_by = $2, resolved_at = now(),
		       acked_by = COALESCE(acked_by, $2), acked_at = COALESCE(acked_at, now()),
		       note = COALESCE(NULLIF($3, ''), note)
		     WHERE id = $1::uuid AND status IN ('OPEN', 'ACKNOWLEDGED') RETURNING ` + alertCols
	default:
		return SecurityAlert{}, ErrInvalidTransition
	}
	a, err := scanAlert(p.pool.QueryRow(ctx, q, id, actor, note))
	if errors.Is(err, pgx.ErrNoRows) {
		return a, ErrInvalidTransition
	}
	return a, err
}

type AuditEntry struct {
	ID      int64           `json:"id"`
	At      time.Time       `json:"at"`
	Actor   string          `json:"actor"`
	Role    string          `json:"role"`
	Action  string          `json:"action"`
	Target  string          `json:"target"`
	Details json.RawMessage `json:"details"`
}

func (p *Postgres) InsertAudit(ctx context.Context, actor, role, action, target string, details any) (AuditEntry, error) {
	d, err := json.Marshal(details)
	if err != nil {
		return AuditEntry{}, fmt.Errorf("audit details: %w", err)
	}
	var e AuditEntry
	err = p.pool.QueryRow(ctx, `INSERT INTO audit_log (actor, role, action, target, details)
		VALUES ($1, $2, $3, $4, $5) RETURNING id, at, actor, role, action, target, details`,
		actor, role, action, target, d).Scan(&e.ID, &e.At, &e.Actor, &e.Role, &e.Action, &e.Target, &e.Details)
	return e, err
}

func (p *Postgres) ListAudit(ctx context.Context, limit int) ([]AuditEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := p.pool.Query(ctx, `SELECT id, at, actor, role, action, target, details
		FROM audit_log ORDER BY at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (AuditEntry, error) {
		var e AuditEntry
		err := r.Scan(&e.ID, &e.At, &e.Actor, &e.Role, &e.Action, &e.Target, &e.Details)
		return e, err
	})
}

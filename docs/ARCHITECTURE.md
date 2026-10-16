# Architecture & Developer Reference

Deep-dive companion to the [README](../README.md): data flow, API contract,
concurrency model and delivery guarantees.

```
 scanners ──HTTP──▶ ingest ──▶ Kafka: uld.telemetry (6 partitions, key = ULD id)
                                      │
                                      ▼
                     processor (consumer group, N members)
                       │  1. tx: event log + ULD state ──▶ PostgreSQL (system of record)
                       │  2. version-guarded HSET + PUBLISH (atomic Lua) ──▶ Redis
                       └─ poison messages ──▶ Kafka: uld.telemetry.dlq
                                                         │ pub/sub "uld:events"
                                                         ▼
                                   realtime (WebSocket hub) ──▶ Next.js dashboard
```

## Directory structure

```
.
├── docker-compose.yml          Postgres, Redis, Kafka (KRaft), topic init, services
├── Makefile
├── backend/                    Go module github.com/radithya/uld-engine
│   ├── Dockerfile              one recipe, --build-arg SERVICE=<cmd>
│   ├── config/api-keys.dev.json  API key hashes, scopes, revocation (dev)
│   ├── migrations/
│   │   ├── 001_schema.sql      aircrafts, flights, warehouse_locations, ulds, uld_scan_events
│   │   ├── 002_seed.sql        3 aircraft/flights, 6x5 warehouse grid, 20 ULDs
│   │   └── 003_security.sql    airports/stations, outstation locations, security_flags
│   ├── cmd/
│   │   ├── ingest/             Telemetry Ingestion API  → Kafka producer
│   │   ├── processor/          Stream processor         → Postgres + Redis
│   │   ├── realtime/           WebSocket server         ← Redis pub/sub
│   │   ├── simulator/          Synthetic scanner traffic (at-least-once client + ledger)
│   │   └── verify/             Chaos-run reconciliation (loss / dupes / order / projection)
│   └── internal/
│       ├── model/              ScanEvent (+validation), ULDState, FlightLoad, Envelope
│       ├── store/              postgres.go (ApplyScan, queries), redis.go (Lua upsert+publish)
│       ├── realtime/           hub.go (fan-out), client.go (read/write pumps)
│       ├── spool/              durable FIFO disk buffer used while Kafka is down
│       ├── secevent/           ECS security events + rule catalogue
│       ├── detect/             impossible travel, weight-integrity detections
│       ├── kafkax/             tuned kafka-go writer/reader
│       ├── httpx/ config/ logx/
├── scripts/                    verify-stack.sh, chaos-test.sh, security-smoke.sh
├── docs/RUNBOOK.md             Phase 1-3 operations guide
└── frontend/                   Next.js 16 (App Router) + Tailwind v4
    ├── app/page.tsx            Dashboard
    ├── lib/useUldStream.ts     WebSocket hook: batching, reconnect, version merge
    └── components/             WarehouseGrid, UldTable, FlightLoads, AlertsPanel
```

> **Operations:** stack validation, Kafka chaos testing and the SOC security
> telemetry (rule catalogue, ECS log schema, SIEM shipping) are documented in
> [RUNBOOK.md](RUNBOOK.md).

## Quick start

Requires Docker (with Compose v2), Node 20+, `jq`.

```bash
docker compose up -d --build --wait                  # infra + ingest/processor/realtime
docker compose --profile sim up -d --wait simulator  # optional: 30 scans/sec of fake traffic
scripts/verify-stack.sh                              # end-to-end health check
cd frontend && cp .env.example .env.local && npm install && npm run dev
```

Open http://localhost:3000.

| Service   | Port  | Endpoints |
|-----------|-------|-----------|
| ingest    | 8080  | `POST /v1/scans` (API key), `GET /healthz`, `/readyz`, `/metrics` |
| processor | 9090  | `GET /healthz`, `/readyz` (Kafka reachable), `/metrics` |
| realtime  | 8081  | `GET /ws` (WebSocket), `GET /api/snapshot`, `GET /healthz` |
| simulator | 8090  | `GET /stats`, `GET /sent` (ledger), `POST /pause`, `POST /resume` |
| Postgres  | 5432  | `uld` / `uld` / db `uld` |
| Redis     | 6379  | |
| Kafka     | 29092 | host listener (containers use `kafka:9092`) |

The frontend was scaffolded with:

```bash
npx create-next-app@latest frontend --ts --tailwind --eslint --app --no-src-dir --import-alias "@/*" --use-npm
```

## Ingestion API

`POST /v1/scans` requires `Authorization: Bearer <api-key>` (or `X-API-Key`)
with the `scans:write` scope. It takes one scan object or an array of up to
500. The whole batch is validated first, then accepted or rejected as one unit.

```json
{
  "event_id": "optional UUID — send one so client retries are idempotent",
  "uld_id": "AKE10001GA",
  "scanner_id": "SCN-STG-01",
  "scan_type": "RFID",
  "weight_kg": 1420.5,
  "location_id": "STA-02",
  "flight_number": "QZ7510",
  "status": "optional: IN_TRANSIT | STAGED | LOADED (otherwise implied by location)",
  "timestamp": "optional RFC3339, defaults to now"
}
```

Responses: `202` with the assigned event ids and `durability` (`kafka`, or
`spool` while Kafka is down: the scan is fsynced to disk and replayed later).
`400` for malformed JSON. `401`/`403` for authentication and authorization
failures. `422` with errors for each invalid item. `503` only if Kafka is down
*and* the spool is full (retry with the same `event_id`).

## Weight alerts

* **ULD overweight.** `weight > min(ULD max gross rating, aircraft per-position limit)`.
  An AKE container is rated for 1,588 kg, but on the B737 freighter (QZ7510)
  each position is limited to 1,500 kg. A 1,550 kg AKE assigned to that flight
  is flagged even though the container itself is within its rating.
* **Flight over payload.** The total weight of every ULD assigned to the flight
  is greater than the aircraft's max payload.

The processor computes both and pushes them with each update. The dashboard
shows them in a banner, an alerts panel, red map cells and table rows, and on
the flight load cards.

## Concurrency and delivery guarantees

**Per-ULD ordering.** Messages are keyed by ULD id with a murmur2 partitioner,
so every scan for a given ULD goes to the same partition. The processor runs
`PROCESSOR_WORKERS` kafka-go Readers in one consumer group, one per goroutine.
Kafka gives each partition to exactly one member, which processes it in order.
Different partitions run in parallel, and no goroutines share a Reader.

**At-least-once delivery, made safe.**
* Offsets are committed only after Postgres and Redis are both updated, or
  after the message is safely written to the DLQ.
* `uld_scan_events.event_id` is the primary key, so a redelivered event becomes
  a no-op in Postgres. It is still re-projected to Redis, in case the earlier
  attempt crashed between the two writes.
* An out-of-order event (older than `last_scan_at`) is logged but does not
  rewind the ULD's current state.
* Transient failures are retried with capped exponential backoff. Permanent
  failures go to `uld.telemetry.dlq` with the error in a header: invalid
  payloads, unknown location or flight, constraint violations.

**Redis projection.** A Lua script does `HSET` then `PUBLISH` atomically, and
only if the incoming `version` is ≥ the stored one. Subscribers never get a
message whose state isn't in the hash yet, and a slow writer can't overwrite
newer state. On startup the processor rebuilds the projection from Postgres.

**WebSocket hub** (`internal/realtime`).
* The client set belongs to a single `Hub.Run` goroutine and changes only
  through the register, unregister and broadcast channels, so it needs no mutex.
* Only the hub sends on or closes a client's `send` channel. That rules out
  the "send on closed channel" panic.
* Each connection has one `readPump` (control frames and dead-peer detection)
  and one `writePump`, its only writer. This matches gorilla/websocket's
  limit of one reader and one writer per connection.
* A slow client is disconnected rather than blocking the others. It reconnects
  and resyncs from a snapshot.
* Each broadcast frame is encoded once (`PreparedMessage`) and reused for every
  client.
* A new client registers before its snapshot is read. Updates that arrive
  during the read are buffered, so none fall in the gap. The browser merges by
  `version` (highest wins), so a message delivered twice is harmless.

**Dashboard.** Socket messages are buffered and flushed into React state every
100 ms. A burst of thousands of scans costs a few renders. Reconnects use
exponential backoff with jitter.

## Development

```bash
make test     # go test -race ./...
make lint     # go vet + eslint
make scan     # send a sample scan
make reset    # wipe the Postgres volume (re-applies migrations on next `up`)
```

To run a Go service on the host against the Compose infra, start it from
`backend/` with `go run ./cmd/<service>`. The defaults point at
`localhost:29092`, `localhost:5432` and `localhost:6379`.

## Deploying the dashboard to Vercel

Set `NEXT_PUBLIC_WS_URL=wss://<your-realtime-host>/ws` in the Vercel project.
Add the Vercel origin to `ALLOWED_ORIGINS` on the realtime service. The Go
services, Kafka, Postgres and Redis must run on infrastructure that supports
long-lived connections, not Vercel functions.

## Production next steps

* Partition `uld_scan_events` by month, and use a migration tool (goose,
  atlas) instead of init scripts.
* Add mTLS for scanners and authentication for the dashboard WebSocket. Load
  API keys from a secret store rather than a mounted file.
* Expose Prometheus metrics (consumer lag, DLQ rate, hub clients) alongside
  the JSON `/metrics` endpoints.
* Run Kafka with replication factor 3 and `min.insync.replicas=2`.

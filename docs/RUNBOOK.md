# Operations Runbook: Validation, Chaos Testing and SOC Telemetry

Run all commands from the repository root. You need Docker Engine with
Compose v2, plus `curl`, `jq` and `column` (`sudo apt install jq bsdextrautils`).

> **Dev credentials.** The API keys below exist only for local testing.
> `backend/config/api-keys.dev.json` stores their SHA-256 hashes, never the
> keys themselves.
>
> | Key | Principal | Scopes | Notes |
> |---|---|---|---|
> | `dev-cgk-scanner-fleet-key-7f3a9c2e` | cgk-scanner-fleet | scans:write | used by the simulator |
> | `dev-mel-scanner-fleet-key-41b8d0e5` | mel-scanner-fleet | scans:write | Melbourne scanners |
> | `dev-ops-dashboard-readonly-key-9e21` | ops-dashboard | scans:read | triggers authorization failures |
> | `dev-retired-handheld-key-0c55aa31` | retired-handheld-07 | scans:write | **revoked** |
>
> To add a key, generate the hash with `printf %s "$KEY" | sha256sum`. Put the
> hash in the keys file and restart `ingest`.

---

## Phase 1: Local stack validation

### Health checks

| Service | Check | Healthy when |
|---|---|---|
| postgres | `pg_isready` | accepts connections (schema is then applied by the one-shot `migrate` service, which processor and realtime wait for) |
| redis | `redis-cli ping` | it replies `PONG` |
| kafka | `kafka-broker-api-versions.sh` | the broker answers API requests |
| ingest / processor / realtime / simulator | `/app healthcheck http://127.0.0.1:<port>/healthz` | the process serves HTTP |

The Go images are distroless, so they contain no shell or curl. Instead, each
binary has a `healthcheck <url>` subcommand that Docker's health probe calls.

`depends_on` gates startup on these checks. Postgres and Redis must be
healthy, and topic creation must finish, before the processor starts. The
realtime service waits for a healthy processor, so Redis is warm before
dashboards connect. The simulator waits for a healthy ingest.

### Command sheet

```bash
# 1. Clean start. -v wipes Postgres and the spool; `migrate` re-applies every migration.
docker compose --profile sim --profile tools down -v
docker compose up -d --build --wait          # blocks until every healthcheck passes

# 2. Start synthetic scanners (30 scans/s, authenticated)
docker compose --profile sim up -d --wait simulator

# 3. Verify every hop inside the Docker network
scripts/verify-stack.sh
```

`verify-stack.sh` checks:

1. Every container is `running`/`healthy`.
2. `ingest /readyz`, `processor /readyz` (Kafka reachable) and `realtime /healthz` respond.
3. The `uld.telemetry` topic has 6 partitions and the `uld-stream-processor`
   consumer group has active members (queried through `kafka:9092`, inside
   the network).
4. A probe scan goes through ingest, Kafka and the processor, and lands in
   **Postgres** (by `event_id`) and **Redis** (`HGET uld:live`). The realtime
   `/ws` endpoint completes a WebSocket upgrade (`101`).
5. Every log line from the Go services parses as JSON.

### Useful one-liners

```bash
docker compose ps                                   # health column
curl -s localhost:8080/readyz | jq                  # ingest mode: direct / degraded_spooling
curl -s localhost:9090/metrics | jq                 # processor counters + security event counts
curl -s localhost:8090/stats | jq                   # simulator ledger
docker compose exec kafka /opt/kafka/bin/kafka-consumer-groups.sh \
  --bootstrap-server kafka:9092 --describe --group uld-stream-processor   # partition lag
docker compose logs -f --no-log-prefix processor | jq -c 'select(.event.dataset=="uld.security")'
```

---

## Phase 2: Kafka failure simulation

### Resilience design

```
scanner/simulator ──▶ ingest ──▶ Kafka ──▶ processor ──▶ Postgres / Redis
  retry same ids       │ spool      ▲          retry + backoff, idempotent
  in order, backoff    └─fsync──────┘ replay   on event_id, DLQ for poison
```

| Layer | During outage | Recovery | Why nothing is lost or doubled |
|---|---|---|---|
| **Simulator (client)** | Buffers scans locally. Retries the **same batch with the same `event_id`s**, in order, with exponential backoff from 250 ms to 10 s. | Resumes in order. | Retries are idempotent, and nothing is dropped unless the 200k-batch buffer fills. |
| **Ingest** | The first failed Kafka write (3 s timeout) switches it to **degraded mode**. Every batch is then fsynced to an append-only spool on the `ingest-spool` volume *before* the 202, which reports `"durability":"spool"`. The spool stays FIFO. | A replayer drains the spool oldest-first. It backs off exponentially (0.5 s to 10 s) and deletes a segment only after Kafka acks it (`acks=all`). Ingest returns to direct mode once the spool is empty. | A 202 always means the scan is durable. The spool survives an ingest crash (tested with `kill -9`). Returns 503 + `Retry-After` only when Kafka is down *and* the spool is full (512 MB). |
| **Processor** | `FetchMessage` backs off from 250 ms to 15 s. Kafka-down is tracked from kafka-go's error stream with 5 s hysteresis. | Reconnects and rejoins the group. Uncommitted offsets are redelivered. | Offsets are committed only after the Postgres and Redis writes. A redelivered `event_id` is a no-op (primary key). |

Kafka-level duplicates can occur, and that is expected. A timed-out direct
write may still complete, and then be spooled and replayed too. Commits can
also be lost while the broker is down. The processor absorbs these through
`event_id` idempotency and counts them in `duplicates_absorbed`. The system
gives **effectively-once results** on top of at-least-once delivery.

### Automated run

```bash
scripts/chaos-test.sh                         # pause scenario, then kill scenario (~5 min)
SCENARIOS=pause OUTAGE=120 scripts/chaos-test.sh
```

The script prints a status line every 5 s, so you can watch ingest switch to
`DEGRADED` and the spool fill and then drain. At the end it runs the verifier
and lists the availability events from the logs.

### Manual walkthrough

Use these with a terminal running `watch -n2 'curl -s localhost:8080/readyz; echo; curl -s localhost:9090/metrics | jq -c "{kafka_down,applied,duplicates_absorbed}"'`:

```bash
# 0. Fresh simulator, so the ledger only covers this run
docker compose --profile sim up -d --force-recreate --wait simulator
sleep 30                                             # steady state

# A. Freeze the broker (SIGSTOP: sockets stay open, no responses)
docker compose pause kafka
sleep 60                                             # ingest: degraded_spooling, spool_pending rising
docker compose unpause kafka
sleep 45                                             # spool drains, processor catches up

# B. Crash the broker (SIGKILL), then restart it
docker compose kill -s SIGKILL kafka
sleep 60
docker compose start kafka
until [ "$(docker compose ps --format '{{.Health}}' kafka)" = healthy ]; do sleep 2; done
sleep 45

# Optional, harsher: crash ingest while it holds spooled data
docker compose kill -s SIGKILL ingest && docker compose start ingest
docker compose logs ingest --no-log-prefix | jq -c 'select(.message|test("recovered spooled"))'

# C. Reconcile
docker compose --profile tools run --rm --build verify
```

### Proving no loss and no duplicates

`verify` (`backend/cmd/verify`) exits non-zero on any failure. It:

1. **Quiesces.** It pauses scan generation and waits until the simulator
   queue and the ingest spool are both empty.
2. **Loads the ground truth.** It fetches the simulator's ledger from
   `GET :8090/sent`: every `event_id` that ingest acknowledged with a 202.
   It checks that the ledger has no duplicates and that every generated scan
   was acknowledged.
3. **Checks for loss.** Every ledger id must be in `uld_scan_events`, and any
   missing ids are listed.
4. **Checks for duplicates.** Each ledger id must have exactly one row. It
   also reports `duplicates_absorbed`, the number of Kafka-level redeliveries
   that idempotency neutralized.
5. **Checks ordering.** For every ULD, `ulds.last_scan_at` must equal the
   newest `scanned_at` in its event log, which shows that late or replayed
   events never rewound state.
6. **Checks the projection.** The Redis version per ULD (read through
   realtime `/api/snapshot`) must match Postgres.

To check by hand:

```bash
curl -s localhost:8090/sent > sent.txt; wc -l sent.txt
{ echo "CREATE TEMP TABLE sent(id uuid); COPY sent FROM STDIN;"; cat sent.txt; echo '\.';
  echo "SELECT count(*) AS sent, count(e.event_id) AS persisted FROM sent s LEFT JOIN uld_scan_events e ON e.event_id=s.id;"
} | docker compose exec -T postgres psql -U uld -d uld
```

### Reference result

This run used local stand-ins rather than Docker, which wasn't available on
the authoring machine. Kafka was the franz-go `kfake` broker behind a TCP
fault injector that emulates pause and kill. Postgres 18 and miniredis were
used, with the real service binaries.

```
Scenario pause 40s + kill 40s at 30 scans/s
  Ledger: 5551 acknowledged (3017 via spool)
  [PASS] no acknowledged scan lost        5551/5551 persisted
  [PASS] no scan persisted twice          rows=5551 distinct=5551
  kafka-level redeliveries absorbed: 73
  [PASS] each ULD reflects its newest scan
  [PASS] redis projection matches postgres
Scenario kill kafka + SIGKILL ingest mid-outage (427 records in spool)
  [PASS] 2058/2058 persisted, 0 duplicates; spool recovered 427 records on restart
```

---

## Phase 3: SOC security telemetry

### Log format

Every Go service writes **one JSON object per line to stdout** using
`log/slog`, with field names from the Elastic Common Schema (ECS). The
`json-file` driver rotates the output (20 MB × 5) and the logs carry the
`siem.collect=true` label. Nothing else writes to stdout or stderr in plain
text:

* The standard `log` package goes through slog.
* Panics are logged as one JSON line by a custom recoverer.
* kafka-go client chatter is sampled to one line per 5 s.
* Attacker-controlled strings are length-capped and stripped of control
  characters.
* Raw API keys are never logged. A 16-hex SHA-256 fingerprint lets analysts
  correlate repeated use of the same bad key.
* `source.ip` is the TCP peer address. `X-Forwarded-For` is recorded as
  `http.request.x_forwarded_for` but never trusted, so it can't be used to
  evade brute-force counting.

Security events live in their own dataset. To filter for them, use
`event.dataset:"uld.security"`.

```json
{"@timestamp":"2026-10-15T16:56:16.848Z","log.level":"error","message":"ULD impossible travel between stations",
 "service":{"name":"processor","type":"uld-engine"},"ecs":{"version":"8.11.0"},
 "event":{"kind":"alert","category":["intrusion_detection"],"type":["indicator"],"action":"geo.impossible_travel",
          "outcome":"unknown","severity":73,"dataset":"uld.security","module":"uld-engine","reason":"implied_speed_exceeds_threshold"},
 "rule":{"id":"ULD-GEO-001","name":"ULD impossible travel between stations","ruleset":"uld-engine"},
 "uld":{"id":"AKE99303QF","scanner_id":"SCN-MEL-01","event_id":"5739…","location_id":"MEL-RCV-01"},
 "geo":{"previous":{"station":"CGK","scanned_at":"…","location":{"lat":-6.1256,"lon":106.6559}},
        "current":{"station":"MEL","scanned_at":"…","location":{"lat":-37.669,"lon":144.841}},
        "distance_km":5210.2,"elapsed_seconds":601,"implied_speed_kmh":31209.4,"threshold_kmh":1000,"min_feasible_seconds":18756.7},
 "kafka":{"partition":3,"offset":1187}}
```

### Rule catalogue

| Rule | Action | Sev | Source | Fires when |
|---|---|---|---|---|
| ULD-AUTH-000 | `auth.success` | 0 | ingest | Valid key (opt-in: `AUTH_LOG_SUCCESS=true`, enabled in compose). Needed for failure→success correlation. |
| ULD-AUTH-001 | `auth.missing_credentials` | 21 | ingest | No `Authorization` or `X-API-Key` header. |
| ULD-AUTH-002 | `auth.malformed_header` | 47 | ingest | Non-Bearer scheme, empty or oversized token, invalid charset, or multiple credentials (header smuggling). |
| ULD-AUTH-003 | `auth.invalid_api_key` | 47 | ingest | Well-formed but unknown key. Logs the key fingerprint. |
| ULD-AUTH-004 | `auth.brute_force_suspected` | 73 | ingest | ≥10 auth failures from one IP within 60 s. Fires once per window. |
| ULD-AUTH-005 | `auth.revoked_api_key` | 73 | ingest | A key marked `revoked` is used (possible credential theft). |
| ULD-AUTHZ-001 | `authz.insufficient_scope` | 47 | ingest | A valid key without `scans:write` posts scans. |
| ULD-INPUT-001 | `input.validation_failed` | 21 | ingest | Malformed JSON, schema violation or oversized body. |
| ULD-GEO-001 | `geo.impossible_travel` | 73 | processor | Implied speed between two stations exceeds 1000 km/h (`DETECT_MAX_SPEED_KMH`). |
| ULD-INT-001 | `integrity.weight_implausible` | 73 | processor | Weight is more than 25% above the effective limit, min(ULD rating, aircraft position limit) (`DETECT_WEIGHT_SUSPICIOUS_MARGIN`). Smaller overloads stay operational alerts on the dashboard. |
| ULD-INT-002 | `integrity.weight_below_tare` | 47 | processor | Gross weight below 90% of the ULD's empty weight (physically impossible). |
| ULD-INT-003 | `integrity.loaded_weight_changed` | 73 | processor | A ULD that is LOADED and stays LOADED changes weight by more than max(50 kg, 10%). |
| ULD-DLQ-001 | `pipeline.dlq_routed` | 47 | processor | A message went to `uld.telemetry.dlq`. Logs the SHA-256 and size of the payload, never the payload itself. |
| ULD-AVAIL-001/002 | `availability.ingest_degraded/recovered` | 47/21 | ingest | Ingest lost Kafka and started spooling, or the spool drained. Includes `outage_seconds`. |
| ULD-AVAIL-003 | `availability.spool_full` | 99 | ingest | Kafka is down and the spool is full, so telemetry is being rejected. |
| ULD-AVAIL-004/005 | `availability.processor_kafka_down/restored` | 47/21 | processor | The consumer lost or regained the broker. |

**Detection guarantees.**

* Processor detections are evaluated inside the same transaction that locks
  the ULD row. Their rule IDs are stored in `uld_scan_events.security_flags`,
  a durable copy that survives log loss.
* An alert is emitted only for the first delivery of an event, so Kafka
  redelivery never produces duplicate SIEM alerts.
* Out-of-order events are still checked for impossible travel and weight,
  but not for loaded-weight change, which needs the true previous state.

### Smoke test

```bash
scripts/security-smoke.sh
```

The script sends one stimulus per rule:

* bad headers and a revoked key;
* 12 random keys from one source (credential stuffing);
* a CGK scan followed by a MEL scan 10 minutes later;
* a 9.5 t PMC;
* a 12 kg AKE;
* a weight change on a loaded ULD;
* scans for an unknown location and an unknown flight (DLQ).

It then asserts that each rule appears in `docker compose logs`, that the
brute-force alert fired exactly once, and that no raw key was logged. It also
prints the flagged rows from Postgres.

Run it at most once per minute: the brute-force alert fires once per 60 s
window.

### Shipping to Security Onion / Elastic

Security Onion 2.4 collects with Elastic Agent. Add a *Custom Logs* or
*Docker* integration policy on the host that runs the stack. If you use a
standalone Filebeat, the equivalent configuration is:

```yaml
filebeat.inputs:
  - type: filestream
    id: uld-engine
    paths: ["/var/lib/docker/containers/*/*-json.log"]
    parsers:
      - container: { format: docker }
      - ndjson: { target: "", overwrite_keys: true, expand_keys: true, add_error_key: true }
    processors:
      - add_docker_metadata: ~
      - drop_event.when.not.equals:
          container.labels.siem_collect: "true"
```

Example correlation queries for the thesis (Kibana KQL / EQL):

```text
# Credential stuffing that ended in a successful login from the same source
sequence by source.ip with maxspan=5m
  [any where rule.id == "ULD-AUTH-003"] with runs=5
  [any where rule.id == "ULD-AUTH-000"]

# Revoked credential used, followed by a data-integrity anomaly from the same scanner fleet
sequence with maxspan=30m
  [any where rule.id == "ULD-AUTH-005"] by user.name
  [any where rule.id in ("ULD-INT-001","ULD-INT-003")] by uld.scanner_id

# Integrity anomaly during a pipeline outage (cover for tampering?)
event.dataset:"uld.security" and rule.id:(ULD-INT-* or ULD-AVAIL-001)
```

---

## V2 operations

### Schema migrations

The `migrate` one-shot compose service applies `backend/migrations/*.sql`,
which are embedded in its binary, and records each version in
`schema_migrations`. `processor` and `realtime` start only after it exits
successfully.

* **Upgrading a v1 database:** v1 applied 001–003 through Postgres initdb
  scripts, which keep no record. On its first run, `migrate` detects their
  objects, marks them `baselined`, then applies 004 and later. Running it
  again is a no-op.
* **Adding a migration:** add `005_*.sql`, written to be idempotent
  (`IF NOT EXISTS`, `ON CONFLICT DO NOTHING`), then deploy.

```bash
docker compose run --rm migrate                       # apply pending (also runs on every `up`)
docker compose exec -T postgres psql -U uld -d uld -c 'TABLE schema_migrations'
```

### Secrets and dashboard configuration (VM)

On its first run, `deploy/gcp/deploy.sh` generates `/etc/uld/secrets.env`
(mode 600) containing:

* `INTERNAL_API_SECRET`: shared by realtime, which verifies role tokens, and
  the dashboard server, which mints them;
* `AUTH_SECRET`: signs Auth.js sessions.

It writes `INTERNAL_API_SECRET` into the repo's `.env` for Compose. It also
creates `/etc/uld/dashboard.env`, which the `uld-dashboard` systemd unit
loads. Keys are only ever *added*, so hand edits survive deploys.

**Enable Google sign-in:**

1. Create an OAuth client (type Web application) with the redirect URI
   `https://<domain>/auth/callback/google`.
2. Add the client to the dashboard config:

   ```bash
   sudo tee -a /etc/uld/dashboard.env <<'ENV'
   AUTH_GOOGLE_ID=…apps.googleusercontent.com
   AUTH_GOOGLE_SECRET=…
   GOOGLE_ROLE_MAP=you@example.com:security_auditor
   GOOGLE_DEFAULT_ROLE=ground_handler
   ENV
   sudo systemctl restart uld-dashboard
   ```

**Turn off the one-click demo accounts:** add `DEMO_LOGIN=off`.

### Network clock

The processor flies each aircraft's outbound and return rotation. Real block
and turnaround times (the `routes` table) are divided by
`NETWORK_TIME_SCALE`. The default is 10, so CGK→SYD takes 42 minutes.

* **Departure:** `LOADED` ULDs become `IN_FLIGHT`. Booked ULDs that aren't
  loaded are rebooked onto the next rotation.
* **Arrival:** ULDs land in the destination's receiving area.
* **Sandbox ULDs** (`…SB`) are purged after `SANDBOX_ULD_TTL` (15 minutes).

Only one processor runs the clock at a time, elected per tick by a Postgres
advisory lock.

### Role-gated API (realtime, internal)

The Next.js server calls these with an HS256 token: audience `uld-realtime`,
2-minute TTL, carrying `sub`, `name` and `role`. Go re-checks the role.
Nginx does not expose them.

| Endpoint | Roles |
|---|---|
| `GET /api/v1/flights/{id}/loadplan` | loadmaster, security_auditor |
| `POST /api/v1/flights/{id}/optimize` `{apply}` | loadmaster |
| `POST /api/v1/flights/{id}/loadplan/move` `{uld_id, position}` | loadmaster |
| `GET /api/v1/security/alerts`, `POST …/{id}/ack\|resolve` | security_auditor |
| `GET /api/v1/audit` | security_auditor |
| `GET /ws/security?token=` | security_auditor |

The browser reaches these only through `/bff/*` routes. Those check the
Auth.js session first and rate-limit sandbox actions to 20 per user per
minute.

### Verifying V2 after a deploy

```bash
curl -s localhost:8081/api/snapshot | jq '.data | {stations: (.stations|length), locations: (.locations|length), time_scale}'
docker compose logs --no-log-prefix processor | jq -c 'select(.message=="network clock")' | tail
docker compose exec -T postgres psql -U uld -d uld -c \
  "SELECT flight_number, status, scheduled_departure, estimated_arrival FROM flights WHERE status <> 'ARRIVED' ORDER BY 3"
```

Then sign in as each demo role. In the sandbox, fire *Trigger impossible
travel*; within about a second it appears for the Security Auditor as a
ULD-GEO-001 alert.

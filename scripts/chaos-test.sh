#!/usr/bin/env bash
# Phase 2: Kafka failure simulation under load, followed by an exact
# delivery reconciliation.
#
#   scripts/chaos-test.sh            # pause scenario, then kill scenario
#   SCENARIOS=pause scripts/chaos-test.sh
#   SCENARIOS=kill OUTAGE=120 scripts/chaos-test.sh
#
# Timeline per scenario:  steady load → fault for $OUTAGE s → recover for $RECOVER s
#   pause : `docker compose pause kafka`  (SIGSTOP: TCP stays open, broker
#           silent — clients hit timeouts; the nastiest failure mode)
#   kill  : `docker compose kill kafka`   (SIGKILL: connections refused),
#           then `docker compose start kafka` (log recovery on restart)
cd "$(dirname "$0")/.." && source scripts/lib.sh
need curl; need jq

SCENARIOS=${SCENARIOS:-"pause kill"}
WARMUP=${WARMUP:-30}
OUTAGE=${OUTAGE:-60}
RECOVER=${RECOVER:-45}
# Fault injection backend. "docker" (default) or "http" (a fault-injection
# proxy exposing POST $CHAOS_HTTP/kafka/{pause,unpause,kill,start}).
CHAOS_DRIVER=${CHAOS_DRIVER:-docker}
VERIFY_CMD=${VERIFY_CMD:-"$COMPOSE --profile tools run --rm --build verify"}

kafka() {
  case "$CHAOS_DRIVER" in
    docker)
      case "$1" in
        pause)   $COMPOSE pause kafka ;;
        unpause) $COMPOSE unpause kafka ;;
        kill)    $COMPOSE kill -s SIGKILL kafka ;;
        start)   $COMPOSE start kafka ;;
      esac >/dev/null ;;
    http) curl -fsS -X POST "$CHAOS_HTTP/kafka/$1" >/dev/null ;;
  esac
}

status_line() {
  local s i p
  s=$(jget "$SIM/stats"); i=$(jget "$INGEST/metrics"); p=$(jget "$PROCESSOR/metrics")
  printf '  %s  sim acked=%-6s unsent=%-5s retries=%-3s | ingest %-8s spool=%-5s | processor kafka_down=%-5s applied=%-6s dupes=%s\n' \
    "$(date +%T)" \
    "$(jq -r '.accepted // "?"' <<<"$s")" "$(jq -r '.unsent // "?"' <<<"$s")" "$(jq -r '.retries // "?"' <<<"$s")" \
    "$(jq -r 'if .degraded then "DEGRADED" else "direct" end' <<<"$i")" "$(jq -r '.spool_pending // "?"' <<<"$i")" \
    "$(jq -r 'if has("kafka_down") then (.kafka_down|tostring) else "?" end' <<<"$p")" "$(jq -r '.applied // "?"' <<<"$p")" "$(jq -r '.duplicates_absorbed // "?"' <<<"$p")"
}

watch_for() { # seconds label
  local end=$((SECONDS + $1))
  info "$2 (${1}s)"
  while [ $SECONDS -lt $end ]; do status_line; sleep 5; done
}

START=$(date -u +%Y-%m-%dT%H:%M:%SZ)

step "0. Preflight"
for u in "$INGEST/readyz" "$PROCESSOR/readyz"; do
  curl -fsS --max-time 5 "$u" >/dev/null && ok "$u" || { bad "$u not ready — run scripts/verify-stack.sh"; exit 1; }
done
if [ "$CHAOS_DRIVER" = docker ]; then
  info "starting a fresh simulator (clean ledger) at 30 scans/s"
  $COMPOSE --profile sim up -d --force-recreate --wait simulator >/dev/null
fi
curl -fsS -X POST "$SIM/resume" >/dev/null
ok "simulator running: $(jget "$SIM/stats" | jq -c '{run_id,rate_per_sec}')"

watch_for "$WARMUP" "steady state"

for sc in $SCENARIOS; do
  case "$sc" in
    pause)
      step "Scenario: broker FROZEN (docker compose pause kafka)"
      kafka pause;   watch_for "$OUTAGE"  "kafka paused — ingest should go DEGRADED and spool"
      kafka unpause; watch_for "$RECOVER" "kafka unpaused — spool should drain, processor catch up" ;;
    kill)
      step "Scenario: broker CRASHED (docker compose kill -s SIGKILL kafka)"
      kafka kill;    watch_for "$OUTAGE"  "kafka killed — connections refused"
      kafka start
      if [ "$CHAOS_DRIVER" = docker ]; then
        info "waiting for kafka healthcheck"
        until [ "$($COMPOSE ps --format '{{.Health}}' kafka)" = healthy ]; do sleep 2; done
      fi
      watch_for "$RECOVER" "kafka restarted — recovery" ;;
    *) bad "unknown scenario $sc"; exit 2 ;;
  esac
done

step "Reconciliation"
$VERIFY_CMD || FAILS=$((FAILS+1))

if [ "$CHAOS_DRIVER" = docker ]; then
  step "Availability / security events emitted during the run"
  $COMPOSE logs --no-log-prefix --no-color --since "$START" ingest processor 2>/dev/null \
    | jq -rc 'select(.event.dataset=="uld.security" and (.rule.id|startswith("ULD-AVAIL")))
              | [."@timestamp", .service.name, .rule.id, .event.action, .event.reason, (.outage_seconds // "")] | @tsv' \
    | column -t || true
fi

echo
[ "$FAILS" = 0 ] && echo "${G}CHAOS TEST PASSED${N}" || { echo "${R}CHAOS TEST FAILED${N}"; exit 1; }

#!/usr/bin/env bash
# Phase 1: verify the stack is healthy and every hop of the pipeline works
# inside the Docker network:
#   ingest -> kafka -> processor -> postgres + redis -> realtime (WebSocket)
cd "$(dirname "$0")/.." && source scripts/lib.sh
need curl; need jq

step "1. Container health"
$COMPOSE ps --format json | jq -s 'if type=="array" and (.[0]|type)=="array" then .[0] else . end' \
  | jq -r '.[] | "\(.Service)\t\(.State)\t\(.Health // "-")"' | while IFS=$'\t' read -r svc state health; do
    if [ "$state" = running ] && { [ "$health" = healthy ] || [ "$health" = "-" ]; }; then ok "$svc: $state ${health/-/}"
    else bad "$svc: state=$state health=$health"; fi
  done
for svc in postgres redis kafka ingest processor realtime; do
  h=$($COMPOSE ps --format '{{.Health}}' "$svc" 2>/dev/null || true)
  [ "$h" = healthy ] || bad "$svc is not healthy (got '${h:-not running}')"
done

step "2. Service endpoints"
for u in "$INGEST/readyz" "$PROCESSOR/readyz" "$REALTIME/healthz"; do
  if body=$(curl -fsS --max-time 5 "$u"); then ok "$u → $body"; else bad "$u unreachable"; fi
done

step "3. Kafka topics and consumer group (in-network, via kafka:9092)"
topics=$($COMPOSE exec -T kafka /opt/kafka/bin/kafka-topics.sh --bootstrap-server kafka:9092 --describe --topic uld.telemetry 2>/dev/null | grep -c 'Partition:' || true)
[ "$topics" = 6 ] && ok "uld.telemetry has 6 partitions" || bad "uld.telemetry partitions: $topics"
members=$($COMPOSE exec -T kafka /opt/kafka/bin/kafka-consumer-groups.sh --bootstrap-server kafka:9092 \
  --describe --group uld-stream-processor --members 2>/dev/null | awk 'NR>1 && NF>0' | wc -l)
[ "$members" -ge 1 ] && ok "consumer group uld-stream-processor has $members active members" \
  || bad "no active consumer-group members"

step "4. End-to-end probe"
EID=$(uuid); TS=$(now_iso)
res=$(scan "$KEY_CGK" "{\"event_id\":\"$EID\",\"uld_id\":\"AKE10012GA\",\"scanner_id\":\"SCN-PROBE\",\"scan_type\":\"RFID\",\"weight_kg\":777,\"location_id\":\"STA-06\",\"timestamp\":\"$TS\"}")
[ "${res%% *}" = 202 ] && ok "ingest accepted probe $EID (${res#* })" || bad "ingest: $res"
for i in $(seq 1 20); do
  n=$(psql_q "SELECT count(*) FROM uld_scan_events WHERE event_id='$EID'" 2>/dev/null || echo 0)
  [ "$n" = 1 ] && break; sleep 0.5
done
[ "$n" = 1 ] && ok "processor persisted probe to Postgres" || bad "probe not in Postgres after 10s"
r=$($COMPOSE exec -T redis redis-cli HGET uld:live AKE10012GA | jq -r '.location_id+" "+(.weight_kg|tostring)' 2>/dev/null || true)
[ "$r" = "STA-06 777" ] && ok "processor projected probe to Redis ($r)" || bad "redis state: '$r'"
code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 3 --http1.1 "$REALTIME/ws" \
  -H 'Connection: Upgrade' -H 'Upgrade: websocket' -H 'Sec-WebSocket-Version: 13' \
  -H 'Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==' || true)
[ "$code" = 101 ] && ok "realtime WebSocket upgrade → 101 Switching Protocols" || bad "websocket handshake: $code"

step "5. Logs are strict JSON (SIEM-ready)"
total=0; badlines=0
for svc in ingest processor realtime; do
  lines=$($COMPOSE logs --no-log-prefix --no-color "$svc" 2>/dev/null)
  t=$(printf '%s\n' "$lines" | grep -c . || true)
  b=$(printf '%s\n' "$lines" | grep . | while IFS= read -r l; do printf '%s' "$l" | jq -e . >/dev/null 2>&1 || echo x; done | wc -l)
  total=$((total+t)); badlines=$((badlines+b))
done
[ "$badlines" = 0 ] && ok "$total log lines, all valid JSON" || bad "$badlines of $total log lines are not JSON"

echo
[ "$FAILS" = 0 ] && echo "${G}STACK OK${N}" || { echo "${R}$FAILS check(s) failed${N}"; exit 1; }

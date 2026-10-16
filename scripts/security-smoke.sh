#!/usr/bin/env bash
# Phase 3: fire one stimulus per detection rule and confirm the matching
# security event reached stdout (Docker logs) as strict JSON.
#
# Uses ULD ids the simulator never touches (…QF), so it is safe to run
# while the simulator is pumping traffic.
cd "$(dirname "$0")/.." && source scripts/lib.sh
need curl; need jq

START=$(date -u +%Y-%m-%dT%H:%M:%SZ)
SFX=$(( RANDOM % 900 + 100 ))  # unique ULD serials per run
U_GEO="AKE99${SFX}QF"; U_INT1="PMC98${SFX}QF"; U_INT2="AKE97${SFX}QF"; U_INT3="AKE96${SFX}QF"
ts() { date -u -d "$1" +%Y-%m-%dT%H:%M:%SZ; }
body() { # uld scanner location weight flight timestamp
  printf '{"event_id":"%s","uld_id":"%s","scanner_id":"%s","scan_type":"RFID","weight_kg":%s,"location_id":"%s","flight_number":"%s","timestamp":"%s"}' \
    "$(uuid)" "$1" "$2" "$4" "$3" "$5" "$6"
}
expect() { # status-line expected-code label
  [ "${1%% *}" = "$2" ] && ok "$3 → HTTP $2" || bad "$3 → got ${1%% *}, want $2 (${1#* })"
}

step "Authentication / authorization (ingest)"
OKB=$(body AKE10001GA SCN-SEC RCV-01 300 "" "$(ts now)")
expect "$(curl -sS -o /dev/null -w '%{http_code} -' -X POST "$INGEST/v1/scans" -d "$OKB")" 401 "ULD-AUTH-001 no credentials"
expect "$(curl -sS -o /dev/null -w '%{http_code} -' -X POST "$INGEST/v1/scans" -H 'Authorization: Basic YWRtaW46YWRtaW4=' -d "$OKB")" 401 "ULD-AUTH-002 Basic scheme"
expect "$(curl -sS -o /dev/null -w '%{http_code} -' -X POST "$INGEST/v1/scans" -H "Authorization: Bearer $KEY_CGK" -H "X-API-Key: $KEY_CGK" -d "$OKB")" 401 "ULD-AUTH-002 duplicate credentials"
expect "$(curl -sS -o /dev/null -w '%{http_code} -' -X POST "$INGEST/v1/scans" -H "Authorization: Bearer ' OR 1=1--" -d "$OKB")" 401 "ULD-AUTH-002 injection-shaped token"
expect "$(scan "$KEY_REVOKED" "$OKB")" 401 "ULD-AUTH-005 revoked key"
expect "$(scan "$KEY_RO" "$OKB")" 403 "ULD-AUTHZ-001 read-only key on write endpoint"
info "credential stuffing: 12 random keys from one source"
for i in $(seq 1 12); do scan "guess-$(uuid | tr -d -)" "$OKB" >/dev/null; done
ok "ULD-AUTH-003 ×12 sent (expect one ULD-AUTH-004 brute-force alert)"

step "Input validation (ingest)"
expect "$(scan "$KEY_CGK" '[{"uld_id":"../../etc/passwd","scan_type":"NFC","weight_kg":-1}]')" 422 "ULD-INPUT-001 invalid payload"

step "Geographic anomaly (processor)"
expect "$(scan "$KEY_CGK" "$(body "$U_GEO" SCN-CGK-01 STA-01 900 "" "$(ts '-30 min')")")" 202 "$U_GEO scanned at CGK (T-30m)"
sleep 1
expect "$(scan "$KEY_MEL" "$(body "$U_GEO" SCN-MEL-01 MEL-RCV-01 900 "" "$(ts '-20 min')")")" 202 "$U_GEO scanned at MEL 10 min later (~5,200 km)"

step "Data integrity (processor)"
expect "$(scan "$KEY_CGK" "$(body "$U_INT1" SCN-BUP-01 BUP-01 9500 GA880 "$(ts now)")")" 202 "$U_INT1 9,500 kg vs 6,804 kg limit (+40%)"
expect "$(scan "$KEY_CGK" "$(body "$U_INT2" SCN-RCV-01 RCV-02 12 "" "$(ts now)")")" 202 "$U_INT2 12 kg (< 82 kg tare)"
expect "$(scan "$KEY_CGK" "$(body "$U_INT3" SCN-RMP-01 RMP-01 900 GA716 "$(ts '-2 min')")")" 202 "$U_INT3 loaded at 900 kg"
sleep 1
expect "$(scan "$KEY_CGK" "$(body "$U_INT3" SCN-RMP-02 RMP-02 1450 GA716 "$(ts '-1 min')")")" 202 "$U_INT3 still loaded, now 1,450 kg"

step "Dead-letter routing (processor)"
expect "$(scan "$KEY_CGK" "$(body AKE10002GA SCN-X 'ZZZ-ROGUE-01' 500 "" "$(ts now)")")" 202 "scan for non-existent location ZZZ-ROGUE-01"
expect "$(scan "$KEY_CGK" "$(body AKE10002GA SCN-X STA-01 500 'XX9999' "$(ts now)")")" 202 "scan for non-existent flight XX9999"

info "waiting for the processor"; sleep 4

step "Security events in Docker logs since $START"
EVENTS=$($COMPOSE logs --no-log-prefix --no-color --since "$START" ingest processor 2>/dev/null \
  | jq -c 'select(.event.dataset=="uld.security")' 2>/dev/null || true)
printf '%s\n' "$EVENTS" | jq -r '[.service.name, .rule.id, (.event.severity|tostring), .event.action, .event.reason,
     (.source.ip // .uld.id // "")] | @tsv' 2>/dev/null | column -t -s $'\t' | sed 's/^/  /'

step "Expected detections"
for rule in ULD-AUTH-001 ULD-AUTH-002 ULD-AUTH-003 ULD-AUTH-004 ULD-AUTH-005 ULD-AUTHZ-001 \
            ULD-INPUT-001 ULD-GEO-001 ULD-INT-001 ULD-INT-002 ULD-INT-003 ULD-DLQ-001; do
  n=$(printf '%s\n' "$EVENTS" | jq -r --arg r "$rule" 'select(.rule.id==$r) | .rule.id' 2>/dev/null | wc -l)
  [ "$n" -ge 1 ] && ok "$rule ($n)" || bad "$rule not observed"
done
n=$(printf '%s\n' "$EVENTS" | jq -r 'select(.rule.id=="ULD-AUTH-004")|1' | wc -l)
[ "$n" = 1 ] && ok "brute-force alert de-duplicated (exactly 1)" || bad "ULD-AUTH-004 count = $n, want 1"
leak=$($COMPOSE logs --no-log-prefix --since "$START" ingest 2>/dev/null | grep -c "$KEY_REVOKED" || true)
[ "$leak" = 0 ] && ok "no raw API key in logs" || bad "raw API key found in logs ($leak lines)"

step "Durable flags in Postgres"
psql_q "SELECT uld_id, security_flags FROM uld_scan_events WHERE security_flags <> '{}' AND ingested_at >= '$START' ORDER BY ingested_at" | sed 's/^/  /'

step "Sample event (ULD-GEO-001)"
printf '%s\n' "$EVENTS" | jq 'select(.rule.id=="ULD-GEO-001")' | head -60

echo
[ "$FAILS" = 0 ] && echo "${G}SECURITY SMOKE TEST PASSED${N}" || { echo "${R}$FAILS check(s) failed${N}"; exit 1; }

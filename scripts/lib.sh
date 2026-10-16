# Shared helpers for the operator scripts. Source, don't execute.
set -euo pipefail

COMPOSE=${COMPOSE:-docker compose}
INGEST=${INGEST:-http://localhost:8080}
PROCESSOR=${PROCESSOR:-http://localhost:9090}
REALTIME=${REALTIME:-http://localhost:8081}
SIM=${SIM:-http://localhost:8090}
# DEV ONLY keys (hashes live in backend/config/api-keys.dev.json).
KEY_CGK=${KEY_CGK:-dev-cgk-scanner-fleet-key-7f3a9c2e}
KEY_MEL=${KEY_MEL:-dev-mel-scanner-fleet-key-41b8d0e5}
KEY_RO=${KEY_RO:-dev-ops-dashboard-readonly-key-9e21}
KEY_REVOKED=${KEY_REVOKED:-dev-retired-handheld-key-0c55aa31}

if [ -t 1 ]; then G=$'\e[32m'; R=$'\e[31m'; Y=$'\e[33m'; B=$'\e[1m'; N=$'\e[0m'; else G= R= Y= B= N=; fi
FAILS=0
ok()   { printf '  %s✔%s %s\n' "$G" "$N" "$*"; }
bad()  { printf '  %s✘%s %s\n' "$R" "$N" "$*"; FAILS=$((FAILS+1)); }
info() { printf '  %s•%s %s\n' "$Y" "$N" "$*"; }
step() { printf '\n%s== %s%s\n' "$B" "$*" "$N"; }
need() { command -v "$1" >/dev/null || { echo "missing dependency: $1" >&2; exit 2; }; }
uuid() { cat /proc/sys/kernel/random/uuid; }
now_iso() { date -u +%Y-%m-%dT%H:%M:%S.%3NZ; }
jget() { curl -fsS --max-time 5 "$1" 2>/dev/null || echo '{}'; }

# scan <key> <json-body> -> prints "<http_status> <body>"
scan() {
  local out; out=$(mktemp)
  curl -sS --max-time 10 -o "$out" -w '%{http_code}' -X POST "$INGEST/v1/scans" \
    -H 'Content-Type: application/json' -H "Authorization: Bearer $1" -d "$2"
  printf ' %s\n' "$(cat "$out")"; rm -f "$out"
}

psql_q() { $COMPOSE exec -T postgres psql -U uld -d uld -tAc "$1"; }

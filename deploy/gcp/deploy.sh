#!/usr/bin/env bash
# Build and (re)start the whole stack on the VM. Idempotent; run as root.
#
# Called by:
#   * deploy/gcp/startup.sh on every boot (after it writes .env and the
#     low-memory docker-compose.override.yml from instance metadata);
#   * .github/workflows/deploy.yml on every push to main (after `git pull`).
#
# Usage: deploy.sh [--full-restart]
#   --full-restart  run `docker compose down` before starting. Off by default:
#                   Kafka keeps its log inside the container, so `down` deletes
#                   any telemetry not yet consumed plus all consumer offsets,
#                   and takes the whole stack offline while Kafka cold-starts.
#                   A plain `up -d` only recreates containers whose image or
#                   config changed.
set -euo pipefail
cd "$(dirname "$(readlink -f "$0")")/../.."

FULL_RESTART=false
[ "${1:-}" = "--full-restart" ] && FULL_RESTART=true

# NEXT_PUBLIC_WS_URL is baked into the dashboard at build time. startup.sh
# passes it in; on CI deploys reuse what the last build used.
WS_URL=${WS_URL:-$(cat frontend/.built-ws-url 2>/dev/null || true)}
if [ -z "$WS_URL" ]; then
  echo "WS_URL unknown: run deploy/gcp/startup.sh once first" >&2
  exit 1
fi

echo "== deploying $(git rev-parse --short HEAD): $(git log -1 --format=%s)"

# --- Secrets & runtime config ----------------------------------------------
# Generated once per VM and kept outside the repo. INTERNAL_API_SECRET is
# shared by the realtime service (verifies role tokens) and the dashboard
# server (mints them); AUTH_SECRET signs Auth.js sessions.
install -d -m 0755 /etc/uld
SECRETS=/etc/uld/secrets.env
if [ ! -f "$SECRETS" ]; then
  umask 077
  printf 'INTERNAL_API_SECRET=%s\nAUTH_SECRET=%s\n' "$(openssl rand -hex 32)" "$(openssl rand -hex 32)" > "$SECRETS"
  umask 022
fi
# shellcheck disable=SC1090
. "$SECRETS"

# set_kv FILE KEY VALUE: add KEY only if missing, so values an operator adds
# or edits by hand (e.g. AUTH_GOOGLE_ID) survive deploys.
set_kv() { touch "$1"; grep -q "^$2=" "$1" || printf '%s=%s\n' "$2" "$3" >> "$1"; }
# upsert_kv FILE KEY VALUE: always set KEY (for values derived from the deploy itself).
upsert_kv() {
  touch "$1"
  if grep -q "^$2=" "$1"; then sed -i "s#^$2=.*#$2=$3#" "$1"; else printf '%s=%s\n' "$2" "$3" >> "$1"; fi
}
set_kv .env INTERNAL_API_SECRET "$INTERNAL_API_SECRET"

DASH_ENV=/etc/uld/dashboard.env
DASH_ENV_BEFORE=$(cat "$DASH_ENV" 2>/dev/null | sha256sum)
# Public origin of the dashboard, derived from the WebSocket URL. Auth.js
# needs it explicitly: behind Nginx it would otherwise build redirect URLs
# from Next.js's internal host (localhost:3000).
case "$WS_URL" in
  wss://*) h=${WS_URL#wss://}; PUBLIC_ORIGIN="https://${h%%/*}" ;;
  *)       h=${WS_URL#ws://}; h=${h%%/*}; PUBLIC_ORIGIN="http://${h%%:*}:3000" ;;
esac
upsert_kv "$DASH_ENV" AUTH_URL "$PUBLIC_ORIGIN"
set_kv "$DASH_ENV" AUTH_SECRET "$AUTH_SECRET"
set_kv "$DASH_ENV" AUTH_TRUST_HOST true
set_kv "$DASH_ENV" INTERNAL_API_SECRET "$INTERNAL_API_SECRET"
set_kv "$DASH_ENV" REALTIME_INTERNAL_URL http://127.0.0.1:8081
set_kv "$DASH_ENV" INGEST_INTERNAL_URL http://127.0.0.1:8080
set_kv "$DASH_ENV" SIM_CONTROL_URL http://127.0.0.1:8090
set_kv "$DASH_ENV" SANDBOX_API_KEY dev-web-sandbox-key-5c2e81f0a9
chmod 0640 "$DASH_ENV"
[ "$(sha256sum < "$DASH_ENV")" = "$DASH_ENV_BEFORE" ] || DASH_ENV_CHANGED=true

# Heartbeat: steps like `go build` are silent for minutes; regular output keeps
# an SSH session (the CI deploy runs over an IAP tunnel) from idling out.
( while sleep 30; do echo "   ... $(date +%T) still working"; done ) &
HEARTBEAT=$!
trap 'kill $HEARTBEAT 2>/dev/null || true' EXIT

# --- Backend --------------------------------------------------------------
# Build before stopping anything, so a broken build leaves the old stack up.
# One image at a time: parallel Go builds exhaust memory on small VMs.
for svc in migrate ingest processor realtime simulator; do
  echo "== building $svc"
  BUILDKIT_PROGRESS=plain docker compose --profile sim build "$svc"
done

if $FULL_RESTART; then
  echo "== full restart requested: docker compose down"
  docker compose --profile sim down --remove-orphans
fi
docker compose up -d --remove-orphans
docker compose --profile sim up -d simulator

echo "== waiting for backend health"
healthy=false
for _ in $(seq 1 60); do
  if curl -fsS localhost:8080/healthz >/dev/null 2>&1 &&
     curl -fsS localhost:8081/healthz >/dev/null 2>&1 &&
     curl -fsS localhost:9090/readyz  >/dev/null 2>&1; then
    healthy=true
    break
  fi
  sleep 5
done
docker compose ps
if ! $healthy; then
  echo "backend did not become healthy within 5 minutes" >&2
  docker compose logs --tail=40 ingest processor realtime >&2
  exit 1
fi
echo "backend healthy"

# --- Dashboard -------------------------------------------------------------
id uld >/dev/null 2>&1 ||
  useradd --system --create-home --home-dir /var/lib/uld --shell /usr/sbin/nologin uld
chown -R uld:uld frontend
chgrp uld "$DASH_ENV"

# Rebuild when the frontend source (git tree hash) or the WebSocket URL changed.
STAMP="$WS_URL $(git rev-parse HEAD:frontend)"
restart_dashboard=false
if [ ! -d frontend/.next ] || [ "$(cat frontend/.build-stamp 2>/dev/null)" != "$STAMP" ]; then
  echo "== building dashboard (NEXT_PUBLIC_WS_URL=$WS_URL)"
  sudo -u uld env HOME=/var/lib/uld NEXT_PUBLIC_WS_URL="$WS_URL" NODE_OPTIONS=--max-old-space-size=1024 \
    bash -c 'cd frontend && npm ci --no-audit --no-fund && npm run build'
  echo "$STAMP" > frontend/.build-stamp
  echo "$WS_URL" > frontend/.built-ws-url
  restart_dashboard=true
else
  echo "== dashboard unchanged; skipping build"
fi

UNIT=/etc/systemd/system/uld-dashboard.service
NEW_UNIT=$(cat <<EOF
[Unit]
Description=ULD Tracking Engine dashboard (Next.js)
After=network-online.target docker.service

[Service]
User=uld
WorkingDirectory=$PWD/frontend
Environment=NODE_ENV=production HOME=/var/lib/uld
EnvironmentFile=/etc/uld/dashboard.env
ExecStart=/usr/bin/npm start -- -p 3000
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF
)
if [ "$(cat "$UNIT" 2>/dev/null)" != "$NEW_UNIT" ]; then
  echo "$NEW_UNIT" > "$UNIT"
  systemctl daemon-reload
  restart_dashboard=true
fi
systemctl enable --quiet uld-dashboard
if $restart_dashboard || [ "${DASH_ENV_CHANGED:-false}" = true ] || ! systemctl is-active --quiet uld-dashboard; then
  systemctl restart uld-dashboard
fi

for _ in $(seq 1 30); do
  curl -fsS -o /dev/null localhost:3000 && { echo "dashboard healthy"; break; }
  sleep 2
done
curl -fsS -o /dev/null localhost:3000 || { echo "dashboard not responding on :3000" >&2; exit 1; }

echo "== deploy of $(git rev-parse --short HEAD) complete"

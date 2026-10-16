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

# Heartbeat: steps like `go build` are silent for minutes; regular output keeps
# an SSH session (the CI deploy runs over an IAP tunnel) from idling out.
( while sleep 30; do echo "   ... $(date +%T) still working"; done ) &
HEARTBEAT=$!
trap 'kill $HEARTBEAT 2>/dev/null || true' EXIT

# --- Backend --------------------------------------------------------------
# Build before stopping anything, so a broken build leaves the old stack up.
# One image at a time: parallel Go builds exhaust memory on small VMs.
for svc in ingest processor realtime simulator; do
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
if $restart_dashboard || ! systemctl is-active --quiet uld-dashboard; then
  systemctl restart uld-dashboard
fi

for _ in $(seq 1 30); do
  curl -fsS -o /dev/null localhost:3000 && { echo "dashboard healthy"; break; }
  sleep 2
done
curl -fsS -o /dev/null localhost:3000 || { echo "dashboard not responding on :3000" >&2; exit 1; }

echo "== deploy of $(git rev-parse --short HEAD) complete"

#!/usr/bin/env bash
# Provision the ULD Tracking Engine demo on a Compute Engine VM.
# Run from the repository root:   deploy/gcp/provision.sh
#
# Overridable settings:
#   ZONE=us-central1-a     e2-micro free tier: us-central1, us-west1 or us-east1
#   MACHINE=e2-micro       e2-small (2 GB) or e2-medium (4 GB) for a snappier demo
#   API_SOURCE=0.0.0.0/0   who may reach the ingest API on :8080;
#                          e.g. API_SOURCE="$(curl -s ifconfig.me)/32"
#   SIM_RATE=5             synthetic scans/second for the demo
set -euo pipefail

PROJECT=${PROJECT:-$(gcloud config get-value project 2>/dev/null)}
ZONE=${ZONE:-us-central1-a}
VM=${VM:-uld-demo}
MACHINE=${MACHINE:-e2-micro}
API_SOURCE=${API_SOURCE:-0.0.0.0/0}
SIM_RATE=${SIM_RATE:-5}
NETWORK=${NETWORK:-default}
G="gcloud --project=$PROJECT"

echo "project=$PROJECT zone=$ZONE vm=$VM machine=$MACHINE"
$G services enable compute.googleapis.com

# ---- 2. Firewall rules (created only if missing) ----
rule() { # name ports source-range tag description
  if $G compute firewall-rules describe "$1" >/dev/null 2>&1; then
    echo "firewall rule $1 exists"
  else
    $G compute firewall-rules create "$1" --network="$NETWORK" --direction=INGRESS \
      --action=ALLOW --rules="$2" --source-ranges="$3" --target-tags="$4" --description="$5"
  fi
}
rule uld-allow-http-https tcp:80,tcp:443   0.0.0.0/0     uld-web    "HTTP/HTTPS (reverse proxy)"
rule uld-allow-dashboard  tcp:3000,tcp:8081 0.0.0.0/0    uld-app    "Next.js dashboard + realtime WebSocket"
rule uld-allow-ingest     tcp:8080          "$API_SOURCE" uld-ingest "Scanner ingest API"

# ---- 1 + 3. VM with ephemeral public IP; startup script installs and starts everything ----
$G compute instances create "$VM" \
  --zone="$ZONE" \
  --machine-type="$MACHINE" \
  --image-family=ubuntu-2404-lts-amd64 --image-project=ubuntu-os-cloud \
  --boot-disk-size=30GB --boot-disk-type=pd-standard \
  --tags=uld-web,uld-app,uld-ingest \
  --shielded-secure-boot --shielded-vtpm --shielded-integrity-monitoring \
  --metadata=uld-sim-rate="$SIM_RATE" \
  --metadata-from-file=startup-script=deploy/gcp/startup.sh
  # An ephemeral external IP is attached by default (no --no-address flag).

IP=$($G compute instances describe "$VM" --zone="$ZONE" \
      --format='get(networkInterfaces[0].accessConfigs[0].natIP)')
cat <<EOF

VM created. Public IP: $IP
First boot takes ~20-30 min on e2-micro. Watch it with:
  gcloud compute ssh $VM --zone=$ZONE -- sudo journalctl -u google-startup-scripts.service -f

Then open:
  Dashboard  http://$IP:3000
  Ingest API http://$IP:8080/healthz
EOF

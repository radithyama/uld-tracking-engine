#!/usr/bin/env bash
# Delete everything provision.sh created, so nothing keeps billing.
set -euo pipefail
PROJECT=${PROJECT:-$(gcloud config get-value project 2>/dev/null)}
ZONE=${ZONE:-us-central1-a}
VM=${VM:-uld-demo}
G="gcloud --project=$PROJECT"

$G compute instances delete "$VM" --zone="$ZONE" --quiet || true
for r in uld-allow-http-https uld-allow-dashboard uld-allow-ingest; do
  $G compute firewall-rules delete "$r" --quiet || true
done
# Only if you reserved one (see the static-IP note in provision.sh output/README):
$G compute addresses delete uld-ip --region="${ZONE%-*}" --quiet 2>/dev/null || true

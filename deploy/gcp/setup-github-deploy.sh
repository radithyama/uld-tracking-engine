#!/usr/bin/env bash
# One-time setup so .github/workflows/deploy.yml can deploy to the VM without
# any long-lived Google credentials:
#   GitHub OIDC token -> Workload Identity Federation -> service account
#   -> IAP tunnel -> SSH (key in instance metadata) -> deploy/gcp/deploy.sh
#
# Run from the repo root on a machine where `gcloud` and `gh` are logged in.
# Safe to re-run: existing resources are reused.
set -euo pipefail

PROJECT_ID=${PROJECT_ID:-$(gcloud config get-value project 2>/dev/null)}
REPO=${REPO:-radithyama/uld-tracking-engine}
INSTANCE=${INSTANCE:-uld-demo}
ZONE=${ZONE:-us-central1-a}
POOL=github
PROVIDER=uld-tracking-engine
SA_NAME=gha-deployer
KEY_FILE=${KEY_FILE:-$(mktemp -d)/gha-deploy}

PROJECT_NUMBER=$(gcloud projects describe "$PROJECT_ID" --format='value(projectNumber)')
SA_EMAIL="$SA_NAME@$PROJECT_ID.iam.gserviceaccount.com"
# Numeric repo id: unlike owner/name it can't be re-registered by someone else
# if the repository is ever renamed or deleted.
REPO_ID=$(gh api "repos/$REPO" --jq .id)
VM_SA=$(gcloud compute instances describe "$INSTANCE" --zone "$ZONE" --format='value(serviceAccounts[0].email)')
G="gcloud --project=$PROJECT_ID"
exists() { "$@" >/dev/null 2>&1; }

echo "project=$PROJECT_ID ($PROJECT_NUMBER) repo=$REPO (id $REPO_ID) vm=$INSTANCE vm_sa=$VM_SA"

# ---- APIs -----------------------------------------------------------------
$G services enable iam.googleapis.com iamcredentials.googleapis.com sts.googleapis.com \
  compute.googleapis.com iap.googleapis.com

# ---- 1a. Workload Identity Pool + GitHub OIDC provider ---------------------
exists $G iam workload-identity-pools describe "$POOL" --location=global ||
  $G iam workload-identity-pools create "$POOL" --location=global \
    --display-name="GitHub Actions"

# Only tokens from THIS repository's main branch are accepted at all.
exists $G iam workload-identity-pools providers describe "$PROVIDER" \
    --location=global --workload-identity-pool="$POOL" ||
  $G iam workload-identity-pools providers create-oidc "$PROVIDER" \
    --location=global --workload-identity-pool="$POOL" \
    --display-name="uld-tracking-engine" \
    --issuer-uri="https://token.actions.githubusercontent.com" \
    --attribute-mapping="google.subject=assertion.sub,attribute.repository=assertion.repository,attribute.repository_id=assertion.repository_id,attribute.ref=assertion.ref" \
    --attribute-condition="assertion.repository_id == '$REPO_ID' && assertion.ref == 'refs/heads/main'"

PROVIDER_ID=$($G iam workload-identity-pools providers describe "$PROVIDER" \
  --location=global --workload-identity-pool="$POOL" --format='value(name)')

# ---- 1b. Service account ----------------------------------------------------
exists $G iam service-accounts describe "$SA_EMAIL" ||
  $G iam service-accounts create "$SA_NAME" --display-name="GitHub Actions deployer (uld-demo)"

# ---- 1c. Let the repository impersonate it ----------------------------------
$G iam service-accounts add-iam-policy-binding "$SA_EMAIL" \
  --role=roles/iam.workloadIdentityUser \
  --member="principalSet://iam.googleapis.com/projects/$PROJECT_NUMBER/locations/global/workloadIdentityPools/$POOL/attribute.repository_id/$REPO_ID" \
  --condition=None >/dev/null

# ---- 1d. Permissions, scoped as narrowly as gcloud compute ssh allows -------
# Add its SSH key to THIS VM's metadata (gcloud falls back from project to
# instance metadata when it can't write the former) and read the instance.
$G compute instances add-iam-policy-binding "$INSTANCE" --zone="$ZONE" \
  --member="serviceAccount:$SA_EMAIL" --role=roles/compute.instanceAdmin.v1 --condition=None >/dev/null
# Changing metadata on a VM that runs as a service account requires actAs on
# that service account only (not every service account in the project).
$G iam service-accounts add-iam-policy-binding "$VM_SA" \
  --member="serviceAccount:$SA_EMAIL" --role=roles/iam.serviceAccountUser --condition=None >/dev/null
# `gcloud compute ssh` reads project info (compute.projects.get) and the
# action always tunnels through IAP.
$G projects add-iam-policy-binding "$PROJECT_ID" \
  --member="serviceAccount:$SA_EMAIL" --role=roles/compute.viewer --condition=None >/dev/null
$G projects add-iam-policy-binding "$PROJECT_ID" \
  --member="serviceAccount:$SA_EMAIL" --role=roles/iap.tunnelResourceAccessor --condition=None >/dev/null

# ---- IAP -> SSH firewall rule (Google's IAP range only) --------------------
exists $G compute firewall-rules describe uld-allow-iap-ssh ||
  $G compute firewall-rules create uld-allow-iap-ssh --network=default --direction=INGRESS \
    --action=ALLOW --rules=tcp:22 --source-ranges=35.235.240.0/20 --target-tags=uld-web \
    --description="SSH via Identity-Aware Proxy (GitHub Actions deploys)"

# ---- Deploy SSH key ---------------------------------------------------------
# RSA: ssh-compute's key parser has had trouble with ed25519 keys.
if [ ! -f "$KEY_FILE" ]; then
  ssh-keygen -q -t rsa -b 4096 -N "" -C gha-deploy -f "$KEY_FILE"
fi

# ---- 2. GitHub secrets ------------------------------------------------------
gh secret set GCP_WORKLOAD_IDENTITY_PROVIDER --repo "$REPO" --body "$PROVIDER_ID"
gh secret set GCP_SERVICE_ACCOUNT            --repo "$REPO" --body "$SA_EMAIL"
gh secret set GCP_SSH_PRIVATE_KEY            --repo "$REPO" < "$KEY_FILE"
# The private key now lives only in GitHub's encrypted secrets.
shred -u "$KEY_FILE" "$KEY_FILE.pub" 2>/dev/null || rm -f "$KEY_FILE" "$KEY_FILE.pub"

cat <<EOF

Done.
  GCP_WORKLOAD_IDENTITY_PROVIDER = $PROVIDER_ID
  GCP_SERVICE_ACCOUNT            = $SA_EMAIL
  GCP_SSH_PRIVATE_KEY            = (new RSA-4096 key; local copy deleted)

Push to main (or run the workflow manually) to deploy:
  gh workflow run deploy.yml --repo $REPO
EOF

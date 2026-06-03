#!/usr/bin/env bash
# Generate NATS account + service user JWTs for LocalAI distributed mode.
#
# Requires: nsc (https://docs.nats.io/running-a-nats-service/configuration/securing_nats/auth_intro/nsc)
#
# Usage:
#   ./scripts/nats-auth-setup.sh
#
# Outputs operator/account seeds and a service user JWT suitable for:
#   LOCALAI_NATS_ACCOUNT_SEED
#   LOCALAI_NATS_SERVICE_JWT
#
# Per-node worker JWTs are minted automatically by the frontend at registration
# when LOCALAI_NATS_ACCOUNT_SEED is set.

set -euo pipefail

if ! command -v nsc >/dev/null 2>&1; then
	echo "nsc is required. Install from https://github.com/nats-io/nsc/releases" >&2
	exit 1
fi

OPERATOR="${NATS_OPERATOR_NAME:-localai-operator}"
ACCOUNT="${NATS_ACCOUNT_NAME:-localai}"
SERVICE_USER="${NATS_SERVICE_USER:-localai-frontend}"

nsc add operator -n "$OPERATOR" --generate-signing-key
nsc add account -n "$ACCOUNT"
nsc add user -n "$SERVICE_USER" --account "$ACCOUNT"

# Broad publish for frontend control plane (tighten with custom claims in production).
nsc edit user -n "$SERVICE_USER" --account "$ACCOUNT" \
	--allow-pub "nodes.>,gallery.>,agent.>,jobs.>,mcp.>,cache.>,prefixcache.>,finetune.>" \
	--allow-sub "nodes.>,gallery.>,agent.>,jobs.>,mcp.>,cache.>,prefixcache.>,_INBOX.>"

KEYS_DIR="${NATS_KEYS_DIR:-./nats-keys}"
mkdir -p "$KEYS_DIR"
nsc generate creds -a "$ACCOUNT" -n "$SERVICE_USER" -o "$KEYS_DIR"

ACCOUNT_SEED=$(nsc describe account "$ACCOUNT" -o json | jq -r '.nats.private_key')
SERVICE_JWT=$(cat "$KEYS_DIR/${ACCOUNT}/${SERVICE_USER}.jwt" 2>/dev/null || cat "$KEYS_DIR/${SERVICE_USER}.jwt")

echo ""
echo "=== LocalAI NATS auth material ==="
echo "LOCALAI_NATS_ACCOUNT_SEED=${ACCOUNT_SEED}"
echo "LOCALAI_NATS_SERVICE_JWT=${SERVICE_JWT}"
echo ""
echo "Configure the NATS server with the generated operator/account JWTs under $KEYS_DIR"
echo "and set LOCALAI_NATS_REQUIRE_AUTH=true on frontends and workers in production."
#!/usr/bin/env bash
# Generate NATS JWT authentication material and server configuration
# for LocalAI distributed mode.
#
# Requires: nsc (https://docs.nats.io/running-a-nats-service/configuration/securing_nats/auth_intro/nsc)
#
# Outputs:
#   ./nats-keys/localai-nats.env
#   ./nats-keys/localai-frontend.creds
#   ./nats-keys/nats-auth.conf
#   ./nats-keys/nats-server.conf
#
# Environment overrides:
#   NATS_OPERATOR_NAME
#   NATS_ACCOUNT_NAME
#   NATS_SERVICE_USER
#   NATS_KEYS_DIR

#
# LocalAI workers receive their own JWT and user seed when registering
# with the frontend.

set -euo pipefail

# Ensure newly created secret files are private by default.
umask 077

if ! command -v nsc >/dev/null 2>&1; then
	echo "nsc is required. Install from https://github.com/nats-io/nsc/releases" >&2
	exit 1
fi

OPERATOR="${NATS_OPERATOR_NAME:-localai-operator}"
ACCOUNT="${NATS_ACCOUNT_NAME:-localai}"
SYSTEM_ACCOUNT="${NATS_SYSTEM_ACCOUNT_NAME:-SYS}"
SERVICE_USER="${NATS_SERVICE_USER:-localai-frontend}"
OUTPUT_DIR="${NATS_KEYS_DIR:-./nats-keys}"

CREDS_FILE="$OUTPUT_DIR/${SERVICE_USER}.creds"
ENV_FILE="$OUTPUT_DIR/localai-nats.env"
AUTH_CONFIG_FILE="$OUTPUT_DIR/nats-auth.conf"
SERVER_CONFIG_FILE="$OUTPUT_DIR/nats-server.conf"

mkdir -p "$OUTPUT_DIR"

echo "Configuring NATS operator: $OPERATOR"

# Create the operator if it does not exist, otherwise select it.
if nsc select operator "$OPERATOR" >/dev/null 2>&1; then
	echo "[ OK ] using existing operator '$OPERATOR'"
else
	nsc add operator \
		-n "$OPERATOR" \
		--generate-signing-key

	nsc select operator "$OPERATOR" >/dev/null
fi

# Create and assign the NATS system account.
if nsc describe account \
	-n "$SYSTEM_ACCOUNT" >/dev/null 2>&1; then
	echo "[ OK ] using existing system account '$SYSTEM_ACCOUNT'"
else
	nsc add account -n "$SYSTEM_ACCOUNT"
fi

nsc edit operator \
	--system-account "$SYSTEM_ACCOUNT"

# Create the LocalAI account if it does not exist.
if nsc describe account -n "$ACCOUNT" >/dev/null 2>&1; then
	echo "[ OK ] using existing account '$ACCOUNT'"
else
	nsc add account -n "$ACCOUNT"
fi

nsc select account "$ACCOUNT" >/dev/null

# Create the frontend service user if it does not exist.
if nsc describe user \
	-n "$SERVICE_USER" \
	--account "$ACCOUNT" >/dev/null 2>&1; then
	echo "[ OK ] using existing user '$SERVICE_USER'"
else
	nsc add user \
		-n "$SERVICE_USER" \
		--account "$ACCOUNT"
fi

# Frontend control-plane permissions.
nsc edit user \
	-n "$SERVICE_USER" \
	--account "$ACCOUNT" \
	--allow-pub "nodes.>,gallery.>,agent.>,staging.>,state.>,jobs.>,mcp.>,cache.>,prefixcache.>,finetune.>" \
	--allow-sub "nodes.>,gallery.>,agent.>,staging.>,state.>,jobs.>,mcp.>,cache.>,prefixcache.>,_INBOX.>"

# Generate a credentials file containing the frontend user JWT and seed.
rm -f "$CREDS_FILE"

nsc generate creds \
	-a "$ACCOUNT" \
	-n "$SERVICE_USER" \
	-o "$CREDS_FILE"

# Extract the frontend JWT from the credentials file.
SERVICE_JWT="$(
	awk '
		/BEGIN NATS USER JWT/ {
			capture = 1
			next
		}
		/END NATS USER JWT/ {
			capture = 0
		}
		capture
	' "$CREDS_FILE" | tr -d '\r\n'
)"

# Extract the frontend user seed from the credentials file.
SERVICE_SEED="$(
	awk '
		/BEGIN USER NKEY SEED/ {
			capture = 1
			next
		}
		/END USER NKEY SEED/ {
			capture = 0
		}
		capture
	' "$CREDS_FILE" | tr -d '\r\n'
)"

# Retrieve the seed belonging to this exact account rather than taking
# the first account key found in the keystore.
ACCOUNT_SEED="$(
	nsc list keys \
		--account "$ACCOUNT" \
		--accounts \
		--show-seeds |
	awk -F '|' -v expected="$ACCOUNT" '
		function trim(value) {
			gsub(/^[[:space:]]+|[[:space:]]+$/, "", value)
			return value
		}

		NF >= 3 {
			entity = trim($2)
			seed = trim($3)

			if (entity == expected && seed ~ /^SA[A-Z0-9]+$/) {
				print seed
				exit
			}
		}
	'
)"

# Validate all extracted values before writing output files.
if [[ ! "$ACCOUNT_SEED" =~ ^SA[A-Z0-9]+$ ]]; then
	echo "Unable to extract the account seed for '$ACCOUNT'." >&2
	exit 1
fi

if [[ ! "$SERVICE_JWT" =~ ^eyJ ]]; then
	echo "Unable to extract the service JWT from '$CREDS_FILE'." >&2
	exit 1
fi

if [[ ! "$SERVICE_SEED" =~ ^SU[A-Z0-9]+$ ]]; then
	echo "Unable to extract the service seed from '$CREDS_FILE'." >&2
	exit 1
fi

# Generate the trusted operator and memory resolver configuration.
# This contains public operator/account JWT claims, not the private seeds.
nsc generate config \
	--mem-resolver \
	--config-file "$AUTH_CONFIG_FILE" \
	--force

# Generate the primary NATS server configuration.
# The include path matches the Docker Compose mounts shown below.
cat >"$SERVER_CONFIG_FILE" <<'NATS_CONFIG'
server_name: localai-nats
port: 4222
http: 8222

jetstream {
	store_dir: /data/jetstream
}

include nats-auth.conf
NATS_CONFIG

# Generate the environment file consumed by the LocalAI frontend.
cat >"$ENV_FILE" <<EOF
LOCALAI_NATS_ACCOUNT_SEED=$ACCOUNT_SEED
LOCALAI_NATS_SERVICE_JWT=$SERVICE_JWT
LOCALAI_NATS_SERVICE_SEED=$SERVICE_SEED
EOF

# The environment and credentials files contain private seeds.
chmod 600 "$CREDS_FILE" "$ENV_FILE"

# These contain server configuration and public JWT claims.
chmod 644 "$AUTH_CONFIG_FILE" "$SERVER_CONFIG_FILE"

echo
echo "=== LocalAI NATS JWT setup complete ==="
echo
echo "LocalAI environment: $ENV_FILE"
echo "Service credentials: $CREDS_FILE"
echo "NATS server config:   $SERVER_CONFIG_FILE"
echo "NATS auth config:     $AUTH_CONFIG_FILE"
echo
echo "Keep '$ENV_FILE' and '$CREDS_FILE' secret."
echo "Do not commit them to source control."
echo
echo "=== LocalAI NATS environment ==="
cat "$ENV_FILE"

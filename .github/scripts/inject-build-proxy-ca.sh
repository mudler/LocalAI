#!/usr/bin/env bash
set -euo pipefail

ca="${LOCALAI_BUILD_PROXY_CA:?build proxy CA is unset}"
builder="${BUILDER_NAME:?Buildx builder name is unset}"
container="$(docker ps --filter "name=buildx_buildkit_${builder}0" --format '{{.ID}}' | head -n1)"
if test -z "$container"; then
  echo 'BuildKit container not found' >&2
  exit 1
fi

docker exec "$container" mkdir -p /usr/local/share/ca-certificates /etc/ssl/certs
docker cp "$ca" "$container:/usr/local/share/ca-certificates/localai-build-proxy.crt"
docker exec "$container" sh -eu -c '
  if command -v update-ca-certificates >/dev/null 2>&1; then
    update-ca-certificates
  else
    cat /usr/local/share/ca-certificates/localai-build-proxy.crt >>/etc/ssl/certs/ca-certificates.crt
  fi
'
docker restart "$container" >/dev/null

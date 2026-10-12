#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
set -euo pipefail
if [[ $# != 1 || ( $1 != baseline && $1 != config ) ]]; then
  echo 'Usage: bash contrib/launcher-worker/verify.sh {baseline|config}' >&2
  exit 2
fi
mode=$1
here=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo=$(cd "$here/../.." && pwd)
source "$here/lib.sh"
work=${LAUNCHER_VERIFY_WORKDIR:-$repo/assets/work/launcher-worker-validation}
mkdir -p "$work"
work=$(cd "$work" && pwd)
for cmd in docker git tar timeout flock; do command -v "$cmd" >/dev/null; done
exec 9>"$work/runner.lock"
flock -n 9 || { echo 'Verification already running in this work directory' >&2; exit 1; }
require_space "$work"
run=$(mktemp -d "$work/$mode.XXXXXX")
mkdir -p "$run/source" "$run/logs" "$work/cache"
prepare_tmp "$run/tmp"
name=localai-launcher-worker-$(basename "$run" | tr '[:upper:].' '[:lower:]-')
cleanup() {
  local status=$?
  trap - EXIT
  docker rm -f "$name" >> "$run/logs/cleanup.log" 2>&1 || true
  printf '%s\n' "$status" > "$run/logs/verify.exit" || status=1
  echo "Verification exit $status; evidence: $run"
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
(cd "$repo"; git ls-files -z | tar --null -T - -cf -) | tar -xf - -C "$run/source"
mkdir -p "$run/source/contrib/launcher-worker"
cp "$here/"* "$run/source/contrib/launcher-worker/"
{ git -C "$repo" rev-parse HEAD; git -C "$repo" diff --stat HEAD; docker version; df -h "$work"; } > "$run/logs/source.txt"
docker_options=()
if [[ $mode == baseline ]]; then
socket=${LAUNCHER_WORKER_DOCKER_SOCKET:-/var/run/docker.sock}
[[ -S $socket ]] || { echo "Docker socket required for real nodes testcontainers: $socket" >&2; exit 1; }
docker_options=(--add-host=host.docker.internal:host-gateway -e TESTCONTAINERS_HOST_OVERRIDE=host.docker.internal -v "$socket:/var/run/docker.sock")
fi
echo "Evidence: $run"
run_logged "$run/logs/docker" timeout --signal=TERM --kill-after=30s 3600s \
  docker run --rm --name "$name" --platform linux/amd64 \
  --cpus=4 --memory=12g --pids-limit=2048 \
  "${docker_options[@]}" -e LAUNCHER_VERIFY_MODE="$mode" \
  -v "$run:/validation" -v "$work/cache:/cache" -v "$run/tmp:/tmp" \
  ubuntu:22.04 bash /validation/source/contrib/launcher-worker/run-container.sh

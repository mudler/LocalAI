#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
set -euo pipefail
cd /validation/source
source contrib/launcher-worker/lib.sh
trap 's=$?; echo "$s" > /validation/logs/container.exit' EXIT
export DEBIAN_FRONTEND=noninteractive HOME=/validation/home TMPDIR=/tmp
export GOCACHE=/cache/go-build GOPATH=/cache/go GOTOOLCHAIN=local
export GOMAXPROCS=4 GOFLAGS=-p=4
mkdir -p "$HOME" /cache/downloads /validation/toolchain
run_logged /validation/logs/apt-update apt-get update
run_logged /validation/logs/apt-install apt-get install -y --no-install-recommends \
  build-essential pkg-config libgl1-mesa-dev xorg-dev libxkbcommon-dev \
  xvfb xauth curl git python3 ca-certificates unzip libopus-dev
version=$(awk '$1 == "go" {print $2}' go.mod)
[[ $version =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]
archive="go${version}.linux-amd64.tar.gz"
curl -fL --retry 3 'https://go.dev/dl/?mode=json&include=all' -o /cache/downloads/go-releases.json
if [[ ! -f /cache/downloads/$archive ]]; then
  curl -fL --retry 3 "https://go.dev/dl/$archive" -o "/cache/downloads/$archive"
fi
python3 - "$archive" <<'PY'
import hashlib, json, sys
name = sys.argv[1]
with open('/cache/downloads/go-releases.json') as stream:
    releases = json.load(stream)
expected = next(f['sha256'] for r in releases for f in r['files'] if f['filename'] == name)
with open('/cache/downloads/' + name, 'rb') as stream:
    actual = hashlib.sha256(stream.read()).hexdigest()
if actual != expected:
    sys.exit('Go archive checksum mismatch')
print('Verified Go archive SHA256:', actual)
PY
tar -xzf "/cache/downloads/$archive" -C /validation/toolchain
export PATH=/validation/toolchain/go/bin:$GOPATH/bin:$PATH
{ cat /etc/os-release; go version; uname -a; } | tee /validation/logs/environment.txt
run_logged /validation/logs/protogen make protogen-go
run_logged /validation/logs/generate make generate
packages=(cmd/launcher/internal core/cli/workerregistry core/services/worker core/services/nodes)
status=0
tags=debug,p2p
if [[ ${LAUNCHER_VERIFY_MODE:-baseline} == status ]]; then
  tags=debug,p2p,launcherstatus
  run_logged /validation/logs/nats-install go install github.com/nats-io/nats-server/v2@v2.10.26
  run_logged /validation/logs/runtime-build go build -tags "$tags" -o /validation/local-ai ./cmd/local-ai
  run_logged /validation/logs/runtime-capability timeout 10s /validation/local-ai worker-status-capability
  [[ $(cat /validation/logs/runtime-capability.log) == localai-worker-status-v1 ]] || exit 1

fi
if [[ ${LAUNCHER_VERIFY_MODE:-baseline} != baseline ]]; then
  mode=$LAUNCHER_VERIFY_MODE
  focus="Secure launcher configuration"
  [[ $mode != launch ]] || focus="Secure launcher launch"
  packages=(cmd/launcher/internal)
  if [[ $mode == status ]]; then
    focus="Worker status"
    packages=(cmd/launcher/internal core/cli/workerregistry core/services/worker core/services/messaging pkg/workerstatus core/http/endpoints/localai core/http/routes)
  fi
  # A focused report intentionally excludes unrelated specs. The full
  # launcher report below still requires every spec to run.
  report=/validation/logs/launcher-$mode.json
  run_logged /validation/logs/launcher-$mode xvfb-run -a go test -count=1 -timeout=20m \
    -tags "$tags" ./cmd/launcher/internal -args -ginkgo.no-color \
    -ginkgo.focus="$focus" -ginkgo.fail-on-empty \
    -ginkgo.fail-on-pending -ginkgo.json-report="$report" || status=1
  run_logged /validation/logs/launcher-$mode-report python3 contrib/launcher-worker/check-report.py "$report" --$mode-focus || status=1
fi
for package in "${packages[@]}"; do
  name=${package//\//-}
  report="/validation/logs/$name.json"
  if ! run_logged "/validation/logs/$name" xvfb-run -a go test -count=1 -timeout=20m \
      -tags "$tags" "./$package" -args -ginkgo.no-color \
      -ginkgo.fail-on-empty -ginkgo.fail-on-pending -ginkgo.json-report="$report"; then
    status=1
  fi
  run_logged "/validation/logs/$name-report" python3 contrib/launcher-worker/check-report.py "$report" || status=1
done
if [[ ${LAUNCHER_VERIFY_MODE:-baseline} == status ]]; then
  for package in pkg/workerstatus core/cli/workerregistry core/services/worker core/services/messaging cmd/launcher/internal; do
    name=${package//\//-}
    report="/validation/logs/race-$name.json"
    run_logged "/validation/logs/race-$name" xvfb-run -a go test -race -count=1 -timeout=15m -tags "$tags" "./$package" -args -ginkgo.no-color -ginkgo.fail-on-empty -ginkgo.fail-on-pending -ginkgo.json-report="$report" || status=1
    run_logged "/validation/logs/race-$name-report" python3 contrib/launcher-worker/check-report.py "$report" || status=1
  done
fi
exit "$status"

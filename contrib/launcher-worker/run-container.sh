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
for package in "${packages[@]}"; do
  name=${package//\//-}
  report="/validation/logs/$name.json"
  if ! run_logged "/validation/logs/$name" xvfb-run -a go test -count=1 -timeout=20m \
      -tags 'debug,p2p' "./$package" -args -ginkgo.no-color \
      -ginkgo.fail-on-empty -ginkgo.fail-on-pending -ginkgo.json-report="$report"; then
    status=1
  fi
  run_logged "/validation/logs/$name-report" python3 contrib/launcher-worker/check-report.py "$report" || status=1
done
exit "$status"

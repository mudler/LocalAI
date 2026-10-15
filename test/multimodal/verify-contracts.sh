#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
set -euo pipefail
scratch="$PWD/.sdd-work/task2/verify"
mkdir -p pkg/grpc/proto
source test/multimodal/generated-overlay.sh
revision=$(git rev-parse HEAD)
finish() {
 code=$?
 trap - EXIT
 printf 'revision=%s\nexit_status=%s\nevidence=%s\n' "$revision" "$code" "$scratch" > "$scratch/verify.log"
 exit "$code"
}
trap finish EXIT
write_generated_overlay "$scratch"
# The HTTP suite supports an isolated port; avoid unrelated local services.
if [[ -z ${LOCALAI_TEST_HTTP_PORT:-} ]]; then
 export LOCALAI_TEST_HTTP_PORT=$(python3 - <<'PYPORT'
import socket
with socket.socket() as sock:
    sock.bind(('127.0.0.1', 0))
    print(sock.getsockname()[1])
PYPORT
)
fi
: > "$scratch/commands.log"
# Pure Go test server: no native backend or model downloads.
printf '%s\n' "go build -mod=readonly -p 1 -overlay=$scratch/overlay.json -o tests/e2e/mock-backend/mock-backend ./tests/e2e/mock-backend" >> "$scratch/commands.log"
go build -mod=readonly -p 1 -overlay="$scratch/overlay.json" -o tests/e2e/mock-backend/mock-backend ./tests/e2e/mock-backend
for package in core/schema pkg/embeddingmedia core/http/middleware core/http/auth core/http; do
 report=${package//\//-}
 rm -f "$scratch/$report.json"
 # core/http also runs an unrelated gallery spec that downloads a large model;
 # the contract gate runs only the multimodal security group there.
 focus=()
 if [[ $package == core/http ]]; then
  focus=(-ginkgo.focus='Embedding production security')
 fi
 printf '%s\n' "go test -mod=readonly -tags auth -p 1 -overlay=$scratch/overlay.json ./$package -args ${focus[*]} -ginkgo.fail-on-empty -ginkgo.fail-on-pending -ginkgo.json-report=$scratch/$report.json" >> "$scratch/commands.log"
 go test -mod=readonly -tags auth -p 1 -overlay="$scratch/overlay.json" "./$package" -args \
  "${focus[@]}" -ginkgo.fail-on-empty -ginkgo.fail-on-pending -ginkgo.json-report="$scratch/$report.json" 2>&1 | tee "$scratch/$report.log"
done
MM_CONTRACT_TEST_SCRATCH="$scratch" python3 -m unittest discover -s test/multimodal -p test_contract_reports.py -v 2>&1 | tee "$scratch/checker-tests.log"
python3 test/multimodal/check_contract_reports.py "$scratch" "$revision"

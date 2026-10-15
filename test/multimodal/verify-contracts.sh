#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
set -euo pipefail
scratch="$PWD/.sdd-work/task2/verify"
mkdir -p "$scratch/generated" pkg/grpc/proto
revision=$(git rev-parse HEAD)
finish() {
 code=$?
 trap - EXIT
 printf 'revision=%s\nexit_status=%s\nevidence=%s\n' "$revision" "$code" "$scratch" > "$scratch/verify.log"
 exit "$code"
}
trap finish EXIT
protoc --experimental_allow_proto3_optional -Ibackend/ \
 --go_out="$scratch/generated" --go_opt=paths=source_relative \
 --go-grpc_out="$scratch/generated" --go-grpc_opt=paths=source_relative backend/backend.proto
# pkg/motion/proto/motion.pb.go is generated and gitignored; build it the same way.
protoc -Ipkg/motion/proto --go_out="$scratch/generated" --go_opt=paths=source_relative pkg/motion/proto/motion.proto
python3 - "$scratch" <<'PY'
import json,sys
from pathlib import Path
scratch=Path(sys.argv[1]); root=Path.cwd()
replacements = {str(root/'pkg/grpc/proto'/name): str(scratch/'generated'/name)
                for name in ('backend.pb.go','backend_grpc.pb.go')}
# Compile-only embed fixture, not a UI build. Keep it outside the source tree.
fixture = scratch/'index.html'
fixture.write_text('<!doctype html><html><head><script src="/assets/contracts-12345678.js"></script></head><body><div id="root"></div></body></html>\n')
asset = scratch/'contracts-12345678.js'
asset.write_text('// Compile-only HTTP static-handler fixture, not a React build.\n')
replacements[str(root/'core/http/react-ui/dist/assets/contracts-12345678.js')] = str(asset)
replacements[str(root/'core/http/react-ui/dist/index.html')] = str(fixture)
replacements[str(root/'pkg/motion/proto/motion.pb.go')] = str(scratch/'generated'/'motion.pb.go')
(scratch/'overlay.json').write_text(json.dumps({'Replace': replacements}))
PY
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
 printf '%s\n' "go test -mod=readonly -tags auth -p 1 -overlay=$scratch/overlay.json ./$package -args -ginkgo.fail-on-empty -ginkgo.fail-on-pending -ginkgo.json-report=$scratch/$report.json" >> "$scratch/commands.log"
 go test -mod=readonly -tags auth -p 1 -overlay="$scratch/overlay.json" "./$package" -args \
  -ginkgo.fail-on-empty -ginkgo.fail-on-pending -ginkgo.json-report="$scratch/$report.json" 2>&1 | tee "$scratch/$report.log"
done
MM_CONTRACT_TEST_SCRATCH="$scratch" python3 -m unittest discover -s test/multimodal -p test_contract_reports.py -v 2>&1 | tee "$scratch/checker-tests.log"
python3 test/multimodal/check_contract_reports.py "$scratch" "$revision"

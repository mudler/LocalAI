#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# Called by verify.sh; use installed generators only (never install tooling).
set -euo pipefail
scratch="${MM_ROUTING_SCRATCH:-.sdd-work/task5/verify}"
# Resolve before creating anything; reject escapes, including symlink parents.
python3 - "$scratch" <<'PYGUARD'
from pathlib import Path
import sys
root = Path.cwd()
allowed = root / '.sdd-work'
target = Path(sys.argv[1]).resolve()
if allowed.resolve() != allowed or not target.is_relative_to(allowed) or target == allowed:
    raise SystemExit('routing scratch must be strictly beneath the worktree .sdd-work')
PYGUARD
mkdir -p "$scratch" pkg/grpc/proto
scratch=$(cd "$scratch" && pwd)
revision=$(git rev-parse HEAD)
finish() {
    code=$?
    trap - EXIT
    printf 'revision=%s\nexit_status=%s\nevidence=%s\n' "$revision" "$code" "$scratch" > "$scratch/verify.log"
    exit "$code"
}
trap finish EXIT
exec > >(tee "$scratch/commands.log") 2>&1
set -x
[[ $(protoc --version) == 'libprotoc 3.21.12' ]]
[[ $(protoc-gen-go --version) == 'protoc-gen-go v1.34.2' ]]
[[ $(protoc-gen-go-grpc --version) == 'protoc-gen-go-grpc 1.4.0' ]]
for dir in generated regenerated; do
    mkdir -p "$scratch/$dir"
    protoc --experimental_allow_proto3_optional -Ibackend/ \
        --go_out="$scratch/$dir" --go_opt=paths=source_relative \
        --go-grpc_out="$scratch/$dir" --go-grpc_opt=paths=source_relative backend/backend.proto
done
cmp "$scratch/generated/backend.pb.go" "$scratch/regenerated/backend.pb.go"
cmp "$scratch/generated/backend_grpc.pb.go" "$scratch/regenerated/backend_grpc.pb.go"
python3 - "$scratch" <<'PY'
import json
from pathlib import Path
import sys
scratch = Path(sys.argv[1])
root = Path.cwd()
(scratch / 'overlay.json').write_text(json.dumps({'Replace': {
    str(root / 'pkg/grpc/proto' / name): str(scratch / 'generated' / name)
    for name in ('backend.pb.go', 'backend_grpc.pb.go')
}}))
PY
# Each suite gets its own report: go test does not merge Ginkgo JSON reports.
for package in core/backend pkg/grpc pkg/model core/services/nodes; do
    report=${package//\//-}
    go test -race -timeout 15m -p 1 -overlay="$scratch/overlay.json" "./$package" -count=1 \
        -ginkgo.fail-on-empty -ginkgo.fail-on-pending \
        -ginkgo.json-report="$scratch/$report.json"
done
# base contains production code but no test suite; compile it explicitly.
go test -race -timeout 15m -p 1 -overlay="$scratch/overlay.json" ./pkg/grpc/base
python3 test/multimodal/check_routing_reports.py "$scratch" "$revision"

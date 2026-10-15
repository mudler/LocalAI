#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# HTTP orchestration of structured embeddings. Pure Go: a fake gRPC peer and a
# fake acquirer stand in for the backend and the network; no download, no
# native build. Called by verify.sh.
set -euo pipefail
scratch="${MM_HTTP_SCRATCH:-.sdd-work/task6/verify}"
# Resolve before creating anything; reject escapes, including symlink parents.
python3 -I - "$scratch" <<'PYGUARD'
from pathlib import Path
import sys
root = Path.cwd()
allowed = root / '.sdd-work'
target = Path(sys.argv[1]).resolve()
if allowed.resolve() != allowed or not target.is_relative_to(allowed) or target == allowed:
    raise SystemExit('http scratch must be strictly beneath the worktree .sdd-work')
PYGUARD
mkdir -p "$scratch"
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
rm -f "$scratch"/*.json
set -x
# Each suite gets its own report: go test does not merge Ginkgo JSON reports.
run() {
    local name=$1 package=$2
    shift 2
    go test "$@" -race -timeout 15m -p 1 "./$package" -count=1 \
        -ginkgo.fail-on-empty -ginkgo.fail-on-pending \
        -ginkgo.json-report="$scratch/$name.json"
}
run core-http-endpoints-openai core/http/endpoints/openai
run core-http-middleware core/http/middleware
run core-http-auth core/http/auth -tags auth
run core-backend core/backend
MM_HTTP_TEST_SCRATCH="$scratch" python3 -I -m unittest discover -s test/multimodal \
    -p test_http_reports.py -v
python3 -I test/multimodal/check_http_reports.py "$scratch" "$revision" "bash test/multimodal/verify.sh http"

#!/bin/sh
# SPDX-License-Identifier: MIT
# Runs the Tables tool checks and prints one JSON case line per test:
#   1. the node unit tests of the proposal card rules (reference parsing, the
#      stored history, the facts the card draws, the confirmation call);
#   2. the Go specs of the tool adapter (discovery, forged fields, approval
#      aliases, the executors, the consumer matrix), of the pending proposal
#      list across the owner and forwarding frontends, and of the real owner
#      with the real Tables executable (tool calls to proposals to
#      confirmation).
# The browser spec of the card went with the old chat page; the chats do not
# draw the card yet.
# verify.sh compares the lines with the exact inventory of the tables-tools
# suite and rejects a skipped, flaky or missing case.
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$root"
cache="$root/.cache/addons"
ui="$root/core/http/react-ui"
mkdir -p "$cache/tmp" "$cache/logs"
export TMPDIR="$cache/tmp" PLAYWRIGHT_BROWSERS_PATH="$cache/browsers"
export PATH="$cache/bin:$PATH"
export GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOFLAGS=-mod=readonly
export GOPATH="$cache/go" GOMODCACHE="$cache/go/pkg/mod" GOCACHE="$cache/go-build"
for file in tests/e2e-ui/ui-test-server tests/e2e/mock-backend/mock-backend core/http/react-ui/node_modules/.bin/playwright; do
    [ -x "$file" ] || { echo "missing $file; run scripts/addons/prepare.sh --integration" >&2; exit 1; }
done
# The server embeds the UI build and the mock model embeds its script
# markers: a build made before the last change would test the old code.
[ "$root/tests/e2e-ui/ui-test-server" -nt "$ui/dist/index.html" ] || { echo 'ui-test-server is older than the UI build; run scripts/addons/prepare.sh --integration' >&2; exit 1; }
[ "$root/tests/e2e/mock-backend/mock-backend" -nt "$root/tests/e2e/mock-backend/main.go" ] || { echo 'mock-backend is older than its source; run scripts/addons/prepare.sh --integration' >&2; exit 1; }
[ "$root/tests/e2e-ui/ui-test-server" -nt "$root/core/services/addons/tools/adapter.go" ] || { echo 'ui-test-server is older than the adapter; run scripts/addons/prepare.sh --integration' >&2; exit 1; }

# 1. Pure card, history and chat action rules.
(cd "$ui" && node tests/unit/run.mjs tablestools.test.mjs)

# 2. Go: ginkgo specs reported as case lines. Specs that a focus filtered out
# are not part of the run and are not printed; a failed or skipped spec is.
go_cases() {
    python3 -I - "$1" <<'PY'
import json, sys
for report in json.load(open(sys.argv[1])):
    for spec in report['SpecReports']:
        if spec['LeafNodeType'] != 'It' or spec['State'] == 'skipped' and not spec.get('Failure'):
            continue
        status = {'passed': 'passed'}.get(spec['State'], 'failed')
        print(json.dumps({'case': 'go: ' + spec['LeafNodeText'], 'status': status}))
PY
}
run_go() {
    name=$1
    shift
    report="$cache/logs/tables-tools-$name.json"
    rm -f "$report"
    go test -count=1 -p 2 -tags auth "$@" -ginkgo.json-report="$report" >"$cache/logs/tables-tools-$name.log" 2>&1 || { tail -40 "$cache/logs/tables-tools-$name.log" >&2; exit 1; }
    go_cases "$report"
}
run_go adapter ./core/services/addons/tools
run_go pending ./core/http/endpoints/addons -ginkgo.focus='lists the caller.s own pending proposals'
run_go owner ./tests/addons/tables -ginkgo.focus='Tables tools under the real owner'

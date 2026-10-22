#!/bin/sh
# SPDX-License-Identifier: MIT
# Runs the Tables tool checks and prints one JSON case line per test:
#   1. the node unit tests of the proposal card rules (reference parsing, the
#      stored history, the facts the card draws, the confirmation call);
#   2. the Go specs of the tool adapter (discovery, forged fields, approval
#      aliases, the executors, the consumer matrix), of the pending proposal
#      list across the owner and forwarding frontends, and of the real owner
#      with the real Tables executable (tool calls to proposals to
#      confirmation);
#   3. the Playwright spec against the UI test server running with real
#      authentication, the real owner, the real Tables executable and a
#      scripted model that answers with the tool call a spec encodes.
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
run_go cli ./core/cli -ginkgo.focus='mcpJobSessionOption'
run_go pending ./core/http/endpoints/addons -ginkgo.focus='lists the caller.s own pending proposals'
run_go owner ./tests/addons/tables -ginkgo.focus='Tables tools under the real owner'

# 3. The real owner, the real Tables executable and the browser.
tables_exe="$cache/tmp/tables-exe"
CGO_ENABLED=1 go build -p 2 -o "$tables_exe" ./addons/tables/cmd/tables
fixture_dir="$cache/tmp/tables-tools-fixture"
rm -rf "$fixture_dir"
mkdir -p "$fixture_dir/tables"
cp "$root/addons/tables/manifest.json" "$fixture_dir/tables/manifest.json"
# The manifest declares the pages of the package, so the package carries them.
node "$root/addons/tables/ui/build.mjs" >"$cache/logs/tables-tools-ui-build.log" 2>&1 || { cat "$cache/logs/tables-tools-ui-build.log" >&2; exit 1; }
cp -R "$root/addons/tables/ui/dist/ui" "$fixture_dir/tables/ui"
port=$(python3 -I -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1])')
creds="$cache/logs/tables-tools-credentials.json"
rm -f "$creds"
log="$cache/logs/tables-tools-server.log"
"$root/tests/e2e-ui/ui-test-server" --mock-backend="$root/tests/e2e/mock-backend/mock-backend" --port="$port" \
    --addons-fixture="$fixture_dir" --addon-executable="$tables_exe" --fixture-out="$creds" \
    --fixture-enable=tables@0.1.0 >"$log" 2>&1 &
server=$!
trap 'kill "$server" 2>/dev/null || true; wait "$server" 2>/dev/null || true; rm -rf "$fixture_dir"' EXIT HUP INT TERM
i=0
until [ -s "$creds" ] && python3 -I -c "import sys,urllib.request;urllib.request.urlopen('http://127.0.0.1:$port/readyz',timeout=1)" 2>/dev/null; do
    i=$((i + 1))
    kill -0 "$server" 2>/dev/null || { echo 'UI test server exited' >&2; tail -20 "$log" >&2; exit 1; }
    [ "$i" -lt 120 ] || { echo 'UI test server readiness timed out' >&2; exit 1; }
    sleep 1
done
report="$cache/logs/tables-tools-report.json"
rm -f "$report"
status=0
(cd "$ui" && ADDONS_UI_URL="http://127.0.0.1:$port" ADDONS_FIXTURE="$creds" ADDONS_UI_REPORT="$report" ./node_modules/.bin/playwright test --config playwright.tools.config.js >"$cache/logs/tables-tools-playwright.log" 2>&1) || status=$?
python3 -I "$root/scripts/addons/ui_cases.py" playwright "$report" || status=1
exit "$status"

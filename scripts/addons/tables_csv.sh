#!/bin/sh
# SPDX-License-Identifier: MIT
# Runs the Tables CSV export checks and prints one JSON case line per test:
#   1. the node unit tests of the file card rules and of the export call of
#      the Tables page;
#   2. the Go specs of the CSV store (snapshot, quoting, formula guard,
#      bounds), of the runtime output handles, of the real owner with the real
#      Tables executable (private artifacts, failures, expiry, quota) and of
#      the owner with two real frontend processes (identical bytes and
#      headers from every replica);
#   3. the Playwright spec against two UI test servers that run the real
#      owner, the real Tables executable and the real built UI package: one
#      with named accounts and one in local-owner mode.
# verify.sh compares the lines with the exact inventory of the tables-csv
# suite and rejects a skipped, flaky or missing case.
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$root"
cache="$root/.cache/addons"
ui="$root/core/http/react-ui"
pkg="$root/addons/tables/ui"
mkdir -p "$cache/tmp" "$cache/logs"
export TMPDIR="$cache/tmp" PLAYWRIGHT_BROWSERS_PATH="$cache/browsers"
export PATH="$cache/bin:$PATH"
export GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOFLAGS=-mod=readonly
export GOPATH="$cache/go" GOMODCACHE="$cache/go/pkg/mod" GOCACHE="$cache/go-build"
for file in tests/e2e-ui/ui-test-server tests/e2e/mock-backend/mock-backend core/http/react-ui/node_modules/.bin/playwright; do
    [ -x "$file" ] || { echo "missing $file; run scripts/addons/prepare.sh --integration" >&2; exit 1; }
done
# The server embeds the UI build: a build made before the last change would
# test the old host code.
[ "$root/tests/e2e-ui/ui-test-server" -nt "$ui/dist/index.html" ] || { echo 'ui-test-server is older than the UI build; run scripts/addons/prepare.sh --integration' >&2; exit 1; }
[ "$root/tests/e2e-ui/ui-test-server" -nt "$root/core/services/addons/resources/service.go" ] || { echo 'ui-test-server is older than the resource service; run scripts/addons/prepare.sh --integration' >&2; exit 1; }
[ "$root/tests/e2e/mock-backend/mock-backend" -nt "$root/tests/e2e/mock-backend/main.go" ] || { echo 'mock-backend is older than its source; run scripts/addons/prepare.sh --integration' >&2; exit 1; }
[ "$root/tests/e2e-ui/ui-test-server" -nt "$root/tests/e2e-ui/addons_fixture.go" ] || { echo 'ui-test-server is older than its fixture; run scripts/addons/prepare.sh --integration' >&2; exit 1; }

# 1. Pure rules: the file card and the export call.
(cd "$ui" && node tests/unit/run.mjs tablesartifacts.test.mjs)

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
    report="$cache/logs/tables-csv-$name.json"
    rm -f "$report"
    go test -count=1 -p 2 -tags auth "$@" -ginkgo.json-report="$report" >"$cache/logs/tables-csv-$name.log" 2>&1 || { tail -40 "$cache/logs/tables-csv-$name.log" >&2; exit 1; }
    go_cases "$report"
}
run_go store ./addons/tables/internal/store -ginkgo.focus='CSV export'
run_go server ./addons/tables/internal/server
run_go owner ./tests/addons/tablesadmin -ginkgo.focus='CSV export'
FOUNDATION_SERVER="$root/tests/e2e-ui/ui-test-server" run_go replicas ./tests/addons/tablesexport

# 3. The real owner, the real Tables executable, the built package.
node "$pkg/build.mjs" >"$cache/logs/tables-csv-build.log" 2>&1 || { cat "$cache/logs/tables-csv-build.log" >&2; exit 1; }
tables_exe="$cache/tmp/tables-csv-exe"
CGO_ENABLED=1 go build -p 2 -o "$tables_exe" ./addons/tables/cmd/tables
fixture_dir="$cache/tmp/tables-csv-fixture"
rm -rf "$fixture_dir"
mkdir -p "$fixture_dir/tables"
cp "$root/addons/tables/manifest.json" "$fixture_dir/tables/manifest.json"
cp -R "$pkg/dist/ui" "$fixture_dir/tables/ui"

free_port() { python3 -I -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1])'; }
named_port=$(free_port)
local_port=$(free_port)
creds="$cache/logs/tables-csv-credentials.json"
local_creds="$cache/logs/tables-csv-local-credentials.json"
rm -f "$creds" "$local_creds"
trap 'kill "$named" "$localsrv" 2>/dev/null || true; wait "$named" "$localsrv" 2>/dev/null || true; rm -rf "$fixture_dir"' EXIT HUP INT TERM
named=0
localsrv=0
"$root/tests/e2e-ui/ui-test-server" --mock-backend="$root/tests/e2e/mock-backend/mock-backend" --port="$named_port" \
    --addons-fixture="$fixture_dir" --addon-executable="$tables_exe" --fixture-out="$creds" \
    --fixture-enable=tables@0.1.0 >"$cache/logs/tables-csv-server.log" 2>&1 &
named=$!
"$root/tests/e2e-ui/ui-test-server" --mock-backend="$root/tests/e2e/mock-backend/mock-backend" --port="$local_port" \
    --addons-fixture="$fixture_dir" --addon-executable="$tables_exe" --fixture-out="$local_creds" \
    --addons-local-owner --fixture-enable=tables@0.1.0 >"$cache/logs/tables-csv-local-server.log" 2>&1 &
localsrv=$!
wait_for() {
    file=$1
    port=$2
    pid=$3
    i=0
    until [ -s "$file" ] && python3 -I -c "import sys,urllib.request;urllib.request.urlopen('http://127.0.0.1:$port/readyz',timeout=1)" 2>/dev/null; do
        i=$((i + 1))
        kill -0 "$pid" 2>/dev/null || { echo 'UI test server exited' >&2; tail -20 "$cache/logs/tables-csv-server.log" "$cache/logs/tables-csv-local-server.log" >&2; exit 1; }
        [ "$i" -lt 180 ] || { echo 'UI test server readiness timed out' >&2; exit 1; }
        sleep 1
    done
}
wait_for "$creds" "$named_port" "$named"
wait_for "$local_creds" "$local_port" "$localsrv"
report="$cache/logs/tables-csv-report.json"
rm -f "$report"
status=0
(cd "$ui" && ADDONS_UI_URL="http://127.0.0.1:$named_port" ADDONS_LOCAL_URL="http://127.0.0.1:$local_port" ADDONS_FIXTURE="$creds" \
    ADDONS_UI_REPORT="$report" ./node_modules/.bin/playwright test --config playwright.csv.config.js >"$cache/logs/tables-csv-playwright.log" 2>&1) || status=$?
python3 -I "$root/scripts/addons/ui_cases.py" playwright "$report" || status=1
exit "$status"

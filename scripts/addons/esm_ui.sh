#!/bin/sh
# SPDX-License-Identifier: MIT
# Runs the add-on UI runtime checks and prints one JSON case line per test:
#   1. the node unit tests of the SDK, loader, style scoping and registry;
#   2. the Go specs of the immutable asset route, the apps UI entry and the
#      owner's lifecycle rules, through real stored keys and sessions;
#   3. the Playwright spec against the UI test server running with real
#      authentication and a real add-on owner, which installs the packages of
#      tests/addons/fixture/ui AFTER the host build, from a catalog on
#      loopback, with the protocol fixture as their process.
# verify.sh compares the lines with the exact inventory of the esm-ui suite and
# rejects a skipped, flaky or missing case.
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

# 1. Pure SDK, loader, style scoping and registry rules.
(cd "$ui" && node tests/unit/run.mjs esm.test.mjs)

# 2. Go: ginkgo specs of this feature, reported as case lines. The label
# "assets" selects them; every other spec of these packages belongs to the
# suites that own them.
i=0
for pkg in ./core/services/addons/apps ./core/services/addons ./core/services/addons/gateway ./core/http/endpoints/addons; do
    i=$((i + 1))
    go_report="$cache/logs/esm-ui-go-$i.json"
    rm -f "$go_report"
    go test -count=1 -p 2 -tags auth "$pkg" -ginkgo.label-filter=assets -ginkgo.json-report="$go_report" >"$cache/logs/esm-ui-go-$i.log" 2>&1 || { tail -40 "$cache/logs/esm-ui-go-$i.log" >&2; exit 1; }
    python3 -I - "$go_report" <<'PY'
import json, sys
for report in json.load(open(sys.argv[1])):
    for spec in report['SpecReports']:
        if spec['LeafNodeType'] != 'It':
            continue
        labels = set(spec.get('LeafNodeLabels') or [])
        for group in spec.get('ContainerHierarchyLabels') or []:
            labels.update(group or [])
        # A spec the label filter left out is another suite's, not skipped here.
        if 'assets' not in labels:
            continue
        status = {'passed': 'passed', 'skipped': 'skipped'}.get(spec['State'], 'failed')
        print(json.dumps({'case': 'go: ' + spec['LeafNodeText'], 'status': status}))
PY
done

# 3. The real owner and the browser.
fixture_exe="$cache/tmp/fixture-addon"
go build -p 2 -tags auth -o "$fixture_exe" ./tests/addons/fixture/cmd
port=$(python3 -I -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1])')
creds="$cache/logs/esm-ui-credentials.json"
rm -f "$creds"
log="$cache/logs/esm-ui-server.log"
"$root/tests/e2e-ui/ui-test-server" --mock-backend="$root/tests/e2e/mock-backend/mock-backend" --port="$port" \
    --addons-fixture="$root/tests/addons/fixture/ui" --addon-executable="$fixture_exe" --fixture-out="$creds" \
    --fixture-enable=uifixture@1.0.0,cssb@1.0.0,bare@1.0.0 >"$log" 2>&1 &
server=$!
trap 'kill "$server" 2>/dev/null || true; wait "$server" 2>/dev/null || true' EXIT HUP INT TERM
i=0
until [ -s "$creds" ] && python3 -I -c "import sys,urllib.request;urllib.request.urlopen('http://127.0.0.1:$port/readyz',timeout=1)" 2>/dev/null; do
    i=$((i + 1))
    kill -0 "$server" 2>/dev/null || { echo 'UI test server exited' >&2; tail -20 "$log" >&2; exit 1; }
    [ "$i" -lt 120 ] || { echo 'UI test server readiness timed out' >&2; exit 1; }
    sleep 1
done
report="$cache/logs/esm-ui-report.json"
rm -f "$report"
status=0
(cd "$ui" && ADDONS_UI_URL="http://127.0.0.1:$port" ADDONS_FIXTURE="$creds" ADDONS_UI_REPORT="$report" ./node_modules/.bin/playwright test --config playwright.esm.config.js >"$cache/logs/esm-ui-playwright.log" 2>&1) || status=$?
python3 -I "$root/scripts/addons/ui_cases.py" playwright "$report" || status=1
exit "$status"

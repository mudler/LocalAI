#!/bin/sh
# SPDX-License-Identifier: MIT
# Runs the host UI slot checks and prints one JSON case line per test:
#   1. the node unit tests of the slot registry, the loader's component
#      validation, the approval frame rules and the SDK approvals call;
#   2. the Go specs of the manifest declarations (unknown slot, unknown
#      placement, remote icon, undeclared tool, duplicates, markup, bounds) and
#      of the apps list that carries them;
#   3. the Playwright spec against the UI test server running with real
#      authentication and a real add-on owner, which installs the "slots"
#      fixture package AFTER the host build, from a catalog on loopback.
# verify.sh compares the lines with the exact inventory of the host-slots suite
# and rejects a skipped, flaky or missing case.
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
# The server embeds the UI build: a build made before the last UI change would
# test the old pages.
[ "$root/tests/e2e-ui/ui-test-server" -nt "$ui/dist/index.html" ] || { echo 'ui-test-server is older than the UI build; run scripts/addons/prepare.sh --integration' >&2; exit 1; }

# 1. Pure slot, loader, approval and SDK rules.
(cd "$ui" && node tests/unit/run.mjs slots.test.mjs)

# 2. Go: ginkgo specs of the declarations, reported as case lines.
go_report="$cache/logs/host-slots-go.json"
rm -f "$go_report"
go test -count=1 -p 2 -tags auth ./tests/addons/hostslots -ginkgo.json-report="$go_report" >"$cache/logs/host-slots-go.log" 2>&1 || { tail -40 "$cache/logs/host-slots-go.log" >&2; exit 1; }
python3 -I - "$go_report" <<'PY'
import json, sys
for report in json.load(open(sys.argv[1])):
    for spec in report['SpecReports']:
        if spec['LeafNodeType'] != 'It':
            continue
        status = {'passed': 'passed', 'skipped': 'skipped'}.get(spec['State'], 'failed')
        print(json.dumps({'case': 'go: ' + spec['LeafNodeText'], 'status': status}))
PY

# 3. The real owner and the browser.
fixture_exe="$cache/tmp/fixture-addon"
go build -p 2 -tags auth -o "$fixture_exe" ./tests/addons/fixture/cmd
port=$(python3 -I -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1])')
creds="$cache/logs/host-slots-credentials.json"
rm -f "$creds"
log="$cache/logs/host-slots-server.log"
"$root/tests/e2e-ui/ui-test-server" --mock-backend="$root/tests/e2e/mock-backend/mock-backend" --port="$port" \
    --addons-fixture="$root/tests/addons/fixture/ui" --addon-executable="$fixture_exe" --fixture-out="$creds" \
    --fixture-enable=slots@1.0.0 >"$log" 2>&1 &
server=$!
trap 'kill "$server" 2>/dev/null || true; wait "$server" 2>/dev/null || true' EXIT HUP INT TERM
i=0
until [ -s "$creds" ] && python3 -I -c "import sys,urllib.request;urllib.request.urlopen('http://127.0.0.1:$port/readyz',timeout=1)" 2>/dev/null; do
    i=$((i + 1))
    kill -0 "$server" 2>/dev/null || { echo 'UI test server exited' >&2; tail -20 "$log" >&2; exit 1; }
    [ "$i" -lt 120 ] || { echo 'UI test server readiness timed out' >&2; exit 1; }
    sleep 1
done
report="$cache/logs/host-slots-report.json"
rm -f "$report"
status=0
(cd "$ui" && ADDONS_UI_URL="http://127.0.0.1:$port" ADDONS_FIXTURE="$creds" ADDONS_UI_REPORT="$report" ./node_modules/.bin/playwright test --config playwright.slots.config.js >"$cache/logs/host-slots-playwright.log" 2>&1) || status=$?
python3 -I "$root/scripts/addons/ui_cases.py" playwright "$report" || status=1
exit "$status"

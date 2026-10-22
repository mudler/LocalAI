#!/bin/sh
# SPDX-License-Identifier: MIT
# Runs the add-on data recovery checks and prints one JSON case line per test:
#   1. the node unit tests of the recovery rules, the typed client and the
#      locale keys of the panel;
#   2. the Go specs of the recovery package (WAL-consistent backup, corrupt and
#      oversized backups, cancellation, retention, every journal phase of an
#      upgrade and of a restore, grant resurrection, purge), of the policy
#      (hold, restore filter, revocation log, ownership migration), of the
#      Tables store (format migration) and of the real owner with the real
#      Tables executable over real HTTP (typed refusals, owner and frontend
#      parity, upgrade with data, reinstall identity, documentation);
#   3. the Playwright spec against a UI test server that runs the real owner,
#      the real Tables executable and the real built UI package.
# verify.sh compares the lines with the exact inventory of the recovery suite
# and rejects a skipped, flaky or missing case.
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
for src in "$ui/src/components/addons/RecoveryPanel.jsx" "$ui/src/components/addons/recoveryState.js" "$ui/src/utils/addonsApi.js"; do
    [ "$ui/dist/index.html" -nt "$src" ] || { echo "the UI build is older than $src; run scripts/addons/prepare.sh --integration" >&2; exit 1; }
done
[ "$root/tests/e2e-ui/ui-test-server" -nt "$root/core/services/addons/recovery/backup.go" ] || { echo 'ui-test-server is older than the recovery package; run scripts/addons/prepare.sh --integration' >&2; exit 1; }
[ "$root/tests/e2e-ui/ui-test-server" -nt "$root/core/http/endpoints/addons/recovery.go" ] || { echo 'ui-test-server is older than the recovery routes; run scripts/addons/prepare.sh --integration' >&2; exit 1; }
[ "$root/tests/e2e/mock-backend/mock-backend" -nt "$root/tests/e2e/mock-backend/main.go" ] || { echo 'mock-backend is older than its source; run scripts/addons/prepare.sh --integration' >&2; exit 1; }

# 1. Pure rules, the typed client and the locale keys.
(cd "$ui" && node tests/unit/run.mjs recovery.test.mjs)

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
    report="$cache/logs/recovery-$name.json"
    rm -f "$report"
    go test -count=1 -p 2 -tags auth "$@" -ginkgo.json-report="$report" >"$cache/logs/recovery-$name.log" 2>&1 || { tail -40 "$cache/logs/recovery-$name.log" >&2; exit 1; }
    go_cases "$report"
}
run_go package ./core/services/addons/recovery
run_go policy ./core/services/addons/policy -ginkgo.focus='Addon recovery state of the policy'
run_go store ./addons/tables/internal/store -ginkgo.focus='Format migration'
run_go owner ./tests/addons/recovery

# 3. The real owner, the real Tables executable, the built package.
node "$pkg/build.mjs" >"$cache/logs/recovery-build.log" 2>&1 || { cat "$cache/logs/recovery-build.log" >&2; exit 1; }
tables_exe="$cache/tmp/recovery-exe"
CGO_ENABLED=1 go build -p 2 -o "$tables_exe" ./addons/tables/cmd/tables
fixture_dir="$cache/tmp/recovery-fixture"
rm -rf "$fixture_dir"
mkdir -p "$fixture_dir/tables"
cp "$root/addons/tables/manifest.json" "$fixture_dir/tables/manifest.json"
cp -R "$pkg/dist/ui" "$fixture_dir/tables/ui"

free_port() { python3 -I -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1])'; }
named_port=$(free_port)
creds="$cache/logs/recovery-credentials.json"
rm -f "$creds"
shots=${RECOVERY_SHOTS:-}
trap 'kill "$named" 2>/dev/null || true; wait "$named" 2>/dev/null || true; rm -rf "$fixture_dir" "$tables_exe"' EXIT HUP INT TERM
named=0
"$root/tests/e2e-ui/ui-test-server" --mock-backend="$root/tests/e2e/mock-backend/mock-backend" --port="$named_port" \
    --addons-fixture="$fixture_dir" --addon-executable="$tables_exe" --fixture-out="$creds" \
    --fixture-enable=tables@0.1.0 >"$cache/logs/recovery-server.log" 2>&1 &
named=$!
i=0
until [ -s "$creds" ] && python3 -I -c "import urllib.request;urllib.request.urlopen('http://127.0.0.1:$named_port/readyz',timeout=1)" 2>/dev/null; do
    i=$((i + 1))
    kill -0 "$named" 2>/dev/null || { echo 'UI test server exited' >&2; tail -20 "$cache/logs/recovery-server.log" >&2; exit 1; }
    [ "$i" -lt 180 ] || { echo 'UI test server readiness timed out' >&2; exit 1; }
    sleep 1
done
report="$cache/logs/recovery-report.json"
rm -f "$report"
status=0
(cd "$ui" && ADDONS_UI_URL="http://127.0.0.1:$named_port" ADDONS_FIXTURE="$creds" ADDONS_SHOTS="$shots" \
    ADDONS_UI_REPORT="$report" ./node_modules/.bin/playwright test --config playwright.recovery.config.js >"$cache/logs/recovery-playwright.log" 2>&1) || status=$?
python3 -I "$root/scripts/addons/ui_cases.py" playwright "$report" || status=1
exit "$status"

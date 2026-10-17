#!/bin/sh
# SPDX-License-Identifier: MIT
# Runs the add-on update detection and upgrade checks and prints one JSON case
# line per test:
#   1. the node unit tests of the update rules, the typed client and the
#      locale keys;
#   2. the Go specs of the checker (moving channels, immutable versions,
#      compatibility, failed checks, cache bounds, rate limit), of the
#      catalog reads it uses, of the real lifecycle manager (pinned upgrade,
#      duplicate and conflicting operations, last good generation) and of the
#      HTTP routes over the owner and two forwarding frontends;
#   3. the Playwright spec against a UI test server that runs the real owner,
#      the real Tables executable and the real built UI package, with a
#      library whose channel the spec moves.
# verify.sh compares the lines with the exact inventory of the updates suite
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
for src in "$ui/src/components/addons/InstalledView.jsx" "$ui/src/components/addons/updateState.js" "$ui/src/components/addons/AppsView.jsx" "$ui/src/utils/addonsApi.js"; do
    [ "$ui/dist/index.html" -nt "$src" ] || { echo "the UI build is older than $src; run scripts/addons/prepare.sh --integration" >&2; exit 1; }
done
for src in core/services/addons/updates/checker.go core/services/addons/updates.go core/http/endpoints/addons/updates.go tests/e2e-ui/addons_fixture.go; do
    [ "$root/tests/e2e-ui/ui-test-server" -nt "$root/$src" ] || { echo "ui-test-server is older than $src; run scripts/addons/prepare.sh --integration" >&2; exit 1; }
done
[ "$root/tests/e2e/mock-backend/mock-backend" -nt "$root/tests/e2e/mock-backend/main.go" ] || { echo 'mock-backend is older than its source; run scripts/addons/prepare.sh --integration' >&2; exit 1; }

# 1. Pure rules, the typed client and the locale keys.
(cd "$ui" && node tests/unit/run.mjs updates.test.mjs)

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
    report="$cache/logs/updates-$name.json"
    rm -f "$report"
    go test -count=1 -p 2 -tags auth "$@" -ginkgo.json-report="$report" >"$cache/logs/updates-$name.log" 2>&1 || { tail -40 "$cache/logs/updates-$name.log" >&2; exit 1; }
    go_cases "$report"
}
run_go checker ./core/services/addons/updates
run_go catalog ./core/services/addons/catalog -ginkgo.focus='what the update check reads'
run_go manager ./core/services/addons -ginkgo.focus='explicit upgrade from an authoritative update check'
run_go routes ./core/http/endpoints/addons -ginkgo.focus='update routes'

# 3. The real owner, the real Tables executable, the built package. The
# fixture library has one moving channel: each directory is one generation of
# it, with the same version label and other bytes. The last generation asks
# for a host this one is not.
node "$pkg/build.mjs" >"$cache/logs/updates-build.log" 2>&1 || { cat "$cache/logs/updates-build.log" >&2; exit 1; }
tables_exe="$cache/tmp/updates-exe"
CGO_ENABLED=1 go build -p 2 -o "$tables_exe" ./addons/tables/cmd/tables
fixture_dir="$cache/tmp/updates-fixture"
rm -rf "$fixture_dir"
mkdir -p "$fixture_dir"
for n in 1 2 3 4 5; do
    d="$fixture_dir/tables-dev$n"
    mkdir -p "$d"
    python3 -I - "$root/addons/tables/manifest.json" "$d/manifest.json" <<'PY'
import json, sys
m = json.load(open(sys.argv[1]))
m['version'] = '0.1.0-dev'
json.dump(m, open(sys.argv[2], 'w'))
PY
    cp -R "$pkg/dist/ui" "$d/ui"
    printf 'build %s\n' "$n" >"$d/build.txt"
    printf 'development\n' >"$d/.fixture-channel"
    printf '%s\n' "$n" >"$d/.fixture-generation"
done
printf '{"host":{"min":9,"max":9}}\n' >"$fixture_dir/tables-dev5/.fixture-requires"

free_port() { python3 -I -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1])'; }
named_port=$(free_port)
creds="$cache/logs/updates-credentials.json"
rm -f "$creds"
shots=${UPDATES_SHOTS:-}
trap 'kill "$named" 2>/dev/null || true; wait "$named" 2>/dev/null || true; rm -rf "$fixture_dir" "$tables_exe"' EXIT HUP INT TERM
named=0
"$root/tests/e2e-ui/ui-test-server" --mock-backend="$root/tests/e2e/mock-backend/mock-backend" --port="$named_port" \
    --addons-fixture="$fixture_dir" --addon-executable="$tables_exe" --fixture-out="$creds" \
    --fixture-enable=tables@development >"$cache/logs/updates-server.log" 2>&1 &
named=$!
i=0
until [ -s "$creds" ] && python3 -I -c "import urllib.request;urllib.request.urlopen('http://127.0.0.1:$named_port/readyz',timeout=1)" 2>/dev/null; do
    i=$((i + 1))
    kill -0 "$named" 2>/dev/null || { echo 'UI test server exited' >&2; tail -20 "$cache/logs/updates-server.log" >&2; exit 1; }
    [ "$i" -lt 180 ] || { echo 'UI test server readiness timed out' >&2; exit 1; }
    sleep 1
done
report="$cache/logs/updates-report.json"
rm -f "$report"
status=0
(cd "$ui" && ADDONS_UI_URL="http://127.0.0.1:$named_port" ADDONS_FIXTURE="$creds" ADDONS_SHOTS="$shots" \
    ADDONS_UI_REPORT="$report" ./node_modules/.bin/playwright test --config playwright.updates.config.js >"$cache/logs/updates-playwright.log" 2>&1) || status=$?
python3 -I "$root/scripts/addons/ui_cases.py" playwright "$report" || status=1
exit "$status"

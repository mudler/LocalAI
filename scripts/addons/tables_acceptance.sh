#!/bin/sh
# SPDX-License-Identifier: MIT
# Runs the Tables acceptance and prints one JSON case line per check:
#   1. Go: the end-to-end specs of tests/addons/tablesacceptance. They start
#      the real owner and two forwarding frontends as separate server
#      processes over the real mTLS carrier with one shared account store, and
#      install the real packaged Tables (built by scripts/addons/package.sh)
#      from a local catalog. Every call uses a real named session or API key,
#      the public HTTP routes and the real SQLite databases of the add-on;
#   2. the Playwright spec against the same kind of cluster, started by the
#      tables-cluster command;
#   3. the evidence manifest, a JSON file the run writes under .cache/addons:
#      commands, case inventory, native platform, package digests and the
#      traces retained for a failure. A Go test checks it.
# Nothing is signed, tagged, pushed or published. verify.sh compares the lines
# with the exact inventory of the tables-acceptance suite and rejects a
# skipped, flaky or missing case.
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
for file in tests/e2e/mock-backend/mock-backend core/http/react-ui/node_modules/.bin/playwright; do
    [ -x "$file" ] || { echo "missing $file; run scripts/addons/prepare.sh --integration" >&2; exit 1; }
done
[ -f "$ui/dist/index.html" ] || { echo 'missing UI build; run scripts/addons/prepare.sh --integration' >&2; exit 1; }
[ "$root/tests/e2e/mock-backend/mock-backend" -nt "$root/tests/e2e/mock-backend/main.go" ] || { echo 'mock-backend is older than its source; run scripts/addons/prepare.sh --integration' >&2; exit 1; }

work=$(mktemp -d "$cache/tmp/tables-acceptance.XXXXXX")
cluster_pid=
. "$root/scripts/addons/leaks.sh"
leaked=0
cleanup() {
    code=$?
    trap - EXIT HUP INT TERM
    if [ -n "$cluster_pid" ]; then
        # The cluster tears its servers down on SIGTERM; wait, bounded.
        kill "$cluster_pid" 2>/dev/null || true
        n=0
        while kill -0 "$cluster_pid" 2>/dev/null && [ "$n" -lt 60 ]; do
            n=$((n + 1))
            sleep 0.5
        done
        kill -KILL "$cluster_pid" 2>/dev/null || true
        wait "$cluster_pid" 2>/dev/null || true
    fi
    # Nothing that names the work or scratch directories may survive. Whatever
    # does is killed, and the run fails.
    reap_leaks "$work" tables-acceptance || leaked=1
    reap_leaks "$cache/tmp/" tables-acceptance paths-only || leaked=1
    find "$work" -type d -exec chmod u+rwx {} + 2>/dev/null || true
    rm -rf "$work"
    if [ "$leaked" -ne 0 ] && [ "$code" -eq 0 ]; then
        code=1
    fi
    exit "$code"
}
trap cleanup EXIT
trap 'exit 143' HUP INT TERM

# The binaries are built here, never taken from an earlier build: the server
# embeds the UI build and every other part of the fixture.
go build -p 2 -tags auth -o "$work/ui-test-server" ./tests/e2e-ui
CGO_ENABLED=1 go build -p 2 -trimpath -o "$work/tables" ./addons/tables/cmd/tables
go build -p 2 -tags auth -o "$work/tables-cluster" ./tests/addons/tablesacceptance/cmd/tables-cluster

evidence="$cache/tables-acceptance-evidence.json"
rm -f "$evidence"
rm -rf "$cache/logs/tables-acceptance-traces"

# 1. The end-to-end specs.
go_report="$cache/logs/tables-acceptance-go.json"
rm -f "$go_report"
TABLES_ACCEPTANCE_SERVER="$work/ui-test-server" TABLES_ACCEPTANCE_TABLES="$work/tables" \
TABLES_ACCEPTANCE_MOCK="$root/tests/e2e/mock-backend/mock-backend" TABLES_ACCEPTANCE_EVIDENCE="$evidence" \
    go test -count=1 -p 2 -tags auth -timeout 30m ./tests/addons/tablesacceptance -ginkgo.json-report="$go_report" >"$cache/logs/tables-acceptance-go.log" 2>&1 || {
        python3 -I - "$go_report" <<'PY' >&2 || true
import json, sys
try:
    for report in json.load(open(sys.argv[1])):
        for spec in report['SpecReports']:
            if spec['LeafNodeType'] == 'It' and spec['State'] != 'passed':
                print('failed case: go: %s (%s)' % (spec['LeafNodeText'], spec['State']))
except (OSError, ValueError):
    pass
PY
        tail -60 "$cache/logs/tables-acceptance-go.log" >&2
        exit 1
    }
python3 -I - "$go_report" <<'PY'
import json, sys
for report in json.load(open(sys.argv[1])):
    for spec in report['SpecReports']:
        if spec['LeafNodeType'] != 'It':
            continue
        status = {'passed': 'passed', 'skipped': 'skipped'}.get(spec['State'], 'failed')
        print(json.dumps({'case': 'go: ' + spec['LeafNodeText'], 'status': status}))
PY

# 2. The browser against the real cluster.
ready="$work/ready.json"
"$work/tables-cluster" -dir "$work/state" -root "$root" -server "$work/ui-test-server" -mock-backend "$root/tests/e2e/mock-backend/mock-backend" \
    -tables "$work/tables" -ready "$ready" >"$cache/logs/tables-acceptance-cluster.log" 2>&1 &
cluster_pid=$!
i=0
until [ -s "$ready" ]; do
    i=$((i + 1))
    kill -0 "$cluster_pid" 2>/dev/null || { echo 'acceptance cluster exited' >&2; tail -20 "$cache/logs/tables-acceptance-cluster.log" >&2; exit 1; }
    [ "$i" -lt 300 ] || { echo 'acceptance cluster readiness timed out' >&2; exit 1; }
    sleep 1
done
report="$cache/logs/tables-acceptance-report.json"
rm -f "$report"
status=0
(cd "$ui" && ADDONS_TABLES_CLUSTER="$ready" ADDONS_UI_REPORT="$report" ./node_modules/.bin/playwright test --config playwright.tables-acceptance.config.js >"$cache/logs/tables-acceptance-playwright.log" 2>&1) || status=1
python3 -I "$root/scripts/addons/ui_cases.py" playwright "$report" || status=1
if [ "$status" -ne 0 ]; then
    echo '--- playwright log (tail) ---' >&2
    tail -60 "$cache/logs/tables-acceptance-playwright.log" >&2 || true
    echo '--- cluster log (tail) ---' >&2
    tail -30 "$cache/logs/tables-acceptance-cluster.log" >&2 || true
    mkdir -p "$cache/logs/tables-acceptance-traces"
    cp "$cache/logs/tables-acceptance-playwright.log" "$cache/logs/tables-acceptance-cluster.log" "$cache/logs/tables-acceptance-traces/" 2>/dev/null || true
fi

# 3. The evidence manifest: add the browser cases and run the check.
python3 -I - "$evidence" "$report" "$cache/logs/tables-acceptance-traces" "$cache/logs/tables-acceptance-playwright.log" <<'PY' || status=1
import json, os, sys
evidence, report, traces, log = sys.argv[1:5]
ev = json.load(open(evidence))
cases = []
def walk(suite):
    for spec in suite.get('specs', []):
        for test in spec.get('tests', []):
            results = test.get('results', [])
            ok = test.get('status') == 'expected' and len(results) == 1 and results[0].get('status') == 'passed'
            case = {'name': spec['title'], 'state': 'passed' if ok else 'failed',
                    'millis': int(sum(r.get('duration', 0) for r in results))}
            if not ok:
                case['trace'] = log
                case['error'] = ((results[-1].get('error') or {}).get('message') or '')[:2000] if results else ''
            cases.append(case)
    for child in suite.get('suites', []):
        walk(child)
try:
    for suite in json.load(open(report)).get('suites', []):
        walk(suite)
except (OSError, ValueError):
    pass
ev['browser'] = cases
ev['commands'].append(['playwright', 'test', '--config', 'playwright.tables-acceptance.config.js'])
tmp = evidence + '.tmp'
json.dump(ev, open(tmp, 'w'), indent=2)
os.replace(tmp, evidence)
PY
if TABLES_ACCEPTANCE_CHECK_EVIDENCE=1 TABLES_ACCEPTANCE_EVIDENCE="$evidence" \
    go test -count=1 -tags auth -run 'TestEvidenceManifest$' -v ./tests/addons/tablesacceptance >"$cache/logs/tables-acceptance-evidence.log" 2>&1 \
    && grep -q -- '--- PASS: TestEvidenceManifest' "$cache/logs/tables-acceptance-evidence.log"; then
    printf '{"case": "manifest: the evidence manifest names the platform, the commands, the package digests and every case", "status": "passed"}\n'
else
    printf '{"case": "manifest: the evidence manifest names the platform, the commands, the package digests and every case", "status": "failed"}\n'
    tail -30 "$cache/logs/tables-acceptance-evidence.log" >&2
    status=1
fi
exit "$status"

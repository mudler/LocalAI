#!/bin/sh
# SPDX-License-Identifier: MIT
# Runs the add-ons foundation acceptance gate and prints one JSON case line per
# check:
#   1. Go: the integration specs of tests/addons/foundation. They start the
#      real owner and two frontends as separate server processes with a shared
#      account store, the real mTLS carrier and the protocol fixture as the
#      add-on process, and drive them through real sessions and API keys;
#   2. regression: the existing model and backend gallery specs and the process
#      adapter specs, which the add-on lifecycle must not disturb. They run
#      offline: core/gallery (gallery, backend gallery, OCI install paths),
#      core/services/galleryop (the operations the gallery routes enqueue) and
#      pkg/model (the process adapter in process_runtime.go);
#   3. the Playwright spec against the same three servers, which proves server
#      authorization from the browser.
# verify.sh compares the lines with the exact inventory of the
# foundation-acceptance suite and rejects a skipped, flaky or missing case.
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

work=$(mktemp -d "$cache/tmp/foundation-gate.XXXXXX")
cluster_pid=
. "$root/scripts/addons/leaks.sh"
leaked=0
cleanup() {
    code=$?
    trap - EXIT HUP INT TERM
    if [ -n "$cluster_pid" ]; then
        # The cluster tears its servers down on SIGTERM; wait for it, bounded.
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
    # does is killed, and the gate fails.
    reap_leaks "$work" foundation || leaked=1
    reap_leaks "$cache/tmp/" foundation paths-only || leaked=1
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
go build -p 2 -tags auth -o "$work/fixture-addon" ./tests/addons/fixture/cmd
go build -p 2 -tags auth -o "$work/foundation-cluster" ./tests/addons/foundation/cmd/foundation-cluster

# One case line per spec of a Ginkgo report. $2 prefixes the case.
report_cases() {
    python3 -I - "$1" "$2" <<'PY'
import json, sys
for report in json.load(open(sys.argv[1])):
    for spec in report['SpecReports']:
        if spec['LeafNodeType'] != 'It':
            continue
        status = {'passed': 'passed', 'skipped': 'skipped'}.get(spec['State'], 'failed')
        print(json.dumps({'case': sys.argv[2] + spec['LeafNodeText'], 'status': status}))
PY
}

# 1. The integration specs.
go_report="$cache/logs/foundation-go.json"
rm -f "$go_report"
FOUNDATION_SERVER="$work/ui-test-server" FOUNDATION_FIXTURE="$work/fixture-addon" FOUNDATION_MOCK="$root/tests/e2e/mock-backend/mock-backend" \
    go test -count=1 -p 2 -tags auth -timeout 20m ./tests/addons/foundation -ginkgo.json-report="$go_report" >"$cache/logs/foundation-go.log" 2>&1 || {
        # Name the failed specs for the runner, then show the end of the log.
        report_cases "$go_report" 'go: ' 2>/dev/null | grep -v '"status": "passed"\|"status": "skipped"' || true
        tail -60 "$cache/logs/foundation-go.log" >&2
        exit 1
    }
report_cases "$go_report" 'go: '

# 2. The existing gallery and process adapter specs. A package case passes when
# its suite passed with at least one spec and no failed or pending one. The only
# skips allowed are the specs that say they need macOS: they cannot run here.
# core/gallery gets its fixture directory, so its model install specs run.
# A package may name one spec to run alone (see below).
regression() {
    pkg=$1
    label=$2
    isolate=${3:-}
    base="$cache/logs/foundation-regression-$(printf %s "$pkg" | tr '/.' '--')"
    rm -f "$base.json" "$base.main.json" "$base.isolated.json" "$base.plan.json"
    if [ -z "$isolate" ]; then
        FIXTURES="$root/tests/fixtures" go test -count=1 -p 2 -tags auth -timeout 15m "$pkg" -ginkgo.json-report="$base.json" >"$base.log" 2>&1 || { tail -40 "$base.log" >&2; exit 1; }
        python3 -I - "$base.json" "" "$label" <<'PY'
import json, sys
specs = platform = failed = 0
for report in json.load(open(sys.argv[1])):
    for spec in report['SpecReports']:
        if spec['LeafNodeType'] != 'It':
            continue
        specs += 1
        if spec['State'] == 'passed':
            continue
        message = (spec.get('Failure') or {}).get('Message', '').lower()
        if spec['State'] == 'skipped' and 'darwin' in message:
            platform += 1
        else:
            failed += 1
ok = specs > 0 and failed == 0
print(json.dumps({'case': sys.argv[3], 'status': 'passed' if ok else 'failed', 'specs': specs, 'platformSkipped': platform}))
PY
        return
    fi
    # One spec of the package replaces the process-wide logger. Specs that log
    # from their own goroutines at the same time make it fail at random, so it
    # runs alone, once, and the rest of the package runs without it. A dry run
    # fixes the inventory: the two runs must account for every spec exactly
    # once, the isolated run for exactly one.
    FIXTURES="$root/tests/fixtures" go test -count=1 -tags auth "$pkg" -ginkgo.dry-run -ginkgo.json-report="$base.plan.json" >"$base.plan.log" 2>&1 || { tail -40 "$base.plan.log" >&2; exit 1; }
    FIXTURES="$root/tests/fixtures" go test -count=1 -p 2 -tags auth -timeout 15m "$pkg" -ginkgo.skip="$isolate" -ginkgo.json-report="$base.main.json" >"$base.main.log" 2>&1 || { tail -40 "$base.main.log" >&2; exit 1; }
    FIXTURES="$root/tests/fixtures" go test -count=1 -p 2 -tags auth -timeout 15m "$pkg" -ginkgo.focus="$isolate" -ginkgo.json-report="$base.isolated.json" >"$base.isolated.log" 2>&1 || { tail -40 "$base.isolated.log" >&2; exit 1; }
    python3 -I - "$base" "$isolate" "$label" <<'PY'
import json, sys
base, isolate, label = sys.argv[1:4]
def its(path):
    return [s for r in json.load(open(path)) for s in r['SpecReports'] if s['LeafNodeType'] == 'It']
plan = its(base + '.plan.json')
main = its(base + '.main.json')
alone = its(base + '.isolated.json')
isolated = [s for s in plan if isolate in s['LeafNodeText']]
problems = []
if len(isolated) != 1:
    problems.append('the isolated spec must match exactly one spec, matched %d' % len(isolated))
if len(main) != len(plan) or len(alone) != len(plan):
    problems.append('a run reported %d and %d specs for a plan of %d' % (len(main), len(alone), len(plan)))
platform = ran = 0
for spec in main:
    if isolate in spec['LeafNodeText']:
        if spec['State'] != 'skipped':
            problems.append('the isolated spec ran in the main run')
        continue
    if spec['State'] == 'passed':
        ran += 1
        continue
    message = (spec.get('Failure') or {}).get('Message', '').lower()
    if spec['State'] == 'skipped' and 'darwin' in message:
        platform += 1
        ran += 1
    else:
        problems.append('%s: %s' % (spec['State'], spec['LeafNodeText']))
once = 0
for spec in alone:
    if isolate in spec['LeafNodeText']:
        if spec['State'] == 'passed':
            once += 1
        else:
            problems.append('isolated spec %s' % spec['State'])
    elif spec['State'] != 'skipped':
        problems.append('a spec outside the isolation ran in the isolated run')
if once != 1 or ran != len(plan) - 1:
    problems.append('accounted %d of %d specs, isolated run %d times' % (ran + once, len(plan), once))
ok = not problems
for p in problems:
    print(p, file=sys.stderr)
print(json.dumps({'case': label, 'status': 'passed' if ok else 'failed', 'specs': ran + once, 'platformSkipped': platform, 'isolated': once}))
PY
}
regression ./core/gallery 'regression: model and backend gallery specs (core/gallery)'
regression ./core/services/galleryop 'regression: gallery operation specs (core/services/galleryop)'
regression ./pkg/model 'regression: process adapter specs (pkg/model)' 'includes the exit code and final stderr line for an unexpected exit'

# 3. The browser against the real cluster.
ready="$work/ready.json"
"$work/foundation-cluster" -dir "$work/cluster" -server "$work/ui-test-server" -mock-backend "$root/tests/e2e/mock-backend/mock-backend" \
    -fixture "$work/fixture-addon" -ready "$ready" >"$cache/logs/foundation-cluster.log" 2>&1 &
cluster_pid=$!
i=0
until [ -s "$ready" ]; do
    i=$((i + 1))
    kill -0 "$cluster_pid" 2>/dev/null || { echo 'foundation cluster exited' >&2; tail -20 "$cache/logs/foundation-cluster.log" >&2; exit 1; }
    [ "$i" -lt 180 ] || { echo 'foundation cluster readiness timed out' >&2; exit 1; }
    sleep 1
done
report="$cache/logs/foundation-report.json"
rm -f "$report"
status=0
(cd "$ui" && ADDONS_FOUNDATION="$ready" ADDONS_UI_REPORT="$report" ./node_modules/.bin/playwright test --config playwright.foundation.config.js >"$cache/logs/foundation-playwright.log" 2>&1) || status=1
python3 -I "$root/scripts/addons/ui_cases.py" playwright "$report" || status=1
if [ "$status" -ne 0 ]; then
    # Keep the evidence in the runner output: the end of the browser log and
    # of the cluster log name the failing step and what the servers saw.
    echo '--- playwright log (tail) ---' >&2
    tail -60 "$cache/logs/foundation-playwright.log" >&2 || true
    echo '--- cluster log (tail) ---' >&2
    tail -30 "$cache/logs/foundation-cluster.log" >&2 || true
fi
exit "$status"

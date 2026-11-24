#!/bin/sh
# SPDX-License-Identifier: MIT
# Runs the add-ons gallery UI checks and prints one JSON case line per test:
# the node unit tests of the pure state, then the Playwright spec against the
# real UI test server on an isolated port. verify.sh compares the lines with
# the exact inventory of the gallery-ui suite and rejects a skipped, flaky or
# missing case.
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$root"
cache="$root/.cache/addons"
ui="$root/core/http/react-ui"
mkdir -p "$cache/tmp" "$cache/logs"
export TMPDIR="$cache/tmp" PLAYWRIGHT_BROWSERS_PATH="$cache/browsers"
export PATH="$cache/bin:$PATH"
for file in tests/e2e-ui/ui-test-server tests/e2e/mock-backend/mock-backend core/http/react-ui/node_modules/.bin/playwright; do
    [ -x "$file" ] || { echo "missing $file; run scripts/addons/prepare.sh --integration" >&2; exit 1; }
done

# 1. Pure state and client: node's own runner, no dependencies.
(cd "$ui" && node tests/unit/run.mjs addons.test.mjs)

# 2. Browser specs against the real server, which embeds the built UI.
port=$(python3 -I -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1])')
log="$cache/logs/gallery-ui-server.log"
"$root/tests/e2e-ui/ui-test-server" --mock-backend="$root/tests/e2e/mock-backend/mock-backend" --port="$port" >"$log" 2>&1 &
server=$!
trap 'kill "$server" 2>/dev/null || true; wait "$server" 2>/dev/null || true' EXIT HUP INT TERM
i=0
until python3 -I -c "import sys,urllib.request;urllib.request.urlopen('http://127.0.0.1:$port/readyz',timeout=1)" 2>/dev/null; do
    i=$((i + 1))
    kill -0 "$server" 2>/dev/null || { echo 'UI test server exited' >&2; exit 1; }
    [ "$i" -lt 90 ] || { echo 'UI test server readiness timed out' >&2; exit 1; }
    sleep 1
done
report="$cache/logs/gallery-ui-report.json"
rm -f "$report"
status=0
(cd "$ui" && ADDONS_UI_URL="http://127.0.0.1:$port" ADDONS_UI_REPORT="$report" ./node_modules/.bin/playwright test --config playwright.addons.config.js >"$cache/logs/gallery-ui-playwright.log" 2>&1) || status=$?
python3 -I "$root/scripts/addons/ui_cases.py" playwright "$report" || status=1
exit "$status"

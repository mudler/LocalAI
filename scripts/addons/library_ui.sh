#!/bin/sh
# SPDX-License-Identifier: MIT
# Runs the library sources spec and prints one JSON case line per test.
# The spec drives a real UI test server that starts like a normal install:
# the default galleries and the default library sources of the add-on, backend
# and model kinds, with add-ons on and nothing installed
# (playwright.library.config.js starts that server itself, on a free port and
# with a fresh data directory). verify.sh compares the lines with the exact
# inventory of the library-sources suite and rejects a skipped, flaky or
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
# The server embeds the UI build: a build made before the last change would
# test the old pages.
[ "$root/tests/e2e-ui/ui-test-server" -nt "$ui/dist/index.html" ] || { echo 'ui-test-server is older than the UI build; run scripts/addons/prepare.sh --integration' >&2; exit 1; }

port=$(python3 -I -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1])')
report="$cache/logs/library-ui-report.json"
rm -f "$report"
status=0
(cd "$ui" && LIBRARY_UI_PORT="$port" LIBRARY_UI_LOG="$cache/logs/library-ui-server.log" ADDONS_UI_REPORT="$report" ./node_modules/.bin/playwright test --config playwright.library.config.js >"$cache/logs/library-ui-playwright.log" 2>&1) || status=$?
python3 -I "$root/scripts/addons/ui_cases.py" playwright "$report" || status=1
exit "$status"

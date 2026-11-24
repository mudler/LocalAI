#!/usr/bin/env bash
# Runs the built launcher (npm run build) under a virtual display with a
# hostile stand-in server, and checks that the WebUI view blocks or routes
# each escape attempt of its page. Prints the attempt results, then the
# launcher's policy decisions from the signal file.
#
#   SEC_DIR=/short/scratch/dir scripts/webui-security.sh [extra Electron args]
#
# Needs node, xvfb-run and the Electron binary (node_modules/electron/dist, or
# ELECTRON_BIN). Pass --no-sandbox on hosts that restrict user namespaces.
# Chromium's fake capture devices are used, so a microphone request reaches
# the permission handler on a host without audio hardware.
# Keep SEC_DIR short: Chromium puts a Unix socket under TMPDIR.
set -euo pipefail
: "${SEC_DIR:?set SEC_DIR to an empty scratch directory}"
HERE=$(cd "$(dirname "$0")" && pwd)
APP=$(cd "$HERE/.." && pwd)
ELECTRON_BIN=${ELECTRON_BIN:-$APP/node_modules/electron/dist/electron}
PORT=${PORT:-18471}
EVIL_PORT=$((PORT + 1))
DEVTOOLS_PORT=$((PORT + 2))

rm -rf "$SEC_DIR/home" "$SEC_DIR/tmp" "$SEC_DIR/signal.txt"
mkdir -p "$SEC_DIR/home/.localai" "$SEC_DIR/tmp" "$SEC_DIR/bin"
printf '#!/bin/sh\nexec "%s" "%s"\n' "$(command -v node)" "$APP/test/fixtures/hostile-server.cjs" > "$SEC_DIR/bin/local-ai"
chmod +x "$SEC_DIR/bin/local-ai"
printf '{"environment_vars":{"FAKE_PORT":"%s","FAKE_EVIL_PORT":"%s"}}\n' "$PORT" "$EVIL_PORT" > "$SEC_DIR/home/.localai/launcher.json"

export HOME="$SEC_DIR/home" XDG_CONFIG_HOME="$SEC_DIR/home/.config" TMPDIR="$SEC_DIR/tmp"
export LOCALAI_LAUNCHER_SIGNAL_FILE="$SEC_DIR/signal.txt" LOCALAI_LAUNCHER_EXTERNAL_DRY=1
export LOCALAI_LAUNCHER_SERVER_BIN="$SEC_DIR/bin/local-ai" LOCALAI_LAUNCHER_ENDPOINT="http://127.0.0.1:$PORT"
export APP ELECTRON_BIN HERE PORT DEVTOOLS_PORT SEC_DIR

xvfb-run -a -s "-screen 0 1280x800x24" bash -c '
  "$ELECTRON_BIN" "$APP" --remote-debugging-port=$DEVTOOLS_PORT --use-fake-device-for-media-stream "$@" > "$SEC_DIR/app.log" 2>&1 &
  for _ in $(seq 1 300); do grep -q "webui-loaded" "$SEC_DIR/signal.txt" 2>/dev/null && break; sleep 0.1; done
  sleep 1
  # A picture of the virtual screen, to check the layout by eye (ImageMagick, optional).
  command -v import >/dev/null && import -window root "$SEC_DIR/window.png" || true
  timeout 120 node "$HERE/webui-security.mjs" "$DEVTOOLS_PORT" "http://127.0.0.1:$PORT" || echo "driver failed: $?"
  pid=$(awk -F= "/ pid=/{print \$2}" "$SEC_DIR/signal.txt")
  kill -TERM "$pid" 2>/dev/null || true
  for _ in $(seq 1 100); do kill -0 "$pid" 2>/dev/null || break; sleep 0.1; done
  kill -0 "$pid" 2>/dev/null && kill -KILL "$pid"
  wait
' bash "$@"

echo "--- launcher decisions"
grep -E "webui|external|ipc-refused" "$SEC_DIR/signal.txt" | cut -d" " -f2- || true
echo "--- requests that reached the other origin"
grep -h "evil-origin" "$SEC_DIR/home/.localai/logs/server.log" || echo "none"
echo "--- files written outside the scratch HOME by downloads"
ls -la "$SEC_DIR/home/Downloads" 2>/dev/null || echo "no Downloads folder"

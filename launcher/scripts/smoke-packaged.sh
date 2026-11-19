#!/usr/bin/env bash
# Smoke test of the packaged launcher, with an isolated HOME and a fake
# release server, so no network is needed. On Linux it runs an AppImage under
# a virtual display (Xvfb). On macOS it runs a .app (for example from the
# mounted DMG) on the runner's own display. On Windows (Git Bash) it runs the
# installed local-ai-launcher.exe on the runner's own desktop.
#
#   SMOKE_DIR=/short/dir scripts/smoke-packaged.sh release/LocalAI-Launcher-x86_64.AppImage [extra args]
#   SMOKE_DIR=/short/dir SMOKE_FEED_FILE=release/latest-mac.yml scripts/smoke-packaged.sh "/Volumes/LocalAI Launcher/LocalAI Launcher.app"
#   SMOKE_DIR=/c/smoke SMOKE_FEED_FILE=release/latest.yml scripts/smoke-packaged.sh "$LOCALAPPDATA/Programs/localai-launcher/local-ai-launcher.exe"
#
# It passes when:
#   1. the main process reports "window-ready" and the shell reports its first
#      render, both within SMOKE_TIMEOUT seconds (default 60),
#   2. the first render shows the state "Not installed" (no LocalAI server in
#      the empty HOME),
#   3. the self-update check reaches the fake feed (release builds), or never
#      reaches it (development builds, SMOKE_SELF_UPDATE=off),
#   4. SIGTERM ends the app within 10 s with a clean-exit line.
# It fails on an early exit, on a missing signal, and on a forced kill.
#
# Limits: this proves that the packaged app starts, renders its shell and
# stops. It does not install or start a LocalAI server, load a model, use a
# GPU, or test the tray. Windows has no SIGTERM: there the app is ended with
# `taskkill /T /F`, so check 4 is only that the app is gone afterwards.
#
# Environment:
#   SMOKE_DIR          empty scratch directory (required). Keep it short:
#                      Chromium puts a Unix socket under TMPDIR, and a path
#                      over about 100 characters fails with "Socket path too long".
#   SMOKE_SELF_UPDATE  "check" (default) or "off" (development builds).
#   SMOKE_FEED_FILE    the feed file for the fake server. Default: the
#                      latest-linux*.yml or latest-mac.yml next to the target.
#                      Needed for "check".
#   SMOKE_TIMEOUT      seconds to wait for the first render (default 60).
#   SMOKE_PORT         port of the fake release server (default 18081).
#
# Use `--appimage-extract-and-run` as an extra argument on hosts without FUSE 2.
set -euo pipefail

TARGET=$(cd "$(dirname "${1:?usage: smoke-packaged.sh <AppImage or .app> [args...]}")" && pwd)/$(basename "$1")
shift || true
case "$(uname -s)" in MINGW* | MSYS* | CYGWIN*) IS_WIN=1 ;; *) IS_WIN=0 ;; esac
if [ "$IS_WIN" = 1 ]; then
  OS=win
  case "$TARGET" in *.exe) ;; *) echo "FAIL: on Windows the target is the launcher .exe"; exit 2 ;; esac
  APP_EXE=$TARGET
  [ -f "$APP_EXE" ] || { echo "FAIL: $APP_EXE does not exist"; exit 2; }
elif [ "$(uname -s)" = Darwin ]; then
  OS=mac
  case "$TARGET" in *.app) ;; *) echo "FAIL: on macOS the target is a .app directory"; exit 2 ;; esac
  APP_EXE=$(find "$TARGET/Contents/MacOS" -maxdepth 1 -type f -perm -u+x | head -n 1)
  [ -n "$APP_EXE" ] || { echo "FAIL: no executable in $TARGET/Contents/MacOS"; exit 2; }
else
  OS=linux
  APP_EXE=$TARGET
  [ -x "$APP_EXE" ] || chmod +x "$APP_EXE"
fi
: "${SMOKE_DIR:?set SMOKE_DIR to an empty scratch directory}"
SMOKE_SELF_UPDATE=${SMOKE_SELF_UPDATE:-check}
SMOKE_TIMEOUT=${SMOKE_TIMEOUT:-60}
SMOKE_PORT=${SMOKE_PORT:-18081}
HERE=$(cd "$(dirname "$0")" && pwd)

case "$SMOKE_SELF_UPDATE" in check | off) ;; *) echo "FAIL: SMOKE_SELF_UPDATE must be check or off"; exit 2 ;; esac
# Chromium makes $TMPDIR/scoped_dirXXXXXX/SingletonSocket; a Unix socket path
# has at most 103 characters on macOS (107 on Linux). Windows uses no such socket.
if [ "$OS" != win ] && [ $(( ${#SMOKE_DIR} + 4 + 36 )) -gt 103 ]; then
  echo "FAIL: SMOKE_DIR is too long (${#SMOKE_DIR} characters); use a path of at most 63 characters"; exit 2
fi

mkdir -p "$SMOKE_DIR/home" "$SMOKE_DIR/feed" "$SMOKE_DIR/tmp"
SIGNAL="$SMOKE_DIR/signal.txt"
FEEDLOG="$SMOKE_DIR/feed.log"
rm -f "$SIGNAL" "$FEEDLOG"
touch "$FEEDLOG"

if [ "$SMOKE_SELF_UPDATE" = check ]; then
  feed=${SMOKE_FEED_FILE:-}
  if [ -z "$feed" ]; then
    if [ "$OS" = win ]; then pattern=latest.yml; else pattern="latest-$OS*.yml"; fi
    feed=$(find "$(dirname "$TARGET")" -maxdepth 1 -name "$pattern" | head -n 1)
  fi
  [ -n "$feed" ] && [ -f "$feed" ] || { echo "FAIL: no feed file next to the target; set SMOKE_FEED_FILE or SMOKE_SELF_UPDATE=off"; exit 1; }
  # The app asks for latest-linux.yml (x64), latest-linux-arm64.yml,
  # latest-mac.yml or latest.yml (Windows).
  cp "$feed" "$SMOKE_DIR/feed/"
fi

node "$HERE/fake-release-server.mjs" "$SMOKE_PORT" "$SMOKE_DIR/feed" "$FEEDLOG" &
FEED_PID=$!
trap 'kill $FEED_PID 2>/dev/null || true' EXIT

export APP_EXE SIGNAL SMOKE_DIR FEEDLOG SMOKE_SELF_UPDATE SMOKE_TIMEOUT OS
export HOME="$SMOKE_DIR/home" XDG_CONFIG_HOME="$SMOKE_DIR/home/.config" TMPDIR="$SMOKE_DIR/tmp"
export LOCALAI_LAUNCHER_SIGNAL_FILE="$SIGNAL"
if [ "$OS" = win ]; then
  # A Windows program reads Windows paths. Do not move USERPROFILE, TEMP or
  # TMP: the packaged app crashes at start (EXCEPTION_BREAKPOINT) when they
  # point to a scratch folder, while it starts with the runner's own values.
  # So on Windows the app runs with the real profile of the (clean) runner.
  # Chromium logs and stack dumps go to the captured app log.
  export ELECTRON_ENABLE_LOGGING=1 ELECTRON_ENABLE_STACK_DUMPING=1
  LOCALAI_LAUNCHER_SIGNAL_FILE=$(cygpath -w "$SIGNAL")
  export LOCALAI_LAUNCHER_SIGNAL_FILE
fi
# The launcher probes the macOS keychain at start. On a CI runner nobody can
# answer the keychain dialog, so the app waits there and never opens its
# window. Chromium's mock keychain exists for this case. Only on a CI runner
# (CI is set): a developer's Mac keeps the real keychain.
if [ "$OS" = mac ] && [ -n "${CI:-}" ]; then
  set -- --use-mock-keychain "$@"
fi
# A development build checks a feed only when one is set explicitly, so the
# "off" run sets none: it proves that a development build as users get it
# never asks a feed.
if [ "$SMOKE_SELF_UPDATE" = check ]; then
  export LOCALAI_LAUNCHER_FEED_URL="http://127.0.0.1:$SMOKE_PORT/"
else
  unset LOCALAI_LAUNCHER_FEED_URL
fi
# Nothing listens here: the launcher must not find a server.
export LOCALAI_LAUNCHER_ENDPOINT="http://127.0.0.1:$((SMOKE_PORT + 1))"

# The checks. On Linux, xvfb-run starts and stops the display around them.
INNER='
  now_ms() { node -e "process.stdout.write(String(Date.now()))"; }
  fail() { echo "FAIL: $*"; echo "--- startup signals"; cat "$SIGNAL" 2>/dev/null || echo "(none)"; if [ "$(uname -s)" = Darwin ] && kill -0 $wrapper 2>/dev/null; then echo "--- where the app waits"; sample $wrapper 3 2>&1 | head -60 || true; fi; echo "--- files the app made"; (cd "$SMOKE_DIR/home" 2>/dev/null && find . -type f -not -path "./Library/Keychains/*" | head -40); for f in "$SMOKE_DIR"/home/.localai/logs/*; do [ -f "$f" ] && { echo "--- $f"; tail -n 40 "$f"; }; done; echo "--- app log"; cat "$SMOKE_DIR/app.log" 2>/dev/null || true; if [ "$OS" = win ]; then echo "--- app folder"; ls -la "$(dirname "$APP_EXE")" "$(dirname "$APP_EXE")/resources" 2>&1 | head -60; echo "--- Windows application errors of the last 10 minutes"; powershell -NoProfile -Command "Get-WinEvent -FilterHashtable @{LogName=\"Application\"; Level=1,2; StartTime=(Get-Date).AddMinutes(-10)} -MaxEvents 5 -ErrorAction SilentlyContinue | Format-List TimeCreated,ProviderName,Message" 2>&1 | head -40; fi; kill -KILL $wrapper 2>/dev/null || true; exit 1; }
  t0=$(now_ms)
  "$APP_EXE" "$@" > "$SMOKE_DIR/app.log" 2>&1 &
  wrapper=$!
  for _ in $(seq 1 $((SMOKE_TIMEOUT * 10))); do
    grep -q "first-render" "$SIGNAL" 2>/dev/null && grep -q "window-ready" "$SIGNAL" && break
    kill -0 $wrapper 2>/dev/null || { wait $wrapper; code=$?; fail "the app exited early (status $code) before its first render"; }
    sleep 0.1
  done
  grep -q "window-ready" "$SIGNAL" 2>/dev/null || fail "no window-ready signal within $SMOKE_TIMEOUT s"
  grep -q "first-render" "$SIGNAL" || fail "no first render within $SMOKE_TIMEOUT s"
  ready=$(awk "/window-ready/{print \$1; exit}" "$SIGNAL"); render=$(awk "/first-render/{print \$1; exit}" "$SIGNAL")
  echo "window-ready after $((ready - t0)) ms, first render after $((render - t0)) ms"
  grep -q "first-render state=Not installed" "$SIGNAL" || fail "the first render did not show \"Not installed\": $(grep first-render "$SIGNAL")"
  echo "state shown: Not installed"
  echo "sandbox: $(awk -F= "/ sandbox=/{print \$2; exit}" "$SIGNAL") (false means the start script added --no-sandbox)"
  # Packaged release builds check for their own update about 3 s after start.
  if [ "$SMOKE_SELF_UPDATE" = check ]; then
    for _ in $(seq 1 150); do grep -q "ua=electron-builder" "$FEEDLOG" && break; sleep 0.1; done
    grep -q "ua=electron-builder" "$FEEDLOG" || fail "no self-update check reached the fake feed"
    grep -E "GET /latest(-linux(-arm64)?|-mac)?\.yml" "$FEEDLOG" | grep -q "ua=electron-builder" || fail "the self-update check did not ask for a feed file"
    echo "self-update check reached the fake feed"
  else
    sleep 8
    ! grep -q "ua=electron-builder" "$FEEDLOG" || fail "a development build asked the feed for an update"
    echo "no self-update check (development build)"
  fi
  kill -0 $wrapper 2>/dev/null || fail "the app exited before the stop test"
  pid=$(awk -F= "/ pid=/{print \$2; exit}" "$SIGNAL" | tr -d "\r")
  [ -n "$pid" ] || fail "no pid line in the signal file"
  if [ "$OS" = win ]; then
    # No SIGTERM on Windows, and closing the window only hides it to the tray.
    taskkill //PID "$pid" //T //F > /dev/null || fail "taskkill could not end the app"
    for _ in $(seq 1 100); do kill -0 $wrapper 2>/dev/null || break; sleep 0.1; done
    kill -0 $wrapper 2>/dev/null && fail "still running 10 s after taskkill"
    tasklist //FI "PID eq $pid" //NH | grep -q "$pid" && fail "process $pid is still there after taskkill"
    echo "PASS (Windows: ended with taskkill, no clean-exit check)"
    exit 0
  fi
  kill -TERM "$pid"
  for _ in $(seq 1 100); do kill -0 "$pid" 2>/dev/null || break; sleep 0.1; done
  if kill -0 "$pid" 2>/dev/null; then kill -KILL "$pid"; fail "still running 10 s after SIGTERM (forced kill)"; fi
  grep -q " exit" "$SIGNAL" || fail "no clean-exit line after SIGTERM"
  set +e; wait $wrapper; code=$?; set -e
  echo "app exit status: $code"
  [ "$code" -lt 128 ] || fail "the app ended by a signal (status $code)"
  echo "PASS"
'
if [ "$OS" = mac ] || [ "$OS" = win ]; then
  bash -c "$INNER" smoke "$@"
else
  xvfb-run -a -s "-screen 0 1280x800x24" bash -c "$INNER" smoke "$@"
fi

echo "--- signal file"; cat "$SIGNAL"
echo "--- requests to the fake release server"; cat "$FEEDLOG"

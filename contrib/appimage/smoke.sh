#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
set -euo pipefail
root=$(cd "$(dirname "$0")/../.." && pwd)
image="$root/dist/LocalAI-Launcher-x86_64.AppImage"
work="$root/dist/appimage/smoke"
rm -rf "$work"
mkdir -p "$work/home" "$work/tmp"
export TMPDIR="$work/tmp"
(cd "$work" && "$image" --appimage-extract >/dev/null)
app="$work/squashfs-root"
test -x "$app/AppRun"
test -f "$app/local-ai-launcher.desktop"
test -f "$app/local-ai-launcher.png"
test ! -e "$app/usr/bin/local-ai"
desktop-file-validate "$app/local-ai-launcher.desktop"
ldd "$app/usr/bin/local-ai-launcher" > "$work/ldd.txt"
if grep -q 'not found' "$work/ldd.txt"; then
    cat "$work/ldd.txt" >&2
    exit 1
fi
# Prevent Initialize from downloading or starting the server during the smoke.
printf '%s\n' '{"auto_start_server":false,"show_welcome":false}' > "$work/config.json"
mkdir -p "$work/home/.localai"
cp "$work/config.json" "$work/home/.localai/launcher.json"
set +e
HOME="$work/home" XDG_CONFIG_HOME="$work/home/.config" LIBGL_ALWAYS_SOFTWARE=1 \
    timeout 15s xvfb-run -a "$app/AppRun" > "$work/launch.log" 2>&1
status=$?
set -e
# A GUI event loop should stay alive; early exit (even code 0) is a failure.
[[ $status == 124 ]]
if grep -Ei 'panic:|fatal error:|failed to initialize GLFW|APIUnavailable' "$work/launch.log"; then
    exit 1
fi
test ! -e "$work/home/.localai/bin/local-ai"

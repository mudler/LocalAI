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
    timeout --kill-after=5s 15s xvfb-run -a "$app/AppRun" > "$work/launch.log" 2>&1
status=$?
set -e
# This is a bounded startup check, not a UI event-loop heartbeat.
# Early exit (even code 0) and forced SIGKILL are failures.
[[ $status == 124 ]]
if grep -Ei 'panic:|fatal error:|failed to initialize GLFW|Failed to initialize launcher:|APIUnavailable' "$work/launch.log"; then
    exit 1
fi
# On a fresh home this milestone follows config loading and startup setup.
# Survival alone could accept a process stalled before initialization.
grep -Fq 'No LocalAI installation found' "$work/launch.log"
test -d "$work/home/.localai/models"
test -d "$work/home/.localai/backends"
test ! -e "$work/home/.localai/bin/local-ai"

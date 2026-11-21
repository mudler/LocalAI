#!/usr/bin/env bash
# Check that a built x86_64 AppImage has the portable layout (see after-pack.cjs):
# the start script, the builder's start script, the bundled loader, and the
# relative ELF interpreter in both binaries. Needs readelf.
#   scripts/portable-linux/check-image.sh release/LocalAI-Launcher-x86_64.AppImage
set -euo pipefail
img=$(cd "$(dirname "${1:?usage: check-image.sh <AppImage>}")" && pwd)/$(basename "$1")
work=$(mktemp -d "${TMPDIR:-/tmp}/portable-check.XXXXXX")
trap 'rm -rf "$work"' EXIT
cd "$work"
"$img" --appimage-extract 'AppRun*' >/dev/null
"$img" --appimage-extract 'glibc/ld-linux-x86-64.so.2' >/dev/null
"$img" --appimage-extract 'local-ai-launcher' >/dev/null
"$img" --appimage-extract 'chrome_crashpad_handler' >/dev/null
r=squashfs-root
head -n 1 "$r/AppRun" | grep -q '^#!/bin/sh' || { echo "FAIL: AppRun is not the portable start script"; exit 1; }
grep -q 'AppRun.electron-builder' "$r/AppRun" || { echo "FAIL: AppRun does not start AppRun.electron-builder"; exit 1; }
[ -x "$r/AppRun.electron-builder" ] || { echo "FAIL: AppRun.electron-builder is missing"; exit 1; }
[ -f "$r/glibc/ld-linux-x86-64.so.2" ] || { echo "FAIL: the bundled loader is missing"; exit 1; }
for b in local-ai-launcher chrome_crashpad_handler; do
  readelf -l "$r/$b" | grep -q 'Requesting program interpreter: ld\]' || { echo "FAIL: $b has not the relative interpreter"; exit 1; }
done
echo "PASS: portable layout"

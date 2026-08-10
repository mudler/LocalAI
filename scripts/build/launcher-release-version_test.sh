#!/usr/bin/env bash
set -euo pipefail

ROOT=$(dirname "$(dirname "$(dirname "$(realpath "$0")")")")
MAKEFILE="$ROOT/Makefile"
WORKFLOW="$ROOT/.github/workflows/release.yaml"

dry_run=$(make -n -f "$MAKEFILE" build-launcher-darwin LAUNCHER_APP_VERSION=9.8.7)
if ! grep -Fq -- '--app-version 9.8.7' <<<"$dry_run"; then
  echo "FAIL: macOS launcher packaging does not pass LAUNCHER_APP_VERSION to Fyne"
  exit 1
fi

if ! grep -Fq -- 'LAUNCHER_APP_VERSION="${GITHUB_REF_NAME#v}"' "$WORKFLOW"; then
  echo "FAIL: release workflow does not derive the launcher app version from the tag"
  exit 1
fi

echo "PASS: macOS launcher releases stamp the tag-derived app version"

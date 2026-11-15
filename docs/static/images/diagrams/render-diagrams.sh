#!/usr/bin/env bash
# Render the diagrams to PNG with headless Chrome. Works offline: fonts and
# the tile mark are local files.
#
#   ./render-diagrams.sh                 all diagrams, light and dark
#   ./render-diagrams.sh quickstart-journey model-resolution
#
# Output, next to the sources: <name>.png (light) and <name>-dark.png (dark).
# Size: 3200 x 1800 px (a 1600 x 900 canvas at 2x).
# Environment: CHROME (path to Chrome or Chromium), OUT (output folder).
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
out="${OUT:-$here}"
chrome="${CHROME:-$(command -v google-chrome || command -v chromium || command -v chromium-browser || true)}"
[ -n "$chrome" ] || { echo "render-diagrams: set CHROME to a Chrome or Chromium binary" >&2; exit 1; }
mkdir -p "$out"

if [ $# -gt 0 ]; then names=("$@"); else
  names=(); for f in "$here"/*.html; do names+=("$(basename "$f" .html)"); done
fi

profile="$(mktemp -d)"
trap 'rm -rf "$profile"' EXIT
for n in "${names[@]}"; do
  for theme in light dark; do
    suffix=""; [ "$theme" = dark ] && suffix="-dark"
    "$chrome" --headless=new --no-sandbox --disable-gpu --hide-scrollbars \
      --user-data-dir="$profile" --force-device-scale-factor=2 --window-size=1600,900 \
      --virtual-time-budget=3000 --screenshot="$out/$n$suffix.png" \
      "file://$here/$n.html?theme=$theme" >/dev/null 2>&1
    echo "$out/$n$suffix.png"
  done
done

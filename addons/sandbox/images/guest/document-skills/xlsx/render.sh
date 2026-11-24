#!/bin/sh
# SPDX-License-Identifier: MIT
# Render a .xlsx to one PNG for each printed page (a sheet may take several), to look at them.
#
#   sh render.sh book.xlsx [outdir] [dpi]
#
# It needs the sandbox profile "office" (LibreOffice Calc and fonts). It
# writes outdir/page-<n>.png (default outdir: the workbook's name with "-render"
# added, default dpi: 80) and the PDF next to them, and prints the PNG paths.
# The PNGs are for you to open and read: a workbook is not done until you have
# looked at every one.
#
# LibreOffice gets a private profile directory under /tmp and a private HOME, so
# nothing depends on a writable home, and two runs do not share a lock. The
# whole conversion is stopped after RENDER_TIMEOUT seconds (default 180).
set -eu
file=${1:-}
[ -n "$file" ] && [ -f "$file" ] || { echo "usage: render.sh book.xlsx [outdir] [dpi]" >&2; exit 2; }
out=${2:-${file%.*}-render}
dpi=${3:-80}
limit=${RENDER_TIMEOUT:-180}
case "$dpi$limit" in *[!0-9]*) echo "render.sh: dpi and RENDER_TIMEOUT must be whole numbers" >&2; exit 2 ;; esac
command -v soffice >/dev/null 2>&1 || {
  echo "render.sh: soffice is not installed. Switch to the office profile first (sandbox_profile, action use, profile office)." >&2
  exit 3
}
command -v pdftoppm >/dev/null 2>&1 || { echo "render.sh: pdftoppm is not installed" >&2; exit 3; }
work=$(mktemp -d /tmp/render.XXXXXX)
trap 'rm -rf "$work"' EXIT INT TERM
mkdir -p "$out" "$work/home"
name=$(basename "${file%.*}")
# Convert a copy, so a name with odd characters or a read-only folder does not matter.
cp "$file" "$work/in.xlsx"
if ! HOME="$work/home" timeout -k 5 "$limit" soffice --headless --norestore --nologo --nofirststartwizard \
  "-env:UserInstallation=file://$work/profile" --convert-to pdf --outdir "$work" "$work/in.xlsx" >"$work/soffice.log" 2>&1; then
  echo "render.sh: soffice failed or ran past ${limit} s:" >&2
  cat "$work/soffice.log" >&2
  exit 1
fi
[ -s "$work/in.pdf" ] || { echo "render.sh: soffice made no PDF:" >&2; cat "$work/soffice.log" >&2; exit 1; }
cp "$work/in.pdf" "$out/$name.pdf"
rm -f "$out"/page-*.png
timeout -k 5 "$limit" pdftoppm -r "$dpi" -png "$work/in.pdf" "$out/page"
ls "$out"/page-*.png

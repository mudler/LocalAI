#!/bin/sh
# SPDX-License-Identifier: MIT
# Recalculate a .xlsx with LibreOffice and save a copy that has the cached values.
#
#   sh recalc.sh book.xlsx [out.xlsx]
#
# A workbook written by openpyxl has formulas and no cached values, so a reader
# that does not calculate (openpyxl with data_only=True, a preview) sees None.
# This loads the workbook in LibreOffice Calc, which calculates every formula,
# and saves it again. Read the values from the copy:
#
#   openpyxl.load_workbook("out.xlsx", data_only=True)
#
# It needs the sandbox profile "office". The default output is the input's name
# with "-recalc" added, next to it. The input is never changed. The copy is
# written by LibreOffice, not by Excel, and LibreOffice may change styles, charts
# or features it does not support: compare the copy with the original before you
# give it to the user, and keep the original when something was lost. Also look
# at the values: a formula that LibreOffice does not know gives an error value
# such as #NAME?.
#
# LibreOffice gets a private profile directory under /tmp and a private HOME, and
# the whole conversion is stopped after RECALC_TIMEOUT seconds (default 180).
set -eu
file=${1:-}
[ -n "$file" ] && [ -f "$file" ] || { echo "usage: recalc.sh book.xlsx [out.xlsx]" >&2; exit 2; }
out=${2:-${file%.*}-recalc.xlsx}
limit=${RECALC_TIMEOUT:-180}
case "$limit" in ''|*[!0-9]*) echo "recalc.sh: RECALC_TIMEOUT must be a whole number" >&2; exit 2 ;; esac
command -v soffice >/dev/null 2>&1 || {
  echo "recalc.sh: soffice is not installed. Switch to the office profile first (sandbox_profile, action use, profile office)." >&2
  exit 3
}
work=$(mktemp -d /tmp/recalc.XXXXXX)
trap 'rm -rf "$work"' EXIT INT TERM
mkdir -p "$work/home" "$work/out"
cp "$file" "$work/in.xlsx"
if ! HOME="$work/home" timeout -k 5 "$limit" soffice --headless --norestore --nologo --nofirststartwizard \
  "-env:UserInstallation=file://$work/profile" --convert-to xlsx --outdir "$work/out" "$work/in.xlsx" >"$work/soffice.log" 2>&1; then
  echo "recalc.sh: soffice failed or ran past ${limit} s:" >&2
  cat "$work/soffice.log" >&2
  exit 1
fi
[ -s "$work/out/in.xlsx" ] || { echo "recalc.sh: soffice made no workbook:" >&2; cat "$work/soffice.log" >&2; exit 1; }
cp "$work/out/in.xlsx" "$out"
echo "$out"

#!/bin/sh
# Tests the render and recalc helpers of document-skills/ (pptx, docx, xlsx)
# with a fake soffice and a fake pdftoppm, so they run anywhere. The real
# conversions are covered by the office image build (check-office-*). Run:
#   sh addons/sandbox/images/guest/render_test.sh
set -eu
here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
work=$(mktemp -d "${TMPDIR:-.}/render-test.XXXXXX")
trap 'rm -rf "$work"' EXIT HUP INT TERM
bin="$work/bin"
mkdir "$bin"
for t in sh cp rm mkdir mktemp basename cat ls timeout dirname sleep; do
  ln -s "$(command -v $t)" "$bin/$t"
done
ok() { echo "ok: $1"; }
bad() { echo "FAIL: $1" >&2; cat "$work/err" >&2 2>/dev/null || true; exit 1; }
export FAKE_LOG="$work/log"
fake_soffice() { # $1 = what the fake does: pdf | xlsx | hang | nothing
  case $1 in
  pdf) body='printf "%%PDF fake" >"$outdir/in.pdf"' ;;
  xlsx) body='printf "recalculated" >"$outdir/in.xlsx"' ;;
  hang) body='sleep 30' ;;
  nothing) body=: ;;
  esac
  cat >"$bin/soffice" <<SH
#!/bin/sh
echo "HOME=\$HOME" >"\$FAKE_LOG"
for a in "\$@"; do echo "arg=\$a" >>"\$FAKE_LOG"; done
outdir=
while [ \$# -gt 0 ]; do [ "\$1" = --outdir ] && outdir=\$2; shift; done
$body
SH
  chmod 755 "$bin/soffice"
}
cat >"$bin/pdftoppm" <<'SH'
#!/bin/sh
echo "pdftoppm $*" >>"$FAKE_LOG"
for last; do :; done
printf png >"${last}-1.png"; printf png >"${last}-2.png"
SH
chmod 755 "$bin/pdftoppm"

# Cases shared by the three render helpers. $1 = kind, $2 = PNG name prefix.
render_cases() {
  kind=$1 prefix=$2
  script="$here/document-skills/$kind/render.sh"
  file="$work/doc.$kind"
  printf 'not a real file' >"$file"
  run() { PATH="$bin" /bin/sh "$script" "$@" >"$work/out" 2>"$work/err"; }
  rm -f "$bin/soffice"
  if run "$file" "$work/$kind.o1"; then bad "$kind: it must fail without soffice"; fi
  grep -q 'office profile' "$work/err" || bad "$kind: the message must name the office profile"
  ok "$kind render: fails and names the office profile when soffice is missing"
  fake_soffice pdf
  run "$file" "$work/$kind.o2" 100 || bad "$kind: a good run must pass"
  [ -s "$work/$kind.o2/$prefix-1.png" ] && [ -s "$work/$kind.o2/$prefix-2.png" ] && [ -s "$work/$kind.o2/doc.pdf" ] || bad "$kind: PNGs and the PDF must be in the output folder"
  grep -q "$prefix-1.png" "$work/out" || bad "$kind: the PNG paths must be printed"
  grep -q 'pdftoppm -r 100 -png' "$work/log" || bad "$kind: the dpi must reach pdftoppm"
  ok "$kind render: makes PNGs and the PDF and prints the paths"
  grep -q '^arg=-env:UserInstallation=file:///tmp/render\.' "$work/log" || bad "$kind: soffice must get a private profile directory under /tmp"
  grep -q '^arg=--headless' "$work/log" || bad "$kind: soffice must run headless"
  home=$(sed -n 's/^HOME=//p' "$work/log")
  case "$home" in /tmp/render.*) ;; *) bad "$kind: HOME must be private, got $home" ;; esac
  ok "$kind render: uses a private profile directory and a private HOME"
  HOME=/nonexistent run "$work/doc.$kind" "$work/$kind.o3" || bad "$kind: it must not need a writable HOME"
  ok "$kind render: does not need a writable HOME"
  fake_soffice hang
  start=$(date +%s)
  if RENDER_TIMEOUT=2 run "$file" "$work/$kind.o4"; then bad "$kind: a hung soffice must fail"; fi
  [ $(( $(date +%s) - start )) -lt 15 ] || bad "$kind: the timeout did not stop soffice"
  grep -q 'ran past 2 s' "$work/err" || bad "$kind: the message must say that it timed out"
  ok "$kind render: stops a hung soffice after the timeout"
  fake_soffice nothing
  if run "$file" "$work/$kind.o5"; then bad "$kind: no PDF must fail"; fi
  ok "$kind render: fails when soffice makes no PDF"
  if run "$work/missing.$kind"; then bad "$kind: a missing file must fail"; fi
  if RENDER_TIMEOUT=abc run "$file"; then bad "$kind: a bad timeout must fail"; fi
  ok "$kind render: refuses a missing file and a bad timeout"
}
render_cases pptx slide
render_cases docx page
render_cases xlsx page

# recalc.sh
script="$here/document-skills/xlsx/recalc.sh"
printf 'orig' >"$work/book.xlsx"
run() { PATH="$bin" /bin/sh "$script" "$@" >"$work/out" 2>"$work/err"; }
rm -f "$bin/soffice"
if run "$work/book.xlsx"; then bad 'recalc: it must fail without soffice'; fi
grep -q 'office profile' "$work/err" || bad 'recalc: the message must name the office profile'
ok 'recalc: fails and names the office profile when soffice is missing'
fake_soffice xlsx
HOME=/nonexistent run "$work/book.xlsx" || bad 'recalc: a good run must pass'
[ "$(cat "$work/book-recalc.xlsx")" = recalculated ] || bad 'recalc: the default output is the name plus -recalc'
[ "$(cat "$work/book.xlsx")" = orig ] || bad 'recalc: the input must not change'
grep -q 'book-recalc.xlsx' "$work/out" || bad 'recalc: the output path must be printed'
grep -q '^arg=-env:UserInstallation=file:///tmp/recalc\.' "$work/log" || bad 'recalc: private profile directory'
grep -q '^arg=xlsx' "$work/log" || bad 'recalc: soffice must convert to xlsx'
ok 'recalc: saves a copy, keeps the input, uses a private profile and no writable HOME'
run "$work/book.xlsx" "$work/named.xlsx" || bad 'recalc: an explicit output must work'
[ -s "$work/named.xlsx" ] || bad 'recalc: the explicit output is missing'
ok 'recalc: writes to the output path given'
fake_soffice hang
if RECALC_TIMEOUT=2 run "$work/book.xlsx"; then bad 'recalc: a hung soffice must fail'; fi
grep -q 'ran past 2 s' "$work/err" || bad 'recalc: the message must say that it timed out'
fake_soffice nothing
if run "$work/book.xlsx"; then bad 'recalc: no workbook must fail'; fi
if run "$work/missing.xlsx"; then bad 'recalc: a missing file must fail'; fi
ok 'recalc: stops a hung soffice and fails on no output or a missing input'
left=$(ls -d /tmp/render.* /tmp/recalc.* 2>/dev/null | wc -l)
[ "$left" = 0 ] || bad 'the private directories must be removed'
ok 'cleans up its private directories'

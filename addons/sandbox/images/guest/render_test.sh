#!/bin/sh
# Tests document-skills/pptx/render.sh with a fake soffice and a fake pdftoppm,
# so it runs anywhere. The real conversion is covered by the office image build
# (check-office-convert). Run: sh addons/sandbox/images/guest/render_test.sh
set -eu
here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
render="$here/document-skills/pptx/render.sh"
work=$(mktemp -d "${TMPDIR:-.}/render-test.XXXXXX")
trap 'rm -rf "$work"' EXIT HUP INT TERM
bin="$work/bin"
mkdir "$bin"
for t in sh cp rm mkdir mktemp basename cat ls timeout dirname sleep; do
  ln -s "$(command -v $t)" "$bin/$t"
done
ok() { echo "ok: $1"; }
bad() { echo "FAIL: $1" >&2; cat "$work/err" >&2 2>/dev/null || true; exit 1; }
printf 'not a real deck' >"$work/deck.pptx"
run() { PATH="$bin" /bin/sh "$render" "$@" >"$work/out" 2>"$work/err"; }

# No soffice: a clear message that names the profile to switch to.
if run "$work/deck.pptx" "$work/o1"; then bad 'it must fail without soffice'; fi
grep -q 'office profile' "$work/err" || bad 'the message must name the office profile'
ok 'fails and names the office profile when soffice is missing'

# A fake soffice that logs its arguments, HOME and makes a PDF.
cat >"$bin/soffice" <<'SH'
#!/bin/sh
echo "HOME=$HOME" >"$FAKE_LOG"
for a in "$@"; do echo "arg=$a" >>"$FAKE_LOG"; done
outdir=
while [ $# -gt 0 ]; do [ "$1" = --outdir ] && outdir=$2; shift; done
printf '%%PDF fake' >"$outdir/in.pdf"
SH
cat >"$bin/pdftoppm" <<'SH'
#!/bin/sh
echo "pdftoppm $*" >>"$FAKE_LOG"
for last; do :; done
printf png >"${last}-1.png"; printf png >"${last}-2.png"
SH
chmod 755 "$bin/soffice" "$bin/pdftoppm"
export FAKE_LOG="$work/log"
run "$work/deck.pptx" "$work/o2" 100 || bad 'a good run must pass'
[ -s "$work/o2/slide-1.png" ] && [ -s "$work/o2/slide-2.png" ] && [ -s "$work/o2/deck.pdf" ] || bad 'PNGs and the PDF must be in the output folder'
grep -q 'slide-1.png' "$work/out" || bad 'the PNG paths must be printed'
grep -q 'pdftoppm -r 100 -png' "$work/log" || bad 'the dpi must reach pdftoppm'
ok 'renders PNGs and the PDF and prints the paths'

grep -q '^arg=-env:UserInstallation=file:///tmp/render\.' "$work/log" || bad 'soffice must get a private profile directory under /tmp'
grep -q '^arg=--headless' "$work/log" || bad 'soffice must run headless'
home=$(sed -n 's/^HOME=//p' "$work/log")
case "$home" in /tmp/render.*) ;; *) bad "HOME must be private, got $home" ;; esac
ok 'uses a private profile directory and a private HOME'

# Nothing is left in /tmp, and a read-only or unset HOME does not matter.
left=$(ls -d /tmp/render.* 2>/dev/null | wc -l)
[ "$left" = 0 ] || bad 'the private directory must be removed'
HOME=/nonexistent run "$work/deck.pptx" "$work/o3" || bad 'it must not need a writable HOME'
ok 'cleans up and does not need a writable HOME'

# A soffice that hangs is stopped by the timeout.
printf '#!/bin/sh\nsleep 30\n' >"$bin/soffice"
start=$(date +%s)
if RENDER_TIMEOUT=2 run "$work/deck.pptx" "$work/o4"; then bad 'a hung soffice must fail'; fi
[ $(( $(date +%s) - start )) -lt 15 ] || bad 'the timeout did not stop soffice'
grep -q 'ran past 2 s' "$work/err" || bad 'the message must say that it timed out'
ok 'stops a hung soffice after the timeout'

# A soffice that makes no PDF is an error.
printf '#!/bin/sh\nexit 0\n' >"$bin/soffice"
if run "$work/deck.pptx" "$work/o5"; then bad 'no PDF must fail'; fi
ok 'fails when soffice makes no PDF'

if run "$work/missing.pptx"; then bad 'a missing deck must fail'; fi
if RENDER_TIMEOUT=abc run "$work/deck.pptx"; then bad 'a bad timeout must fail'; fi
ok 'refuses a missing deck and a bad timeout'

#!/bin/sh
# Tests check-tools.sh with a fake manifest and a fake PATH. Run it from
# anywhere: sh addons/sandbox/images/guest/check-tools_test.sh
set -eu
here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
work=$(mktemp -d "${TMPDIR:-.}/check-tools-test.XXXXXX")
trap 'rm -rf "$work"' EXIT HUP INT TERM
mkdir "$work/bin"
real_jq=$(command -v jq)
ln -s "$real_jq" "$work/bin/jq"
for t in alpha beta gamma; do printf '#!/bin/sh\nexit 0\n' >"$work/bin/$t"; chmod 755 "$work/bin/$t"; done
cat >"$work/m.json" <<'JSON'
{"capabilities":{"toolDefinitions":[
 {"name":"status"},
 {"name":"bash","execution":{"profiles":[
  {"id":"base","tools":[{"name":"alpha","check":["alpha --version"]},{"name":"beta","check":["beta -v","beta -x"]}]},
  {"id":"more","tools":[{"name":"alpha","check":["alpha --version"]},{"name":"delta","check":["delta --version"]}]},
  {"id":"empty","tools":[]},
  {"id":"nocheck","tools":[{"name":"alpha","check":["alpha --version"]},{"name":"gamma"}]}
 ]}}]}}
JSON
run() { PATH="$work/bin" /bin/sh "$here/check-tools.sh" "$@" 2>"$work/err" >/dev/null; }
ok() { echo "ok: $1"; }
bad() { echo "FAIL: $1" >&2; cat "$work/err" >&2; exit 1; }
run base "$work/m.json" || bad 'base passes when every tool exists'
ok 'passes when every tool exists'
if run more "$work/m.json"; then bad 'a missing tool must fail'; fi
grep -q 'delta' "$work/err" || bad 'the error must name the missing tool'
ok 'fails and names the missing tool'
if run nope "$work/m.json"; then bad 'an unknown profile must fail'; fi
grep -q 'no profile nope' "$work/err" || bad 'the error must name the profile'
ok 'fails on an unknown profile'
if run empty "$work/m.json"; then bad 'a profile without checks must fail'; fi
ok 'fails on a profile without checks'
if run nocheck "$work/m.json"; then bad 'a tool without a check must fail'; fi
grep -q 'gamma' "$work/err" || bad 'the error must name the unchecked tool'
ok 'fails on a tool without a check'
if run base "$work/absent.json"; then bad 'a missing manifest must fail'; fi
ok 'fails without a manifest'
# The real manifest: every profile has a check for every tool, and the
# default profile argument is base.
manifest="$here/../../manifest.json"
for p in $(jq -r '.capabilities.toolDefinitions[] | select(.name=="bash") | .execution.profiles[].id' "$manifest"); do
  n=$(jq -r --arg p "$p" '.capabilities.toolDefinitions[] | select(.name=="bash") | .execution.profiles[] | select(.id==$p) | [.tools[] | select((.check // []) | length == 0)] | length' "$manifest")
  [ "$n" = 0 ] || { echo "FAIL: profile $p has $n tools without a check" >&2; exit 1; }
done
ok 'every tool of every profile of the real manifest has a check'

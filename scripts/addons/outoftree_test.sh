#!/bin/sh
# SPDX-License-Identifier: MIT
# Acceptance check for add-ons that live in another repository. It copies
# examples/addon-hello to a directory outside this checkout, points its go.mod
# at this checkout with a replace, and then:
#
#   1. builds the executable and runs the tests of the copy (own go.mod),
#   2. builds the zip with  zip.sh --root,
#   3. builds a layout with package.sh --root and runs  addonpkg verify,
#   4. checks the zip layout and the .sha256 file,
#   5. gives the zip to the installer code (packages.InspectFile), which is
#      what "local-ai addons install" runs on the server side.
#
# It prints one "ok:" line per step and fails on the first error. It never
# pushes or installs anything outside its work directory.
#
#   sh scripts/addons/outoftree_test.sh
#
# ADDON_OOT_WORK names the work directory (default: a new directory under
# TMPDIR, which must not be inside this checkout). The directory is removed at
# the end unless ADDON_OOT_KEEP=1.
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$root"
work=${ADDON_OOT_WORK:-}
if [ -z "$work" ]; then
    work=$(mktemp -d "${TMPDIR:-/tmp}/addon-outoftree.XXXXXX")
else
    mkdir -p "$work"
    work=$(CDPATH= cd -- "$work" && pwd)
fi
case "$work/" in "$root"/*) echo "outoftree_test.sh: the work directory must be outside the checkout" >&2; exit 2 ;; esac
trap '[ -n "${ADDON_OOT_KEEP-}" ] || { chmod -R u+rwx "$work" 2>/dev/null; rm -rf "$work"; }' EXIT HUP INT TERM
ok() { echo "ok: $*"; }
fail() { echo "FAIL: $*" >&2; exit 1; }

src=$work/addon-hello
mkdir -p "$src"
cp -R examples/addon-hello/. "$src/"
rm -rf "$src/dist"
(cd "$src" && go mod edit -replace "github.com/mudler/LocalAI=$root")

# 1. The copy builds and passes its own tests as a separate module.
(cd "$src" && GOFLAGS=-mod=mod go vet ./... && GOFLAGS=-mod=mod go test -count=1 ./... >/dev/null) || fail "the copy does not build or pass its tests"
ok "the copy builds and passes its tests as its own module"

host="$(go env GOOS)/$(go env GOARCH)"
version=$(sed -n 's/^ *"version": *"\([^"]*\)".*/\1/p' "$src/manifest.json" | head -1)
id=$(sed -n 's/^addon: //p' "$src/package/platforms.yaml")
name=$id-$version-$(echo "$host" | tr '/' '-')

# 2. zip.sh --root. GOFLAGS=-mod=mod lets the copy resolve its requirements.
out=$work/out
GOFLAGS=-mod=mod sh scripts/addons/zip.sh --root "$src" --out "$out" >"$work/zip.log" 2>&1 || { cat "$work/zip.log" >&2; fail "zip.sh --root failed"; }
[ -f "$out/$name.zip" ] && [ -f "$out/$name.zip.sha256" ] || fail "zip or sha256 file is missing in $out"
ok "zip.sh --root wrote $name.zip and its .sha256"

# 3. package.sh --root, then addonpkg verify on the layout.
tool=$work/addonpkg
go build -o "$tool" ./scripts/addons/cmd/addonpkg
ADDONPKG=$tool GOFLAGS=-mod=mod sh scripts/addons/package.sh --root "$src" --layout "$work/layout" --version "$version" --platform "$host" >"$work/package.log" 2>&1 || { cat "$work/package.log" >&2; fail "package.sh --root failed"; }
"$tool" verify --config "$src/package/platforms.yaml" --layout "$work/layout" --platform "$host" --version "$version" >"$work/verify.log" 2>&1 || { cat "$work/verify.log" >&2; fail "addonpkg verify failed"; }
grep -q "^verified=$id $version $host" "$work/verify.log" || { cat "$work/verify.log" >&2; fail "addonpkg verify did not report the package"; }
ok "package.sh --root built a layout and addonpkg verify accepted it"

# 4. Zip layout and checksum.
(cd "$out" && sha256sum -c "$name.zip.sha256" >/dev/null) || fail "the .sha256 file does not match the zip"
python3 -I - "$out/$name.zip" "$id" <<'PY' || fail "unexpected zip layout"
import sys, zipfile
z = zipfile.ZipFile(sys.argv[1])
names = sorted(i.filename for i in z.infolist() if not i.is_dir())
want = sorted(["manifest.json", "zip-manifest.json", sys.argv[2], "skills/%s/SKILL.md" % sys.argv[2]])
extra = [n for n in names if n not in want and not n.startswith("skills/")]
missing = [n for n in want if n not in names]
if missing or extra:
    print("zip holds", names, "missing", missing, "unexpected", extra, file=sys.stderr)
    sys.exit(1)
exe = [i for i in z.infolist() if i.filename == sys.argv[2]][0]
if (exe.external_attr >> 16) & 0o111 == 0:
    print("the executable is not marked executable", file=sys.stderr)
    sys.exit(1)
PY
ok "the .sha256 file matches and the zip holds the executable, manifest.json and the skill"

# 5. The installer code accepts the file.
ADDON_ZIP_UNDER_TEST=$out/$name.zip ADDON_ID_UNDER_TEST=$id ADDON_VERSION_UNDER_TEST=$version \
    go test -count=1 -run TestInspectExternalZip ./core/services/addons/packages >"$work/inspect.log" 2>&1 || { cat "$work/inspect.log" >&2; fail "the installer refuses the zip"; }
grep -q "^ok" "$work/inspect.log" || { cat "$work/inspect.log" >&2; fail "the installer check did not run"; }
ok "the installer code (InspectFile) accepts the zip"
echo "outoftree_test.sh: all steps passed"

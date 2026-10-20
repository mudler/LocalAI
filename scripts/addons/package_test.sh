#!/bin/sh
# SPDX-License-Identifier: MIT
# Packaging checks of the first-party add-on OCI package. It prints one JSON
# line per case; scripts/addons/verify.sh compares the lines with the exact
# inventory of the "packaging" suite and rejects a skipped or missing case.
#
#   1. Go specs of pkg/addons/packaging: layout rules, rejected packages,
#      event and channel policy, merge completeness, publication and alias
#      promotion against an in-process registry, catalog template, and the
#      workflow policy with one failing mutant per rule.
#   2. The installer contract: the package built by package.sh is installed
#      by the real installer from a real local OCI distribution and its
#      executable is started by the real supervisor.
#   3. Shell entry points: package.sh, channel.sh, the workflow policy
#      command and static checks that nothing here pushes, tags or signs.
#
# Nothing here pushes, tags, signs or logs in: registries are loopback
# distributions inside the test process and signatures come from a fixture
# authority.
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$root"
cache="$root/.cache/addons"
mkdir -p "$cache/tmp" "$cache/logs"
export TMPDIR="$cache/tmp"
export PATH="$cache/bin:$PATH"
export GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOFLAGS=-mod=readonly
export GOPATH="$cache/go" GOMODCACHE="$cache/go/pkg/mod" GOCACHE="$cache/go-build"
work=$(mktemp -d "$cache/tmp/packaging.XXXXXX")
trap 'chmod -R u+rwx "$work" 2>/dev/null; rm -rf "$work"' EXIT HUP INT TERM

# The UI is a build output of this checkout; build it so the package holds
# the current files.
node addons/tables/ui/build.mjs >"$cache/logs/packaging-ui-build.log" 2>&1 || { cat "$cache/logs/packaging-ui-build.log" >&2; exit 1; }

go_cases() {
    python3 -I - "$1" <<'PY'
import json, sys
for report in json.load(open(sys.argv[1])):
    for spec in report['SpecReports']:
        if spec['LeafNodeType'] != 'It' or spec['State'] == 'skipped' and not spec.get('Failure'):
            continue
        status = {'passed': 'passed'}.get(spec['State'], 'failed')
        print(json.dumps({'case': 'go: ' + spec['LeafNodeText'], 'status': status}))
PY
}
run_go() {
    name=$1
    shift
    report="$cache/logs/packaging-$name.json"
    rm -f "$report"
    go test -count=1 -p 2 -tags auth "$@" -ginkgo.json-report="$report" >"$cache/logs/packaging-$name.log" 2>&1 || { tail -60 "$cache/logs/packaging-$name.log" >&2; exit 1; }
    go_cases "$report"
}
run_go specs ./pkg/addons/packaging
run_go installer ./tests/addons/packaging

# Shell entry points.
tool="$work/addonpkg"
go build -o "$tool" ./scripts/addons/cmd/addonpkg
export ADDONPKG="$tool"
case_ok() { printf '{"case":"%s","status":"passed"}\n' "$1"; }
expect_fail() { # name expected-text command...
    name=$1; expected=$2; shift 2
    if "$@" >"$work/out" 2>&1; then echo "FAIL $name: unexpectedly succeeded" >&2; cat "$work/out" >&2; exit 1; fi
    grep -F -- "$expected" "$work/out" >/dev/null || { echo "FAIL $name: wrong failure (expected $expected)" >&2; cat "$work/out" >&2; exit 1; }
    case_ok "$name"
}
sha=0123456789abcdef0123456789abcdef01234567
host="$(go env GOOS)/$(go env GOARCH)"
printf '#!/bin/sh\nexit 2\n' >"$work/stub"
chmod 755 "$work/stub"

# package.sh: a local check layout for the host builds and verifies.
sh scripts/addons/package.sh --layout "$work/leg" --version 1.2.3 --platform "$host" --executable "$work/stub" --local-check --revision "$sha" >"$work/out" 2>&1 || { cat "$work/out" >&2; exit 1; }
grep -F 'publishable=false' "$work/out" >/dev/null && grep -F 'verified=tables 1.2.3' "$work/out" >/dev/null
test -f "$work/leg/receipt.json" && test -f "$work/leg/index.json" && test -f "$work/leg/oci-layout"
case_ok 'package.sh builds and verifies a local check layout'
expect_fail 'package.sh refuses a platform the matrix has not enabled' 'not enabled' sh scripts/addons/package.sh --layout "$work/leg2" --version 1.2.3 --platform "$host" --executable "$work/stub"
expect_fail 'package.sh refuses an existing non-empty layout' 'not empty' sh scripts/addons/package.sh --layout "$work/leg" --version 1.2.3 --platform "$host" --executable "$work/stub" --local-check
expect_fail 'package.sh requires its arguments' 'usage:' sh scripts/addons/package.sh --version 1.2.3
expect_fail 'package.sh refuses an invalid version' 'semantic version' sh scripts/addons/package.sh --layout "$work/leg3" --version v1 --platform "$host" --executable "$work/stub" --local-check
# package.sh reads the add-on from its platform file. The voice add-on is built
# for the host as a local check, and its layout verifies.
sh scripts/addons/package.sh --config addons/voice/package/platforms.yaml --layout "$work/voice" --version 1.2.3 --platform "$host" --local-check --revision "$sha" >"$work/out" 2>&1 || { cat "$work/out" >&2; exit 1; }
grep -F 'verified=voice 1.2.3' "$work/out" >/dev/null
test -f "$work/voice/receipt.json"
case_ok 'package.sh builds and verifies the voice add-on for the host'
# A platform other than the host is refused for every add-on, whatever the config.
if [ "$host" = linux/amd64 ]; then foreign=linux/arm64; else foreign=linux/amd64; fi
for id in tables voice face fine-tuning quantization; do
    expect_fail "package.sh refuses a foreign platform without an executable: $id" 'is not this host' sh scripts/addons/package.sh --config "addons/$id/package/platforms.yaml" --layout "$work/foreign-$id" --version 1.2.3 --platform "$foreign" --local-check
done
case_ok 'package.sh refuses a foreign platform without an executable for every add-on'
# The build line: cgo for Tables, none for the pure Go add-ons. A stub go
# records the environment and the package it was asked to build.
realgo=$(command -v go)
mkdir "$work/stubbin"
cat >"$work/stubbin/go" <<STUB
#!/bin/sh
if [ "\$1" = build ]; then
    out=
    for a in "\$@"; do
        if [ "\$prev" = -o ]; then out=\$a; fi
        prev=\$a
        last=\$a
    done
    printf '%s CGO_ENABLED=%s\n' "\$last" "\${CGO_ENABLED-unset}" >>"$work/gobuild.log"
    printf '#!/bin/sh\nexit 2\n' >"\$out"
    chmod 755 "\$out"
    exit 0
fi
exec "$realgo" "\$@"
STUB
chmod 755 "$work/stubbin/go"
: >"$work/gobuild.log"
for id in tables voice face fine-tuning quantization; do
    PATH="$work/stubbin:$PATH" sh scripts/addons/package.sh --config "addons/$id/package/platforms.yaml" --layout "$work/stub-$id" --version 1.2.3 --platform "$host" --local-check >"$work/out" 2>&1 || { cat "$work/out" >&2; exit 1; }
done
grep -x './addons/tables/cmd/tables CGO_ENABLED=1' "$work/gobuild.log" >/dev/null
for id in voice face fine-tuning quantization; do
    grep -x "./addons/$id/cmd/$id CGO_ENABLED=0" "$work/gobuild.log" >/dev/null || { echo "FAIL: $id is not built with CGO_ENABLED=0" >&2; cat "$work/gobuild.log" >&2; exit 1; }
done
case_ok 'package.sh builds Tables with cgo and the pure Go add-ons without'
expect_fail 'matrix fails closed while no platform is enabled' 'no enabled platform' "$tool" matrix --config addons/tables/package/platforms.yaml
expect_fail 'matrix fails closed without a file' 'platform matrix' "$tool" matrix --config "$work/absent.yaml"

# channel.sh.
channel() { sh scripts/addons/channel.sh "$@"; }
out=$(channel --event pull_request --ref refs/pull/7/merge --sha "$sha")
printf '%s\n' "$out" | grep -x 'publish=false' >/dev/null && printf '%s\n' "$out" | grep -x 'aliases=' >/dev/null
case_ok 'channel.sh: a pull request never publishes'
out=$(channel --event push --ref refs/heads/master --sha "$sha")
printf '%s\n' "$out" | grep -x 'aliases=development' >/dev/null && printf '%s\n' "$out" | grep -x 'immutable=sha-0123456789ab' >/dev/null
case_ok 'channel.sh: master publishes development'
out=$(channel --event push --ref refs/heads/latest --sha "$sha")
printf '%s\n' "$out" | grep -x 'publish=false' >/dev/null && printf '%s\n' "$out" | grep -x 'aliases=' >/dev/null && printf '%s\n' "$out" | grep -x 'channel=' >/dev/null
case_ok 'channel.sh: a branch push never publishes, even a branch named latest'
out=$(channel --event push --ref refs/tags/v1.2.3 --sha "$sha")
printf '%s\n' "$out" | grep -x 'aliases=' >/dev/null && printf '%s\n' "$out" | grep -x 'immutable=1.2.3' >/dev/null
case_ok 'channel.sh: a tag push prepares the version and moves no alias'
out=$(channel --event release --ref refs/tags/v1.2.3 --sha "$sha")
printf '%s\n' "$out" | grep -x 'aliases=latest' >/dev/null
case_ok 'channel.sh: a published stable release promotes latest'
out=$(channel --event release --ref refs/tags/v1.2.3 --sha "$sha" --prerelease)
printf '%s\n' "$out" | grep -x 'aliases=' >/dev/null
case_ok 'channel.sh: a pre-release never promotes latest'
expect_fail 'channel.sh: a draft release fails' 'draft' channel --event release --ref refs/tags/v1.2.3 --sha "$sha" --draft
expect_fail 'channel.sh: an unknown event fails' 'not allowed to publish' channel --event workflow_dispatch --ref refs/heads/master --sha "$sha"
expect_fail 'channel.sh: a tag that is not a version fails' 'release tag' channel --event push --ref refs/tags/latest --sha "$sha"

# Workflow policy.
"$tool" policy --workflow .github/workflows/addons-package.yml >"$work/out" 2>&1 || { cat "$work/out" >&2; exit 1; }
case_ok 'workflow policy accepts the checked-in workflow'
sed 's/^  release:$/  pull_request_target:\n    types: [opened]\n  release:/' .github/workflows/addons-package.yml >"$work/mutant.yml"
expect_fail 'workflow policy rejects pull_request_target' 'P_PR_TARGET' "$tool" policy --workflow "$work/mutant.yml"
sed 's/^    needs: \[plan, validate, package\]$/    needs: [plan, package]/' .github/workflows/addons-package.yml >"$work/mutant.yml"
expect_fail 'workflow policy rejects a publish job that does not wait for every leg' 'P_NEEDS' "$tool" policy --workflow "$work/mutant.yml"
sed 's/^    name: validate package rules$/    name: validate package rules\n    continue-on-error: true/' .github/workflows/addons-package.yml >"$work/mutant.yml"
expect_fail 'workflow policy rejects a validate job that cannot stop the release' 'P_VALIDATE_GATE' "$tool" policy --workflow "$work/mutant.yml"

# Nothing in the packaging scripts pushes, tags, signs or logs in.
if grep -n -E 'docker push|docker login|git tag|git push|oras (push|copy)|cosign (sign|attest)|crane push|gh release' scripts/addons/package.sh scripts/addons/channel.sh scripts/addons/package_test.sh | grep -v '^[^:]*:[0-9]*:#' | grep -v 'grep -n -E' | grep -v 'static checks'; then
    echo 'FAIL: a packaging script pushes, tags or signs' >&2
    exit 1
fi
case_ok 'scripts never push, tag, sign or log in'
if grep -n -i -E 'quay\.io|go-skynet|backend_merge|backend_build|backend\.yml|ci-cache|docker\.io' .github/workflows/addons-package.yml scripts/addons/package.sh scripts/addons/channel.sh addons/*/package/platforms.yaml gallery/addons/tables.yaml.tmpl; then
    echo 'FAIL: a packaging file references the backend pipeline' >&2
    exit 1
fi
case_ok 'packaging files never reference backend registries or workflows'

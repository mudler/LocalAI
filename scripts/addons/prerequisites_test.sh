#!/bin/sh
# SPDX-License-Identifier: MIT
# Fixtures are isolated copies: never substitute fake tools in a real readiness run.
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
export TMPDIR="$root/.cache/addons/tmp"
mkdir -p "$TMPDIR"
fixture=$(mktemp -d)
trap 'rm -rf "$fixture"' EXIT HUP INT TERM
mkdir -p "$fixture/scripts/addons" "$fixture/bin"
for file in prepare.sh verify.sh suites.json; do
    test -f "$root/scripts/addons/$file" || { echo "missing implementation: $file" >&2; exit 1; }
    cp "$root/scripts/addons/$file" "$fixture/scripts/addons/$file"
done
cat > "$fixture/bin/go" <<'TOOL'
#!/bin/sh
case "$*" in
version) echo "go version ${FIXTURE_GO:-go1.26.1} linux/amd64";;
*) exit 91;;
esac
TOOL
cat > "$fixture/bin/node" <<'TOOL'
#!/bin/sh
echo "${FIXTURE_NODE:-v22.22.0}"
TOOL
cat > "$fixture/bin/protoc" <<'TOOL'
#!/bin/sh
echo "${FIXTURE_PROTOC:-libprotoc 26.1}"
TOOL
chmod +x "$fixture/bin/"*
count=0
reject() {
    name=$1; expected=$2; shift 2
    if "$@" > "$fixture/output" 2>&1; then
        echo "FAIL $name: unexpectedly succeeded" >&2; exit 1
    fi
    if ! grep -F "$expected" "$fixture/output" >/dev/null; then
        echo "FAIL $name: wrong failure (expected $expected)" >&2
        cat "$fixture/output" >&2; exit 1
    fi
    count=$((count + 1))
    printf '{"case":"%s","status":"passed"}\n' "$name"
}
prepare="$fixture/scripts/addons/prepare.sh"
verify="$fixture/scripts/addons/verify.sh"
reject missing-go 'Go 1.26' env PATH="$fixture/bin:/usr/bin:/bin" FIXTURE_GO=missing sh "$prepare" --check
reject old-go 'Go 1.26' env PATH="$fixture/bin:$PATH" FIXTURE_GO=go1.25.9 sh "$prepare" --check
reject future-go 'Go 1.26' env PATH="$fixture/bin:$PATH" FIXTURE_GO=go1.27.0 sh "$prepare" --check
reject old-node 'Node 22.12' env PATH="$fixture/bin:$PATH" FIXTURE_NODE=v22.11.0 sh "$prepare" --check
reject wrong-node 'Node 22.12' env PATH="$fixture/bin:$PATH" FIXTURE_NODE=v24.0.0 sh "$prepare" --check
reject wrong-protoc 'protoc 26.1' env PATH="$fixture/bin:$PATH" FIXTURE_PROTOC='libprotoc 31.1' sh "$prepare" --check
reject absent-generated 'generated file' env PATH="$fixture/bin:$PATH" sh "$prepare" --check
reject empty-name 'suite' sh "$verify" ''
reject future-suite 'suite' sh "$verify" contracts
# Replace the copied inventory and children, not production behavior.
cat > "$fixture/scripts/addons/suites.json" <<'JSON'
{"prerequisites":{"kind":"shell","commands":[],"cases":[]}}
JSON
reject empty-selection 'empty' sh "$verify" prerequisites
cat > "$fixture/scripts/addons/suites.json" <<'JSON'
{"prerequisites":{"kind":"shell","commands":[["sh","scripts/addons/prerequisites_test.sh"]],"cases":["required"]}}
JSON
printf '#!/bin/sh\nexit 42\n' > "$fixture/scripts/addons/prerequisites_test.sh"
reject failed-child '42' sh "$verify" prerequisites
printf '#!/bin/sh\nexit 0\n' > "$fixture/scripts/addons/prerequisites_test.sh"
reject zero-tests 'required' sh "$verify" prerequisites
printf '#!/bin/sh\necho '\''{"case":"required","status":"skipped"}'\''\n' > "$fixture/scripts/addons/prerequisites_test.sh"
reject skipped-tests 'required' sh "$verify" prerequisites
# Machine-readable Ginkgo reports must reject empty and skipped selections.
cat > "$fixture/scripts/addons/suites.json" <<'JSON'
{"prerequisites":{"kind":"ginkgo","commands":[["go","test","-count=1","-tags","auth","./fixture","-ginkgo.json-report=.cache/report.json"]],"cases":["sqlite transaction"],"report":".cache/report.json"}}
JSON
cat > "$fixture/bin/go" <<'TOOL'
#!/bin/sh
mkdir -p .cache
printf '%s' "$FIXTURE_REPORT" > .cache/report.json
TOOL
reject ginkgo-empty 'Ginkgo report' env PATH="$fixture/bin:$PATH" FIXTURE_REPORT='[]' sh "$verify" prerequisites
reject ginkgo-skipped 'Ginkgo report' env PATH="$fixture/bin:$PATH" FIXTURE_REPORT='[{"SuiteSucceeded":true,"SpecReports":[{"State":"skipped","LeafNodeType":"It","LeafNodeText":"sqlite transaction"}]}]' sh "$verify" prerequisites
reject ginkgo-missing 'Ginkgo report' env PATH="$fixture/bin:$PATH" FIXTURE_REPORT='{}' sh "$verify" prerequisites
# A commit at a tagged module resolves to the tag, with an exact module sum.
cat > "$fixture/bin/go" <<'TOOL'
#!/bin/sh
case "$*" in
version) echo 'go version go1.26.1 linux/amd64';;
'env CGO_ENABLED') echo 1;;
'version -m '*protoc-gen-go-grpc) printf '\tmod\tgoogle.golang.org/grpc/cmd/protoc-gen-go-grpc\tv1.4.0\t%s\n' "${FIXTURE_SUM:-h1:9SxA29VM43MF5Z9dQu694wmY5t8E/Gxr7s+RSxiIDmc=}";;
'version -m '*protoc-gen-go) printf '\tmod\tgoogle.golang.org/protobuf\tv1.34.2\th1:6xV6lTsCfpGD21XK49h7MhtcApnLqkfYgPcdHftf6hg=\n';;
*) exit 91;;
esac
TOOL
for tool in npm cc protoc-gen-go protoc-gen-go-grpc; do
    printf '#!/bin/sh\nexit 0\n' > "$fixture/bin/$tool"
    chmod +x "$fixture/bin/$tool"
done
cat > "$fixture/bin/protoc" <<'TOOL'
#!/bin/sh
if [ "$*" = --version ]; then echo 'libprotoc 26.1'; else echo 'pin accepted: generation sentinel' >&2; exit 92; fi
TOOL
reject exact-tag-pin 'generation sentinel' env PATH="$fixture/bin:$PATH" sh "$prepare" --integration
reject wrong-tag-sum 'protoc-gen-go-grpc' env PATH="$fixture/bin:$PATH" FIXTURE_SUM=h1:wrong sh "$prepare" --integration
# Explicit setup must use the local CLI and worktree browser cache, and
# propagate an installer failure rather than reporting incomplete setup ready.
cat > "$fixture/bin/go" <<'TOOL'
#!/bin/sh
case "$*" in
version) echo 'go version go1.26.1 linux/amd64';;
'env CGO_ENABLED') echo 1;;
*) exit 0;;
esac
TOOL
mkdir -p "$fixture/core/http/react-ui/node_modules/.bin"
cat > "$fixture/core/http/react-ui/node_modules/.bin/playwright" <<'TOOL'
#!/bin/sh
[ "$*" = 'install chromium' ] || exit 93
[ "$PLAYWRIGHT_BROWSERS_PATH" = "$EXPECTED_BROWSER_CACHE" ] || exit 94
echo 'browser installation sentinel' >&2
exit 95
TOOL
chmod +x "$fixture/core/http/react-ui/node_modules/.bin/playwright"
# Select the download branch explicitly, regardless of the caller's browser.
# This is scoped to the fixture child; verify must retain the real browser path.
reject browser-setup-failure 'browser installation sentinel' env PATH="$fixture/bin:$PATH" PLAYWRIGHT_CHROMIUM_PATH= EXPECTED_BROWSER_CACHE="$fixture/.cache/addons/browsers" sh "$prepare" --prepare
reject configured-browser-missing 'configured Chromium is not executable' env PATH="$fixture/bin:$PATH" PLAYWRIGHT_CHROMIUM_PATH="$fixture/missing-chromium" sh "$prepare" --prepare
# Setup only checks executability and must bypass the failing installer above.
# This stub is not browser readiness evidence: verify launches real Chromium.
printf '#!/bin/sh\nexit 96\n' > "$fixture/bin/chromium"
chmod +x "$fixture/bin/chromium"
if ! env PATH="$fixture/bin:$PATH" PLAYWRIGHT_CHROMIUM_PATH="$fixture/bin/chromium" sh "$prepare" --prepare > "$fixture/output" 2>&1; then
    echo 'FAIL configured-browser-setup: unexpectedly failed' >&2
    cat "$fixture/output" >&2; exit 1
fi
count=$((count + 1))
printf '{"case":"configured-browser-setup","status":"passed"}\n'
printf '{"total":%s,"passed":%s,"skipped":0}\n' "$count" "$count"

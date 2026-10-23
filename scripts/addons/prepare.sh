#!/bin/sh
# SPDX-License-Identifier: MIT
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$root"
export GOTOOLCHAIN=local
export GOPATH="$root/.cache/addons/go" GOMODCACHE="$root/.cache/addons/go/pkg/mod"
export GOCACHE="$root/.cache/addons/go-build" GOBIN="$root/.cache/addons/bin"
export npm_config_cache="$root/.cache/addons/npm" PLAYWRIGHT_BROWSERS_PATH="$root/.cache/addons/browsers"
export PATH="$GOBIN:$PATH"
mode=${1:-}
case "$mode" in --check|--prepare|--integration) ;; *) echo 'usage: prepare.sh --check|--prepare|--integration' >&2; exit 2;; esac
[ "$#" -eq 1 ] || exit 2
fail() { echo "$*" >&2; exit 1; }
go version 2>/dev/null | grep -Eq '^go version go1\.26\.[0-9]+ ' || fail 'Go 1.26.x required (GOTOOLCHAIN=local)'
node --version 2>/dev/null | grep -Eq '^v22\.([1-9][2-9]|[2-9][0-9]|[1-9][0-9]{2,})\.[0-9]+$' || fail 'Node 22.12 or later within Node 22 required'
[ "$(protoc --version 2>/dev/null)" = 'libprotoc 26.1' ] || fail 'protoc 26.1 required; provision explicitly (Makefile downloader uses a different version)'
if [ "$mode" = --check ]; then
    # Fail before invoking compilers or package managers if generated inputs are absent.
    for file in pkg/grpc/proto/backend.pb.go pkg/grpc/proto/backend_grpc.pb.go core/http/react-ui/dist/index.html; do
        [ -s "$file" ] || fail "missing generated file: $file; run --integration explicitly"
    done
fi
command -v npm >/dev/null || fail 'npm required'
command -v "${CC:-cc}" >/dev/null || fail 'CGO C compiler required'
[ "$(go env CGO_ENABLED)" = 1 ] || fail 'CGO_ENABLED=1 required'
space() {
    available=$(df -Pk "$root" | awk 'END {print $4}')
    [ "$available" -ge 20971520 ] || fail 'stop: at least 20 GiB free required before setup/build'
}
if [ "$mode" = --prepare ]; then
    space
    mkdir -p "$GOBIN" "$root/.cache/addons/tmp"
    export TMPDIR="$root/.cache/addons/tmp"
    # Exact existing Makefile pins; Go module checksums are authenticated by sum.golang.org.
    go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.34.2
    space
    go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@1958fcbe2ca8bd93af633f11e97d44e567e945af
    space
    go mod download
    space
    (cd core/http/react-ui && npm ci --no-audit --no-fund)
    # Explicit setup only: the lockfile-pinned local CLI selects the browser
    # revision. Never use npx or install host OS packages here.
    if [ -z "${PLAYWRIGHT_CHROMIUM_PATH:-}" ]; then
        space
        core/http/react-ui/node_modules/.bin/playwright install chromium
    else
        [ -x "$PLAYWRIGHT_CHROMIUM_PATH" ] || fail 'configured Chromium is not executable'
    fi
    exit 0
fi
# Go resolves the Makefile commit to cmd/protoc-gen-go-grpc/v1.4.0.
# Bind the tag to the checksum authenticated by sum.golang.org, not its label.
check_plugin() {
    metadata=$(go version -m "$(command -v "$1")") || fail "$1 build metadata unavailable"
    printf '%s\n' "$metadata" | awk -v module="$2" -v version="$3" -v sum="$4" '
        $1 == "mod" && $2 == module && $3 == version && $4 == sum { found=1 }
        $1 == "=>" { replaced=1 }
        END { exit !(found && !replaced) }
    ' || fail "$1 exact module version/checksum required"
}
check_plugin protoc-gen-go google.golang.org/protobuf v1.34.2 'h1:6xV6lTsCfpGD21XK49h7MhtcApnLqkfYgPcdHftf6hg='
check_plugin protoc-gen-go-grpc google.golang.org/grpc/cmd/protoc-gen-go-grpc v1.4.0 'h1:9SxA29VM43MF5Z9dQu694wmY5t8E/Gxr7s+RSxiIDmc='
export GOPROXY=off GOSUMDB=off GOFLAGS=-mod=readonly
if [ "$mode" = --integration ]; then
    space
    mkdir -p pkg/grpc/proto "$root/.cache/addons/tmp"
    export TMPDIR="$root/.cache/addons/tmp"
    protoc --experimental_allow_proto3_optional -Ibackend/ --go_out=pkg/grpc/proto/ --go_opt=paths=source_relative --go-grpc_out=pkg/grpc/proto/ --go-grpc_opt=paths=source_relative backend/backend.proto
    space
    (cd core/http/react-ui && npm run build)
    space
    go build -p 2 -tags auth -o tests/e2e/mock-backend/mock-backend ./tests/e2e/mock-backend
    space
    go build -p 2 -tags auth -o tests/e2e-ui/ui-test-server ./tests/e2e-ui
    exit 0
fi
for file in tests/e2e/mock-backend/mock-backend tests/e2e-ui/ui-test-server core/http/react-ui/node_modules/.bin/playwright; do
    [ -x "$file" ] || fail "missing executable: $file"
done
(cd core/http/react-ui && npm ls --depth=0 --offline >/dev/null) || fail 'installed npm dependencies do not match package/lock requirements'
# --check inspects only; verify performs native compile/runtime and browser probes.
printf '{"case":"toolchain","status":"passed"}\n'

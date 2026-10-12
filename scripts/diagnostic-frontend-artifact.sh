#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# Artifact-only diagnostic build. No image publishing or deployment.
set -euo pipefail

check_identity() {
  [[ ${GITHUB_REF:-} == refs/heads/diag/dispatch-ci ]]
  [[ ${EXPECTED_SHA:-} =~ ^[0-9a-f]{40}$ ]]
  [[ $EXPECTED_SHA == "${GITHUB_SHA:-}" ]]
  [[ $(git rev-parse HEAD) == "$EXPECTED_SHA" ]]
  [[ $(uname -m) == x86_64 ]]
}

resources() {
  df -h .
  free -h
  getconf _NPROCESSORS_ONLN
  local available
  available=$(df -B1 --output=avail . | tail -1 | tr -d ' ')
  # Fail instead of deleting runner tools, caches, or files to make space.
  [[ $available -ge $1 ]] || { echo "Insufficient disk space: $available bytes" >&2; return 1; }
}

check_identity
mkdir -p artifact
case "${1:-}" in
  preflight)
    resources $((20 * 1024 * 1024 * 1024)) 2>&1 | tee artifact/preflight.log
    ;;
  build)
    resources $((12 * 1024 * 1024 * 1024)) 2>&1 | tee artifact/build-resources.log
    {
      go version
      node --version
      npm --version
      printf 'CGO_ENABLED=%s GO_TAGS=%s GOFLAGS=%s GOMAXPROCS=%s\n' "$CGO_ENABLED" "$GO_TAGS" "$GOFLAGS" "$GOMAXPROCS"
      # Keep the normal Makefile path: pinned protoc/plugins, generated Go
      # sources, inference defaults, UI embedding, and version linker flags.
      make build
      make build-mock-backend
    } 2>&1 | tee artifact/build.log
    go version -m local-ai > artifact/go-build-info.txt
    ldd local-ai > artifact/runtime-libraries.txt
    # Preserve the executable bit across artifact upload/download.
    tar -czf artifact/local-ai-linux-amd64.tar.gz local-ai
    ;;
  test)
    resources $((3 * 1024 * 1024 * 1024)) 2>&1 | tee artifact/test-resources.log
    LOCALAI_TEST_HTTP_PORT=$(python3 - <<'PY'
import socket
with socket.socket() as sock:
    sock.bind(('127.0.0.1', 0))
    print(sock.getsockname()[1])
PY
)
    export LOCALAI_TEST_HTTP_PORT
    printf 'LOCALAI_TEST_HTTP_PORT=%s\n' "$LOCALAI_TEST_HTTP_PORT" > artifact/test-port.txt
    # This gate matches the diagnostic plan; the auth-tag build above is
    # separate from these default-tag regression suites.
    go test -p 2 -race -count=1 -timeout=10m \
      ./pkg/diagnostics ./pkg/grpc ./core/config ./core/backend \
      ./core/services/nodes ./core/http/middleware ./core/http/auth \
      ./core/http/endpoints/openai ./core/http 2>&1 | tee artifact/race.log
    ;;
  finalize)
    printf '%s\n' "$GITHUB_SHA" > artifact/source-sha.txt
    printf 'build=%s\nrace_gate=%s\n' "${BUILD_OUTCOME:-unknown}" "${GATE_OUTCOME:-unknown}" > artifact/status.txt
    {
      echo 'Runner-local caches only; no remote cache restored or published.'
      go env GOCACHE GOMODCACHE
      du -sh "$(go env GOCACHE)" "$(go env GOMODCACHE)" || true
      df -h .
    } > artifact/cache-status.txt 2>&1
    git diff --stat > artifact/generated-diff-stat.txt
    git diff -- go.mod go.sum core/http/react-ui/package-lock.json > artifact/dependency-diff.patch
    cat > artifact/README.txt <<'TEXT'
Diagnostic frontend artifact, Linux x86_64, built on Ubuntu 24.04.
Extract local-ai-linux-amd64.tar.gz to retain the executable bit.
Check status.txt before using the binary: upload does not imply test success.
See source-sha.txt, go-build-info.txt, runtime-libraries.txt, and SHA256SUMS.
Built with CGO_ENABLED=1 and GO_TAGS=auth, using normal make build flags.
Not a container image or a complete runtime bundle. No backend, model,
configuration, credentials, or deployment manifests are included.
Runtime requires compatible libc and the libraries listed by ldd; some
features also need ffmpeg, external backends, and separate model assets.
Build dependencies are resolved by the existing Makefile and package managers;
this artifact is not a claim of reproducible builds or deployment readiness.
The race gate does not satisfy worker/C++ tests, inference validation, or
independent review. No live request or inference smoke test runs here.
TEXT
    (cd artifact; find . -type f ! -name SHA256SUMS -print0 | sort -z | xargs -0 sha256sum > SHA256SUMS)
    ;;
  *) echo 'Usage: diagnostic-frontend-artifact.sh preflight|build|test|finalize' >&2; exit 2 ;;
esac

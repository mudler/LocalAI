#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
set -Eeuo pipefail
[[ ${GITHUB_EVENT_NAME:-} == workflow_dispatch ]]
[[ ${GITHUB_REF:-} == refs/heads/diag/worker-dispatch-boundaries ]]
[[ ${EXPECTED_SHA:-} =~ ^[0-9a-f]{40}$ && $EXPECTED_SHA == "$GITHUB_SHA" ]]
[[ $(git rev-parse HEAD) == "$EXPECTED_SHA" && $(uname -m) == aarch64 ]]
: "${RUNNER_TEMP:?}"
OUT="$RUNNER_TEMP/worker-dispatch-artifacts"
WORK="$RUNNER_TEMP/worker-dispatch-work"
mkdir "$OUT" "$WORK"
finish() {
    rc=$?
    trap - EXIT
    printf 'exit_code=%s\n' "$rc" > "$OUT/status.txt"
    # Only this allowlisted, bounded directory is uploaded, never the image or source tree.
    python3 - "$OUT" <<'PY'
from pathlib import Path
import sys
root = Path(sys.argv[1])
for path in root.iterdir():
    if path.is_file() and path.suffix in ('.log', '.txt', '.json') and path.stat().st_size > 8 * 1024 * 1024:
        with path.open('rb') as source:
            source.seek(-8 * 1024 * 1024, 2)
            tail = source.read()
        path.write_bytes(b'[truncated to last 8 MiB]\n' + tail)
PY
    exit "$rc"
}
trap finish EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
printf 'source_sha=%s\nengine_sha=a868c3e3c56657f7e8a6231190dbbe90e7dd86c0\nartifact_only=true\n' "$GITHUB_SHA" > "$OUT/source-identity.txt"
printf 'not_run\n' > "$OUT/test-status.txt"
sha256sum .github/diagnostics/historical-builder-manifest.json > "$OUT/manifest-sha256.txt"
# No disk assumptions or destructive cleanup. Budget compressed OCI + Docker import
# + 7.22 GiB rootfs + 1.16 GiB gRPC copy + source/build/artifact headroom.
docker_root=$(docker info --format '{{.DockerRootDir}}')
# The unprivileged runner owns this evidence file.
# shellcheck disable=SC2024
sudo python3 - "$WORK" "$docker_root" > "$OUT/capacity.json" <<'PY'
import json, os, shutil, sys
paths = sys.argv[1:]
same = os.stat(paths[0]).st_dev == os.stat(paths[1]).st_dev
budgets = [36, 36] if same else [16, 24]
rows = [{'location': label, 'free_bytes': shutil.disk_usage(path).free,
         'required_bytes': budget * 1024**3}
        for label, path, budget in zip(['workspace', 'docker'], paths, budgets)]
print(json.dumps(rows, indent=2))
if any(row['free_bytes'] < row['required_bytes'] for row in rows):
    sys.exit('Insufficient measured capacity for historical builder; use a larger native ARM runner. No cleanup attempted.')
PY
# Logs belong to the runner user, not root.
# shellcheck disable=SC2024
sudo apt-get update > "$OUT/install.log" 2>&1
# shellcheck disable=SC2024
sudo apt-get install -y --no-install-recommends skopeo >> "$OUT/install.log" 2>&1
python3 .github/scripts/dispatch-diagnostics-recovery.py \
    .github/diagnostics/historical-builder-manifest.json "$WORK/oci" > "$OUT/recovery.log" 2>&1
skopeo copy "oci:$WORK/oci:historical" docker-daemon:worker-dispatch-builder:historical > "$OUT/import.log" 2>&1
# Stage only tracked public wrapper/proto inputs; no local scratch or private evidence.
mkdir -p "$WORK/source"
git archive HEAD backend/cpp/llama-cpp backend/backend.proto | tar -x -C "$WORK/source"
(cd "$WORK/source" && find backend -type f -print0 | sort -z | xargs -0 sha256sum) > "$OUT/source-sha256.txt"
ENGINE=a868c3e3c56657f7e8a6231190dbbe90e7dd86c0
E="$WORK/source/backend/cpp/llama-cpp/llama.cpp"
git init "$E" > "$OUT/engine.log" 2>&1
git -C "$E" remote add origin https://github.com/ggml-org/llama.cpp.git
git -C "$E" fetch --depth 1 origin "$ENGINE" >> "$OUT/engine.log" 2>&1
git -C "$E" checkout --detach FETCH_HEAD >> "$OUT/engine.log" 2>&1
[[ $(git -C "$E" rev-parse HEAD) == "$ENGINE" ]]
git -C "$E" submodule update --init --recursive --depth 1 >> "$OUT/engine.log" 2>&1
git -C "$E" submodule status --recursive > "$OUT/engine-submodules.txt"
if grep -qE '^[-+U]' "$OUT/engine-submodules.txt"; then exit 1; fi
cat > "$WORK/build.sh" <<'INNER'
#!/usr/bin/env bash
set -Eeuo pipefail
umask 000
export PATH=/usr/local/cuda/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
export CCACHE_DISABLE=1 BUILD_TYPE=cublas NATIVE=false JOBS=1
cp -a /opt/grpc/. /usr/local/
{ gcc --version; cmake --version; nvcc --version; } > /evidence/toolchain.txt
[[ $(gcc -dumpfullversion) == 13.3.0 ]]
cmake --version | grep -q 'version 3.28.3'
nvcc --version | grep -q 'V13.0.88'
# Observe the actual Makefile's configure command without replacing its flags or
# preparation rules. Its fallback target creates a fresh variant and patches once.
mkdir /tmp/cmake-checkpoint
export REAL_CMAKE
REAL_CMAKE=$(command -v cmake)
cat > /tmp/cmake-checkpoint/cmake <<'WRAPPER'
#!/usr/bin/env bash
set -uo pipefail
"$REAL_CMAKE" "$@"
rc=$?
if [[ ${1:-} == .. ]]; then
    printf 'configure_exit_code=%s\n' "$rc" > /evidence/configure-status.txt
    test ! -f CMakeCache.txt || cp CMakeCache.txt /evidence/CMakeCache.txt
    test ! -f CMakeFiles/CMakeConfigureLog.yaml || tail -c 8388608 CMakeFiles/CMakeConfigureLog.yaml > /evidence/configure.log
    if [[ $rc == 0 ]]; then
        for flag in BUILD_SHARED_LIBS GGML_NATIVE GGML_AVX GGML_AVX2 GGML_AVX512 GGML_FMA GGML_F16C GGML_BMI2; do
            grep -Eiq "^${flag}:[^=]+=(OFF|FALSE|0)$" CMakeCache.txt || rc=1
        done
        grep -Eq '^GGML_CUDA:[^=]+=ON$' CMakeCache.txt || rc=1
        grep -Eq '^LLAMA_GRPC_BUILD_TESTS:[^=]+=ON$' CMakeCache.txt || rc=1
        grep -Eq '^CMAKE_CUDA_ARCHITECTURES:[^=]+=75-virtual;80-virtual;86-real;89-real;90-virtual;120a-real;121a-real$' CMakeCache.txt || rc=1
        printf 'configure_flags_exit_code=%s\n' "$rc" >> /evidence/configure-status.txt
    fi
    sync /evidence
fi
exit "$rc"
WRAPPER
chmod +x /tmp/cmake-checkpoint/cmake
export PATH=/tmp/cmake-checkpoint:$PATH
export CMAKE_ARGS='-DCMAKE_C_COMPILER_LAUNCHER=ccache -DCMAKE_CXX_COMPILER_LAUNCHER=ccache -DCMAKE_CUDA_COMPILER_LAUNCHER=ccache -DCMAKE_CUDA_ARCHITECTURES=75-virtual\;80-virtual\;86-real\;89-real\;90-virtual\;120a-real\;121a-real -DLLAMA_GRPC_BUILD_TESTS=ON'
cd /LocalAI/backend/cpp/llama-cpp
make JOBS=1 BUILD_TYPE=cublas NATIVE=false llama-cpp-fallback
B=/LocalAI/backend/cpp/llama-cpp-fallback-build/llama.cpp/build
# Preserve the worker if the later fixture build/test fails; it is not a release.
if [[ -f "$B/bin/grpc-server" ]]; then
    [[ $(stat -c %s "$B/bin/grpc-server") -le 1073741824 ]]
    cp "$B/bin/grpc-server" /evidence/grpc-server
    (cd /evidence && sha256sum grpc-server > binary-sha256.txt)
fi
cmake --build "$B" --parallel 1 --target dispatch_correlation_fixture dispatch_diagnostics_test
cd "$B/tools/grpc-server"
printf 'running\n' > /evidence/test-status.txt
set +e
ctest --output-on-failure --no-tests=error -R '^(dispatch_correlation_fixture|dispatch_diagnostics_test)$' > /evidence/ctest.log 2>&1
rc=$?
printf 'ctest_exit_code=%s\n' "$rc" > /evidence/test-status.txt
exit "$rc"
INNER
# No credentials, GPU, model, network, or daemon socket enters the build container.
docker run --rm --network none --platform linux/arm64 \
    --mount "type=bind,src=$WORK/source,dst=/LocalAI" \
    --mount "type=bind,src=$OUT,dst=/evidence" \
    --mount "type=bind,src=$WORK/build.sh,dst=/build.sh,readonly" \
    --entrypoint /bin/bash worker-dispatch-builder:historical /build.sh > "$OUT/build.log" 2>&1

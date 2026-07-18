#!/bin/bash
# Regression test for the cuDNN packaging in scripts/build/package-gpu-libs.sh.
#
# cuDNN 9 is a dispatcher (libcudnn.so.9) plus seven sublibraries the dispatcher
# dlopen()s by bare soname at runtime. Only the dispatcher is ever a DT_NEEDED,
# so ldd finds it but never the seven - they have to be completed explicitly.
#
# Three end states are correct, and which one applies is a property of the
# backend, not of the Dockerfile that built it:
#
#   venv has a complete pip cuDNN   -> bundle nothing  (longcat-video)
#   venv has no pip cuDNN           -> bundle all 8    (vllm: Jetson-index torch
#                                                       links cuDNN, no wheel)
#   no venv, nothing links cuDNN    -> bundle nothing  (llama-cpp, whisper,
#                                                       rfdetr-cpp, sam3-cpp,
#                                                       stablediffusion-ggml)
#   no venv, something links cuDNN  -> bundle all 8    (face-detect, voice-detect)
#
# Everything else is a bug this file exists to catch. Historically the allowlist
# force-copied three cuDNN libs into every CUDA backend, which produced the two
# failures behind issue #10905: a partial bundle shadowing a complete pip set
# via LD_LIBRARY_PATH (longcat), and a partial bundle silently completed from
# the runtime image's system cuDNN (vllm and five Go/C++ backends).
#
# Requires gcc (present in the build images); skips otherwise.
set -euo pipefail

CURDIR=$(dirname "$(realpath "$0")")
SCRIPT="$CURDIR/package-gpu-libs.sh"

if ! command -v gcc >/dev/null 2>&1; then
    echo "SKIP: gcc not available"
    exit 0
fi

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

# The full cuDNN 9 family as shipped by the libcudnn9-cuda-13 apt package.
CUDNN_FAMILY=(
    libcudnn
    libcudnn_adv
    libcudnn_cnn
    libcudnn_engines_precompiled
    libcudnn_engines_runtime_compiled
    libcudnn_graph
    libcudnn_heuristic
    libcudnn_ops
)

echo 'int cudnn_stub(void){return 0;}' > "$WORK/stub.c"

# A consumer must actually CALL into cuDNN, not merely name it on the link line:
# the toolchain defaults to --as-needed and drops the DT_NEEDED otherwise, which
# would leave the fixture silently testing nothing.
printf 'int cudnn_stub(void);\nint consume(void){return cudnn_stub();}\n' > "$WORK/consumer.c"

# A system (apt) cuDNN: real .so.9.24.0 files behind .so.9 symlinks. This is
# what the L4T build image carries and it holds no TensorRT, matching reality -
# nothing in the Dockerfiles installs libnvinfer.
SYS="$WORK/sys"
mkdir -p "$SYS"
for name in "${CUDNN_FAMILY[@]}"; do
    gcc -shared -fPIC -o "$SYS/${name}.so.9.24.0" "$WORK/stub.c"
    ln -s "${name}.so.9.24.0" "$SYS/${name}.so.9"
done

# Same, plus a TensorRT stand-in that DT_NEEDEDs cuDNN. Used to prove a bundled
# library pulling cuDNN in also gets the family completed, and that excluding
# cuDNN never over-excludes its dependents.
SYSTRT="$WORK/systrt"
mkdir -p "$SYSTRT"
cp -a "$SYS"/. "$SYSTRT/"
gcc -shared -fPIC -o "$SYSTRT/libnvinfer.so.10" "$WORK/consumer.c" \
    -L"$SYS" -l:libcudnn.so.9 -Wl,-rpath,"$SYS"

# Build a backend dir: <edir>/lib is the bundle target, <edir>/venv is the venv.
# pip ships cuDNN as plain libcudnn*.so.9 files with no versioned real name.
#
# "links-cudnn" reproduces the Go/C++ layout: package.sh stages the backend's
# own shared object into package/lib, which IS the target dir, so the existing
# transitive sweep sees it. That is how face-detect/voice-detect are detected.
#
# $1 = backend name, $2 = pip-cudnn | venv-no-cudnn | no-venv | links-cudnn
make_backend() {
    local edir="$WORK/$1"
    mkdir -p "$edir/lib"
    case "$2" in
        pip-cudnn)
            local sp="$edir/venv/lib/python3.12/site-packages/nvidia/cudnn/lib"
            mkdir -p "$sp"
            local name
            for name in "${CUDNN_FAMILY[@]}"; do
                gcc -shared -fPIC -o "$sp/${name}.so.9" "$WORK/stub.c"
            done
            ;;
        venv-no-cudnn)
            mkdir -p "$edir/venv/lib/python3.12/site-packages/nvidia"
            ;;
        links-cudnn)
            gcc -shared -fPIC -o "$edir/lib/libfacedetect.so" "$WORK/consumer.c" \
                -L"$SYS" -l:libcudnn.so.9 -Wl,-rpath,"$SYS"
            ;;
        no-venv) ;;
    esac
    echo "$edir"
}

# Run the packager for one backend in a fresh bash. A ( ) subshell would inherit
# the COPIED_FILES dedup map from a previous run and skip everything.
# $1 = backend lib dir, $2 = system lib dir
run_packager() {
    env BUILD_TYPE=l4t CUDA_LIB_DIRS="$2" \
        bash -c 'source "$1" "$2"; package_cuda_libs' _ "$SCRIPT" "$1" 2>&1
}

bundled_cudnn() {
    find "$1" -maxdepth 1 -name 'libcudnn*' -printf '%f\n' 2>/dev/null | sort | tr '\n' ' '
}

# Report how many of the 8 sonames are present, for the "expect all" cases.
missing_cudnn() {
    local dir="$1" name out=()
    for name in "${CUDNN_FAMILY[@]}"; do
        [ -e "$dir/${name}.so.9" ] || out+=("${name}.so.9")
    done
    echo "${out[*]:-}"
}

rc=0
pass() { echo "PASS: $1"; }
fail() { echo "FAIL: $1"; rc=1; }

# --- 1. venv provides a complete pip cuDNN (longcat-video).
EDIR=$(make_backend pipbackend pip-cudnn)
run_packager "$EDIR/lib" "$SYSTRT" >/dev/null 2>&1 || true
leaked=$(bundled_cudnn "$EDIR/lib")
if [ -z "$leaked" ]; then
    pass "venv with complete pip cuDNN -> nothing bundled"
else
    fail "venv already has cuDNN but we bundled: $leaked"
fi
if [ -e "$EDIR/lib/libnvinfer.so.10" ]; then
    pass "excluding cuDNN does not over-exclude its dependents"
else
    fail "excluding cuDNN dropped libnvinfer.so.10 too"
fi

# --- 2. venv exists but ships NO pip cuDNN (vllm). The consumers live inside
# the venv where the sweep cannot see them, so this stays conservative.
EDIR=$(make_backend vllmbackend venv-no-cudnn)
run_packager "$EDIR/lib" "$SYS" >/dev/null 2>&1 || true
missing=$(missing_cudnn "$EDIR/lib")
if [ -z "$missing" ]; then
    pass "venv without pip cuDNN -> complete family bundled"
else
    fail "venv without pip cuDNN left the backend short of cuDNN: $missing"
fi

# --- 3. no venv and nothing links cuDNN (llama-cpp, whisper, rfdetr-cpp,
# sam3-cpp, stablediffusion-ggml). ggml uses cuBLAS, not cuDNN. These carry
# ~57 MB of partial cuDNN today; completing the family for them would take that
# to ~576 MB, all of it for libraries with no consumer.
EDIR=$(make_backend gonocudnn no-venv)
run_packager "$EDIR/lib" "$SYS" >/dev/null 2>&1 || true
leaked=$(bundled_cudnn "$EDIR/lib")
if [ -z "$leaked" ]; then
    pass "no venv and nothing links cuDNN -> nothing bundled"
else
    fail "bundled cuDNN into a backend with no cuDNN consumer: $leaked"
fi

# --- 4. no venv but the backend's own .so links cuDNN (face-detect,
# voice-detect, built with -DFACEDETECT_GGML_CUDNN=ON on arm64 + CUDA 13).
EDIR=$(make_backend golinkscudnn links-cudnn)
run_packager "$EDIR/lib" "$SYS" >/dev/null 2>&1 || true
missing=$(missing_cudnn "$EDIR/lib")
if [ -z "$missing" ]; then
    pass "no venv but the backend links cuDNN -> complete family bundled"
else
    fail "backend links cuDNN but the family was left incomplete: $missing"
fi

# --- 5. a bundled library pulling cuDNN in must also get the family completed.
# Nothing installs TensorRT today, but if it ever is, libnvinfer's DT_NEEDED on
# libcudnn would otherwise reintroduce exactly the partial set from #10905.
EDIR=$(make_backend gotrt no-venv)
run_packager "$EDIR/lib" "$SYSTRT" >/dev/null 2>&1 || true
missing=$(missing_cudnn "$EDIR/lib")
if [ -z "$missing" ]; then
    pass "a dependent dragging cuDNN in -> complete family bundled"
else
    fail "libnvinfer pulled cuDNN in but the family was left incomplete: $missing"
fi

# --- 6. no cuDNN in the build image at all (every non-arm64 CUDA image).
EDIR=$(make_backend nocudnnanywhere venv-no-cudnn)
NOSYS="$WORK/nosys"
mkdir -p "$NOSYS"
if run_packager "$EDIR/lib" "$NOSYS" >/dev/null 2>&1; then
    pass "no cuDNN in the build image and none needed -> build still succeeds"
else
    fail "build failed for a backend that has no cuDNN available anywhere"
fi

# --- Guard unit checks. Source once for direct access to the helpers.
mkdir -p "$WORK/guard"
# shellcheck source=/dev/null
source "$SCRIPT" "$WORK/guard"

for fn in verify_cudnn_bundle cudnn_family_state cudnn_venv_lib_dir cudnn_is_referenced; do
    if ! declare -F "$fn" >/dev/null; then
        echo "FAIL: package-gpu-libs.sh does not define $fn"
        exit 1
    fi
done

# The shape five Go/C++ backends plus vllm ship today: three of eight bundled,
# no venv cuDNN, a complete cuDNN in the build image. It survives only because
# the runtime image's system cuDNN completes the family - libcudnn_cnn.so.9 has
# a hard DT_NEEDED on libcudnn_graph.so.9, which none of them bundle, so it
# resolves to /lib/aarch64-linux-gnu and the process runs bundled 9.22.0 against
# system 9.23.2. The build image is not the runtime image; this must not pass.
FLEET="$WORK/fleetshape"
mkdir -p "$FLEET"
for name in libcudnn libcudnn_cnn libcudnn_ops; do
    cp "$SYS/${name}.so.9.24.0" "$FLEET/${name}.so.9.22.0"
    ln -s "${name}.so.9.22.0" "$FLEET/${name}.so.9"
done
if verify_cudnn_bundle "$FLEET" absent complete 2>/dev/null; then
    fail "verify_cudnn_bundle accepted the venv=0 bundled=3 fleet shape"
else
    pass "verify_cudnn_bundle rejected the venv=0 bundled=3 fleet shape"
fi

# Mixed-version bundle: two cuDNN builds in one directory.
MIXED="$WORK/mixed"
mkdir -p "$MIXED"
for name in "${CUDNN_FAMILY[@]}"; do
    cp "$SYS/${name}.so.9.24.0" "$MIXED/"
    ln -sf "${name}.so.9.24.0" "$MIXED/${name}.so.9"
done
cp "$SYS/libcudnn_adv.so.9.24.0" "$MIXED/libcudnn_adv.so.9.20.0"
ln -sf libcudnn_adv.so.9.20.0 "$MIXED/libcudnn_adv.so.9"
if verify_cudnn_bundle "$MIXED" absent absent 2>/dev/null; then
    fail "verify_cudnn_bundle accepted a mixed 9.24.0 / 9.20.0 bundle"
else
    pass "verify_cudnn_bundle rejected a mixed-version bundle"
fi

# cuDNN in BOTH the bundle and the venv: the bundle shadows the venv, so even
# two individually complete sets are a misconfiguration.
COMPLETE="$WORK/complete"
mkdir -p "$COMPLETE"
for name in "${CUDNN_FAMILY[@]}"; do
    cp "$SYS/${name}.so.9.24.0" "$COMPLETE/"
    ln -sf "${name}.so.9.24.0" "$COMPLETE/${name}.so.9"
done
if verify_cudnn_bundle "$COMPLETE" complete absent 2>/dev/null; then
    fail "verify_cudnn_bundle accepted cuDNN in both the bundle and the venv"
else
    pass "verify_cudnn_bundle rejected cuDNN in both the bundle and the venv"
fi

# Zero cuDNN is CORRECT when nothing references it - that is llama-cpp, whisper
# and friends, and it is the common case. The guard must not demand a cuDNN for
# backends that never call one just because the build image happens to have it.
EMPTY="$WORK/empty"
mkdir -p "$EMPTY"
if verify_cudnn_bundle "$EMPTY" absent complete; then
    pass "zero cuDNN accepted when nothing references it"
else
    fail "verify_cudnn_bundle demanded a cuDNN no consumer asked for"
fi

# Zero cuDNN is WRONG when something does reference it. A backend whose own .so
# links the dispatcher and that ends up with no cuDNN cannot load at all.
NEEDS="$WORK/needscudnn"
mkdir -p "$NEEDS"
gcc -shared -fPIC -o "$NEEDS/libfacedetect.so" "$WORK/consumer.c" \
    -L"$SYS" -l:libcudnn.so.9 -Wl,-rpath,"$SYS"
if verify_cudnn_bundle "$NEEDS" absent complete 2>/dev/null; then
    fail "verify_cudnn_bundle accepted zero cuDNN for a backend that links it"
else
    pass "verify_cudnn_bundle rejected zero cuDNN for a backend that links it"
fi

# cudnn_is_referenced must see a dlopen()ed soname too, not just DT_NEEDED.
# A consumer that only ever dlopen()s cuDNN has the string in .rodata and no
# dynamic entry at all, so an ldd-based check would miss it entirely.
DLOPEN="$WORK/dlopenonly"
mkdir -p "$DLOPEN"
printf 'const char *n="libcudnn.so.9";\nint f(void){return 0;}\n' > "$WORK/dl.c"
gcc -shared -fPIC -o "$DLOPEN/libdlopener.so" "$WORK/dl.c"
if cudnn_is_referenced "$DLOPEN"; then
    pass "cudnn_is_referenced detects a dlopen-only consumer"
else
    fail "cudnn_is_referenced missed a dlopen-only consumer (ldd cannot see it)"
fi

# The venv-only end state stays valid.
if verify_cudnn_bundle "$EMPTY" complete complete; then
    pass "verify_cudnn_bundle accepts the venv-only end state"
else
    fail "verify_cudnn_bundle rejected the correct venv-only end state"
fi

exit $rc

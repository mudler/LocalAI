#!/bin/bash
#
# Bundle the nemo-speech-cpp-grpc binary, the three libnemo_speech_*_c shared
# objects, the core runtime libs (libc/libstdc++/libgomp + ld.so) and the GPU
# runtime for the active BUILD_TYPE so the package is self-contained. Mirrors
# backend/go/whisper/package.sh; run.sh routes the (CGO_ENABLED=0) binary
# through lib/ld.so so the packaged libc is used instead of the host's.

set -e

CURDIR=$(dirname "$(realpath "$0")")
REPO_ROOT="${CURDIR}/../../.."

mkdir -p "$CURDIR/package/lib"

cp -avf "$CURDIR/nemo-speech-cpp-grpc" "$CURDIR/package/"
cp -avf "$CURDIR/run.sh" "$CURDIR/package/"

# The runtime ships three C ABI shared objects, not one. All three are
# required: main.go dlopens them eagerly, so a package missing any of them
# fails at startup. ASR and NMT expose the ABI through a dedicated _c library;
# TTS compiles its c_api into libnemo_speech_tts itself and has no _c variant,
# hence the asymmetric list. purego.Dlopen resolves them via the
# NEMO_SPEECH_*_LIBRARY paths that run.sh points at lib/.
#
# libnemo_speech_asr and libnemo_speech_nmt are in the list because the matching
# _c shims carry a DT_NEEDED on them: dlopen of the shim fails without the
# implementation DSO alongside it.
for lib in libnemo_speech_asr_c libnemo_speech_asr libnemo_speech_tts libnemo_speech_nmt_c libnemo_speech_nmt; do
	cp -avf "$CURDIR"/${lib}.so* "$CURDIR/package/lib/" 2>/dev/null || true
	cp -avf "$CURDIR"/${lib}*.dylib "$CURDIR/package/lib/" 2>/dev/null || true
	if ! ls "$CURDIR"/package/lib/${lib}.* >/dev/null 2>&1; then
		echo "ERROR: ${lib} shared library not found in $CURDIR, run 'make' first" >&2
		exit 1
	fi
done

# Detect architecture and copy the core runtime libs the shared objects link
# against, plus the matching dynamic loader as lib/ld.so.
source "$CURDIR/../../../scripts/build/package-system-libs.sh" "$CURDIR/package/lib" ""

# Package GPU libraries (CUDA/ROCm/Intel/Vulkan loader + ICDs + drivers)
# based on BUILD_TYPE so the backend can reach the GPU without the runtime
# base image shipping those drivers.
GPU_LIB_SCRIPT="${REPO_ROOT}/scripts/build/package-gpu-libs.sh"
if [ -f "$GPU_LIB_SCRIPT" ]; then
    echo "Packaging GPU libraries for BUILD_TYPE=${BUILD_TYPE:-cpu}..."
    source "$GPU_LIB_SCRIPT" "$CURDIR/package/lib"
    package_gpu_libs
fi

echo "Packaging completed successfully"
ls -liah "$CURDIR/package/" "$CURDIR/package/lib/"

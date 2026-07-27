#!/bin/bash
# Darwin/Metal build for the audio-cpp backend.
#
# Modelled on scripts/build/ds4-darwin.sh rather than llama-cpp-darwin.sh:
# audio-cpp is a single grpc-server, like ds4 and privacy-filter, whereas
# llama-cpp ships three binaries and splits its ggml objects between the package
# root and lib/. One deliberate departure from BOTH of those scripts, and from
# privacy-filter-darwin.sh in particular:
#
#   THIS SCRIPT DOES NOT REASSEMBLE THE PACKAGE. It runs the backend's own
#   `make package` and copies the result.
#
# privacy-filter-darwin.sh hand-assembles build/darwin and never calls its
# package.sh. Copied here that would silently drop assets/, and assets/ is what
# makes the "bundled:silero_vad" / "bundled:marblenet_vad" model paths resolve
# with nothing downloaded - the only zero-download path this backend has.
# backend/cpp/audio-cpp/package.sh already produces the exact layout the image
# needs, and its Darwin branch stops right before the Linux-only loader/ldd work
# and hands over to this script. Reusing it means the Linux and Darwin packages
# cannot drift.
#
# What this script adds on top of package.sh is the part that is genuinely
# macOS-only: walking otool -L to bundle the non-system dylibs the binary needs.
set -euo pipefail
set -x

IMAGE_NAME="${IMAGE_NAME:-localai/audio-cpp-darwin}"
PLATFORMARCH="${PLATFORMARCH:-darwin/arm64}"

# NATIVE=false matches ds4-darwin.sh and the backend Makefile's own default: the
# image is published for every Apple Silicon Mac, not just the CI runner's, so
# the CPU kernels must not be compiled for the build host's exact ISA.
pushd backend/cpp/audio-cpp
make NATIVE=false grpc-server package
popd

rm -rf build/darwin
mkdir -p build/darwin backend-images
# -a, not -r: package.sh may have left symlink chains among the ggml objects
# (libggml.dylib -> libggml.0.dylib -> ...), and the SONAME the binary asks for
# has to keep naming a file here.
cp -a backend/cpp/audio-cpp/package/. build/darwin/

# THE LAYOUT ASSERTION - the Darwin half of the one in
# backend/cpp/audio-cpp/package.sh, and the reason that one is written at such
# length. run.sh's Darwin branch execs grpc-server directly, so
# _NSGetExecutablePath names the package ROOT. Two independent consumers read
# that directory: ggml's backend registry, which discovers dlopened
# libggml-cpu-* objects by scanning it, and resolve_model_path(), which expands
# "bundled:<name>" to <exe dir>/assets/<name>. Tidying assets/ or the ggml
# objects into lib/, which is where the llama-cpp layout would put them,
# produces a package that builds, publishes, and then fails at run time with
# "model path does not exist: <pkg>/lib/assets/...". Fail here instead.
for required in grpc-server run.sh assets/silero_vad assets/marblenet_vad; do
    if [ ! -e "build/darwin/$required" ]; then
        echo "audio-cpp-darwin.sh: missing from the package root: $required" >&2
        echo "audio-cpp-darwin.sh: grpc-server, run.sh, the ggml dylibs and" >&2
        echo "audio-cpp-darwin.sh: assets/ all have to sit in ONE directory," >&2
        echo "audio-cpp-darwin.sh: because run.sh execs the binary from there" >&2
        echo "audio-cpp-darwin.sh: and both the ggml backend scan and the" >&2
        echo "audio-cpp-darwin.sh: bundled: asset lookup are relative to it." >&2
        exit 1
    fi
done

# Apple Silicon: pick up Homebrew-installed protobuf utf8_validity if present
# (same as ds4-darwin.sh - it is a transitive dep otool may not surface).
if [[ "$(uname -s)" == "Darwin" && "$(uname -m)" == "arm64" ]]; then
    ADDITIONAL_LIBS=${ADDITIONAL_LIBS:-$(ls /opt/homebrew/Cellar/protobuf/**/lib/libutf8_validity*.dylib 2>/dev/null || true)}
else
    ADDITIONAL_LIBS=${ADDITIONAL_LIBS:-""}
fi
for file in $ADDITIONAL_LIBS; do
    cp -fLv "$file" build/darwin/lib
done

# Bundle the FULL dylib closure, not just grpc-server's direct dependencies.
#
# ds4-darwin.sh and llama-cpp-darwin.sh walk one level. That is enough only
# while every indirect dependency happens to be linked directly by the binary
# too. It is not enough here: Homebrew's grpc++ pulls libgrpc, ~40 libabsl_*,
# libupb, libcares, libre2 and OpenSSL, and grpc-server links few of those
# itself. A missing one resolves on the build host (the install names are
# absolute Homebrew paths) and fails on a user's Mac that has no Homebrew grpc,
# which is exactly the failure a bundled package exists to prevent. The Linux
# side already does a full ldd closure in package.sh; this is its equivalent.
#
# Leaf-name deduplication is what makes DYLD_LIBRARY_PATH work: run.sh exports
# lib/ and the package root, and dyld searches those by leaf name BEFORE
# falling back to the absolute install name recorded in the binary.
#
# /bin/bash on macOS is 3.2, so no associative arrays: the seen-set is a
# space-delimited string matched with `case`.
BUNDLED_LEAVES=" "
UNRESOLVED=0

bundle_dylib_closure() {
    local object="$1"
    local dep leaf src
    while read -r dep; do
        case "$dep" in
            *.dylib) ;;
            *) continue ;;
        esac
        leaf=$(basename "$dep")
        case "$BUNDLED_LEAVES" in
            *" $leaf "*) continue ;;
        esac

        src="$dep"
        case "$dep" in
            @*)
                # @rpath / @loader_path / @executable_path are resolved against
                # the package itself, so such a dependency is satisfiable only
                # if the file is already inside it. Nothing produces one today
                # (a Metal build sets neither BUILD_SHARED_LIBS nor
                # GGML_BACKEND_DL, so ggml is static and package.sh copies no
                # dylibs at all), but if that changes, a missing one has to be
                # loud: dyld fails at load with nothing else to go on.
                if [ -e "build/darwin/$leaf" ]; then
                    src="build/darwin/$leaf"
                elif [ -e "build/darwin/lib/$leaf" ]; then
                    src="build/darwin/lib/$leaf"
                else
                    echo "audio-cpp-darwin.sh: unresolved @rpath dependency of ${object}: ${dep}" >&2
                    UNRESOLVED=1
                    continue
                fi
                BUNDLED_LEAVES="${BUNDLED_LEAVES}${leaf} "
                bundle_dylib_closure "$src"
                continue
                ;;
        esac

        # System dylibs live in the dyld shared cache and have no file on disk.
        # That absence is the filter that keeps libSystem, libc++ and the
        # frameworks out of the package; there is no allow-list to maintain.
        [ -e "$dep" ] || continue

        BUNDLED_LEAVES="${BUNDLED_LEAVES}${leaf} "
        # -L dereferences: Homebrew's unversioned names are symlinks into the
        # Cellar, and copying the link would land a dangling relative symlink.
        # The binary asks for whatever install name is recorded, so the file has
        # to exist under that leaf.
        cp -fLv "$dep" build/darwin/lib/
        bundle_dylib_closure "$dep"
    done < <(otool -L "$object" | awk 'NR > 1 { print $1 }')
    # Explicit, because `set -e` would abort on whatever status the loop
    # happened to end on.
    return 0
}

bundle_dylib_closure build/darwin/grpc-server
if compgen -G "build/darwin/*.dylib" > /dev/null; then
    for object in build/darwin/*.dylib; do
        bundle_dylib_closure "$object"
    done
fi

echo "Bundled libraries:"
ls -la build/darwin/lib

if [ "$UNRESOLVED" -ne 0 ]; then
    echo "audio-cpp-darwin.sh: refusing to package: see the unresolved dependencies above." >&2
    exit 1
fi

# Nothing above rewrites an install name or otherwise edits grpc-server, which
# matters on Apple Silicon: the linker ad-hoc signs the binary, and any
# in-place edit would invalidate that signature and make the package
# unlaunchable. cp preserves it.
./local-ai util create-oci-image \
        build/darwin/. \
        --output ./backend-images/audio-cpp.tar \
        --image-name "$IMAGE_NAME" \
        --platform "$PLATFORMARCH"

rm -rf build/darwin

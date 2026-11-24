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
# lib/ arrives from package.sh, which mkdir -p's an empty one. Do not rely on
# that: everything below copies into it, and if package.sh ever stops
# pre-creating it the first copy fails with an opaque BSD-cp message instead of
# any of the assertions this script otherwise leans on.
mkdir -p build/darwin/lib

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

# The LC_RPATH entries recorded in an object, one per line.
object_rpaths() {
    otool -l "$1" | awk '/LC_RPATH/ { f = 1 } f && $1 == "path" { print $2; f = 0 }'
}

# THE RAW otool OUTPUT, LOGGED BEFORE THE WALK AND WITHOUT ANY CONDITION.
#
# Both awk filters in this script (this one's LC_RPATH state machine and the
# `NR > 1 { print $1 }` in bundle_dylib_closure) assume a column layout nobody
# working on this can observe, since the script runs only on the CI Mac. A green
# first Darwin run proves nothing about that assumption: an awk that silently
# matched nothing yields an empty dependency list and an empty rpath list, which
# reads exactly like "no non-system dependencies" and packages happily, shipping
# an image that fails at load time on a user's machine. Both filters otherwise
# feed process substitutions, so their input never reaches the log at all.
# Printing the unparsed text once lets the next person check the real format
# against the parser without a Mac.
log_object_layout() {
    local object="$1"
    echo "audio-cpp-darwin.sh: otool -L ${object}:"
    otool -L "$object" | sed 's/^/audio-cpp-darwin.sh:     /'
    echo "audio-cpp-darwin.sh: LC_RPATH entries parsed out of ${object}:"
    object_rpaths "$object" | sed 's/^/audio-cpp-darwin.sh:     /'
}

# Resolve an @rpath / @loader_path / @executable_path dependency to a real file,
# printing the path and returning 0, or returning 1 if nothing matches.
#
# This is not hypothetical any more. The Metal build links ggml statically, so
# no ggml object arrives as an @rpath dep, but the OpenMP hint in the backend
# Makefile makes libomp.dylib a dependency, and whether it is recorded as an
# absolute /opt/homebrew/opt/libomp path or as @rpath/libomp.dylib is a property
# of Homebrew's fix_dynamic_linkage that cannot be observed from Linux. Skipping
# it (what ds4-darwin.sh does with a non-existent path) would ship a package
# missing its OpenMP runtime; failing without expanding rpaths first would turn
# a resolvable dependency into a red build.
resolve_at_path_dep() {
    local object="$1" dep="$2"
    local objdir rpath candidate
    objdir=$(dirname "$object")
    case "$dep" in
        @loader_path/*)
            candidate="$objdir/${dep#@loader_path/}"
            if [ -e "$candidate" ]; then printf '%s\n' "$candidate"; return 0; fi
            ;;
        @executable_path/*)
            # At run time the executable is grpc-server at the package root.
            candidate="build/darwin/${dep#@executable_path/}"
            if [ -e "$candidate" ]; then printf '%s\n' "$candidate"; return 0; fi
            ;;
        @rpath/*)
            while read -r rpath; do
                case "$rpath" in
                    @loader_path)   rpath="$objdir" ;;
                    @loader_path/*) rpath="$objdir/${rpath#@loader_path/}" ;;
                    @executable_path)   rpath="build/darwin" ;;
                    @executable_path/*) rpath="build/darwin/${rpath#@executable_path/}" ;;
                esac
                candidate="$rpath/${dep#@rpath/}"
                if [ -e "$candidate" ]; then printf '%s\n' "$candidate"; return 0; fi
            done < <(object_rpaths "$object")
            ;;
    esac
    return 1
}

# Copy one dylib into lib/ and walk its own dependencies. A no-op if a file of
# the same leaf name is already bundled, which is both the deduplication and the
# cycle guard: mutual dependencies terminate because the second visit finds the
# leaf already recorded.
bundle_dylib() {
    local path="$1"
    local leaf
    leaf=$(basename "$path")
    case "$BUNDLED_LEAVES" in
        *" $leaf "*) return 0 ;;
    esac
    BUNDLED_LEAVES="${BUNDLED_LEAVES}${leaf} "
    # -L dereferences: Homebrew's unversioned names are symlinks into the
    # Cellar, and copying the link would land a dangling relative symlink. The
    # binary asks for whatever install name is recorded, so the file has to
    # exist under that leaf.
    cp -fLv "$path" build/darwin/lib/
    bundle_dylib_closure "$path"
}

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

        case "$dep" in
            @*)
                # Already inside the package (the ggml objects package.sh copies
                # to the root would land here): record and walk in place.
                if [ -e "build/darwin/$leaf" ]; then
                    BUNDLED_LEAVES="${BUNDLED_LEAVES}${leaf} "
                    bundle_dylib_closure "build/darwin/$leaf"
                elif [ -e "build/darwin/lib/$leaf" ]; then
                    BUNDLED_LEAVES="${BUNDLED_LEAVES}${leaf} "
                    bundle_dylib_closure "build/darwin/lib/$leaf"
                elif src=$(resolve_at_path_dep "$object" "$dep"); then
                    bundle_dylib "$src"
                else
                    # Nothing on disk answers this, so dyld will not find it
                    # either. Print the rpath list with it: this fails on a
                    # machine nobody can attach to, and without the rpaths the
                    # log says only that something was missing.
                    echo "audio-cpp-darwin.sh: unresolved dependency of ${object}: ${dep}" >&2
                    echo "audio-cpp-darwin.sh: LC_RPATH entries of ${object}:" >&2
                    object_rpaths "$object" | sed 's/^/audio-cpp-darwin.sh:     /' >&2
                    UNRESOLVED=1
                fi
                continue
                ;;
        esac

        # System dylibs live in the dyld shared cache and have no file on disk.
        # That absence is the filter that keeps libSystem, libc++ and the
        # frameworks out of the package; there is no allow-list to maintain.
        [ -e "$dep" ] || continue

        bundle_dylib "$dep"
    done < <(otool -L "$object" | awk 'NR > 1 { print $1 }')
    # Explicit, because `set -e` would abort on whatever status the loop
    # happened to end on.
    return 0
}

# Apple Silicon: pick up Homebrew-installed protobuf utf8_validity if present
# (same as ds4-darwin.sh - it is a transitive dep otool may not surface).
# Unlike ds4-darwin.sh these go through bundle_dylib rather than a bare cp, so
# they are seeded into the seen-set (no duplicate copy when the walk reaches
# them) and their own non-system dependencies are bundled too.
if [[ "$(uname -s)" == "Darwin" && "$(uname -m)" == "arm64" ]]; then
    ADDITIONAL_LIBS=${ADDITIONAL_LIBS:-$(ls /opt/homebrew/Cellar/protobuf/**/lib/libutf8_validity*.dylib 2>/dev/null || true)}
else
    ADDITIONAL_LIBS=${ADDITIONAL_LIBS:-""}
fi
for file in $ADDITIONAL_LIBS; do
    bundle_dylib "$file"
done

log_object_layout build/darwin/grpc-server

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

# EVERY SYMLINK IN THE PACKAGE IS LISTED, AND ONE THAT CANNOT RESOLVE INSIDE
# THE IMAGE IS A HARD FAILURE.
#
# A dangling symlink does not fail anything above. Every assertion in this
# script tests with -e, which FOLLOWS links and so cannot tell a real file from
# a broken link either way, and bundle_dylib's `cp -fL` only dereferences the
# things IT copies. The `cp -a` of package.sh's output is archive mode on
# purpose (see its comment: a libggml.dylib -> libggml.0.dylib chain has to keep
# naming a file here), so whatever links package.sh left arrive intact. The
# result of getting this wrong is not a red build, it is a dlopen failure on a
# user's Mac naming a path that never existed on their machine.
#
# Links are therefore NOT banned outright, which would break the chain `cp -a`
# exists to preserve. What is banned is a link that resolves on this build host
# and will not resolve in the image: a broken one, or an absolute one pointing
# outside the package, both of which look fine here and ship broken.
SYMLINKS="$(find build/darwin -type l -print)"
if [ -n "$SYMLINKS" ]; then
    echo "audio-cpp-darwin.sh: symlinks in the package:"
    BAD_LINKS=""
    while IFS= read -r link; do
        target=$(readlink "$link")
        echo "audio-cpp-darwin.sh:     $link -> $target"
        if [ ! -e "$link" ]; then
            BAD_LINKS="${BAD_LINKS}${link} -> ${target} (dangling)"$'\n'
            continue
        fi
        case "$target" in
            /*) BAD_LINKS="${BAD_LINKS}${link} -> ${target} (absolute, outside the package)"$'\n' ;;
        esac
    done <<< "$SYMLINKS"

    if [ -n "$BAD_LINKS" ]; then
        echo "audio-cpp-darwin.sh: refusing to package: these links cannot resolve inside the image." >&2
        printf '%s' "$BAD_LINKS" | sed 's/^/audio-cpp-darwin.sh:     /' >&2
        echo "audio-cpp-darwin.sh: a link is only allowed when its target is a" >&2
        echo "audio-cpp-darwin.sh: RELATIVE path that exists in the package too;" >&2
        echo "audio-cpp-darwin.sh: anything else resolves on this build host and" >&2
        echo "audio-cpp-darwin.sh: fails at dlopen on a user's Mac." >&2
        exit 1
    fi
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

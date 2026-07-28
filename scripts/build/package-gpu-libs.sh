#!/bin/bash
# Script to package GPU libraries based on BUILD_TYPE
# This script copies GPU-specific runtime libraries to a target lib directory
# so backends can run in isolation with their own GPU libraries.
#
# Usage: source package-gpu-libs.sh TARGET_LIB_DIR
#        package_gpu_libs
#
# Environment variables:
#   BUILD_TYPE - The GPU build type (cublas, l4t, hipblas, sycl_f16, sycl_f32, intel, vulkan)
#   CUDA_MAJOR_VERSION - CUDA major version (for cublas/l4t builds)
#
# This enables backends to be fully self-contained and run on a unified base image
# without requiring GPU drivers to be pre-installed in the host image.

set -e

TARGET_LIB_DIR="${1:-./lib}"

# Create target directory if it doesn't exist
mkdir -p "$TARGET_LIB_DIR"

# Associative array to track copied files by basename
# Note: We use basename for deduplication because the target is a flat directory.
# If the same library exists in multiple source paths, we only copy it once.
declare -A COPIED_FILES

# Helper function to copy library preserving symlinks structure
# Instead of following symlinks and duplicating files, this function:
# 1. Resolves symlinks to their real target
# 2. Copies the real file only once
# 3. Recreates symlinks pointing to the real file
copy_lib() {
    local src="$1"

    # Check if source exists (follows symlinks)
    if [ ! -e "$src" ]; then
        return
    fi

    local src_basename
    src_basename=$(basename "$src")

    # Skip if we've already processed this filename
    if [[ -n "${COPIED_FILES[$src_basename]:-}" ]]; then
        return
    fi

    # Families we deliberately do not bundle are excluded on every route into
    # the target dir, not just the allowlist. The transitive sweep resolves
    # DT_NEEDED entries against the build image's system libs, so without this
    # it would quietly re-import part of an excluded family (e.g. libnvinfer
    # pulling libcudnn back in) and recreate the partial-set hazard.
    # shellcheck disable=SC2053  # unquoted on purpose: it is a glob pattern
    if [[ -n "${EXCLUDE_LIB_PATTERN:-}" && "$src_basename" == ${EXCLUDE_LIB_PATTERN} ]]; then
        return
    fi

    if [ -L "$src" ]; then
        # Source is a symbolic link
        # Resolve the real file (following all symlinks)
        local real_file
        real_file=$(readlink -f "$src")

        if [ ! -e "$real_file" ]; then
            echo "Warning: symlink target does not exist: $src -> $real_file" >&2
            return
        fi

        local real_basename
        real_basename=$(basename "$real_file")

        # Copy the real file if we haven't already
        if [[ -z "${COPIED_FILES[$real_basename]:-}" ]]; then
            cp -v "$real_file" "$TARGET_LIB_DIR/$real_basename" 2>/dev/null || true
            COPIED_FILES[$real_basename]=1
        fi

        # Create the symlink if the source name differs from the real file name
        if [ "$src_basename" != "$real_basename" ]; then
            # Point directly to the real file for simplicity and reliability
            ln -sfv "$real_basename" "$TARGET_LIB_DIR/$src_basename" 2>/dev/null || true
        fi
        COPIED_FILES[$src_basename]=1
    else
        # Source is a regular file - copy if not already copied
        if [[ -z "${COPIED_FILES[$src_basename]:-}" ]]; then
            cp -v "$src" "$TARGET_LIB_DIR/$src_basename" 2>/dev/null || true
        fi
        COPIED_FILES[$src_basename]=1
    fi
}

# Helper function to copy all matching libraries from a glob pattern
# Files are sorted so that regular files are processed before symlinks
copy_libs_glob() {
    local pattern="$1"
    # Use nullglob option to handle non-matching patterns gracefully
    local old_nullglob=$(shopt -p nullglob)
    shopt -s nullglob
    local matched=($pattern)
    eval "$old_nullglob"

    # Sort files: regular files first, then symlinks
    # This ensures real files are copied before we try to create symlinks pointing to them
    local regular_files=()
    local symlinks=()
    for file in "${matched[@]}"; do
        if [ -L "$file" ]; then
            symlinks+=("$file")
        elif [ -e "$file" ]; then
            regular_files+=("$file")
        fi
    done

    # Process regular files first, then symlinks
    for lib in "${regular_files[@]}" "${symlinks[@]}"; do
        copy_lib "$lib"
    done
}

# Returns success for the core runtime libs the base image and package.sh
# already provide. We must NOT bundle our own copies of these — a second libc
# or libstdc++ on LD_LIBRARY_PATH clashes with the loader and the rest of the
# process — so they're skipped when pulling in a driver's transitive deps.
is_core_lib() {
    case "$1" in
        ld-linux*|ld.so|libc.so.*|libm.so.*|libdl.so.*|libpthread.so.*|librt.so.*|\
        libgcc_s.so.*|libstdc++.so.*|libresolv.so.*|libutil.so.*|linux-vdso.so.*)
            return 0 ;;
    esac
    return 1
}

# Copy the shared-library dependencies of an ELF file into TARGET_LIB_DIR.
# Used to make a bundled GPU driver self-contained: e.g. the Mesa Vulkan ICDs
# pull in libdrm, libexpat and (for RADV/lavapipe) libLLVM, none of which the
# runtime base image is guaranteed to have. Core libc-family deps are skipped.
copy_elf_deps() {
    local elf="$1"
    [ -e "$elf" ] || return 0
    command -v ldd >/dev/null 2>&1 || return 0

    # ldd lines look like: "<TAB>libfoo.so.1 => /path/to/libfoo.so.1 (0x..)".
    # Take the resolved absolute path (field 3) and skip vdso/static entries.
    while read -r dep; do
        if is_core_lib "$(basename "$dep")"; then
            continue
        fi
        copy_lib "$dep"
    done < <(ldd "$elf" 2>/dev/null | awk '/=>/ && $3 ~ /^\// {print $3}')
}

# Sweep the transitive shared-library dependencies of everything already
# bundled in a lib dir. The per-vendor packagers below copy an explicit
# allowlist of top-level runtime libs, but those libs pull in transitive deps
# that aren't in the list (e.g. ROCm's librocprofiler-register.so.0, libnuma,
# libdrm_amdgpu). Because backends run through the bundled lib/ld.so with
# LD_LIBRARY_PATH=lib (see run.sh), an unbundled transitive dep is a hard load
# failure (issue #10537: "librocprofiler-register.so.0: cannot open shared
# object file"). ldd resolves the full recursive closure, so a single pass over
# the already-bundled libs is enough; core libc-family deps are skipped via
# copy_elf_deps/is_core_lib so we never shadow the loader's own libc/libstdc++.
sweep_transitive_deps() {
    local dir="${1:-$TARGET_LIB_DIR}"
    command -v ldd >/dev/null 2>&1 || return 0

    # Snapshot the current set first: copy_elf_deps adds files as it runs, and
    # ldd already returns the full recursive closure, so we only need to sweep
    # the libs that were present before the sweep started.
    # `local x=$(...)` keeps set -e from tripping on shopt -p's nonzero exit.
    local old_nullglob=$(shopt -p nullglob)
    shopt -s nullglob
    local libs=("$dir"/*.so*)
    eval "$old_nullglob"

    local lib
    for lib in "${libs[@]}"; do
        [ -e "$lib" ] || continue
        # Skip symlinks: their real target is in the snapshot and gets swept.
        [ -L "$lib" ] && continue
        copy_elf_deps "$lib"
    done
}

# Whether to bundle cuDNN into the backend's lib/ at all.
#
# "auto" decides per backend from what that backend's venv actually provides,
# which is the only thing that can be right across the fleet:
#
#   - venv ships a complete pip cuDNN (longcat-video, speaker-recognition):
#     do not bundle. lib/ is first on LD_LIBRARY_PATH and LD_LIBRARY_PATH beats
#     DT_RUNPATH, so anything bundled shadows the exact cuDNN torch was built
#     against. cuDNN's own libraries have RUNPATH=$ORIGIN, so the dispatcher
#     finds its siblings in the venv unaided.
#   - venv ships no pip cuDNN (vllm, and any backend on the Jetson index, whose
#     torch links the bundled cuDNN instead): bundle the complete family, or the
#     backend ends up with no cuDNN at all. This stays conservative rather than
#     detecting consumers, because for a Python backend they live inside the
#     venv - torch, ctranslate2, onnxruntime - where the transitive sweep does
#     not look.
#   - no venv (Go/C++ backends): bundle only if something in the package
#     actually references cuDNN. Go backends stage their own shared object into
#     package/lib, which IS the target dir, so the existing sweep already finds
#     the dispatcher when it is a real dependency - that is how libcudnn_graph
#     reached longcat. ggml uses cuBLAS, not cuDNN, so llama-cpp, whisper,
#     rfdetr-cpp, sam3-cpp and stablediffusion-ggml reference none of it: they
#     shed the ~57 MB they carry today rather than growing to ~576 MB for
#     libraries they never call. face-detect and voice-detect, built with
#     -D*_GGML_CUDNN=ON on arm64 + CUDA 13, do reference it and get the
#     complete family - that growth is the cost of correctness, paid only where
#     cuDNN is actually used.
#
# A static per-Dockerfile flag cannot express this: both shapes occur among
# Python backends on the very same image, and a backend switches shape whenever
# it changes package index or gains/loses torch. Detection stays correct on its
# own. "true"/"false" remain as explicit overrides.
#
# This works because both Dockerfiles populate the backend before packaging:
# Dockerfile.python builds the venv (RUN ... make) first, and Go backends invoke
# this script from their own package.sh after staging their binaries.
PACKAGE_CUDNN="${PACKAGE_CUDNN:-auto}"

# The cuDNN 9 sublibraries that must always travel together. The dispatcher
# libcudnn.so.9 is a thin shim that dlopen()s these by bare soname on first use,
# so none of them is a DT_NEEDED of anything and sweep_transitive_deps cannot
# discover them. See verify_cudnn_bundle for why a partial set is fatal.
CUDNN9_SUBLIBS=(
    libcudnn_adv
    libcudnn_cnn
    libcudnn_engines_precompiled
    libcudnn_engines_runtime_compiled
    libcudnn_graph
    libcudnn_heuristic
    libcudnn_ops
)

# Classify the cuDNN 9 install in a directory: "complete", "partial" or
# "absent". Works for both layouts we care about - apt ships versioned real
# files behind .so.9 symlinks, pip ships plain .so.9 files - because it only
# ever looks for the .so.9 sonames the loader actually resolves.
cudnn_family_state() {
    local dir="$1"
    local present=0 missing=0 name

    for name in libcudnn "${CUDNN9_SUBLIBS[@]}"; do
        if [ -e "$dir/${name}.so.9" ]; then
            present=$((present + 1))
        else
            missing=$((missing + 1))
        fi
    done

    if [ "$present" -eq 0 ]; then
        echo absent
    elif [ "$missing" -eq 0 ]; then
        echo complete
    else
        echo partial
    fi
}

# Whether anything in a directory references cuDNN.
#
# Deliberately a string scan of the binaries rather than ldd. ldd reports only
# DT_NEEDED, which would miss a consumer that solely dlopen()s cuDNN - the
# soname then lives in .rodata with no dynamic entry at all. Matching the string
# catches both, and over-matching is the safe direction here: the cost of a
# false positive is a bundled library nobody calls, the cost of a false negative
# is a backend that cannot load.
cudnn_is_referenced() {
    local dir="$1"
    [ -d "$dir" ] || return 1

    # Already-bundled cuDNN counts: it means something pulled it in, and the
    # family has to be completed around it.
    local old_nullglob=$(shopt -p nullglob)
    shopt -s nullglob
    local existing=("$dir"/libcudnn*)
    eval "$old_nullglob"
    [ ${#existing[@]} -gt 0 ] && return 0

    grep -rlq --binary-files=binary -e 'libcudnn' "$dir" 2>/dev/null
}

# Copy any missing member of the cuDNN 9 family into the target dir.
#
# Only the dispatcher is ever a DT_NEEDED, so the sweep can discover it but
# never the seven sublibraries it dlopen()s. Once anything has pulled cuDNN in,
# the rest of the family has to be completed by hand or the backend ships the
# partial set behind issue #10905.
# Args: $1 = target dir, $2.. = source lib dirs
complete_cudnn_family() {
    local dir="$1"; shift
    local search=("$@") name src found

    for name in libcudnn "${CUDNN9_SUBLIBS[@]}"; do
        [ -e "$dir/${name}.so.9" ] && continue
        found=false
        for src in "${search[@]}"; do
            if [ -e "$src/${name}.so.9" ]; then
                copy_lib "$src/${name}.so.9"
                found=true
                break
            fi
        done
        if [ "$found" = false ]; then
            echo "WARNING: cuDNN is in use but ${name}.so.9 was not found in ${search[*]}" >&2
        fi
    done
}

# Whether this backend has a Python venv at all, which is what separates the
# conservative Python path from the detection-driven Go/C++ one.
backend_has_venv() {
    local edir="${1:-$(dirname "$TARGET_LIB_DIR")}"
    [ -d "$edir/venv" ]
}

# Locate the cuDNN a Python backend's venv provides, if any. libbackend.sh fixes
# the venv at <backend>/venv, and TARGET_LIB_DIR is <backend>/lib, so the
# backend dir is one level up. Prints nothing when there is no venv at all,
# which is the normal case for Go/C++ backends.
cudnn_venv_lib_dir() {
    local edir="${1:-$(dirname "$TARGET_LIB_DIR")}"

    # `local x=$(...)` on purpose: masks shopt -p's nonzero exit under set -e.
    local old_nullglob=$(shopt -p nullglob)
    shopt -s nullglob
    local candidates=("$edir"/venv/lib/python*/site-packages/nvidia/cudnn/lib)
    eval "$old_nullglob"

    local candidate
    for candidate in "${candidates[@]}"; do
        if [ -d "$candidate" ]; then
            echo "$candidate"
            return 0
        fi
    done
}

# Fail the build unless exactly one complete cuDNN ends up visible to the
# backend. Both failure modes below are silent at build time and only surface
# when a model first reaches a cuDNN call, so they have to be caught here.
#
# Backends run with LD_LIBRARY_PATH=<backend>/lib (libbackend.sh / run.sh), and
# LD_LIBRARY_PATH is searched before a library's own DT_RUNPATH. So anything in
# lib/ shadows the venv's cuDNN:
#
#   - a PARTIAL bundle shadows part of the venv's set while the rest still
#     resolves from the venv, leaving the process on two cuDNN builds at once
#     (issue #10905, longcat-video: 4 of 8 at 9.24.0 vs the venv's 9.20.0.48);
#   - bundling NOTHING when the venv has nothing either leaves the backend with
#     no cuDNN at all (vllm, whose Jetson-index torch ships no pip cuDNN).
#
# That second case is indistinguishable from a correct skip by looking at lib/
# alone, which is why the venv state and what the build image had to offer are
# both inputs here.
#
# Args: $1 = bundle dir, $2 = venv cuDNN state, $3 = system cuDNN state.
verify_cudnn_bundle() {
    local dir="${1:-$TARGET_LIB_DIR}"
    local venv_state="${2:-}"
    local system_state="${3:-}"

    [ -n "$venv_state" ] || venv_state=$(cudnn_family_state "$(cudnn_venv_lib_dir)")
    [ -n "$system_state" ] || system_state=absent

    local bundle_state
    bundle_state=$(cudnn_family_state "$dir")

    # `local x=$(...)` on purpose: masks shopt -p's nonzero exit under set -e.
    local old_nullglob=$(shopt -p nullglob)
    shopt -s nullglob
    local cudnn_files=("$dir"/libcudnn*.so.*)
    eval "$old_nullglob"

    # Distinct versions among the real (non-symlink) files. Bare-major sonames
    # like libcudnn.so.9 carry no minor/patch, so they say nothing about which
    # build a file came from and are skipped here.
    local versions=() f ver
    for f in "${cudnn_files[@]}"; do
        [ -L "$f" ] && continue
        ver="${f##*.so.}"
        case "$ver" in
            *.*) versions+=("$ver") ;;
        esac
    done

    if [ ${#versions[@]} -gt 1 ]; then
        local distinct
        distinct=$(printf '%s\n' "${versions[@]}" | sort -u)
        if [ "$(printf '%s\n' "$distinct" | grep -c .)" -gt 1 ]; then
            echo "ERROR: bundled cuDNN mixes multiple builds in $dir:" >&2
            # shellcheck disable=SC2086  # split on purpose: one version per line
            printf '       %s\n' $distinct >&2
            echo "       a mixed set fails at runtime with CUDNN_STATUS_SUBLIBRARY_VERSION_MISMATCH" >&2
            return 1
        fi
    fi

    if [ "$bundle_state" = partial ]; then
        local missing=() sublib
        for sublib in libcudnn "${CUDNN9_SUBLIBS[@]}"; do
            [ -e "$dir/${sublib}.so.9" ] || missing+=("${sublib}.so.9")
        done
        echo "ERROR: incomplete cuDNN 9 bundle in $dir, missing: ${missing[*]}" >&2
        echo "       cuDNN's sublibraries are dlopen()ed, so a partial set is only" >&2
        echo "       detectable here - at runtime it fails with CUDNN_STATUS_SUBLIBRARY_VERSION_MISMATCH" >&2
        return 1
    fi

    if [ "$venv_state" = partial ]; then
        echo "ERROR: the backend venv carries an incomplete cuDNN 9" >&2
        echo "       (site-packages/nvidia/cudnn/lib); a pip cuDNN is complete or absent" >&2
        return 1
    fi

    if [ "$bundle_state" = complete ] && [ "$venv_state" = complete ]; then
        echo "ERROR: cuDNN is present both in $dir and in the backend venv" >&2
        echo "       lib/ precedes DT_RUNPATH on LD_LIBRARY_PATH, so the bundle would" >&2
        echo "       shadow the cuDNN this backend's torch was built against" >&2
        return 1
    fi

    # Zero cuDNN is the correct and common end state - llama-cpp, whisper and
    # every other ggml backend go through cuBLAS and never call cuDNN. It is only
    # an error when something in the package does reference cuDNN, because then
    # the backend cannot load. Note this asks what the package needs, not what
    # the build image happens to have: the two are different machines, and
    # letting the runtime image's system cuDNN complete a bundle is precisely
    # the silent breakage in #10905.
    if [ "$bundle_state" = absent ] && [ "$venv_state" = absent ] && cudnn_is_referenced "$dir"; then
        echo "ERROR: something in $dir references cuDNN but no cuDNN is available to it" >&2
        echo "       nothing bundled and no pip cuDNN in the venv (build image: ${system_state})." >&2
        echo "       It would resolve against the runtime image's system cuDNN, if any," >&2
        echo "       mixing versions - or fail to load outright." >&2
        return 1
    fi

    return 0
}

# Package NVIDIA CUDA libraries
package_cuda_libs() {
    echo "Packaging CUDA libraries for BUILD_TYPE=${BUILD_TYPE}..."

    # CUDA_LIB_DIRS (space-separated) overrides the search roots, which keeps
    # the packaging logic testable without a real CUDA install.
    local cuda_lib_paths
    if [ -n "${CUDA_LIB_DIRS:-}" ]; then
        # shellcheck disable=SC2206  # intentional word-split of the override
        cuda_lib_paths=(${CUDA_LIB_DIRS})
    else
        cuda_lib_paths=(
            "/usr/local/cuda/lib64"
            "/usr/local/cuda-${CUDA_MAJOR_VERSION:-}/lib64"
            "/usr/lib/x86_64-linux-gnu"
            "/usr/lib/aarch64-linux-gnu"
        )
    fi

    # Core CUDA runtime libraries.
    #
    # Patterns are deliberately per *family* (libfoo*.so*) rather than per
    # soname. Several CUDA components split into sublibraries that the main
    # library dlopen()s at runtime - cuDNN 9 into eight, TensorRT into
    # libnvinfer_plugin/libnvinfer_builder_resource, nvRTC into
    # libnvrtc-builtins. dlopen leaves no DT_NEEDED entry, so
    # sweep_transitive_deps cannot find them and every one of them has to be
    # matched here. Copying part of a family is worse than copying none of it:
    # lib/ is first on LD_LIBRARY_PATH, so the copied part shadows a complete
    # set from the backend's venv while the rest still loads from the venv,
    # leaving the process on two different builds at once (issue #10905).
    local cuda_libs=(
        "libcudart.so*"
        "libcublas*.so*"
        "libcufft*.so*"
        "libcurand*.so*"
        "libcusparse*.so*"
        "libcusolver*.so*"
        "libnvrtc*.so*"
        "libnvJitLink.so*"
        "libnvinfer*.so*"
        "libnvonnxparser*.so*"
    )

    # Decide per backend whether to bundle cuDNN (see PACKAGE_CUDNN).
    local cudnn_venv_dir cudnn_venv_state cudnn_system_state=absent bundle_cudnn
    cudnn_venv_dir=$(cudnn_venv_lib_dir)
    cudnn_venv_state=$(cudnn_family_state "${cudnn_venv_dir:-/nonexistent}")

    local lib_path
    for lib_path in "${cuda_lib_paths[@]}"; do
        if [ "$(cudnn_family_state "$lib_path")" != absent ]; then
            cudnn_system_state=$(cudnn_family_state "$lib_path")
            break
        fi
    done

    # "detect" defers to the transitive sweep: cuDNN is copied only if something
    # in the package actually references it, and the family is completed after.
    case "${PACKAGE_CUDNN}" in
        true)  bundle_cudnn=true ;;
        false) bundle_cudnn=false ;;
        *)
            if [ "$cudnn_venv_state" = complete ]; then
                bundle_cudnn=false
            elif backend_has_venv; then
                bundle_cudnn=true
            else
                bundle_cudnn=detect
            fi
            ;;
    esac

    echo "cuDNN: venv=${cudnn_venv_state} system=${cudnn_system_state} PACKAGE_CUDNN=${PACKAGE_CUDNN} -> bundle=${bundle_cudnn}"

    # When cuDNN is skipped outright the exclusion has to cover the transitive
    # sweep too, or a dependent's DT_NEEDED on libcudnn drags a partial family
    # back in. Under "detect" that sweep is exactly what we want to run, so no
    # exclusion is set and the family is completed once it has.
    if [ "$bundle_cudnn" = "true" ]; then
        cuda_libs+=("libcudnn*.so*")
    elif [ "$bundle_cudnn" = "false" ]; then
        echo "Skipping cuDNN: the backend venv already provides a complete set at ${cudnn_venv_dir}"
        export EXCLUDE_LIB_PATTERN="libcudnn*"
    fi

    for lib_path in "${cuda_lib_paths[@]}"; do
        if [ -d "$lib_path" ]; then
            for lib_pattern in "${cuda_libs[@]}"; do
                copy_libs_glob "${lib_path}/${lib_pattern}"
            done
        fi
    done

    # Copy CUDA target directory for runtime compilation support
    # if [ -d "/usr/local/cuda/targets" ]; then
    #     mkdir -p "$TARGET_LIB_DIR/../cuda"
    #     cp -arfL /usr/local/cuda/targets "$TARGET_LIB_DIR/../cuda/" 2>/dev/null || true
    # fi

    # Pull in transitive deps the allowlist misses so the backend is
    # self-contained (same class of failure as #10537).
    sweep_transitive_deps "$TARGET_LIB_DIR"

    # The sweep can only ever have brought in the dispatcher, so complete the
    # family around whatever it found.
    if [ "$bundle_cudnn" != "false" ] && cudnn_is_referenced "$TARGET_LIB_DIR"; then
        complete_cudnn_family "$TARGET_LIB_DIR" "${cuda_lib_paths[@]}"
    fi

    # Hard-fail the image build rather than ship a backend that only breaks once
    # a model actually reaches a cuDNN call at inference time.
    verify_cudnn_bundle "$TARGET_LIB_DIR" "$cudnn_venv_state" "$cudnn_system_state"

    echo "CUDA libraries packaged successfully"
}

# Copy a ROCm library data subdirectory (e.g. rocblas, hipblaslt) into the
# bundled lib/ dir. These directories hold the TensileLibrary_*.dat GPU kernel
# tuning files, which rocBLAS/hipBLASLt load at runtime *relative to their own
# .so*. Since backends ship their own copies of libhipblaslt.so/librocblas.so
# under lib/, the matching data dir must travel with them or the libs fall back
# to slow generic kernels (rocblaslt error: Cannot read TensileLibrary_lazy_gfx*.dat;
# see issue #10660).
#
# The ROCm search roots default to /opt/rocm{,-*} but can be overridden via the
# ROCM_BASE_DIRS env var (space-separated), which keeps the copy unit-testable
# without a real ROCm install.
# Args: $1 = data subdir name found under <rocm-root>/lib{,64}/
copy_rocm_data_dir() {
    local data_name="$1"
    # Single-line `local x=$(...)` on purpose: `local` masks the command
    # substitution's exit status, which is 1 when nullglob is unset and would
    # otherwise trip the script's `set -e`.
    local old_nullglob=$(shopt -p nullglob)
    shopt -s nullglob
    local rocm_dirs
    if [ -n "${ROCM_BASE_DIRS:-}" ]; then
        # shellcheck disable=SC2206  # intentional word-split of the override
        rocm_dirs=(${ROCM_BASE_DIRS})
    else
        rocm_dirs=(/opt/rocm /opt/rocm-*)
    fi
    eval "$old_nullglob"
    local found=false
    local rocm_base lib_subdir
    for rocm_base in "${rocm_dirs[@]}"; do
        for lib_subdir in lib lib64; do
            if [ -d "$rocm_base/$lib_subdir/$data_name" ]; then
                echo "Found $data_name data at $rocm_base/$lib_subdir/$data_name"
                mkdir -p "$TARGET_LIB_DIR/$data_name"
                cp -arfL "$rocm_base/$lib_subdir/$data_name/"* "$TARGET_LIB_DIR/$data_name/" || echo "WARNING: Failed to copy $data_name data from $rocm_base/$lib_subdir/$data_name"
                found=true
            fi
        done
    done
    if [ "$found" = false ]; then
        echo "WARNING: No $data_name library data found in ${ROCM_BASE_DIRS:-/opt/rocm*}/lib{,64}/$data_name"
    fi
}

# Package AMD ROCm/HIPBlas libraries
package_rocm_libs() {
    echo "Packaging ROCm/HIPBlas libraries for BUILD_TYPE=${BUILD_TYPE}..."

    local rocm_lib_paths=(
        "/opt/rocm/lib"
        "/opt/rocm/lib64"
        "/opt/rocm/hip/lib"
    )

    # Find the actual ROCm versioned directory
    for rocm_dir in /opt/rocm-*; do
        if [ -d "$rocm_dir/lib" ]; then
            rocm_lib_paths+=("$rocm_dir/lib")
        fi
    done

    # Core ROCm/HIP runtime libraries
    local rocm_libs=(
        "libamdhip64.so*"
        "libhipblas.so*"
        "libhipblaslt.so*"
        "librocblas.so*"
        "librocrand.so*"
        "librocsparse.so*"
        "librocsolver.so*"
        "librocfft.so*"
        "libMIOpen.so*"
        "libroctx64.so*"
        "libhsa-runtime64.so*"
        "libamd_comgr.so*"
        "libhip_hcc.so*"
        "libhiprtc.so*"
    )

    for lib_path in "${rocm_lib_paths[@]}"; do
        if [ -d "$lib_path" ]; then
            for lib_pattern in "${rocm_libs[@]}"; do
                copy_libs_glob "${lib_path}/${lib_pattern}"
            done
        fi
    done

    # Copy rocBLAS and hipBLASLt kernel data (TensileLibrary_*.dat tuning files)
    # so the bundled libs find their per-arch kernels at runtime instead of
    # falling back to slow generic code (see copy_rocm_data_dir / issue #10660).
    copy_rocm_data_dir rocblas
    copy_rocm_data_dir hipblaslt

    # Copy libomp from LLVM (required for ROCm)
    # Single-line `local x=$(...)` on purpose: masks shopt -p's nonzero exit
    # (nullglob unset) so it doesn't trip `set -e`.
    local old_nullglob=$(shopt -p nullglob)
    shopt -s nullglob
    local omp_libs=(/opt/rocm*/lib/llvm/lib/libomp.so*)
    eval "$old_nullglob"
    for omp_path in "${omp_libs[@]}"; do
        if [ -e "$omp_path" ]; then
            copy_lib "$omp_path"
        fi
    done

    # Pull in transitive deps the allowlist misses (librocprofiler-register.so.0,
    # libnuma, libdrm_amdgpu, ...) so the backend is self-contained. See #10537.
    sweep_transitive_deps "$TARGET_LIB_DIR"

    echo "ROCm libraries packaged successfully"
}

# Package Intel oneAPI/SYCL libraries
package_intel_libs() {
    echo "Packaging Intel oneAPI/SYCL libraries for BUILD_TYPE=${BUILD_TYPE}..."

    local intel_lib_paths=(
        "/opt/intel/oneapi/compiler/latest/lib"
        "/opt/intel/oneapi/mkl/latest/lib/intel64"
        "/opt/intel/oneapi/tbb/latest/lib/intel64/gcc4.8"
    )

    # Core Intel oneAPI runtime libraries
    local intel_libs=(
        "libsycl.so*"
        "libOpenCL.so*"
        "libmkl_core.so*"
        "libmkl_intel_lp64.so*"
        "libmkl_intel_thread.so*"
        "libmkl_sequential.so*"
        "libmkl_sycl.so*"
        "libiomp5.so*"
        "libsvml.so*"
        "libirng.so*"
        "libimf.so*"
        "libintlc.so*"
        "libtbb.so*"
        "libtbbmalloc.so*"
        "libpi_level_zero.so*"
        "libpi_opencl.so*"
        "libze_loader.so*"
    )

    for lib_path in "${intel_lib_paths[@]}"; do
        if [ -d "$lib_path" ]; then
            for lib_pattern in "${intel_libs[@]}"; do
                copy_libs_glob "${lib_path}/${lib_pattern}"
            done
        fi
    done

    # Pull in transitive deps the allowlist misses so the backend is
    # self-contained (same class of failure as #10537).
    sweep_transitive_deps "$TARGET_LIB_DIR"

    echo "Intel oneAPI libraries packaged successfully"
}

# Package Vulkan libraries
package_vulkan_libs() {
    echo "Packaging Vulkan libraries for BUILD_TYPE=${BUILD_TYPE}..."

    local vulkan_lib_paths=(
        "/usr/lib/x86_64-linux-gnu"
        "/usr/lib/aarch64-linux-gnu"
        "/usr/local/lib"
    )

    # Core Vulkan runtime: the loader plus the shader tooling shipped by the SDK.
    local vulkan_libs=(
        "libvulkan.so*"
        "libshaderc_shared.so*"
        "libSPIRV.so*"
        "libSPIRV-Tools.so*"
        "libglslang.so*"
    )

    for lib_path in "${vulkan_lib_paths[@]}"; do
        if [ -d "$lib_path" ]; then
            for lib_pattern in "${vulkan_libs[@]}"; do
                copy_libs_glob "${lib_path}/${lib_pattern}"
            done
        fi
    done

    # Bundle the ICD drivers. Rather than hard-code Mesa's (platform- and
    # version-dependent) driver sonames, treat each installed ICD manifest as
    # the source of truth: every /usr/share/vulkan/icd.d/*.json names the exact
    # driver .so it needs in its "library_path". So we copy whatever drivers
    # the manifests reference (libvulkan_intel/radeon/lvp/... on amd64, the SoC
    # drivers on arm64, ...) plus each driver's transitive deps, and skip any
    # manifest whose driver isn't actually installed. The loader picks the
    # right driver for the GPU at runtime.
    if [ -d "/usr/share/vulkan/icd.d" ]; then
        local icd_dest="$TARGET_LIB_DIR/../vulkan/icd.d"
        mkdir -p "$icd_dest"

        local manifest driver driver_base resolved lib_path
        for manifest in /usr/share/vulkan/icd.d/*.json; do
            [ -e "$manifest" ] || continue

            # Pull the driver path out of "library_path": "<path-or-soname>".
            driver=$(sed -nE 's/.*"library_path"[[:space:]]*:[[:space:]]*"([^"]+)".*/\1/p' "$manifest" | head -n1)
            [ -n "$driver" ] || continue
            driver_base=$(basename "$driver")

            # Resolve to an absolute path: honour an absolute library_path,
            # else look in the standard lib dirs, else fall back to ldconfig.
            resolved=""
            case "$driver" in
                /*) [ -e "$driver" ] && resolved="$driver" ;;
            esac
            if [ -z "$resolved" ]; then
                for lib_path in "${vulkan_lib_paths[@]}"; do
                    if [ -e "${lib_path}/${driver_base}" ]; then
                        resolved="${lib_path}/${driver_base}"
                        break
                    fi
                done
            fi
            if [ -z "$resolved" ] && command -v ldconfig >/dev/null 2>&1; then
                resolved=$(ldconfig -p | awk -v n="$driver_base" '$1 == n { print $NF; exit }')
            fi

            if [ -z "$resolved" ] || [ ! -e "$resolved" ]; then
                echo "Vulkan ICD: driver '$driver_base' for $(basename "$manifest") not installed; skipping its manifest" >&2
                continue
            fi

            # Bundle the driver + its transitive deps (libdrm, libexpat, and
            # libLLVM for RADV/lavapipe, ...) so the backend is self-contained
            # on a runtime base image without Mesa.
            copy_lib "$resolved"
            copy_elf_deps "$resolved"

            # Copy the manifest and rewrite its library_path to a bare soname
            # so the loader resolves our bundled driver via LD_LIBRARY_PATH
            # (run.sh adds lib/ to it) instead of a host path that won't exist
            # on the runtime image.
            cp -arfL "$manifest" "$icd_dest/" 2>/dev/null || true
            sed -i -E 's#("library_path"[[:space:]]*:[[:space:]]*")[^"]*/#\1#' "$icd_dest/$(basename "$manifest")"
        done
    fi

    echo "Vulkan libraries packaged successfully"
}

# Main function to package GPU libraries based on BUILD_TYPE
package_gpu_libs() {
    local build_type="${BUILD_TYPE:-}"

    echo "Packaging GPU libraries for BUILD_TYPE=${build_type}..."

    case "$build_type" in
        cublas|l4t)
            package_cuda_libs
            ;;
        hipblas)
            package_rocm_libs
            ;;
        sycl_f16|sycl_f32|intel)
            package_intel_libs
            ;;
        vulkan)
            package_vulkan_libs
            ;;
        ""|cpu)
            echo "No GPU libraries to package for BUILD_TYPE=${build_type}"
            ;;
        *)
            echo "Unknown BUILD_TYPE: ${build_type}, skipping GPU library packaging"
            ;;
    esac

    echo "GPU library packaging complete. Contents of ${TARGET_LIB_DIR}:"
    ls -la "$TARGET_LIB_DIR/" 2>/dev/null || echo "  (empty or not created)"
}

# Export the function so it can be sourced and called
export -f package_gpu_libs
export -f copy_lib
export -f copy_libs_glob
export -f is_core_lib
export -f copy_elf_deps
export -f sweep_transitive_deps
export -f copy_rocm_data_dir
export -f cudnn_family_state
export -f cudnn_is_referenced
export -f complete_cudnn_family
export -f backend_has_venv
export -f cudnn_venv_lib_dir
export -f verify_cudnn_bundle
export -f package_cuda_libs
export -f package_rocm_libs
export -f package_intel_libs
export -f package_vulkan_libs

# If script is run directly (not sourced), execute the packaging
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    package_gpu_libs
fi

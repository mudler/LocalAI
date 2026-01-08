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

# Associative array to track copied files (keyed by inode to detect hardlinks and duplicates)
declare -A COPIED_INODES
# Associative array to track copied file paths
declare -A COPIED_FILES

# Helper function to get inode of a file (cross-platform: Linux and BSD/macOS)
get_inode() {
    local file="$1"
    stat -c '%i' "$file" 2>/dev/null || stat -f '%i' "$file" 2>/dev/null
}

# Helper function to copy library preserving symlinks structure
# Instead of following symlinks and duplicating files, this function:
# 1. Resolves symlinks to their real target
# 2. Copies the real file only once
# 3. Recreates the symlink structure in the target directory
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

        # Get inode of the real file to track duplicates
        local inode
        inode=$(get_inode "$real_file")

        # Copy the real file if we haven't already (check by inode and filename)
        if [[ -z "${COPIED_INODES[$inode]:-}" ]] && [[ -z "${COPIED_FILES[$real_basename]:-}" ]]; then
            cp -v "$real_file" "$TARGET_LIB_DIR/$real_basename" 2>/dev/null || true
            COPIED_INODES[$inode]="$real_basename"
            COPIED_FILES[$real_basename]=1
        fi

        # Create the symlink if the source name differs from the real file name
        if [ "$src_basename" != "$real_basename" ]; then
            # Point directly to the real file for simplicity and reliability
            ln -sfv "$real_basename" "$TARGET_LIB_DIR/$src_basename" 2>/dev/null || true
        fi
        COPIED_FILES[$src_basename]=1
    else
        # Source is a regular file
        local inode
        inode=$(get_inode "$src")

        # Copy only if we haven't already copied this file (by inode or name)
        if [[ -z "${COPIED_INODES[$inode]:-}" ]] && [[ -z "${COPIED_FILES[$src_basename]:-}" ]]; then
            cp -v "$src" "$TARGET_LIB_DIR/$src_basename" 2>/dev/null || true
            COPIED_INODES[$inode]="$src_basename"
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

# Package NVIDIA CUDA libraries
package_cuda_libs() {
    echo "Packaging CUDA libraries for BUILD_TYPE=${BUILD_TYPE}..."

    local cuda_lib_paths=(
        "/usr/local/cuda/lib64"
        "/usr/local/cuda-${CUDA_MAJOR_VERSION:-}/lib64"
        "/usr/lib/x86_64-linux-gnu"
        "/usr/lib/aarch64-linux-gnu"
    )

    # Core CUDA runtime libraries
    local cuda_libs=(
        "libcudart.so*"
        "libcublas.so*"
        "libcublasLt.so*"
        "libcufft.so*"
        "libcurand.so*"
        "libcusparse.so*"
        "libcusolver.so*"
        "libnvrtc.so*"
        "libnvrtc-builtins.so*"
        "libcudnn.so*"
        "libcudnn_ops.so*"
        "libcudnn_cnn.so*"
        "libnvJitLink.so*"
        "libnvinfer.so*"
        "libnvonnxparser.so*"
    )

    for lib_path in "${cuda_lib_paths[@]}"; do
        if [ -d "$lib_path" ]; then
            for lib_pattern in "${cuda_libs[@]}"; do
                copy_libs_glob "${lib_path}/${lib_pattern}"
            done
        fi
    done

    # Copy CUDA target directory for runtime compilation support
    if [ -d "/usr/local/cuda/targets" ]; then
        mkdir -p "$TARGET_LIB_DIR/../cuda"
        cp -arfL /usr/local/cuda/targets "$TARGET_LIB_DIR/../cuda/" 2>/dev/null || true
    fi

    echo "CUDA libraries packaged successfully"
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

    # Copy rocblas library data (tuning files, etc.)
    local old_nullglob=$(shopt -p nullglob)
    shopt -s nullglob
    local rocm_dirs=(/opt/rocm /opt/rocm-*)
    eval "$old_nullglob"
    for rocm_base in "${rocm_dirs[@]}"; do
        if [ -d "$rocm_base/lib/rocblas" ]; then
            mkdir -p "$TARGET_LIB_DIR/rocblas"
            cp -arfL "$rocm_base/lib/rocblas/"* "$TARGET_LIB_DIR/rocblas/" 2>/dev/null || true
        fi
    done

    # Copy libomp from LLVM (required for ROCm)
    shopt -s nullglob
    local omp_libs=(/opt/rocm*/lib/llvm/lib/libomp.so*)
    eval "$old_nullglob"
    for omp_path in "${omp_libs[@]}"; do
        if [ -e "$omp_path" ]; then
            copy_lib "$omp_path"
        fi
    done

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

    # Core Vulkan runtime libraries
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

    # Copy Vulkan ICD files
    if [ -d "/usr/share/vulkan/icd.d" ]; then
        mkdir -p "$TARGET_LIB_DIR/../vulkan/icd.d"
        cp -arfL /usr/share/vulkan/icd.d/* "$TARGET_LIB_DIR/../vulkan/icd.d/" 2>/dev/null || true
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
export -f get_inode
export -f copy_lib
export -f copy_libs_glob
export -f package_cuda_libs
export -f package_rocm_libs
export -f package_intel_libs
export -f package_vulkan_libs

# If script is run directly (not sourced), execute the packaging
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    package_gpu_libs
fi

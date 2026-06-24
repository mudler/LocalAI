#!/bin/bash
# Script to package runtime shared libraries for the vllm backend.
#
# The final Dockerfile.python stage is FROM scratch, so system libraries
# must be explicitly copied into ${BACKEND}/lib so the backend can run on
# any host without installing them. libbackend.sh automatically adds that
# directory to LD_LIBRARY_PATH at run time.
#
# vllm's CPU C++ extension (vllm._C) dlopens libnuma.so.1 at import time;
# if it's missing, the _C_utils torch ops are never registered and the
# engine crashes with AttributeError on init_cpu_threads_env. libgomp is
# used by torch's CPU kernels; on some stripped-down hosts it's also
# absent, so we bundle it too.

set -e

CURDIR=$(dirname "$(realpath "$0")")
LIB_DIR="${CURDIR}/lib"
mkdir -p "${LIB_DIR}"

copy_with_symlinks() {
    local soname="$1"
    local hit=""
    for dir in /usr/lib/x86_64-linux-gnu /usr/lib/aarch64-linux-gnu /lib/x86_64-linux-gnu /lib/aarch64-linux-gnu /usr/lib /lib; do
        if [ -e "${dir}/${soname}" ]; then
            hit="${dir}/${soname}"
            break
        fi
    done
    if [ -z "${hit}" ]; then
        echo "warning: ${soname} not found in standard lib paths" >&2
        return 0
    fi
    # Follow the symlink to the real file, copy it, then recreate the symlink.
    local real
    real=$(readlink -f "${hit}")
    cp -v "${real}" "${LIB_DIR}/"
    local real_base
    real_base=$(basename "${real}")
    if [ "${real_base}" != "${soname}" ]; then
        ln -sf "${real_base}" "${LIB_DIR}/${soname}"
    fi
}

copy_with_symlinks libnuma.so.1
copy_with_symlinks libgomp.so.1

# CPU profile only: bundle a g++ toolchain so torch._inductor's
# ISA probe (always run at vllm engine startup, regardless of
# enforce_eager) finds a C++ compiler. The LocalAI runtime image
# is FROM ubuntu:24.04 with a minimal apt list that does not
# include build-essential, and the backend image itself is FROM
# scratch -- so without this, cpu-vllm crashes with
# torch._inductor.exc.InvalidCxxCompiler at first inference
# unless the operator manually sets TORCH_COMPILE_DISABLE=1.
#
# We snapshot every file owned by the toolchain packages, mirroring
# the /usr/... layout into ${BACKEND}/toolchain/ so g++ can find
# cc1plus, headers, libs etc. via GCC_EXEC_PREFIX / CPATH /
# LIBRARY_PATH at runtime (libbackend.sh wires those up). Adds
# ~400 MB to the cpu-vllm image, which is tolerable -- cpu-vllm is
# already a niche profile.
if [ "${BUILD_TYPE:-}" = "" ] && command -v dpkg-query >/dev/null 2>&1; then
    TOOLCHAIN_DIR="${CURDIR}/toolchain"
    mkdir -p "${TOOLCHAIN_DIR}"
    # The unversioned g++/gcc packages on Debian/Ubuntu only ship
    # symlinks; the actual binaries live in g++-${VER}/gcc-${VER}.
    # Discover the active version so the symlink targets get bundled
    # along with their owners.
    GCC_VER=$(gcc -dumpversion 2>/dev/null | cut -d. -f1 || true)
    # `g++-${VER}` itself is just another symlink layer on Debian/
    # Ubuntu — the real binary `x86_64-linux-gnu-g++-${VER}` lives
    # in `g++-${VER}-x86-64-linux-gnu` (a separate package pulled in
    # as a dependency). Same story for gcc/cpp. Compute the dpkg
    # arch-triplet to find the right package name for both amd64 and
    # arm64 hosts.
    case "$(dpkg --print-architecture 2>/dev/null)" in
        amd64) HOST_TRIPLET="x86-64-linux-gnu" ;;
        arm64) HOST_TRIPLET="aarch64-linux-gnu" ;;
        *)     HOST_TRIPLET="" ;;
    esac
    PKGS=(g++ gcc cpp libstdc++-${GCC_VER}-dev libgcc-${GCC_VER}-dev libc6 libc6-dev binutils binutils-common libbinutils libc-dev-bin linux-libc-dev libcrypt-dev libgomp1 libstdc++6 libgcc-s1 libisl23 libmpc3 libmpfr6 libjansson4 libctf0 libctf-nobfd0 libsframe1)
    if [ -n "${GCC_VER}" ]; then
        PKGS+=("g++-${GCC_VER}" "gcc-${GCC_VER}" "cpp-${GCC_VER}" "gcc-${GCC_VER}-base")
        if [ -n "${HOST_TRIPLET}" ]; then
            PKGS+=(
                "g++-${GCC_VER}-${HOST_TRIPLET}"
                "gcc-${GCC_VER}-${HOST_TRIPLET}"
                "cpp-${GCC_VER}-${HOST_TRIPLET}"
                "binutils-${HOST_TRIPLET}"
            )
        fi
    fi
    for pkg in "${PKGS[@]}"; do
        if ! dpkg-query -W "${pkg}" >/dev/null 2>&1; then
            continue
        fi
        # Copy each owned path, preserving symlinks and mode. We
        # tolerate dpkg listing directories alongside files.
        dpkg -L "${pkg}" | while IFS= read -r path; do
            if [ -L "${path}" ] || [ -f "${path}" ]; then
                mkdir -p "${TOOLCHAIN_DIR}$(dirname "${path}")"
                cp -aP "${path}" "${TOOLCHAIN_DIR}${path}" 2>/dev/null || true
            fi
        done
    done
    # Ubuntu's filesystem layout has /lib -> /usr/lib (UsrMerge) and
    # /lib64 -> /usr/lib64. ld scripts (e.g. libm.so) hardcode
    # `/lib/x86_64-linux-gnu/libm.so.6`; with --sysroot the linker
    # looks for that path under the sysroot, which means we need
    # the same symlinks under TOOLCHAIN_DIR.
    [ -e "${TOOLCHAIN_DIR}/lib" ]   || ln -s usr/lib   "${TOOLCHAIN_DIR}/lib"
    [ -e "${TOOLCHAIN_DIR}/lib64" ] || ln -s usr/lib64 "${TOOLCHAIN_DIR}/lib64"

    # Replace the unversioned g++/gcc/cpp symlinks with wrapper
    # scripts that pass --sysroot=<toolchain> and -B <gcc-exec-prefix>.
    # Without these flags gcc would fall back to its compiled-in
    # /usr search and fail to find headers (the runtime image has no
    # libc6-dev) or fail to invoke `as`/`ld` (binutils not on PATH at
    # /usr/bin). Wrappers self-resolve their location at runtime so
    # they work from any BackendsPath.
    BIN_DIR="${TOOLCHAIN_DIR}/usr/bin"
    if [ -n "${GCC_VER}" ] && [ -n "${HOST_TRIPLET}" ]; then
        # HOST_TRIPLET in package names uses dashes ("x86-64-linux-gnu");
        # the binary suffix uses underscores in the arch part
        # ("x86_64-linux-gnu-g++-13"). Translate.
        BIN_TRIPLET=${HOST_TRIPLET//x86-64/x86_64}
        for tool in g++ gcc cpp; do
            real="${BIN_DIR}/${BIN_TRIPLET}-${tool}-${GCC_VER}"
            if [ -x "${real}" ]; then
                rm -f "${BIN_DIR}/${tool}" "${BIN_DIR}/${tool}-${GCC_VER}"
                cat > "${BIN_DIR}/${tool}" <<EOF
#!/bin/bash
# Auto-generated by package.sh. Passes --sysroot and -B so the
# bundled toolchain works from any BackendsPath without depending
# on libc6-dev / binutils being installed at /usr in the runtime
# image. See backend/python/vllm/package.sh.
DIR="\$(dirname "\$(readlink -f "\$0")")"     # …/toolchain/usr/bin
SYSROOT="\$(dirname "\$(dirname "\${DIR}")")" # …/toolchain
exec "\${DIR}/${BIN_TRIPLET}-${tool}-${GCC_VER}" \\
    -B "\${SYSROOT}/usr/lib/gcc/${BIN_TRIPLET}/${GCC_VER}/" \\
    --sysroot="\${SYSROOT}" \\
    "\$@"
EOF
                chmod +x "${BIN_DIR}/${tool}"
            fi
        done
    fi
    echo "Bundled g++ toolchain (gcc-${GCC_VER}) into ${TOOLCHAIN_DIR} ($(du -sh "${TOOLCHAIN_DIR}" | cut -f1))"
fi

echo "vllm packaging completed successfully"
ls -liah "${LIB_DIR}/"

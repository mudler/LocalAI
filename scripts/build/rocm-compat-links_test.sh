#!/usr/bin/env bash
# Regression test for .docker/rocm-compat-links.sh.
#
# ROCm 10.x installs into /opt/rocm/core-<major.minor>/ and no longer provides
# /opt/rocm/llvm or /opt/rocm-<version>/. The hipblas branch of the ggml backend
# Makefiles uses $(ROCM_HOME)/llvm/bin/clang++ as CXX, and the Dockerfiles linked
# /opt/rocm-**/lib/llvm/lib/libomp.so to /usr/lib/libomp.so. On a 10.x tree the
# first path does not exist and the second glob creates a dangling link.
#
# This test builds fake ROCm 7.x and 10.x trees, runs the real script against
# them and asserts that both paths resolve to real files afterwards. It also
# keeps the 7.x behaviour pinned (nothing replaced, libomp still found) and
# shows that the previous libomp command fails on the 10.x layout.
set -euo pipefail

CURDIR=$(dirname "$(realpath "$0")")
SCRIPT="$CURDIR/../../.docker/rocm-compat-links.sh"

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

fail() {
    echo "FAIL: $*"
    exit 1
}

# Fake ROCm 10.1 tree, laid out like the amdrocm*10.1 packages plus TheRock's
# fallback links (/opt/rocm/{bin,lib,include} -> core-10.1/...).
make_rocm10() {
    local root=$1
    local core="$root/core-10.1"
    mkdir -p "$core/lib/llvm/bin" "$core/lib/llvm/lib" "$core/bin" "$core/include"
    echo clang >"$core/lib/llvm/bin/clang"
    ln -s clang "$core/lib/llvm/bin/clang++"
    echo omp >"$core/lib/llvm/lib/libomp.so"
    ln -s core-10.1/lib "$root/lib"
    ln -s core-10.1/bin "$root/bin"
    ln -s core-10.1/include "$root/include"
}

# Fake ROCm 7.2 tree: /opt/rocm-7.2.1 with llvm/ and lib/llvm -> ../llvm, and
# /opt/rocm pointing at it.
make_rocm7() {
    local base=$1
    local versioned="$base/rocm-7.2.1"
    mkdir -p "$versioned/llvm/bin" "$versioned/llvm/lib" "$versioned/lib"
    echo clang >"$versioned/llvm/bin/clang"
    ln -s clang "$versioned/llvm/bin/clang++"
    echo omp >"$versioned/llvm/lib/libomp.so"
    ln -s ../llvm "$versioned/lib/llvm"
    ln -s rocm-7.2.1 "$base/rocm"
}

# --- ROCm 10.x layout ---------------------------------------------------------
R10="$WORK/r10/opt/rocm"
mkdir -p "$R10" "$WORK/r10/usr/lib"
make_rocm10 "$R10"
[ -e "$R10/llvm/bin/clang++" ] && fail "fixture already has \$ROCM_ROOT/llvm"

ROCM_ROOT="$R10" OMP_LINK_DIR="$WORK/r10/usr/lib" sh "$SCRIPT"

# What the Makefiles use as CXX/CC must now be a real file.
[ -f "$R10/llvm/bin/clang++" ] || fail "10.x: \$ROCM_HOME/llvm/bin/clang++ does not resolve"
[ -f "$R10/llvm/bin/clang" ] || fail "10.x: \$ROCM_HOME/llvm/bin/clang does not resolve"
# And libomp must be a link to the real library, not a dangling one.
[ -L "$WORK/r10/usr/lib/libomp.so" ] || fail "10.x: libomp.so link not created"
[ -f "$WORK/r10/usr/lib/libomp.so" ] || fail "10.x: libomp.so link is dangling"

# Running it again changes nothing and does not fail (Dockerfiles may run it
# in more than one stage).
before=$(readlink "$R10/llvm")
ROCM_ROOT="$R10" OMP_LINK_DIR="$WORK/r10/usr/lib" sh "$SCRIPT" >/dev/null
[ "$(readlink "$R10/llvm")" = "$before" ] || fail "10.x: second run changed \$ROCM_ROOT/llvm"

# Counter-check: the command this script replaces leaves a dangling link on
# the same 10.x tree. If this ever stops failing, the fixture no longer models
# ROCm 10 and the assertions above prove nothing.
mkdir -p "$WORK/r10-old/usr/lib"
# shellcheck disable=SC2211 # the unmatched glob is exactly what we demonstrate
ln -s "$R10"-**/lib/llvm/lib/libomp.so "$WORK/r10-old/usr/lib/libomp.so"
[ -e "$WORK/r10-old/usr/lib/libomp.so" ] && fail "old libomp glob unexpectedly resolves on the 10.x fixture"

# --- ROCm 7.x layout ----------------------------------------------------------
R7BASE="$WORK/r7/opt"
mkdir -p "$R7BASE" "$WORK/r7/usr/lib"
make_rocm7 "$R7BASE"

ROCM_ROOT="$R7BASE/rocm" OMP_LINK_DIR="$WORK/r7/usr/lib" sh "$SCRIPT"

# The real llvm/ directory of 7.x must not be replaced by a link.
[ -d "$R7BASE/rocm-7.2.1/llvm" ] && [ ! -L "$R7BASE/rocm-7.2.1/llvm" ] || fail "7.x: llvm/ was replaced"
[ -f "$R7BASE/rocm/llvm/bin/clang++" ] || fail "7.x: clang++ no longer resolves"
[ -f "$WORK/r7/usr/lib/libomp.so" ] || fail "7.x: libomp.so link missing or dangling"

# --- no ROCm at all (hipblas with SKIP_DRIVERS=true on ubuntu:24.04) ---------
mkdir -p "$WORK/none/usr/lib"
ROCM_ROOT="$WORK/none/opt/rocm" OMP_LINK_DIR="$WORK/none/usr/lib" sh "$SCRIPT" 2>/dev/null
[ -e "$WORK/none/opt/rocm/llvm" ] || [ -L "$WORK/none/opt/rocm/llvm" ] && fail "no ROCm: created \$ROCM_ROOT/llvm"
[ -L "$WORK/none/usr/lib/libomp.so" ] && fail "no ROCm: created a libomp.so link"

# --- an existing libomp.so is left alone -------------------------------------
mkdir -p "$WORK/keep/usr/lib" "$WORK/keep/opt/rocm"
make_rocm10 "$WORK/keep/opt/rocm"
echo distro >"$WORK/keep/usr/lib/libomp.so"
ROCM_ROOT="$WORK/keep/opt/rocm" OMP_LINK_DIR="$WORK/keep/usr/lib" sh "$SCRIPT" >/dev/null
[ ! -L "$WORK/keep/usr/lib/libomp.so" ] && [ "$(cat "$WORK/keep/usr/lib/libomp.so")" = distro ] ||
    fail "an existing libomp.so was replaced"

echo "PASS: rocm-compat-links resolves clang++ and libomp on ROCm 7.x and 10.x layouts"

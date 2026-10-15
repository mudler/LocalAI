#!/bin/bash
# Darwin uses system GPU frameworks and ships Bash 3.2 without associative arrays.
set -euo pipefail
SCRIPT="$(dirname "$(realpath "$0")")/package-gpu-libs.sh"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
uname() { echo Darwin; }
declare() {
    if [[ "${1:-}" == -A ]]; then
        echo 'Bash 3.2 does not support declare -A' >&2
        return 2
    fi
    builtin declare "$@"
}
export BUILD_TYPE=metal
source "$SCRIPT" "$WORK/lib"
package_gpu_libs
test -z "$(find "$WORK" -type f -print)"
echo 'PASS: Darwin does not load the Linux GPU packager'

#!/bin/bash
set -euo pipefail

build_type=${1:-}
pyproject=${2:?usage: prepare-source.sh BUILD_TYPE PYPROJECT}
prepared=$(mktemp "${pyproject}.XXXXXX")
trap 'rm -f "$prepared"' EXIT

awk -v build_type="$build_type" '
    /^dependencies = \[$/ { in_project_dependencies = 1 }
    build_type == "hipblas" && in_project_dependencies && /^[[:space:]]*"(torch|torchaudio)[^"]*",?[[:space:]]*$/ { next }
    in_project_dependencies && /^[[:space:]]*"pyaudio",?[[:space:]]*$/ { next }
    { print }
    in_project_dependencies && /^\]$/ { in_project_dependencies = 0 }
' "$pyproject" > "$prepared"

mv "$prepared" "$pyproject"
trap - EXIT

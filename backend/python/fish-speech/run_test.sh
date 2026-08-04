#!/bin/bash
# SPDX-License-Identifier: MIT
set -euo pipefail

backend_dir=$(cd "$(dirname "$0")" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

mkdir -p "$work/backend/common" "$work/backend/fish-speech-src"
cp "$backend_dir/run.sh" "$work/backend/run.sh"

cat > "$work/backend/common/libbackend.sh" <<'EOF'
EDIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
startBackend() {
    printf '%s\n' "$PYTHONPATH"
}
EOF

actual=$(PYTHONPATH=/existing/path bash "$work/backend/run.sh")
expected="$work/backend/fish-speech-src:/existing/path"

if [ "$actual" != "$expected" ]; then
    printf 'expected PYTHONPATH %s, got %s\n' "$expected" "$actual" >&2
    exit 1
fi

echo "PASS: relocated fish-speech source is importable"

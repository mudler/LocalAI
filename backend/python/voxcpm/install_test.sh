#!/bin/bash
# SPDX-License-Identifier: MIT
set -euo pipefail

backend_dir=$(cd "$(dirname "$0")" && pwd)
fixture_dir=$(mktemp -d)
trap 'rm -rf "$fixture_dir"' EXIT

mkdir -p "$fixture_dir/common" "$fixture_dir/voxcpm/modules/minicpm4"
cp "$backend_dir/install.sh" "$fixture_dir/install.sh"
cat > "$fixture_dir/common/libbackend.sh" <<'EOF'
installRequirements() { :; }
pip() { :; }
uv() { :; }
python() { printf '%s\n' "$VOXCPM_TEST_PACKAGE"; }
EOF

# Represent both methods touched by the old global substitution. Installation
# must preserve upstream attention code for the pinned CUDA 12 runtime.
cat > "$fixture_dir/expected.py" <<'EOF'
def forward():
    query_states = query_states.contiguous()

def forward_step():
    query_states = query_states.contiguous()
    key_cache = key_cache.contiguous()
    value_cache = value_cache.contiguous()
EOF

for use_pip in true false; do
    cp "$fixture_dir/expected.py" "$fixture_dir/voxcpm/modules/minicpm4/model.py"
    BUILD_TYPE=cublas BUILD_PROFILE=cublas12 USE_PIP="$use_pip" \
        VOXCPM_TEST_PACKAGE="$fixture_dir/voxcpm" bash "$fixture_dir/install.sh"
    if ! cmp -s "$fixture_dir/expected.py" "$fixture_dir/voxcpm/modules/minicpm4/model.py"; then
        echo "FAIL: CUDA 12 installation rewrote upstream attention (USE_PIP=$use_pip)" >&2
        exit 1
    fi
done

echo "PASS: pip and uv installation preserve CUDA 12 attention source"

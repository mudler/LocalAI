#!/bin/bash
set -euo pipefail

SCRIPT_DIR=$(dirname "$(realpath "$0")")
WORK_DIR=$(mktemp -d)
trap 'rm -rf "$WORK_DIR"' EXIT

write_fixture() {
    cat > "$1" <<'EOF'
[project]
dependencies = [
    "numpy",
    "torch==2.8.0",
    "torchaudio==2.8.0",
    "pyaudio",
]

[project.optional-dependencies]
stable = [
    "torch==2.8.0",
    "torchaudio",
]
EOF
}

write_fixture "$WORK_DIR/rocm.toml"
write_fixture "$WORK_DIR/cuda.toml"
write_fixture "$WORK_DIR/cpu.toml"

bash "$SCRIPT_DIR/prepare-source.sh" hipblas "$WORK_DIR/rocm.toml"
bash "$SCRIPT_DIR/prepare-source.sh" cublas "$WORK_DIR/cuda.toml"
bash "$SCRIPT_DIR/prepare-source.sh" "" "$WORK_DIR/cpu.toml"

cat > "$WORK_DIR/expected-rocm.toml" <<'EOF'
[project]
dependencies = [
    "numpy",
]

[project.optional-dependencies]
stable = [
    "torch==2.8.0",
    "torchaudio",
]
EOF

cat > "$WORK_DIR/expected-default.toml" <<'EOF'
[project]
dependencies = [
    "numpy",
    "torch==2.8.0",
    "torchaudio==2.8.0",
]

[project.optional-dependencies]
stable = [
    "torch==2.8.0",
    "torchaudio",
]
EOF

diff -u "$WORK_DIR/expected-rocm.toml" "$WORK_DIR/rocm.toml"
diff -u "$WORK_DIR/expected-default.toml" "$WORK_DIR/cuda.toml"
diff -u "$WORK_DIR/expected-default.toml" "$WORK_DIR/cpu.toml"

echo "PASS: source preparation preserves each platform's PyTorch dependencies"

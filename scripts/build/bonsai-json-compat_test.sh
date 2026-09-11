#!/bin/bash
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
PATCHER="$ROOT/backend/cpp/bonsai/patch-grpc-server.sh"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

cat > "$WORK/grpc-server.cpp" <<'EOF'
try {
    json::parse("{");
} catch (const common_json_error& e) {
}
EOF

bash "$PATCHER" "$WORK/grpc-server.cpp"
grep -q 'catch (const json::parse_error& e)' "$WORK/grpc-server.cpp"
! grep -q 'common_json_error' "$WORK/grpc-server.cpp"

# A repeated preparation pass must not change the generated source.
cp "$WORK/grpc-server.cpp" "$WORK/once.cpp"
bash "$PATCHER" "$WORK/grpc-server.cpp"
cmp "$WORK/once.cpp" "$WORK/grpc-server.cpp"

echo "PASS: Bonsai uses its fork-compatible JSON exception"

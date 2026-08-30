#!/bin/bash
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
PATCHER="$ROOT/backend/cpp/bonsai/patch-grpc-server.sh"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

mkdir -p "$WORK/old/common" "$WORK/new/common"
cat > "$WORK/old/grpc-server.cpp" <<'EOF'
try {
    json::parse("{");
} catch (const common_json_error& e) {
}
EOF
cat > "$WORK/old/common/json.h" <<'EOF'
// The older Bonsai JSON API exposes nlohmann's parse exception directly.
EOF
cat > "$WORK/new/grpc-server.cpp" <<'EOF'
try {
    json::parse("{");
} catch (const json::parse_error& e) {
}
EOF
cat > "$WORK/new/common/json.h" <<'EOF'
struct common_json_error : std::runtime_error {};
EOF

bash "$PATCHER" "$WORK/old/grpc-server.cpp" "$WORK/old"
grep -q 'catch (const json::parse_error& e)' "$WORK/old/grpc-server.cpp"
! grep -q 'common_json_error' "$WORK/old/grpc-server.cpp"

bash "$PATCHER" "$WORK/new/grpc-server.cpp" "$WORK/new"
grep -q 'catch (const common_json_error& e)' "$WORK/new/grpc-server.cpp"
! grep -q 'json::parse_error' "$WORK/new/grpc-server.cpp"

# A repeated preparation pass must not change the generated source.
cp "$WORK/old/grpc-server.cpp" "$WORK/old/once.cpp"
cp "$WORK/new/grpc-server.cpp" "$WORK/new/once.cpp"
bash "$PATCHER" "$WORK/old/grpc-server.cpp" "$WORK/old"
bash "$PATCHER" "$WORK/new/grpc-server.cpp" "$WORK/new"
cmp "$WORK/old/once.cpp" "$WORK/old/grpc-server.cpp"
cmp "$WORK/new/once.cpp" "$WORK/new/grpc-server.cpp"

echo "PASS: Bonsai selects the JSON exception exposed by its pinned fork"

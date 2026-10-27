#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# Sourced by the Go gates. Generated protobuf bindings and the react-ui dist are
# gitignored, so a clean checkout lacks them. This writes them into a scratch
# directory and an overlay that `go build`/`go test -overlay` read, leaving the
# source tree untouched. Call from the repository root.
#
#   write_generated_overlay <scratch>   # writes <scratch>/overlay.json
write_generated_overlay() {
    local scratch=$1
    mkdir -p "$scratch/generated"
    protoc --experimental_allow_proto3_optional -Ibackend/ \
        --go_out="$scratch/generated" --go_opt=paths=source_relative \
        --go-grpc_out="$scratch/generated" --go-grpc_opt=paths=source_relative backend/backend.proto
    # pkg/motion/proto/motion.pb.go is generated and gitignored; build it the same way.
    protoc -Ipkg/motion/proto --go_out="$scratch/generated" --go_opt=paths=source_relative pkg/motion/proto/motion.proto
    python3 -I - "$scratch" <<'PY'
import json,sys
from pathlib import Path
scratch=Path(sys.argv[1]); root=Path.cwd()
replacements = {str(root/'pkg/grpc/proto'/name): str(scratch/'generated'/name)
                for name in ('backend.pb.go','backend_grpc.pb.go')}
# Compile-only embed fixture, not a UI build. Keep it outside the source tree.
fixture = scratch/'index.html'
fixture.write_text('<!doctype html><html><head><script src="/assets/contracts-12345678.js"></script></head><body><div id="root"></div></body></html>\n')
asset = scratch/'contracts-12345678.js'
asset.write_text('// Compile-only HTTP static-handler fixture, not a React build.\n')
replacements[str(root/'core/http/react-ui/dist/assets/contracts-12345678.js')] = str(asset)
replacements[str(root/'core/http/react-ui/dist/index.html')] = str(fixture)
replacements[str(root/'pkg/motion/proto/motion.pb.go')] = str(scratch/'generated'/'motion.pb.go')
(scratch/'overlay.json').write_text(json.dumps({'Replace': replacements}))
PY
}

#!/bin/bash

set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
SOURCE="$SCRIPT_DIR/../llama-cpp/grpc-server.cpp"
TMP_DIR=$(mktemp -d)
trap 'rm -rf "$TMP_DIR"' EXIT

cp "$SOURCE" "$TMP_DIR/grpc-server.cpp"
bash "$SCRIPT_DIR/patch-grpc-server.sh" "$TMP_DIR/grpc-server.cpp"
bash "$SCRIPT_DIR/../llama-cpp/disable-score-task.sh" "$TMP_DIR/grpc-server.cpp"
bash "$SCRIPT_DIR/patch-grpc-server.sh" "$TMP_DIR/grpc-server.cpp"

for unsupported in \
    'params.cache_idle_slots' \
    'params.speculative.types' \
    'params.speculative.draft.' \
    'common_speculative_types_from_names' \
    'COMMON_SPECULATIVE_TYPE_DRAFT_SIMPLE' \
    'ctx_server.impl->model_tgt'; do
    if grep -Fq "$unsupported" "$TMP_DIR/grpc-server.cpp"; then
        echo "unsupported buun API remains: $unsupported" >&2
        exit 1
    fi
done

grep -Fq '#define LOCALAI_LLAMA_CPP_NO_SCORE_TASK 1' "$TMP_DIR/grpc-server.cpp"
grep -Fq '#define LOCALAI_TURBOQUANT_NO_CHECKPOINT_MIN_STEP 1' "$TMP_DIR/grpc-server.cpp"
grep -Fq 'params.speculative.mparams_dft.path = request->draftmodel();' "$TMP_DIR/grpc-server.cpp"
grep -Fq 'params.speculative.type = COMMON_SPECULATIVE_TYPE_DRAFT;' "$TMP_DIR/grpc-server.cpp"
grep -Fq 'ctx_server.impl->model' "$TMP_DIR/grpc-server.cpp"

echo "buun grpc-server compatibility transform passed"

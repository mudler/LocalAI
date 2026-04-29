#!/usr/bin/env bash
# vLLM multi-node DP smoke test.
#
# Brings up the head + follower via docker compose, polls until the
# model is loaded across both ranks, then sends a chat completion
# request and asserts a non-empty response.
#
# Requires 2 NVIDIA GPUs and nvidia-container-runtime on the host.
#
# Usage:
#   ./tests/e2e/vllm-multinode/smoke.sh
#   ./tests/e2e/vllm-multinode/smoke.sh --keep    # leave containers running on success
set -euo pipefail

KEEP=0
COMPOSE_FILE="${COMPOSE_FILE:-docker-compose.vllm-multinode.yaml}"
[[ "${1:-}" == "--keep" ]] && KEEP=1
[[ "${1:-}" == "--intel" ]] && COMPOSE_FILE="docker-compose.vllm-multinode.intel.yaml"

cd "$(dirname "$0")/../../.."

cleanup() {
    if [[ "${KEEP}" -eq 0 ]]; then
        docker compose -f "${COMPOSE_FILE}" down -v >/dev/null 2>&1 || true
    fi
}
trap cleanup EXIT

echo "[smoke] starting head + follower (${COMPOSE_FILE})"
docker compose -f "${COMPOSE_FILE}" up -d

echo "[smoke] waiting for /readyz on the head (up to 10 minutes for first boot)"
for _ in $(seq 1 60); do
    if curl -sf http://localhost:8080/readyz >/dev/null; then
        break
    fi
    sleep 10
done
curl -sf http://localhost:8080/readyz >/dev/null || {
    echo "[smoke] head never became ready"
    docker compose -f "${COMPOSE_FILE}" logs --tail=200
    exit 1
}

echo "[smoke] sending chat completion"
response=$(curl -sf -X POST http://localhost:8080/v1/chat/completions \
    -H "Content-Type: application/json" \
    -d '{
        "model": "qwen-dp",
        "messages": [{"role": "user", "content": "Reply with the single word: pong."}],
        "max_tokens": 16,
        "temperature": 0
    }')

echo "[smoke] response: ${response}"

content=$(echo "${response}" | python3 -c 'import json,sys; print(json.load(sys.stdin)["choices"][0]["message"]["content"])')
if [[ -z "${content}" ]]; then
    echo "[smoke] empty completion content"
    exit 1
fi

echo "[smoke] OK — DP=2 cluster served a completion"

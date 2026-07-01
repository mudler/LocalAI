#!/usr/bin/env bash
set -euo pipefail

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  cat <<'EOF'
Usage: paged-inference-gates.sh

Run the LocalAI paged llama.cpp inference safety gates on a DGX checkout.

Environment:
  BIN        llama.cpp build bin dir (default: ~/llama-phase6-source/build-cuda/bin)
  MOE        MoE GGUF path (default: ~/bench/q36-35b-a3b-nvfp4.gguf)
  DENSE      Dense GGUF path (default: ~/bench/q36-27b-nvfp4.gguf)
  ART        artifact dir (default: ~/bench/paged_inference_gates/<timestamp>)
  OPS        comma-separated test-backend-ops filters (default: MUL_MAT_ID)
  EXTRA_ENV  extra env assignments for completion gates, e.g. "GDN_TC=5"

Expected md5:
  MoE paged:   8cb0ce23777bf55f92f63d0292c756b0
  Dense paged: 5951a5b4d624ce891e22ab5fca9bc439
EOF
  exit 0
fi

MOE_MD5_EXPECTED=8cb0ce23777bf55f92f63d0292c756b0
DENSE_MD5_EXPECTED=5951a5b4d624ce891e22ab5fca9bc439

BIN=${BIN:-"$HOME/llama-phase6-source/build-cuda/bin"}
MOE=${MOE:-"$HOME/bench/q36-35b-a3b-nvfp4.gguf"}
DENSE=${DENSE:-"$HOME/bench/q36-27b-nvfp4.gguf"}
OPS=${OPS:-MUL_MAT_ID}
ART=${ART:-"$HOME/bench/paged_inference_gates/$(date +%Y%m%d_%H%M%S)"}
EXTRA_ENV=${EXTRA_ENV:-}

require_file() {
  if [[ ! -e "$1" ]]; then
    echo "missing required path: $1" >&2
    exit 2
  fi
}

check_idle() {
  if command -v docker >/dev/null 2>&1; then
    local docker_count
    docker_count=$(docker ps -q | wc -l)
    if [[ "$docker_count" != "0" ]]; then
      echo "docker containers are running: $docker_count" >&2
      docker ps >&2
      exit 3
    fi

    local local_ai_worker
    local_ai_worker=$(docker ps --format "{{.Names}}" | grep -c local-ai-worker || true)
    if [[ "$local_ai_worker" != "0" ]]; then
      echo "local-ai-worker container is running" >&2
      exit 3
    fi
  fi

  if command -v nvidia-smi >/dev/null 2>&1; then
    local compute_count
    compute_count=$(nvidia-smi --query-compute-apps=pid --format=csv,noheader | sed "/^$/d" | wc -l)
    if [[ "$compute_count" != "0" ]]; then
      echo "GPU compute processes are already running: $compute_count" >&2
      nvidia-smi >&2
      exit 3
    fi
  fi

  local owner_file="$HOME/gpu_bench_lock/owner"
  if [[ -f "$owner_file" ]]; then
    local owner
    owner=$(cat "$owner_file")
    if [[ -n "$owner" && "$owner" != FREE* ]]; then
      echo "GPU lock is owned: $owner" >&2
      exit 3
    fi
  fi
}

run_completion_gate() {
  local name=$1
  local model=$2
  local expected=$3
  local out="$ART/${name}.txt"
  local err="$ART/${name}.err"
  local md5_file="$ART/${name}.md5"

  env LLAMA_KV_PAGED=1 LLAMA_MOE_FORCE_GRAPHS=1 GGML_NO_BACKTRACE=1 $EXTRA_ENV \
    "$BIN/llama-completion" -m "$model" -ngl 99 -fa on -c 4096 \
      --temp 0 --seed 1 -n 48 -p "The capital of France is" \
      </dev/null >"$out" 2>"$err"

  md5sum "$out" >"$md5_file"
  local actual
  actual=$(awk '{print $1}' "$md5_file")
  if [[ "$actual" != "$expected" ]]; then
    echo "$name md5 mismatch: got $actual expected $expected" >&2
    echo "artifacts: $ART" >&2
    exit 4
  fi
  echo "$name md5 OK: $actual"
}

run_op_gate() {
  local op=$1
  local out="$ART/op_${op}.txt"
  "$BIN/test-backend-ops" test -b CUDA0 -o "$op" -j 1 >"$out" 2>&1
  if ! grep -q "Backend CUDA0: .*OK" "$out"; then
    echo "$op gate failed" >&2
    tail -80 "$out" >&2
    echo "artifacts: $ART" >&2
    exit 5
  fi
  grep -E "[0-9]+/[0-9]+ tests passed|Backend CUDA0" "$out" | tail -2
}

mkdir -p "$ART"
require_file "$BIN/llama-completion"
require_file "$BIN/test-backend-ops"
require_file "$MOE"
require_file "$DENSE"
check_idle

run_completion_gate moe "$MOE" "$MOE_MD5_EXPECTED"
run_completion_gate dense "$DENSE" "$DENSE_MD5_EXPECTED"

IFS=',' read -r -a op_list <<<"$OPS"
for op in "${op_list[@]}"; do
  op=${op//[[:space:]]/}
  [[ -n "$op" ]] || continue
  run_op_gate "$op"
done

echo "paged inference gates OK"
echo "artifacts: $ART"

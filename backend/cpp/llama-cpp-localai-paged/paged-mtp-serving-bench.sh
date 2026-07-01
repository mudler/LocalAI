#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: paged-mtp-serving-bench.sh

Runs a direct llama-server serving A/B on DGX:
  baseline: no speculative decoding
  mtp:      --spec-type draft-mtp

Environment overrides:
  SRC       llama.cpp source dir (default: ~/llama-phase6-source)
  BIN       binary dir (default: $SRC/build-cuda/bin)
  MODEL     MoE GGUF path (default: ~/bench/q36-35b-a3b-nvfp4.gguf)
  ART       artifact dir (default: ~/bench/phase15_mtp_serving/<timestamp>)
  PORT      server port (default: 8097)
  NPL       comma/space list of concurrency values (default: "8 32 128")
  PTOK      prompt filler words for h2h_cli3.py (default: 128)
  GEN       max generated tokens (default: 128)
  CTX       server context (default: 131072)
  PARALLEL  server parallel slots (default: 128)
  BATCH     server logical batch size (default: 2048)
  UBATCH    server physical batch size (default: 512)
  SKIP_GATES=1 to skip pre/post paged inference gates
EOF
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

SRC=${SRC:-"$HOME/llama-phase6-source"}
BIN=${BIN:-"$SRC/build-cuda/bin"}
MODEL=${MODEL:-"$HOME/bench/q36-35b-a3b-nvfp4.gguf"}
ART=${ART:-"$HOME/bench/phase15_mtp_serving/$(date +%Y%m%d_%H%M%S)"}
PORT=${PORT:-8097}
NPL=${NPL:-"8 32 128"}
PTOK=${PTOK:-128}
GEN=${GEN:-128}
CTX=${CTX:-131072}
PARALLEL=${PARALLEL:-128}
BATCH=${BATCH:-2048}
UBATCH=${UBATCH:-512}
SKIP_GATES=${SKIP_GATES:-0}

LOCK_DIR="$HOME/gpu_bench_lock"
OWNER="$LOCK_DIR/owner"
SERVER_PID=""

log() {
  printf '[%s] %s\n' "$(date -Is)" "$*" | tee -a "$ART/run.log"
}

preflight() {
  mkdir -p "$ART"
  local docker_count local_ai compute owner
  docker_count=$(docker ps -q | wc -l)
  local_ai=$(docker ps --format "{{.Names}}" | grep -c local-ai-worker || true)
  compute=$(nvidia-smi --query-compute-apps=pid --format=csv,noheader | sed '/^$/d' | wc -l)
  owner="FREE-no-lock-file"
  if [[ -f "$OWNER" ]]; then
    owner=$(cat "$OWNER")
  fi
  {
    echo "docker=$docker_count"
    echo "local_ai_worker=$local_ai"
    echo "compute=$compute"
    echo "$owner"
  } | tee "$ART/preflight.txt"
  [[ "$docker_count" == "0" ]]
  [[ "$local_ai" == "0" ]]
  [[ "$compute" == "0" ]]
  case "$owner" in
    FREE*|FREE-no-lock-file) ;;
    *) echo "GPU lock is busy: $owner" >&2; exit 2 ;;
  esac
}

acquire_lock() {
  mkdir -p "$LOCK_DIR"
  echo "codex-phase15-mtp-serving-bench $(date +%s)" > "$OWNER"
}

release_lock() {
  if [[ -n "$SERVER_PID" ]]; then
    kill "$SERVER_PID" >/dev/null 2>&1 || true
    wait "$SERVER_PID" >/dev/null 2>&1 || true
    SERVER_PID=""
  fi
  mkdir -p "$LOCK_DIR"
  echo "FREE released-by-codex-phase15-mtp-serving-bench $(date +%s)" > "$OWNER"
}

wait_server() {
  local health="$1"
  for _ in $(seq 1 180); do
    if curl -fsS "http://127.0.0.1:$PORT/health" > "$health" 2>"$health.err"; then
      return 0
    fi
    if ! kill -0 "$SERVER_PID" 2>/dev/null; then
      return 1
    fi
    sleep 1
  done
  return 1
}

stop_server() {
  if [[ -n "$SERVER_PID" ]]; then
    kill "$SERVER_PID" >/dev/null 2>&1 || true
    wait "$SERVER_PID" >/dev/null 2>&1 || true
    SERVER_PID=""
  fi
}

run_gate() {
  local name="$1"
  if [[ "$SKIP_GATES" == "1" ]]; then
    log "skipping $name inference gate"
    return
  fi
  log "running $name inference gate"
  ART="$ART/gate_$name" "$HOME/paged-inference-gates.sh" > "$ART/gate_$name.log" 2>&1
  cat "$ART/gate_$name.log" | tee -a "$ART/run.log"
}

run_arm() {
  local arm="$1"
  shift
  local arm_dir="$ART/$arm"
  mkdir -p "$arm_dir"
  log "starting $arm server"
  cd "$BIN"
  env LLAMA_KV_PAGED=1 LLAMA_MOE_FORCE_GRAPHS=1 GGML_NO_BACKTRACE=1 \
    ./llama-server \
      -m "$MODEL" -ngl 99 -fa on -c "$CTX" -b "$BATCH" -ub "$UBATCH" \
      --parallel "$PARALLEL" --host 127.0.0.1 --port "$PORT" --no-webui "$@" \
      > "$arm_dir/server.log" 2>&1 &
  SERVER_PID=$!
  if ! wait_server "$arm_dir/health.json"; then
    tail -120 "$arm_dir/server.log" >&2 || true
    exit 3
  fi

  for n in $NPL; do
    log "running $arm n=$n"
    python3 "$HOME/bench/h2h_cli3.py" \
      --url "http://127.0.0.1:$PORT/v1/completions" \
      --model m -n "$n" --ptok "$PTOK" --gen "$GEN" \
      --nonce "${arm}_${n}_$(date +%s)" --no-cache \
      > "$arm_dir/n${n}.json"
    cat "$arm_dir/n${n}.json" | tee -a "$ART/run.log"
  done

  grep -E "draft acceptance|statistics[[:space:]]+draft-mtp|speculative decoding context|bounded partial|backend sampling|common_speculative_impl_draft_mtp" \
    "$arm_dir/server.log" > "$arm_dir/spec_lines.txt" || true
  stop_server
}

preflight

log "building llama-server and test-backend-ops"
cmake --build "$SRC/build-cuda" --target llama-server test-backend-ops llama-completion -j 8 \
  > "$ART/build.log" 2>&1

if [[ ! -x "$HOME/paged-inference-gates.sh" ]]; then
  echo "missing $HOME/paged-inference-gates.sh; copy paged-inference-gates.sh there first" >&2
  exit 4
fi

run_gate pre
acquire_lock
trap release_lock EXIT
run_arm baseline
run_arm mtp --spec-type draft-mtp --spec-draft-n-max 3 --no-spec-draft-backend-sampling
release_lock
trap - EXIT
run_gate post

python3 - "$ART" <<'PY' | tee "$ART/summary.tsv"
import json
import sys
from pathlib import Path

art = Path(sys.argv[1])
rows = []
for arm in ("baseline", "mtp"):
    for path in sorted((art / arm).glob("n*.json")):
        data = json.loads(path.read_text())
        rows.append((arm, data["n"], data["gen_total"], data["agg_tps"],
                     data["decode_agg_tps"], data["decode_perseq_tps"],
                     data["ttft_mean_ms"], data["wall_s"]))
print("arm\tn\tgen_total\tagg_tps\tdecode_agg_tps\tdecode_perseq_tps\tttft_mean_ms\twall_s")
for row in rows:
    print("\t".join(str(x) for x in row))
PY

log "artifacts: $ART"

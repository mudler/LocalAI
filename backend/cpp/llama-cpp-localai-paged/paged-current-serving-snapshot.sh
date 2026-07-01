#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: paged-current-serving-snapshot.sh [--summarize-gates ART]

Run a current-stack paged llama.cpp vs vLLM MoE serving snapshot on DGX.

This harness uses the clean llama.cpp mirror by default, not stale development
trees. It runs pre/post paged inference gates, then a same-session serving
comparison with the h2h client.

Environment overrides:
  SRC          llama.cpp source dir (default: ~/llama-phase6-source)
  BIN          llama.cpp build bin dir (default: $SRC/build-cuda/bin)
  MODEL        paged GGUF path (default: ~/bench/q36-35b-a3b-nvfp4.gguf)
  VLLM_MODEL   vLLM model dir (default: ~/bench/q36-35b-a3b-nvfp4-vllm)
  H2H          h2h client (default: ~/bench/h2h_cli3.py)
  ART          artifact dir (default: ~/bench/phase_current_serving_snapshot/<timestamp>)
  NPL          concurrency list (default: "8 32 128")
  PTOK         prompt filler words (default: 128)
  GEN          generated tokens (default: 64)
  CTX          llama-server context (default: 131072)
  PARALLEL     llama-server parallel slots (default: 128)
  BATCH        llama-server logical batch (default: 2048)
  UBATCH       llama-server physical batch (default: 512)
  LLAMA_PORT   llama-server port (default: 8098)
  VLLM_PORT    vLLM port (default: 8000)
  VLLM_BIN     vLLM executable (default: ~/vllm-bench/bin/vllm)
  SKIP_GATES=1 to skip pre/post paged inference gates
  DRY_RUN=1    validate inputs/preflight, write hardware.txt, and print commands without running servers

Options:
  --summarize-gates ART  write ART/gate_summary.tsv from existing gate_pre/gate_post artifacts
EOF
}

SUMMARY_GATES_ART=""
case "${1:-}" in
  -h|--help)
  usage
  exit 0
  ;;
  --summarize-gates)
    if [[ -z "${2:-}" ]]; then
      usage >&2
      exit 2
    fi
    SUMMARY_GATES_ART="$2"
  ;;
  "")
  ;;
  *)
    usage >&2
    exit 2
  ;;
esac

SRC=${SRC:-"$HOME/llama-phase6-source"}
BIN=${BIN:-"$SRC/build-cuda/bin"}
MODEL=${MODEL:-"$HOME/bench/q36-35b-a3b-nvfp4.gguf"}
VLLM_MODEL=${VLLM_MODEL:-"$HOME/bench/q36-35b-a3b-nvfp4-vllm"}
H2H=${H2H:-"$HOME/bench/h2h_cli3.py"}
ART=${ART:-"$HOME/bench/phase_current_serving_snapshot/$(date +%Y%m%d_%H%M%S)"}
NPL=${NPL:-"8 32 128"}
PTOK=${PTOK:-128}
GEN=${GEN:-64}
CTX=${CTX:-131072}
PARALLEL=${PARALLEL:-128}
BATCH=${BATCH:-2048}
UBATCH=${UBATCH:-512}
LLAMA_PORT=${LLAMA_PORT:-8098}
VLLM_PORT=${VLLM_PORT:-8000}
VLLM_BIN=${VLLM_BIN:-"$HOME/vllm-bench/bin/vllm"}
SKIP_GATES=${SKIP_GATES:-0}
DRY_RUN=${DRY_RUN:-0}
MOE_MD5_EXPECTED=8cb0ce23777bf55f92f63d0292c756b0
DENSE_MD5_EXPECTED=5951a5b4d624ce891e22ab5fca9bc439

LOCK_DIR="$HOME/gpu_bench_lock"
OWNER="$LOCK_DIR/owner"
SERVER_PID=""

log() {
  printf '[%s] %s\n' "$(date -Is)" "$*" | tee -a "$ART/run.log"
}

require_path() {
  if [[ ! -e "$1" ]]; then
    echo "missing required path: $1" >&2
    exit 2
  fi
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
    *) echo "GPU lock is busy: $owner" >&2; exit 3 ;;
  esac
}

write_hardware_report() {
  local out="$ART/hardware.txt"
  local gpu_name hardware_class

  gpu_name=$(nvidia-smi --query-gpu=name --format=csv,noheader 2>/dev/null | head -1 || true)
  hardware_class="unknown"
  case "$gpu_name" in
    *B200*|*B100*|*GB200*) hardware_class="datacenter_blackwell" ;;
    *H200*|*H100*) hardware_class="datacenter_other" ;;
    *GB10*|*"DGX Spark"*|*RTX*|*"PRO 6000"*) hardware_class="gb10_or_workstation_blackwell" ;;
  esac

  {
    echo "nvidia_smi_L:"
    nvidia-smi -L || true
    echo
    echo "nvidia_smi_query:"
    if ! nvidia-smi --query-gpu=name,driver_version,memory.total,compute_cap --format=csv,noheader; then
      nvidia-smi --query-gpu=name,driver_version,memory.total --format=csv,noheader || true
    fi
    echo
    echo "gpu_name=$gpu_name"
    echo "hardware_class=$hardware_class"
    case "$hardware_class" in
      datacenter_blackwell)
        echo "parity_note=datacenter Blackwell hardware: full parity methodology can choose new levers"
        ;;
      datacenter_other)
        echo "parity_note=datacenter non-Blackwell hardware: do not generalize GB10 parity decisions"
        ;;
      gb10_or_workstation_blackwell)
        echo "parity_note=GB10/workstation Blackwell hardware: GB10 shortcut closures apply unless new evidence says otherwise"
        ;;
      *)
        echo "parity_note=unknown hardware: classify before making parity claims"
        ;;
    esac
  } > "$out"
  log "hardware report: $out"
}

acquire_lock() {
  mkdir -p "$LOCK_DIR"
  echo "codex-current-serving-snapshot $(date +%s)" > "$OWNER"
}

release_lock() {
  if [[ -n "$SERVER_PID" ]]; then
    kill "$SERVER_PID" >/dev/null 2>&1 || true
    wait "$SERVER_PID" >/dev/null 2>&1 || true
    SERVER_PID=""
  fi
  pkill -9 -f "[l]lama-server.*--port $LLAMA_PORT" >/dev/null 2>&1 || true
  pkill -9 -u "$(id -u)" -f "[v]llm serve" >/dev/null 2>&1 || true
  mkdir -p "$LOCK_DIR"
  echo "FREE released-by-codex-current-serving-snapshot $(date +%s)" > "$OWNER"
}

wait_http() {
  local url="$1"
  local pattern="$2"
  local log_file="$3"
  local health="$4"
  for _ in $(seq 1 240); do
    if curl -fsS "$url" > "$health" 2>"$health.err" && grep -q "$pattern" "$health"; then
      return 0
    fi
    if [[ -n "$SERVER_PID" ]] && ! kill -0 "$SERVER_PID" >/dev/null 2>&1; then
      tail -120 "$log_file" >&2 || true
      return 1
    fi
    sleep 1
  done
  tail -120 "$log_file" >&2 || true
  return 1
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

run_paged() {
  local arm_dir="$ART/paged"
  mkdir -p "$arm_dir"
  log "starting paged current-stack server"
  cd "$BIN"
  env LLAMA_KV_PAGED=1 LLAMA_MOE_FORCE_GRAPHS=1 GGML_NO_BACKTRACE=1 \
    ./llama-server \
      -m "$MODEL" -ngl 99 -fa on -c "$CTX" -b "$BATCH" -ub "$UBATCH" \
      --parallel "$PARALLEL" --host 127.0.0.1 --port "$LLAMA_PORT" --no-webui \
      > "$arm_dir/server.log" 2>&1 &
  SERVER_PID=$!
  wait_http "http://127.0.0.1:$LLAMA_PORT/health" "ok" "$arm_dir/server.log" "$arm_dir/health.json"
  python3 "$H2H" --url "http://127.0.0.1:$LLAMA_PORT/v1/completions" \
    --model q36 -n 8 --ptok "$PTOK" --gen 16 --nonce "warm_paged_$(date +%s)" --no-cache >/dev/null
  for n in $NPL; do
    log "paged n=$n"
    python3 "$H2H" --url "http://127.0.0.1:$LLAMA_PORT/v1/completions" \
      --model q36 -n "$n" --ptok "$PTOK" --gen "$GEN" \
      --nonce "paged_${n}_$(date +%s)" --no-cache > "$arm_dir/n${n}.json"
    cat "$arm_dir/n${n}.json" | tee -a "$ART/run.log"
  done
  kill "$SERVER_PID" >/dev/null 2>&1 || true
  wait "$SERVER_PID" >/dev/null 2>&1 || true
  SERVER_PID=""
  sleep 3
}

run_vllm() {
  local arm_dir="$ART/vllm"
  mkdir -p "$arm_dir"
  export PATH="$(dirname "$VLLM_BIN"):$PATH"
  export VLLM_LOGGING_LEVEL=${VLLM_LOGGING_LEVEL:-INFO}
  export HF_HUB_OFFLINE=${HF_HUB_OFFLINE:-1}
  log "starting vLLM server"
  nohup "$VLLM_BIN" serve "$VLLM_MODEL" \
    --served-model-name q36 --gpu-memory-utilization 0.85 --max-model-len 4096 \
    --max-num-seqs 256 --host 127.0.0.1 --port "$VLLM_PORT" --tensor-parallel-size 1 \
    > "$arm_dir/server.log" 2>&1 &
  SERVER_PID=$!
  wait_http "http://127.0.0.1:$VLLM_PORT/v1/models" "q36" "$arm_dir/server.log" "$arm_dir/models.json"
  python3 "$H2H" --url "http://127.0.0.1:$VLLM_PORT/v1/completions" \
    --model q36 -n 8 --ptok "$PTOK" --gen 16 --nonce "warm_vllm_$(date +%s)" --no-cache >/dev/null
  for n in $NPL; do
    log "vllm n=$n"
    python3 "$H2H" --url "http://127.0.0.1:$VLLM_PORT/v1/completions" \
      --model q36 -n "$n" --ptok "$PTOK" --gen "$GEN" \
      --nonce "vllm_${n}_$(date +%s)" --no-cache > "$arm_dir/n${n}.json"
    cat "$arm_dir/n${n}.json" | tee -a "$ART/run.log"
  done
  kill "$SERVER_PID" >/dev/null 2>&1 || true
  pkill -9 -u "$(id -u)" -f "[v]llm serve" >/dev/null 2>&1 || true
  wait "$SERVER_PID" >/dev/null 2>&1 || true
  SERVER_PID=""
  sleep 5
}

write_summary() {
  python3 - "$ART" <<'PY' | tee "$ART/summary.tsv"
import json
import sys
from pathlib import Path

art = Path(sys.argv[1])
rows = []
for arm in ("paged", "vllm"):
    for path in sorted((art / arm).glob("n*.json")):
        data = json.loads(path.read_text())
        rows.append((arm, data["n"], data["agg_tps"], data["decode_agg_tps"],
                     data["decode_perseq_tps"], data["prefill_tps"],
                     data["ttft_mean_ms"], data["wall_s"]))

print("arm\tn\tagg_tps\tdecode_agg_tps\tdecode_perseq_tps\tprefill_tps\tttft_mean_ms\twall_s")
for row in rows:
    print("\t".join(str(x) for x in row))

by_key = {(row[0], row[1]): row for row in rows}
print("\nratio\tn\tpaged_decode_over_vllm\tpaged_perseq_over_vllm\tpaged_agg_over_vllm\tpaged_ttft_over_vllm")
for n in sorted({row[1] for row in rows}):
    paged = by_key.get(("paged", n))
    vllm = by_key.get(("vllm", n))
    if not paged or not vllm:
        continue
    print(f"ratio\t{n}\t{paged[3]/vllm[3]:.4f}\t{paged[4]/vllm[4]:.4f}\t{paged[2]/vllm[2]:.4f}\t{paged[6]/vllm[6]:.4f}")
PY
}

write_gate_summary() {
  python3 - "$ART" "$MOE_MD5_EXPECTED" "$DENSE_MD5_EXPECTED" <<'PY' | tee "$ART/gate_summary.tsv"
import re
import sys
from pathlib import Path

art = Path(sys.argv[1])
expected = {
    "moe": sys.argv[2],
    "dense": sys.argv[3],
}
ansi = re.compile(r"\x1b\[[0-9;]*m")
bad = False

print("phase\tcheck\tstatus\tactual\texpected\tdetails")

for phase in ("pre", "post"):
    gate_dir = art / f"gate_{phase}"
    if not gate_dir.exists():
        print(f"{phase}\tall\tskipped\t\t\t{gate_dir} missing")
        continue

    for name, want in expected.items():
        md5_path = gate_dir / f"{name}.md5"
        if not md5_path.exists():
            print(f"{phase}\t{name}_md5\tmissing\t\t{want}\t{md5_path} missing")
            bad = True
            continue
        got = md5_path.read_text().split()[0]
        status = "ok" if got == want else "mismatch"
        if status != "ok":
            bad = True
        print(f"{phase}\t{name}_md5\t{status}\t{got}\t{want}\t{md5_path}")

    op_paths = sorted(gate_dir.glob("op_*.txt"))
    if not op_paths:
        print(f"{phase}\top\tmissing\t\t\tno op_*.txt files")
        bad = True
        continue

    for path in op_paths:
        op = path.stem.removeprefix("op_")
        text = ansi.sub("", path.read_text(errors="replace"))
        passed = re.search(r"(\d+)/(\d+) tests passed", text)
        backend_ok = re.search(r"Backend CUDA0:\s+OK", text)
        if passed:
            actual = f"{passed.group(1)}/{passed.group(2)}"
            status = "ok" if passed.group(1) == passed.group(2) and backend_ok else "fail"
        else:
            actual = ""
            status = "missing"
        if status != "ok":
            bad = True
        print(f"{phase}\top_{op}\t{status}\t{actual}\tall\t{path}")

if bad:
    sys.exit(6)
PY
}

if [[ -n "$SUMMARY_GATES_ART" ]]; then
  ART="$SUMMARY_GATES_ART"
  require_path "$ART"
  write_gate_summary
  exit 0
fi

require_path "$SRC"
require_path "$BIN/llama-server"
require_path "$BIN/llama-completion"
require_path "$BIN/test-backend-ops"
require_path "$MODEL"
require_path "$VLLM_MODEL"
require_path "$H2H"
require_path "$VLLM_BIN"
require_path "$HOME/paged-inference-gates.sh"

preflight
write_hardware_report
log "artifact=$ART"
log "source=$(git -C "$SRC" log --oneline -1)"

if [[ "$DRY_RUN" == "1" ]]; then
  log "dry run only; commands validated"
  log "would build: cmake --build $SRC/build-cuda --target llama-server llama-completion test-backend-ops -j8"
  log "would run paged NPL=[$NPL] PTOK=$PTOK GEN=$GEN"
  log "would run vLLM NPL=[$NPL] PTOK=$PTOK GEN=$GEN"
  exit 0
fi

log "building llama-server, llama-completion, and test-backend-ops"
cmake --build "$SRC/build-cuda" --target llama-server llama-completion test-backend-ops -j 8 \
  > "$ART/build.log" 2>&1

run_gate pre
acquire_lock
trap release_lock EXIT
run_paged
run_vllm
release_lock
trap - EXIT
run_gate post
write_gate_summary
write_summary
log "artifacts: $ART"

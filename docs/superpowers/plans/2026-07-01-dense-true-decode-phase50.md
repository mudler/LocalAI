# Phase50 Dense True Decode Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Measure dense Qwen3.5 true steady decode on GB10 for paged llama.cpp and vLLM, separated from h2h TTFT/prefill-overlap accounting, while proving inference output and touched backend ops remain unchanged before and after the run.

**Architecture:** Do not change inference code. Run canonical pre/post paged inference gates, then collect graph-node-traced nsys profiles for dense paged llama.cpp and dense vLLM using the difference method: `ntg=64 - ntg=16` at the same `npl=128`, `npp=128` shape. Record the result in the parity docs and keep the next code target limited to scheduler/admission tracing only if true steady decode does not explain the Phase47 high-N serving gap.

**Tech Stack:** DGX GB10 over `ssh dgx.casa`, llama.cpp fork build in `~/llama-phase6-source/build-cuda` for `llama-batched-bench`, vLLM 0.23.0 in `~/vllm-bench`, `nsys --cuda-graph-trace=node`, LocalAI parity docs.

---

### Task 1: Confirm DGX is idle and acquire an artifact directory

**Files:**
- Create on DGX: `~/bench/phase50_dense_true_decode/<timestamp>/preflight.txt`
- Create on DGX: `~/bench/phase50_dense_true_decode/<timestamp>/hardware.txt`
- Create on DGX: `~/bench/phase50_dense_true_decode/<timestamp>/run.log`
- Modify later: `docs/superpowers/plans/2026-07-01-dense-true-decode-phase50.md`

- [x] **Step 1: Check the DGX preflight**

Run:

```bash
ssh dgx.casa 'set -euo pipefail
ART=$HOME/bench/phase50_dense_true_decode/$(date +%Y%m%d_%H%M%S)
mkdir -p "$ART"
{
  printf "docker="; docker ps -q | wc -l
  printf "local_ai_worker="; docker ps --format "{{.Names}}" | grep -c local-ai-worker || true
  printf "compute="; nvidia-smi --query-compute-apps=pid --format=csv,noheader | sed "/^$/d" | wc -l
  printf "owner="; if [ -f "$HOME/gpu_bench_lock/owner" ]; then cat "$HOME/gpu_bench_lock/owner"; else echo FREE-no-lock-file; fi
} | tee "$ART/preflight.txt"
nvidia-smi -L | tee "$ART/hardware.txt"
nvidia-smi --query-gpu=name,driver_version,memory.total --format=csv,noheader | tee -a "$ART/hardware.txt"
echo "$ART"'
```

Expected: `docker=0`, `local_ai_worker=0`, `compute=0`, and `owner=FREE...`.

- [x] **Step 2: Acquire the owner-file lock**

Run with `ART` set to the printed artifact directory:

```bash
ssh dgx.casa 'set -euo pipefail
ART="$HOME/bench/phase50_dense_true_decode/REPLACE_WITH_TIMESTAMP"
mkdir -p "$HOME/gpu_bench_lock"
echo "codex-phase50-dense-true-decode $(date +%s)" > "$HOME/gpu_bench_lock/owner"
cat "$HOME/gpu_bench_lock/owner" | tee -a "$ART/run.log"'
```

Expected: owner starts with `codex-phase50-dense-true-decode`.

Actual artifact: `/home/mudler/bench/phase50_dense_true_decode/20260701_103120`.
Preflight was clean: docker `0`, `local-ai-worker` `0`, compute `0`, owner
`FREE released-by-codex-current-serving-snapshot 1782893824`.

### Task 2: Run pre-profile inference gates

**Files:**
- Create on DGX: `~/bench/phase50_dense_true_decode/<timestamp>/gate_pre/`
- Create on DGX: `~/bench/phase50_dense_true_decode/<timestamp>/gate_pre.log`

- [x] **Step 1: Run the canonical paged gate helper**

Run:

```bash
ssh dgx.casa 'set -euo pipefail
ART="$HOME/bench/phase50_dense_true_decode/REPLACE_WITH_TIMESTAMP"
BIN="$HOME/llama-phase6-source/build-phase36/bin" \
ART="$ART/gate_pre" \
OPS=MUL_MAT,MUL_MAT_ID \
  "$HOME/paged-inference-gates.sh" 2>&1 | tee "$ART/gate_pre.log"'
```

Expected:

```text
paged inference gates OK
```

Required values:
- MoE md5: `8cb0ce23777bf55f92f63d0292c756b0`
- Dense md5: `5951a5b4d624ce891e22ab5fca9bc439`
- `MUL_MAT`: `1146/1146`
- `MUL_MAT_ID`: `806/806`

Actual: `build-phase36` pre-gate passed, then `build-cuda` pre-gate also
passed because `build-phase36/bin` does not contain `llama-batched-bench`.
The profiled binary set is therefore `~/llama-phase6-source/build-cuda/bin`.

### Task 3: Profile dense paged llama.cpp true decode

**Files:**
- Create on DGX: `~/bench/phase50_dense_true_decode/<timestamp>/paged_dense_n128_ntg16.nsys-rep`
- Create on DGX: `~/bench/phase50_dense_true_decode/<timestamp>/paged_dense_n128_ntg16.bench.log`
- Create on DGX: `~/bench/phase50_dense_true_decode/<timestamp>/paged_dense_n128_ntg64.nsys-rep`
- Create on DGX: `~/bench/phase50_dense_true_decode/<timestamp>/paged_dense_n128_ntg64.bench.log`

- [x] **Step 1: Run ntg=16 graph-node profile**

Run:

```bash
ssh dgx.casa 'set -euo pipefail
ART="$HOME/bench/phase50_dense_true_decode/REPLACE_WITH_TIMESTAMP"
BIN="$HOME/llama-phase6-source/build-cuda/bin/llama-batched-bench"
MODEL="$HOME/bench/q36-27b-nvfp4.gguf"
REP="$ART/paged_dense_n128_ntg16"
rm -f "$REP.nsys-rep" "$REP.sqlite"
nsys profile --cuda-graph-trace=node --trace=cuda,nvtx --sample=none --cpuctxsw=none \
  --force-overwrite=true -o "$REP" \
  env LLAMA_KV_PAGED=1 LLAMA_MOE_FORCE_GRAPHS=1 GGML_NO_BACKTRACE=1 \
  "$BIN" -m "$MODEL" -c 131072 -b 2048 -ub 512 -ngl 99 -fa on \
    -npp 128 -ntg 16 -npl 128 > "$REP.bench.log" 2>&1
grep -E "model|\\| *128|llama_perf|error|Error|Traceback" "$REP.bench.log" | tail -40'
```

Expected: command exits 0 and writes `paged_dense_n128_ntg16.nsys-rep`.

Actual: `T_TG=5.754s`, `S_TG=355.93 t/s`; report
`paged_dense_n128_ntg16.nsys-rep` written.

- [x] **Step 2: Run ntg=64 graph-node profile**

Run:

```bash
ssh dgx.casa 'set -euo pipefail
ART="$HOME/bench/phase50_dense_true_decode/REPLACE_WITH_TIMESTAMP"
BIN="$HOME/llama-phase6-source/build-cuda/bin/llama-batched-bench"
MODEL="$HOME/bench/q36-27b-nvfp4.gguf"
REP="$ART/paged_dense_n128_ntg64"
rm -f "$REP.nsys-rep" "$REP.sqlite"
nsys profile --cuda-graph-trace=node --trace=cuda,nvtx --sample=none --cpuctxsw=none \
  --force-overwrite=true -o "$REP" \
  env LLAMA_KV_PAGED=1 LLAMA_MOE_FORCE_GRAPHS=1 GGML_NO_BACKTRACE=1 \
  "$BIN" -m "$MODEL" -c 131072 -b 2048 -ub 512 -ngl 99 -fa on \
    -npp 128 -ntg 64 -npl 128 > "$REP.bench.log" 2>&1
grep -E "model|\\| *128|llama_perf|error|Error|Traceback" "$REP.bench.log" | tail -40'
```

Expected: command exits 0 and writes `paged_dense_n128_ntg64.nsys-rep`.

Actual: `T_TG=21.768s`, `S_TG=376.33 t/s`; report
`paged_dense_n128_ntg64.nsys-rep` written.

### Task 4: Profile dense vLLM true decode

**Files:**
- Create on DGX: `~/bench/phase50_dense_true_decode/<timestamp>/vllm_dense_decode_prof.py`
- Create on DGX: `~/bench/phase50_dense_true_decode/<timestamp>/vllm_dense_n128_ntg16.nsys-rep`
- Create on DGX: `~/bench/phase50_dense_true_decode/<timestamp>/vllm_dense_n128_ntg16.run.log`
- Create on DGX: `~/bench/phase50_dense_true_decode/<timestamp>/vllm_dense_n128_ntg64.nsys-rep`
- Create on DGX: `~/bench/phase50_dense_true_decode/<timestamp>/vllm_dense_n128_ntg64.run.log`

- [x] **Step 1: Write the vLLM dense profile driver**

Run:

```bash
ssh dgx.casa 'set -euo pipefail
ART="$HOME/bench/phase50_dense_true_decode/REPLACE_WITH_TIMESTAMP"
cat > "$ART/vllm_dense_decode_prof.py" <<'"'"'PY'"'"'
import os, time, torch
os.environ["HF_HUB_OFFLINE"] = "1"
os.environ["VLLM_LOGGING_LEVEL"] = "WARNING"
os.environ["VLLM_ENABLE_V1_MULTIPROCESSING"] = "0"
from vllm import LLM, SamplingParams
from vllm.inputs import TokensPrompt

MODEL = os.environ.get("MODEL", "/home/mudler/bench/q36-27b-nvfp4-vllm")
NSEQ = int(os.environ.get("NSEQ", "128"))
PROMPT_TOKS = int(os.environ.get("PT", "128"))
GEN = int(os.environ.get("GEN", "64"))

llm = LLM(
    model=MODEL,
    enforce_eager=False,
    max_model_len=4096,
    gpu_memory_utilization=0.85,
    max_num_seqs=256,
    tensor_parallel_size=1,
    enable_prefix_caching=False,
    disable_log_stats=True,
)
prompts = [
    TokensPrompt(prompt_token_ids=[1000 + (i * 7 + j * 13) % 30000 for j in range(PROMPT_TOKS)])
    for i in range(NSEQ)
]
sp = SamplingParams(temperature=0.0, max_tokens=GEN, ignore_eos=True, min_tokens=GEN)
print(f"dense vLLM NSEQ={NSEQ} PT={PROMPT_TOKS} GEN={GEN} warmup...", flush=True)
llm.generate(prompts, sp, use_tqdm=False)
torch.cuda.synchronize()
print("PROFILED GENERATE START", flush=True)
torch.cuda.cudart().cudaProfilerStart()
t0 = time.time()
outs = llm.generate(prompts, sp, use_tqdm=False)
torch.cuda.synchronize()
t1 = time.time()
torch.cuda.cudart().cudaProfilerStop()
ntok = sum(len(o.outputs[0].token_ids) for o in outs)
print(f"PROFILED END seqs={len(outs)} gen_tok={ntok} wall={t1-t0:.3f}s tok/s={ntok/(t1-t0):.1f} incl_prefill", flush=True)
PY'
```

Expected: `vllm_dense_decode_prof.py` exists in the artifact directory.

Actual: used an equivalent self-contained `python -c` target under nsys instead
of writing a DGX source script. No inference code or repo file was changed.

- [x] **Step 2: Run ntg=16 graph-node profile**

Run:

```bash
ssh dgx.casa 'set -euo pipefail
ART="$HOME/bench/phase50_dense_true_decode/REPLACE_WITH_TIMESTAMP"
REP="$ART/vllm_dense_n128_ntg16"
rm -f "$REP.nsys-rep" "$REP.sqlite"
PATH="$HOME/vllm-bench/bin:$PATH" HF_HUB_OFFLINE=1 NSEQ=128 PT=128 GEN=16 \
nsys profile --cuda-graph-trace=node --capture-range=cudaProfilerApi --capture-range-end=stop \
  --trace=cuda --sample=none --cpuctxsw=none --force-overwrite=true -o "$REP" \
  "$HOME/vllm-bench/bin/python" "$ART/vllm_dense_decode_prof.py" > "$REP.run.log" 2>&1
grep -E "PROFILED|Error|error|Traceback" "$REP.run.log" | tail -20'
```

Expected: command exits 0 and writes `vllm_dense_n128_ntg16.nsys-rep`.

Actual: profiled generate `2048` tokens in `13.041s`; report
`vllm_dense_n128_ntg16.nsys-rep` written.

- [x] **Step 3: Run ntg=64 graph-node profile**

Run:

```bash
ssh dgx.casa 'set -euo pipefail
ART="$HOME/bench/phase50_dense_true_decode/REPLACE_WITH_TIMESTAMP"
REP="$ART/vllm_dense_n128_ntg64"
rm -f "$REP.nsys-rep" "$REP.sqlite"
PATH="$HOME/vllm-bench/bin:$PATH" HF_HUB_OFFLINE=1 NSEQ=128 PT=128 GEN=64 \
nsys profile --cuda-graph-trace=node --capture-range=cudaProfilerApi --capture-range-end=stop \
  --trace=cuda --sample=none --cpuctxsw=none --force-overwrite=true -o "$REP" \
  "$HOME/vllm-bench/bin/python" "$ART/vllm_dense_decode_prof.py" > "$REP.run.log" 2>&1
grep -E "PROFILED|Error|error|Traceback" "$REP.run.log" | tail -20'
```

Expected: command exits 0 and writes `vllm_dense_n128_ntg64.nsys-rep`.

Actual: profiled generate `8192` tokens in `27.165s`; report
`vllm_dense_n128_ntg64.nsys-rep` written.

### Task 5: Compute the difference-method summary

**Files:**
- Create on DGX: `~/bench/phase50_dense_true_decode/<timestamp>/summary.tsv`
- Create on DGX: `~/bench/phase50_dense_true_decode/<timestamp>/profile_files.txt`

- [x] **Step 1: Parse paged and vLLM throughput rows**

Run:

```bash
ssh dgx.casa 'set -euo pipefail
ART="$HOME/bench/phase50_dense_true_decode/REPLACE_WITH_TIMESTAMP"
python3 - "$ART" <<'"'"'PY'"'"'
import pathlib, re, sys
art = pathlib.Path(sys.argv[1])

def paged_ttg(name):
    text = (art / f"{name}.bench.log").read_text(errors="replace")
    rows = [line for line in text.splitlines() if "|   128 |" in line or "|  128 |" in line]
    if not rows:
        rows = [line for line in text.splitlines() if re.search(r"\|\s*128\s*\|", line)]
    if not rows:
        raise SystemExit(f"missing paged row in {name}.bench.log")
    parts = [p.strip() for p in rows[-1].split("|") if p.strip()]
    # columns: PP, TG, B, N_KV, T_PP, S_PP, T_TG, S_TG, T, S
    return float(parts[6]), float(parts[7])

def vllm_wall(name):
    text = (art / f"{name}.run.log").read_text(errors="replace")
    m = re.search(r"PROFILED END seqs=(\d+) gen_tok=(\d+) wall=([0-9.]+)s", text)
    if not m:
        raise SystemExit(f"missing vLLM PROFILED END in {name}.run.log")
    return int(m.group(1)), int(m.group(2)), float(m.group(3))

p16_ttg, p16_stg = paged_ttg("paged_dense_n128_ntg16")
p64_ttg, p64_stg = paged_ttg("paged_dense_n128_ntg64")
v16_seq, v16_tok, v16_wall = vllm_wall("vllm_dense_n128_ntg16")
v64_seq, v64_tok, v64_wall = vllm_wall("vllm_dense_n128_ntg64")
paged_delta_tokens = 128 * (64 - 16)
paged_delta_wall = p64_ttg - p16_ttg
vllm_delta_tokens = v64_tok - v16_tok
vllm_delta_wall = v64_wall - v16_wall
paged_decode = paged_delta_tokens / paged_delta_wall
vllm_decode = vllm_delta_tokens / vllm_delta_wall
with (art / "summary.tsv").open("w") as f:
    f.write("engine\tshape\tntg16_wall_s\tntg64_wall_s\tdelta_tokens\tdelta_wall_s\ttrue_decode_tps\n")
    f.write(f"paged\tdense_n128_pt128\t{p16_ttg:.3f}\t{p64_ttg:.3f}\t{paged_delta_tokens}\t{paged_delta_wall:.3f}\t{paged_decode:.2f}\n")
    f.write(f"vllm\tdense_n128_pt128\t{v16_wall:.3f}\t{v64_wall:.3f}\t{vllm_delta_tokens}\t{vllm_delta_wall:.3f}\t{vllm_decode:.2f}\n")
    f.write(f"ratio\tpaged_over_vllm\t\t\t\t\t{paged_decode / vllm_decode:.4f}\n")
print((art / "summary.tsv").read_text())
PY
ls -1 "$ART"/*.nsys-rep "$ART"/*.log > "$ART/profile_files.txt"'
```

Expected: `summary.tsv` contains `paged`, `vllm`, and `ratio` rows.

Actual:

| engine | ntg16 wall s | ntg64 wall s | delta tokens | delta wall s | true decode t/s |
|--------|--------------|--------------|--------------|--------------|-----------------|
| paged | `5.754` | `21.768` | `6144` | `16.014` | `383.66` |
| vLLM | `13.041` | `27.165` | `6144` | `14.124` | `435.00` |
| ratio | | | | | `0.8820` |

### Task 6: Run post-profile inference gates and release DGX

**Files:**
- Create on DGX: `~/bench/phase50_dense_true_decode/<timestamp>/gate_post/`
- Create on DGX: `~/bench/phase50_dense_true_decode/<timestamp>/gate_post.log`
- Modify later: `docs/superpowers/plans/2026-07-01-dense-true-decode-phase50.md`

- [x] **Step 1: Run the canonical paged gate helper again**

Run:

```bash
ssh dgx.casa 'set -euo pipefail
ART="$HOME/bench/phase50_dense_true_decode/REPLACE_WITH_TIMESTAMP"
BIN="$HOME/llama-phase6-source/build-cuda/bin" \
ART="$ART/gate_post" \
OPS=MUL_MAT,MUL_MAT_ID \
  "$HOME/paged-inference-gates.sh" 2>&1 | tee "$ART/gate_post.log"'
```

Expected:

```text
paged inference gates OK
```

Actual: `build-cuda` post-gate passed with MoE md5
`8cb0ce23777bf55f92f63d0292c756b0`, dense md5
`5951a5b4d624ce891e22ab5fca9bc439`, `MUL_MAT` `1146/1146`, and
`MUL_MAT_ID` `806/806`.

- [x] **Step 2: Release the owner-file lock**

Run:

```bash
ssh dgx.casa 'set -euo pipefail
ART="$HOME/bench/phase50_dense_true_decode/REPLACE_WITH_TIMESTAMP"
echo "FREE released-by-codex-phase50-dense-true-decode $(date +%s)" > "$HOME/gpu_bench_lock/owner"
cat "$HOME/gpu_bench_lock/owner" | tee -a "$ART/run.log"'
```

Expected: owner starts with `FREE released-by-codex-phase50-dense-true-decode`.

Actual: owner `FREE released-by-codex-phase50-dense-true-decode 1782895927`;
docker `0`, `local-ai-worker` `0`, compute `0`.

### Task 7: Record the result and choose the next code target

**Files:**
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md`
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md`
- Modify: `backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md`
- Modify: `docs/superpowers/plans/2026-07-01-dense-true-decode-phase50.md`

- [x] **Step 1: Mark completed plan steps**

Update every completed checkbox in this file. Leave failed or skipped steps unchecked and add a short note with the artifact path and failure reason.

- [x] **Step 2: Add the Phase50 result to the parity docs**

Record:
- artifact directory
- preflight result
- pre/post gate md5 and op-count values
- paged true decode, vLLM true decode, and ratio from `summary.tsv`
- whether Phase47 high-N serving loss is a true GPU decode gap or mostly scheduler/accounting

Actual: recorded the artifact, preflight, gates, true-decode table, and
decision in `GB10_PARITY_PHASE0_RESULTS.md`, `VLLM_PARITY_LEVER_MAP.md`, and
`PARITY_HANDOFF.md`. Interpretation: a real dense decode gap remains, but it is
about `12%`; the larger Phase47 high-N serving loss points at
scheduler/admission and prefill-overlap/accounting.

- [x] **Step 3: Commit the documentation-only result**

Run:

```bash
git status --short
git add docs/superpowers/plans/2026-07-01-dense-true-decode-phase50.md \
  backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md \
  backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md \
  backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md
git commit -m "docs(paged): record dense true decode profile" -m "Assisted-by: Codex:gpt-5"
```

Expected: commit succeeds and `.claude/` remains the only unrelated untracked path.

## Self-Review

- Spec coverage: covers inference safety via pre/post md5 and op checks, true steady decode via graph-node nsys difference method, and docs/plan phase tracking.
- Placeholder scan: no `TBD`, `TODO`, or unspecified test commands.
- Type consistency: the artifact path placeholder is consistently `REPLACE_WITH_TIMESTAMP`; replace it with the actual timestamp before running each command.

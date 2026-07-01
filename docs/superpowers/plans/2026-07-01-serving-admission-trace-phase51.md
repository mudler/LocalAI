# Phase51 Serving Admission Trace Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an opt-in llama.cpp server trace that reports serving batch admission shape so dense high-N TTFT/aggregate gaps can be separated from true GPU decode speed.

**Architecture:** Implement fork-first on `mudler/llama.cpp:localai-paged`. Keep inference behavior unchanged by gating the trace behind `LLAMA_SERVING_TRACE`. Add a small unit-tested formatter/accumulator and wire counters into `server_context_impl::pre_decode()` without changing scheduling predicates.

**Tech Stack:** llama.cpp fork, `tools/server/server-context.cpp`, CMake unit test, DGX GB10 `build-cuda`, canonical md5 and backend-op gates.

---

### Task 1: Add red unit test

**Files:**
- Modify: `/home/mudler/_git/llama.cpp/tests/CMakeLists.txt`
- Create: `/home/mudler/_git/llama.cpp/tests/test-server-admission-trace.cpp`

- [x] **Step 1: Add the test target and assertions**

Added `test-server-admission-trace.cpp`, asserting summary output includes
`steps`, `decode_only_steps`, `decode_tokens`, `prompt_tokens`,
`max_waiting_prompt_slots`, `started_prompt_slots`, `continued_prompt_slots`,
`last_n_batch`, `last_n_ubatch`, `last_prefill_budget_step`, and
`last_prefill_cap_per_slot`.

- [x] **Step 2: Verify red**

Run:

```bash
cmake -S . -B build >/tmp/llama-phase51-cmake.log
cmake --build build --target test-server-admission-trace -j2
```

Expected and observed: build failed because
`../tools/server/server-admission-trace.h` did not exist.

### Task 2: Implement opt-in trace

**Files:**
- Create: `/home/mudler/_git/llama.cpp/tools/server/server-admission-trace.h`
- Modify: `/home/mudler/_git/llama.cpp/tools/server/CMakeLists.txt`
- Modify: `/home/mudler/_git/llama.cpp/tools/server/server-context.cpp`

- [x] **Step 1: Add accumulator and formatter**

Added `server_admission_trace_step`, `server_admission_trace_totals`,
`server_admission_trace_accumulate()`, and `server_admission_trace_format()`.

- [x] **Step 2: Wire counters into `pre_decode()`**

`LLAMA_SERVING_TRACE=1` now tracks:

- decode tokens already in the batch
- prompt tokens admitted this step
- waiting prompt slots seen by the prompt-admission loop
- started and continued prompt slots that actually admitted prompt tokens
- decode-only steps
- `n_batch`, `n_ubatch`, `prefill_budget_step`, and `prefill_cap_per_slot`

The trace is printed once from `server_context_impl` destruction when enabled
and at least one step was observed.

### Task 3: Verify locally and on DGX

**Files:**
- DGX artifact: `/home/mudler/bench/phase51_serving_admission_trace/20260701_110130`

- [x] **Step 1: Run local unit and server build**

Commands:

```bash
cmake -S . -B build >/tmp/llama-phase51-cmake.log
cmake --build build --target test-server-admission-trace -j2
./build/bin/test-server-admission-trace
cmake --build build --target llama-server -j2
ctest --test-dir build -R '^test-server-admission-trace$' --output-on-failure
```

Observed: unit test passed, `llama-server` built, CTest passed.

- [x] **Step 2: Apply patch to DGX mirror and build**

Applied the local patch to `dgx:~/llama-phase6-source`, then ran:

```bash
cmake -S . -B build-cuda
cmake --build build-cuda --target test-server-admission-trace llama-server -j2
ctest --test-dir build-cuda -R '^test-server-admission-trace$' --output-on-failure
```

Observed: DGX CTest passed.

- [x] **Step 3: Run canonical inference gate**

Run:

```bash
BIN=$HOME/llama-phase6-source/build-cuda/bin \
ART=$HOME/bench/phase51_serving_admission_trace/20260701_110130/gate_post \
OPS=MUL_MAT,MUL_MAT_ID \
  $HOME/paged-inference-gates.sh
```

Observed:

- MoE md5 `8cb0ce23777bf55f92f63d0292c756b0`
- dense md5 `5951a5b4d624ce891e22ab5fca9bc439`
- `MUL_MAT` `1146/1146`
- `MUL_MAT_ID` `806/806`

### Task 4: Commit and mirror

**Files:**
- Modify later: `backend/cpp/llama-cpp-localai-paged/patches/paged/`
- Modify later: `backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md`
- Modify later: `backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md`
- Modify later: `backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md`

- [x] **Step 1: Commit on the llama.cpp fork**

Local fork commit:

```text
c6cb8460e feat(server): trace serving admission batches
```

- [ ] **Step 2: Push fork branch**

Blocked by policy: ask before every push. Do not push without explicit approval.

- [ ] **Step 3: Regenerate LocalAI patch series**

Pending until the fork branch is pushed, per the fork-first mirror invariant.

- [x] **Step 4: Record Phase51 status in LocalAI docs**

Record the fork commit, DGX artifact, gates, and pending push/mirror state.

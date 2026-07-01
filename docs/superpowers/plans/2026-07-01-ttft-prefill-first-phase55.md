# Phase55 TTFT Prefill-First Scheduler A/B Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Test a default-off scheduler A/B that prioritizes first-token admission by deferring token 2+ decode while any prompt still has not reached first token.

**Architecture:** Implement fork-first in `/home/mudler/_git/llama.cpp` on `localai-paged`. Keep default behavior unchanged. Add a tiny tested scheduler helper, wire it into `server_context_impl::pre_decode()` behind `LLAMA_TTFT_PREFILL_FIRST=1`, extend `LLAMA_SERVING_TRACE=1` with deferred-decode counters, then verify locally and on DGX with md5/op gates and a dense `n=128` A/B. Do not regenerate LocalAI patches until the fork branch is pushed with explicit approval.

**Tech Stack:** llama.cpp server scheduler, CMake unit tests, DGX GB10 `build-cuda`, `paged-inference-gates.sh`, `h2h_cli.py`.

---

### Task 1: Reconcile Current Patch State

**Files:**
- Modify later: `backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md`
- Modify later: `backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md`
- Modify later: `backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md`

- [x] **Step 1: Record current fork and mirror state**

Run:

```bash
cd /home/mudler/_git/llama.cpp
git status --short
git log --oneline -5
git rev-list --left-right --count fork/localai-paged...HEAD || true

cd /home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention
git status --short
ls backend/cpp/llama-cpp-localai-paged/patches/paged | tail
```

Expected: llama.cpp fork is clean at `bd7b2e952` before Phase55, with local
trace commits not mirrored into LocalAI patches. LocalAI worktree may still have
the unrelated untracked `.claude/`.

Observed: fork was clean at `bd7b2e952` before Phase55, and after implementation
is clean at `8a97629a4`. It is `18` commits ahead of `fork/localai-paged`.
LocalAI patches still stop at `0063`.

- [x] **Step 2: Keep mirror blocked until push approval**

Document that Phase51, Phase54, and Phase55 fork commits are local only. Do not
edit `backend/cpp/llama-cpp-localai-paged/patches/paged/*.patch` directly.

### Task 2: Add Red Scheduler Helper Test

**Files:**
- Create: `/home/mudler/_git/llama.cpp/tools/server/server-admission-policy.h`
- Create: `/home/mudler/_git/llama.cpp/tests/test-server-admission-policy.cpp`
- Modify: `/home/mudler/_git/llama.cpp/tests/CMakeLists.txt`

- [x] **Step 1: Create the failing helper test**

Add a test that calls:

```cpp
server_admission_should_defer_decode_for_ttft(false, true, 8) == false
server_admission_should_defer_decode_for_ttft(true, false, 8) == false
server_admission_should_defer_decode_for_ttft(true, true, 0) == false
server_admission_should_defer_decode_for_ttft(true, true, 1) == true
server_admission_should_defer_decode_for_ttft(true, true, 64) == true
```

- [x] **Step 2: Run red**

Run:

```bash
cd /home/mudler/_git/llama.cpp
cmake --build build --target test-server-admission-policy -j2
```

Expected: build fails because `server-admission-policy.h` or the helper does
not exist.

Observed: after reconfiguring CMake, the build failed because
`../tools/server/server-admission-policy.h` did not exist.

### Task 3: Implement Helper and Trace Counter

**Files:**
- Create: `/home/mudler/_git/llama.cpp/tools/server/server-admission-policy.h`
- Modify: `/home/mudler/_git/llama.cpp/tools/server/server-admission-trace.h`
- Modify: `/home/mudler/_git/llama.cpp/tests/test-server-admission-trace.cpp`
- Modify: `/home/mudler/_git/llama.cpp/tests/test-server-admission-policy.cpp`
- Modify: `/home/mudler/_git/llama.cpp/tests/CMakeLists.txt`

- [x] **Step 1: Add the helper**

Implement:

```cpp
static inline bool server_admission_should_defer_decode_for_ttft(
        bool enabled,
        bool prompt_waiting,
        int32_t n_decoded) {
    return enabled && prompt_waiting && n_decoded > 0;
}
```

- [x] **Step 2: Add trace counter**

Add `ttft_deferred_decode_slots` to `server_admission_trace_step` and
`server_admission_trace_totals`, accumulate it, and format it as
`ttft_deferred_decode_slots=<N>`.

- [x] **Step 3: Verify local tests**

Run:

```bash
cd /home/mudler/_git/llama.cpp
cmake --build build --target test-server-admission-policy test-server-admission-trace -j2
./build/bin/test-server-admission-policy
./build/bin/test-server-admission-trace
ctest --test-dir build -R 'test-server-admission-(policy|trace)' --output-on-failure
```

Expected: both tests pass.

Observed: both tests passed locally and under CTest.

### Task 4: Wire Default-Off Scheduler A/B

**Files:**
- Modify: `/home/mudler/_git/llama.cpp/tools/server/server-context.cpp`

- [x] **Step 1: Include the helper**

Add `#include "server-admission-policy.h"` beside the trace include.

- [x] **Step 2: Detect prompt backlog before collecting generating slots**

Before the generating-slot loop, scan slots for:

```cpp
slot.state == SLOT_STATE_STARTED || slot.state == SLOT_STATE_PROCESSING_PROMPT
```

Store this as `ttft_prompt_waiting`.

- [x] **Step 3: Defer token 2+ decode when enabled**

Inside the generating-slot loop, before touching `slot_batched`, skip slots
where:

```cpp
server_admission_should_defer_decode_for_ttft(
    ttft_prefill_first,
    ttft_prompt_waiting,
    slot.n_decoded)
```

Increment `serving_trace_step.ttft_deferred_decode_slots` for each skipped
slot when trace is enabled.

- [x] **Step 4: Verify local build**

Run:

```bash
cd /home/mudler/_git/llama.cpp
cmake --build build --target test-server-admission-policy test-server-admission-trace llama-server -j2
ctest --test-dir build -R 'test-server-admission-(policy|trace)' --output-on-failure
```

Expected: build and focused tests pass.

Observed: focused tests passed and `llama-server` built. Local UI provisioning
used the repo fallback bundle path after the local Node engine mismatch.

### Task 5: Commit Fork Patch

**Files:**
- Fork commit only

- [x] **Step 1: Commit locally**

Commit message:

```text
feat(server): add TTFT prefill-first scheduler mode
```

Include trailer:

```text
Assisted-by: Codex:gpt-5
```

Do not push without explicit approval.

Local fork commit:

```text
8a97629a4 feat(server): add TTFT prefill-first scheduler mode
```

### Task 6: DGX Verification and A/B

**Files:**
- DGX mirror: `~/llama-phase6-source`
- Artifact: `~/bench/phase55_ttft_prefill_first/<timestamp>`

- [x] **Step 1: Preflight**

Require: Docker `0`, `local-ai-worker` `0`, compute apps `0`, lock `FREE*`.

Observed: docker `0`, `local-ai-worker` `0`, compute `0`, lock
`FREE released-by-codex-phase54-hist 1782898659`, DGX mirror clean at
`2cbb61969443cf52aa1aa58eb9f5a8d7c20a7780`.

- [x] **Step 2: Apply temporary stack and build**

Apply the local fork stack to the clean DGX mirror, build
`test-server-admission-policy`, `test-server-admission-trace`, `llama-server`,
`llama-cli`, and `test-backend-ops`.

Observed: CMake reconfiguration was required for the new test target. After
reconfigure, all requested targets built and focused CTests passed on DGX.

- [x] **Step 3: Run pre and post gates**

Use:

```bash
BIN=$HOME/llama-phase6-source/build-cuda/bin \
ART=$ART/gate_pre \
OPS=MUL_MAT,MUL_MAT_ID \
  $HOME/paged-inference-gates.sh
```

Repeat for `gate_post`. Expected md5/op gates:

- MoE md5 `8cb0ce23777bf55f92f63d0292c756b0`
- dense md5 `5951a5b4d624ce891e22ab5fca9bc439`
- `MUL_MAT` `1146/1146`
- `MUL_MAT_ID` `806/806`

Observed: `gate_pre`, `gate_post`, and an extra `gate_after_ab` all matched the
expected md5/op gates.

- [x] **Step 4: Run dense A/B**

Run Phase54 baseline shape with `LLAMA_SERVING_TRACE=1`:

- `n=128`
- `ptok=168`
- `gen=64`
- `--parallel 128`
- `-c 131072 -b 2048 -ub 512`

Run variants:

- default
- `LLAMA_TTFT_PREFILL_FIRST=1`

Record h2h JSON and trace line for both.

Artifact: `/home/mudler/bench/phase55_ttft_prefill_first/20260701_114929`.

Default:

```json
{"n": 128, "reqs": 128, "gen_total": 8192, "prompt_tok_total": 22913, "gen_per_req": 64.0, "agg_tps": 138.2, "decode_agg_tps": 361.3, "decode_perseq_tps": 1.91, "prefill_tps": 626.0, "ttft_mean_ms": 23231.9, "ttft_max_ms": 36599.5, "wall_s": 59.272}
```

```text
steps=76 decode_only_steps=0 decode_tokens=8064 prompt_tokens=22913 waiting_prompt_slots=267 max_waiting_prompt_slots=34 started_prompt_slots=128 continued_prompt_slots=139 ttft_deferred_decode_slots=0 prompt_hist=0:63,1-64:1,513+:12 decode_hist=0:3,1-63:10,64-127:10,128-255:53 waiting_hist=0:63,1-7:1,8-15:2,16-31:9,32-63:1
```

`LLAMA_TTFT_PREFILL_FIRST=1`:

```json
{"n": 128, "reqs": 128, "gen_total": 8192, "prompt_tok_total": 22913, "gen_per_req": 64.0, "agg_tps": 142.9, "decode_agg_tps": 336.9, "decode_perseq_tps": 1.86, "prefill_tps": 694.2, "ttft_mean_ms": 21520.8, "ttft_max_ms": 33008.2, "wall_s": 57.323}
```

```text
steps=76 decode_only_steps=0 decode_tokens=8064 prompt_tokens=22913 waiting_prompt_slots=267 max_waiting_prompt_slots=35 started_prompt_slots=128 continued_prompt_slots=139 ttft_deferred_decode_slots=660 prompt_hist=0:63,1-64:1,257-512:1,513+:11 decode_hist=0:13,128-255:63 waiting_hist=0:63,1-7:1,8-15:3,16-31:8,32-63:1
```

- [x] **Step 5: Decide**

Accept Phase55 only if it improves TTFT without material aggregate throughput
loss, or improves aggregate throughput without TTFT collapse. Reject if it
mostly shifts cost from late prompts to already-started streams.

Decision: keep as a promising default-off A/B. On this dense shape it improved
aggregate throughput by `+3.4%`, prefill throughput by `+10.9%`, mean TTFT by
`-7.4%`, max TTFT by `-9.8%`, and wall time by `-3.3%`. Decode-agg fell by
`-6.8%`, which is expected because the policy explicitly shifts early compute
toward first-token prompt admission.

- [x] **Step 6: Revert DGX mirror**

Reverse the temporary patch stack, remove any untracked files introduced by the
patch, release the lock, and verify no GPU compute apps remain.

Observed: temporary stack reverted, trace/policy files removed from the clean
mirror, lock released as `FREE released-by-codex-phase55-ttft 1782899730`, and
no compute apps were reported.

### Task 7: Record Results

**Files:**
- Modify: `/home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention/docs/superpowers/plans/2026-07-01-ttft-prefill-first-phase55.md`
- Modify: `/home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention/backend/cpp/llama-cpp-localai-paged/docs/GB10_PARITY_PHASE0_RESULTS.md`
- Modify: `/home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention/backend/cpp/llama-cpp-localai-paged/docs/VLLM_PARITY_LEVER_MAP.md`
- Modify: `/home/mudler/_git/LocalAI/.claude/worktrees/feat+paged-attention/backend/cpp/llama-cpp-localai-paged/docs/PARITY_HANDOFF.md`

- [x] **Step 1: Mark completed steps**

Update this plan as each step completes.

- [x] **Step 2: Commit LocalAI docs**

Commit with:

```text
docs(paged): record TTFT prefill-first A/B

Assisted-by: Codex:gpt-5
```

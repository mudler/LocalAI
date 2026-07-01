# Phase54 Admission Histogram Trace Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extend the Phase51 default-off serving trace with compact per-step histograms so scheduler work can see whether the dense high-N run is dominated by a few very large prompt-admission steps, many small mixed steps, or waiting-slot tails.

**Architecture:** Keep the trace fork-first and default-off behind `LLAMA_SERVING_TRACE=1`. Add only accumulator buckets and formatter output, then temporarily apply the Phase51+Phase54 stack to the DGX mirror, bracket with canonical md5/op gates, run the Phase52-aligned dense trace, and revert the DGX mirror.

**Tech Stack:** llama.cpp fork, `tools/server/server-admission-trace.h`, CMake unit test, DGX GB10 `build-cuda`, `h2h_cli.py`, `paged-inference-gates.sh`.

---

### Task 1: Add red histogram assertions

- [x] **Step 1: Extend the focused unit test**

Added assertions to `tests/test-server-admission-trace.cpp` requiring:

- `prompt_hist=0:1,257-512:1`
- `decode_hist=128-255:2`
- `waiting_hist=1-7:2`

- [x] **Step 2: Verify red**

Observed failure before implementation:

```text
missing 'prompt_hist=0:1,257-512:1'
```

### Task 2: Implement histogram counters

- [x] **Step 1: Add bucket counters and formatting**

Added prompt-token, decode-token, and waiting-slot histograms to
`server_admission_trace_totals`. The formatter emits only nonzero buckets.

- [x] **Step 2: Verify local green**

Commands:

```bash
cmake --build build --target test-server-admission-trace -j2
./build/bin/test-server-admission-trace
ctest --test-dir build -R '^test-server-admission-trace$' --output-on-failure
cmake --build build --target llama-server -j2
```

Observed: focused unit test passed, CTest passed, and `llama-server` built. The
local UI asset build first hit a Node engine mismatch and then recovered through
the repo's downloaded UI bundle path.

### Task 3: Commit fork patch

- [x] **Step 1: Commit on the llama.cpp fork**

Local fork commit:

```text
bd7b2e952 feat(server): add admission trace histograms
```

Fork stack now has two unpushed trace commits:

- `c6cb8460e feat(server): trace serving admission batches`
- `bd7b2e952 feat(server): add admission trace histograms`

- [ ] **Step 2: Push fork branch**

Blocked by policy: ask before every push. Do not push without explicit approval.

- [ ] **Step 3: Regenerate LocalAI patch series**

Pending until the fork branch is pushed, per the fork-first mirror invariant.

### Task 4: Verify on DGX

- [x] **Step 1: Apply temporary stack and build**

Applied `/tmp/phase54-admission-trace-stack.patch` to the clean
`~/llama-phase6-source` mirror. Built `test-server-admission-trace`,
`llama-server`, `llama-cli`, and `test-backend-ops` in `build-cuda`.

DGX CTest passed:

```bash
ctest --test-dir build-cuda -R '^test-server-admission-trace$' --output-on-failure
```

- [x] **Step 2: Run canonical pre/post inference gates**

Artifact:
`/home/mudler/bench/phase54_admission_hist_trace/20260701_113201`.

Pre and post gates both matched:

- MoE md5 `8cb0ce23777bf55f92f63d0292c756b0`
- dense md5 `5951a5b4d624ce891e22ab5fca9bc439`
- `MUL_MAT` `1146/1146`
- `MUL_MAT_ID` `806/806`

- [x] **Step 3: Run dense histogram trace**

First diagnostic run used `--ptok 128` and produced `prompt_tok_total=17793`;
kept as `paged_hist/`.

The Phase52-aligned run used `--ptok 168`, matching the prior prompt envelope:

```json
{"n": 128, "reqs": 128, "gen_total": 8192, "prompt_tok_total": 22913, "gen_per_req": 64.0, "agg_tps": 138.1, "decode_agg_tps": 360.2, "decode_perseq_tps": 1.92, "prefill_tps": 626.7, "ttft_mean_ms": 23393.2, "ttft_max_ms": 36560.5, "wall_s": 59.303}
```

Trace:

```text
serving admission trace: steps=76 decode_only_steps=0 decode_tokens=8064 prompt_tokens=22913 waiting_prompt_slots=267 max_waiting_prompt_slots=34 started_prompt_slots=128 continued_prompt_slots=139 last_n_batch=2048 last_n_ubatch=512 last_prefill_budget_step=0 last_prefill_cap_per_slot=0 prompt_hist=0:63,1-64:1,513+:12 decode_hist=0:3,1-63:10,64-127:10,128-255:53 waiting_hist=0:63,1-7:1,8-15:2,16-31:9,32-63:1
```

### Task 5: Clean up and decide

- [x] **Step 1: Revert temporary DGX stack**

Reverted the temporary patch stack and removed the two untracked trace files it
created on the DGX mirror. Final source tree was clean.

Final DGX state:

- Docker containers: `0`
- GPU compute apps: `0`
- Lock: `FREE released-by-codex-phase54-hist 1782898659`

- [x] **Step 2: Record decision**

The histogram shows the default scheduler spends `63/76` steps with no prompt
tokens and no waiting prompts, then admits prompt work in a small number of very
large prompt chunks (`prompt_hist=513+:12`). Decode remains mostly full-width
(`decode_hist=128-255:53`) and there are still no pure decode-only steps. Static
budget shrinkage is already rejected; the next scheduler A/B should target
first-token admission or prompt-front loading, not lower global batch budgets.

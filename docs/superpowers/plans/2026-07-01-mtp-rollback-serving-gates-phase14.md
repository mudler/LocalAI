# MTP Rollback and Serving Gates Phase 14 Plan

> **For agentic workers:** keep checkboxes current while executing. This phase
> is safety-gated and must not claim an MTP parity win.

**Goal:** prove that MTP speculative decode can reject drafts without corrupting
Qwen3.6 paged KV or recurrent GDN state.

**Design:** `docs/superpowers/specs/2026-07-01-mtp-rollback-serving-gates-design.md`

## Required Safety Gates

- DGX must have no running docker containers, no `local-ai-worker`, no GPU
  compute PIDs, and a free or absent `~/gpu_bench_lock/owner`.
- Use `/home/mudler/llama-phase6-source` on DGX and keep it clean unless a
  source patch is explicitly required.
- Do not benchmark MTP as a parity win in this phase.
- Do not enable MTP by default in LocalAI or llama-server.

## Task 1: Preflight and Existing Rollback Gate

- [x] **Step 1: Confirm DGX is free**

  Result:

  ```text
  docker=0
  local_ai_worker=0
  compute=0
  FREE released-by-codex-phase6-mmq-grid 1782860601
  ```

- [x] **Step 2: Run recurrent rollback test on actual MoE GGUF**

  Command:

  ```bash
  ssh dgx.casa 'cd /home/mudler/llama-phase6-source/build-cuda &&
    cmake --build . --target test-recurrent-state-rollback -j 8 &&
    ./bin/test-recurrent-state-rollback \
      -m /home/mudler/bench/q36-35b-a3b-nvfp4.gguf \
      -ngl 99 -fa on -c 4096 -b 64 -ub 64 \
      > /home/mudler/bench/phase14_mtp_rollback/recurrent_rollback.out \
      2> /home/mudler/bench/phase14_mtp_rollback/recurrent_rollback.err'
  ```

  Current evidence from the same command family:

  - Artifact:
    `/home/mudler/bench/phase14_mtp_rollback/recurrent_rollback.err`.
  - Result:
    `main : recurrent rollback checkpoint restored successfully`.

## Task 2: MTP Greedy-Equivalence Gate

- [x] **Step 1: Build required binaries**

  Build `llama-completion`, `llama-speculative-simple`, and
  `test-recurrent-state-rollback`.

- [x] **Step 2: Run baseline greedy completion**

  Save stdout/stderr and md5 under
  `/home/mudler/bench/phase14_mtp_rollback/greedy_baseline.*`.

  Additional raw text-generation baselines were saved under
  `/home/mudler/bench/phase14_mtp_rollback/completion_nocnv_n{8,16,24,32,48}.*`
  because `llama-completion` defaults to conversation mode for this model unless
  `-no-cnv` is passed.

- [x] **Step 3: Run MTP speculative completion with the same prompt/seed**

  Use:

  - `--spec-type draft-mtp`
  - `--spec-draft-model /home/mudler/bench/q36-35b-a3b-nvfp4.gguf`
  - `--spec-draft-ngl 99`
  - `--spec-draft-n-max 3`
  - `--temp 0 --seed 1`

  Save stdout/stderr and md5 under
  `/home/mudler/bench/phase14_mtp_rollback/mtp_greedy_equiv.*`.

- [x] **Step 4: Compare outputs**

  Exact transcript md5 is not a valid cross-frontend comparator here:

  - `llama-speculative-simple --spec-type none` is not a working no-draft
    baseline; it still tries to load an empty draft model and exits with
    `failed to load draft model, ''`.
  - `--spec-draft-n-max 0` is not a no-draft baseline either; the recorded run
    still drafted and accepted tokens (`n_drafted=17`, `n_accept=17`).
  - `llama-speculative-simple` counts/emits accepted token groups, so the same
    `-n` can produce a longer raw completion than `llama-completion -no-cnv`.

  Normalized raw-output prefix gate passed for `n=8,16,24,32,48`; no run showed
  a first differing token. The MTP output had the `llama-completion -no-cnv`
  output as a prefix in each case. The `n=32` MTP artifact was
  `/home/mudler/bench/phase14_mtp_rollback/mtp_greedy_equiv.out`.

## Task 3: MTP Partial-Rejection Gate

- [x] **Step 1: Confirm rejection occurred**

  Parse MTP stderr and require:

  - `n_drafted > 0`
  - `n_accept >= 0`
  - `n_drafted > n_accept`

  Result from `/home/mudler/bench/phase14_mtp_rollback/mtp_greedy_equiv.err`:

  ```text
  n_drafted = 39
  n_accept  = 20
  accept    = 51.282%
  ```

- [x] **Step 2: Confirm no backend sampler error**

  Fail if stderr contains:

  ```text
  backend sampling requires at most one output token per sequence
  ```

  Result: absent from the MTP stderr. The expected warning was present instead:
  `backend draft sampling is disabled for MTP`.

- [x] **Step 3: Record whether bounded recurrent rollback is active**

  Record `n_rs_seq` or the log line showing bounded partial sequence removal.

  Result from `/home/mudler/bench/phase14_mtp_rollback/mtp_greedy_equiv.err`:

  ```text
  common_context_can_seq_rm: the context supports bounded partial sequence removal
  ```

## Task 4: Standard Inference Gates

- [x] **Step 1: Run paged inference gate helper**

  Run:

  ```bash
  /tmp/paged-inference-gates.sh
  ```

  Expected:

  - MoE md5 `8cb0ce23777bf55f92f63d0292c756b0`.
  - Dense md5 `5951a5b4d624ce891e22ab5fca9bc439`.
  - `MUL_MAT_ID` `806/806`.

  Result:

  ```text
  moe md5 OK: 8cb0ce23777bf55f92f63d0292c756b0
  dense md5 OK: 5951a5b4d624ce891e22ab5fca9bc439
    806/806 tests passed
    Backend CUDA0: OK
  paged inference gates OK
  artifacts: /home/mudler/bench/paged_inference_gates/20260701_041117
  ```

## Task 5: Disposition

- [x] **Step 1: If all gates pass**

  Update:

  - `GB10_PARITY_PHASE0_RESULTS.md`
  - `VLLM_PARITY_LEVER_MAP.md`
  - `PARITY_HANDOFF.md`

  Record that MTP rollback safety is green and Phase 15 can be a serving/API
  benchmark, still default-off.

- [x] **Step 2: If any gate fails**

  Stop before performance benchmarking, save artifacts, and either implement a
  narrow fork-first fix or record the failed gate as a blocker for MTP parity.

  Reviewed and not taken. The original exact-md5 wording was too strict for
  this example harness, but there was no token divergence after raw-output
  normalization. Do not add a production source patch in Phase 14. Carry the
  frontend/token accounting finding into Phase 15 and benchmark serving only
  behind the same canonical inference gates.

## Self-Review

- No placeholders remain.
- Scope is limited to rollback and greedy-equivalence safety.
- Phase 14 does not claim or benchmark speed parity.

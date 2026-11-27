# llama.cpp patches

The llama-cpp backend builds the llama.cpp commit pinned in `Makefile`
(`LLAMA_VERSION`). `prepare.sh` applies every `patches/*.patch` file to that
checkout, in name order, before the build. A patch that does not apply stops
the build, so a llama.cpp bump fails loudly on the bump pull request.

This file is for the maintainer who bumps llama.cpp and has to keep the
patches alive. Other files in `patches/` are not applied.

## The patches

| Patch | What it does |
|---|---|
| `0001-add-server-task-type-score.patch` | The `SERVER_TASK_TYPE_SCORE` task (log-probabilities of candidate continuations), used by the `Score` RPC. |
| `0002-paged-serving.patch` | Hooks of the paged serving mode: admission, preemption, resume by recompute, and the decode-first prefill budget. |

## Rules for 0002

- Scope: server scheduling policy and small getters. The hooks live in
  `tools/server/server-context.cpp`, the four option fields in
  `common/common.h`. A hunk in any other file needs the maintainer's approval.
- No kernels, no model code, no ggml API changes, no environment variables, no
  instrumentation.
- The arithmetic lives in `serving_policy.h` (LocalAI-owned, tested by
  `serving_policy_test.cpp`). `prepare.sh` copies it to `tools/server/` before
  the build. The hooks only collect slot state and act on the decisions.
- Every hook runs only when `params_base.paged_serving` is set, or, for the
  prefill budget, when `params_base.prefill_budget > 0`. With the fields off
  (`paged_kv:false`, `prefill_budget:0`), the server behaves exactly as
  upstream. Keep it that way: no moved statements, no changed upstream
  defaults. The fields default to off in `common.h`; LocalAI turns them on in
  `grpc-server.cpp` (`serving_policy::resolve_paged`,
  `default_prefill_budget`), so a plain `llama-server` built from the patched
  tree is unchanged.
- `grpc-server.cpp` checks `LOCALAI_HAS_SERVING_POLICY` (a `prepare.sh` probe
  for `paged_serving` in `common.h`). Forks that drop `patches/` (bonsai,
  turboquant, olala) still build and run without paged serving. They warn
  only when a model sets `paged_kv:true` or `prefill_budget`.

## Hook reference

Line numbers drift with every bump. Find each hook by the grep anchor.

| Hook | Anchor (grep) | What it does |
|---|---|---|
| H1 | `kv_unified_per_slot = 0;` in `common.h` | Fields `paged_serving`, `paged_preempt`, `kv_output_reserve`, `prefill_budget`. |
| H2 | `paged_n_cells`, `paged_used`, `paged_src` in `copy_prompt_to` and `copy_state_to` | Cells a slot holds: `llama_memory_seq_pos_max - seq_pos_min + 1`, or its prompt length if larger. A slot that copied another slot's prompt records the source and the cell count; `paged_used` counts those shared cells once while the source still runs the parent task (`serving_policy::unique_cells`). |
| H3 | `paged_admit`, called at `if (slot.state == SLOT_STATE_PROCESSING_PROMPT \|\| slot.state == SLOT_STATE_STARTED)` | A new prompt starts only when the pool has room for it plus the output reserve, after the claims of the running slots. Otherwise the slot stays in `SLOT_STATE_STARTED` and asks again next step. Idle or waiting slots lose their cached cells first (`paged_purge`). First come, first served by task id, so a preempted request goes before newer ones. A resumed request needs only the part of its reserve it has not generated. With the RAM prompt cache, the launch path saves the slot's old prompt and sets `paged_cache_load`; the cache is loaded here, at admission, so a waiting slot holds no cells. |
| H4 | `n_fill_max` before `next, batch any pending prompts` | Decode-first prefill budget: the prompt-fill loops stop at `n_fill_max` instead of `n_batch`. Equal to `n_batch` when the budget is off. A negative `prefill_budget` asks for the adaptive budget (`serving_policy::step_prefill_budget`, from the number of generating slots). |
| H5 | `paged_preempt_pass`, called after `batch.clear()` in `pre_decode()` | When decode plus the rest of the admitted prompts need more cells than the pool has, drop the cached cells of idle or waiting slots. With `paged_preempt`, then preempt the youngest running slot until the step fits or one slot is left. A request preempted `paged_max_preemptions` times (2) is skipped while another can go. |
| H6 | `paged_resume`, `paged_resume_fill` | A preempted slot keeps its text tokens in `paged_resume`, loses its cells, waits for admission, decodes the tokens again without outputs, then generates on. Its sampler and grammar keep their state. |
| H7 | `paged_attach`, called before `pos_next = slot.prompt.tokens.pos_next(n_past)` in the `SLOT_STATE_STARTED` branch | Prefix sharing: when another slot (busy or idle) holds a longer prefix of the new prompt, at least `paged_min_attach` (256) tokens, the slot drops its own cells and `seq_cp`s that prefix. Off for SWA, recurrent and hybrid memory, multimodal, draft models, aLoRA, shared-prefix children and `n_cache_reuse`. |

A slot that cannot be resumed (speculative decoding or MTP, a multimodal
prompt, a score parent or child, recurrent or hybrid memory, aLoRA) is
preempted only when no resumable slot is left. It fails with the retryable
error "KV pool full, preempted; retry the request" (HTTP 503 class). Each
such abort logs `aborts = N`; each recompute logs `preemptions = N`; each
admission wait logs `waits = N`.

## Upstream invariants the hooks rely on

Check each one after a bump. If one no longer holds, redesign the hook rather
than force the old hunk in.

1. Slot lifecycle `STARTED -> PROCESSING_PROMPT -> DONE_PROMPT -> GENERATING`.
   A slot in `STARTED` has not touched `slot.stats` (`update_prompt_start`
   asserts on a second call), so H3 can return before it.
2. `pre_decode()` calls `batch.clear()`, then scans the generating slots and
   appends their sampled (and draft) tokens, then fills prompts. H5 runs
   between `batch.clear()` and that scan; H4 reads `batch.size()` as the
   decode load.
3. A generating slot's `prompt.tokens` holds every decoded token; the last
   sampled token (`slot.sampled`) is not in it until
   `handle_last_sampled_token()`. H6 relies on this to rebuild the KV cells.
4. `send_error()`, `release()` and `prompt_clear()` free a slot and its cells.
   `try_clear_idle_slots()` clears one idle slot per call.
5. `slot.task` is `const`: H6 keeps the resume buffer on the slot, not in the
   task.
6. The end-of-prompt branch (`slot.prompt.n_tokens() == slot.task->n_tokens()`)
   calls `init_sampler()` and `set_output()`. H6 bypasses it on purpose: no
   logits are needed, and `init_sampler()` would reset the sampler.
7. `post_decode()` skips a slot whose `i_batch` is outside the batch. H6 sets
   `i_batch = -1` when it preempts.
8. The decode-failure branch in `decode()` ("Context size has been exceeded")
   is untouched. It stays the last safety net.
9. With the unified KV cache, `find_slot()` places tokens in any free cells
   (no contiguous run needed), so a count of free cells is enough.
10. Prompt copies between slots go through `server_slot::copy_prompt_to()` and
    `copy_state_to()` (`seq_cp`), and a child task's `id_parent` is the task id
    of the slot it copied from. H2 hooks both to count shared cells once.
11. The launch path calls `update_prompt_cache()` (save the old prompt, load the
    best cache match) under `plan.update_cache`. Paged serving splits it: save
    at launch, `prompt_load()` at admission.

## Re-porting after a bump

1. Run `prepare.sh` on the new pin. Note which hunk of 0002 fails.
2. Find the new place with the grep anchors above. Re-check the invariants of
   that hook.
3. Edit a checkout of the pin with 0001 applied, then regenerate the patch with
   `git diff` from that tree (paths `a/` and `b/`, `-p1`).
4. Prove it, in order:
   - `bash backend/cpp/run-unit-tests.sh` (policy unit test).
   - Flag-off parity: the same greedy requests, stock pin and patched pin with
     the options off, must give identical text.
   - Overcommit: `paged_kv` with a small pool and more requests than fit, all
     requests complete (`make test-extra-backend-llama-cpp-paged`).
   - Budget: with `prefill_budget:256`, the longest decode gap during a long
     prompt drops (`tests/serving-bench`, workload `mixed`).
   - Parity with preemption: greedy outputs of preempted streams match the
     unpreempted run, except at near ties (top-1 margin below 0.1 nats).
   - Bench smoke against the last recorded numbers.
5. Record the result in the bump pull request.

## Upstream watch

When one of these lands upstream, part of 0002 can go. The probe is a grep on
the new pin.

| Upstream item | Probe | When it lands | Re-benchmark |
|---|---|---|---|
| ggml-org/llama.cpp#30970 `--slot-attach` (open) | `slot_attach` in `common/common.h` | Map shared-prefix serving to it; make H2 count unique cells; re-check victim choice | shared, overcommit, parity |
| ggml-org/llama.cpp#32358 shared prefix across slots (open) | `copy_prefix_to` or `shares_cells` in `tools/server` | Same as #30970, whichever merges first | shared, overcommit |
| ggml-org/llama.cpp#10718 chunked prefill (open) | `n_chunk_pp` or a new budget field in `common.h` | Delete H4, map `prefill_budget` to the upstream field | mixed, budget arms vs upstream |
| Preemption TODO in `decode()` ("try to terminate only the largest active slot") | the TODO is gone, or a new preempt symbol | Delete H5 and H6, keep H3 if upstream has no admission, map `paged_preempt` | overcommit-preempt, parity |
| ggml-org/llama.cpp#32520 quantized-KV `kv_rows` on CUDA (open) | `kv_rows` in `ggml/src/ggml-cuda/fattn-vec*` | No hunk change; update the docs; add a q8_0 KV arm | steady, single on GPU |
| ggml-org/llama.cpp#24124 `--kv-unified-per-slot` (merged) | `kv_unified_per_slot` in `common.h` (`HAS_KV_UNIFIED_PER_SLOT`) | Nothing to do; H3 respects `n_ctx_slot()` | overcommit with a per-slot cap |

`upstream-watch.sh [llama.cpp dir]` runs these probes on a checkout and prints
`LANDED` or `absent` for each item. Run it on the bump pull request's pin.
Running it from the bump workflow and commenting on the pull request is a
follow-up.

## Known limits

- Hybrid and recurrent models, speculative decoding and MTP are not resumable.
  Admission and the prefill budget work for them; preemption aborts them only
  as a last resort.
- Shared cells are counted once only for prompt copies between slots
  (parallel completions, shared prompt prefixes) while the source runs the
  parent task. After the parent moves on, the child's copy counts as its own:
  safe, slightly conservative. Score forks live within one step and are not
  counted.
- A resumed stream recomputes its KV cells in prompt-sized batches, which
  round differently from the token-by-token decode that first wrote them.
  Greedy output can then change at a near tie (top-1 margin below about
  0.1 nats). Requests that are not preempted are not affected.
- A preempted request still waits until an older running request frees
  enough cells; under a full pool that can take as long as one request's
  generation. The backend logs each wait (`resuming after waiting`).
- Defaults: paged serving uses 8 slots when `parallel` is unset. The KV cache
  is shared and does not grow, but per-slot state does (sampler; recurrent
  state and checkpoints of hybrid and recurrent models).
- Waiting requests keep their slot. `parallel` is the limit of requests in
  flight, waiting or running.
- Vulkan, SYCL and ROCm read K/V through the dense-mask fallback: correct,
  slower than CPU, CUDA and Metal.

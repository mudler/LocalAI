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
| `0002-paged-serving.patch` | Hooks of the paged serving mode: admission, preemption, resume by recompute, the decode-first prefill budget, prefix sharing between live slots, and load-adaptive drafting. |
| `0003-server-request-priority.patch` | Backport of ggml-org/llama.cpp#32219: `server_task::priority` and the deferred-queue pick by priority. Drop it when the pin contains #32219. |
| `0004-paged-admission-order.patch` | Order of waiting requests (class with aging, uncached tokens, arrival) at the deferred queue, the admission wait and the prompt fill; the per-request prefill cap; idle-slot eviction least recently used first with save and tail trim; lazy idle-slot save. |
| `0005-grammar-repetition-limit.patch` | The limit of the GBNF repetition check in `src/llama-grammar.cpp` comes from `LLAMA_GRAMMAR_MAX_REPETITION` when it is set to a positive number (upstream: a fixed 2000). `grpc-server.cpp` sets it from the `grammar_max_repetition` option, 100000 by default (`grammar_engine.h`). Drop it when the pin can configure the limit (ggml-org/llama.cpp#20867 and the PRs linked there). |

## Rules for 0002

- Scope: server scheduling policy and small getters. The hooks live in
  `tools/server/server-context.cpp`, the option fields in `common/common.h`.
  0003 and 0004 also touch `server-task.h` (the priority, queue time and
  uncached-token fields) and `server-queue.*` (the deferred-queue pick), as
  approved by the maintainer. Any other file needs the maintainer's approval.
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
| H1 | `kv_unified_per_slot = 0;` in `common.h` | Fields `paged_serving`, `paged_preempt`, `kv_output_reserve`, `prefill_budget`, `prefill_long`. |
| H2 | `paged_n_cells`, `paged_used`, `paged_src` in `copy_prompt_to` and `copy_state_to` | Cells a slot holds: `llama_memory_seq_pos_max - seq_pos_min + 1`, or its prompt length if larger. A slot that copied another slot's prompt records the source and the cell count; `paged_used` counts those shared cells once while the source still runs the parent task (`serving_policy::unique_cells`). |
| H3 | `paged_admit`, called at `if (slot.state == SLOT_STATE_PROCESSING_PROMPT \|\| slot.state == SLOT_STATE_STARTED)` | A new prompt starts only when the pool has room for it plus the output reserve, after the claims of the running slots. Otherwise the slot stays in `SLOT_STATE_STARTED` and asks again next step. Idle or waiting slots lose their cached cells first (`paged_purge`). First come, first served by task id, so a preempted request goes before newer ones. A resumed request needs only the part of its reserve it has not generated. With the RAM prompt cache, the launch path saves the slot's old prompt and sets `paged_cache_load`; the cache is loaded here, at admission, so a waiting slot holds no cells. |
| H4 | `n_fill_max` before `next, batch any pending prompts` | Decode-first prefill budget: the prompt-fill loops stop at `n_fill_max` instead of `n_batch`. Equal to `n_batch` when the budget is off. A negative `prefill_budget` asks for the adaptive budget (`serving_policy::step_prefill_budget`, from the number of generating slots). | Each slot stops at `n_fill_lim`: `n_fill_max` when `serving_policy::budget_applies(task n_tokens, prefill_long)`, else `n_batch`, so prompts up to `prefill_long` tokens are read whole.
| H5 | `paged_preempt_pass`, called after `batch.clear()` in `pre_decode()` | When decode plus the rest of the admitted prompts need more cells than the pool has, drop the cached cells of idle or waiting slots. With `paged_preempt`, then preempt a running slot until the step fits or one slot is left: the one holding the fewest cells, the youngest on a tie; a request preempted `paged_max_preemptions` times (2) is skipped while another can go. A preempted request is admitted again only with room for its tokens plus `resume_need`'s spare (twice the reserve, at least 256 cells). Without `paged_preempt` an overflow reaches the upstream decode failure, which fails every in-flight stream with HTTP 500: keep it on. |
| H6 | `paged_resume`, `paged_resume_fill`, `paged_ckpt_at` | A preempted slot keeps its text tokens in `paged_resume`, loses its cells, waits for admission, decodes the tokens again without outputs, then generates on. Its sampler and grammar keep their state. Recurrent and hybrid memory (`seq_rm` FULL or RS) resume the same way: the full removal frees the recurrent cell and the recompute from position 0 rebuilds it. For those, and for SWA, the recompute stops once 4 tokens before the end of the original prompt to make the context checkpoint the upstream prompt path would have made, so the next turn can reuse the prefix. |
| H7 | `paged_attach`, called before `pos_next = slot.prompt.tokens.pos_next(n_past)` in the `SLOT_STATE_STARTED` branch | Prefix sharing: when another slot (busy or idle) holds a longer prefix of the new prompt, at least `paged_min_attach` (256) tokens, the slot drops its own cells and `seq_cp`s that prefix. Off for SWA, recurrent and hybrid memory, multimodal, draft models, aLoRA, shared-prefix children and `n_cache_reuse`. |
| H8 | `spec_paused_now` in `pre_decode()` | Drafting pauses when more than `spec_max_streams` slots decode and resumes with hysteresis (`serving_policy::spec_paused`). n-gram drafting (no draft model) is resumable: the tables are rebuilt after a resume. |
| H9 | `pop_deferred_task` in `server-queue.cpp` (0003, 0004) | The freed slot goes to the deferred task with the highest priority (#32219); with `paged_order`, by `serving_policy::goes_before` (class with aging, overtake cap, fewest uncached tokens, arrival). `n_uncached_hint` is set before a task is deferred. |
| H10 | `paged_fill_order`, `fill_slot` | With `paged_order`, the prompt fill visits waiting and prefilling slots by the same key, parents before children; `paged_admit` uses the key instead of the task id. `prefill_cap` limits one request's prompt tokens per step while others read prompts. |
| H11 | `paged_trim_idle`; `cache_idle_slots()` after a launch | When the pool is short, the least recently used idle slot is saved to the RAM prompt cache once and trimmed from the tail, only as much as needed (at least `paged_min_trim`). With `paged_lazy_save`, the save-and-clear of idle slots after each launch is skipped. |

Draft models and MTP resume too: the recompute batches go through
`common_speculative_process()` like any target batch, so the draft KV is
rebuilt with the target's; at load a draft context smaller than the target
context is logged, as the draft would fill up first.

A slot that cannot be recomputed (a multimodal prompt, aLoRA) is saved to a
RAM stash instead (`paged_stash`, `paged_unstash`): its sequence state
(`llama_state_seq_get_data_ext`, target and draft), its prompt and its slot
state are kept on the slot, its cells are freed, and at admission the state
is set back (`llama_state_seq_set_data_ext`) and the slot goes on from the
state it was in. The stash of all slots is capped at `cache_ram`
(`serving_policy::stash_fits`; -1 no limit, 0 no stash). Score requests are
never victims: they are short forks of one prompt. A slot that neither
recomputes nor fits the stash (the parent or a child of a shared prompt, a
full stash) is
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
| ggml-org/llama.cpp#30970 `--slot-attach` (open, does not apply to the pin) | `slot_attach` in `common/common.h` | Drop H7, map `slot_attach` to it; keep H2's shared-cell count in step with it; re-check victim choice | shared, overcommit, parity |
| ggml-org/llama.cpp#32358 shared prefix across slots (open draft, does not apply to the pin) | `copy_prefix_to` or `shares_cells` in `tools/server` | Same as #30970, whichever merges first | shared, overcommit |
| ggml-org/llama.cpp#32219 request priority (open) | `int priority` in `tools/server/server-task.h` at the pin | Drop 0003; 0004 keeps its own ordering on top | hol, collide |
| ggml-org/llama.cpp#10718 chunked prefill (open) | `n_chunk_pp` or a new budget field in `common.h` | Delete H4, map `prefill_budget` to the upstream field | mixed, budget arms vs upstream |
| Preemption TODO in `decode()` ("try to terminate only the largest active slot") | the TODO is gone, or a new preempt symbol | Delete H5 and H6, keep H3 if upstream has no admission, map `paged_preempt` | overcommit-preempt, parity |
| ggml-org/llama.cpp#32520 quantized-KV `kv_rows` on CUDA (open) | `kv_rows` in `ggml/src/ggml-cuda/fattn-vec*` | No hunk change; update the docs; add a q8_0 KV arm | steady, single on GPU |
| ggml-org/llama.cpp#24124 `--kv-unified-per-slot` (merged) | `kv_unified_per_slot` in `common.h` (`HAS_KV_UNIFIED_PER_SLOT`) | Nothing to do; H3 respects `n_ctx_slot()` | overcommit with a per-slot cap |

`upstream-watch.sh [llama.cpp dir]` runs these probes on a checkout and prints
`LANDED` or `absent` for each item. Run it on the bump pull request's pin.
Running it from the bump workflow and commenting on the pull request is a
follow-up.

Paged serving switches itself off at load for a purely recurrent model
(`llama_model_is_recurrent` and not `llama_model_is_hybrid`): such a model
keeps one state per slot and no KV pool, so counting tokens against
`n_ctx` would only make requests wait.

## Known limits

- Multimodal and aLoRA requests resume from the RAM stash, which holds the
  whole sequence state in host memory (for example about 56 MiB for 512
  tokens of Qwen3-0.6B) up to `cache_ram`, on top of the prompt cache. The
  parent or a child of a shared prompt (`n` > 1 completions) is not stashed
  and still fails with the retryable error as a last resort.
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

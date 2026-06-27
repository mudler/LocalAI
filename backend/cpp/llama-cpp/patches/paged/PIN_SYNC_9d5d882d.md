# Pin-sync: paged patch-stack -> llama.cpp 9d5d882d

Status: COMPLETE. The paged patch-stack (0001-0024) was rebased onto llama.cpp
`9d5d882d`, rebuilt clean (CUDA sm_121), and the bit-exact gate is GREEN on both
the dense and MoE NVFP4 baselines. The LocalAI-side `.patch` files were then
re-exported from the rebased commits; **4 patch files changed** and are updated
in this commit. A quick decode bench confirms the patchset performs the same on
the new tip.

## Early-warning canary: when to run the NEXT pin-sync

The shipped pin (this file's tip, mirrored in
`backend/cpp/llama-cpp-localai-paged/Makefile`) is advanced ONLY by this manual,
GPU-verified PIN_SYNC. Because the paged backend is excluded from the nightly
auto-bumper (`.github/workflows/bump_deps.yaml`), nothing nightly tells you when
upstream has drifted past the patches. That signal comes from a dedicated
scheduled canary:

- **Workflow:** `.github/workflows/llama-cpp-paged-canary.yml` (weekly, plus
  `workflow_dispatch`). It resolves the latest `ggml-org/llama.cpp` master tip,
  then in two jobs (a) APPLIES the full series to that tip with the build's own
  `git apply` method via `.github/scripts/paged-canary-apply.sh`, and (b)
  COMPILES the paged backend (cublas) against it using the same base-grpc-cuda-12
  toolchain + `make grpc-server` target the shipped build uses.
- **Green** = the series still applies and compiles on upstream HEAD; nothing to
  do.
- **Red** = upstream moved out from under the patches. **Canary red -> run a
  PIN_SYNC** (rebase the patches onto the new tip, pass the bit-exact gate on the
  GPU, re-export the `.patch` files, then advance the pin). The canary is
  signal-only: it opens no PR and never moves the pin, so the shipped build and
  the dep-bump PRs stay green regardless.
- **0019 handling:** the canary apply helper excludes ONLY the stray
  `SSM_DECODE_FIX_RESULTS.md` dev-doc hunk (the pre-existing quirk documented in
  the "Pre-existing finding" section below and in `PIN_BUMP_APPLY_CHECK.md`),
  applying 0019's real code hunks atomically. So that benign quirk never
  false-positives the canary, but a genuine code break in 0019 still turns it
  red.

## Upstream jump

- OLD LocalAI pin: `8be759e6`
- NEW LocalAI pin (target): `9d5d882d` ("model : Add label for LFM2.5-230M (#25008)")
- Upstream jump `8be759e6..9d5d882d` = **17 commits**.

### Note on the dev-tree base (important)
The DGX dev tree's `paged` branch was NOT based on the old pin `8be759e6`. Its
real base (merge-base of `paged` with both pins) is `f3e1828`
("mtmd: llava_uhd should no longer use batch dim (#24732)"), which is an ancestor
of `8be759e6` by 92 commits. So the rebase traversed `f3e1828..9d5d882d` =
**109 upstream commits**, a strictly larger surface than the 17-commit pin bump.
The end state (paged patches on `9d5d882d`) is identical either way; the larger
traverse only means the conflict surface was the worst case, and it still came
through bit-exact.

## Rebase

- Command: `git rebase --onto 9d5d882d f3e1828 paged` (merge.conflictStyle=diff3).
- 26 commits replayed (24 shipped patch-commits + the 2 dev-scaffolding "Gate-0/
  FA-gate driver" commits and 1 docs commit; the scaffolding/docs commits are not
  shipped as `.patch` files).
- Backup ref before rebase: `paged-prerebase-backup` = `a8a9d12` (old patch 0024).
- New rebased range: `9d5d882d..paged`, HEAD = `2ee65c2` (patch 0024).

### Conflicts during rebase (3 commits, ALL in `tools/server/server-context.cpp`)

Every rebase conflict was in the llama-server continuous-batch scheduler wiring,
all of which is gated behind env (`LLAMA_KV_PAGED` / `LLAMA_PREFILL_BUDGET` /
`LLAMA_MAX_BATCH_TOKENS`) and therefore a strict no-op for the gate (the gate
uses `llama-completion`, not the server, with no env set). The root cause was a
single upstream refactor of `update_slots()`:

- the outer slot loop became `iterate(slots, [&](server_slot & slot){...})`,
  replacing bottom-of-loop `break` with a top-of-lambda
  `if (!add_ok || batch.size() >= n_batch) return;` (the `add_ok` flag is set
  false on `batch.add()` failure);
- the embedding/rerank early-exits changed `continue;` -> `return;`;
- the `server_batch` token count accessor was renamed `batch.n_tokens` ->
  `batch.size()` (`server_batch` has a `.size()` method and **no** `.n_tokens`
  member; the raw `llama_batch` in `send_embedding`/`send_rerank` keeps `.n_tokens`).

**patch 0008** (`240758e`, cross-request prefix share) - 1 conflict.
Hunk 3 (the prefix-commit block) collided with the `continue`->`return` refactor.
Hunks 1 (namespace shim) and 2 (the share block) applied cleanly. Resolved by
keeping HEAD's refactored structure and re-inserting the `[paged 0008]`
`paged_prefix_api::commit(...)` block verbatim after `slot.state = SLOT_STATE_GENERATING;`
and before `if (slot.can_speculate())`, re-indented to the new (de-nested) level,
with the identical `paged_kv_commit && cache_prompt && !has_mtmd` guard. Semantics
unchanged.

**patch 0013** (`6d37431`, static `LLAMA_PREFILL_BUDGET`) - 3 conflicts.
- C1: inserted the `n_prefill_budget` / `n_prompt_budgeted` var block before
  HEAD's new `auto & alora_scale = batch.alora_scale;` references (upstream moved
  alora_scale/disabled_id into the `server_batch` struct).
- C2: merged the budget gate into HEAD's `while (... batch.size() < n_batch ...)`
  (took upstream's `batch.size()` rename, kept the budget condition).
- C3: the original outer `break` was translated to the new idiom `add_ok = false;`
  (exact semantic equivalent of "stop admitting prompts to remaining slots"); the
  upstream-removed `if (batch.n_tokens >= n_batch) break;` was dropped (now handled
  by the top-of-lambda check).

**patch 0016** (`02fa047`, dynamic decode-first budget, supersedes 0013) - 2
conflicts + 1 clean-hunk fix.
- The big budget-block rewrite hunk applied cleanly (its expected parent == the
  faithfully-resolved 0013 block).
- Clean-hunk fix: the clean-applied line `const int32_t n_decode_in_batch = batch.n_tokens;`
  referenced the `server_batch` member, which has no `.n_tokens` -> changed to
  `batch.size()` (== D, the Phase-1 decode load; identical value).
- C-A: while-condition -> took THEIRS (dynamic `prefill_budget_step` +
  `prefill_cap_per_slot`), adopted `batch.size()`.
- C-B: admission break -> 0016 dynamic budget check with `break` -> `add_ok = false`,
  dropped the upstream-removed `batch.n_tokens >= n_batch` break.

OFF-path invariant verified by construction in all three: with the env knobs
unset (`prefill_budget_step == prefill_cap_per_slot == 0`, `paged_kv_* == false`)
the added conditions never fire, so the scheduler is byte-identical to stock HEAD.

### Kernel patches: ZERO rebase conflicts
Patches 0017-0024 - which touch the bit-exact compute paths
(`gated_delta_net.cu` +330, `mmq.cu`/`mmq.cuh` +209, `ssm-conv.cu` +112,
`quantize.cu`, `fattn.cu`, `src/models/qwen35.cpp`/`qwen35moe.cpp`/`qwen3next.cpp`,
`src/llama-kv-cache.*`, `src/paged-*`, `tests/test-backend-ops.cpp` +79) - all
applied **cleanly** during the rebase (3-way). No math, reduction order, or kernel
context was touched during conflict resolution.

## Clean rebuild
`cmake --build build-cuda --target clean && cmake --build build-cuda -j20`,
preserving the existing CMakeCache (CMAKE_CUDA_ARCHITECTURES=121, GGML_CUDA=ON,
GGML_CUDA_FA=ON, GGML_CUDA_GRAPHS=ON, GGML_CUDA_NCCL=ON). Result: BUILD_EXIT=0,
all targets at 100%. (The only log "error" is a benign webui `dist.tar.gz`
download miss, unrelated to the gate binaries.)

## GATE: ALL GREEN

(a) `test-backend-ops` (Backend CUDA0):
| op | result |
|----|--------|
| GATED_DELTA_NET | 36/36 OK |
| SSM_CONV        | 45/45 OK |
| MUL_MAT         | 1146/1146 OK |
| MUL_MAT_ID      | 806/806 OK |

(b) greedy md5 (`llama-completion -ngl 99 -fa on -p "The capital of France is" -n 48 --temp 0 --seed 1`):
| model | md5 | baseline | verdict |
|-------|-----|----------|---------|
| dense `q36-27b-nvfp4`     | `5951a5b4d624ce891e22ab5fca9bc439` | `5951a5b4d624ce891e22ab5fca9bc439` | PASS |
| MoE `q36-35b-a3b-nvfp4`   | `07db32c2bcb78d17a43ed18bc22705cd` | `07db32c2bcb78d17a43ed18bc22705cd` | PASS |

Bit-exactness preserved across the upstream jump.

## Decode bench sanity (rebased build, post-pin-sync)

`llama-batched-bench -ngl 99 -fa on -npp 128 -ntg 128 -npl 32,128 -c 33000`,
S_TG (decode) tok/s at npl128, patch defaults on:
| model | npl128 S_TG (new tip) | post-0023 reference | delta |
|-------|----------------------|---------------------|-------|
| dense `q36-27b-nvfp4`   | **366.41** | 373.2 | -1.8% |
| MoE `q36-35b-a3b-nvfp4` | **751.11** | 745.7 | +0.7% |

Both within the +/-3% noise band -> the patchset performs the same on `9d5d882d`.
(npl32 also matches: dense 205.83 vs 207.6; MoE 438.29 vs 440.0.)

## Export phase: re-export `.patch` files and pick the ones that changed

The committed `.patch` files were generated against the old base. Each shipped
patch was re-exported from its rebased commit (`git format-patch -1 <commit>`) and
compared body-to-body against the committed file (ignoring the volatile `From`
commit-hash line and the `index` blob-hash lines). Classification:

- **CONTENT (real hunk-body change -> MUST update):** `0008`, `0013`, `0015`, `0016`.
- **LINENUM only (hunk bodies byte-identical, only `@@` line-numbers shifted ->
  still apply cleanly, left as-is):** `0009`, `0017`, `0018`, `0019`, `0020`,
  `0021`, `0024`.
- **IDENTICAL (no change at all):** `0001`, `0002`, `0003`, `0004`, `0006`,
  `0007`, `0010`, `0011`, `0012`, `0014`, `0022`, `0023`.

An independent isolated `git apply --check` sweep (each shipped patch vs the
rebased pre-state tree) agreed exactly: the same 4 (`0008`/`0013`/`0015`/`0016`)
are the only ones that no longer `git apply` to `9d5d882d`. The build applies the
series with plain `git apply` (Makefile) which tolerates `@@` line-number offsets,
so the 7 LINENUM patches still apply (verified) and are intentionally not churned.

### 0015 was a 4th change beyond the 3 rebase conflicts
The rebase reported only 3 conflicts (`0008`/`0013`/`0016`). `0015`
(expert-density MoE token-tile auto-select) rebased *cleanly* via 3-way merge, but
its committed `.patch` no longer applies to `9d5d882d` via plain `git apply`:
upstream inserted a new test case
(`test_mul_mat_id(GGML_TYPE_Q4_0, GGML_TYPE_F32, 32, 2, false, 2880, 32, 2880)`)
in `tests/test-backend-ops.cpp` right at `0015`'s insertion anchor, so the hunk's
context lines shifted. `0015`'s own inserted lines are unchanged - it is a pure
context re-anchor, no behavioral change. This is exactly why a per-patch
re-export/apply-check was run instead of trusting the 3-conflict count.

### What changed in each updated patch (From/index hash noise aside)
- `0008`: same `[paged 0008]` commit block (identical env-guard + `paged_prefix_api::commit`
  call), re-indented to the refactored `update_slots` lambda level and re-anchored
  after `slot.state = SLOT_STATE_GENERATING;`; `@@` headers updated.
- `0013`: budget var-block / while-gate / admission-break re-expressed against the
  refactored loop (`batch.size()`, `add_ok=false`); `@@` headers updated.
- `0015`: hunk context re-anchored around the new upstream test case; inserted
  lines identical; `@@` header updated.
- `0016`: dynamic budget block + `n_decode_in_batch = batch.size()` + admission
  `add_ok=false` against the refactored loop; `@@` headers updated.

## Equivalence proof (the updated series == the gate-green tree)

The 4 updated files are byte-faithful `git format-patch -1` exports of the
gate-green rebased commits (`240758e`, `6d37431`, `5349f82`, `02fa047`). Applying
the full corrected series (the 19 unchanged committed patches + the 4 re-exports)
in order to a fresh bare `9d5d882d` checkout with plain `git apply` succeeds for
all 23 patches, and the resulting tree is **byte-identical to the gate-green
`paged` tip (`2ee65c2`) for every code file** (`git diff` over all paths except
`*.md` and the unshipped `examples/simple/*` scaffold drivers is empty). So the
shipped `.patch` series reproduces exactly the tree that passed test-backend-ops,
the md5 bit-exact gate, and the bench.

## Pre-existing finding (NOT introduced by this pin-sync, NOT fixed here)
Committed patch `0019` carries a *modify* hunk against the dev-only doc
`SSM_DECODE_FIX_RESULTS.md` (`index 2e7c8c2..77879e4 100644`), a file that exists
only because of an unshipped docs commit on the dev tree and is absent from a
clean llama.cpp checkout. Under strict `git apply` that hunk fails ("No such file
or directory"). This is pin-independent (the file is upstream-absent on both
`8be759e6` and `9d5d882d`) and present identically in the old and new `0019`
(LINENUM class), so it is left untouched to keep the pin-sync faithful. (`0021`'s
`CONV_STATE_FUSION_RESULTS.md` is a *create* hunk and applies fine.) Stripping the
stray dev-doc hunks from the shipped patches is a separate cleanup, out of scope
for the pin-sync.

## Source of truth
The rebased branch on the DGX dev tree (`~/llama-paged-dev`, branch `paged`, HEAD
`2ee65c2`) is the source of truth; `paged-prerebase-backup` (`a8a9d12`) retains
the pre-rebase state.

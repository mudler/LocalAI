# Pin-bump apply-feasibility check: paged patch series vs latest llama.cpp tip

Date: 2026-06-27. Scope: textual `git apply` feasibility ONLY. No compile, no
bit-exact gate (those require the DGX GPU and the manual PIN_SYNC process). This
report answers one question: if we bumped the pin to the latest upstream tip,
would the vendored paged patch series still apply?

## Pins

| | commit | subject |
|---|---|---|
| Current shipped pin | `9d5d882d8cd0f0a9283d87ed5e6fe3ee0d925fb1` | model : Add label for LFM2.5-230M (#25008) |
| Latest master tip   | `c299a92c38b6de6a1139617652b66081828648db` | binaries : Improve rpc-server and export-graph-ops names (#25045) |

Gap: the pin is **23 commits behind** the latest master tip (`ahead_by: 23`,
GitHub compare API). The upstream range touched many files across the tree
(modifications plus at least one rename).

## Method

Two fresh shallow clones of `ggml-org/llama.cpp` (the current pin as a baseline,
and the latest master tip as the target). The series
`backend/cpp/llama-cpp/patches/paged/0*.patch` (28 files: 0001-0030, gaps at
0005 and 0027) was applied IN ORDER to each tree.

Each patch was classified two ways:

- **`git apply --check -p1`** - this is the BUILD's real apply method
  (`backend/cpp/llama-cpp/Makefile`'s `llama.cpp` target does
  `git apply --verbose "$p" || exit 1`). This is the only signal that decides
  whether a bumped build succeeds. `git apply` natively tolerates `@@`
  line-number offsets but NOT context-line changes.
- **GNU `patch -p1` dry-run** - the `prepare.sh` fallback method, used here as a
  recovery probe to tell a fixable offset/fuzz from a genuine conflict.

Running against BOTH pins isolates bump-induced failures from pre-existing,
pin-independent quirks of the shipped series.

## Result: the bump is CLEAN / offset-tolerant. Zero re-exports needed for the bump.

The series behaves **identically** under `git apply` on the latest tip and on
the current pin.

- **27 / 28 patches apply CLEAN under `git apply`** on the latest tip (same 27
  as on the current pin).
- **1 / 28 fails `git apply` (0019) - and it fails identically on the current
  pin too**, for a reason that has nothing to do with the bump (see below). Its
  code applies fine.
- **No new conflicts.** Not a single patch that applied on the current pin fails
  on the latest tip.
- **Zero context-fuzz anywhere.** Every recovery the GNU-patch probe reported is
  a pure line-number offset, which `git apply` absorbs natively.

### What the 23-commit jump actually changed

Only which patches `git apply` has to place at a line offset (context drift from
the 23 upstream commits). All still apply CLEAN; none needs re-export.

- Offset-placed on the current pin (6): 0009, 0017, 0018, 0020, 0021, 0024.
- Offset-placed on the latest tip (10): 0009, 0015, 0017, 0018, 0020, 0021,
  0024, 0025, 0026, 0028.
- New offsets introduced by the bump (4): **0015, 0025, 0026, 0028** - all
  remain CLEAN under `git apply` (line offset only, no fuzz, no conflict).

### The single `git apply` failure (0019) is pre-existing, not a bump regression

`0019-qwen35-ssm-decode-fused-gather.patch` fails `git apply` on BOTH pins. The
sole cause is its first hunk, a *modify* hunk against `SSM_DECODE_FIX_RESULTS.md`
- a dev-only doc that exists on the DGX dev tree (from an unshipped docs commit)
but is absent from any clean upstream checkout:

```
error: SSM_DECODE_FIX_RESULTS.md: No such file or directory
```

`git apply` is atomic, so that one stray hunk rejects the whole patch. 0019's 8
real code files (ggml.h, ggml-cpu/ops.cpp, ggml-cuda/gated_delta_net.cu, ggml.c,
delta-net-base.cpp, models.h, qwen35.cpp, qwen35moe.cpp) all apply cleanly (the
GNU-patch probe applies them with only line offsets and reports 0 failed code
hunks). This is exactly the pre-existing finding documented in
`PIN_SYNC_9d5d882d.md` ("Pre-existing finding ... NOT introduced by this
pin-sync, NOT fixed here ... a separate cleanup, out of scope"). It is identical
at both pins, so it is NOT introduced by a bump. Stripping the stray dev-doc
hunk from 0019 (and the analogous 0021 *create* hunk for
`CONV_STATE_FUSION_RESULTS.md`, which happens to apply fine) is a cleanup that
should happen regardless of any pin bump.

## Verdict

A pin bump from `9d5d882d` to the latest tip `c299a92c` is **textually clean**:
the full paged series applies via the build's `git apply` with only benign
line-number offsets and zero conflicts - no patch needs re-export for the bump.
The lone `git apply` failure (0019) is a pre-existing shipped-series defect (a
stray dev-doc hunk), present identically on the current pin, and unrelated to the
bump.

## Caveats (why this does NOT authorise shipping a bump)

This is a textual apply check only. It does NOT verify that the patches are still
SEMANTICALLY correct against upstream's 23 refactor commits, that the result
compiles, or that it stays bit-exact. The 23 upstream commits touched many files;
a clean text-apply can still hide a semantic break (e.g. a function the kernel
patches call was refactored). The manual PIN_SYNC process on the DGX GPU
(rebuild + `test-backend-ops` + the greedy-md5 bit-exact gate + a decode bench)
remains the gate before any pin is advanced. This report only establishes that
the bump's textual conflict surface is empty, so that pin-sync would start from a
clean apply.

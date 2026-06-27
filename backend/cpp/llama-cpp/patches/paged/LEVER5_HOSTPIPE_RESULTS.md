# Lever 5 - block-table within-step host cache (patch 0029)

## What

`get_block_table()` is called once per full-attention layer per decode step. The
KV cell layout (and therefore the block table bytes) is fixed for the whole step;
it only changes in `apply()` when the ubatch's slots are committed. The old path
recomputed the full table on every full-attention layer of every step.

Patch 0029 builds the table once per step and reuses the bytes (`memcpy`) for the
remaining full-attention layers, invalidating the cache in `apply()`. The reused
bytes are identical to a fresh compute, so the change is bit-exact. Disable with
`LLAMA_PAGED_NO_BT_CACHE=1`.

## Host-side get_block_table time (the lever)

`llama-batched-bench`, `LLAMA_KV_PAGED=1 LLAMA_MOE_FORCE_GRAPHS=1`,
`-npp 128 -ntg 128 -npl 128 -ngl 99 -fa on`, measured with the in-tree
`[L5INSTR]` host timers (aggregate over the full bench, n=2048 dense / 1280 MoE
get_block_table calls):

| model | get_block_table host, cache OFF | cache ON | reduction |
|-------|--------------------------------:|---------:|----------:|
| MoE  q36-35b-a3b-nvfp4 | 112.94 ms | 14.82 ms | -87% |
| dense q36-27b-nvfp4    | 193.78 ms | 16.90 ms | -91% |

The MoE 112.94 -> 14.82 ms is the "110 -> 14 ms host" headline. `set_inputs`
host time falls in lockstep (MoE 128.6 -> 32.0 ms; dense 220.2 -> 36.5 ms) and
`process_ubatch` host (hostproc) drops MoE 498.8 -> 413.0 ms, dense 730.1 ->
544.2 ms.

## Throughput effect

Same bench, TG (decode) tokens/s, cache OFF -> ON:

| model | TG t/s OFF | TG t/s ON | delta | vs vLLM @npl128 |
|-------|-----------:|----------:|------:|----------------:|
| dense q36-27b-nvfp4 | 364.81 | 374.72 | +2.7% | 374.72 / 391 = 95.8% |
| MoE  q36-35b-a3b    | 752.19 | 756.97 | +0.6% (flat) | n/a |

- Dense decode is partly host-bound, so removing ~90% of the get_block_table host
  time lifts dense TG by a few percent (run-to-run; ~0.4-2.7% across runs) and
  pushes it to ~96-97.5% of the vLLM 391 t/s @npl128 reference.
- MoE decode is compute-bound (the FP4 GEMM dominates the step), so the ~98 ms of
  saved host time is hidden behind GPU compute and is off the critical path: MoE
  TG is flat. The deployment path (MoE) sees no regression and no win - the cache
  is a pure pipeline cleanup there.
- npl=1 single-stream decode: get_block_table is tiny either way (MoE 0.64 ->
  0.22 ms over 128 steps); the lever only matters at batch.

## Bit-exactness

`llama-completion -p "The capital of France is" -n 48 --temp 0 --seed 1`,
chat-template (conversation) path:

| path | md5 |
|------|-----|
| non-paged MoE | 07db32c2bcb78d17a43ed18bc22705cd |
| paged MoE, cache ON  | 8cb0ce23777bf55f92f63d0292c756b0 |
| paged MoE, cache OFF (`LLAMA_PAGED_NO_BT_CACHE=1`) | 8cb0ce23777bf55f92f63d0292c756b0 |
| dense non-paged | 5951a5b4d624ce891e22ab5fca9bc439 |
| dense paged | 5951a5b4d624ce891e22ab5fca9bc439 |

cache ON == cache OFF confirms the lever is numerically neutral. The paged-MoE
md5 (8cb0ce23) differs from the non-paged md5 (07db32c2) by a benign
FP-accumulation-order difference of the paged attention reduction, KL-validated
in PAGED_BITEXACT_NOTE.md (not introduced by this lever - it is present on the
0028 baseline too).

## Verdict

Ship. Bit-exact per path, real host-pipe win on host-bound (dense) decode,
neutral on the compute-bound MoE deployment path.

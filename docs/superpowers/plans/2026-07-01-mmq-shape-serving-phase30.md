# MMQ Shape Serving Phase 30 Plan

> **For agentic workers:** Use verification-before-completion before claiming
> trace or gate results. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Use patch `0056` to collect live grouped-MMQ selector shapes under the
n128 serving workload and derive the next structural-kernel target shape.

**Architecture:** Measurement-only. Start `llama-server` with
`LLAMA_MOE_MMQ_SHAPE_TRACE=4096`, run h2h n128, parse the server log, then run
post-serving md5/op gates.

**Tech Stack:** DGX GB10, llama.cpp CUDA backend, h2h client,
`paged-inference-gates.sh`.

---

## Checklist

- [x] **Step 1: Check DGX preflight and lock**
  - `docker=0`
  - `local_ai_worker=0`
  - `compute=0`
  - owner file set to `codex-phase30-mmq-shape-serving`

- [x] **Step 2: Run traced n128 serving workload**
  - Artifact: `/home/mudler/bench/phase30_mmq_shape_serving/20260701_043300`
  - Source: `dgx:~/llama-phase6-source`, commit `826c97a05`
  - Env: `LLAMA_MOE_MMQ_SHAPE_TRACE=4096`
  - h2h result: `decode_agg_tps=645.8`, `agg_tps=313.3`,
    `prefill_tps=1597.9`, `TTFT mean=8192.3 ms`

- [x] **Step 3: Parse trace distribution**
  - Total traced calls: `4096`
  - Decode-like (`ncols_max <= 128`): `1200`
  - Prefill-like (`ncols_max > 128`): `2896`
  - Decode-like selected `mmq_x_best` only in `{32,40,48,64}` with density
    `1-4`.
  - Prefill-like was mostly density `16` with `mmq_x_best=128`.
  - `stream_k=1` for all traced calls.

- [x] **Step 4: Run post-serving inference gates**
  - MoE md5: `8cb0ce23777bf55f92f63d0292c756b0`
  - Dense md5: `5951a5b4d624ce891e22ab5fca9bc439`
  - `MUL_MAT_ID`: `806/806`

## Result

The next grouped-MMQ structural experiment should target decode small-M shapes
separately from prefill: `ncols_max` 26-111, density 1-4, selected tile <= 64,
with stream-k/fixup behavior accounted for.

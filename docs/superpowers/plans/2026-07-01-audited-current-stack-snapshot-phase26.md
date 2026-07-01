# Audited Current Stack Snapshot Phase 26 Plan

**Date:** 2026-07-01
**Phase:** 26
**Goal:** run the reusable current-stack paged-vs-vLLM serving harness end to
end on the DGX, with hardware and compact inference gates attached to the
artifact, so throughput comparisons cannot hide an inference regression.

## Context

Phase 20 refreshed the current-stack serving numbers. Phase 24 added
`hardware.txt`; Phase 25 added `gate_summary.tsv`. Phase 26 is the first full
serving run that uses both audit surfaces in one artifact.

## Checklist

- [x] **Step 1: Preflight DGX**
  - Verified no running docker containers before launch.
  - Verified no `local-ai-worker` container before launch.
  - Verified no active GPU compute processes before launch.
  - Used the owner-file GPU lock protocol.

- [x] **Step 2: Launch full current-stack snapshot**
  - Ran `paged-current-serving-snapshot.sh` from the LocalAI worktree copy.
  - Target source: `dgx:~/llama-phase6-source`.
  - Source HEAD: `f2521ab12 feat(server): trace speculative batch shapes`.
  - Artifact: `/home/mudler/bench/phase26_audited_snapshot/20260701_053650`.

- [x] **Step 3: Preserve hardware evidence**
  - `hardware.txt` recorded `hardware_class=gb10_or_workstation_blackwell`.
  - `hardware.txt` recorded `GPU 0: NVIDIA GB10`.
  - Driver: `580.159.03`.
  - Compute capability: `12.1`.

- [x] **Step 4: Gate inferencing before and after serving**
  - Pre MoE md5: `8cb0ce23777bf55f92f63d0292c756b0`.
  - Pre dense md5: `5951a5b4d624ce891e22ab5fca9bc439`.
  - Pre `MUL_MAT_ID`: `806/806`.
  - Post MoE md5: `8cb0ce23777bf55f92f63d0292c756b0`.
  - Post dense md5: `5951a5b4d624ce891e22ab5fca9bc439`.
  - Post `MUL_MAT_ID`: `806/806`.
  - `gate_summary.tsv` records all rows as `ok`.

- [x] **Step 5: Capture same-session serving numbers**
  - Paged and vLLM were run in the same artifact with the same h2h client.
  - `summary.tsv` records the aggregate, decode, per-sequence, TTFT, and prefill
    rows plus ratios.

- [x] **Step 6: Record results in project docs**
  - Updated `README.md` with Phase 26 as the latest current-stack snapshot.
  - Updated `GB10_PARITY_PHASE0_RESULTS.md` with the full audited result.
  - Updated `PARITY_HANDOFF.md` with the operational handoff result and artifact
    index.
  - Updated `VLLM_PARITY_LEVER_MAP.md` with the current benchmark baseline.

## Result

Phase 26 confirms that the current clean stack still does not reach vLLM serving
parity on GB10, while the inference gates remain green before and after the
serving benchmark.

| n | paged decode_agg | vLLM decode_agg | paged/vLLM decode | paged agg | vLLM agg | paged/vLLM agg |
|---|------------------|-----------------|-------------------|-----------|----------|----------------|
| 8 | 230.8 | 283.2 | 81.5% | 170.6 | 241.6 | 70.6% |
| 32 | 420.0 | 609.0 | 69.0% | 254.6 | 466.7 | 54.6% |
| 128 | 673.4 | 1025.0 | 65.7% | 324.0 | 656.5 | 49.4% |

Treat `/home/mudler/bench/phase26_audited_snapshot/20260701_053650` as the
current audit-grade GB10 baseline.

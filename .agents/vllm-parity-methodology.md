# Methodology: Closing the vLLM Decode-Throughput Gap in llama.cpp

This is the playbook that took the paged backend
([.agents/llama-cpp-localai-paged-backend.md](llama-cpp-localai-paged-backend.md))
from ~38% of vLLM decode to **parity-to-ahead on dense** (and a proven, honest
ceiling on MoE) on GB10. Use it for any "make llama.cpp match or beat engine X on
accelerator Y" effort. The *levers* are model- and hardware-specific; the
*discipline* below is not. The worked example, with all numbers, is the paged
backend README.

## The core loop

1. **Establish a bit-exact baseline and gate FIRST.** Record the greedy md5 (per
   path) and an f32 reference. Every optimization must stay byte-identical to it -
   or ship as an explicit, default-off precision opt-in. This is what lets you
   optimize aggressively without silently regressing quality. Gate two ways:
   greedy md5, and `test-backend-ops` against the CPU oracle.

2. **Profile - do not assume.** nsys the steady-state decode step, broken down per
   *kernel* AND per *memcpy*. Find the dominant cost. "It's the GEMM" was wrong
   here: on hybrid gated-DeltaNet models the bottleneck was the recurrent-state
   **plumbing** (state memcpy + gathers, ~67% of the step), not the weight GEMM.
   Also sanity-check GPU-busy %: an early "low utilization" reading was a profiling
   window artifact (decode was 96-99% GPU-busy), not real idle.

3. **Ground-truth BOTH engines.** Decompose *your* decode step AND the
   competitor's, side by side, per bucket, and compute the per-bucket delta. This
   tells you WHERE the gap actually is - not where you would guess. It overturned
   premises here: e.g. vLLM does NOT run the GDN/attn projections as NVFP4 (it
   keeps them bf16, same as us); the MoE expert GEMM was a llama *win*, not the gap.

4. **Per-lever discipline.** For each candidate: implement -> bit-exact gate ->
   same-harness A/B bench. Use a runtime env-toggle (flag off vs on) ONLY for
   levers that are actually runtime-gated; a lever **compiled into** the binary
   (e.g. the SSM decode fusions here) is NOT isolated by a runtime flag, so measure
   it build-vs-build. The full-patchset "stock" baseline likewise needs a
   **separately-built unpatched binary at the same pin** - toggling the runtime
   flag on the patched binary does not reproduce stock (it measures only the gated
   part; here that was ~neutral, which is exactly how this gotcha hides). Bank only
   what lifts AND gates. **Record every rejected or flat lever with the reason** -
   over time this is the most valuable part: it stops the next person re-running
   dead ends.

5. **Name the structural floor.** Prove the bit-exact ceiling exhaustively (every
   lever measured, not assumed). What remains is physical - the memory-bandwidth
   floor, the irreducible serial-SSM host loop (sampling can't start until logits
   land). Name it; do not claim more than you measured.

## Hard rules learned

- **Apples-to-apples, or label it.** Stock-vs-patched on the SAME harness
  (`llama-batched-bench`) is exact - lead with it. But "stock" must be a
  separately-built unpatched binary at the SAME pin, NOT the patched binary with
  the runtime flag off (compiled-in wins survive the toggle). Cross-engine "% of vLLM"
  (batched-bench vs vLLM server+client) is *indicative*; always caveat the harness
  and config (context length alone shifted the MoE figure 76% <-> 86%).
- **The win may be a precision trade, not a free lever.** bf16 SSM state was +12%
  but failed the f32 KL gate (vLLM keeps f32 too), so it ships default-off opt-in -
  never in a recommended config.
- **Reject the obvious-but-wrong, with evidence.** A faster kernel that is off the
  critical path benches FLAT (the freed time becomes idle). Quantizing the bf16
  projections to NVFP4 cost ~6% PPL - and vLLM keeps them bf16 for the same reason.
  Always measure before believing; a plausible mechanism is not a result.
- **The gate can be per-path.** Paged vs non-paged attention legitimately produces
  different (equivalent) FP-reduction orders; validate the difference is benign
  (KLD to f32) and then gate each path against its own reference.

## Orchestration (multi-agent)

- **One GPU profiler/bencher at a time** (the GPU-contention rule). Parallel
  design/analysis/read agents are fine; concurrent GPU benches pollute each other's
  numbers.
- **Adversarial verify.** Before banking a finding, spawn skeptics prompted to
  *refute* it; majority-refute kills it. Prevents plausible-but-wrong results.
- **Anti-punt.** Use foreground, blocking ssh loops with short benches and a
  progress-file checkpoint. Agents that background work and "wait for the monitor
  event" stall - forbid that pattern.
- **GPU coexistence.** On a shared host, stop the user's deployments for a clean
  benchmark window (with their OK) and ALWAYS restore them (wrap the bench so a
  failure cannot strand them).

## What generalizes (and what doesn't)

The *speedups* may be hardware-specific (here: CUDA/Blackwell - the SSM fusions,
NVFP4 FP4-MMA, the occupancy tune), which is why other accelerators did not
benefit. But the *findings* often generalize and are worth upstreaming: the
"decode is plumbing-bound, not GEMM-bound" insight and the bit-exact, CPU-mirrored
fusion ops help any backend running these models. Separate "ship our tuned backend"
from "upstream the portable op" - they are different deliverables.

## The closing record

Write up the result HONESTLY: the shipped wins, the rejected levers (with reasons),
the structural ceiling, and the cross-backend / cross-quant generality. Negative
results are as valuable as wins. The paged backend README is the template.

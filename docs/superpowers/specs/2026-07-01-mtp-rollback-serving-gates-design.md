# MTP Rollback and Serving Gates Design

## Goal

Move MTP speculative decoding from a smoke-only Phase 9 result to a gated
parity workstream by proving that Qwen3.6 hybrid recurrent state can be rolled
back safely under speculative rejection.

This phase does not enable MTP by default and does not count MTP as a speed
win. It creates the evidence required before any serving benchmark can be
interpreted as valid.

## Current Evidence

Phase 9 proved that:

- The MoE GGUF contains Qwen3.6 `nextn` tensors.
- `draft-mtp` can run with the current model after backend draft sampling is
  disabled for MTP.
- Normal MoE and dense transcript md5 gates remain canonical.

The missing proof is that speculative rejection restores both memory systems:

- paged attention KV state,
- gated-DeltaNet recurrent state, including `n_rs_seq` snapshot rollback.

## Existing Mechanism

The current fork already contains the mechanism this phase should validate:

- `common_params_speculative::need_n_rs_seq()` requests recurrent snapshots for
  `draft-mtp` and `draft-eagle3`.
- Qwen3.5/Qwen3.6 architectures advertise recurrent rollback support through
  `llm_arch_supports_rs_rollback()`.
- `llama_memory_recurrent::seq_rm()` can roll back within the bounded
  `n_rs_seq` window by selecting an older recurrent-state snapshot.
- `tests/test-recurrent-state-rollback.cpp` verifies snapshot save/restore and
  dirty-context cleanup for recurrent models.

## Phase 14 Gates

Phase 14 has three gates:

1. **Rollback mechanism gate.** Build and run `test-recurrent-state-rollback`
   against `/home/mudler/bench/q36-35b-a3b-nvfp4.gguf` on DGX. This proves the
   actual model can restore recurrent snapshots and replay logits.
2. **MTP greedy-equivalence gate.** Run baseline greedy completion and MTP
   speculative completion on the same prompt/seed and compare normalized raw
   text. Exact transcript md5 is only valid when the same frontend emits the
   same number of generated tokens. `llama-speculative-simple` commits accepted
   token groups, so its output can be longer than `llama-completion -no-cnv`
   for the same `-n`. Treat the gate as a safety pass only if one normalized
   output is a prefix of the other and there is no first differing token.
3. **MTP partial-rejection gate.** Run an MTP configuration that drafts more
   than one token and records `n_drafted > n_accept`, while still matching
   greedy output. This proves rejection happened and did not corrupt
   inferencing state.

## Source Policy

Do not add a production source patch in this phase unless one of the gates fails
and the root cause is isolated. If all gates pass, record the evidence and then
scope a separate serving/API benchmark phase.

If a source patch is required, it must be fork-first, default-off or
test-only, and must pass:

- MoE transcript md5 `8cb0ce23777bf55f92f63d0292c756b0`.
- Dense transcript md5 `5951a5b4d624ce891e22ab5fca9bc439`.
- `test-recurrent-state-rollback` on the actual MoE GGUF.
- The MTP greedy-equivalence and partial-rejection gates.

## Stop Conditions

Stop and do not benchmark MTP for speed if:

- rollback test fails,
- MTP output differs from greedy baseline at `temp=0` after normalizing the
  example frontend's leading newlines,
- no run can produce both `n_drafted > 0` and `n_drafted > n_accept`,
- any run requires backend draft sampling for MTP,
- DGX is not free of docker containers, `local-ai-worker`, and GPU compute
  processes.

## Follow-up

Only after Phase 14 passes should Phase 15 measure serving/API throughput.
Phase 15 must compare non-spec serving against MTP serving with the same prompt
shape, request count, seed behavior, and canonical inference gates.

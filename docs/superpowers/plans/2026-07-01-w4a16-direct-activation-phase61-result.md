# W4A16 Direct-Activation Phase61 Result

Verdict: rejected.

The default-off direct-A kernel was implemented and gated, but it failed the
performance keep gate. The rejected local diff was saved at:

- `/tmp/phase61-w4a16-direct-a-rejected.diff`

The llama.cpp fork keeps only the safe routing stub:

- `41be3da5b test(cuda): cover W4A16 direct activation policy`
- `7967ad47f feat(cuda): route W4A16 direct activation stub`

## Correctness

Default inference gates:

- Artifact: `/home/mudler/bench/phase61_direct_default_gates/20260701_132057`
- MoE md5: `8cb0ce23777bf55f92f63d0292c756b0`
- dense md5: `5951a5b4d624ce891e22ab5fca9bc439`
- `MUL_MAT`: `1146/1146`
- `MUL_MAT_ID`: `806/806`

Forced direct-A op gate:

- Initial direct kernel: `794/806`, failed only `b=1` NVFP4 cases.
- Root cause: `ids_to_sorted` is a flat source-row index for `get_rows_cuda`,
  not a `(token, expert-slot)` pair.
- Fixed direct load: `src_base = src1 + src_row*nb11`.
- Final direct gate: `806/806`.

Opt-in transcript check:

- Artifact: `/home/mudler/bench/phase61_direct_ab/20260701_132237`
- forced W4A16 MoE md5: `07db32c2bcb78d17a43ed18bc22705cd`
- direct-A MoE md5: `07db32c2bcb78d17a43ed18bc22705cd`
- forced and direct-A transcripts were byte-identical.

## Performance

MoE prefill, `npl=32`, `ntg=4`:

| path | npp512 S_PP | npp2048 S_PP |
|------|-------------|--------------|
| default FP4-MMQ | `2325.45` | `2423.18` |
| forced W4A16 | `1471.05` | `1502.46` |
| forced W4A16 direct-A | `1566.30` | `1605.82` |

Direct-A improved forced W4A16 by `+6.5%` at `npp=512` and `+6.9%` at
`npp=2048`. It reached only `0.67x` and `0.66x` of default FP4-MMQ.

The keep gate required at least `+12%` over forced W4A16 and at least `0.75x`
of default FP4-MMQ. Phase61 failed both thresholds.

## Decision

Do not commit the direct-A kernel. Do not continue W4A16 body tuning as the next
GB10 parity lever. The sorted activation gather and cast were real overhead, but
removing them is not enough: the W4A16 grouped kernel body remains too slow
relative to default FP4-MMQ on GB10.

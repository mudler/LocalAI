# tensorfold

LocalAI backend for [TensorFold](https://github.com/ashhart/TensorFold). It runs
TensorFold in process: MLX on Apple Silicon, CUDA on NVIDIA.

## Hardware

- Apple Silicon (MLX).
- NVIDIA GPUs with compute capability 9.0 or newer (sm_90, sm_100, sm_120,
  sm_121). Ampere and Ada are refused at load time with an explicit message.
  The code path is here, but LocalAI publishes the CUDA 13 image in a
  follow-up; until then only the Apple Silicon image exists.

## One process per model

MLX memory and wired limits and the `MLX_ENV` variables are process-wide, so
each loaded model needs its own backend process. LocalAI already starts one
backend process per loaded model, so do not share one process between models.

## Load options

Set them in the model `options` list as `key:value` (split on the first colon).
An unknown key fails the load.

| Group | Keys |
|---|---|
| Generation defaults | `parallel`, `context`, `max_tokens`, `thinking`, `reasoning_effort`, `thinking_budget`, `temperature`, `top_p`, `top_k`, `min_p` |
| Drafting | `drafter` (`auto`, `none`, repo id or directory), `no_drafts`, `mtp_drafts`, `mtp_confidence` |
| Caches (MLX) | `prompt_cache_gib`, `checkpoint_slots`, `spill_gib`, `snapshot_dir`, `max_snapshots`, `decode_share`, `mlx_cache_gib` |
| CUDA | `kv_dtype` (`bf16`, `int8`, `int4`; Flash Next only) |
| Other | `vision`, `memory_limit_gb` (exported as `TENSORFOLD_MEMORY_LIMIT_GB`) |

TensorFold refuses options that do not apply to the chosen backend, and the
backend passes that error through unchanged. `ssd_experts`, `ple_on_ssd`,
`lane_kernels`, `tp`, `rank` and `master` are not exposed.

## Upstream pin

TensorFold has no public embed API and no PyPI release, so the backend pins an
exact upstream commit in the `Makefile` (`TENSORFOLD_VERSION`, currently the
commit of tag `v0.5.0`). `make` fetches that commit into `sources/TensorFold`
and `install.sh` installs it from there. The bump bot edits
`TENSORFOLD_VERSION`; the embed surface smoke test turns a bump red when an
upstream symbol the backend relies on changes.

## CUDA requirements files

`requirements-cublas13.txt` and `requirements-l4t13.txt` list bare `torch` and
`triton` for now. They are placeholders until the CUDA packaging work pins the
exact wheel source.

# LongCat Video backend

This backend serves Meituan's `LongCat-Video` and
`LongCat-Video-Avatar-1.5` checkpoints through LocalAI's `GenerateVideo`
RPC. It supports:

- text-to-video and image-to-video with `LongCat-Video`;
- audio + text-to-avatar and portrait + audio-to-avatar with Avatar 1.5;
- multi-segment avatar continuation for speech longer than one segment;
- PyTorch SDPA when FlashAttention is unavailable, including CUDA 13 ARM64
  systems such as NVIDIA DGX Spark.

The upstream source is pinned in `Makefile` and patched at build time. The
patch adds only the missing SDPA attention branches; model and source licenses
remain MIT.

## Model options

| Option | Default | Description |
| --- | --- | --- |
| `attention_backend` | `sdpa` | `sdpa`, `auto`, `flash2`, `flash3`, or `xformers`. The packaged backend guarantees only `sdpa`. |
| `use_distill` | `true` for Avatar, `false` for base | Loads the checkpoint's fast distillation LoRA. |
| `use_int8` | `false` | Loads Avatar 1.5's INT8 DiT. BF16 has a lower load-time peak on unified-memory systems. |
| `base_model` | `meituan-longcat/LongCat-Video` | Base components used by Avatar 1.5. |
| `max_segments` | `8` | Maximum avatar continuation segments accepted per request. |
| `resolution` | `480p` | Image-conditioned generation resolution (`480p` or `720p`). |

Per-request `params` may set `num_segments`, `audio_guidance_scale`,
`offload_kv_cache`, `ref_img_index`, `mask_frame_range`, and `resolution`.

LongCat is CUDA-only and very large. Avatar 1.5 also loads tokenizer,
text-encoder, and VAE components from the base checkpoint. Keep ample unified
memory and storage available; no CPU or macOS backend image is published. The
initial backend supports one GPU per process; tensor parallel sizes above one
are rejected explicitly.

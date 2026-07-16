+++
disableToc = false
title = "LongCat Video and Avatar"
weight = 19
url = "/features/longcat-video/"
+++

LocalAI's `longcat-video` backend serves Meituan's official LongCat video-generation models through the `/video` API and the Studio **Video** page.

| Gallery model | Upstream checkpoint | Inputs | Output |
|---------------|---------------------|--------|--------|
| `longcat-video` | `meituan-longcat/LongCat-Video` | text, optional start image | video |
| `longcat-video-avatar-1.5` | `meituan-longcat/LongCat-Video-Avatar-1.5` | text, audio, optional portrait | video with the source audio |

The base checkpoint supports text-to-video and image-to-video. Avatar 1.5 adds audio-driven character animation, optional portrait conditioning, and continuation segments for longer speech.

{{% notice warning %}}
LongCat is a large, CUDA-only model family. LocalAI publishes this backend for Linux with NVIDIA CUDA 12 or CUDA 13 on x86_64 and CUDA 13 on ARM64. CPU, ROCm, and macOS images are not available. Avatar 1.5 also loads components from the base checkpoint, so reserve substantial disk and GPU or unified memory.
{{% /notice %}}

## Install from the Model Gallery

Install one or both recipes from **Models** in the web UI, or use the CLI:

```bash
local-ai models install longcat-video
local-ai models install longcat-video-avatar-1.5
```

You can also import either official Hugging Face URL. The importer recognizes the two repositories and writes a `longcat-video` model config with the appropriate use case and input/output modalities.

The required OCI backend is installed automatically when LocalAI first loads the model. The hardware detector selects the CUDA 12, CUDA 13, or CUDA 13 ARM64 variant.

### DGX Spark and NVIDIA ARM64

Use a LocalAI CUDA 13 ARM64 image as described in [GPU acceleration]({{%relref "features/GPU-acceleration" %}}). The backend defaults to PyTorch SDPA, avoiding the FlashAttention dependency that is commonly unavailable on Blackwell ARM64 systems.

For unified-memory systems, start with BF16 (`use_int8:false`, the default). INT8 lowers steady-state DiT memory but can have a higher load-time peak because the full model is materialized before the quantized weights are applied.

## Generate in Studio

1. Open **Studio**, then choose **Video**.
2. Select `longcat-video` or `longcat-video-avatar-1.5`.
3. Enter a prompt and choose `832x480` or `1280x720`.
4. Expand **Reference media** to upload a start image. For Avatar 1.5, upload or record the speech under **Avatar audio**.
5. Select **Generate**.

The base model can run without a reference image for text-to-video. Avatar 1.5 requires audio; the portrait is optional.

## API examples

### Text-to-video

```bash
curl http://localhost:8080/video \
  -H "Content-Type: application/json" \
  -d '{
    "model": "longcat-video",
    "prompt": "A cinematic tracking shot through a misty redwood forest",
    "width": 832,
    "height": 480,
    "num_frames": 93,
    "fps": 15
  }'
```

### Image-to-video

`start_image` accepts raw base64, a browser-style data URI, or a public HTTP(S) URL:

```bash
curl http://localhost:8080/video \
  -H "Content-Type: application/json" \
  -d "{
    \"model\": \"longcat-video\",
    \"prompt\": \"The subject turns toward the camera as leaves move in the breeze\",
    \"start_image\": \"$(base64 --wrap=0 portrait.png)\",
    \"params\": {
      \"resolution\": \"480p\"
    }
  }"
```

### Avatar from speech and a portrait

`audio` accepts raw base64, a data URI, or a public HTTP(S) URL. Each staged image or audio input is limited to 128 MiB.

```bash
curl http://localhost:8080/video \
  -H "Content-Type: application/json" \
  -d "{
    \"model\": \"longcat-video-avatar-1.5\",
    \"prompt\": \"A friendly presenter speaking naturally to camera\",
    \"start_image\": \"$(base64 --wrap=0 portrait.png)\",
    \"audio\": \"$(base64 --wrap=0 speech.wav)\",
    \"width\": 832,
    \"height\": 480,
    \"params\": {
      \"offload_kv_cache\": \"true\"
    }
  }"
```

Avatar output is generated at 25 FPS and is muxed with the submitted audio. When neither `num_frames` nor `params.num_segments` is provided, LocalAI derives the continuation count from the audio duration, up to the model's `max_segments` setting.

## Model configuration

The gallery and importer make each model self-describing. A manual Avatar 1.5 config looks like this:

```yaml
name: longcat-video-avatar-1.5
backend: longcat-video
known_usecases:
  - video
known_input_modalities:
  - text
  - image
  - audio
known_output_modalities:
  - video
options:
  - attention_backend:sdpa
  - use_distill:true
  - max_segments:8
parameters:
  model: meituan-longcat/LongCat-Video-Avatar-1.5
```

The explicit modality declarations are used by `GET /v1/models/capabilities` and attachment-aware clients. They avoid inferring model behavior from backend or checkpoint names.

### Load options

Model load options use `key:value` entries in `options`:

| Option | Default | Description |
|--------|---------|-------------|
| `attention_backend` | `sdpa` | `sdpa`, `auto`, `flash2`, `flash3`, or `xformers`; packaged images guarantee `sdpa` |
| `use_distill` | Avatar: `true`; base: `false` | Use the checkpoint's accelerated distillation path |
| `use_int8` | `false` | Use Avatar 1.5's INT8 DiT; unsupported by the base model |
| `base_model` | `meituan-longcat/LongCat-Video` | Base tokenizer, text encoder, and VAE used by Avatar 1.5 |
| `max_segments` | `8` | Maximum continuation segments accepted for one request |
| `resolution` | `480p` | Default image-conditioned resolution: `480p` or `720p` |

The initial backend supports one GPU per process. Tensor or context parallel sizes above one are rejected.

### Per-request parameters

The `/video` request's `params` object accepts string values:

| Parameter | Description |
|-----------|-------------|
| `num_segments` | Explicit number of Avatar continuation segments |
| `audio_guidance_scale` | Audio classifier-free guidance when distillation is disabled |
| `offload_kv_cache` | Offload continuation KV cache (`true` or `false`) |
| `ref_img_index` | Reference-frame index used during continuation |
| `mask_frame_range` | Number of frames blended around continuation boundaries |
| `resolution` | Per-request image-conditioned resolution (`480p` or `720p`) |

With distillation enabled, Avatar uses eight inference steps and fixed text/audio guidance of `1.0`. Disable `use_distill` in the model config before tuning `step`, `cfg_scale`, or `audio_guidance_scale`.

## Troubleshooting

- **HTTP 400, audio is required**: Avatar 1.5 was selected without `audio`.
- **HTTP 400, request needs too many segments**: trim the audio or raise `max_segments` in the model options.
- **HTTP 412**: the installed LocalAI runtime cannot select a compatible NVIDIA backend image.
- **Out of memory while loading**: use BF16 on unified-memory hardware, close other GPU workloads, or reduce model concurrency. INT8 is not guaranteed to reduce peak load memory.
- **Slow first request**: the backend and checkpoints are downloaded and loaded on demand; subsequent requests reuse the loaded pipeline.

See the general [`/video` API reference]({{%relref "features/video-generation" %}}) for the complete request and response schema.

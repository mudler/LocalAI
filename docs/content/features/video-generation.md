+++
disableToc = false
title = "Video Generation"
weight = 18
url = "/features/video-generation/"
aliases = ["/features/longcat-video/"]
+++

LocalAI can generate videos from text prompts and optional image or audio conditioning via the `/video` endpoint. Supported backends include `diffusers`, `stablediffusion`, `vllm-omni`, and the dedicated `longcat-video` backend.

## API

- **Method:** `POST`
- **Endpoint:** `/video`

### Request

The request body is JSON with the following fields:

| Parameter         | Type     | Required | Default | Description                                              |
|-------------------|----------|----------|---------|----------------------------------------------------------|
| `model`           | `string` | Yes      |         | Model name to use                                        |
| `prompt`          | `string` | Yes      |         | Text description of the video to generate                |
| `negative_prompt` | `string` | No       |         | What to exclude from the generated video                 |
| `start_image`     | `string` | No       |         | Starting image as base64 string or URL                   |
| `end_image`       | `string` | No       |         | Ending image for guided generation                       |
| `audio`           | `string` | No       |         | Audio conditioning as base64, a data URI, or URL          |
| `width`           | `int`    | No       | 512     | Video width in pixels                                    |
| `height`          | `int`    | No       | 512     | Video height in pixels                                   |
| `num_frames`      | `int`    | No       |         | Number of frames                                         |
| `fps`             | `int`    | No       |         | Frames per second                                        |
| `seconds`         | `string` | No       |         | Duration in seconds                                      |
| `size`            | `string` | No       |         | Size specification (alternative to width/height)         |
| `input_reference` | `string` | No       |         | Input reference for the generation                       |
| `seed`            | `int`    | No       |         | Random seed for reproducibility                          |
| `cfg_scale`       | `float`  | No       |         | Classifier-free guidance scale                           |
| `step`            | `int`    | No       |         | Number of inference steps                                |
| `response_format` | `string` | No       | `url`   | `url` to return a file URL, `b64_json` for base64 output |
| `params`          | `object` | No       |         | Backend-specific string parameters                        |

### Response

Returns an OpenAI-compatible JSON response:

| Field           | Type     | Description                                    |
|-----------------|----------|------------------------------------------------|
| `created`       | `int`    | Unix timestamp of generation                   |
| `id`            | `string` | Unique identifier (UUID)                       |
| `data`          | `array`  | Array of generated video items                 |
| `data[].url`    | `string` | URL path to video file (if `response_format` is `url`) |
| `data[].b64_json` | `string` | Base64-encoded video (if `response_format` is `b64_json`) |

## Usage

First install a video-generation model from the gallery (the examples below use `longcat-video`):

```bash
local-ai run longcat-video
```

### Generate a video from a text prompt

```bash
curl http://localhost:8080/video \
  -H "Content-Type: application/json" \
  -d '{
    "model": "longcat-video",
    "prompt": "A cat playing in a garden on a sunny day",
    "width": 512,
    "height": 512,
    "num_frames": 16,
    "fps": 8
  }'
```

### Example response

```json
{
  "created": 1709900000,
  "id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
  "data": [
    {
      "url": "/generated-videos/abc123.mp4"
    }
  ]
}
```

### Generate with a starting image

```bash
curl http://localhost:8080/video \
  -H "Content-Type: application/json" \
  -d '{
    "model": "longcat-video",
    "prompt": "A timelapse of flowers blooming",
    "start_image": "https://example.com/flowers.jpg",
    "num_frames": 24,
    "fps": 12,
    "seed": 42,
    "cfg_scale": 7.5,
    "step": 30
  }'
```

### Get base64-encoded output

```bash
curl http://localhost:8080/video \
  -H "Content-Type: application/json" \
  -d '{
    "model": "longcat-video",
    "prompt": "Ocean waves on a beach",
    "response_format": "b64_json"
  }'
```

## LongCat-Video and Avatar 1.5

LocalAI's `longcat-video` backend serves Meituan's official LongCat video-generation models through the `/video` API and the Studio **Video** page.

| Gallery model | Upstream checkpoint | Inputs | Output |
|---------------|---------------------|--------|--------|
| `longcat-video` | `meituan-longcat/LongCat-Video` | text, optional start image | video |
| `longcat-video-avatar-1.5` | `meituan-longcat/LongCat-Video-Avatar-1.5` | text, audio, optional portrait | video with the source audio |

The base checkpoint supports text-to-video and image-to-video. Avatar 1.5 adds audio-driven character animation, optional portrait conditioning, and continuation segments for longer speech.

{{% notice warning %}}
LongCat is a large, CUDA-only model family. LocalAI publishes this backend for Linux with NVIDIA CUDA 12 or CUDA 13 on x86_64 and CUDA 13 on ARM64. CPU, ROCm, and macOS images are not available. Avatar 1.5 also loads components from the base checkpoint, so reserve substantial disk and GPU or unified memory.
{{% /notice %}}

### Install from the Model Gallery

Install one or both recipes from **Models** in the web UI, or use the CLI:

```bash
local-ai models install longcat-video
local-ai models install longcat-video-avatar-1.5
```

You can also import either official Hugging Face URL. The importer recognizes the two repositories and writes a `longcat-video` model config with the appropriate use case and input/output modalities.

The required OCI backend is installed automatically when LocalAI first loads the model. The hardware detector selects the CUDA 12, CUDA 13, or CUDA 13 ARM64 variant.

#### DGX Spark and NVIDIA ARM64

Use a LocalAI CUDA 13 ARM64 image as described in [GPU acceleration]({{%relref "features/GPU-acceleration" %}}). The backend defaults to PyTorch SDPA, avoiding the FlashAttention dependency that is commonly unavailable on Blackwell ARM64 systems.

For unified-memory systems, start with BF16 (`use_int8:false`, the default). INT8 lowers steady-state DiT memory but can have a higher load-time peak because the full model is materialized before the quantized weights are applied.

### Generate in Studio

1. Open **Studio**, then choose **Video**.
2. Select `longcat-video` or `longcat-video-avatar-1.5`.
3. Enter a prompt and choose `832x480` or `1280x720`.
4. Expand **Reference media** to upload a start image. For Avatar 1.5, upload or record the speech under **Avatar audio**.
5. Select **Generate**.

The base model can run without a reference image for text-to-video. Avatar 1.5 requires audio; the portrait is optional.

### LongCat API examples

#### Text-to-video

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

#### Image-to-video

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

#### Avatar from speech and a portrait

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

### LongCat model configuration

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

#### Load options

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

#### Per-request parameters

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

### LongCat troubleshooting

- **HTTP 400, audio is required**: Avatar 1.5 was selected without `audio`.
- **HTTP 400, request needs too many segments**: trim the audio or raise `max_segments` in the model options.
- **HTTP 412**: the installed LocalAI runtime cannot select a compatible NVIDIA backend image.
- **Out of memory while loading**: use BF16 on unified-memory hardware, close other GPU workloads, or reduce model concurrency. INT8 is not guaranteed to reduce peak load memory.
- **Slow first request**: the backend and checkpoints are downloaded and loaded on demand; subsequent requests reuse the loaded pipeline.

## Error Responses

| Status Code | Description                                          |
|-------------|------------------------------------------------------|
| 400         | Missing or invalid model or request parameters       |
| 412         | The selected backend cannot run on the available hardware |
| 500         | Backend error during video generation                |

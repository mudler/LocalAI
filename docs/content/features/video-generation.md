+++
disableToc = false
title = "Video Generation"
weight = 51
url = "/features/video-generation/"
aliases = ["/features/longcat-video/"]
+++

LocalAI can generate videos from text prompts and optional image or audio conditioning via the `/video` endpoint. Supported backends include `diffusers`, `stablediffusion`, `vllm-omni`, `vllm-cpp` (MiniMax-H3, which generates video **and** audio together), and the dedicated `longcat-video` backend.

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
| `width`           | `int`    | No       | backend | Video width in pixels; omit it to get the model's own default canvas |
| `height`          | `int`    | No       | backend | Video height in pixels; omit it to get the model's own default canvas |
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

## MiniMax-H3 (vllm.cpp)

The `vllm-cpp` backend — LocalAI's own C++ port of vLLM — also serves MiniMax-H3, which generates **video and audio jointly**. The clip comes back as an MP4 with a real AAC track rather than a silent render.

| Gallery model | Upstream checkpoint | Inputs | Output |
|---------------|---------------------|--------|--------|
| `minimax-h3-fl2va-q4` | `MiniMaxAI/MiniMax-H3`, Q4_K_M FL2VA partition | text, optional start/end frame | video with generated audio |

```bash
local-ai models install minimax-h3-fl2va-q4
```

{{% notice warning %}}
This is a large, slow model. The five weight files total roughly 40 GB, and generation was measured at about 176 seconds per denoise step at the default 1344x768 canvas on a 20-SM device — so the 50-step default is a **multi-hour** request, not a multi-second one. Nothing in the path imposes a deadline, but plan for a long-running HTTP call, and use a CUDA host.
{{% /notice %}}

### Ask for the sound

The model generates picture and sound from the same prompt, so a prompt that only describes what is *seen* produces room tone and ambience. To get speech, say that the character talks and put the words in the prompt:

```text
It is TALKING to the camera: its mouth moves clearly in sync with its speech,
in a dry, deadpan tone.

It says, clearly and audibly: "Michael scheduled another all-hands.
It is about the printer. Again."

Audio: a single clear voice, close-miked, with quiet room tone underneath.
```

### Geometry and clip length

The trained canvas is **1344x768 at 124 frames and 24 fps**, about 5.2 seconds, and that is what the gallery entry defaults to. Two rules the engine enforces:

- The canvas is truncated onto a 32-pixel grid.
- The frame count sits on a **17n+5** grid (…, 90, 107, 124, 141, …). A count off the grid is rounded up, and LocalAI logs the value it actually rendered.

The trained clip range is roughly 124 to 362 frames (about 5 to 15 seconds).

### Text-to-video with sound

```bash
curl http://localhost:8080/video \
  -H "Content-Type: application/json" \
  -d '{
    "model": "minimax-h3-fl2va-q4",
    "prompt": "A cyan llama mascot in a grey office chair, talking to the camera. It says, clearly and audibly: \"the printer is down again\". Audio: one clear close-miked voice.",
    "num_frames": 124,
    "step": 50,
    "seed": 42
  }'
```

### First-frame conditioning

`start_image` pins the supplied image as frame 0 (H3's `fl2va` task); `end_image` pins the last frame. LocalAI converts the upload to the binary PPM at the exact output canvas that the engine requires, using `ffmpeg`, so PNG and JPEG uploads work. When no `width`/`height` is given, the canvas is derived from the image's aspect on a 768-pixel short edge.

```bash
curl http://localhost:8080/video \
  -H "Content-Type: application/json" \
  -d "{
    \"model\": \"minimax-h3-fl2va-q4\",
    \"prompt\": \"the subject turns toward the camera and starts speaking\",
    \"start_image\": \"$(base64 --wrap=0 portrait.png)\"
  }"
```

### Partitions: what this checkpoint will and will not do

MiniMax-H3 ships as two DiT partitions, and the gallery entry installs **FL2VA**, which serves `t2va` (text only) and `fl2va` (first/last frame). Reference conditioning — a whole reference image, a reference clip, or reference audio — belongs to the separate **Ref2VA** checkpoint.

This matters because the mismatch does not fail cleanly upstream: a reference passed to an FL2VA DiT renders for hours and returns a coloured lattice over the frame. The backend refuses the combination up front instead, naming the partition. The community quantisations strip the release metadata and the two DiTs are byte-structurally identical, so the partition is *declared* in the model config (`video_partition`) rather than detected.

### ffmpeg is required on the host

The engine writes frames and a WAV and composes the `ffmpeg` command line; LocalAI runs it. That process boundary is deliberate upstream, so the backend image ships no `ffmpeg`: install one on the host, or point `options: [ffmpeg:/path/to/ffmpeg]` at a binary. Without it, generation succeeds and the mux fails with a message saying so.

### MiniMax-H3 model configuration

```yaml
name: minimax-h3-fl2va-q4
backend: vllm-cpp
cuda: true
known_usecases:
  - video
known_input_modalities:
  - text
  - image
known_output_modalities:
  - video
options:
  - video_encoder:minimax-h3/qwen3vl-32B-MiniMax-H3-Q4_K_M.gguf
  - video_tokenizer:minimax-h3/tokenizer.json
  - video_vae:minimax-h3/video_vae.safetensors
  - video_vae_config:minimax-h3/video_vae_config.json
  - audio_vae:minimax-h3/audio_vae.safetensors
  - audio_vae_config:minimax-h3/audio_vae_config.json
  - video_partition:fl2va
  - video_device:cuda
  - video_dequant_bf16:true
  - video_width:1344
  - video_height:768
  - video_num_frames:124
parameters:
  model: minimax-h3/MiniMax-H3-FL2VA-Q4_K_M.gguf
```

`parameters.model` is the DiT. H3 is a checkpoint *set* rather than one model directory, so the encoder, the tokenizer and the two VAEs are named in `options`. Relative paths resolve against the models directory.

#### Load options

| Option | Default | Description |
|--------|---------|-------------|
| `video_encoder` | — | H3 text encoder (GGUF or a bf16 shard directory). Required unless `video_prompt_embeds` is set |
| `video_tokenizer` | — | `tokenizer.json` for the encoder |
| `video_vae` | — | Video VAE weights (`.safetensors`). Required |
| `video_vae_config` | `config.json` beside the weights | Carries `latents_mean` / `latents_std` and `clip_length` / `token_drop`; the decode is wrong without it |
| `audio_vae` | — | Audio VAE weights. Required |
| `audio_vae_config` | `config.json` beside the weights | As above, for audio |
| `video_prompt_embeds` | — | Pre-computed f32 conditioning, as an alternative to an encoder |
| `video_partition` | `fl2va` | `fl2va` or `ref2va`; must match the DiT you installed |
| `video_device` | `cpu`, or `cuda` when the config sets `cuda: true` | `cpu` or `cuda` |
| `video_dequant_bf16` | `false` | Dequantise and stream the DiT as bf16; what the Q4_K_M GGUF arm wants |
| `video_fp4_resident` | `false` | NVFP4 on CUDA: keep FP4 packed and use the Marlin W4A16 GEMM |
| `video_width` / `video_height` | 1344 / 768 in the gallery entry | Default canvas when the request omits it |
| `video_num_frames` | 124 in the gallery entry | Default clip length when the request omits it |
| `video_steps` | engine default (50) | Default denoise steps when the request omits it |
| `video_workdir` | a temporary directory | Where `frame_%06d.ppm` and `audio.wav` land. Set it to keep every run's frames |
| `video_crf` | 18 | x264 CRF for the mux |
| `ffmpeg` | `ffmpeg` from `PATH` | The mux binary |

#### Per-request parameters

The `/video` request's `params` object accepts string values. Unknown keys are rejected rather than ignored, so a typo does not cost you a multi-hour render of the wrong thing.

| Parameter | Description |
|-----------|-------------|
| `noise_aug` | Keyframe pinning strength; the default is 1.0 |
| `ref_image` | `ref2va` only: one whole reference image, as a binary PPM |
| `ref_video` | `ref2va` only: a directory of `frame_%06d.ppm` |
| `crf` | Per-request x264 CRF override |

`negative_prompt`, `cfg_scale` and `fps` have no MiniMax-H3 equivalent: H3 has no negative prompt or CFG scale, and it renders at a fixed frame rate that the audio track is synchronised to. Setting them is logged and ignored rather than silently honoured.

### MiniMax-H3 troubleshooting

- **`ffmpeg not found`**: install ffmpeg on the host or set `options: [ffmpeg:<path>]`. The frames and WAV are already rendered; only the mux failed.
- **`the FL2VA checkpoint serves t2va and fl2va only`**: you passed a reference image, clip or audio to the FL2VA DiT. Use `start_image` for first-frame conditioning, or install a Ref2VA checkpoint.
- **`video_partition must be "fl2va" or "ref2va"`**: the config declares something else.
- **`unknown params key`**: `params` accepts only the four keys above.
- **Text inside the frame comes out malformed**: this is the model's weakest area. Composite logos and signage in afterwards.

## Error Responses

| Status Code | Description                                          |
|-------------|------------------------------------------------------|
| 400         | Missing or invalid model or request parameters       |
| 412         | The selected backend cannot run on the available hardware |
| 500         | Backend error during video generation                |

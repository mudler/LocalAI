+++
disableToc = false
title = "Video Generation"
weight = 18
url = "/features/video-generation/"
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

### Generate a video from a text prompt

```bash
curl http://localhost:8080/video \
  -H "Content-Type: application/json" \
  -d '{
    "model": "video-model",
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
    "model": "video-model",
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
    "model": "video-model",
    "prompt": "Ocean waves on a beach",
    "response_format": "b64_json"
  }'
```

## LongCat-Video and Avatar 1.5

The `longcat-video` backend supports the two official Meituan checkpoints:

- `meituan-longcat/LongCat-Video` for text-to-video and image-to-video;
- `meituan-longcat/LongCat-Video-Avatar-1.5` for speech-driven avatar video, optionally conditioned on a portrait.

Install either preset from the Model Gallery, import its Hugging Face URL, or use the CLI:

```bash
local-ai models install longcat-video-avatar-1.5
```

The importer selects `longcat-video` automatically. The backend is published for CUDA 12 and CUDA 13 on x86_64 and for CUDA 13 on ARM64, including DGX Spark. LongCat requires Linux and NVIDIA CUDA; there are no CPU, ROCm, or macOS builds. Both checkpoints are very large, and Avatar also loads tokenizer, text encoder, and VAE components from the base model.

### Generate an avatar from a portrait and speech

`audio` and `start_image` accept raw base64, browser-style data URIs, or public HTTP(S) URLs. Each staged input is limited to 128 MiB.

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

Avatar 1.5 generates at 25 FPS. When `num_frames` and `params.num_segments` are omitted, the backend derives the required continuation segments from the audio duration, up to the configured `max_segments` model option.

The backend defaults to PyTorch SDPA so it can run without FlashAttention on Blackwell ARM64. Model options are expressed in the model YAML `options` list:

```yaml
backend: longcat-video
known_usecases:
  - video
options:
  - attention_backend:sdpa
  - use_distill:true
  - max_segments:8
parameters:
  model: meituan-longcat/LongCat-Video-Avatar-1.5
```

Supported request `params` are `num_segments`, `audio_guidance_scale`, `offload_kv_cache`, `ref_img_index`, `mask_frame_range`, and `resolution` (`480p` or `720p`). See the [backend README](https://github.com/mudler/LocalAI/tree/master/backend/python/longcat-video) for all load options.

## Error Responses

| Status Code | Description                                          |
|-------------|------------------------------------------------------|
| 400         | Missing or invalid model or request parameters       |
| 412         | The selected backend cannot run on the available hardware |
| 500         | Backend error during video generation                |

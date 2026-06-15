+++
disableToc = false
title = "Video Generation"
weight = 18
url = "/features/video-generation/"
+++

LocalAI can generate videos from text prompts and optional reference images via the `/video` endpoint. Supported backends include `diffusers`, `stablediffusion`, and `vllm-omni`.

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

## Error Responses

| Status Code | Description                                          |
|-------------|------------------------------------------------------|
| 400         | Missing or invalid model or request parameters       |
| 500         | Backend error during video generation                |

+++
disableToc = false
title = "Sound Generation"
weight = 19
url = "/features/sound-generation/"
+++

LocalAI supports generating audio from text descriptions via the `/v1/sound-generation` endpoint. This endpoint is compatible with the [ElevenLabs sound generation API](https://elevenlabs.io/docs/api-reference/sound-generation) and can produce music, sound effects, and other audio content.

## API

- **Method:** `POST`
- **Endpoint:** `/v1/sound-generation`

### Request

The request body is JSON. There are two usage modes: simple and advanced.

#### Simple mode

| Parameter        | Type     | Required | Description                                  |
|------------------|----------|----------|----------------------------------------------|
| `model_id`       | `string` | Yes      | Model identifier                             |
| `text`           | `string` | Yes      | Audio description or prompt                  |
| `instrumental`   | `bool`   | No       | Generate instrumental audio (no vocals)      |
| `vocal_language` | `string` | No       | Language code for vocals (e.g. `bn`, `ja`)   |

#### Advanced mode

| Parameter           | Type     | Required | Description                                     |
|---------------------|----------|----------|-------------------------------------------------|
| `model_id`          | `string` | Yes      | Model identifier                                |
| `text`              | `string` | Yes      | Text prompt or description                      |
| `duration_seconds`  | `float`  | No       | Target duration in seconds                      |
| `prompt_influence`  | `float`  | No       | Temperature / prompt influence parameter        |
| `do_sample`         | `bool`   | No       | Enable sampling                                 |
| `think`             | `bool`   | No       | Enable extended thinking for generation         |
| `caption`           | `string` | No       | Caption describing the audio                    |
| `lyrics`            | `string` | No       | Lyrics for the generated audio                  |
| `bpm`               | `int`    | No       | Beats per minute                                |
| `keyscale`          | `string` | No       | Musical key/scale (e.g. `Ab major`)             |
| `language`          | `string` | No       | Language code                                   |
| `vocal_language`    | `string` | No       | Vocal language (fallback if `language` is empty) |
| `timesignature`     | `string` | No       | Time signature (e.g. `4`)                       |
| `instrumental`      | `bool`   | No       | Generate instrumental audio (no vocals)         |

### Response

Returns a binary audio file with the appropriate `Content-Type` header (e.g. `audio/wav`, `audio/mpeg`, `audio/flac`, `audio/ogg`).

## Usage

### Generate a sound effect

```bash
curl http://localhost:8080/v1/sound-generation \
  -H "Content-Type: application/json" \
  -d '{
    "model_id": "sound-model",
    "text": "rain falling on a tin roof"
  }' \
  --output rain.wav
```

### Generate a song with vocals

```bash
curl http://localhost:8080/v1/sound-generation \
  -H "Content-Type: application/json" \
  -d '{
    "model_id": "sound-model",
    "text": "a soft Bengali love song for a quiet evening",
    "instrumental": false,
    "vocal_language": "bn"
  }' \
  --output song.wav
```

### Generate music with advanced parameters

```bash
curl http://localhost:8080/v1/sound-generation \
  -H "Content-Type: application/json" \
  -d '{
    "model_id": "sound-model",
    "text": "upbeat pop",
    "caption": "A funky Japanese disco track",
    "lyrics": "[Verse 1]\nDancing in the neon lights",
    "think": true,
    "bpm": 120,
    "duration_seconds": 225,
    "keyscale": "Ab major",
    "language": "ja",
    "timesignature": "4"
  }' \
  --output disco.wav
```

## Error Responses

| Status Code | Description                                      |
|-------------|--------------------------------------------------|
| 400         | Missing or invalid model or request parameters   |
| 500         | Backend error during sound generation            |

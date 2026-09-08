+++
disableToc = false
title = "audio.cpp backend"
weight = 38
url = "/features/audio-cpp/"
+++

[audio.cpp](https://github.com/0xShug0/audio.cpp) is a GGML audio inference framework
covering roughly thirty model families in one engine. LocalAI exposes it through the
native C++ `audio-cpp` backend, so a single installed backend can serve text to speech,
voice cloning, transcription, forced alignment, voice activity detection, speaker
diarization, source separation, voice conversion and music generation, depending only on
which model you point it at.

audio.cpp is developed by ShugoAI LLC, not by the LocalAI project. Model packages live in
[huggingface.co/audio-cpp/audio.cpp-gguf](https://huggingface.co/audio-cpp/audio.cpp-gguf).

## Installing

```bash
local-ai backends install audio-cpp
```

Or install it from the **Backends** page in the web UI. `audio-cpp` is a
preference-only backend: LocalAI never picks it automatically during model import,
because the only reliable signal for an audio.cpp model is the `audiocpp.model_spec.family`
key stored *inside* the GGUF, which cannot be read from a remote repository, and the
upstream GGUF repository hosts every family in one place. Set `backend: audio-cpp`
in the model YAML, or select it explicitly in the import form.

The bundled audio-cpp gallery entries set `backend:best` in `options` to select
an available compute backend, with CPU as the fallback. To force CPU execution,
replace that option with `backend:cpu`. Model configurations that omit this
option still default to CPU.

## What it serves

One model serves one family, and a family advertises the tasks it can perform. The
backend routes each LocalAI endpoint onto an audio.cpp task and refuses, with the
family's real capability list in the message, when the loaded model cannot serve it.

| LocalAI endpoint | gRPC RPC | audio.cpp tasks tried |
|---|---|---|
| `/v1/audio/speech`, `/tts` | `TTS`, `TTSStream` | `tts`, `clon` (voice cloning), `vdes` (voice design) |
| `/v1/audio/transcriptions` | `AudioTranscription`, `AudioTranscriptionStream` | `asr`, `align` (forced alignment, when a prompt is supplied) |
| `/v1/realtime` live transcription | `AudioTranscriptionLive` | `asr` in streaming mode |
| `/v1/vad` | `VAD` | `vad` |
| `/v1/audio/diarization` | `Diarize` | `diar` |
| `/v1/sound-generation` | `SoundGeneration` | `gen` |
| `/audio/transform`, `/audio/transformations` | `AudioTransform` | `sep` (separation), `vc` (voice conversion), `svc`, `s2s` |

A supplied speaker clip routes TTS to `clon`; free-form instructions route it to `vdes`.
Singing voice conversion (`svc`) is never chosen automatically, because no request signal
means "this input is singing"; pin it with `task:svc`.

### Not served, and why

These RPCs return `UNIMPLEMENTED` with an explanation rather than a degraded answer:

- **`AudioEncode` / `AudioDecode`**: audio.cpp has no codec task kind, so no family can be
  asked to turn PCM into codec frames or back.
- **`AudioTransformStream`**: no family advertises streaming for any task
  `AudioTransform` routes to (`sep`, `vc`, `svc`, `s2s`). Upstream advertises streaming for
  `tts`, `asr` and `vad` only, so a streaming transform here would be a buffered offline
  call in disguise.
- **`AudioToAudioStream`**: LocalAI's contract is an OpenAI-Realtime shaped audio
  conversation with a system prompt and a tool loop. audio.cpp's `s2s` is offline
  clip-to-clip processing against a target voice.
- **`VoiceEmbed`**: no family advertises the `spk` (speaker recognition) task, so nothing in
  the engine can produce a speaker embedding. Use the
  [voice recognition]({{%relref "features/voice-recognition" %}}) backends instead.

## Model YAML

None of the option names are guessable, so here is the full shape:

```yaml
name: supertonic
backend: audio-cpp
parameters:
  model: supertonic-3-orig.gguf
known_usecases:
  - FLAG_TTS
options:
  # Optional for an audio.cpp GGUF: the family is read from the file's
  # audiocpp.model_spec.family metadata. REQUIRED for a safetensors file or a
  # package directory, which carry no such key.
  - family:supertonic
  # Optional: pins the audio.cpp task instead of routing it from the RPC.
  - task:tts
  # ggml compute backend inside audio.cpp (not the LocalAI backend above).
  - backend:cuda
  - device:0
  - threads:8
  # Give up on a queued request instead of piling up behind a wedged run.
  - busy_timeout_ms:120000
  # Per-family knobs, passed through to the upstream loader and session.
  - load.vibevoice.lora:/models/my-adapter
  - session.seed_vc.weight_type:q8_0
```

Every entry is `key:value`, split on the **first** colon so values may contain more
(paths, for instance). An unknown key fails the load with `INVALID_ARGUMENT` and a message
listing the keys that exist, rather than being silently ignored.

### Option reference

| Option | Default | Meaning |
|---|---|---|
| `family:<name>` | read from the GGUF | The audio.cpp family. Optional for a standalone audio.cpp GGUF, which embeds `audiocpp.model_spec.family`. Required for a safetensors file or a package directory. Setting it explicitly also overrides the embedded value. |
| `task:<name>` | routed from the RPC | Pins the task. One of `gen`, `tts`, `clon`, `vc`, `svc`, `s2s`, `asr`, `align`, `vad`, `diar`, `sep`, `vdes`, `spk`. A pinned task is honoured exactly: if the family cannot serve it, the request is refused rather than rerouted. |
| `backend:<name>` | `cpu` | ggml compute backend: `cpu`, `cuda`, `vulkan`, `metal`, `best`. Must match the backend image you installed (a `cuda` value needs the CUDA image). |
| `device:<n>` | `0` | GPU index for the selected compute backend. Non-negative integer. |
| `threads:<n>` | runtime default | CPU threads. `0` leaves the choice to the runtime. |
| `busy_timeout_ms:<n>` | `0` (unbounded) | Bounds the wait for the model's single inference lane. A request that arrives while a run has already been in flight longer than this fails immediately with `UNAVAILABLE` instead of queueing, and a request that waits this long without the lane freeing up fails the same way. `0` waits indefinitely. |
| `live_idle_timeout_ms:<n>` | `30000` | How long `AudioTranscriptionLive` waits for the next audio frame before cancelling the stream and releasing the lane. That RPC holds the lane for the whole stream, so a peer that stops sending without closing would otherwise block every other request against this model. `0` disables the limit. |
| `model_spec_override:<path>` | unset | A model spec JSON file, or a directory containing `<family>.json`, used instead of the spec embedded in the GGUF. The path must exist at load time. |
| `load.<key>:<value>` | none | Forwarded to the upstream loader with the `load.` prefix stripped. |
| `session.<key>:<value>` | none | Forwarded to the upstream session with the `session.` prefix stripped. |

Numeric options are validated at load time: a non-integer or negative value is refused
before the model is read, not after.

### The `load.` and `session.` namespaces

Upstream families advertise their own options in two buckets, and the prefix decides which
bucket an entry lands in. The key that follows the prefix is passed through verbatim, and
upstream names those keys after the family, so both segments are needed:

```yaml
options:
  # Loader options (upstream "load options"):
  - load.ace_step.dit_model_path:acestep-v15-turbo
  - load.vevo2.whisper_model_path:/models/whisper-dir
  - load.vibevoice.lora:/models/my-adapter
  # Session options (upstream "session options"):
  - session.seed_vc.weight_type:q8_0
```

The names each family accepts are declared upstream in its loader, and audio.cpp's own CLI
prints them under "Model session options" and "Model load options" when asked for a model's
help. Options that upstream lists as *request* options are not configurable here: they come
from the API request, not from the model YAML.

## Voice activity detection with no download

The backend package ships upstream's `silero_vad` and `marblenet_vad` assets, so VAD works
with nothing to fetch. `bundled:<name>` resolves to the asset directory inside the installed
backend. Those assets are directories rather than GGUF files, so the family cannot be
inferred and `family:` is required:

```yaml
name: silero-vad
backend: audio-cpp
parameters:
  model: bundled:silero_vad
known_usecases:
  - FLAG_VAD
options:
  - family:silero_vad
```

Then call it like any other VAD model, as described in
[Voice Activity Detection]({{%relref "features/voice-activity-detection" %}}).

## Source separation stems

Separation families such as `htdemucs` and `mel_band_roformer` produce several named
outputs from one
run. Running inference once per stem would cost a full separation each time, so the backend
runs **once**, writes every stem to a sibling file next to the main output, and puts the
selected one in the response body. `params[stem]` picks which one:

```bash
curl http://localhost:8080/audio/transformations \
  -F model=htdemucs -F audio=@song.wav -F 'params[stem]=drums' -o drums.wav
```

Without `params[stem]` the body carries `vocals` when the model produces it, and the first
output otherwise. An unknown stem name is refused with a message listing the stems the model
really has, rather than quietly returning a different one. Every stem, including the one in
the body, is listed in the `X-Audio-Stems` response header. See
[Audio Transform]({{%relref "features/audio-transform" %}}) for the full endpoint contract.

`params[stem]` against a model that produces a single unnamed output is refused rather than
ignored.

## Text-conditioned transforms

`s2s` is a text-and-prosody route, not an audio-only one: `vevo2` resynthesises a line in
another voice and reads the line itself, refusing the run without it. `AudioTransform` has
no text field, so the text arrives as `params[text]` (or `params[target_text]`, the name
vevo2's own option table uses) and the backend lifts it into the engine's text input.
`params[language]` rides along when the line is not English.

```bash
curl http://localhost:8080/audio/transformations \
  -F model=audio-cpp-vevo2-speech-to-speech \
  -F audio=@source.wav -F reference=@target-speaker.wav \
  -F 'params[text]=The quick brown fox jumps over the lazy dog.' -o converted.wav
```

A model that takes no text ignores the keys, so separation and plain voice conversion are
unaffected.

## Pinned tasks

Two routes are unreachable by auto-routing and need `task:` in the model config, because
nothing in a request distinguishes them from a task the same family also advertises:

- `task:svc` for singing voice conversion. `seed_vc` and `vevo2` advertise both `svc` and
  ordinary `vc`, and no request signal means "this input is singing", so `vc` always wins.
- `task:s2s` for speech to speech, for the same reason against the same `vc`.

The gallery entries that ship these routes carry the pin already. Removing it gives plain
voice conversion from the same weights.

## Family notes

- **Supertonic**: use the `orig` GGUF package, whose weights are f32. The f16 package was
  observed to reach `ggml_concat` with mismatched operand types and take the backend
  process down with `SIGABRT` on the first request, rather than returning an error.
  Upstream's `docs/gguf.md` leaves supertonic's 16-bit column untested and records its
  q8_0 as "No (unsupported weight dtype)", which says the format is unusable rather than
  that it crashes. The backend refuses both at load time with a message naming the
  offending tensor, so neither reaches that abort.
- **Live transcription latency**: `AudioTranscriptionLive` needs a family advertising `asr`
  in streaming mode. At the pinned upstream revision that is `nemotron_asr`,
  `higgs_audio_stt` and `voxtral_realtime`. `higgs_audio_stt` and `voxtral_realtime` decode
  incrementally and emit partial text as chunks arrive. `nemotron_asr` only buffers in its
  chunk handler and decodes everything in `finalize()`, so with that family every delta
  arrives after the client closes its send side. Pick accordingly if you need partials
  during speech.
- **Voice conversion vs singing**: `seed_vc` and `vevo2` advertise both `vc` and `svc`, and
  automatic routing always picks `vc`. Pin `task:svc` for singing voice conversion.
- **Cloning-only families**: `chatterbox` advertises `clon` and `vc`, and no plain `tts` at
  all, so every `/v1/audio/speech` request against it must carry a reference clip in
  `voice`. Without one the request is refused with `INVALID_ARGUMENT` naming that field,
  rather than failing deep inside the model.

## Acceleration

| Variant | Platform |
|---|---|
| CPU | linux/amd64, linux/arm64 |
| CUDA 12 | linux/amd64 |
| CUDA 13 | linux/amd64 |
| Metal | darwin/arm64 |

There is no ROCm image, because upstream has no HIP build configuration, and no Vulkan
image: it would ship a Vulkan loader with no ICD inside the container. `BUILD_TYPE=vulkan`
still works when building the backend yourself.

+++
disableToc = false
title = "Speaker Diarization"
weight = 33
url = "/features/audio-diarization/"
+++

![Diarization: segment, embed, and cluster (or a single ASR pass) into speaker-labelled segments](/images/diagrams/diarization-pipeline.png)

Speaker diarization answers the question **"who spoke when?"** - given an audio clip with multiple speakers, it returns time-stamped segments labelled with a stable speaker ID (`SPEAKER_00`, `SPEAKER_01`, …).

LocalAI exposes this through the `/v1/audio/diarization` endpoint, modelled after `/v1/audio/transcriptions`. Five backends are supported today:

- **[sherpa-onnx](https://github.com/k2-fsa/sherpa-onnx)** - pyannote-3.0 segmentation + a speaker-embedding extractor (3D-Speaker, NeMo, WeSpeaker) + fast clustering. Pure diarization - no transcription cost. Recommended when you only need speaker turns.
- **[vibevoice.cpp](https://github.com/microsoft/VibeVoice)** - produces speaker-labelled segments as a by-product of its long-form ASR pass, so you can optionally get a transcript per segment for free.
- **[NeMo-Speech.cpp](https://github.com/NVIDIA/NeMo-Speech.cpp)** - NVIDIA Sortformer, served standalone by the [NeMo-Speech.cpp backend]({{%relref "features/nemo-speech-cpp" %}}). It is end to end, so the speaker capacity is fixed by the checkpoint and the count hints are ignored. The same backend can instead put speaker tags on a transcript, by attaching a Sortformer model to an ASR one.
- **[audio.cpp](https://github.com/0xShug0/audio.cpp)** - the `sortformer_diar` family, served by the multi-modality [audio.cpp backend]({{%relref "features/audio-cpp" %}}).
- **[parakeet.cpp](https://github.com/mudler/parakeet.cpp)** - NVIDIA Nemotron-3-Diarization (Sortformer, up to 8 speakers), served standalone or paired with a Parakeet ASR model for per-segment text. See the [Audio to Text]({{% relref "audio-to-text" %}}) page for the parakeet-cpp option reference.

Because diarization is exposed as a regular OpenAI-compatible endpoint, any HTTP client works. There is no Python dependency on pyannote or NeMo on the consumer side.

In distributed mode, LocalAI stages uploaded audio on the remote worker before
running dedicated diarization and releases the staged input after the request.
The worker does not need access to the frontend’s temporary upload directory.

## Endpoint

```
POST /v1/audio/diarization
Content-Type: multipart/form-data
```

| Field | Type | Description |
|-------|------|-------------|
| `file` | file (required) | audio file in any format `ffmpeg` accepts |
| `model` | string (required) | name of the diarization-capable model |
| `num_speakers` | int | exact speaker count when known (>0 forces; 0 = auto) |
| `min_speakers` | int | hint when auto-detecting |
| `max_speakers` | int | hint when auto-detecting |
| `clustering_threshold` | float | cosine distance threshold used when `num_speakers` is unknown |
| `min_duration_on` | float | discard segments shorter than this many seconds |
| `min_duration_off` | float | merge gaps shorter than this many seconds |
| `language` | string | only meaningful for backends that bundle ASR (e.g. vibevoice) |
| `include_text` | bool | when the backend can emit per-segment transcript for free, populate it |
| `include_sounds` | bool | add the closed sound events of the clip as `sounds`. Needs a model with a sound companion; see [Sound events](#sound-events). Default `false` |
| `response_format` | string | `json` (default), `verbose_json`, or `rttm` |

### Response - `json` (default)

Compact payload, no transcription, no per-speaker summary:

```json
{
  "task": "diarize",
  "duration": 12.34,
  "num_speakers": 2,
  "segments": [
    {"id": 0, "speaker": "SPEAKER_00", "label": "0", "start": 0.00, "end": 2.34},
    {"id": 1, "speaker": "SPEAKER_01", "label": "1", "start": 2.34, "end": 4.10}
  ]
}
```

`speaker` is the normalized, zero-padded label clients should display. `label` preserves the raw backend-emitted ID for clients that maintain their own speaker dictionary.

### Response - `verbose_json`

Adds per-speaker totals and (when the backend supports it and `include_text=true`) the per-segment transcript:

```json
{
  "task": "diarize",
  "duration": 12.34,
  "language": "en",
  "num_speakers": 2,
  "segments": [
    {"id": 0, "speaker": "SPEAKER_00", "label": "0", "start": 0.00, "end": 2.34, "text": "Hello, world."},
    {"id": 1, "speaker": "SPEAKER_01", "label": "1", "start": 2.34, "end": 4.10, "text": "How are you?"}
  ],
  "speakers": [
    {"id": "SPEAKER_00", "label": "0", "total_speech_duration": 5.6, "segment_count": 3},
    {"id": "SPEAKER_01", "label": "1", "total_speech_duration": 1.76, "segment_count": 1}
  ]
}
```

### Speaker names

With a parakeet-cpp model that has a `speaker_model:` (or a `speaker_component:`, for a bundle file such as `parakeet-cpp-bundle-standard`) and voices registered through `/v1/voice/register`, segments whose speaker matches a registered voice gain `name` and `name_score` (the cosine similarity of the match), and the matching `speakers` entry gains `name`. Both fields are omitted for a speaker that was not identified, so an unnamed response looks exactly as before. `speaker` stays `SPEAKER_NN`, and RTTM output still uses `SPEAKER_NN`. See [Voice Recognition]({{% relref "voice-recognition" %}}#naming-speakers-in-diarization-and-live-transcription) for the setup and the limits.

```json
{
  "task": "diarize",
  "duration": 12.34,
  "num_speakers": 2,
  "segments": [
    {"id": 0, "speaker": "SPEAKER_00", "label": "0", "start": 0.00, "end": 2.34, "text": "Hello, world.", "name": "Alice", "name_score": 0.82},
    {"id": 1, "speaker": "SPEAKER_01", "label": "1", "start": 2.34, "end": 4.10, "text": "How are you?"}
  ],
  "speakers": [
    {"id": "SPEAKER_00", "label": "0", "name": "Alice", "total_speech_duration": 5.6, "segment_count": 3},
    {"id": "SPEAKER_01", "label": "1", "total_speech_duration": 1.76, "segment_count": 1}
  ]
}
```

### Sound events

With `include_sounds=true` the response gains a `sounds` array: the sound events (AudioSet labels such as `Dog`, `Applause`, `Cough`) found anywhere in the clip, in the same call that returns the speakers, the text and the voice prints. Each item is `{start, end, label, confidence}`: `start` and `end` are in seconds from the start of the audio, and `confidence` is the peak score the tagger reached while the event lasted (0 to 1). Items are sorted by `start`. Both `json` and `verbose_json` carry the array; `rttm` has no place for it and returns 400.

```bash
curl http://localhost:8080/v1/audio/diarization \
  -F model=parakeet-cpp-multilingual-diarization-speakers-sounds \
  -F file=@meeting.wav \
  -F include_text=true -F include_speaker_profiles=true -F include_sounds=true \
  -F response_format=verbose_json
```

```json
{
  "task": "diarize",
  "duration": 31.2,
  "language": "en",
  "num_speakers": 2,
  "segments": [
    {"id": 0, "speaker": "SPEAKER_00", "label": "0", "start": 0.0, "end": 6.4, "text": "Good morning, everyone."},
    {"id": 1, "speaker": "SPEAKER_01", "label": "1", "start": 6.8, "end": 11.2, "text": "Morning."}
  ],
  "speakers": [
    {"id": "SPEAKER_00", "label": "0", "total_speech_duration": 6.4, "segment_count": 1},
    {"id": "SPEAKER_01", "label": "1", "total_speech_duration": 4.4, "segment_count": 1}
  ],
  "speaker_profiles": {"version": 1, "encoder": {"identity": "sha256:...", "dimension": 256}, "speakers": ["..."]},
  "sounds": [
    {"start": 5.5, "end": 6.75, "label": "Cough", "confidence": 0.91},
    {"start": 12.0, "end": 14.5, "label": "Applause", "confidence": 0.68}
  ]
}
```

An empty `sounds` array means the sound model ran and found no event. The field is absent when `include_sounds` is not set. The events come from the same sound stream, with the same on and off thresholds, that a [realtime session]({{% relref "openai-realtime" %}}) runs, so a clip gives the same events offline and live. The thresholds are not request fields.

The model needs a sound companion, a `sound_model:` option pointing at a CED GGUF (the gallery entry `parakeet-cpp-multilingual-diarization-speakers-sounds` has one). Without it the request fails with HTTP 501 and a message that starts with the stable code `include_sounds_unsupported`; the same happens for a backend that cannot report sound events. LocalAI never answers with an empty list in place of that error. The speaker segments, text and voice prints are independent of the sound model and cost nothing extra when `include_sounds` is off.

### Response - `rttm`

NIST RTTM, the standard interchange format used by `pyannote.metrics` / `dscore`:

```
SPEAKER audio 1 0.000 2.340 <NA> <NA> SPEAKER_00 <NA> <NA>
SPEAKER audio 1 2.340 1.760 <NA> <NA> SPEAKER_01 <NA> <NA>
```

Returned as `Content-Type: text/plain; charset=utf-8`.

## Quick start

First install a diarization-capable model from the gallery. The example below uses `vibevoice-cpp-asr`, which serves the vibevoice.cpp backend and returns speaker-labelled segments (and, optionally, a transcript):

```bash
local-ai run vibevoice-cpp-asr
```

```bash
curl http://localhost:8080/v1/audio/diarization \
  -H "Content-Type: multipart/form-data" \
  -F file="@meeting.wav" \
  -F model="vibevoice-cpp-asr" \
  -F num_speakers=3
```

The sections below show how to configure the two supported backends by hand when you want full control over the segmentation and embedding models.

## Backend setup - sherpa-onnx (pure diarization)

Sherpa-onnx needs two ONNX models: pyannote segmentation and a speaker-embedding extractor. Place them under your LocalAI models directory and reference them from the YAML:

```yaml
name: pyannote-diarization
backend: sherpa-onnx
type: diarization
parameters:
  model: sherpa-onnx-pyannote-segmentation-3-0/model.onnx
options:
  - diarize.embedding_model=3dspeaker_speech_campplus_sv_zh-cn_16k-common.onnx
  # Optional clustering knobs (per-call DiarizeRequest fields override these):
  - diarize.threshold=0.5
  - diarize.min_duration_on=0.3
  - diarize.min_duration_off=0.5
known_usecases:
  - FLAG_DIARIZATION
```

Both `model:` and `diarize.embedding_model=` are resolved relative to the LocalAI models directory.

## Backend setup - vibevoice.cpp (diarization + ASR)

vibevoice.cpp's ASR mode emits `[{Start, End, Speaker, Content}]` natively, so a single pass gives both diarization and transcription:

```yaml
name: vibevoice-diarize
backend: vibevoice-cpp
parameters:
  model: vibevoice-asr.gguf
options:
  - type=asr
  - tokenizer=vibevoice-tokenizer.gguf
known_usecases:
  - FLAG_DIARIZATION
  - FLAG_TRANSCRIPT
```

Pass `include_text=true` on the request to populate the `text` field on each diarization segment.

```bash
curl http://localhost:8080/v1/audio/diarization \
  -H "Content-Type: multipart/form-data" \
  -F file="@interview.wav" \
  -F model="vibevoice-diarize" \
  -F include_text=true \
  -F response_format=verbose_json
```

## Backend setup - parakeet-cpp (Nemotron-3-Diarization)

Choose an existing gallery entry for the output you need:

| Output | Gallery entry | Request options |
|---|---|---|
| Speaker turns only | `parakeet-cpp-nemotron-3-diarization` | Default options |
| Speaker turns and transcript | `parakeet-cpp-nemotron-3-diarization-asr` | `include_text=true`, `response_format=verbose_json` |
| Speaker turns, transcript, and identification | `parakeet-cpp-nemotron-3-diarization-asr-speakers` | Same transcript options; explicitly enroll voices for names |
| Multilingual transcript, speakers, voice prints and sound events in one call | `parakeet-cpp-multilingual-diarization-speakers-sounds` | `include_text=true`, `include_speaker_profiles=true`, `include_sounds=true`, `response_format=verbose_json` |

The complete `-asr-speakers` entry downloads Nemotron-3-Diarization, Parakeet TDT+CTC 110M ASR, and the WeSpeaker ResNet34 speaker encoder.
It configures both `asr_model` and `speaker_model`; no custom gallery configuration is needed.
See [Remember speakers in the Web UI](#remember-speakers-in-the-web-ui) for installation and enrollment.

The `parakeet-cpp-multilingual-diarization-speakers-sounds` entry downloads Parakeet TDT 0.6B v3 (25 European languages), Nemotron-3-Diarization, the WeSpeaker ResNet34 speaker encoder and CED-Tiny, and sets `diarization_model`, `speaker_model` and `sound_model`. It answers the whole request above from one model name. See [Sound events](#sound-events).

The entries `parakeet-cpp-bundle-small` and `parakeet-cpp-bundle-standard` hold Nemotron-3-Diarization, an ASR model and the WeSpeaker speaker encoder in one file (`diar_component:diar` and `speaker_component:voice`), so one install serves the transcript and the identification options above. See [Bundle GGUF files]({{% relref "audio-to-text" %}}#bundle-gguf-files-several-models-in-one-file).

For manual configuration, this example pairs Sortformer with ASR:

```yaml
name: parakeet-diarize
backend: parakeet-cpp
parameters:
  model: nemotron-3-diarization-q8_0.gguf
options:
  - asr_model:tdt_ctc-110m-f16.gguf
known_usecases:
  - diarization
```

Getting text on each segment needs both: an `asr_model` companion loaded on the model, and `include_text=true` on the request. With only one of the two, segments carry no text and no error is raised. Sortformer has a fixed speaker capacity and no clustering stage, so `num_speakers`, `min_speakers`, `max_speakers` and `clustering_threshold` are ignored (logged at debug); `min_duration_on` and `min_duration_off` are honored. Speaker labels are the decimal index the model assigned (`"0"`, `"1"`, …), or `"unknown"` when a segment has no diarized speaker.

```bash
curl http://localhost:8080/v1/audio/diarization \
  -H "Content-Type: multipart/form-data" \
  -F file="@meeting.wav" \
  -F model="parakeet-diarize" \
  -F include_text=true \
  -F response_format=verbose_json
```

Sortformer clusters on voice-like characteristics, not on "is this a human". A loud non-speech sound with voice-like pitch and rhythm (a rooster crow, in one test clip) can come back as its own speaker segment alongside the real speakers. This is model behavior, not a bug in the LocalAI integration: treat an unexpected extra speaker as a hint the clip may contain a non-speech sound, and use [Sound Classification]({{% relref "audio-classification" %}}) to confirm what it is.

## Notes

- **Speaker identity across files**: speaker IDs (`SPEAKER_00`, `SPEAKER_01`, …) are local to each request. To track the same person across multiple recordings, combine `/v1/audio/diarization` with `/v1/voice/embed` (speaker embedding) and maintain your own embedding store.
- **Hints vs. forces**: `num_speakers` overrides clustering when set; `min_speakers` / `max_speakers` are advisory and only honored by backends that expose a range hint. vibevoice.cpp and parakeet-cpp (Sortformer) ignore them - the model picks the count itself.
- **Sample rate**: input is automatically converted to 16 kHz mono via ffmpeg before the backend sees it; sherpa-onnx pyannote-3.0 requires 16 kHz.

## See also

- [Sound Classification]({{% relref "audio-classification" %}}) - tag non-speech sound events (alarms, glass breaking, baby cry) in a clip.

### Backend profile transport

The parakeet backend supports opt-in speaker profile export through the internal
`DiarizeRequest.include_speaker_profiles` field. This native transport underpins
HTTP profile export and explicit enrollment through `POST /v1/voice/register`,
as described in [Portable speaker enrollment](#portable-speaker-enrollment) below.
It requires a configured `speaker_model` and a library
with `parakeet_capi_diarize_profiles_pcm_json`; an empty recognition registry
is supported. Export does not register anyone. With `include_text` and a loaded
ASR companion, one profile-capable diarization supplies all speaker slots,
profiles, names, and intervals. Timestamped ASR words are assigned to those
same slots; the backend does not run a second diarization. Either inference
failure fails the request. If no ASR companion is loaded, the existing fallback
applies: the response includes profiles and diarization segments without text.
A loaded ASR companion without the timestamped PCM API returns an explicit error.

`DiarizeResponse.speaker_profiles_json` carries the native version-1
`speaker_profiles` object, including original clean preview intervals and one
embedding per usable speaker. Normal requests retain their existing output.
Profile `speaker` values are raw native slot IDs. Match their decimal string to
segment `label` or speaker-summary `label`, not to normalized `SPEAKER_NN`,
array position, or display name. Slots can be sparse, and profile order can
differ from transcript order. Profiles retain their original clean intervals
even when transcript segments use word boundaries or duration filters.
These vectors are sensitive biometric data: callers must authorize export and
explicit enrollment separately.

The internal backend Status response supplies `speaker_encoder`, derived from
the loaded encoder's SHA-256 identity and dimension. Enrollment code must use
`backend.ModelSpeakerEncoder` with server-selected model configuration and
validate profiles against that result, never against caller-provided metadata.
Unavailable metadata or unsupported export fails closed. Renaming a GGUF does
not change its identity; modifying or quantizing its bytes does.

Recognition replay carries registration IDs separately from display names.
Distinct IDs with the same display name remain independent native entries,
and both offline and realtime matches are translated back to display names.
Legacy transport clients without IDs retain name-keyed behavior. The native
registry's aggregation defaults are unchanged. LocalAI's recognition registry
remains global and in-memory; this adds neither persistence nor automatic
registration and is unrelated to persistent TTS voice cloning.

## Portable speaker enrollment

Profile-capable parakeet models can export one biometric embedding per discovered
speaker, including when the recognition registry is empty. Export is opt-in:

```bash
curl http://localhost:8080/v1/audio/diarization \
  -F model=parakeet-diarization -F file=@conversation.wav \
  -F include_speaker_profiles=true -F include_text=true \
  -F response_format=verbose_json
```

The `/audio/diarization` alias has the same protection. With user authentication,
export additionally requires the **voice-recognition** permission. Existing model
access controls still apply. Without opt-in, `speaker_profiles` is omitted.
Both `json` and `verbose_json` support profiles; `rttm` with profiles returns 400.
`include_text=true` retains supported transcripts in either JSON format.
Unsupported profile backends return 501 rather than silently omitting profiles.

Alternatively send `Content-Type: application/json`:

```json
{
  "model": "parakeet-diarization",
  "file": "<raw base64 audio bytes>",
  "include_speaker_profiles": true,
  "include_text": true,
  "response_format": "verbose_json"
}
```

The `speaker_profiles` response object contains `version: 1`,
`encoder: {"identity": "sha256:<64 lowercase hex digits>", "dimension": N}`,
and `speakers`. Each speaker contains:

- `speaker`: the raw numeric speaker slot;
- `clean_duration`: retained clean speech in seconds;
- `intervals`: `{start, end}` ranges in seconds in the original recording;
- `unavailable_reason`: null for usable profiles, otherwise a reason string;
- `embedding`: one vector for a usable speaker, omitted when unavailable.

**UI association:** convert each profile's numeric `speaker` to a decimal string
and match segment/summary `label`. Do not use `SPEAKER_NN`, array position, or
human name. Slots may be sparse and out of order; display names may repeat.
Preview `intervals` against the original audio, not separated audio. Disable
saving unavailable profiles. Enrollment is explicit, never automatic; only
relabel after a successful registration response. See
[portable voice registration](/features/voice-recognition/#portable-profile-registration).

Profiles are sensitive, unsigned biometric data, not proof of identity or consent.
Do not log their vectors. Obtain the speaker's consent before enrollment.

API tracing excludes the entire exchange for `/v1/audio/diarization`, its
`/audio/diarization` alias, and `/v1/voice/register` before capturing bodies.
This also protects JSON base64 audio when profile export is off. These routes
produce no in-memory or persisted API trace; other routes keep their existing
tracing behavior. External proxies and client logs must apply the same privacy
policy. Existing trace files from older versions are not retroactively scrubbed.

## Remember speakers in the Web UI

Use a LocalAI build with portable enrollment support and a profile-capable `parakeet-cpp` backend.
The backend needs the profile APIs from merged upstream commit
[`bee7c14`](https://github.com/mudler/parakeet.cpp/commit/bee7c14dfcc23613df58176c59a40459e7b47095) or a compatible later build.
Installing the model weights alone does not update an older backend.

1. Open **Models → Explore** and search for `parakeet-cpp-nemotron-3-diarization-asr-speakers`.
2. Select **Install** and wait for installation to complete. Check **Operate → Activity** for progress or errors.
3. Open **Studio → Diarization** (or `/app/diarization`). Select that model and upload your recording.

Obtain the speaker's consent before enrollment. To remember a speaker from that recording:

1. Select **Prepare speakers to remember**, then select **Diarize**. This
   requests profiles, transcript text, and speaker summaries. Use a
   profile-capable parakeet-cpp model configured with a speaker encoder.
2. In **Speakers**, select **Preview 1**, **Preview 2**, or another available
   interval to listen to clean speech from the original recording. Playback
   stops at the end of that interval. **Stop preview** stops it earlier.
   Your browser must support the recording's audio format.
3. For an unknown speaker, select **Name and remember**. Enter a name and
   select **Remember**. No second recording or audio upload is needed.
4. After the server confirms registration, the name appears on all turns for
   that speaker. A failed save keeps the entered name so you can retry.

Upload another recording and select **Diarize** to match remembered voices.
You can turn off **Prepare speakers to remember**; recognition does not require another profile export.
Matches show their names; unmatched speakers keep their speaker labels.
With preparation off, the UI requests speaker turns without transcript text.
Use the API example below to request text without exporting profiles.

Speakers without a usable profile cannot be
remembered; try longer speech without overlapping speakers. Duplicate names
are allowed: each save creates a separate registration, not a merged voice.
Changing the model or recording clears the current results and save dialog.
A save already sent to the server can still complete, but cannot rename turns
in a different recording.

The page requires the **Audio Diarization** permission and access to the selected model. Preparing profiles and
remembering speakers additionally require **Voice Recognition**. Users without
that permission can still run normal diarization. If the backend does not
support profiles, the page reports an error: choose a compatible model or
turn off **Prepare speakers to remember**. It does not silently retry without
profiles.

{{% notice warning %}}
Remembered voices are shared globally on this server and are lost when it
restarts. Nothing is enrolled automatically. The browser stores only the new
registration's ID, name, and registration time for the existing voice
management list, not its embedding or recording. That list is local to the
browser and is not a durable server registry.
{{% /notice %}}

Use **Manage remembered voices**, then the **Enrollment** tab, to see or
remove registrations saved in this browser. Clean-clip voice enrollment stays
available there and does not require diarization.

### API example: install, export, and remember

This example uses the same complete gallery entry and requires `curl` and `jq`.
The commands assume a local server without authentication.
If authentication is enabled, add `-H "Authorization: Bearer <key>"` to every request using your authorized key.
Keep keys out of shared scripts, logs, and shell history; see [Authentication]({{% relref "authentication" %}}).
Installation requires model-management access; inference and enrollment require the permissions described above.

Install the model if it is not already installed:

```bash
LOCALAI=http://localhost:8080
MODEL=parakeet-cpp-nemotron-3-diarization-asr-speakers
curl --fail-with-body "$LOCALAI/models/apply" \
  -H 'Content-Type: application/json' \
  -d '{"id":"localai@parakeet-cpp-nemotron-3-diarization-asr-speakers"}'
```

Installation is asynchronous. Wait for successful completion in **Operate → Activity** before continuing.
API clients can query the returned job `status` URL; see the [model gallery API]({{% relref "model-gallery" %}}).

{{% notice warning %}}
Exported profiles contain biometric vectors. Obtain consent before enrollment.
Keep the recording, response, and registration files private. Do not log or share their contents.
Use a new private directory so existing files cannot retain broader permissions. Delete these files when no longer needed.
{{% /notice %}}

Export profiles and transcript text from your recording, keeping the complete JSON response:

```bash
umask 077
WORK=$(mktemp -d)
curl --fail-with-body "$LOCALAI/v1/audio/diarization" \
  -F "model=$MODEL" -F file=@conversation.wav \
  -F include_text=true -F include_speaker_profiles=true \
  -F response_format=verbose_json > "$WORK/diarization.json"

# Inspect raw slots, clean intervals, and transcript labels without printing vectors.
jq '.speaker_profiles.speakers[] | {speaker, clean_duration, intervals, unavailable_reason}' \
  "$WORK/diarization.json"
jq '.segments[] | {label, start, end, text}' "$WORK/diarization.json"
```

Choose a usable raw `speaker` slot whose decimal string matches the intended segment `label`.
Listen to its `intervals` in the original recording before assigning a name.
Do not select by array position, normalized `SPEAKER_NN`, or display name.
If `unavailable_reason` indicates insufficient speech, try a longer recording without overlapping speakers.

Replace `0` below with your chosen raw slot. Zero is valid, but does not mean “the first array element.”
Keep the complete `speaker_profiles` object unchanged:

```bash
SLOT=0
NAME=Ada
jq --arg model "$MODEL" --arg name "$NAME" --argjson slot "$SLOT" \
  '{model: $model, name: $name, speaker_slot: $slot, speaker_profiles: .speaker_profiles}' \
  "$WORK/diarization.json" > "$WORK/register.json"
curl --fail-with-body "$LOCALAI/v1/voice/register" \
  -H 'Content-Type: application/json' \
  --data-binary @"$WORK/register.json"
```

After successful registration, submit another recording with the same model:

```bash
curl --fail-with-body "$LOCALAI/v1/audio/diarization" \
  -F "model=$MODEL" -F file=@next-conversation.wav \
  -F include_text=true -F response_format=verbose_json > "$WORK/next.json"
jq '.segments[] | {label, name, start, end, text}' "$WORK/next.json"

# Remove private example outputs when no longer needed.
rm -f "$WORK/diarization.json" "$WORK/register.json" "$WORK/next.json"
rmdir "$WORK"
```

Matching speakers can now carry `name`, even though this request omits `include_speaker_profiles`.
Keep `include_text=true` and `verbose_json` when you want transcript text.
Recognition is not proof of identity. Registrations remain global and disappear on server restart.
See [portable profile registration](/features/voice-recognition/#portable-profile-registration) for encoder compatibility and validation rules.

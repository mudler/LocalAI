+++
disableToc = false
title = "NeMo-Speech.cpp backend"
weight = 39
url = "/features/nemo-speech-cpp/"
+++

[NeMo-Speech.cpp](https://github.com/NVIDIA/NeMo-Speech.cpp) is NVIDIA's Apache-2.0
C++/ggml runtime for the Nemotron Speech models. LocalAI exposes it through the native
`nemo-speech-cpp` backend, which serves four model families from one installed backend:
transcription, speaker diarization, speech synthesis and text translation.

NeMo-Speech.cpp is developed by NVIDIA, not by the LocalAI project.

## Installing

```bash
local-ai backends install nemo-speech-cpp
```

Or install it from the **Backends** page in the web UI. `nemo-speech-cpp` is a
preference-only backend: LocalAI never picks it automatically during model import,
because the family is decided by the GGUF's `general.architecture` key, which cannot be
read from a remote repository, and because a translation model carries an ordinary LLM
architecture with no NeMo-specific marker at all. Set `backend: nemo-speech-cpp` in the
model YAML, or select it explicitly in the import form.

## How the family is chosen

The backend reads `general.architecture` from the GGUF at load time and picks the family
from it. Nothing else in the config selects it.

| `general.architecture` | Family | Serves |
|---|---|---|
| `asr` | Transcription | `/v1/audio/transcriptions`, the same endpoint with `stream=true`, and the realtime live-transcription path |
| `sortformer` | Diarization | `/v1/audio/diarization` |
| `magpietts` | Text to speech | `/v1/audio/speech`, `/tts`, including streamed audio |
| `nemo-nano-codec`, `vad`, `pnc` | *(none)* | Auxiliary assets. Loading one directly is refused. |
| anything else | Translation | `/v1/chat/completions` and `/v1/completions` |

Two rows need explaining.

**The auxiliary architectures** are converted NeMo components that attach to a primary
model and cannot run on their own. Pointing `parameters.model` at one fails the load with
a message naming the option it belongs on: the codec belongs on `codec_model` of a
`magpietts` model, the VAD on `vad_model` of an `asr` model, the punctuation model on
`pnc_model` of an `asr` model.

**Everything else is translation**, and that is deliberate rather than a fallback that
happens to catch it. Riva-Translate GGUFs are produced by llama.cpp's converter and carry
an ordinary LLM architecture such as `qwen3`, so there is no NeMo-specific string to match
on. Selecting this backend explicitly is the signal that the model is meant for it.

A request for the wrong family is refused with `UNIMPLEMENTED` naming the family the model
was loaded as, rather than failing somewhere inside the runtime.

## Model YAML

Options are `key:value` entries in the `options:` list, split on the **first** colon so a
value may contain more. Every path option is resolved relative to the models directory
when it is not absolute. An unknown key is ignored rather than rejected, so a config
written for a newer backend still loads on an older one.

### Transcription

```yaml
name: nemotron-asr
backend: nemo-speech-cpp
parameters:
  model: asr.gguf
known_usecases:
  - FLAG_TRANSCRIPT
options:
  # All optional. Each attaches a converted NeMo component to the recognizer.
  - vad_model:vad.gguf
  - pnc_model:pnc-bert-base-en.q8_0.gguf
  - itn_dir:sparrowhawk_grammars
  - diar_model:diarization.gguf
  - language_code:en-US
  # GPU device index. Omit it, or set -1, to run on the CPU.
  - gpu:0
```

Attaching `diar_model` is what turns on per-word speaker tags: the backend asks for
diarization only when the recognizer was created with a diarization model, because the
runtime refuses a request for it otherwise. Transcript segments are then cut at each
change of speaker. Without it there is a single unlabelled segment. Word-level timings are
returned only when the request asks for them with
`timestamp_granularities[]=word`, following the OpenAI contract.

`language_code` is the model-level default. A per-request `language` wins over it, and
both may be left empty, which the runtime reads as the model's own default.

### Diarization

A `sortformer` model is standalone: this pipeline has no ASR in it, so segments carry
speaker labels and timings but no text.

```yaml
name: sortformer-diarization
backend: nemo-speech-cpp
parameters:
  model: diarization.gguf
known_usecases:
  - FLAG_DIARIZATION
options:
  - gpu:0
```

Sortformer is end to end and its speaker capacity is fixed by the checkpoint, so
`num_speakers`, `min_speakers`, `max_speakers` and `clustering_threshold` have nothing to
map onto and are logged and dropped. `include_text` is dropped for the same reason: there
is no ASR in this pipeline. `min_duration_on` and `min_duration_off` are honoured. To get
speaker labels *on a transcript*, use an `asr` model with `diar_model` instead.

### Text to speech

```yaml
name: magpie-tts
backend: nemo-speech-cpp
parameters:
  model: magpie-tts/magpietts.gguf
known_usecases:
  - FLAG_TTS
options:
  # Both are required, and both are auto-discovered when unset (see below).
  - codec_model:magpie-tts/nanocodec.gguf
  - tokenizer_dir:magpie-tts/extracted
  # Optional Sparrowhawk text-normalization grammars applied to the input text.
  - tn_dir:tts_grammars
  - language_code:en-US
  - gpu:0
```

`codec_model` and `tokenizer_dir` are both required, and both are discovered from the
model's own directory when they are not set: a sibling file whose name contains
`nanocodec` or `nano-codec` becomes the codec, and a sibling directory named `extracted`
becomes the tokenizer directory. If discovery finds nothing, the load fails naming the
option to set. That is a hard error rather than a warning because the alternative is a
synthesizer that loads and emits noise.

Voices are selected by the request's `voice` field. A non-negative integer is used as a
speaker index; anything else is passed through as a voice name. MagpieTTS conditions on a
speaker, not on a prose style, so `instructions` has nothing to map onto and is logged and
ignored. Per-request `params` are read for `seed`, `steps`, `top_k`, `temperature` and
`cfg_scale`; anything else is left at the synthesizer's configured value.

The TTS runtime has a three-way CPU/CUDA/auto preference rather than a device index, so
`gpu` with a non-negative value means "let the runtime choose" here, and any negative
value (`-1` is the default) pins it to the CPU.

### Translation

```yaml
name: riva-translate
backend: nemo-speech-cpp
parameters:
  model: translate.q8_0.gguf
known_usecases:
  - FLAG_COMPLETION
  - FLAG_CHAT
options:
  - source_language:en
  - target_language:de
  - gpu:0
```

Both flags run through the same pair of RPCs, `Predict` and `PredictStream`, which is why
both are listed for this backend in `core/config/backend_capabilities.go`. `FLAG_COMPLETION`
is the plain shape: prompt in, translation out. `FLAG_CHAT` adds three things, and each
turn of the conversation is translated on its own: the model becomes eligible as the
default chat model when a request names none, it appears in the web UI's chat model
picker, and it stays visible under the gallery's Chat filter (`completion` is not a
gallery filter, `chat` is). Neither flag gates a request that names the model explicitly.

### Option reference

| Option | Family | Default | Meaning |
|---|---|---|---|
| `vad_model:<path>` | ASR | unset | Converted Silero VAD GGUF, attached to the recognizer. |
| `pnc_model:<path>` | ASR | unset | Punctuation and capitalization BERT GGUF. |
| `diar_model:<path>` | ASR | unset | Sortformer GGUF. Setting it is the opt-in that enables per-word speaker tags. |
| `itn_dir:<path>` | ASR | unset | Sparrowhawk grammar directory for inverse text normalization, or a parent directory whose children are named per language (`en`, `es`, …). Linux only, see the limitations below. |
| `language_code:<code>` | ASR, TTS | unset | Model-level default language. A per-request `language` overrides it. |
| `codec_model:<path>` | TTS | auto-discovered | NanoCodec GGUF. Required. |
| `tokenizer_dir:<path>` | TTS | auto-discovered | Directory holding the extracted MagpieTTS tokenizer assets. Required. |
| `tn_dir:<path>` | TTS | unset | Sparrowhawk text-normalization grammars applied to the input text. Linux only, see the limitations below. |
| `source_language:<code>` | Translation | unset | Default source language. May be overridden per request. |
| `target_language:<code>` | Translation | unset | Default target language. A request with neither this nor a per-request override is refused. |
| `gpu:<n>` | all | `-1` (CPU) | Device index. `-1` selects the CPU. A value that is not an integer is ignored with a warning and the model runs on the CPU. |

## Translating

Translation is reached through the text endpoints: the prompt is the text to translate,
and the languages come from `source_language` and `target_language`.

```bash
curl http://localhost:8080/v1/chat/completions -H "Content-Type: application/json" -d '{
  "model": "riva-translate",
  "messages": [{"role": "user", "content": "The quick brown fox jumps over the lazy dog."}]
}'
```

A leading `[src->tgt]` prefix on the prompt overrides the configured pair for that one
request:

```json
{"role": "user", "content": "[en->zh-cn] The quick brown fox jumps over the lazy dog."}
```

Either side may be left out to keep the model-level default for it, so `[->de]` changes
only the target. Three-segment pair tags such as `en-zh-cn`, `en-pt-br` and `zh-tw-en`
parse correctly. The prefix is stripped before the text reaches the model.

The C API takes a source and a target language and has no free-form generation entry
point, so there is no prompt in the LLM sense. Sampling parameters, tools, grammars and
attached media have no equivalent and are ignored; the structural ones (`grammar`,
`tools`, `images`, `videos`, `audios`, `negative_prompt`, `logprobs`) are named in the log
when a request sets them. Streaming works, but the runtime returns the finished
translation in one piece, so the whole result arrives as a single chunk rather than token
by token.

## Streaming transcription

Streaming transcription and the realtime live path both emit a delta per **finalized**
utterance. Interim hypotheses are produced by the runtime and deliberately dropped: the
wire contract defines `delta` as newly finalized text that consumers concatenate, and the
runtime rewrites a final rather than extending its interims (it runs ITN and formatting
stripping on the final only). Forwarding interims would assemble "he", "hell", "hello",
"Hello." into `hehellhelloHello.` rather than into the transcript, and no diffing trick
recovers it.

The cost is latency: the first delta of an utterance arrives at its endpoint rather than
mid-word. That is the trade this backend takes.

## Acceleration

| Variant | Platform |
|---|---|
| CPU | linux/amd64, linux/arm64 |
| CUDA 12 | linux/amd64 |
| CUDA 13 | linux/amd64 |
| Vulkan | linux/amd64, linux/arm64 |
| NVIDIA Jetson (L4T), CUDA 12 | linux/arm64 |
| NVIDIA Jetson (L4T), CUDA 13 | linux/arm64 |
| Metal | darwin/arm64 |

There is **no AMD ROCm and no Intel SYCL** support, because upstream NeMo-Speech.cpp has
no HIP and no SYCL backend, so there is nothing to build against. A host reporting either
capability gets the CPU build, which is the honest answer rather than a broken image. If
you want NeMo ASR on an AMD or Intel GPU, use
[parakeet-cpp](https://github.com/mudler/parakeet.cpp) instead, which is described on the
[Audio to text]({{%relref "features/audio-to-text" %}}) page.

## Limitations

- **Text normalization is Linux only.** The macOS build ships without it: the
  Sparrowhawk/OpenFST stack assumes a GNU toolchain, and the gcc-12 pin it needs (OpenFST's
  templates fail to compile on gcc-13 and gcc-14 at `-O2`) has no macOS equivalent. One
  build flag covers both directions, so on macOS **both** `itn_dir` (inverse text
  normalization on ASR output) and `tn_dir` (text normalization on TTS input) do nothing.
  A `tn_dir` set on a macOS build logs a warning and carries on with normalization
  disabled. `pnc_model` is unaffected: punctuation is always compiled in.
- **Interim streaming results are suppressed**, as described above. This costs latency.
- **Translation runs at the library's default limits**: 1024 tokens of context and 256
  new tokens per call. Neither is configurable from the model YAML, because both are
  create-time settings on the translator and raising the context costs one context-sized
  KV cache per pooled context. The two limits fail differently. Input longer than the
  context is **rejected**, with `nmt: prompt too long (N tokens) for context 1024`
  surfacing as a failed request, so you will know. Output longer than 256 tokens is
  **silently cut**: generation simply stops at the limit and the truncated translation is
  returned as if it were complete. Translate a sentence or a paragraph at a time rather
  than a whole document.
- **There are no gallery entries yet.** Models have to be converted with upstream's
  converter and configured by hand, as below. This is a follow-up, not an oversight.

## Converting models

Upstream has a single conversion entry point for every family. `SOURCE` may be a `.nemo`
archive, an extracted NeMo checkpoint, a local Hugging Face directory, or a Hugging Face
repository ID.

```bash
git clone https://github.com/NVIDIA/NeMo-Speech.cpp
cd NeMo-Speech.cpp
pip install -r requirements.txt

python3 convert_model.py nvidia/nemotron-speech-streaming-en-0.6b \
    --outfile /models/asr.gguf
```

Copy the resulting GGUF into your models directory and write the YAML above against it.

Text to speech is the one family that needs more than a single conversion. It loads **two**
GGUFs, the MagpieTTS token generator and the NanoCodec decoder, and it needs the tokenizer
assets that live inside the MagpieTTS `.nemo` archive rather than in the GGUF. Extract the
archive and keep the extracted directory next to the converted model, so that
`tokenizer_dir` (or the `extracted/` auto-discovery) has something to point at, and put the
codec GGUF in the same directory so `codec_model` (or its auto-discovery) finds it.

```bash
# 1. MagpieTTS: download the .nemo, extract it for the tokenizer, convert it
hf download nvidia/magpie_tts_multilingual_357m --revision v2602 \
    --local-dir /models/magpie-tts
mkdir -p /models/magpie-tts/extracted
tar -xf /models/magpie-tts/magpie_tts_multilingual_357m.nemo \
    -C /models/magpie-tts/extracted
python3 convert_model.py /models/magpie-tts/extracted \
    --outfile /models/magpie-tts/magpietts.gguf

# 2. NanoCodec: no tokenizer, it is a codec decoder. The filename carries
#    "nanocodec" so the auto-discovery in the YAML above picks it up.
hf download nvidia/nemo-nano-codec-22khz-1.89kbps-21.5fps \
    --local-dir /models/magpie-tts/nano-codec
python3 convert_model.py \
    /models/magpie-tts/nano-codec/nemo-nano-codec-22khz-1.89kbps-21.5fps.nemo \
    --outfile /models/magpie-tts/nanocodec.gguf
```

Skipping the codec leaves the model unloadable: the backend fails with
`no NanoCodec GGUF found next to ...` naming the `codec_model` option.

Translation additionally uses the pinned llama.cpp converter, which has to be initialized
first:

```bash
git submodule update --init llama.cpp
pip install -r llama.cpp/requirements/requirements-convert_hf_to_gguf.txt
python3 convert_model.py nvidia/Riva-Translate-4B-Instruct-v2 \
    --outfile /models/translate.q8_0.gguf --outtype q8_0
```

See upstream's [model conversion
guide](https://github.com/NVIDIA/NeMo-Speech.cpp/blob/main/docs/model-conversion.md) for
the per-architecture defaults and options.

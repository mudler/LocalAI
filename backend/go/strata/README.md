# strata (experimental)

A LocalAI backend for the [Strata](https://github.com/Niko1221/Strata) inference engine (MIT).
It starts the engine in `--serve` mode as a child process and speaks its stdin/stdout line
protocol. The backend is pure Go (`CGO_ENABLED=0`). It does not build or ship the engine.

Status: experimental. The unit tests run against a stub engine that speaks the same line protocol,
including `LP` lines and `GENI`. The notes under "Verified on hardware" say what was run on a real engine.

## What you need

- A Strata engine executable (`strata`) built for your GPU, from a Strata release or a source build.
- For images: the `strata-vision` program from the same engine package, and the model's mmproj file.
- A Strata source checkout. The backend uses three of its Python files for tokenization, the chat
  template and output parsing (see "Tokenizer, template and parser").
- Python 3 with the `regex` and `jinja2` packages (and `pillow` and `numpy` if the Strata modules you
  use import them).
- A Strata model directory. See "Model files".

## Configuration

Set these in the model config under `options:` as `key:value`. Set `use_tokenizer_template: true`
in the model's `template:` block so that LocalAI passes structured messages and tools; the backend then
renders the prompt with the model's own chat template.

| Option | Required | Meaning |
|---|---|---|
| `engine_path` | yes (or env `STRATA_ENGINE_PATH`) | Path to the engine executable. |
| `strata_root` | yes (or env `STRATA_ROOT`) | Strata source checkout. Must contain `tools/strata_tokenizer.py` and `serve/frontend.py`. |
| `python` | no | Python executable. Default `python3`. |
| `pack` | no | Pack directory, when it is not the model directory itself or its `pack/` subdirectory. |
| `native`, `native_dense_gguf`, `ple_gguf`, `embd_gguf`, `expert_profile`, `mtp` | no | Files the engine reads next to the pack. Each becomes the engine flag of the same name (`--native`, `--native-dense-gguf`, `--ple-gguf`, `--embd-gguf`, `--expert-profile`, `--mtp`). A relative path is looked up in the pack directory, the models directory and the pack's parent. |
| `ple_io`, `mmap_experts` | no | `--ple-io <mode>` and `--mmap-experts` (`true`). |
| `engine_args` | no | Extra engine arguments, split on spaces. Placed last, so they can override the options above. |
| `env` | no | `KEY=VALUE` for the engine process. Repeat the option for more. LocalAI's `environment` map is passed too. |
| `mmproj`, `vision_exe` | no | Image support: the mmproj file and the `strata-vision` program. `vision_exe` defaults to `strata-vision` next to the engine, or env `STRATA_VISION_PATH`. Images also need `native`, because the encoder reads the text model's vocabulary. `vision: off` disables it. |
| `vision_gpu`, `vision_threads`, `vision_max_tokens` | no | Encoder settings: run on the GPU (`true`), CPU threads (default: the model's `threads`), and the most image tokens per image. |
| `media_marker` | no | The placeholder LocalAI puts in message text for each image. Default `<__media__>`. |
| `ready_timeout` | no | Go duration. How long to wait for `READY`. Default `15m`. |
| `stop_timeout` | no | Go duration. How long to wait for the engine to end a request after `STOP` before it is killed. Default `30s`. |
| `eos_ids` | no | Comma-separated end-of-turn token ids. Default: the ids of `<|endoftext|>` and `<|im_end|>` in the tokenizer. |

Model options LocalAI sends and what they do: `context_size` becomes `--max-context`; `main_gpu` becomes
`--gpu` (the engine sets `CUDA_VISIBLE_DEVICES` from it); `threads` is used for the image encoder only (the
engine sizes its own CPU pool). `gpu_layers`, `tensor_split`, `f16`, `mmap`, `mlock`, rope and cache-type
settings are ignored: the engine decides placement from the free memory, and the knobs it has are
engine arguments.

The engine is started as `<engine_path> --pack <pack> [file flags] [--max-context N] [--gpu N] [--vision] [engine_args] --serve`.

## Model files

A Strata model is not one file. The directory LocalAI points at holds, or the options name:

- the pack directory (`tokenizer/`, `index.txt`, `native_experts.txt`, ...), made by Strata's setup or
  `tools/iq_pack.py` from the GGUF shards;
- the GGUF shards: `native` is the first shard, `ple_gguf` the shard with the n-gram table when it is
  not found by itself;
- optionally the MTP draft layer (`mtp`), an expert profile, and for images the mmproj file.

`Load` fails when no `tokenizer/vocab.json` is found, so the backend does not capture unrelated models
during backend probing.

Example:

```yaml
name: qwen-flash-next
backend: strata
parameters:
  model: qwen-flash-next          # a directory under the models path
context_size: 32768
template:
  use_tokenizer_template: true
options:
  - engine_path:/opt/strata/engine/strata
  - strata_root:/opt/strata
  - native:Qwen3.8-Flash-Next-GSQ-RCO-IQ2_XS-00001-of-00002.gguf
  - ple_gguf:Qwen3.8-Flash-Next-GSQ-RCO-IQ2_XS-00002-of-00002.gguf
  - mmproj:mmproj-Qwen3.8-Flash-Next-BF16.gguf
  - vision_exe:/opt/strata/engine/strata-vision
  - mmap_experts:true
```

## Features

| LocalAI feature | How it works here |
|---|---|
| Chat template, system and tool messages | Strata's own `openai_to_messages` and `ChatTemplate`, so the prompt ids match the Strata server. |
| Reasoning split | Strata's `OutputParser` runs on the generated text. Reasoning goes to `ChatDelta.reasoning_content`, the answer to `content`. `enable_thinking` and `reasoning_effort` in the request metadata reach the template. |
| Tool calls | The same parser. Calls stream as `ToolCallDelta` (name first, then argument chunks); a non-streaming reply carries the whole call. Parser recovery for drifted call formats is on. |
| `tool_choice` | `none` drops the tools. `required` and a named function write the opening of the call into the prompt, so the model can only continue it. The engine has no grammar, so this turns thinking off for that request. An unknown value or a name that is not among the tools acts as `auto`. |
| Logprobs, top logprobs | `logprobs=K` on the request line (K is `top_logprobs`, at most 20). The values are the model's own distribution before sampling, penalties and temperature. They come back as JSON in `Reply.logprobs` on the last reply of a stream. |
| Images | Each image is encoded by `strata-vision` and sent with `GENI`. See "Images". |
| Stop strings | Matched on the raw text; the engine is sent `STOP` and the text is cut. |
| Sampling | `temperature`, `top_p`, `top_k` (at most 64), `min_p`, `seed`, repeat, frequency and presence penalty. |
| Usage | `Reply.tokens` and `prompt_tokens` are exact (the prompt count includes image rows). `timing_prompt_processing` and `timing_token_generation` are the engine's milliseconds. |
| Finish reason | `Reply` has no field for it. LocalAI derives `length` from the token count. The engine's reason is in the debug log. |
| Cancel | A cancelled request sends `STOP` and the next request runs normally. |
| `TokenizeString`, `Detokenize` | `TokenizeString` renders the template when the request has messages. Image rows are not counted. |
| `Status` | `READY`/`BUSY`/`ERROR`, and memory: resident size of the engine and encoder processes. VRAM is not reported by the engine; on unified-memory machines the resident size includes what the GPU uses. |
| Embeddings | Not supported; returns an error. |

### Images

`Load` starts the engine with `--vision` when `mmproj` and the encoder are configured. The encoder
(`strata-vision`) starts with the first image, not at load, and stays up. For each image the backend
decodes it (base64, a `data:` URL or an http(s) URL; JPEG, PNG, BMP and GIF pass through, WebP is
converted), asks the encoder for its embeddings, widens the template's single `<|image_pad|>` to one per
image row, and writes the embeddings of all images of the request to one file named in the `GENI` line.
Encoded images are cached by content (64 kept), because chat clients send earlier images again each turn.
The EXIF orientation of a JPEG is not applied. Video and audio are refused.

## Not supported

- Embeddings, grammar, `logit_bias`, mirostat, typical-p, tail-free sampling, video and audio input.
- A forced tool call together with thinking (see `tool_choice`).
- A tool call cut off by the token limit is delivered as far as it was written, with incomplete arguments.
- One engine and one request at a time. `--batch` and `--slots` are not used.

## Protocol assumptions

Checked against upstream commit `9f7d977d` (v0.2.11), `src/program/generate.cpp`, `--serve` block.
The protocol has no version field. A later upstream change can break this backend without any
signal; re-check on each bump.

- Engine to client: `READY <ctx> [stop] [multi=N]` once after load. Then per request:
  `T <id>` per token, `LP <logprob> [id:logprob ...]` after each token when `logprobs=K` was sent,
  `DONE <produced> <prompt_tokens> <prompt_ms> <decode_ms> <finish> <accepted> <offered> <resumed> ...`,
  or `ERR <message>`. Other lines (`INFO`, `PP`, ...) are skipped.
- Client to engine: `GEN <max_new> [key=value ...] <id,id,...>`, or `GENI <max_new> [key=value ...] <embeddings file> <id,id,...>`
  for images. Ids are comma-separated. Sampling keys sit between `max_new` and the ids. Absent keys mean
  greedy decoding. The engine requires prompt + `max_new` + 8 to fit the context; the backend leaves that room.
  `STOP` cancels the request in flight; the engine then still prints `DONE` (finish `cancel`),
  and the backend reads to it so the stream stays in step. `QUIT` leaves the serve loop.
- The engine prints the end-of-turn token as an ordinary `T` line and finishes with `stop`.
  The backend drops ids in `eos_ids` from the output.
- One request at a time. The engine's `--batch` mode is not used.

Mapped sampling fields: `temperature`, `top_p`, `top_k` (capped at 64, an engine limit), `min_p`,
`seed`, `penalty_repeat` (from `Penalty`), `penalty_freq`, `penalty_present`, `penalty_last_n`
(from `Repeat`, default 64 when any penalty is set). Sampling keys other than the penalties are sent only
when temperature is above zero. `max_new` is `Tokens`, capped to the room left in the context.
Stop strings are matched in Go, then the engine is sent `STOP`.

## Lifetime of the engine

- `Load` waits for `READY` up to `ready_timeout`. If the engine exits first, the error includes the
  last lines of its stderr.
- On Linux the engine gets `PDEATHSIG=SIGKILL`, tied to a pinned OS thread so it fires when the
  backend process dies, not when some Go thread exits. Closing stdin also ends the serve loop.
- `Free` sends `QUIT`, closes stdin, and kills the engine after 10 s.
- A cancelled gRPC stream sends `STOP`. If the engine does not finish within `stop_timeout`, it is
  killed and the next request fails with an "engine exited" error until the model is reloaded.
  `Status` reports `ERROR` when the engine is dead.

## Tokenizer, template and parser

The engine takes token ids only. Strata keeps the tokenizer (byte-level BPE, `qwen35`
pre-tokenizer), the Jinja chat template and the output parser (reasoning, tool calls) in Python. The
backend defines a Go interface (`Tokenizer`: `Encode`, `TokenBytes`, `Prompt`, `NewParser`) and
implements it by running a small helper, `tokenizer_helper.py`, that imports Strata's own
`tools/strata_tokenizer.py` and `serve/frontend.py` (`openai_to_messages`, `ChatTemplate`, `OutputParser`,
`forced_call`). The helper talks JSON lines on stdin/stdout: `prompt` renders and encodes a chat request
(including the "literal think and control tags in user text stay text" marking the Strata server does),
and `parser_*` runs the output parser, one call per decoded token.

Why this and not a Go port:

- The pre-tokenizer regex uses `\s+(?!\S)`, a lookahead that Go's RE2 does not support. A faithful
  port needs a custom splitter or a regex library with lookahead, plus a parity test against the
  Python tokenizer over a large corpus.
- The chat template is the model's own Jinja. Strata renders it in a sandbox with custom filters
  and applies message rewrites first (empty assistant turns dropped, think-literal marking, tool
  result ordering). A Go port needs a Jinja engine and those rewrites.
- Reusing the Python code gives the same ids as the Strata server for the same prompt.

The helper sends the full vocabulary once at start (base64 token bytes), so decoding runs in Go
with no per-token round trip, and a character split across tokens is held until complete.

What is left for a native Go tokenizer: the pre-tokenizer, the BPE merge loop, special-token
matching, and a template engine (for example `gonja`) plus Strata's message rewrites. About 3 to
5 days with a parity corpus; the template part is the risky half.

Differences from the Strata server: `PromptEncoder` caching is not used, the model-card sampling
defaults are not applied (LocalAI's own defaults and the request decide), and the reasoning budget and
answer-room limits of the server are not applied.

## Known gaps

- Protocol is unversioned (see above). `engine_args` is not validated.
- The Python helper needs a Strata source tree and Python packages next to the backend image. The
  backend image alone is not enough to run a model, and it does not contain the engine.
- No model gallery entry.
- CI builds Linux amd64 CUDA 12 and 13 images only. No CPU, ROCm, Darwin or arm64 build.
- Split or multi-GPU layouts and the drafter options are reachable only through `engine_args`, untested.

## Tests

```
go test ./backend/go/strata/
```

The tests run this test binary as a stub engine and as a stub image encoder (see `stub_engine_test.go`
and `features_test.go`). They cover the `READY` timeout, early exit, streaming, `STOP` on cancel, an
engine that ignores `STOP`, a crash mid stream, `ERR` lines, stop strings, UTF-8 splits, reasoning and
tool-call deltas (streaming and not), forced tool calls, logprobs, `GENI` with image rows, option
mapping and the load checks. `TestHelperTokenizer` runs the real Python helper, with the real
Strata parser, and is skipped unless `STRATA_ROOT` and `STRATA_TEST_PACK` (a directory with `tokenizer/`)
are set.

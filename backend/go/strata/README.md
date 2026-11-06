# strata (experimental)

A LocalAI backend for the [Strata](https://github.com/Niko1221/Strata) inference engine (MIT).
It starts the engine in `--serve` mode as a child process and speaks its stdin/stdout line
protocol. The backend is pure Go (`CGO_ENABLED=0`). It does not build or ship the engine.

Status: spike. Text generation works against a stub engine in the unit tests. It has not been
run against a real engine, a GPU, or a real model pack.

## What you need

- A Strata engine executable (`strata`) built for your GPU.
- A Strata source checkout. The backend uses two of its Python files for tokenization and the
  chat template (see "Tokenizer and chat template").
- Python 3 with the `regex` and `jinja2` packages.
- A Strata model pack directory that contains `tokenizer/` (`vocab.json`, `merges.txt`,
  `token_type.json`, `tokenizer.json`, `chat_template.jinja`).

## Configuration

Set these in the model config under `options:` as `key:value`.

| Option | Required | Meaning |
|---|---|---|
| `engine_path` | yes (or env `STRATA_ENGINE_PATH`) | Path to the engine executable. |
| `strata_root` | yes (or env `STRATA_ROOT`) | Strata source checkout. Must contain `tools/strata_tokenizer.py` and `serve/frontend.py`. |
| `python` | no | Python executable. Default `python3`. |
| `engine_args` | no | Extra engine arguments, split on spaces. Placed after `--pack`/`--max-context`. |
| `ready_timeout` | no | Go duration. How long to wait for `READY`. Default `15m`. |
| `stop_timeout` | no | Go duration. How long to wait for the engine to end a request after `STOP` before it is killed. Default `30s`. |
| `eos_ids` | no | Comma-separated end-of-turn token ids. Default `248044,248046` (Qwen family). |

The model pack directory is the model file (`model_file`, else `models path` + `model`). `context_size`
becomes `--max-context`. `Load` fails when `engine_path` is unset or the directory has no
`tokenizer/vocab.json`, so the backend does not capture unrelated models during backend probing.

The engine is started as `<engine_path> --pack <pack> [--max-context N] [engine_args] --serve`.

## Protocol assumptions

Checked against upstream commit `9f7d977d` (v0.2.11), `src/program/generate.cpp`, `--serve` block.
The protocol has no version field. A later upstream change can break this backend without any
signal; re-check on each bump.

- Engine to client: `READY <ctx> [stop] [multi=N]` once after load. Then per request:
  `T <id>` per token, `LP ...` after a token when logprobs are asked for (ignored), `DONE <produced> <prompt_tokens> <prompt_ms> <decode_ms> <finish> ...`,
  or `ERR <message>`. Other lines (`INFO`, `PP`, ...) are skipped.
- Client to engine: `GEN <max_new> [key=value ...] <id,id,...>`. Ids are comma-separated.
  Sampling keys sit between `max_new` and the ids. Absent keys mean greedy decoding.
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

## Tokenizer and chat template

The engine takes token ids only. Strata keeps the tokenizer (byte-level BPE, `qwen35`
pre-tokenizer) and the Jinja chat template in Python. The backend defines a Go interface
(`Tokenizer`: `Encode`, `TokenBytes`, `ApplyChatTemplate`) and implements it by running a small
helper, `tokenizer_helper.py`, that imports Strata's own `tools/strata_tokenizer.py` and
`serve/frontend.py` (`ChatTemplate`). The helper talks JSON lines on stdin/stdout.

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

Differences from the Strata server: the prompt is encoded with control tokens parsed, but the
server's "literal think tags in user text stay plain text" marking is not applied, and
`PromptEncoder` caching is not used.

## Known gaps

- No embeddings (explicit error).
- No vision: `GENI`, images, video and audio input are refused. The engine needs `--vision` and
  precomputed image embeddings from a separate encoder.
- No tool-call parsing. Tools are passed to the chat template, but model output comes back as raw
  text. Strata parses its tool-call format in `serve/server.py`; that is not ported. No reasoning
  split either.
- No grammar, logit bias, mirostat, typical-p or tail-free sampling. Grammar is refused; the rest
  is ignored.
- No logprobs (the engine's `logprobs=` key and `LP` lines are not wired).
- Model directory: one pack directory only. Packs that spread across several paths, `--native`
  presets, split or multi-GPU layouts and the drafter options are reachable only through
  `engine_args`, untested.
- Protocol is unversioned (see above). `engine_args` is not validated.
- One engine and one request at a time. `--batch` and `--slots` are not used.
- The Python helper needs a Strata source tree and Python packages next to the backend image. The
  backend image alone is not enough to run a model.
- No model gallery entry: the licence of the weights has not been checked.
- CI builds Linux amd64 CUDA 12 and 13 images only. No CPU, ROCm, Darwin or arm64 build.

## Tests

```
go test ./backend/go/strata/
```

The tests run this test binary as a stub engine (see `stub_engine_test.go`). They cover the `READY`
timeout, early exit, streaming, `STOP` on cancel, an engine that ignores `STOP`, a crash mid
stream, `ERR` lines, stop strings, UTF-8 splits and the load checks. `TestHelperTokenizer` runs
the real Python helper and is skipped unless `STRATA_ROOT` and `STRATA_TEST_PACK` are set.

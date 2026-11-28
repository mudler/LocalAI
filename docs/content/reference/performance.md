+++
disableToc = false
title = "Request and streaming overhead"
weight = 55
description = "The CPU time and the memory allocations that LocalAI itself spends per request and per streamed token, on the chat, Anthropic and Open Responses endpoints, and how to measure them."
keywords = ["performance", "benchmark", "overhead", "streaming", "time to first byte", "CPU", "allocations"]
+++

This page shows what LocalAI itself costs when it serves a request, apart from
the model. The cost is CPU time and memory allocations in the LocalAI process,
for each streamed token and for each request. On a large host this cost does
not limit throughput. On a small host (a Jetson, an N100 mini PC, a laptop) the
same CPU time is taken from the model, so it matters there.

The serving speed of llama.cpp itself (for example paged serving) is on the
[engine runtime settings]({{% relref "/models/engine-settings" %}}) page.

## What LocalAI does for each request and each token

For each request, before the first token, LocalAI:

- reads the JSON body and finds the model configuration;
- applies the defaults of the configuration and checks it;
- renders the chat template, or converts the messages for the backend when the
  model uses the tokenizer template of the backend;
- builds the grammar when the request offers tools and the model uses one;
- opens a gRPC stream to the backend.

For each token that the backend streams, LocalAI:

- receives one gRPC message;
- splits reasoning text from the answer (the backend can also do this, see
  [Two backend modes](#two-backend-modes));
- when the request offers tools, looks for a tool call in the output;
- encodes one server-sent event as JSON and writes it to the client.

## Results

The numbers below compare LocalAI before and after the streaming changes of
November 2026. The backend is the mock backend of the test suite, which streams
as fast as LocalAI reads. Everything the process spends is therefore LocalAI's
own work.

### Per streamed token, 16 concurrent streams

| Endpoint | Reply | CPU per token | Allocations per token |
|---|---|---|---|
| `/v1/chat/completions` | 500 tokens | 19.8 µs → 11.3 µs | 24 → 15.5 |
| `/v1/chat/completions` | 200 reasoning + 300 answer tokens | 18.2 µs → 11.8 µs | 24 → 15.5 |
| `/v1/chat/completions` with tools | 300 tokens, then a tool call | 206 µs → 18.7 µs | 1004 → 31 |
| `/v1/messages` | 500 tokens | 16.1 µs → 11.1 µs | 21 → 11 |
| `/v1/messages` with tools | 300 tokens, then a tool call | 2695 µs → 18.5 µs | 14206 → 23 |
| `/v1/responses` | 500 tokens | 18.9 µs → 12.2 µs | 22 → 12 |
| `/v1/responses` with tools | 300 tokens, then a tool call | 1255 µs → 20.2 µs | 6744 → 28 |

With one stream the cost per token is similar. Before the changes, the cost
per token with tools grew with the length of the output. After the changes,
a long answer costs about the same per token as a short one.

The largest gap between two chunks (p99) with tools was 5 ms to 12 ms. It is
now about 0.15 ms.

### Two backend modes

Most llama.cpp models run in one or both of these modes:

- **Autoparser.** The backend splits reasoning, answer and tool calls itself
  and sends them as separate deltas (ChatDeltas). LocalAI does not need to
  parse the text.
- **Tokenizer template.** With `use_tokenizer_template: true`, the backend
  renders the chat template. LocalAI sends the messages instead of a prompt.

Per streamed token with the autoparser, 16 concurrent streams, on a model with
`backend: llama-cpp` and `use_tokenizer_template: true`:

| Endpoint | Reply | CPU per token | Allocations per token | Chunk gap p99 |
|---|---|---|---|---|
| `/v1/chat/completions` | 500 tokens | 23.1 µs → 9.7 µs | 26 → 17 | 221 µs → 75 µs |
| `/v1/chat/completions` | 200 reasoning + 300 answer tokens | 21.7 µs → 10.2 µs | 24 → 17 | 295 µs → 70 µs |
| `/v1/chat/completions` with tools | 300 tokens, then a tool call | 23.5 µs → 8.9 µs | 27 → 18 | 167 µs → 62 µs |
| `/v1/messages` | 500 tokens | 18.7 µs → 11.6 µs | 24 → 14 | 159 µs → 117 µs |
| `/v1/messages` with tools | 300 tokens, then a tool call | 2155 µs → 10.9 µs | 13929 → 15 | 124 ms → 0.1 ms |
| `/v1/responses` | 500 tokens | 22.2 µs → 14.2 µs | 24 → 14 | 170 µs → 143 µs |
| `/v1/responses` with tools | 300 tokens, then a tool call | 896 µs → 12.4 µs | 6509 → 15 | 69 ms → 0.08 ms |

### Per request, and time to first byte

One request at a time, with a reply of one token. The CPU time and the
allocations are for the whole request.

| Endpoint | CPU per request | Allocations per request | Time to first byte (p50) |
|---|---|---|---|
| `/v1/chat/completions` | 1100 µs → 650 µs | 883 → 415 | 0.85 ms → 0.42 ms |
| `/v1/chat/completions`, `backend: llama-cpp` | 1850 µs → 650 µs | 1015 → 419 | 1.69 ms → 0.47 ms |
| `/v1/chat/completions` with tools | 1950 µs → 800 µs | 2773 → 1046 | 1.28 ms → 0.58 ms |
| `/v1/chat/completions`, tokenizer template | 1050 µs → 500 µs | 870 → 373 | 0.92 ms → 0.37 ms |
| `/v1/chat/completions` with tools, tokenizer template | 1800 µs → 500 µs | 2299 → 576 | 1.11 ms → 0.39 ms |
| `/v1/messages` | 950 µs → 500 µs | 871 → 410 | 0.58 ms → 0.21 ms |
| `/v1/responses` | 1200 µs → 750 µs | 952 → 446 | 0.87 ms → 0.44 ms |

With `backend: llama-cpp`, LocalAI asked the backend for the model metadata on
every request of a model without a multimodal projector. It now asks once for
each running backend process.

## What changed

- The tool-call parse skips the output while the output cannot hold a call: a
  call needs a `<`, `{` or `[`. After such a byte, the parse skips prose bytes
  that cannot start JSON. The parsers of the preset formats are built once.
- The reply is no longer copied in full for each token. The usage counter keeps
  only the last event of a stream, not the whole stream.
- The chat endpoint encodes each event into a reused buffer. It writes the
  events that are ready together, with one flush. It never waits for an event
  that is not ready.
- For each request, LocalAI reuses the GPU probe for 30 seconds, looks up the
  model instead of listing all models, and compiles the backend name pattern
  once.

The streams are byte for byte the same as before. The test suite compares
them for each endpoint (`tests/e2e/stream_golden_test.go`).

## Measure it yourself

1. Build LocalAI, the mock backend and the load tool:

   ```sh
   make build
   make build-mock-backend
   go build -o streamload ./scripts/bench/cmd/streamload
   ```

2. Add a model that uses the mock backend. Put an empty file `bench.bin` in the
   models directory, and this file as `bench.yaml`:

   ```yaml
   name: bench
   backend: mock-backend
   context_size: 32768
   parameters:
     model: bench.bin
   ```

3. Start LocalAI with the mock backend and the metrics endpoint. Set the same
   `GOMAXPROCS` for every run that you compare:

   ```sh
   GOMAXPROCS=4 ./local-ai run --models-path ./models \
     --external-grpc-backends mock-backend:$PWD/tests/e2e/mock-backend/mock-backend
   ```

4. Run the load tool on the same host. It reads the CPU time of the server from
   `/proc`, so it runs on Linux only:

   ```sh
   GOMAXPROCS=2 ./streamload -url http://127.0.0.1:8080 -pid $(pgrep -x local-ai) \
     -model bench -endpoint chat -c 16 -r 8 -spec "n=500" -tokens 500
   ```

   The tool prints one JSON line: CPU time and allocations per token, the time
   to first byte, and the gaps between chunks. It reads the allocation counters
   from `/metrics` and sends no API key, so run the server without API keys.

The `-spec` value goes to the mock backend in the prompt, after the
`MOCK_BENCH` marker:

| Field | Effect |
|---|---|
| `n=<count>` | Stream `<count>` answer tokens. |
| `reasoning=<count>` | Stream `<count>` reasoning tokens first. |
| `tool=1` | End with a tool call, streamed in 4-byte pieces. |
| `deltas=1` | Send the text as ChatDeltas, as the llama.cpp autoparser does. |
| `msg=1` | With `deltas=1`, also put the answer text in the message, as the llama.cpp backend does. |
| `calls=1` | With `deltas=1` and `tool=1`, send the tool call as tool-call deltas. |

Set `-tokens` to the number of tokens that the backend sends, so that the tool
can divide by it. Use `-endpoint` with `chat`, `chat-tools`, `anthropic`,
`anthropic-tools`, `responses` or `responses-tools`.

To test the tokenizer template, add `template: {use_tokenizer_template: true}`
to the model. The mock backend then reads the marker from the last message.

## Limits of these numbers

- The backend is a mock. A real model streams much more slowly, so a real
  server spends less CPU time on LocalAI for each second. The cost for each
  token is the same.
- The host was an x86 machine with other work on it. On another host, and with
  another load, the numbers change. Compare two builds on the same host, one
  after the other, with the same `GOMAXPROCS`.
- The CPU time has a resolution of 10 ms, so measure at least a few seconds of
  load.
- The mock backend sends fixed words. Text with many numbers or quoted strings
  costs more in the tool-call parse.

# vLLM streaming + tool-parser benchmark

A small, self-contained Python script (stdlib only) that measures
time-to-first-token (TTFT) for the vLLM backend's streaming path with
a tool parser configured.

## Why this exists

When a vLLM tool parser is active and a streaming chat completion is requested,
LocalAI used to buffer the full generation to prevent raw tool-call markup
(e.g. `<tool_call>...`) from leaking as `delta.content`. That was correct
for tool-call responses, but it turned plain-text responses into effectively
non-streaming — the client received nothing until the model finished.

With native parser-side streaming (`parser.extract_tool_calls_streaming`,
implemented by every concrete vLLM 0.23+ tool parser), each delta can be
classified per-token: emit as content, emit as a structured tool_call, or
suppress. This benchmark shows the difference.

## Two scenarios

| Scenario | Request | Expected outcome |
|---|---|---|
| `tool_call` | "What is the weather in Paris? Please use the tool." | Model calls `get_weather`. `delta.tool_calls` chunks; no content leak. |
| `plain_text` | "Explain in 3 short sentences what a hash table is. Do NOT call any tool." | Model writes prose. With the streaming parser, content streams progressively; without it, the entire response arrives in one chunk. |

## What the script reports

For each scenario, across N runs:

- `ttf_content_s` — time until the first `delta.content` chunk
- `ttf_tool_s` — time until the first `delta.tool_calls` chunk
- `n_content_chunks` — total number of distinct content deltas (1 = bundled, >1 = streamed)
- `n_tool_chunks` — total tool_call deltas
- `total_s` — total wall-clock until `[DONE]`
- `finish_reason` — `tool_calls` / `stop` / `length`

## Usage

```bash
python ttft_streaming_tool_parser.py --url http://localhost:8080 --model my-coder --runs 3
```

JSON results are written to `ttft_bench_<label>.json` (default label: `run`).

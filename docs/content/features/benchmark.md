+++
title = "Benchmark text models"
weight = 35
+++

Use `local-ai benchmark` to compare text inference through a running LocalAI
server. Install and configure the models first. The command sends sequential,
non-streaming requests to `/v1/chat/completions` and reports full request latency
and end-to-end completion tokens per second.

Pass one or more configured model names. To compare backends, configure separate
model aliases with the desired backend, then pass those aliases. Backend names
alone are not model names. The command does not install, discover, or unload
models; the server's normal loading and eviction settings still apply.

For example, with models named `text-llama-cpp` and `text-vllm` configured:

```sh
local-ai benchmark text-llama-cpp text-vllm --runs 5 --warmup 1 \
  --prompt 'Explain how a rainbow forms.' --max-tokens 128
```

To save settings, per-model summaries, and every measured sample:

```sh
local-ai benchmark text-llama-cpp text-vllm --json > benchmark.json
```

For an authenticated server, set `LOCALAI_API_KEY` or `API_KEY` in the environment.
The API key is excluded from the JSON report. Use `--endpoint` for a remote server
or reverse proxy:

```sh
local-ai benchmark text-llama-cpp --endpoint https://localai.example.org/proxy/v1
```

The endpoint accepts a server root, an optional `/v1` suffix, and trailing
slashes. A reverse proxy path prefix is preserved. Redirects are refused.
Credentials in the URL, query strings, and fragments are rejected.

## Arguments and flags

| Argument or flag | Default | Description |
|---|---|---|
| `MODEL ...` | Required | One or more configured text model names. |
| `--endpoint` | `http://127.0.0.1:8080` | Server URL, optionally ending in `/v1`. |
| `--api-key` | Unset | API key; also reads `LOCALAI_API_KEY`, then `API_KEY`. |
| `--prompt` | `Explain why the sky is blue.` | Nonblank user message repeated for every request. |
| `--max-tokens` | `128` | Positive maximum number of completion tokens per request. |
| `--runs` | `3` | Positive number of measured requests per model. |
| `--warmup` | `1` | Unmeasured requests before each model; zero disables warmups. |
| `--timeout` | `5m` | Positive timeout per request, including reading its response. |
| `--json` | `false` | Write JSON instead of a table. |
| `-h`, `--help` | | Show command help. |

Every request sets temperature to `0` and streaming to `false`. Interrupting the
command cancels the active request. A failed request stops the benchmark with a
model and run error; results are written only after every model succeeds.

## Reading the results

Each model has minimum, mean, and maximum latency across measured requests.
Latency runs from sending the request through parsing the complete response.
It includes transport, queueing, prompt processing, generation, and response
parsing. This command does not measure time to first token.

End-to-end completion tokens per second is the sum of server-reported completion
tokens divided by the sum of full request durations. It is not decode-only speed
or a substitute for `llama-bench` kernel measurements. If any measured response
omits completion token usage, throughput is `null` in JSON and `N/A` in the table.
Reported zero tokens remain zero. Missing prompt or completion counts remain
`null` in each JSON sample; the command never estimates tokens from text length.

Warmups run separately for each model and do not appear in measurements. They can
absorb model loading time, but repeated prompts can also benefit from prompt
caching. With `--warmup 0`, measured requests can include model loading. Other
clients and server queueing can affect results; compare under similar load.

Keep hardware, quantization, context size, backend settings, and prompt consistent
when comparing engines. Different model tokenizers can report different token
counts for the same text, and models can stop before `--max-tokens`. Temperature
zero does not guarantee identical output across models or engines. The JSON
settings describe the benchmark requests, not the server's full model
configuration; record that configuration alongside the report.

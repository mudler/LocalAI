+++
title = "Traffic"
weight = 50
aliases = ["/operations/traffic/"]
+++

The **Traffic** tab of the Administration console answers three questions: how much is
the server used, what failed, and how are the machine and the models doing. It
opens on an overview and has a second row of links: Overview, Usage, Models,
GPU and host, Traces, Middleware and Prometheus. One time window (24 hours, 7
days, 30 days or all time) is shared by the Overview, Usage and Models pages.

Every figure comes from a record LocalAI really keeps, and each page says which.
Where LocalAI keeps no record, the page leaves the figure out and does not draw
a zero.

| Record | What it holds | Pages that read it |
|---|---|---|
| Usage ledger | Requests and tokens, by model, user and API key, in hourly, daily or monthly buckets | Overview, Usage, Models |
| Trace buffer | The most recent API requests, with status, error and duration. Off until tracing is on | Overview (failed requests, p95), Traces |
| Backend-operation buffer | Loads and runs of each model, with their errors | Models, a trace |
| Resources reading | Memory per GPU, system memory, CPU share, models disk | GPU and host |

## Overview

Five figures in a row: requests, failed requests, latency p95, tokens in and
tokens out. Under them, three charts: requests per bucket, failed requests (the
trace buffer, in twelve columns) and tokens in and out per bucket. Each chart has
a data table behind it, and you can read a value with the arrow keys. A table of
the five busiest models closes the page.

- **Failed** counts a transport error or a 5xx answer. A 4xx answer is the caller
  being refused, and LocalAI does not count it as a failure.
- **p95** is the 95th percentile of request duration in the trace buffer. LocalAI
  does not compute a p50 or a p99, so there are none.
- With tracing off, the failed and p95 figures say so and link to the setting.
  The ledger figures do not depend on tracing.
- The trace summary reports at most 7 days. For the 30-day and all-time windows,
  failed requests and latency cover the last 7 days, and the page says so.

## Usage

The ledger as a table, grouped by model, by user (admins) or by API key (when
authentication is on). Rows sort by any column, can be searched, and can be
filtered by model. A row opens in place on its own chart. A user who is not an
admin sees only their own numbers and their quotas, and the page tells them
whether the current pace stays inside each quota.

**Export CSV** and **Export JSON** save the rows the table holds. The export runs
in the browser. The ledger does not record status, endpoint or node, so those are
not groups. Estimated cost is optional: you type a price per million tokens, it
stays in your browser, and LocalAI has no price of its own.

## Models

One row per model: requests and tokens from the ledger, failed operations and the
mean operation time from the backend-operation buffer, and the memory the backend
process holds now. That is the resident host memory of the process, when the
server reports it. GPU memory per model is not reported, and neither is a
per-model latency percentile or a memory history.

## GPU and host

The current reading, refreshed every 5 seconds: memory per GPU, system memory,
the CPU share and the models disk. LocalAI does not report GPU utilisation or
temperature. Two charts show readings the page took itself, **since you opened
this page**: the memory pool and the CPU share. They are kept in the browser, at
most 240 readings, and leaving the page empties them. On a cluster the page also
lists every node with its memory and CPU share.

## Traces

The recent API requests and backend operations, with the tracing settings above
the list. Filter by failed or slow requests, search, sort, export and clear.
Opening an API request shows its page: the status and the error LocalAI recorded,
a timeline of the request with the backend operations that ran while it was open,
and the request and response bodies. Bodies stay closed until you press
**Reveal**, because they can hold prompts and personal data. Request headers are
never listed. The two buffers share no request id, so operations are matched on
time and model, and the page says so. A trace leaves the buffer when newer
requests push it out; its page then says it is no longer there.

With tracing off, the page explains what is lost and offers **Turn on tracing**.
You can also start LocalAI with `LOCALAI_ENABLE_TRACING=true`.

## OpenTelemetry export

The trace buffer above is local to the process. To ship traces to an
OpenTelemetry collector, set `OTEL_TRACES_EXPORTER=otlp`:

```bash
export OTEL_TRACES_EXPORTER=otlp
export OTEL_EXPORTER_OTLP_ENDPOINT=https://otel.example.com:4318
export OTEL_EXPORTER_OTLP_CERTIFICATE=/etc/ssl/certs/example-ca.pem
export OTEL_SERVICE_NAME=localai
```

Export is off unless the variable asks for `otlp`, so a stock install makes no
outbound connection. `OTEL_SDK_DISABLED=true` turns it off again. The usual
OTLP variables apply: `OTEL_EXPORTER_OTLP_PROTOCOL` (`http/protobuf` by
default, or `grpc`), `OTEL_EXPORTER_OTLP_HEADERS`,
`OTEL_RESOURCE_ATTRIBUTES`, `OTEL_TRACES_SAMPLER` and
`OTEL_TRACES_SAMPLER_ARG`.

The protocol and the port must agree. The default is HTTP on port `4318`; a
gRPC collector on `4317` needs `OTEL_EXPORTER_OTLP_PROTOCOL=grpc`.

For HTTP, a generic `OTEL_EXPORTER_OTLP_ENDPOINT` is a base URL and `/v1/traces`
is appended to its path (`https://host/otel` posts to `/otel/v1/traces`). An
`OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` is the complete URL and is used as given.

TLS is the default. An endpoint without a scheme is treated as `https`, and
`OTEL_EXPORTER_OTLP_INSECURE=true` is only accepted for a loopback collector:
LocalAI refuses to send traces in cleartext to another host rather than
downgrade silently.

Two kinds of span are produced:

- a server span for each API request, named `METHOD /route`, with the HTTP
  method, route, URL path, client address, user agent and response status;
- a `gen_ai.chat <model>` client span for each backend call, with
  `gen_ai.operation.name`, `gen_ai.provider.name`, `gen_ai.request.model`, the
  sampling parameters, `gen_ai.usage.input_tokens` and
  `gen_ai.usage.output_tokens`, the duration and, when streaming, the time to
  first token. A prompt-only call made in the background — moderation, prompt
  compression, title generation — is named `gen_ai.text_completion <model>`
  instead, so it is not mistaken for a chat turn.

An inbound W3C `traceparent` is continued, so LocalAI's spans join the trace a
gateway already started. That is the direction out: LocalAI does not inject
`traceparent` into calls to remote models.

Prompts, completions, tool arguments and reasoning tokens are never exported,
and neither are error messages: a failed span carries an error status and the
error type, not the text, because a backend error can echo request content.
The spans carry metadata only: the bodies stay in the local trace buffer, which
is enabled separately by `LOCALAI_ENABLE_TRACING`. When it is on, the server
and backend spans carry `localai.exchange_id`, the id that finds the matching
entry under **Traces**; when it is off, the attribute is simply absent.

## Middleware

The order a request passes through: Proxy, Admission, Filtering, Routing, Model.
The server fixes that order. Selecting a step shows only its rules. See
[Middleware]({{% relref "/administration/middleware" %}}) for what each step does.

## Prometheus

`GET /metrics` is admin only and returns the Prometheus text format. The page
shows the URL and a scrape configuration with a copy button, checks the endpoint
against the running server, and lists the metrics LocalAI can export. Only
`api_call` is always present: a histogram of request duration in seconds by HTTP
method and route. The others appear while the feature behind them runs. Metrics
are off when LocalAI runs with `LOCALAI_DISABLE_METRICS_ENDPOINT=true`.

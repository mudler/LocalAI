---
title: "Context compression"
description: "Configure automatic compression for long chat histories"
---

Context compression is an opt-in, per-model policy for chat requests that approach
the model context limit. The configuration is disabled by default and does not
change existing requests unless `enabled` is true.

```yaml
name: long-context-chat
backend: llama-cpp
parameters:
  model: chat-model.gguf
compression:
  enabled: true
  trigger_at_ratio: 0.75
  keep_tail_tokens: 8000
  max_summary_tokens: 2048
  compressor_model: fast-summarizer
  on_post_compression_overflow: drop_oldest_summary
```

The chat middleware counts the request before inference. Requests below the configured
ratio pass through unchanged. Requests above it replace the oldest complete turns with
a system summary while retaining the newest messages and keeping assistant tool calls
with their tool results.

Token counts use a conservative byte-level upper-bound estimate so compression never downloads a
tokenizer vocabulary in the request path. Tool schemas and the configured maximum
completion length are included in the context budget.

- `trigger_at_ratio` selects the fraction of `context_size` that starts compression.
- `keep_tail_tokens` protects the newest part of the conversation from compression.
- `max_summary_tokens` limits the generated summary.
- `compressor_model` selects a secondary model. An empty value selects the primary model.
- `on_post_compression_overflow` selects `drop_oldest_summary` or `error` when the compressed request still exceeds the context limit.

When omitted, `trigger_at_ratio` defaults to `0.75`, `keep_tail_tokens` to `2048`,
`max_summary_tokens` to `512`, and `on_post_compression_overflow` to `error`.

Compression applies to `/v1/chat/completions`, `/chat/completions`, and the LocalAI
MCP chat-completion routes. Non-streaming responses include `usage.compression_meta`.
Streaming responses include the same metadata in the trailing usage chunk when the
request sets `stream_options.include_usage`.

Compression is not supported with `cloud-proxy` passthrough mode because LocalAI
cannot safely rewrite an opaque provider payload. Configure cloud proxy translation
mode to use context compression.

The compressor model must be installed and configured. If `compressor_model` is empty,
LocalAI uses the primary model. A compressor failure returns an error instead of sending
an over-limit request to the primary model. The `drop_oldest_summary` overflow policy
removes up to two existing summary messages; if the request still does not fit, LocalAI
returns HTTP 413.

The `/metrics` endpoint exports `localai_compression_events_total`,
`localai_compression_ratio`, and `localai_compression_duration_seconds`.

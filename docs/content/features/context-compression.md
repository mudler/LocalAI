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

The fields reserve the model-level contract used by the chat request compressor:

- `trigger_at_ratio` selects the fraction of `context_size` that starts compression.
- `keep_tail_tokens` protects the newest part of the conversation from compression.
- `max_summary_tokens` limits the generated summary.
- `compressor_model` selects a secondary model. An empty value selects the primary model.
- `on_post_compression_overflow` selects `drop_oldest_summary` or `error` when the compressed request still exceeds the context limit.

{{% notice warning %}}
The configuration schema is available, but the request-compression middleware is
still under development. Enabling it does not transform requests yet.
{{% /notice %}}

+++
title = "PII analyze and redact API"
date = 2026-06-18
description = "The PII detection pipeline becomes a standalone service, callable without routing a chat request through the middleware."
url = "/blog/pii-analyze-redact-api/"
+++

The PII detection pipeline (NER plus restricted-regex pattern tiers) is now reachable directly, without routing a chat request through the middleware:

- `POST /api/pii/analyze` returns the detected entity spans.
- `POST /api/pii/redact` returns the sanitised text, or `400 pii_blocked`.

Events also gain an `origin` field (`middleware`, `proxy`, `pii_analyze`, `pii_redact`), so `/api/pii/events` can be filtered by which surface produced them.

See [Middleware]({{% relref "operations/middleware" %}}#analyze--redact-api). Shipped in [PR #10360](https://github.com/mudler/LocalAI/pull/10360).

+++
title = "Model capabilities endpoint"
date = 2026-07-05
description = "GET /v1/models/capabilities reports what each model can do and which modalities it accepts, so clients stop guessing from backend names."
url = "/blog/model-capabilities-endpoint/"
+++

`GET /v1/models/capabilities` is a new endpoint: an additive superset of `/v1/models` that reports each model's `capabilities` alongside its `input_modalities` and `output_modalities` (`text`, `image`, `audio`, `video`).

The practical effect is that a client can decide where to send an attachment by asking the server, instead of pattern-matching on backend names. Modalities are either inferred by LocalAI or declared explicitly in the model config.

Because the endpoint is additive, existing `/v1/models` consumers are unaffected.

See [API discovery]({{% relref "features/api-discovery" %}}#model-capabilities). Shipped in [PR #10687](https://github.com/mudler/LocalAI/pull/10687).

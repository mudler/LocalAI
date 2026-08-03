+++
title = "Distributed mode hardening"
date = 2026-06-12
description = "Prefix-cache-aware routing, a production-ready request router, ds4 layer-split inference, NATS JWT auth with TLS/mTLS, and resumable uploads."
url = "/blog/distributed-mode-hardening/"
+++

Distributed mode picked up a round of production hardening:

- [Prefix-cache-aware routing](https://github.com/mudler/LocalAI/pull/10071), so requests sharing a prompt prefix land on the replica that already holds it.
- [A production-ready request router with auto-sized embedding and rerank batches](https://github.com/mudler/LocalAI/pull/10104).
- [ds4 layer-split distributed inference](https://github.com/mudler/LocalAI/pull/10098).
- [NATS JWT auth plus TLS/mTLS](https://github.com/mudler/LocalAI/pull/10159).
- [Resumable file uploads](https://github.com/mudler/LocalAI/pull/10109).

See [Distributed inferencing]({{% relref "features/distributed_inferencing" %}}).

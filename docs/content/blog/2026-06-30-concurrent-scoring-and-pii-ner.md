+++
title = "Concurrent scoring and PII NER on llama.cpp"
date = 2026-06-30
description = "Score and TokenClassify now ride llama.cpp's server task queue instead of locking the context, so they run alongside chat traffic."
url = "/blog/concurrent-scoring-and-pii-ner/"
+++

The `Score` primitive (used by the router classifier) and `TokenClassify` (used by the PII NER tier) previously locked the llama.cpp context for the duration of the call. They now ride llama.cpp's server task queue instead.

What changes as a result:

- Scoring and token classification run concurrently with chat, completion and embedding traffic, and with each other.
- The `known_usecases` restriction that forced dedicated scorer and NER model configs on `llama-cpp` is lifted.
- Repeated scoring calls reuse the prompt KV cache across candidates.
- Scoring inputs are no longer capped by the physical batch size.

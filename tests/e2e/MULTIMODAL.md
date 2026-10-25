# Multimodal decision E2E

The `Multimodal` Ginkgo label drives the registered LocalAI public routes over
loopback HTTP, through the existing application and external mock gRPC backend.
It runs in the normal CI E2E suite; no model download is required.

```sh
make prepare-test
go test ./tests/e2e -v -count=1 -timeout=15m -ginkgo.label-filter=Multimodal
go test -race ./tests/e2e -v -count=1 -timeout=20m -ginkgo.label-filter=Multimodal
```

The mock implements `Score(question_type=systemone)` and returns independently
overlapping probabilities. Generated solid red/blue PNG fixtures exercise image
content rather than prompt markers. Tests cover exact JSON state forwarding,
ordered images, image-only OpenAI and Anthropic requests, selection, fallback,
validation statuses, absence of error usage stamps, and parent cancellation.
OpenAI passes raw base64 to prediction; Anthropic retains MIME-bearing data URLs.
Neither test normalizes away that distinction.

## Opt-in real embeddings and rerank test

`MultimodalEmbeddingReal` starts a LocalAI instance with the real `llama-cpp` gRPC
backend and checks structured `/v1/embeddings` and typed `/v1/rerank` requests. It
is labelled `real-models`, skips with a reason unless `MM_E2E_REAL=1`, and a spec
whose model files are missing skips on its own, so it never runs in the default
suite or in CI.

Models (about 0.87 GB for the embedding pair and 1.9 GB for the reranker pair):

- EmbeddingGemma 2: `embeddinggemma-2-Q8_0.gguf` and `mmproj-embeddinggemma-2-Q8_0.gguf`
  from `AtomicChat/embeddinggemma-2-GGUF` at revision
  `96a5415769c8a56d2557846c4ac23b9f724231be`. Check the license terms first.
- A Qwen3-VL reranker whose GGUF carries the `rerank` chat template, for example
  `Qwen3-VL-Reranker-2B-Q4_K_M.gguf` with `mmproj-Qwen3-VL-Reranker-2B-F16.gguf`
  from `gorse-io/Qwen3-VL-Reranker-2B-GGUF` (Apache 2.0). Some other conversions
  drop the template and cannot rank images.

```sh
# a CPU build of the backend: see backend/cpp/llama-cpp/Makefile
MM_E2E_BACKEND=/absolute/path/to/grpc-server \
MM_E2E_EMBED_MODEL=/path/embeddinggemma-2-Q8_0.gguf \
MM_E2E_EMBED_PROJECTOR=/path/mmproj-embeddinggemma-2-Q8_0.gguf \
MM_E2E_RERANK_MODEL=/path/Qwen3-VL-Reranker-2B-Q4_K_M.gguf \
MM_E2E_RERANK_PROJECTOR=/path/mmproj-Qwen3-VL-Reranker-2B-F16.gguf \
make test-e2e-multimodal-embeddings
```

The fixtures are the deterministic ones under `test/multimodal`. To score a frozen
acceptance contract and write its raw vectors, also set `MM_E2E_CONTRACT`,
`MM_E2E_FIXTURES` and `MM_E2E_VECTORS_OUT`, then recompute with
`test/multimodal/check_adapter_proof.py`.

## Opt-in real gallery test

Accept the model's CC-BY-NC-4.0 license before running. Supply an existing cache
containing `OpenJev-Q4_K_M.gguf` and `mmproj-OpenJev-Q8_0.gguf`, and a compatible
CPU llama.cpp gRPC server. The test checks both complete SHA256 hashes against
`gallery/index.yaml` before installation. Missing or mismatched files fail.
No weights or private locations are stored in this repository.

```sh
DECISION_REAL_E2E=1 \
DECISION_MODEL_CACHE=/absolute/path/to/existing-cache \
DECISION_BACKEND=/absolute/path/to/grpc-server \
go test ./tests/e2e -v -count=1 -timeout=45m \
  -ginkgo.label-filter=MultimodalReal
```

If the binary needs non-system shared libraries, set `LD_LIBRARY_PATH` as well.
The test uses `InstallModelFromGallery`, not a handwritten substitute config.
Existing validated files are linked into the temporary model directory; the
installer checks the gallery artifacts again. Only resource settings change
following installation: four CPU threads, zero GPU layers, one slot, batch 512,
context 8192. The artifacts total 19,603,119,520 bytes; provision additional RAM
for model, vision encoder, KV cache and inference buffers. These are correctness
tests, not performance measurements. Run only one real test process at a time.

Actual `/v1/systemone` responses must have finite normalized probabilities and
opposite winning colors above 0.9. `/v1/chat/completions` and `/v1/messages` must
route opposite colors to distinct candidates using the real model/projector.
Only final candidate generation is mocked. `real-models` excludes this test from
normal CI; it never requires a 19 GB CI download.

The cache case exercises the configured embedding-cache wrapper on a text
classifier: image input bypasses the cache and reaches explicit rejection and
fallback. Native decisions do not support embedding-cache composition. A
separate overlap case activates both independent policies and requires the
candidate whose labels cover both. Long image-bearing history remains on the
native path rather than entering text trimming.

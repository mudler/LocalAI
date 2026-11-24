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

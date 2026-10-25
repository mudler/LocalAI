
+++
disableToc = false
title = "Reranker"
weight = 61
url = "/features/reranker/"
+++

![Two-stage retrieval: a fast retriever finds candidates, a cross-encoder reorders them by relevance](/images/diagrams/reranker-pipeline.png)

A **reranking** model, often referred to as a cross-encoder, is a core component in the two-stage retrieval systems used in information retrieval and natural language processing tasks.
Given a query and a set of documents, it will output similarity scores.

We can use then the score to reorder the documents by relevance in our RAG system to increase its overall accuracy and filter out non-relevant results.

![output](https://github.com/mudler/LocalAI/assets/2420543/ede67b25-fac4-4833-ae4f-78290e401e60)

LocalAI supports reranker models, and you can use them by using the `rerankers` backend, which uses [rerankers](https://github.com/AnswerDotAI/rerankers).

## Usage

You can test `rerankers` by using container images with python (this does **NOT** work with `core` images) and a model config file like this, or by installing `cross-encoder` from the gallery in the UI:

```yaml
name: jina-reranker-v1-base-en
backend: rerankers
parameters:
  model: cross-encoder

```

and test it with:

```bash

    curl http://localhost:8080/v1/rerank \
      -H "Content-Type: application/json" \
      -d '{
      "model": "jina-reranker-v1-base-en",
      "query": "Organic skincare products for sensitive skin",
      "documents": [
        "Eco-friendly kitchenware for modern homes",
        "Biodegradable cleaning supplies for eco-conscious consumers",
        "Organic cotton baby clothes for sensitive skin",
        "Natural organic skincare range for sensitive skin",
        "Tech gadgets for smart homes: 2024 edition",
        "Sustainable gardening tools and compost solutions",
        "Sensitive skin-friendly facial cleansers and toners",
        "Organic food wraps and storage solutions",
        "All-natural pet food for dogs with allergies",
        "Yoga mats made from recycled materials"
      ],
      "top_n": 3
    }'
```
## Multimodal rerank

The query and each document can be a string or a content object, with `text` and
`image_url` parts, using the same content objects, media sources, resource limits
and fetching rules as [multimodal embeddings]({{%relref "features/embeddings#multimodal-input" %}}).
A request of one string query and string documents is unchanged.

```bash
curl http://localhost:8080/v1/rerank -X POST -H "Content-Type: application/json" -d '{
  "model": "qwen3-vl-reranker",
  "query": "A hand rings a brass bell.",
  "documents": [
    {"content": [{"type": "image_url", "image_url": {"url": "https://media.example.com/drum.png"}}]},
    {"content": [{"type": "text", "text": "A bell"}, {"type": "image_url", "image_url": {"url": "data:image/png;base64,<base64>"}}]},
    "Water pours from a jug into a glass."
  ],
  "top_n": 2
}'
```

- Within one query or document, text parts come first and images follow: the
  reranker appends images to the text, and LocalAI refuses text after an image
  instead of reordering it (`400`). Audio and video parts are refused (`422`).
- The result lists `index` and `relevance_score`. For a document sent as a content
  object, `document.text` is the text you sent (empty for an image-only document),
  never a URL or backend text.
- **No silent fallback.** A request with images goes through the typed
  `MultimodalRerank` RPC of the loaded instance. If the model cannot read images
  the answer is `422`, if the backend does not publish the capability it is `501`,
  and the request is never scored as text only. A content object made only of text
  parts has nothing to drop and is scored as text.
- The statuses and limits are those of embeddings: `413` for a bound, `415` for a
  wrong media format, `429` when the two preparation slots are in use (they are
  shared with embeddings), `502` for a failed fetch or an invalid result, `504` for
  a deadline. The total of query and document parts is limited to 128, and the
  body to 48 MiB.

The `llama-cpp` backend supports this for reranker models with a projector and a
`rerank` chat template in the GGUF, for example a Qwen3-VL reranker. Check that your
GGUF carries the template: some conversions drop it, and such a model cannot rank
images (`422`).

```yaml
name: qwen3-vl-reranker
backend: llama-cpp
reranking: true
mmproj: mmproj-Qwen3-VL-Reranker-2B-F16.gguf
context_size: 8192
batch: 8192
parameters:
  model: Qwen3-VL-Reranker-2B-Q4_K_M.gguf
```

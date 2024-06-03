
+++
disableToc = false
title = "📈 Reranker"
weight = 11
url = "/features/reranker/"
+++

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

# optionally:
# type: flashrank
# diffusers:
#  pipeline_type: en # to specify the english language
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

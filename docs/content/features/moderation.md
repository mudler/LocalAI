+++
disableToc = false
title = "Moderation"
weight = 65
url = "/features/moderation/"
+++

LocalAI exposes an OpenAI-compatible text moderation endpoint at
`POST /v1/moderations`. It uses a local text-generation model with a constrained
JSON grammar, so no separate moderation service or cloud API is required.

```bash
curl http://localhost:8080/v1/moderations \
  -H "Content-Type: application/json" \
  -d '{
    "model": "your-instruct-model",
    "input": "Text to classify"
  }'
```

`input` may be one string or an array of strings. The response contains one
result per input with `flagged`, `categories`, `category_scores`, and
`category_applied_input_types` fields. The category names match the OpenAI
moderation API, including harassment, hate, illicit activity, self-harm,
sexual content, and violence categories.

The selected model must support text completion. For consistent results, use
an instruction-tuned model that follows safety-classification prompts well.
LocalAI constrains the output shape, but the model determines the classification
quality and confidence scores.

{{% notice note %}}

This first implementation supports text only. OpenAI-style multimodal input
objects containing images return a validation error.

{{% /notice %}}

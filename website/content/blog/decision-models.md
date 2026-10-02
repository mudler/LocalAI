---
title: "Run decision models in LocalAI"
date: 2026-10-02
author: "Ettore Di Giacinto"
category: "Engineering"
tags: ["decisions", "classification", "models"]
summary: "Ask named questions about text and get structured answers for routing, moderation, and model selection."
extracss: ["blog.css"]
---

LocalAI now supports Jev-style decision models that answer named questions about text and return structured results. You can choose a category, assess a statement, or assign a level on a scale, with several questions in one request.

This is useful when your application needs a decision rather than a written reply. You supply the text and describe the choices. Your application reads the answers by question name, without extracting a label from a chat response.

For example, a support system could choose a queue for an incoming ticket and separately check whether the customer asks to cancel. A moderation workflow could flag a comment for review and assign an urgency level. A model selector could classify a request as translation, code, or general assistance before sending it to a suitable model.

These are application patterns, not built-in workflows. You define the categories and decide what to do with each answer. A result can route work automatically or put it in a review queue, depending on the consequences of a mistake.

## What is supported

The Decisions API accepts text and a set of questions at `POST /v1/systemone`. Questions can ask for a choice from named options, an assessment of whether a statement holds, or a level on a scale. The response groups answers under the question names you supplied.

The current gallery includes Laya, GLiNER2.5-Decide, Tev1 in 0.8B and 4B sizes, kev 0.8B, Nimble 9B, and CLM v0.1 8B. Use a current master build and the **alpha** vllm-cpp backend for these entries.

With the backend and model installed on your machine, inference runs there. Installation downloads the required files. Local execution does not remove the need to control access or review your server's logging and tracing settings.

Scores are not guaranteed probabilities or calibrated measures of correctness. Test your categories on representative examples and keep human review for consequential decisions, including moderation.

## Get started

In **Models → Explore**, find and install `laya-vllm-cpp`. Wait for installation to finish. The [Decisions API guide](/docs/features/decisions/) lists the other gallery IDs, memory requirements, request limits, and model-specific differences.

With authentication enabled, set `LOCALAI_API_KEY` to a valid key for a user with the `decisions` feature enabled. Otherwise, omit the Authorization header. Send only text you are authorized to process.

This example asks which kind of model should receive a request:

```bash
curl --fail-with-body http://localhost:8080/v1/systemone \
  -H "Authorization: Bearer ${LOCALAI_API_KEY}" \
  -H 'Content-Type: application/json' \
  --data-binary '{
    "model": "laya-vllm-cpp",
    "state": "Translate the attached release notes from English into Italian.",
    "questions": {
      "route": {
        "type": "choice",
        "instructions": "What kind of assistance does this request need?",
        "criteria": {
          "translation": "Translate text between languages",
          "code": "Write or debug software",
          "general": "Answer general questions"
        }
      }
    }
  }'
```

Read the selected option from `answers.route.choice`. This call classifies the text; your application must send the original request to the chosen model. Add another named question when you need a separate assessment of the same text. Keep the first trial small enough to check each answer yourself.

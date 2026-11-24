---
title: "Run decision models in LocalAI"
date: 2026-10-02
author: "Ettore Di Giacinto"
category: "Engineering"
tags: ["decisions", "classification", "models"]
summary: "Ask named questions about text and get structured answers for routing, moderation, and model selection."
extracss: ["blog.css"]
---

LocalAI can run decision models: you send text and a few named questions, and you get a structured answer for each one. You can choose a category, assess a statement, or assign a level on a scale, with several questions in one request.

Use it when your code needs a choice, not a paragraph. You supply the text and describe the choices. Your application reads the answers by question name, without extracting a label from a chat response.

For example, a support system could choose a queue for an incoming ticket and separately check whether the customer asks to cancel. A moderation workflow could flag a comment for review and assign an urgency level. A model selector could classify a request as translation, code, or general assistance before sending it to a suitable model.

LocalAI does not route requests for you. You define the categories, and your code reads each answer and decides what to do. A result can route work automatically or put it in a review queue, depending on the consequences of a mistake.

## What is supported

The Decisions API accepts text and a set of questions at `POST /v1/systemone`. Questions can ask for a choice from named options, an assessment of whether a statement holds, or a level on a scale. The response groups answers under the question names you supplied.

The current gallery includes Laya, GLiNER2.5-Decide, Tev1 in 0.8B and 4B sizes, kev 0.8B, Nimble 9B, and CLM v0.1 8B. Use the **alpha** vllm-cpp backend for these entries.

Inference runs on your machine, and the install downloads the files. Check the access, logging and tracing settings before you send sensitive text.

The scores are not calibrated probabilities. Test your categories on your own examples, and keep a person in the loop for moderation and other consequential decisions.

## Get started

In **Models → Explore**, find and install `laya-vllm-cpp`. Wait for installation to finish. The [Decisions API guide](/docs/api/decisions/) lists the other gallery IDs, memory requirements, request limits, and model-specific differences.

This example asks which kind of model should receive a request. It uses `curl` 7.76 or newer for `--fail-with-body`. With authentication off, send it as it is:

```bash
curl --fail-with-body http://localhost:8080/v1/systemone \
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

With authentication on, add `-H "Authorization: Bearer ${LOCALAI_API_KEY}"` to the command. The key must belong to a user who has the `decisions` feature. Send only text you are allowed to process.

Read the selected option from `answers.route.choice`. This call classifies the text; your application must send the original request to the chosen model. Add another named question when you need a separate assessment of the same text. Keep the first trial small enough to check each answer yourself.

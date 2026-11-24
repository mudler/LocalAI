+++
disableToc = true
title = "Quantization"
description = "Shrink a model to a smaller quantized file, then import it so you can chat with it."
weight = 60
keywords = ["quantization", "gguf", "add-on"]
+++

Quantization adds a page and chat tools to shrink a model to a smaller file and import the result. The quantization backend is built into LocalAI.

## At a glance

| Topic | Detail |
|---|---|
| What it adds | The page `/app/addons/quantization/jobs` and chat tools: list jobs, propose a job, read the status of a job, stop a job and import the result as a model. |
| Needs | The Quantization permission (`quantization`), which is off by default for new users on a server with accounts. |
| Guide | [Quantize a model]({{% relref "/models/training/quantization#quantization-is-an-add-on" %}}) |

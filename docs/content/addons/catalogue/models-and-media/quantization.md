+++
disableToc = true
title = "Quantization"
description = "Shrink a model to a smaller quantized file, then import it so you can chat with it."
weight = 60
keywords = ["quantization", "gguf", "add-on"]
+++

The Quantization add-on (ID `quantization`) is the Quantize page and the chat tools for quantization jobs. The API and the quantization backend are part of LocalAI. In the current development builds, it is in the development channel of the add-on library, so it can change.

## At a glance

| | |
|---|---|
| What it adds | The page `/app/addons/quantization/jobs` and chat tools: list jobs, propose a job, read the status of a job, stop a job and import the result as a model. |
| Who installs it | An administrator, from **Add-ons**, **Find add-ons**, with **Install and turn on**. With authentication off, you are the administrator. |
| Who turns it on | A person needs the `addons` feature and the `quantization` feature. Both are off by default for new users on a server with accounts. With `--addons-enabled=false` this feature is off. |
| Guide | [Quantize a model]({{% relref "/models/training/quantization#quantization-is-an-add-on" %}}) |

This feature is experimental and may change.

+++
disableToc = true
title = "Fine-tuning"
description = "Train an adapter on your own data, then export it as a model you can chat with."
weight = 50
keywords = ["fine-tuning", "training", "lora", "add-on"]
+++

The Fine-tuning add-on (ID `fine-tuning`) is the Fine-Tune page and the chat tools for fine-tuning jobs. The API and the training backends are part of LocalAI. In the current development builds, it is in the development channel of the add-on library, so it can change.

## At a glance

| | |
|---|---|
| What it adds | The page `/app/addons/fine-tuning/jobs` and chat tools: list jobs, propose a job, store a small dataset, read the status and the checkpoints of a job, stop a job and export a checkpoint as a model. |
| Who installs it | An administrator, from **Add-ons**, **Find add-ons**, with **Install and turn on**. With authentication off, you are the administrator. |
| Who turns it on | A person needs the `addons` feature and the `fine_tuning` feature. Both are off by default for new users on a server with accounts. With `--addons-enabled=false` this feature is off. |
| Guide | [Fine-tune a model]({{% relref "/models/training/fine-tuning#fine-tuning-is-an-add-on" %}}) |

This feature is experimental and may change.

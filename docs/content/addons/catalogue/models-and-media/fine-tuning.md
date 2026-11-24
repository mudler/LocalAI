+++
disableToc = true
title = "Fine-tuning"
description = "Train an adapter on your own data, then export it as a model you can chat with."
weight = 50
keywords = ["fine-tuning", "training", "lora", "add-on"]
+++

Fine-tuning adds a page and chat tools to train an adapter on your data and export it as a model. The training backends are built into LocalAI.

## At a glance

| Topic | Detail |
|---|---|
| What it adds | The page `/app/addons/fine-tuning/jobs` and chat tools: list jobs, propose a job, store a small dataset, read the status and the checkpoints of a job, stop a job and export a checkpoint as a model. |
| Needs | The Fine-Tuning permission (`fine_tuning`), which is off by default for new users on a server with accounts. |
| Guide | [Fine-tune a model]({{% relref "/models/training/fine-tuning#fine-tuning-is-an-add-on" %}}) |

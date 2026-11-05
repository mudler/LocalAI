+++
disableToc = false
title = "Getting started in 5 minutes"
weight = 1
url = "/basics/five-minutes/"
icon = "timer"
+++

This is the shortest path from nothing to a model that answers and does some
work for you. You need Docker and about 3 GB of free disk space for a small
model.

## 1. Start LocalAI (1 minute)

```bash
docker run -ti --name local-ai -p 8080:8080 localai/localai:latest
```

For a GPU, a Mac or another install method, see
[Install]({{% relref "getting-started/install" %}}) and
[Container images]({{% relref "getting-started/containers" %}}). Open
**http://localhost:8080**.

## 2. Install a model (2 minutes)

Open **Models**, search for `qwen3-4b` and choose **Install**. It is small,
runs on a CPU, and supports tool calling, which you need for the steps below.
Wait for the download to finish.

## 3. Chat (30 seconds)

Open **Chat**, choose `qwen3-4b`, and write a message. The server keeps the
conversation. You can keep typing while the model works.

The same model answers the OpenAI API:

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model": "qwen3-4b", "messages": [{"role": "user", "content": "Hello!"}]}'
```

## 4. Make it do the work (2 minutes)

Ask the chat to change something about LocalAI for you:

> Call me Sam and keep answers short.

LocalAI saves your name and the rule as files in your home folder. Every chat
uses them from your next message. A small model may need a second try.
Then try:

> Remember that I prefer metric units.

The model proposes a note and a card asks you to confirm it. See
[Work with a chat]({{% relref "features/chat-control" %}}).

## What next

- Run code and make documents: install the **Sandbox** and **Simple Office**
  add-ons from **Add-ons**, **Find add-ons**. See
  [Use add-ons]({{% relref "features/using-addons" %}}).
- Save an agent with its own instructions:
  [Build your first agent]({{% relref "getting-started/first-agent" %}}).
- Talk to your agent from a phone:
  [Connectors]({{% relref "features/chats-connectors" %}}).
- Share the server with other people:
  [Users and what each can do]({{% relref "features/users-and-features" %}}).
- Find anything by goal: [What do you want to do?]({{% relref "features/by-goal" %}}).

{{% notice note %}}
Exposing LocalAI beyond your own machine? Turn on user authentication first
(`LOCALAI_AUTH=true`). See
[Authentication and authorization]({{% relref "features/authentication" %}}).
{{% /notice %}}

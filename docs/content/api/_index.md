+++
title = "API"
description = "The HTTP API and the pages that describe it."
weight = 6
type = "chapter"
icon = "api"
+++

LocalAI serves one HTTP API. These pages describe the parts of it that need more than an endpoint list.

- [OpenAI functions and tools]({{% relref "/api/openai-functions" %}}): function calling and the tools API.
- [Realtime API]({{% relref "/api/openai-realtime" %}}): voice and text conversations over WebSocket.
- [API discovery]({{% relref "/api/api-discovery" %}}): how LocalAI lists what it can do.
- [Decisions API]({{% relref "/api/decisions" %}})
- [Stores]({{% relref "/api/stores" %}}): vector similarity search.
- [API errors]({{% relref "/api/api-errors" %}}): what each error code means.
- [Integrations]({{% relref "/api/integrations" %}}): projects and tools built on LocalAI.

Task pages that use the API, such as text generation, embeddings and image generation, are in [Features]({{% relref "/features" %}}). The server also publishes its own interactive API reference at `/swagger/index.html`.

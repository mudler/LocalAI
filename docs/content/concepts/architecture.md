+++
disableToc = false
title = "Architecture"
weight = 20
aliases = ["/reference/architecture/"]
description = "How LocalAI works: a small core, gRPC backends started on demand, and one API."
keywords = ["architecture","backends","grpc"]
devbuild = "install"
+++

LocalAI is an API written in Go. A program that uses an OpenAI SDK works with LocalAI after you change the base URL. Anthropic clients work too. It runs on a laptop CPU, and a GPU makes inference faster. The inference runs in separate backends, many of them in C++. Backends are gRPC servers. You can build your own gRPC server, or point LocalAI at an external gRPC server that you run yourself (`LOCALAI_EXTERNAL_GRPC_BACKENDS`).

Backends are written in C++, Go and Python. The [compatibility table]({{%relref "/models/compatibility-table" %}}) lists which backend runs which model family.

{{< diagram "architecture-overview" "How LocalAI works: clients call one API on a small core with chats, agents, an add-on host and an MCP client and server, which talks over gRPC to a separate backend process for each model, running on your own hardware." >}}

{{< diagram "request-flow" "How a request flows: the client calls the API, the model config picks a backend, the backend process runs the model over gRPC on your hardware, and tokens stream back." >}}


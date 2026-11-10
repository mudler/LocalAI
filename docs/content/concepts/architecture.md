+++
disableToc = false
title = "Architecture"
weight = 20
aliases = ["/reference/architecture/"]
description = "How LocalAI works: a small core, gRPC backends started on demand, and one API."
keywords = ["architecture","backends","grpc"]
+++

LocalAI is an API written in Go. It follows the OpenAI API, so software that uses an OpenAI SDK can use LocalAI instead. It runs on consumer hardware. The inference runs in separate backends, many of them in C++. Backends are gRPC servers. You can build your own gRPC server, or point LocalAI at an external one that it then manages.

LocalAI uses a mixture of backends written in various languages (C++, Golang, Python, ...). You can check [the model compatibility table]({{%relref "/models/compatibility-table" %}}) to learn about all the components of LocalAI.

{{< diagram "architecture-overview" "How LocalAI works: clients call one API on a small core, which talks over gRPC to a separate backend process for each model, running on your own hardware." >}}

{{< diagram "request-flow" "How a request flows: the client calls the API, the model config picks a backend, the backend process runs the model over gRPC on your hardware, and tokens stream back." >}}

For the history of the project, read [LocalAI, from March 2023 to now](https://localai.io/blog/localai-since-march-2023/).

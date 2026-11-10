+++
disableToc = false
title = "Architecture"
weight = 20
aliases = ["/reference/architecture/"]
+++

LocalAI is an API written in Go that serves as an OpenAI shim, enabling software already developed with OpenAI SDKs to seamlessly integrate with LocalAI. It can be effortlessly implemented as a substitute, even on consumer-grade hardware. This capability is achieved by employing various C++ backends, including [ggml](https://github.com/ggerganov/ggml), to perform inference on LLMs using both CPU and, if desired, GPU. Internally LocalAI backends are just gRPC server, indeed you can specify and build your own gRPC server and extend LocalAI in runtime as well. It is possible to specify external gRPC server and/or binaries that LocalAI will manage internally.

LocalAI uses a mixture of backends written in various languages (C++, Golang, Python, ...). You can check [the model compatibility table]({{%relref "/models/compatibility-table" %}}) to learn about all the components of LocalAI.

![How LocalAI works: clients speak one API to a small core, which routes each request over gRPC to separate backend processes pulled on demand](/docs/images/diagrams/architecture-overview.png)

For the history of the project, read [LocalAI, from March 2023 to now](https://localai.io/blog/localai-since-march-2023/).

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

![How LocalAI works: clients speak one API to a small core, which routes each request over gRPC to separate backend processes pulled on demand](/docs/images/diagrams/architecture-overview.png)

For the history of the project, read [LocalAI, from March 2023 to now](https://localai.io/blog/localai-since-march-2023/).


+++
disableToc = false
title = "Development documentation"
weight = 7
+++

{{% notice note %}}

This section is for developers and contributors. If you are looking for the user documentation, this is not the right place!

{{% /notice %}}

This section will collect how-to, notes and development documentation

## Contributing

We use conventional commits and semantic versioning. Please follow the [conventional commits](https://www.conventionalcommits.org/en/v1.0.0/) specification when writing commit messages.

## LocalAI Project Structure

### LocalAI is made of multiple components, developed in multiple repositories:

The core repository, containing the primary `local-ai` server code, gRPC stubs, this documentation website, and docker container building resources are all located at [mudler/LocalAI](https://github.com/mudler/LocalAI).

As LocalAI is designed to make use of multiple, independent model galleries, those are maintained seperately. The following public model galleries are available for use:

* [go-skynet/model-gallery](https://github.com/go-skynet/model-gallery) - The original gallery, the `golang` huggingface scraper ran into limits and was largely retired, so this now holds handmade yaml configs
* [dave-gray101/model-gallery](https://github.com/dave-gray101/model-gallery) - An automated gallery designed to track HuggingFace uploads and produce best-effort automatically generated configurations for LocalAI. It is designed to produce one LocalAI gallery per repository on HuggingFace.

### Directory Structure of this Repo

The core repository is broken up into the following primary chunks:

* `/backend`: gRPC protobuf specification and gRPC backends. Subfolders for each language.
* `/core`: golang sourcecode for the core LocalAI application. Broken down below.
* `/docs`: localai.io website that you are reading now
* `/examples`: example code integrating LocalAI to other projects and/or developer samples and tools
* `/internal`: **here be dragons**. Don't touch this, it's used for automatic versioning.
* `/models`: _No code here!_ This is where models are installed!
* `/pkg`: golang sourcecode that is intended to be reusable or at least widely imported across LocalAI. Broken down below
* `/prompt-templates`: _No code here!_ This is where **example** prompt templates were historically stored. Somewhat obsolete these days, model-galleries tend to replace manually creating these?
* `/tests`: Does what it says on the tin. Please write tests and put them here when you do.

The `core` folder is broken down further:

* `/core/backend`: code that interacts with a gRPC backend to perform AI tasks.
* `/core/http`: code specifically related to the REST server
* `/core/http/endpoints`: Has two subdirectories, `openai` and `localai` for binding the respective endpoints to the correct backend or service.
* `/core/mqtt`: core specifically related to the MQTT server. Stub for now. Coming soon!
* `/core/services`: code implementing functionality performed by `local-ai` itself, rather than delegated to a backend.
* `/core/startup`: code related specifically to application startup of `local-ai`. Potentially to be refactored to become a part of `/core/services` at a later date, or not.

The `pkg` folder is broken down further:

* `/pkg/assets`: Currently contains a single function related to extracting files from archives. Potentially to be refactored to become a part of `/core/utils` at a later date?
* `/pkg/datamodel`: Contains the data types and definitions used by the LocalAI project. Imported widely!
* `/pkg/gallery`: Code related to interacting with a `model-gallery`
* `/pkg/grammar`: Code related to BNF / functions for LLM
* `/pkg/grpc`: base classes and interfaces for gRPC backends to implement
* `/pkg/langchain`: langchain related code in golang
* `/pkg/model`: Code related to loading and initializing a model and creating the appropriate gRPC backend.
* `/pkg/stablediffusion`: Code related to stablediffusion in golang.
* `/pkg/utils`: Every real programmer knows what they are going to find in here... it's our junk drawer of utility functions.


## Creating a gRPC backend

LocalAI backends are `gRPC` servers.

In order to create a new backend you need:

- If there are changes required to the protobuf code, modify the [proto](https://github.com/go-skynet/LocalAI/blob/master/pkg/grpc/proto/backend.proto) file and re-generate the code with `make protogen`.
- Modify the `Makefile` to add your new backend and re-generate the client code with `make protogen` if necessary.
- Create a new `gRPC` server in `extra/grpc` if it's not written in go: [link](https://github.com/go-skynet/LocalAI/tree/master/extra/grpc), and create the specific implementation.
    - Golang `gRPC` servers should be added in the [pkg/backend](https://github.com/go-skynet/LocalAI/tree/master/pkg/backend) directory given their type. See [piper](https://github.com/go-skynet/LocalAI/blob/master/pkg/backend/tts/piper.go) as an example.
    - Golang servers needs a respective `cmd/grpc` binary that must be created too, see also [cmd/grpc/piper](https://github.com/go-skynet/LocalAI/tree/master/cmd/grpc/piper) as an example, update also the Makefile accordingly to build the binary during build time.
- Update the Dockerfile: if the backend is written in another language, update the `Dockerfile` default *EXTERNAL_GRPC_BACKENDS* variable by listing the new binary [link](https://github.com/go-skynet/LocalAI/blob/c2233648164f67cdb74dd33b8d46244e14436ab3/Dockerfile#L14).

Once you are done, you can either re-build `LocalAI` with your backend or you can try it out by running the `gRPC` server manually and specifying the host and IP to LocalAI with `--external-grpc-backends` or using (`EXTERNAL_GRPC_BACKENDS` environment variable, comma separated list of `name:host:port` tuples, e.g. `my-awesome-backend:host:port`):

```bash
./local-ai --debug --external-grpc-backends "my-awesome-backend:host:port" ...
```

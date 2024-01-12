
+++
disableToc = false
title = "Development documentation"
weight = 7
+++

{{% alert note %}}

This section is for developers and contributors. If you are looking for the user documentation, this is not the right place!

{{% /alert %}}

This section will collect how-to, notes and development documentation

## Contributing

We use conventional commits and semantic versioning. Please follow the [conventional commits](https://www.conventionalcommits.org/en/v1.0.0/) specification when writing commit messages.

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

+++
disableToc = false
title = "Build LocalAI"
icon = "model_training"
weight = 5
url = '/basics/build/'
+++


### Build

LocalAI can be built as a container image or as a single, portable binary. Note that some model architectures might require Python libraries, which are not included in the binary.

LocalAI's extensible architecture allows you to add your own backends, which can be written in any language, and as such the container images contains also the Python dependencies to run all the available backends (for example, in order to run backends like __Diffusers__ that allows to generate images and videos from text).

This section contains instructions on how to build LocalAI from source.

#### Build LocalAI locally

##### Requirements

In order to build LocalAI locally, you need the following requirements:

- Golang >= 1.21
- GCC
- GRPC

To install the dependencies follow the instructions below:

{{< tabs >}}
{{% tab title="Apple" %}}

Install `xcode` from the App Store

```bash
brew install go protobuf protoc-gen-go protoc-gen-go-grpc wget
```

{{% /tab %}}
{{% tab title="Debian" %}}

```bash
apt install golang make protobuf-compiler-grpc
```

After you have golang installed and working, you can install the required binaries for compiling the golang protobuf components via the following commands

```bash
go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.34.2
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@1958fcbe2ca8bd93af633f11e97d44e567e945af

```

{{% /tab %}}
{{% tab title="From source" %}}

```bash
make build
```

{{% /tab %}}
{{< /tabs >}}

##### Build
To build LocalAI with `make`:

```
git clone https://github.com/go-skynet/LocalAI
cd LocalAI
make build
```

This should produce the binary `local-ai`

#### Container image

Requirements:

- Docker or podman, or a container engine

In order to build the `LocalAI` container image locally you can use `docker`, for example:

```
docker build -t localai .
docker run localai
```

### Example: Build on mac

Building on Mac (M1, M2 or M3) works, but you may need to install some prerequisites using `brew`. 

The below has been tested by one mac user and found to work. Note that this doesn't use Docker to run the server:

Install `xcode` from the Apps Store (needed for metalkit)

```
brew install abseil cmake go grpc protobuf wget protoc-gen-go protoc-gen-go-grpc

git clone https://github.com/go-skynet/LocalAI.git

cd LocalAI

make build

wget https://huggingface.co/TheBloke/phi-2-GGUF/resolve/main/phi-2.Q2_K.gguf -O models/phi-2.Q2_K

cp -rf prompt-templates/ggml-gpt4all-j.tmpl models/phi-2.Q2_K.tmpl

./local-ai backends install llama-cpp

./local-ai --models-path=./models/ --debug=true

curl http://localhost:8080/v1/models

curl http://localhost:8080/v1/chat/completions -H "Content-Type: application/json" -d '{
     "model": "phi-2.Q2_K",
     "messages": [{"role": "user", "content": "How are you?"}],
     "temperature": 0.9 
   }'
```

#### Troubleshooting mac

- If you encounter errors regarding a missing utility metal, install `Xcode` from the App Store.

- After the installation of Xcode, if you receive a xcrun error `'xcrun: error: unable to find utility "metal", not a developer tool or in PATH'`. You might have installed the Xcode command line tools before installing Xcode, the former one is pointing to an incomplete SDK.

```
xcode-select --print-path

sudo xcode-select --switch /Applications/Xcode.app/Contents/Developer
```

- If completions are slow, ensure that `gpu-layers` in your model yaml matches the number of layers from the model in use (or simply use a high number such as 256).

- If you get a compile error: `error: only virtual member functions can be marked 'final'`, reinstall all the necessary brew packages, clean the build, and try again.

```
brew reinstall go grpc protobuf wget

make clean

make build
```

## Build backends

LocalAI have several backends available for installation in the backend gallery. The backends can be also built by source. As backends might vary from language and dependencies that they require, the documentation will provide generic guidance for few of the backends, which can be applied with some slight modifications also to the others.

### Manually

Typically each backend include a Makefile which allow to package the backend.

In the LocalAI repository, for instance you can build `bark-cpp` by doing:

```
git clone https://github.com/go-skynet/LocalAI.git

make -C LocalAI/backend/go/bark-cpp build package

make -C LocalAI/backend/python/vllm
```

### With Docker

Building with docker is simpler as abstracts away all the requirement, and focuses on building the final OCI images that are available in the gallery. This allows for instance also to build locally a backend and install it with LocalAI. You can refer to [Backends](https://localai.io/backends/) for general guidance on how to install and develop backends.

In the LocalAI repository, you can build `bark-cpp` by doing:

```
git clone https://github.com/go-skynet/LocalAI.git

make docker-build-bark-cpp
```

Note that `make` is only by convenience, in reality it just runs a simple `docker` command as:

```bash
docker build --build-arg BUILD_TYPE=$(BUILD_TYPE) --build-arg BASE_IMAGE=$(BASE_IMAGE) -t local-ai-backend:bark-cpp -f LocalAI/backend/Dockerfile.golang --build-arg BACKEND=bark-cpp .               
```

Note:

- BUILD_TYPE can be either: `cublas`, `hipblas`, `sycl_f16`, `sycl_f32`, `metal`.
- BASE_IMAGE is tested on `ubuntu:22.04` (and defaults to it) and `quay.io/go-skynet/intel-oneapi-base:latest` for intel/sycl

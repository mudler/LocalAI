
+++
disableToc = false
title = "Build LocalAI from source"
weight = 6
url = '/basics/build/'
ico = "rocket_launch"
+++

### Build

LocalAI can be built as a container image or as a single, portable binary. Note that the some model architectures might require Python libraries, which are not included in the binary. The binary contains only the core backends written in Go and C++. 

LocalAI's extensible architecture allows you to add your own backends, which can be written in any language, and as such the container images contains also the Python dependencies to run all the available backends (for example, in order to run backends like __Diffusers__ that allows to generate images and videos from text).

In some cases you might want to re-build LocalAI from source (for instance to leverage Apple Silicon acceleration), or to build a custom container image with your own backends. This section contains instructions on how to build LocalAI from source.



#### Build LocalAI locally

##### Requirements

In order to build LocalAI locally, you need the following requirements:

- Golang >= 1.21
- Cmake/make
- GCC
- GRPC

To install the dependencies follow the instructions below:

{{< tabs tabTotal="3"  >}}
{{% tab tabName="Apple" %}}

Install `xcode` from the App Store

```bash
brew install abseil cmake go grpc protobuf protoc-gen-go protoc-gen-go-grpc python wget
```

After installing the above dependencies, you need to install grpcio-tools from PyPI. You could do this via a pip --user install or a virtualenv.

```bash
pip install --user grpcio-tools
```

{{% /tab %}}
{{% tab tabName="Debian" %}}

```bash
apt install cmake golang libgrpc-dev make protobuf-compiler-grpc python3-grpc-tools
```

After you have golang installed and working, you can install the required binaries for compiling the golang protobuf components via the following commands

```bash
go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.34.2
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@1958fcbe2ca8bd93af633f11e97d44e567e945af

```

{{% /tab %}}
{{% tab tabName="From source" %}}

Specify `BUILD_GRPC_FOR_BACKEND_LLAMA=true` to build automatically the gRPC dependencies

```bash
make ... BUILD_GRPC_FOR_BACKEND_LLAMA=true build
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

Here is the list of the variables available that can be used to customize the build:

| Variable | Default | Description |
| ---------------------| ------- | ----------- |
| `BUILD_TYPE`         |   None      | Build type. Available: `cublas`, `openblas`, `clblas`, `metal`,`hipblas`, `sycl_f16`, `sycl_f32` |
| `GO_TAGS`            |   `tts stablediffusion`      | Go tags. Available: `stablediffusion`, `tts`, `tinydream` |
| `CLBLAST_DIR`        |         | Specify a CLBlast directory |
| `CUDA_LIBPATH`       |         | Specify a CUDA library path |
| `BUILD_API_ONLY` | false | Set to true to build only the API (no backends will be built) |

{{% alert note %}}

#### CPU flagset compatibility


LocalAI uses different backends based on ggml and llama.cpp to run models. If your CPU doesn't support common instruction sets, you can disable them during build:

```
CMAKE_ARGS="-DGGML_F16C=OFF -DGGML_AVX512=OFF -DGGML_AVX2=OFF -DGGML_AVX=OFF -DGGML_FMA=OFF" make build
```

To have effect on the container image, you need to set `REBUILD=true`:

```
docker run  quay.io/go-skynet/localai
docker run --rm -ti -p 8080:8080 -e DEBUG=true -e MODELS_PATH=/models -e THREADS=1 -e REBUILD=true -e CMAKE_ARGS="-DGGML_F16C=OFF -DGGML_AVX512=OFF -DGGML_AVX2=OFF -DGGML_AVX=OFF -DGGML_FMA=OFF" -v $PWD/models:/models quay.io/go-skynet/local-ai:latest
```

{{% /alert %}}

#### Container image

Requirements:

- Docker or podman, or a container engine

In order to build the `LocalAI` container image locally you can use `docker`, for example:

```
# build the image
docker build -t localai .
docker run localai
```

There are some build arguments that can be used to customize the build:

| Variable | Default | Description |
| ---------------------| ------- | ----------- |
| `IMAGE_TYPE`         |   `extras`      | Build type. Available: `core`, `extras` |


### Example: Build on mac

Building on Mac (M1, M2 or M3) works, but you may need to install some prerequisites using `brew`. 

The below has been tested by one mac user and found to work. Note that this doesn't use Docker to run the server:

Install `xcode` from the Apps Store (needed for metalkit)

```
# install build dependencies
brew install abseil cmake go grpc protobuf wget protoc-gen-go protoc-gen-go-grpc

# clone the repo
git clone https://github.com/go-skynet/LocalAI.git

cd LocalAI

# build the binary
make build

# Download phi-2 to models/
wget https://huggingface.co/TheBloke/phi-2-GGUF/resolve/main/phi-2.Q2_K.gguf -O models/phi-2.Q2_K

# Use a template from the examples
cp -rf prompt-templates/ggml-gpt4all-j.tmpl models/phi-2.Q2_K.tmpl

# Run LocalAI
./local-ai --models-path=./models/ --debug=true

# Now API is accessible at localhost:8080
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
# print /Library/Developer/CommandLineTools, if command line tools were installed in advance
xcode-select --print-path

# point to a complete SDK
sudo xcode-select --switch /Applications/Xcode.app/Contents/Developer
```

- If completions are slow, ensure that `gpu-layers` in your model yaml matches the number of layers from the model in use (or simply use a high number such as 256).

- If you a get a compile error: `error: only virtual member functions can be marked 'final'`, reinstall all the necessary brew packages, clean the build, and try again.

```
# reinstall build dependencies
brew reinstall abseil cmake go grpc protobuf wget

make clean

make build
```

**Requirements**: OpenCV, Gomp

Image generation requires `GO_TAGS=stablediffusion` or `GO_TAGS=tinydream` to be set during build:

```
make GO_TAGS=stablediffusion build
```

### Build with Text to audio support

**Requirements**: piper-phonemize

Text to audio support is experimental and requires `GO_TAGS=tts` to be set during build:

```
make GO_TAGS=tts build
```

### Acceleration

#### OpenBLAS

Software acceleration.

Requirements: OpenBLAS

```
make BUILD_TYPE=openblas build
```

#### CuBLAS

Nvidia Acceleration.

Requirement: Nvidia CUDA toolkit

Note: CuBLAS support is experimental, and has not been tested on real HW. please report any issues you find!

```
make BUILD_TYPE=cublas build
```

More informations available in the upstream PR: https://github.com/ggerganov/llama.cpp/pull/1412


#### Hipblas (AMD GPU with ROCm on Arch Linux)

Packages:
```
pacman -S base-devel git rocm-hip-sdk rocm-opencl-sdk opencv clblast grpc
```

Library links:
```
export CGO_CFLAGS="-I/usr/include/opencv4"
export CGO_CXXFLAGS="-I/usr/include/opencv4"
export CGO_LDFLAGS="-L/opt/rocm/hip/lib -lamdhip64 -L/opt/rocm/lib -lOpenCL -L/usr/lib -lclblast -lrocblas -lhipblas -lrocrand -lomp -O3 --rtlib=compiler-rt -unwindlib=libgcc -lhipblas -lrocblas --hip-link"
```

Build:
```
make BUILD_TYPE=hipblas GPU_TARGETS=gfx1030
```

#### ClBLAS

AMD/Intel GPU acceleration.

Requirement: OpenCL, CLBlast

```
make BUILD_TYPE=clblas build
```

To specify a clblast dir set: `CLBLAST_DIR`

#### Intel GPU acceleration

Intel GPU acceleration is supported via SYCL.

Requirements: [Intel oneAPI Base Toolkit](https://www.intel.com/content/www/us/en/developer/tools/oneapi/base-toolkit-download.html) (see also [llama.cpp setup installations instructions](https://github.com/ggerganov/llama.cpp/blob/d71ac90985854b0905e1abba778e407e17f9f887/README-sycl.md?plain=1#L56))

```
make BUILD_TYPE=sycl_f16 build # for float16
make BUILD_TYPE=sycl_f32 build # for float32
```

#### Metal (Apple Silicon)

```
make build

# correct build type is automatically used on mac (BUILD_TYPE=metal)
# Set `gpu_layers: 256` (or equal to the number of model layers) to your YAML model config file and `f16: true`
```

### Windows compatibility

Make sure to give enough resources to the running container. See https://github.com/go-skynet/LocalAI/issues/2

### Examples

More advanced build options are available, for instance to build only a single backend.

#### Build only a single backend

You can control the backends that are built by setting the `GRPC_BACKENDS` environment variable. For instance, to build only the `llama-cpp` backend only:

```bash
make GRPC_BACKENDS=backend-assets/grpc/llama-cpp build
```

By default, all the backends are built.

#### Specific llama.cpp version

To build with a specific version of llama.cpp, set `CPPLLAMA_VERSION` to the tag or wanted sha:

```
CPPLLAMA_VERSION=<sha> make build
```

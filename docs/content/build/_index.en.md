
+++
disableToc = false
title = "Build"
weight = 5
url = '/basics/build/'

+++

### Build locally

Requirements:

Either Docker/podman, or
- Golang >= 1.21
- Cmake/make
- GCC

In order to build the `LocalAI` container image locally you can use `docker`:

```
# build the image
docker build -t localai .
docker run localai
```

Or you can build the manually binary with `make`:

```
git clone https://github.com/go-skynet/LocalAI
cd LocalAI
make build
```

To run: `./local-ai`

{{% notice note %}}

#### CPU flagset compatibility


LocalAI uses different backends based on ggml and llama.cpp to run models. If your CPU doesn't support common instruction sets, you can disable them during build:

```
CMAKE_ARGS="-DLLAMA_F16C=OFF -DLLAMA_AVX512=OFF -DLLAMA_AVX2=OFF -DLLAMA_AVX=OFF -DLLAMA_FMA=OFF" make build
```

To have effect on the container image, you need to set `REBUILD=true`:

```
docker run  quay.io/go-skynet/localai
docker run --rm -ti -p 8080:8080 -e DEBUG=true -e MODELS_PATH=/models -e THREADS=1 -e REBUILD=true -e CMAKE_ARGS="-DLLAMA_F16C=OFF -DLLAMA_AVX512=OFF -DLLAMA_AVX2=OFF -DLLAMA_AVX=OFF -DLLAMA_FMA=OFF" -v $PWD/models:/models quay.io/go-skynet/local-ai:latest
```

{{% /notice %}}

### Build on mac

Building on Mac (M1 or M2) works, but you may need to install some prerequisites using `brew`. 

The below has been tested by one mac user and found to work. Note that this doesn't use Docker to run the server:

```
# install build dependencies
brew install abseil cmake go grpc protobuf wget

# clone the repo
git clone https://github.com/go-skynet/LocalAI.git

cd LocalAI

# build the binary
make build

# Download gpt4all-j to models/
wget https://gpt4all.io/models/ggml-gpt4all-j.bin -O models/ggml-gpt4all-j

# Use a template from the examples
cp -rf prompt-templates/ggml-gpt4all-j.tmpl models/

# Run LocalAI
./local-ai --models-path=./models/ --debug=true

# Now API is accessible at localhost:8080
curl http://localhost:8080/v1/models

curl http://localhost:8080/v1/chat/completions -H "Content-Type: application/json" -d '{
     "model": "ggml-gpt4all-j",
     "messages": [{"role": "user", "content": "How are you?"}],
     "temperature": 0.9 
   }'
```

### Build with Image generation support


**Requirements**: OpenCV, Gomp

Image generation is experimental and requires `GO_TAGS=stablediffusion` to be set during build:

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

List of the variables available to customize the build:

| Variable | Default | Description |
| ---------------------| ------- | ----------- |
| `BUILD_TYPE`         |   None      | Build type. Available: `cublas`, `openblas`, `clblas`, `metal`,`hipblas` |
| `GO_TAGS`            |   `tts stablediffusion`      | Go tags. Available: `stablediffusion`, `tts` |
| `CLBLAST_DIR`        |         | Specify a CLBlast directory |
| `CUDA_LIBPATH`       |         | Specify a CUDA library path |

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

#### Hipblas (AMD GPU)

AMD GPU Acceleration

Requirement: ROCm

```
make BUILD_TYPE=hipblas build
```

Specific GPU targets can be specified with `GPU_TARGETS`:
  
```
make BUILD_TYPE=hipblas GPU_TARGETS=gfx90a build
```

#### ClBLAS

AMD/Intel GPU acceleration.

Requirement: OpenCL, CLBlast

```
make BUILD_TYPE=clblas build
```

To specify a clblast dir set: `CLBLAST_DIR`

### Metal (Apple Silicon)

```
make BUILD_TYPE=metal build

# Set `gpu_layers: 1` to your YAML model config file and `f16: true`
# Note: only models quantized with q4_0 are supported!
```

### Windows compatibility

Make sure to give enough resources to the running container. See https://github.com/go-skynet/LocalAI/issues/2

+++
disableToc = false
title = "⚡ GPU acceleration"
weight = 9
url = "/features/gpu-acceleration/"
+++

{{% alert context="warning" %}}
Section under construction
{{% /alert %}}

This section contains instruction on how to use LocalAI with GPU acceleration.

{{% alert icon="⚡" context="warning" %}}
For accelleration for AMD or Metal HW there are no specific container images, see the [build]({{%relref "docs/getting-started/build#Acceleration" %}})
{{% /alert %}}

### CUDA(NVIDIA) acceleration

#### Requirements

Requirement: nvidia-container-toolkit (installation instructions [1](https://www.server-world.info/en/note?os=Ubuntu_22.04&p=nvidia&f=2) [2](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/install-guide.html))

To check what CUDA version do you need, you can either run `nvidia-smi` or `nvcc --version`. 

Alternatively, you can also check nvidia-smi with docker:

```
docker run --runtime=nvidia --rm nvidia/cuda nvidia-smi
```

To use CUDA, use the images with the `cublas` tag, for example.

The image list is on [quay](https://quay.io/repository/go-skynet/local-ai?tab=tags):

- CUDA `11` tags: `master-cublas-cuda11`, `v1.40.0-cublas-cuda11`, ...
- CUDA `12` tags: `master-cublas-cuda12`, `v1.40.0-cublas-cuda12`, ...
- CUDA `11` + FFmpeg tags: `master-cublas-cuda11-ffmpeg`, `v1.40.0-cublas-cuda11-ffmpeg`, ...
- CUDA `12` + FFmpeg tags: `master-cublas-cuda12-ffmpeg`, `v1.40.0-cublas-cuda12-ffmpeg`, ...

In addition to the commands to run LocalAI normally, you need to specify `--gpus all` to docker, for example:

```bash
docker run --rm -ti --gpus all -p 8080:8080 -e DEBUG=true -e MODELS_PATH=/models -e THREADS=1 -v $PWD/models:/models quay.io/go-skynet/local-ai:v1.40.0-cublas-cuda12
```

If the GPU inferencing is working, you should be able to see something like:

```
5:22PM DBG Loading model in memory from file: /models/open-llama-7b-q4_0.bin
ggml_init_cublas: found 1 CUDA devices:
  Device 0: Tesla T4
llama.cpp: loading model from /models/open-llama-7b-q4_0.bin
llama_model_load_internal: format     = ggjt v3 (latest)
llama_model_load_internal: n_vocab    = 32000
llama_model_load_internal: n_ctx      = 1024
llama_model_load_internal: n_embd     = 4096
llama_model_load_internal: n_mult     = 256
llama_model_load_internal: n_head     = 32
llama_model_load_internal: n_layer    = 32
llama_model_load_internal: n_rot      = 128
llama_model_load_internal: ftype      = 2 (mostly Q4_0)
llama_model_load_internal: n_ff       = 11008
llama_model_load_internal: n_parts    = 1
llama_model_load_internal: model size = 7B
llama_model_load_internal: ggml ctx size =    0.07 MB
llama_model_load_internal: using CUDA for GPU acceleration
llama_model_load_internal: mem required  = 4321.77 MB (+ 1026.00 MB per state)
llama_model_load_internal: allocating batch_size x 1 MB = 512 MB VRAM for the scratch buffer
llama_model_load_internal: offloading 10 repeating layers to GPU
llama_model_load_internal: offloaded 10/35 layers to GPU
llama_model_load_internal: total VRAM used: 1598 MB
...................................................................................................
llama_init_from_file: kv self size  =  512.00 MB
```

#### Model configuration

Depending on the model architecture and backend used, there might be different ways to enable GPU acceleration. It is required to configure the model you intend to use with a YAML config file. For example, for `llama.cpp` workloads a configuration file might look like this (where `gpu_layers` is the number of layers to offload to the GPU):

```yaml
name: my-model-name
# Default model parameters
parameters:
  # Relative to the models path
  model: llama.cpp-model.ggmlv3.q5_K_M.bin

context_size: 1024
threads: 1

f16: true # enable with GPU acceleration
gpu_layers: 22 # GPU Layers (only used when built with cublas)

```

For diffusers instead, it might look like this instead:

```yaml
name: stablediffusion
parameters:
  model: toonyou_beta6.safetensors
backend: diffusers
step: 30
f16: true
diffusers:
  pipeline_type: StableDiffusionPipeline
  cuda: true
  enable_parameters: "negative_prompt,num_inference_steps,clip_skip"
  scheduler_type: "k_dpmpp_sde"
```
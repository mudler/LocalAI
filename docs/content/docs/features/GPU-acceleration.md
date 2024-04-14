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
For accelleration for AMD or Metal HW is still in development, for additional details see the [build]({{%relref "docs/getting-started/build#Acceleration" %}})
{{% /alert %}}


## Model configuration

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

## CUDA(NVIDIA) acceleration

### Requirements

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

## ROCM(AMD) acceleration

There are a limited number of tested configurations for ROCm systems however most newer deditated GPU consumer grade devices seem to be supported under the current ROCm6 implementation. 

Due to the nature of ROCm it is best to run all implementations in containers as this limits the number of packages required for installation on host system, compatability and package versions for dependencies across all variations of OS must be tested independently if disired, please refer to the [build]({{%relref "docs/getting-started/build#Acceleration" %}}) documentation.

### Requirements

- `ROCm 6.x.x` compatible GPU/accelerator
- OS: `Ubuntu` (22.04, 20.04), `RHEL` (9.3, 9.2, 8.9, 8.8), `SLES` (15.5, 15.4)
- Installed to host: `amdgpu-dkms` and `rocm` >=6.0.0 as per ROCm documentation.

### Recommendations

- Do not use on a system running Wayland.
- If running with Xorg do not use GPU assigned for compute for desktop rendering.
- Ensure at least 100GB of free space on disk hosting container runtime and storing images prior to installation.

### Limitations

Ongoing verification testing of ROCm compatability with integrated backends.
Please note the following list of verified backends and devices.

### Verified 

The devices in the following list have been tested with `hipblas` images running `ROCm 6.0.0`

| Backend | Verified | Devices |
| ---- | ---- | ---- |
| llama.cpp | yes | Radeon VII (gfx906) |
| diffusers | yes | Radeon VII (gfx906) |
| piper | yes | Radeon VII (gfx906) |
| whisper | no | none |
| autogptq | no | none |
| bark | no | none |
| coqui | no | none |
| transformers | no | none |
| exllama | no | none |
| exllama2 | no | none |
| mamba | no | none |
| petals | no | none |
| sentencetransformers | no | none |
| transformers-musicgen | no | none |
| vall-e-x | no | none |
| vllm | no | none |

### Setup

1. Check your GPU LLVM target is compatible with the version of ROCm. This can be found in the [LLVM Docs](https://llvm.org/docs/AMDGPUUsage.html).
2. Check which ROCm version is compatible with your LLVM target and your chosen OS (pay special attention to supported kernel versions). See the following for compatability for ([ROCm 6.0.0](https://rocm.docs.amd.com/projects/install-on-linux/en/docs-6.0.0/reference/system-requirements.html)) or ([ROCm 6.0.2](https://rocm.docs.amd.com/projects/install-on-linux/en/latest/reference/system-requirements.html))
3. Install you chosen version of the `dkms` and `rocm` (it is recommended that the native package manager be used for this process for any OS as version changes are executed more easily via this method if updates are required). Take care to restart after installing `amdgpu-dkms` and before installing `rocm`, for details regarding this see the installation documentation for your chosen OS ([6.0.2](https://rocm.docs.amd.com/projects/install-on-linux/en/latest/how-to/native-install/index.html) or [6.0.0](https://rocm.docs.amd.com/projects/install-on-linux/en/docs-6.0.0/how-to/native-install/index.html))


#### Example (Docker/containerd)



#### Example (k8s)


### Notes

When installing the ROCM kernel driver on your system ensure that you are installing a newer version that that which is currently implemented in LocalAI (6.0.0 at time of writing).

AMD documentation indicates that this 

## Intel acceleration (sycl)

### Requirements

If building from source, you need to install [Intel oneAPI Base Toolkit](https://software.intel.com/content/www/us/en/develop/tools/oneapi/base-toolkit/download.html) and have the Intel drivers available in the system.

### Container images

To use SYCL, use the images with the `sycl-f16` or `sycl-f32` tag, for example `{{< version >}}-sycl-f32-core`, `{{< version >}}-sycl-f16-ffmpeg-core`, ...

The image list is on [quay](https://quay.io/repository/go-skynet/local-ai?tab=tags).

#### Example

To run LocalAI with Docker and sycl starting `phi-2`, you can use the following command as an example:

```bash
docker run -e DEBUG=true --privileged -ti -v $PWD/models:/build/models -p 8080:8080  -v /dev/dri:/dev/dri --rm quay.io/go-skynet/local-ai:master-sycl-f32-ffmpeg-core phi-2
```

### Notes

In addition to the commands to run LocalAI normally, you need to specify `--device /dev/dri` to docker, for example:

```bash
docker run --rm -ti --device /dev/dri -p 8080:8080 -e DEBUG=true -e MODELS_PATH=/models -e THREADS=1 -v $PWD/models:/models quay.io/go-skynet/local-ai:{{< version >}}-sycl-f16-ffmpeg-core
```

Note also that sycl does have a known issue to hang with `mmap: true`. You have to disable it in the model configuration if explicitly enabled.

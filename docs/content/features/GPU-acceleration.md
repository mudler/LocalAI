+++
disableToc = false
title = "⚡ GPU acceleration"
weight = 9
url = "/features/gpu-acceleration/"
+++

This page covers how to use LocalAI with GPU acceleration across different hardware vendors. For container image tags and registry details, see [Container Images]({{%relref "getting-started/container-images" %}}). For memory management with multiple GPU-accelerated models, see [VRAM Management]({{%relref "advanced/vram-management" %}}).

## Automatic Backend Detection

When you install a model from the gallery (or a YAML file), LocalAI intelligently detects the required backend and your system's capabilities, then downloads the correct version for you. Whether you're running on a standard CPU, an NVIDIA GPU, an AMD GPU, or an Intel GPU, LocalAI handles it automatically.

For advanced use cases or to override auto-detection, you can use the `LOCALAI_FORCE_META_BACKEND_CAPABILITY` environment variable. Here are the available options:

- `default`: Forces CPU-only backend. This is the fallback if no specific hardware is detected.
- `nvidia`: Forces backends compiled with CUDA support for NVIDIA GPUs.
- `amd`: Forces backends compiled with ROCm support for AMD GPUs.
- `intel`: Forces backends compiled with SYCL/oneAPI support for Intel GPUs.

## Model configuration

Depending on the model architecture and backend used, there might be different ways to enable GPU acceleration. It is required to configure the model you intend to use with a YAML config file. For example, for `llama.cpp` workloads a configuration file might look like this (where `gpu_layers` is the number of layers to offload to the GPU):

```yaml
name: my-model-name
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

### Multi-GPU Support

#### llama.cpp

For llama.cpp models, you can control which GPU layers are offloaded using `gpu_layers`. When multiple NVIDIA GPUs are present, llama.cpp distributes layers across available devices automatically. You can control GPU visibility with the `CUDA_VISIBLE_DEVICES` environment variable:

```bash
# Use only GPU 0 and GPU 1
docker run --gpus all -e CUDA_VISIBLE_DEVICES=0,1 ...
```

For AMD GPUs, use `HIP_VISIBLE_DEVICES` instead:

```bash
docker run --device /dev/dri --device /dev/kfd -e HIP_VISIBLE_DEVICES=0,1 ...
```

#### diffusers

For multi-GPU support with diffusers, configure the model with `tensor_parallel_size` set to the number of GPUs you want to use.

```yaml
name: stable-diffusion-multigpu
model: stabilityai/stable-diffusion-xl-base-1.0
backend: diffusers
parameters:
  tensor_parallel_size: 2 # Number of GPUs to use
```

The `tensor_parallel_size` parameter is set in the gRPC proto configuration (in `ModelOptions` message, field 55). When this is set to a value greater than 1, the diffusers backend automatically enables `device_map="auto"` to distribute the model across multiple GPUs.

#### Tips

- For optimal performance, use GPUs of the same type and memory capacity.
- Ensure you have sufficient GPU memory across all devices.
- When running multiple models concurrently, consider using [VRAM Management]({{%relref "advanced/vram-management" %}}) to automatically unload idle models.

## CUDA(NVIDIA) acceleration

### Requirements

Requirement: nvidia-container-toolkit (installation instructions [1](https://www.server-world.info/en/note?os=Ubuntu_22.04&p=nvidia&f=2) [2](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/install-guide.html))

If using a system with SELinux, ensure you have the policies installed, such as those [provided by nvidia](https://github.com/NVIDIA/dgx-selinux/)

To check what CUDA version do you need, you can either run `nvidia-smi` or `nvcc --version`.

Alternatively, you can also check nvidia-smi with docker:

```
docker run --runtime=nvidia --rm nvidia/cuda:12.8.0-base-ubuntu24.04 nvidia-smi
```

To use CUDA, use the images with the `cublas` tag, for example.

The image list is on [quay](https://quay.io/repository/go-skynet/local-ai?tab=tags):

- CUDA `11` tags: `master-gpu-nvidia-cuda-11`, `v1.40.0-gpu-nvidia-cuda-11`, ...
- CUDA `12` tags: `master-gpu-nvidia-cuda-12`, `v1.40.0-gpu-nvidia-cuda-12`, ...
- CUDA `13` tags: `master-gpu-nvidia-cuda-13`, `v1.40.0-gpu-nvidia-cuda-13`, ...

In addition to the commands to run LocalAI normally, you need to specify `--gpus all` to docker, for example:

```bash
docker run --rm -ti --gpus all -p 8080:8080 -e DEBUG=true -e MODELS_PATH=/models -e THREADS=1 -v $PWD/models:/models quay.io/go-skynet/local-ai:v1.40.0-gpu-nvidia-cuda12
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

Due to the nature of ROCm it is best to run all implementations in containers as this limits the number of packages required for installation on host system, compatibility and package versions for dependencies across all variations of OS must be tested independently if desired, please refer to the [build]({{%relref "installation/build#Acceleration" %}}) documentation.

### Requirements

- `ROCm 6.x.x` compatible GPU/accelerator
- OS: `Ubuntu` (22.04, 20.04), `RHEL` (9.3, 9.2, 8.9, 8.8), `SLES` (15.5, 15.4)
- Installed to host: `amdgpu-dkms` and `rocm` >=6.0.0 as per ROCm documentation.

### Recommendations

- Make sure to do not use GPU assigned for compute for desktop rendering.
- Ensure at least 100GB of free space on disk hosting container runtime and storing images prior to installation.

### Limitations

Ongoing verification testing of ROCm compatibility with integrated backends.
Please note the following list of verified backends and devices.

LocalAI hipblas images are built against the following targets: gfx900,gfx906,gfx908,gfx940,gfx941,gfx942,gfx90a,gfx1030,gfx1031,gfx1100,gfx1101

If your device is not one of these you must specify the corresponding `GPU_TARGETS` and specify `REBUILD=true`. Otherwise you don't need to specify these in the commands below.

### Verified

The devices in the following list have been tested with `hipblas` images running `ROCm 6.0.0`

| Backend | Verified | Devices |
| ---- | ---- | ---- |
| llama.cpp | yes | Radeon VII (gfx906) |
| diffusers | yes | Radeon VII (gfx906) |
| piper | yes | Radeon VII (gfx906) |
| whisper | no | none |
| coqui | no | none |
| transformers | no | none |
| sentencetransformers | no | none |
| transformers-musicgen | no | none |
| vllm | no | none |

**You can help by expanding this list.**

### System Prep

1. Check your GPU LLVM target is compatible with the version of ROCm. This can be found in the [LLVM Docs](https://llvm.org/docs/AMDGPUUsage.html).
2. Check which ROCm version is compatible with your LLVM target and your chosen OS (pay special attention to supported kernel versions). See the following for compatibility for ([ROCm 6.0.0](https://rocm.docs.amd.com/projects/install-on-linux/en/docs-6.0.0/reference/system-requirements.html)) or ([ROCm 6.0.2](https://rocm.docs.amd.com/projects/install-on-linux/en/latest/reference/system-requirements.html))
3. Install you chosen version of the `dkms` and `rocm` (it is recommended that the native package manager be used for this process for any OS as version changes are executed more easily via this method if updates are required). Take care to restart after installing `amdgpu-dkms` and before installing `rocm`, for details regarding this see the installation documentation for your chosen OS ([6.0.2](https://rocm.docs.amd.com/projects/install-on-linux/en/latest/how-to/native-install/index.html) or [6.0.0](https://rocm.docs.amd.com/projects/install-on-linux/en/docs-6.0.0/how-to/native-install/index.html))
4. Deploy. Yes it's that easy.

#### Setup Example (Docker/containerd)

The following are examples of the ROCm specific configuration elements required.

```yaml
    # For full functionality select a non-'core' image, version locking the image is recommended for debug purposes.
    image: quay.io/go-skynet/local-ai:master-aio-gpu-hipblas
    environment:
      - DEBUG=true
      # If your gpu is not already included in the current list of default targets the following build details are required.
      - REBUILD=true
      - BUILD_TYPE=hipblas
      - GPU_TARGETS=gfx906 # Example for Radeon VII
    devices:
      # AMD GPU only require the following devices be passed through to the container for offloading to occur.
      - /dev/dri
      - /dev/kfd
```

The same can also be executed as a `run` for your container runtime

```
docker run \
 -e DEBUG=true \
 -e REBUILD=true \
 -e BUILD_TYPE=hipblas \
 -e GPU_TARGETS=gfx906 \
 --device /dev/dri \
 --device /dev/kfd \
 quay.io/go-skynet/local-ai:master-aio-gpu-hipblas
```

Please ensure to add all other required environment variables, port forwardings, etc to your `compose` file or `run` command.

The rebuild process will take some time to complete when deploying these containers and it is recommended that you `pull` the image prior to deployment as depending on the version these images may be ~20GB in size.

#### Example (k8s) (Advanced Deployment/WIP)

For k8s deployments there is an additional step required before deployment, this is the deployment of the [ROCm/k8s-device-plugin](https://artifacthub.io/packages/helm/amd-gpu-helm/amd-gpu).
For any k8s environment the documentation provided by AMD from the ROCm project should be successful. It is recommended that if you use rke2 or OpenShift that you deploy the SUSE or RedHat provided version of this resource to ensure compatibility.
After this has been completed the [helm chart from go-skynet](https://github.com/go-skynet/helm-charts) can be configured and deployed mostly un-edited.

The following are details of the changes that should be made to ensure proper function.
While these details may be configurable in the `values.yaml` development of this Helm chart is ongoing and is subject to change.

The following details indicate the final state of the localai deployment relevant to GPU function.

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: {NAME}-local-ai
...
spec:
  ...
  template:
    ...
    spec:
      containers:
        - env:
            - name: HIP_VISIBLE_DEVICES
              value: '0'
              # This variable indicates the devices available to container (0:device1 1:device2 2:device3) etc.
              # For multiple devices (say device 1 and 3) the value would be equivalent to HIP_VISIBLE_DEVICES="0,2"
              # Please take note of this when an iGPU is present in host system as compatibility is not assured.
          ...
          resources:
            limits:
              amd.com/gpu: '1'
            requests:
              amd.com/gpu: '1'
```

This configuration has been tested on a 'custom' cluster managed by SUSE Rancher that was deployed on top of Ubuntu 22.04.4, certification of other configuration is ongoing and compatibility is not guaranteed.

### Notes

- When installing the ROCM kernel driver on your system ensure that you are installing an equal or newer version that that which is currently implemented in LocalAI (6.0.0 at time of writing).
- AMD documentation indicates that this will ensure functionality however your mileage may vary depending on the GPU and distro you are using.
- If you encounter an `Error 413` on attempting to upload an audio file or image for whisper or llava/bakllava on a k8s deployment, note that the ingress for your deployment may require the annotation `nginx.ingress.kubernetes.io/proxy-body-size: "25m"` to allow larger uploads. This may be included in future versions of the helm chart.

## Intel acceleration (sycl)

### Requirements

If building from source, you need to install [Intel oneAPI Base Toolkit](https://software.intel.com/content/www/us/en/develop/tools/oneapi/base-toolkit/download.html) and have the Intel drivers available in the system.

### Container images

To use SYCL, use the images with `gpu-intel` in the tag, for example `{{< version >}}-gpu-intel`, ...

The image list is on [quay](https://quay.io/repository/go-skynet/local-ai?tab=tags).

#### Example

To run LocalAI with Docker and sycl starting `phi-2`, you can use the following command as an example:

```bash
docker run -e DEBUG=true --privileged -ti -v $PWD/models:/models -p 8080:8080  -v /dev/dri:/dev/dri --rm quay.io/go-skynet/local-ai:master-gpu-intel phi-2
```

### Notes

In addition to the commands to run LocalAI normally, you need to specify `--device /dev/dri` to docker, for example:

```bash
docker run --rm -ti --device /dev/dri -p 8080:8080 -e DEBUG=true -e MODELS_PATH=/models -e THREADS=1 -v $PWD/models:/models quay.io/go-skynet/local-ai:{{< version >}}-gpu-intel
```

Note also that sycl does have a known issue to hang with `mmap: true`. You have to disable it in the model configuration if explicitly enabled.

## Vulkan acceleration

### Requirements

If using nvidia, follow the steps in the [CUDA](#cudanvidia-acceleration) section to configure your docker runtime to allow access to the GPU.

### Container images

To use Vulkan, use the images with the `vulkan` tag, for example `{{< version >}}-gpu-vulkan`.

#### Example

To run LocalAI with Docker and Vulkan, you can use the following command as an example:

```bash
docker run -p 8080:8080 -e DEBUG=true -v $PWD/models:/models localai/localai:latest-gpu-vulkan
```

### Notes

In addition to the commands to run LocalAI normally, you need to specify additional flags to pass the GPU hardware to the container.

These flags are the same as the sections above, depending on the hardware, for [nvidia](#cudanvidia-acceleration), [AMD](#rocmamd-acceleration) or [Intel](#intel-acceleration-sycl).

If you have mixed hardware, you can pass flags for multiple GPUs, for example:

```bash
docker run -p 8080:8080 -e DEBUG=true -v $PWD/models:/models \
--gpus=all \ # nvidia passthrough
--device /dev/dri --device /dev/kfd \ # AMD/Intel passthrough
localai/localai:latest-gpu-vulkan
```

## NVIDIA L4T (Jetson/ARM64) acceleration

LocalAI supports NVIDIA ARM64 devices including Jetson Nano, Jetson Xavier NX, Jetson AGX Orin, and DGX Spark. Pre-built container images are available for both CUDA 12 and CUDA 13.

For detailed setup instructions, platform compatibility, and build commands, see the dedicated [Running on Nvidia ARM64]({{%relref "reference/nvidia-l4t" %}}) page.

### Quick start

```bash
# Jetson AGX Orin (CUDA 12)
docker run -e DEBUG=true -p 8080:8080 -v $PWD/models:/models \
  --runtime nvidia --gpus all \
  quay.io/go-skynet/local-ai:latest-nvidia-l4t-arm64

# DGX Spark (CUDA 13)
docker run -e DEBUG=true -p 8080:8080 -v $PWD/models:/models \
  --runtime nvidia --gpus all \
  quay.io/go-skynet/local-ai:latest-nvidia-l4t-arm64-cuda-13
```

## GPU monitoring

Use these vendor-specific tools to verify that LocalAI is using your GPU and to monitor resource usage during inference.

### NVIDIA

```bash
# Real-time GPU utilization, memory, temperature
nvidia-smi

# Continuous monitoring (updates every 1 second)
nvidia-smi --loop=1

# Inside a container
docker run --rm --gpus all nvidia/cuda:12.8.0-base-ubuntu24.04 nvidia-smi
```

Look for non-zero **GPU-Util** and **Memory-Usage** values while running inference to confirm GPU acceleration is active.

### AMD

```bash
# ROCm System Management Interface
rocm-smi

# Continuous monitoring
watch -n1 rocm-smi

# Show detailed GPU info
rocm-smi --showallinfo
```

### Intel

```bash
# Intel GPU top (part of intel-gpu-tools)
sudo intel_gpu_top

# List available Intel GPUs
sycl-ls
```

## Troubleshooting

### GPU not detected in container

- **NVIDIA**: Ensure `nvidia-container-toolkit` is installed and the Docker runtime is configured. Test with `docker run --rm --gpus all nvidia/cuda:12.8.0-base-ubuntu24.04 nvidia-smi`.
- **AMD**: Ensure `/dev/dri` and `/dev/kfd` are passed to the container and that `amdgpu-dkms` is installed on the host.
- **Intel**: Ensure `/dev/dri` is passed to the container and Intel GPU drivers are installed on the host.

### Model loads on CPU instead of GPU

- Check that `gpu_layers` is set in your model YAML configuration. Setting it to a high number (e.g., `999`) offloads all possible layers to GPU.
- Verify you are using a GPU-enabled container image (tags containing `gpu-nvidia-cuda`, `gpu-hipblas`, `gpu-intel`, etc.).
- Enable `DEBUG=true` and check the logs for GPU initialization messages.

### Out of memory (OOM) errors

- Reduce `gpu_layers` to offload fewer layers, keeping some on CPU.
- Lower `context_size` to reduce VRAM usage.
- Use [VRAM Management]({{%relref "advanced/vram-management" %}}) to automatically unload idle models when running multiple models.
- Use quantized models (e.g., Q4_K_M) which require less memory than full-precision models.

### ROCm: unsupported GPU target

If your AMD GPU is not in the default target list, set `REBUILD=true` and `GPU_TARGETS` to your device's gfx target:

```bash
docker run -e REBUILD=true -e BUILD_TYPE=hipblas -e GPU_TARGETS=gfx1030 \
  --device /dev/dri --device /dev/kfd \
  quay.io/go-skynet/local-ai:master-aio-gpu-hipblas
```

### Intel SYCL: model hangs

SYCL has a known issue where models hang when `mmap: true` is set. Ensure `mmap` is disabled in the model configuration:

```yaml
mmap: false
```

### Slow performance or unexpected CPU fallback

- Ensure `f16: true` is set in the model YAML for GPU-accelerated backends.
- Set `threads: 1` when using full GPU offloading to avoid CPU thread contention.
- Verify the correct `BUILD_TYPE` matches your hardware (e.g., `cublas` for NVIDIA, `hipblas` for AMD).

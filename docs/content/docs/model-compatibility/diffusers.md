
+++
disableToc = false
title = "🧨 Diffusers"
weight = 4
+++

[Diffusers](https://huggingface.co/docs/diffusers/index) is the go-to library for state-of-the-art pretrained diffusion models for generating images, audio, and even 3D structures of molecules. LocalAI has a diffusers backend which allows image generation using the `diffusers` library.

![anime_girl](https://github.com/go-skynet/LocalAI/assets/2420543/8aaca62a-e864-4011-98ae-dcc708103928)
(Generated with [AnimagineXL](https://huggingface.co/Linaqruf/animagine-xl))

Note: currently only the image generation is supported. It is experimental, so you might encounter some issues on models which weren't tested yet.

## Setup

This is an extra backend - in the container is already available and there is nothing to do for the setup.

## Model setup

The models will be downloaded the first time you use the backend from `huggingface` automatically.

Create a model configuration file in the `models` directory, for instance to use `Linaqruf/animagine-xl` with CPU:

```yaml
name: animagine-xl
parameters:
  model: Linaqruf/animagine-xl
backend: diffusers
cuda: true
f16: true
diffusers:
  scheduler_type: euler_a
```

## Local models

You can also use local models, or modify some parameters like `clip_skip`, `scheduler_type`, for instance:

```yaml
name: stablediffusion
parameters:
  model: toonyou_beta6.safetensors
backend: diffusers
step: 30
f16: true
cuda: true
diffusers:
  pipeline_type: StableDiffusionPipeline
  enable_parameters: "negative_prompt,num_inference_steps,clip_skip"
  scheduler_type: "k_dpmpp_sde"
  cfg_scale: 8
  clip_skip: 11
```

## Configuration parameters

The following parameters are available in the configuration file:

| Parameter | Description | Default |
| --- | --- | --- |
| `f16` | Force the usage of `float16` instead of `float32` | `false` |
| `step` | Number of steps to run the model for | `30` |
| `cuda` | Enable CUDA acceleration | `false` |
| `enable_parameters` | Parameters to enable for the model | `negative_prompt,num_inference_steps,clip_skip` |
| `scheduler_type` | Scheduler type | `k_dpp_sde` |
| `cfg_scale` | Configuration scale | `8` |
| `clip_skip` | Clip skip | None |
| `pipeline_type` | Pipeline type | `AutoPipelineForText2Image` |

There are available several types of schedulers:

| Scheduler | Description |
| --- | --- |
| `ddim` | DDIM |
| `pndm` | PNDM |
| `heun` | Heun |
| `unipc` | UniPC |
| `euler` | Euler |
| `euler_a` | Euler a |
| `lms` | LMS |
| `k_lms` | LMS Karras |
| `dpm_2` | DPM2 |
| `k_dpm_2` | DPM2 Karras |
| `dpm_2_a` | DPM2 a |
| `k_dpm_2_a` | DPM2 a Karras |
| `dpmpp_2m` | DPM++ 2M |
| `k_dpmpp_2m` | DPM++ 2M Karras |
| `dpmpp_sde` | DPM++ SDE |
| `k_dpmpp_sde` | DPM++ SDE Karras |
| `dpmpp_2m_sde` | DPM++ 2M SDE |
| `k_dpmpp_2m_sde` | DPM++ 2M SDE Karras |

Pipelines types available:

| Pipeline type | Description |
| --- | --- |
| `StableDiffusionPipeline` | Stable diffusion pipeline |
| `StableDiffusionImg2ImgPipeline` | Stable diffusion image to image pipeline |
| `StableDiffusionDepth2ImgPipeline` | Stable diffusion depth to image pipeline |
| `DiffusionPipeline` | Diffusion pipeline |
| `StableDiffusionXLPipeline` | Stable diffusion XL pipeline |

## Usage

### Text to Image
Use the `image` generation endpoint with the `model` name from the configuration file:

```bash
curl http://localhost:8080/v1/images/generations \
    -H "Content-Type: application/json" \
    -d '{
      "prompt": "<positive prompt>|<negative prompt>", 
      "model": "animagine-xl", 
      "step": 51,
      "size": "1024x1024" 
    }'
```

## Image to Image

https://huggingface.co/docs/diffusers/using-diffusers/img2img

An example model (GPU):
```yaml
name: stablediffusion-edit
parameters:
  model: nitrosocke/Ghibli-Diffusion
backend: diffusers
step: 25
cuda: true
f16: true
diffusers:
  pipeline_type: StableDiffusionImg2ImgPipeline
  enable_parameters: "negative_prompt,num_inference_steps,image"
```

```bash
IMAGE_PATH=/path/to/your/image
(echo -n '{"file": "'; base64 $IMAGE_PATH; echo '", "prompt": "a sky background","size": "512x512","model":"stablediffusion-edit"}') |
curl -H "Content-Type: application/json" -d @-  http://localhost:8080/v1/images/generations
```

## Depth to Image

https://huggingface.co/docs/diffusers/using-diffusers/depth2img

```yaml
name: stablediffusion-depth
parameters:
  model: stabilityai/stable-diffusion-2-depth
backend: diffusers
step: 50
# Force CPU usage
f16: true
cuda: true
diffusers:
  pipeline_type: StableDiffusionDepth2ImgPipeline
  enable_parameters: "negative_prompt,num_inference_steps,image"
  cfg_scale: 6
```

```bash
(echo -n '{"file": "'; base64 ~/path/to/image.jpeg; echo '", "prompt": "a sky background","size": "512x512","model":"stablediffusion-depth"}') |
curl -H "Content-Type: application/json" -d @-  http://localhost:8080/v1/images/generations
```

## img2vid


```yaml
name: img2vid
parameters:
  model: stabilityai/stable-video-diffusion-img2vid
backend: diffusers
step: 25
# Force CPU usage
f16: true
cuda: true
diffusers:
  pipeline_type: StableVideoDiffusionPipeline
```

```bash
(echo -n '{"file": "https://huggingface.co/datasets/huggingface/documentation-images/resolve/main/diffusers/svd/rocket.png?download=true","size": "512x512","model":"img2vid"}') |
curl -H "Content-Type: application/json" -X POST -d @- http://localhost:8080/v1/images/generations
```

## txt2vid

```yaml
name: txt2vid
parameters:
  model: damo-vilab/text-to-video-ms-1.7b
backend: diffusers
step: 25
# Force CPU usage
f16: true
cuda: true
diffusers:
  pipeline_type: VideoDiffusionPipeline
  cuda: true
```

```bash
(echo -n '{"prompt": "spiderman surfing","size": "512x512","model":"txt2vid"}') |
curl -H "Content-Type: application/json" -X POST -d @- http://localhost:8080/v1/images/generations
```
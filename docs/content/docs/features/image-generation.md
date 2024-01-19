
+++
disableToc = false
title = "🎨 Image generation"
weight = 12
url = "/features/image-generation/"
+++

![anime_girl](https://github.com/go-skynet/LocalAI/assets/2420543/8aaca62a-e864-4011-98ae-dcc708103928)
(Generated with [AnimagineXL](https://huggingface.co/Linaqruf/animagine-xl))

LocalAI supports generating images with Stable diffusion, running on CPU using C++ and Python implementations.

## Usage

OpenAI docs: https://platform.openai.com/docs/api-reference/images/create

To generate an image you can send a POST request to the `/v1/images/generations` endpoint with the instruction as the request body:

```bash
# 512x512 is supported too
curl http://localhost:8080/v1/images/generations -H "Content-Type: application/json" -d '{
  "prompt": "A cute baby sea otter",
  "size": "256x256"
}'
```

Available additional parameters: `mode`, `step`.

Note: To set a negative prompt, you can split the prompt with `|`, for instance: `a cute baby sea otter|malformed`.

```bash
curl http://localhost:8080/v1/images/generations -H "Content-Type: application/json" -d '{
  "prompt": "floating hair, portrait, ((loli)), ((one girl)), cute face, hidden hands, asymmetrical bangs, beautiful detailed eyes, eye shadow, hair ornament, ribbons, bowties, buttons, pleated skirt, (((masterpiece))), ((best quality)), colorful|((part of the head)), ((((mutated hands and fingers)))), deformed, blurry, bad anatomy, disfigured, poorly drawn face, mutation, mutated, extra limb, ugly, poorly drawn hands, missing limb, blurry, floating limbs, disconnected limbs, malformed hands, blur, out of focus, long neck, long body, Octane renderer, lowres, bad anatomy, bad hands, text",
  "size": "256x256"
}'
```

## Backends

### stablediffusion-cpp

| mode=0                                                                                                                | mode=1 (winograd/sgemm)                                                                                                                |
|------------------------------------------------------------------------------------------------------------------------|------------------------------------------------------------------------------------------------------------------------|
| ![test](https://github.com/go-skynet/LocalAI/assets/2420543/7145bdee-4134-45bb-84d4-f11cb08a5638)                      | ![b643343452981](https://github.com/go-skynet/LocalAI/assets/2420543/abf14de1-4f50-4715-aaa4-411d703a942a)          |
| ![b6441997879](https://github.com/go-skynet/LocalAI/assets/2420543/d50af51c-51b7-4f39-b6c2-bf04c403894c)              | ![winograd2](https://github.com/go-skynet/LocalAI/assets/2420543/1935a69a-ecce-4afc-a099-1ac28cb649b3)                |
| ![winograd](https://github.com/go-skynet/LocalAI/assets/2420543/1979a8c4-a70d-4602-95ed-642f382f6c6a)                | ![winograd3](https://github.com/go-skynet/LocalAI/assets/2420543/e6d184d4-5002-408f-b564-163986e1bdfb)                |

Note: image generator supports images up to 512x512. You can use other tools however to upscale the image, for instance: https://github.com/upscayl/upscayl.

#### Setup

Note: In order to use the `images/generation` endpoint with the `stablediffusion` C++ backend, you need to build LocalAI with `GO_TAGS=stablediffusion`. If you are using the container images, it is already enabled.

{{< tabs >}}
{{% tab name="Prepare the model in runtime" %}}

While the API is running, you can install the model by using the `/models/apply` endpoint and point it to the `stablediffusion` model in the [models-gallery](https://github.com/go-skynet/model-gallery#image-generation-stable-diffusion):

```bash
curl http://localhost:8080/models/apply -H "Content-Type: application/json" -d '{
  "url": "github:go-skynet/model-gallery/stablediffusion.yaml"
}'
```

{{% /tab %}}
{{% tab name="Automatically prepare the model before start" %}}

You can set the `PRELOAD_MODELS` environment variable:

```bash
PRELOAD_MODELS=[{"url": "github:go-skynet/model-gallery/stablediffusion.yaml"}]
```

or as arg:

```bash
local-ai --preload-models '[{"url": "github:go-skynet/model-gallery/stablediffusion.yaml"}]'
```

or in a YAML file:

```bash
local-ai --preload-models-config "/path/to/yaml"
```

YAML:

```yaml
- url: github:go-skynet/model-gallery/stablediffusion.yaml
```

{{% /tab %}}
{{% tab name="Install manually" %}}

1. Create a model file `stablediffusion.yaml` in the models folder:

```yaml
name: stablediffusion
backend: stablediffusion
parameters:
  model: stablediffusion_assets
```

2. Create a `stablediffusion_assets` directory inside your `models` directory
3. Download the ncnn assets from https://github.com/EdVince/Stable-Diffusion-NCNN#out-of-box and place them in `stablediffusion_assets`.

The models directory should look like the following:

```bash
models
├── stablediffusion_assets
│   ├── AutoencoderKL-256-256-fp16-opt.param
│   ├── AutoencoderKL-512-512-fp16-opt.param
│   ├── AutoencoderKL-base-fp16.param
│   ├── AutoencoderKL-encoder-512-512-fp16.bin
│   ├── AutoencoderKL-fp16.bin
│   ├── FrozenCLIPEmbedder-fp16.bin
│   ├── FrozenCLIPEmbedder-fp16.param
│   ├── log_sigmas.bin
│   ├── tmp-AutoencoderKL-encoder-256-256-fp16.param
│   ├── UNetModel-256-256-MHA-fp16-opt.param
│   ├── UNetModel-512-512-MHA-fp16-opt.param
│   ├── UNetModel-base-MHA-fp16.param
│   ├── UNetModel-MHA-fp16.bin
│   └── vocab.txt
└── stablediffusion.yaml
```

{{% /tab %}}

{{< /tabs >}}

### Diffusers

[Diffusers](https://huggingface.co/docs/diffusers/index) is the go-to library for state-of-the-art pretrained diffusion models for generating images, audio, and even 3D structures of molecules. LocalAI has a diffusers backend which allows image generation using the `diffusers` library.

![anime_girl](https://github.com/go-skynet/LocalAI/assets/2420543/8aaca62a-e864-4011-98ae-dcc708103928)
(Generated with [AnimagineXL](https://huggingface.co/Linaqruf/animagine-xl))

#### Model setup

The models will be downloaded the first time you use the backend from `huggingface` automatically.

Create a model configuration file in the `models` directory, for instance to use `Linaqruf/animagine-xl` with CPU:

```yaml
name: animagine-xl
parameters:
  model: Linaqruf/animagine-xl
backend: diffusers

# Force CPU usage - set to true for GPU
f16: false
diffusers:
  cuda: false # Enable for GPU usage (CUDA)
  scheduler_type: euler_a
```

#### Dependencies

This is an extra backend - in the container is already available and there is nothing to do for the setup. Do not use *core* images (ending with `-core`). If you are building manually, see the [build instructions]({{%relref "docs/getting-started/build" %}}).

#### Model setup

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

#### Local models

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

#### Configuration parameters

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

#### Usage

#### Text to Image
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

#### Image to Image

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

#### Depth to Image

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

#### img2vid


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

#### txt2vid

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
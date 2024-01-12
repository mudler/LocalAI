
+++
disableToc = false
title = "🎨 Image generation"
weight = 2
+++

![anime_girl](https://github.com/go-skynet/LocalAI/assets/2420543/8aaca62a-e864-4011-98ae-dcc708103928)
(Generated with [AnimagineXL](https://huggingface.co/Linaqruf/animagine-xl))

LocalAI supports generating images with Stable diffusion, running on CPU using a C++ implementation, [Stable-Diffusion-NCNN](https://github.com/EdVince/Stable-Diffusion-NCNN) ([binding](https://github.com/mudler/go-stable-diffusion)) and [🧨 Diffusers]({{%relref "docs/model-compatibility/diffusers" %}}).

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

## stablediffusion-cpp

| mode=0                                                                                                                | mode=1 (winograd/sgemm)                                                                                                                |
|------------------------------------------------------------------------------------------------------------------------|------------------------------------------------------------------------------------------------------------------------|
| ![test](https://github.com/go-skynet/LocalAI/assets/2420543/7145bdee-4134-45bb-84d4-f11cb08a5638)                      | ![b643343452981](https://github.com/go-skynet/LocalAI/assets/2420543/abf14de1-4f50-4715-aaa4-411d703a942a)          |
| ![b6441997879](https://github.com/go-skynet/LocalAI/assets/2420543/d50af51c-51b7-4f39-b6c2-bf04c403894c)              | ![winograd2](https://github.com/go-skynet/LocalAI/assets/2420543/1935a69a-ecce-4afc-a099-1ac28cb649b3)                |
| ![winograd](https://github.com/go-skynet/LocalAI/assets/2420543/1979a8c4-a70d-4602-95ed-642f382f6c6a)                | ![winograd3](https://github.com/go-skynet/LocalAI/assets/2420543/e6d184d4-5002-408f-b564-163986e1bdfb)                |

Note: image generator supports images up to 512x512. You can use other tools however to upscale the image, for instance: https://github.com/upscayl/upscayl.

### Setup

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

## Diffusers

This is an extra backend - in the container is already available and there is nothing to do for the setup.

### Model setup

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

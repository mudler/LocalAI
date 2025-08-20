
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

### stablediffusion-ggml

This backend is based on [stable-diffusion.cpp](https://github.com/leejet/stable-diffusion.cpp). Every model supported by that backend is supported indeed with LocalAI.


#### Setup

There are already several models in the gallery that are available to install and get up and running with this backend, you can for example run flux by searching it in the Model gallery (`flux.1-dev-ggml`) or start LocalAI with `run`:

```bash
local-ai run flux.1-dev-ggml
```

To use a custom model, you can follow these steps:

1. Create a model file `stablediffusion.yaml` in the models folder:

```yaml
name: stablediffusion
backend: stablediffusion-ggml
parameters:
  model: gguf_model.gguf
step: 25
cfg_scale: 4.5
options:
- "clip_l_path:clip_l.safetensors"
- "clip_g_path:clip_g.safetensors"
- "t5xxl_path:t5xxl-Q5_0.gguf"
- "sampler:euler"
```

2. Download the required assets to the `models` repository
3. Start LocalAI


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
  clip_skip: 11

cfg_scale: 8
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
| `lora_adapters` | A list of lora adapters (file names relative to model directory) to apply | None |
| `lora_scales` | A list of lora scales (floats) to apply | None |


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
| `StableVideoDiffusionPipeline` | Stable video diffusion pipeline |
| `AutoPipelineForText2Image` | Automatic detection pipeline for text to image |
| `VideoDiffusionPipeline` | Video diffusion pipeline |
| `StableDiffusion3Pipeline` | Stable diffusion 3 pipeline |
| `FluxPipeline` | Flux pipeline |
| `FluxTransformer2DModel` | Flux transformer 2D model |
| `SanaPipeline` | Sana pipeline |

##### Advanced: Additional parameters

Additional arbitrarly parameters can be specified in the option field in key/value separated by `:`:

```yaml
name: animagine-xl
# ...
options:
- "cfg_scale:6"
```

**Note**: There is no complete parameter list. Any parameter can be passed arbitrarly and is passed to the model directly as argument to the pipeline. Different pipelines/implementations support different parameters.

The example above, will result in the following python code when generating images:

```python
pipe(
    prompt="A cute baby sea otter", # Options passed via API
    size="256x256", # Options passed via API
    cfg_scale=6 # Additional parameter passed via configuration file
)
```

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

##### 🖼️ Flux kontext with `stable-diffusion.cpp`

LocalAI supports Flux Kontext and can be used to edit images via the API:

Install with:

```local-ai run flux.1-kontext-dev```

To test:

```
curl http://localhost:8080/v1/images/generations -H "Content-Type: application/json" -d '{
  "model": "flux.1-kontext-dev",
  "prompt": "change 'flux.cpp' to 'LocalAI'",
  "size": "256x256",
  "ref_images": [
  	"https://raw.githubusercontent.com/leejet/stable-diffusion.cpp/master/assets/flux/flux1-dev-q8_0.png"
  ]
}'
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

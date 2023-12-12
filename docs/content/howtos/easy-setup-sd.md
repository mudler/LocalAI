+++
disableToc = false
title = "Easy Setup - Stable Diffusion"
weight = 2
+++

{{% notice note %}}
- There is a error in the v2.0 docker image working with ``StableDiffusionXLPipeline``, This page will use a known working pipeline for now, sorry for the delay!
- For a working yaml please try this yaml file - [Download Link 1](https://tea-cup.midori-ai.xyz/download/167a0ade-2bc0-423b-929d-f44be9b3d3e5-stablediffusion.yaml) / [Download Link 2](https://github.com/lunamidori5/localai-lunademo/blob/main/models/stablediffusion.yaml)
{{% /notice %}}

To set up a Stable Diffusion model is super easy.
In your models folder make a file called ``stablediffusion.yaml``, then edit that file with the following. (You can change ``Linaqruf/animagine-xl`` with what ever ``sd-lx`` model you would like.
```yaml
name: stablediffusion

parameters:
  model: dreamlike-art/dreamlike-anime-1.0
backend: diffusers

f16: true # Force GPU usage - set to false for CPU
threads: 10
low_vram: true
mmap: false
mmlock: false

diffusers:
  pipeline_type: StableDiffusionPipeline
  cuda: true # Force GPU usage - set to false for CPU
  scheduler_type: dpm_2_a
  enable_parameters: "negative_prompt,num_inference_steps"
```
Then run 
```bash
docker compose restart
```

If you are using docker and are unable to use stable diffusion, you will need to update the docker compose file in the localai folder. Run these commands to update it.
```bash
docker compose down
```

Then in your ``.env`` file uncomment this line.
```yaml
COMPEL=0
```

After that we can reinstall the LocalAI docker VM by running in the localai folder with the ``docker-compose.yaml`` file in it
```bash
docker compose up -d
```

Then to download and setup the model, Just send in a normal ``OpenAI`` request! LocalAI will do the rest!
```bash
curl http://localhost:8080/v1/images/generations -H "Content-Type: application/json" -d '{
  "prompt": "Two Boxes, 1blue, 1red",
  "size": "256x256"
}'
```

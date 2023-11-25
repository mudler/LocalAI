
+++
disableToc = false
title = "Getting started"
weight = 1
url = '/basics/getting_started/'
+++

`LocalAI` is available as a container image and binary. You can check out all the available images with corresponding tags [here](https://quay.io/repository/go-skynet/local-ai?tab=tags&tag=latest).

### How to get started
For a always up to date step by step how to of setting up LocalAI, Please see our [How to]({{%relref "howtos" %}}) page.

### Fast Setup
The easiest way to run LocalAI is by using [`docker compose`](https://docs.docker.com/compose/install/) or with [Docker](https://docs.docker.com/engine/install/) (to build locally, see the [build section]({{%relref "build" %}})). The following example uses `docker compose`:

```bash

git clone https://github.com/go-skynet/LocalAI

cd LocalAI

# (optional) Checkout a specific LocalAI tag
# git checkout -b build <TAG>

# copy your models to models/
cp your-model.bin models/

# (optional) Edit the .env file to set things like context size and threads
# vim .env

# start with docker compose
docker compose up -d --pull always
# or you can build the images with:
# docker compose up -d --build

# Now API is accessible at localhost:8080
curl http://localhost:8080/v1/models
# {"object":"list","data":[{"id":"your-model.bin","object":"model"}]}

curl http://localhost:8080/v1/completions -H "Content-Type: application/json" -d '{
     "model": "your-model.bin",
     "prompt": "A long time ago in a galaxy far, far away",
     "temperature": 0.7
   }'
```

### Example: Use luna-ai-llama2 model with `docker compose`


```bash
# Clone LocalAI
git clone https://github.com/go-skynet/LocalAI

cd LocalAI

# (optional) Checkout a specific LocalAI tag
# git checkout -b build <TAG>

# Download luna-ai-llama2 to models/
wget https://huggingface.co/TheBloke/Luna-AI-Llama2-Uncensored-GGUF/resolve/main/luna-ai-llama2-uncensored.Q4_0.gguf -O models/luna-ai-llama2

# Use a template from the examples
cp -rf prompt-templates/getting_started.tmpl models/luna-ai-llama2.tmpl

# (optional) Edit the .env file to set things like context size and threads
# vim .env

# start with docker compose
docker compose up -d --pull always
# or you can build the images with:
# docker compose up -d --build
# Now API is accessible at localhost:8080
curl http://localhost:8080/v1/models
# {"object":"list","data":[{"id":"luna-ai-llama2","object":"model"}]}

curl http://localhost:8080/v1/chat/completions -H "Content-Type: application/json" -d '{
     "model": "luna-ai-llama2",
     "messages": [{"role": "user", "content": "How are you?"}],
     "temperature": 0.9
   }'

# {"model":"luna-ai-llama2","choices":[{"message":{"role":"assistant","content":"I'm doing well, thanks. How about you?"}}]}
```

{{% notice note %}}
- If running on Apple Silicon (ARM) it is **not** suggested to run on Docker due to emulation. Follow the [build instructions]({{%relref "build" %}}) to use Metal acceleration for full GPU support.
- If you are running Apple x86_64 you can use `docker`, there is no additional gain into building it from source.
- If you are on Windows, please run ``docker-compose`` not ``docker compose`` and make sure the project is in the Linux Filesystem, otherwise loading models might be slow. For more Info: [Microsoft Docs](https://learn.microsoft.com/en-us/windows/wsl/filesystems)
{{% /notice %}}

### From binaries

LocalAI binary releases are available in [Github](https://github.com/go-skynet/LocalAI/releases).

You can control LocalAI with command line arguments, to specify a binding address, or the number of threads.

<details>

Usage:

```
local-ai --models-path <model_path> [--address <address>] [--threads <num_threads>]
```

| Parameter                      | Environmental Variable          | Default Variable                                   | Description                                                         |
| ------------------------------ | ------------------------------- | -------------------------------------------------- | ------------------------------------------------------------------- |
| --f16                          | $F16                            | false                                              | Enable f16 mode                                                     |
| --debug                        | $DEBUG                          | false                                              | Enable debug mode                                                   |
| --cors                         | $CORS                           | false                                              | Enable CORS support                                                 |
| --cors-allow-origins value     | $CORS_ALLOW_ORIGINS             |                                                    | Specify origins allowed for CORS                                     |
| --threads value                | $THREADS                        | 4    | Number of threads to use for parallel computation                    |
| --models-path value            | $MODELS_PATH                    | ./models       | Path to the directory containing models used for inferencing        |
| --preload-models value         | $PRELOAD_MODELS                 |           | List of models to preload in JSON format at startup                  |
| --preload-models-config value  | $PRELOAD_MODELS_CONFIG          |  | A config with a list of models to apply at startup. Specify the path to a YAML config file |
| --config-file value            | $CONFIG_FILE                    |                                         | Path to the config file                                             |
| --address value                | $ADDRESS                        | :8080                    | Specify the bind address for the API server                         |
| --image-path value             | $IMAGE_PATH                     |                                     | Path to the directory used to store generated images                             |
| --context-size value           | $CONTEXT_SIZE                   | 512                 | Default context size of the model                                   |
| --upload-limit value           | $UPLOAD_LIMIT                   | 15                         | Default upload limit in megabytes (audio file upload)                                  |
| --galleries                    | $GALLERIES                      |                                                    | Allows to set galleries from command line                           |

</details>

### Docker

LocalAI has a set of images to support CUDA, ffmpeg and 'vanilla' (CPU-only). The image list is on [quay](https://quay.io/repository/go-skynet/local-ai?tab=tags):

- Vanilla images tags: `master`, `v1.40.0`, `latest`, ...
- FFmpeg images tags: `master-ffmpeg`, `v1.40.0-ffmpeg`, ...
- CUDA `11` tags: `master-cublas-cuda11`, `v1.40.0-cublas-cuda11`, ...
- CUDA `12` tags: `master-cublas-cuda12`, `v1.40.0-cublas-cuda12`, ...
- CUDA `11` + FFmpeg tags: `master-cublas-cuda11-ffmpeg`, `v1.40.0-cublas-cuda11-ffmpeg`, ...
- CUDA `12` + FFmpeg tags: `master-cublas-cuda12-ffmpeg`, `v1.40.0-cublas-cuda12-ffmpeg`, ...

Example:

- Standard (GPT + `stablediffusion`): `quay.io/go-skynet/local-ai:latest`
- FFmpeg: `quay.io/go-skynet/local-ai:v1.40.0-ffmpeg`
- CUDA 11+FFmpeg: `quay.io/go-skynet/local-ai:v1.40.0-cublas-cuda11-ffmpeg`
- CUDA 12+FFmpeg: `quay.io/go-skynet/local-ai:v1.40.0-cublas-cuda12-ffmpeg`

Example of starting the API with `docker`:

```bash
docker run -p 8080:8080 -v $PWD/models:/models -ti --rm quay.io/go-skynet/local-ai:latest --models-path /models --context-size 700 --threads 4
```

You should see:
```
┌───────────────────────────────────────────────────┐
│                   Fiber v2.42.0                   │
│               http://127.0.0.1:8080               │
│       (bound on host 0.0.0.0 and port 8080)       │
│                                                   │
│ Handlers ............. 1  Processes ........... 1 │
│ Prefork ....... Disabled  PID ................. 1 │
└───────────────────────────────────────────────────┘
```

{{% notice note %}}
Note: the binary inside the image is pre-compiled, and might not suite all CPUs.
To enable CPU optimizations for the execution environment,
the default behavior is to rebuild when starting the container.
To disable this auto-rebuild behavior,
set the environment variable `REBUILD` to `false`.

See [docs on all environment variables]({{%relref "advanced#environment-variables" %}})
for more info.
{{% /notice %}}

#### CUDA:

Requirement: nvidia-container-toolkit (installation instructions [1](https://www.server-world.info/en/note?os=Ubuntu_22.04&p=nvidia&f=2) [2](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/install-guide.html))

You need to run the image with `--gpus all`, and

```
docker run --rm -ti --gpus all -p 8080:8080 -e DEBUG=true -e MODELS_PATH=/models -e PRELOAD_MODELS='[{"url": "github:go-skynet/model-gallery/openllama_7b.yaml", "name": "gpt-3.5-turbo", "overrides": { "f16":true, "gpu_layers": 35, "mmap": true, "batch": 512 } } ]' -e THREADS=1 -v $PWD/models:/models quay.io/go-skynet/local-ai:v1.40.0-cublas-cuda12
```

In the terminal where LocalAI was started, you should see:

```
5:13PM DBG Config overrides map[gpu_layers:10]
5:13PM DBG Checking "open-llama-7b-q4_0.bin" exists and matches SHA
5:13PM DBG Downloading "https://huggingface.co/SlyEcho/open_llama_7b_ggml/resolve/main/open-llama-7b-q4_0.bin"
5:13PM DBG Downloading open-llama-7b-q4_0.bin: 393.4 MiB/3.5 GiB (10.88%) ETA: 40.965550709s
5:13PM DBG Downloading open-llama-7b-q4_0.bin: 870.8 MiB/3.5 GiB (24.08%) ETA: 31.526866642s
5:13PM DBG Downloading open-llama-7b-q4_0.bin: 1.3 GiB/3.5 GiB (36.26%) ETA: 26.37351405s
5:13PM DBG Downloading open-llama-7b-q4_0.bin: 1.7 GiB/3.5 GiB (48.64%) ETA: 21.11682624s
5:13PM DBG Downloading open-llama-7b-q4_0.bin: 2.2 GiB/3.5 GiB (61.49%) ETA: 15.656029361s
5:14PM DBG Downloading open-llama-7b-q4_0.bin: 2.6 GiB/3.5 GiB (74.33%) ETA: 10.360950226s
5:14PM DBG Downloading open-llama-7b-q4_0.bin: 3.1 GiB/3.5 GiB (87.05%) ETA: 5.205663978s
5:14PM DBG Downloading open-llama-7b-q4_0.bin: 3.5 GiB/3.5 GiB (99.85%) ETA: 61.269714ms
5:14PM DBG File "open-llama-7b-q4_0.bin" downloaded and verified
5:14PM DBG Prompt template "openllama-completion" written
5:14PM DBG Prompt template "openllama-chat" written
5:14PM DBG Written config file /models/gpt-3.5-turbo.yaml
```

LocalAI will download automatically the OpenLLaMa model and run with GPU. Wait for the download to complete. You can also avoid automatic download of the model by not specifying a `PRELOAD_MODELS` variable. For compatible models with GPU support see the [model compatibility table]({{%relref "model-compatibility" %}}).

To test that the API is working run in another terminal:

```
curl http://localhost:8080/v1/chat/completions -H "Content-Type: application/json" -d '{
     "model": "gpt-3.5-turbo",
     "messages": [{"role": "user", "content": "What is an alpaca?"}],
     "temperature": 0.1
   }'
```

And if the GPU inferencing is working, you should be able to see something like:

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

{{% notice note %}}
When enabling GPU inferencing, set the number of GPU layers to offload with: `gpu_layers: 1` to your YAML model config file and `f16: true`. You might also need to set `low_vram: true` if the device has low VRAM.
{{% /notice %}}

### Run LocalAI in Kubernetes

LocalAI can be installed inside Kubernetes with helm.

Requirements:
- SSD storage class, or disable `mmap` to load the whole model in memory

<details>
By default, the helm chart will install LocalAI instance using the ggml-gpt4all-j model without persistent storage.

1. Add the helm repo
    ```bash
    helm repo add go-skynet https://go-skynet.github.io/helm-charts/
    ```
2. Install the helm chart:
    ```bash
    helm repo update
    helm install local-ai go-skynet/local-ai -f values.yaml
    ```
> **Note:** For further configuration options, see the [helm chart repository on GitHub](https://github.com/go-skynet/helm-charts).
### Example values
Deploy a single LocalAI pod with 6GB of persistent storage serving up a `ggml-gpt4all-j` model with custom prompt.
```yaml
### values.yaml

replicaCount: 1

deployment:
  image: quay.io/go-skynet/local-ai:latest ##(This is for CPU only, to use GPU change it to a image that supports GPU IE "v1.40.0-cublas-cuda12")
  env:
    threads: 4
    context_size: 512
  modelsPath: "/models"

resources:
  {}
  # We usually recommend not to specify default resources and to leave this as a conscious
  # choice for the user. This also increases chances charts run on environments with little
  # resources, such as Minikube. If you do want to specify resources, uncomment the following
  # lines, adjust them as necessary, and remove the curly braces after 'resources:'.
  # limits:
  #   cpu: 100m
  #   memory: 128Mi
  # requests:
  #   cpu: 100m
  #   memory: 128Mi

# Prompt templates to include
# Note: the keys of this map will be the names of the prompt template files
promptTemplates:
  {}
  # ggml-gpt4all-j.tmpl: |
  #   The prompt below is a question to answer, a task to complete, or a conversation to respond to; decide which and write an appropriate response.
  #   ### Prompt:
  #   {{.Input}}
  #   ### Response:

# Models to download at runtime
models:
  # Whether to force download models even if they already exist
  forceDownload: false

  # The list of URLs to download models from
  # Note: the name of the file will be the name of the loaded model
  list:
  - url: "https://gpt4all.io/models/ggml-gpt4all-j.bin"
      # basicAuth: base64EncodedCredentials

  # Persistent storage for models and prompt templates.
  # PVC and HostPath are mutually exclusive. If both are enabled,
  # PVC configuration takes precedence. If neither are enabled, ephemeral
  # storage is used.
  persistence:
    pvc:
      enabled: false
      size: 6Gi
      accessModes:
        - ReadWriteOnce

      annotations: {}

      # Optional
      storageClass: ~

    hostPath:
      enabled: false
      path: "/models"

service:
  type: ClusterIP
  port: 80
  annotations: {}
  # If using an AWS load balancer, you'll need to override the default 60s load balancer idle timeout
  # service.beta.kubernetes.io/aws-load-balancer-connection-idle-timeout: "1200"

ingress:
  enabled: false
  className: ""
  annotations:
    {}
    # kubernetes.io/ingress.class: nginx
    # kubernetes.io/tls-acme: "true"
  hosts:
    - host: chart-example.local
      paths:
        - path: /
          pathType: ImplementationSpecific
  tls: []
  #  - secretName: chart-example-tls
  #    hosts:
  #      - chart-example.local

nodeSelector: {}

tolerations: []

affinity: {}
```
</details>


### Build from source

See the [build section]({{%relref "build" %}}).

### Other examples

![Screenshot from 2023-04-26 23-59-55](https://user-images.githubusercontent.com/2420543/234715439-98d12e03-d3ce-4f94-ab54-2b256808e05e.png)

To see other examples on how to integrate with other projects for instance for question answering or for using it with chatbot-ui, see: [examples](https://github.com/go-skynet/LocalAI/tree/master/examples/).


### Clients

OpenAI clients are already compatible with LocalAI by overriding the basePath, or the target URL.

## Javascript

<details>

https://github.com/openai/openai-node/

```javascript
import { Configuration, OpenAIApi } from 'openai';

const configuration = new Configuration({
  basePath: `http://localhost:8080/v1`
});
const openai = new OpenAIApi(configuration);
```

</details>

## Python

<details>

https://github.com/openai/openai-python

Set the `OPENAI_API_BASE` environment variable, or by code:

```python
import openai

openai.api_base = "http://localhost:8080/v1"

# create a chat completion
chat_completion = openai.ChatCompletion.create(model="gpt-3.5-turbo", messages=[{"role": "user", "content": "Hello world"}])

# print the completion
print(completion.choices[0].message.content)
```

</details>

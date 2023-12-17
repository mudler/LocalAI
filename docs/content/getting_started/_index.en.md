
+++
disableToc = false
title = "Getting started"
weight = 1
url = '/basics/getting_started/'
+++

`LocalAI` is available as a container image and binary. It can be used with docker, podman, kubernetes and any container engine. You can check out all the available images with corresponding tags [here](https://quay.io/repository/go-skynet/local-ai?tab=tags&tag=latest).

See also our [How to]({{%relref "howtos" %}}) section for end-to-end guided examples curated by the community.

### How to get started

The easiest way to run LocalAI is by using [`docker compose`](https://docs.docker.com/compose/install/) or with [Docker](https://docs.docker.com/engine/install/) (to build locally, see the [build section]({{%relref "build" %}})).

{{% notice note %}}
To run with GPU Accelleration, see [GPU acceleration]({{%relref "features/gpu-acceleration" %}}).
{{% /notice %}}

{{< tabs >}}
{{% tab name="Docker" %}}

```bash
# Prepare the models into the `model` directory
mkdir models

# copy your models to it
cp your-model.gguf models/

# run the LocalAI container
docker run -p 8080:8080 -v $PWD/models:/models -ti --rm quay.io/go-skynet/local-ai:latest --models-path /models --context-size 700 --threads 4
# You should see:
# 
# ┌───────────────────────────────────────────────────┐
# │                   Fiber v2.42.0                   │
# │               http://127.0.0.1:8080               │
# │       (bound on host 0.0.0.0 and port 8080)       │
# │                                                   │
# │ Handlers ............. 1  Processes ........... 1 │
# │ Prefork ....... Disabled  PID ................. 1 │
# └───────────────────────────────────────────────────┘

# Try the endpoint with curl
curl http://localhost:8080/v1/completions -H "Content-Type: application/json" -d '{
     "model": "your-model.gguf",
     "prompt": "A long time ago in a galaxy far, far away",
     "temperature": 0.7
   }'
```

{{% notice note %}}
- If running on Apple Silicon (ARM) it is **not** suggested to run on Docker due to emulation. Follow the [build instructions]({{%relref "build" %}}) to use Metal acceleration for full GPU support.
- If you are running Apple x86_64 you can use `docker`, there is no additional gain into building it from source.
{{% /notice %}}

{{% /tab %}}
{{% tab name="Docker compose" %}}

```bash
# Clone LocalAI
git clone https://github.com/go-skynet/LocalAI

cd LocalAI

# (optional) Checkout a specific LocalAI tag
# git checkout -b build <TAG>

# copy your models to models/
cp your-model.gguf models/

# (optional) Edit the .env file to set things like context size and threads
# vim .env

# start with docker compose
docker compose up -d --pull always
# or you can build the images with:
# docker compose up -d --build

# Now API is accessible at localhost:8080
curl http://localhost:8080/v1/models
# {"object":"list","data":[{"id":"your-model.gguf","object":"model"}]}

curl http://localhost:8080/v1/completions -H "Content-Type: application/json" -d '{
     "model": "your-model.gguf",
     "prompt": "A long time ago in a galaxy far, far away",
     "temperature": 0.7
   }'
```

Note: If you are on Windows, please make sure the project is on the Linux Filesystem, otherwise loading models might be slow. For more Info: [Microsoft Docs](https://learn.microsoft.com/en-us/windows/wsl/filesystems)

{{% /tab %}}

{{% tab name="Kubernetes" %}}

For installing LocalAI in Kubernetes, you can use the following helm chart:

```bash
# Install the helm repository
helm repo add go-skynet https://go-skynet.github.io/helm-charts/
# Update the repositories
helm repo update
# Get the values
helm show values go-skynet/local-ai > values.yaml

# Edit the values value if needed
# vim values.yaml ...

# Install the helm chart
helm install local-ai go-skynet/local-ai -f values.yaml
```

{{% /tab %}}

{{< /tabs >}}

### Container images

LocalAI has a set of images to support CUDA, ffmpeg and 'vanilla' (CPU-only). The image list is on [quay](https://quay.io/repository/go-skynet/local-ai?tab=tags):

{{< tabs >}}
{{% tab name="Vanilla / CPU Images" %}}
- `master`
- `latest`
- `{{< version >}}`
- `{{< version >}}-ffmpeg`
- `{{< version >}}-ffmpeg-core`

Core Images - Smaller images without predownload python dependencies
{{% /tab %}}

{{% tab name="GPU Images CUDA 11" %}}
- `master-cublas-cuda11`
- `master-cublas-cuda11-core`
- `{{< version >}}-cublas-cuda11`
- `{{< version >}}-cublas-cuda11-core`
- `{{< version >}}-cublas-cuda11-ffmpeg`
- `{{< version >}}-cublas-cuda11-ffmpeg-core`

Core Images - Smaller images without predownload python dependencies
{{% /tab %}}

{{% tab name="GPU Images CUDA 12" %}}
- `master-cublas-cuda12`
- `master-cublas-cuda12-core`
- `{{< version >}}-cublas-cuda12`
- `{{< version >}}-cublas-cuda12-core`
- `{{< version >}}-cublas-cuda12-ffmpeg`
- `{{< version >}}-cublas-cuda12-ffmpeg-core`

Core Images - Smaller images without predownload python dependencies

{{% /tab %}}

{{< /tabs >}}

Example:

- Standard (GPT + `stablediffusion`): `quay.io/go-skynet/local-ai:latest`
- FFmpeg: `quay.io/go-skynet/local-ai:{{< version >}}-ffmpeg`
- CUDA 11+FFmpeg: `quay.io/go-skynet/local-ai:{{< version >}}-cublas-cuda11-ffmpeg`
- CUDA 12+FFmpeg: `quay.io/go-skynet/local-ai:{{< version >}}-cublas-cuda12-ffmpeg`

{{% notice note %}}
Note: the binary inside the image is pre-compiled, and might not suite all CPUs.
To enable CPU optimizations for the execution environment,
the default behavior is to rebuild when starting the container.
To disable this auto-rebuild behavior,
set the environment variable `REBUILD` to `false`.

See [docs on all environment variables]({{%relref "advanced#environment-variables" %}})
for more info.
{{% /notice %}}

### Example: Use luna-ai-llama2 model with `docker`

```bash
mkdir models

# Download luna-ai-llama2 to models/
wget https://huggingface.co/TheBloke/Luna-AI-Llama2-Uncensored-GGUF/resolve/main/luna-ai-llama2-uncensored.Q4_0.gguf -O models/luna-ai-llama2

# Use a template from the examples
cp -rf prompt-templates/getting_started.tmpl models/luna-ai-llama2.tmpl

docker run -p 8080:8080 -v $PWD/models:/models -ti --rm quay.io/go-skynet/local-ai:latest --models-path /models --context-size 700 --threads 4

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

To see other model configurations, see also the example section [here](https://github.com/mudler/LocalAI/tree/master/examples/configurations).


### From binaries

LocalAI binary releases are available in [Github](https://github.com/go-skynet/LocalAI/releases).

You can control LocalAI with command line arguments, to specify a binding address, or the number of threads.

### CLI parameters

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
|--parallel-requests              | $PARALLEL_REQUESTS     |   false |            Enable backends to handle multiple requests in parallel. This is for backends that supports multiple requests in parallel, like llama.cpp or vllm |
| --single-active-backend   | $SINGLE_ACTIVE_BACKEND |  false |    Allow only one backend to be running |
| --api-keys value |   $API_KEY | empty |  List of API Keys to enable API authentication. When this is set, all the requests must be authenticated with one of these API keys.
| --enable-watchdog-idle | $WATCHDOG_IDLE | false | Enable watchdog for stopping idle backends. This will stop the backends if are in idle state for too long. (default: false) [$WATCHDOG_IDLE]
| --enable-watchdog-busy   |     $WATCHDOG_BUSY | false |         Enable watchdog for stopping busy backends that exceed a defined threshold.|
| --watchdog-busy-timeout value | $WATCHDOG_BUSY_TIMEOUT | 5m | Watchdog timeout. This will restart the backend if it crashes.  |
| --watchdog-idle-timeout value | $WATCHDOG_IDLE_TIMEOUT | 15m | Watchdog idle timeout. This will restart the backend if it crashes. |
| --preload-backend-only | $PRELOAD_BACKEND_ONLY | false | If set, the api is NOT launched, and only the preloaded models / backends are started. This is intended for multi-node setups. |
| --external-grpc-backends | EXTERNAL_GRPC_BACKENDS | none | Comma separated list of external gRPC backends to use. Format: `name:host:port` or `name:/path/to/file` |

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
  image: quay.io/go-skynet/local-ai:latest ##(This is for CPU only, to use GPU change it to a image that supports GPU IE "v2.0.0-cublas-cuda12-core")
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

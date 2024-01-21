 
+++
disableToc = false
title = "Run models manually"
weight = 5
icon = "rocket_launch"

+++


1. Ensure you have a model file, a configuration YAML file, or both. Customize model defaults and specific settings with a configuration file. For advanced configurations, refer to the [Advanced Documentation](docs/advanced).

2. For GPU Acceleration instructions, visit [GPU acceleration](docs/features/gpu-acceleration).

{{< tabs tabTotal="5" >}}
{{% tab tabName="Docker" %}}

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

{{% alert icon="💡" %}}

**Other Docker Images**:

For other Docker images, please see the table in
https://localai.io/basics/getting_started/#container-images.

{{% /alert %}}

Here is a more specific example:

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

{{% alert note %}}
- If running on Apple Silicon (ARM) it is **not** suggested to run on Docker due to emulation. Follow the [build instructions]({{%relref "docs/getting-started/build" %}}) to use Metal acceleration for full GPU support.
- If you are running Apple x86_64 you can use `docker`, there is no additional gain into building it from source.
{{% /alert %}}

{{% /tab %}}
{{% tab tabName="Docker compose" %}}

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

{{% alert icon="💡" %}}

**Other Docker Images**:

For other Docker images, please see the table in
https://localai.io/basics/getting_started/#container-images.

{{% /alert %}}

Note: If you are on Windows, please make sure the project is on the Linux Filesystem, otherwise loading models might be slow. For more Info: [Microsoft Docs](https://learn.microsoft.com/en-us/windows/wsl/filesystems)

{{% /tab %}}

{{% tab tabName="Kubernetes" %}}

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
{{% tab tabName="From binary" %}}

LocalAI binary releases are available in [Github](https://github.com/go-skynet/LocalAI/releases).

{{% /tab %}}

{{% tab tabName="From source" %}}

See the [build section]({{%relref "docs/getting-started/build" %}}).
  
{{% /tab %}}

{{< /tabs >}}

For more model configurations, visit the [Examples Section](https://github.com/mudler/LocalAI/tree/master/examples/configurations).

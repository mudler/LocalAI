---

disableToc: false
title: "Run models manually"
weight: 5
icon: "rocket_launch"

---

# Run Models Manually

Follow these steps to manually run models using LocalAI:

1. **Prepare Your Model and Configuration Files**:
   Ensure you have a model file and a configuration YAML file, if necessary. Customize model defaults and specific settings with a configuration file. For advanced configurations, refer to the [Advanced Documentation]({{% relref "docs/advanced" %}}).

2. **GPU Acceleration**:
   For instructions on GPU acceleration, visit the [GPU acceleration]({{% relref "docs/features/gpu-acceleration" %}}) page.

3. **Run LocalAI**:
   Choose one of the following methods to run LocalAI:

{{< tabs tabTotal="5" >}}
{{% tab tabName="Docker" %}}

```bash
# Prepare the models into the `models` directory
mkdir models

# Copy your models to the directory
cp your-model.gguf models/

# Run the LocalAI container
docker run -p 8080:8080 -v $PWD/models:/models -ti --rm quay.io/go-skynet/local-ai:latest --models-path /models --context-size 700 --threads 4

# Expected output:
# ┌───────────────────────────────────────────────────┐
# │                   Fiber v2.42.0                   │
# │               http://127.0.0.1:8080               │
# │       (bound on host 0.0.0.0 and port 8080)       │
# │                                                   │
# │ Handlers ............. 1  Processes ........... 1 │
# │ Prefork ....... Disabled  PID ................. 1 │
# └───────────────────────────────────────────────────┘

# Test the endpoint with curl
curl http://localhost:8080/v1/completions -H "Content-Type: application/json" -d '{
     "model": "your-model.gguf",
     "prompt": "A long time ago in a galaxy far, far away",
     "temperature": 0.7
   }'
```

{{% alert icon="💡" %}}
**Other Docker Images**:

For other Docker images, please refer to the table in [the container images section]({{% relref "docs/getting-started/container-images" %}}).
{{% /alert %}}

### Example:

```bash
mkdir models

# Download luna-ai-llama2 to models/
wget https://huggingface.co/TheBloke/Luna-AI-Llama2-Uncensored-GGUF/resolve/main/luna-ai-llama2-uncensored.Q4_0.gguf -O models/luna-ai-llama2

# Use a template from the examples, if needed
cp -rf prompt-templates/getting_started.tmpl models/luna-ai-llama2.tmpl

docker run -p 8080:8080 -v $PWD/models:/models -ti --rm quay.io/go-skynet/local-ai:latest --models-path /models --context-size 700 --threads 4

# Now the API is accessible at localhost:8080
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
- If running on Apple Silicon (ARM), it is **not** recommended to run on Docker due to emulation. Follow the [build instructions]({{% relref "docs/getting-started/build" %}}) to use Metal acceleration for full GPU support.
- If you are running on Apple x86_64, you can use Docker without additional gain from building it from source.
{{% /alert %}}

{{% /tab %}}
{{% tab tabName="Docker Compose" %}}

```bash
# Clone LocalAI
git clone https://github.com/go-skynet/LocalAI

cd LocalAI

# (Optional) Checkout a specific LocalAI tag
# git checkout -b build <TAG>

# Copy your models to the models directory
cp your-model.gguf models/

# (Optional) Edit the .env file to set parameters like context size and threads
# vim .env

# Start with Docker Compose
docker compose up -d --pull always
# Or build the images with:
# docker compose up -d --build

# Now the API is accessible at localhost:8080
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

For other Docker images, please refer to the table in [Getting Started](https://localai.io/basics/getting_started/#container-images).
{{% /alert %}}

Note: If you are on Windows, ensure the project is on the Linux filesystem to avoid slow model loading. For more information, see the [Microsoft Docs](https://learn.microsoft.com/en-us/windows/wsl/filesystems).

{{% /tab %}}
{{% tab tabName="Kubernetes" %}}

For Kubernetes deployment, see the [Kubernetes section]({{% relref "docs/getting-started/kubernetes" %}}).

{{% /tab %}}
{{% tab tabName="From Binary" %}}

LocalAI binary releases are available on [GitHub](https://github.com/go-skynet/LocalAI/releases).

{{% alert icon="⚠️" %}}
If installing on macOS, you might encounter a message saying:

> "local-ai-git-Darwin-arm64" (or the name you gave the binary) can't be opened because Apple cannot check it for malicious software.

Hit OK, then go to Settings > Privacy & Security > Security and look for the message:

> "local-ai-git-Darwin-arm64" was blocked from use because it is not from an identified developer.

Press "Allow Anyway."
{{% /alert %}}

{{% /tab %}}
{{% tab tabName="From Source" %}}

For instructions on building LocalAI from source, see the [Build Section]({{% relref "docs/getting-started/build" %}}).

{{% /tab %}}
{{< /tabs >}}

For more model configurations, visit the [Examples Section](https://github.com/mudler/LocalAI/tree/master/examples/configurations).

---

+++
disableToc = false
title = "FAQ"
weight = 24
icon = "quiz"
url = "/faq/"
+++

## Frequently asked questions

Here are answers to some of the most common questions.


### How do I get models? 

There are several ways to get models for LocalAI:

1. **WebUI Import** (Easiest): Use the WebUI's model import interface:
   - Open `http://localhost:8080` and navigate to the Models tab
   - Click "Import Model" or "New Model"
   - Enter a model URI (Hugging Face, OCI, file path, etc.)
   - Configure preferences in Simple Mode or edit YAML in Advanced Mode
   - The WebUI provides syntax highlighting, validation, and a user-friendly interface

2. **Model Gallery** (Recommended): Use the built-in model gallery accessible via:
   - WebUI: Navigate to the Models tab in the LocalAI interface and browse available models
   - CLI: `local-ai models list` to see available models, then `local-ai models install <model-name>`
   - Online: Browse models at [models.localai.io](https://models.localai.io)

3. **Hugging Face**: Most GGUF-based models from Hugging Face work with LocalAI. You can install them via:
   - WebUI: Import using `huggingface://TheBloke/phi-2-GGUF/phi-2.Q8_0.gguf`
   - CLI: `local-ai run huggingface://TheBloke/phi-2-GGUF/phi-2.Q8_0.gguf`

4. **Manual Installation**: Download model files and place them in your models directory. See [Install and Run Models]({{% relref "docs/getting-started/models" %}}) for details.

5. **OCI Registries**: Install models from OCI-compatible registries:
   - WebUI: Import using `ollama://gemma:2b` or `oci://localai/phi-2:latest`
   - CLI: `local-ai run ollama://gemma:2b` or `local-ai run oci://localai/phi-2:latest`

**Security Note**: Be cautious when downloading models from the internet. Always verify the source and use trusted repositories when possible.

### Where are models stored?

LocalAI stores downloaded models in the following locations by default:

- **Command line**: `./models` (relative to current working directory)
- **Docker**: `/models` (inside the container, typically mounted to `./models` on host)
- **Launcher application**: `~/.localai/models` (in your home directory)

You can customize the model storage location using the `LOCALAI_MODELS_PATH` environment variable or `--models-path` command line flag. This is useful if you want to store models outside your home directory for backup purposes or to avoid filling up your home directory with large model files.

### How much storage space do models require?

Model sizes vary significantly depending on the model and quantization level:

- **Small models (1-3B parameters)**: 1-3 GB
- **Medium models (7-13B parameters)**: 4-8 GB  
- **Large models (30B+ parameters)**: 15-30+ GB

**Quantization levels** (smaller files, slightly reduced quality):
- `Q4_K_M`: ~75% of original size
- `Q4_K_S`: ~60% of original size
- `Q2_K`: ~50% of original size

**Storage recommendations**:
- Ensure you have at least 2-3x the model size available for downloads and temporary files
- Use SSD storage for better performance
- Consider the model size relative to your system RAM - models larger than your RAM may not run efficiently

### Benchmarking LocalAI and llama.cpp shows different results!

LocalAI applies a set of defaults when loading models with the llama.cpp backend, one of these is mirostat sampling - while it achieves better results, it slows down the inference. You can disable this by setting `mirostat: 0` in the model config file. See also the advanced section ({{%relref "docs/advanced/advanced-usage" %}}) for more information and [this issue](https://github.com/mudler/LocalAI/issues/2780).

### What's the difference with Serge, or XXX?

LocalAI is a multi-model solution that doesn't focus on a specific model type (e.g., llama.cpp or alpaca.cpp), and it handles all of these internally for faster inference,  easy to set up locally and deploy to Kubernetes.

### Everything is slow, how is it possible?

There are few situation why this could occur. Some tips are:
- Don't use HDD to store your models. Prefer SSD over HDD. In case you are stuck with HDD, disable `mmap` in the model config file so it loads everything in memory.
- Watch out CPU overbooking. Ideally the `--threads` should match the number of physical cores. For instance if your CPU has 4 cores, you would ideally allocate `<= 4` threads to a model.
- Run LocalAI with `DEBUG=true`. This gives more information, including stats on the token inference speed.
- Check that you are actually getting an output: run a simple curl request with `"stream": true` to see how fast the model is responding. 

### Can I use it with a Discord bot, or XXX?

Yes! If the client uses OpenAI and supports setting a different base URL to send requests to, you can use the LocalAI endpoint. This allows to use this with every application that was supposed to work with OpenAI, but without changing the application!

### Can this leverage GPUs? 

There is GPU support, see {{%relref "docs/features/GPU-acceleration" %}}.

### Where is the webUI? 

LocalAI includes a built-in WebUI that is automatically available when you start LocalAI. Simply navigate to `http://localhost:8080` in your web browser after starting LocalAI.

The WebUI provides:
- Chat interface for interacting with models
- Model gallery browser and installer
- Backend management
- Configuration tools

If you prefer a different interface, LocalAI is compatible with any OpenAI-compatible UI. You can find examples in the [LocalAI-examples repository](https://github.com/mudler/LocalAI-examples), including integrations with popular UIs like chatbot-ui.

### Does it work with AutoGPT? 

Yes, see the [examples](https://github.com/mudler/LocalAI-examples)!

### How can I troubleshoot when something is wrong?

Enable the debug mode by setting `DEBUG=true` in the environment variables. This will give you more information on what's going on.
You can also specify `--debug` in the command line.

### I'm getting 'invalid pitch' error when running with CUDA, what's wrong?

This typically happens when your prompt exceeds the context size. Try to reduce the prompt size, or increase the context size.

### I'm getting a 'SIGILL' error, what's wrong?

Your CPU probably does not have support for certain instructions that are compiled by default in the pre-built binaries. If you are running in a container, try setting `REBUILD=true` and disable the CPU instructions that are not compatible with your CPU. For instance: `CMAKE_ARGS="-DGGML_F16C=OFF -DGGML_AVX512=OFF -DGGML_AVX2=OFF -DGGML_FMA=OFF" make build`

Alternatively, you can use the backend management system to install a compatible backend for your CPU architecture. See [Backend Management]({{% relref "docs/features/backends" %}}) for more information.

### How do I install backends?

LocalAI now uses a backend management system where backends are automatically downloaded when needed. You can also manually install backends:

```bash
# List available backends
local-ai backends list

# Install a specific backend
local-ai backends install llama-cpp

# Install a backend for a specific GPU type
local-ai backends install llama-cpp --gpu-type nvidia
```

For more details, see the [Backends documentation]({{% relref "docs/features/backends" %}}).

### How do I set up API keys for security?

You can secure your LocalAI instance by setting API keys using the `API_KEY` environment variable:

```bash
# Single API key
API_KEY=your-secret-key local-ai

# Multiple API keys (comma-separated)
API_KEY=key1,key2,key3 local-ai
```

When API keys are set, all requests must include the key in the `Authorization` header:
```bash
curl http://localhost:8080/v1/models \
  -H "Authorization: Bearer your-secret-key"
```

**Important**: API keys provide full access to all LocalAI features (admin-level access). Make sure to protect your API keys and use HTTPS when exposing LocalAI remotely.

### My model is not loading or showing errors

Here are common issues and solutions:

1. **Backend not installed**: The required backend may not be installed. Check with `local-ai backends list` and install if needed.
2. **Insufficient memory**: Large models require significant RAM. Check available memory and consider using a smaller quantized model.
3. **Wrong backend specified**: Ensure the backend in your model configuration matches the model type. See the [Compatibility Table]({{% relref "docs/reference/compatibility-table" %}}).
4. **Model file corruption**: Re-download the model file.
5. **Check logs**: Enable debug mode (`DEBUG=true`) to see detailed error messages.

For more troubleshooting help, see the [Troubleshooting Guide]({{% relref "docs/troubleshooting" %}}).

### How do I use GPU acceleration?

LocalAI supports multiple GPU types:

- **NVIDIA (CUDA)**: Use `--gpus all` with Docker and CUDA-enabled images
- **AMD (ROCm)**: Use images with `hipblas` tag
- **Intel**: Use images with `intel` tag or Intel oneAPI
- **Apple Silicon (Metal)**: Automatically detected on macOS

For detailed setup instructions, see [GPU Acceleration]({{% relref "docs/features/gpu-acceleration" %}}).

### Can I use LocalAI with LangChain, AutoGPT, or other frameworks?

Yes! LocalAI is compatible with any framework that supports OpenAI's API. Simply point the framework to your LocalAI endpoint:

```python
# Example with LangChain
from langchain.llms import OpenAI

llm = OpenAI(
    openai_api_key="not-needed",
    openai_api_base="http://localhost:8080/v1"
)
```

See the [Integrations]({{% relref "docs/integrations" %}}) page for a list of compatible projects and examples.

### What's the difference between AIO images and standard images?

**AIO (All-in-One) images** come pre-configured with:
- Pre-installed models ready to use
- All necessary backends included
- Quick start with no configuration needed

**Standard images** are:
- Smaller in size
- No pre-installed models
- You install models and backends as needed
- More flexible for custom setups

Choose AIO images for quick testing and standard images for production deployments. See [Container Images]({{% relref "docs/getting-started/container-images" %}}) for details.

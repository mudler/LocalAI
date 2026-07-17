
+++
disableToc = false
title = "FAQ"
weight = 24
icon = "quiz"
url = "/faq/"
+++

## Frequently asked questions

Here are answers to some of the most common questions.


### Do I need to install all the backends?

No. You install only the backends your models use. LocalAI's core is a single binary (or container) that provides the OpenAI-compatible API, request routing, the web UI, and agents. Each inference backend (llama.cpp, vLLM, whisper.cpp, stable-diffusion, MLX, and others) is a separate artifact, installed only when a model needs it.

In practice:

- **You install one backend, not all of them.** Run a model with `local-ai run <model>` and the matching backend is pulled automatically; nothing else is downloaded.
- **Each backend is purpose-built for its engine.** LocalAI builds a dedicated gRPC backend around each engine, so every one stays independently optimized without a single binary trying to support every model architecture at once.
- **You manage backends individually** with `local-ai backends list/install/uninstall` or from the web UI.

The catalog's breadth is optionality: you only ever run what your models use.

### Can I bring my own model or backend?

Yes. You can load any compatible model, not just the ones in the gallery. And because every backend talks to the core over a simple gRPC interface, you can write your own backend in any language and plug it in, exactly how the built-in backends work. Nothing about the core is closed off, which gives you the flexibility to run precisely the stack you want.

### How do I get models? 

The easiest way is the built-in model gallery: open the web interface at `http://localhost:8080`, go to the Models page, and install a model by name. You can also install from the command line with `local-ai models install <model-name>`. Most gguf-based models work, and you can point LocalAI at any compatible GGUF from Hugging Face (https://huggingface.co/models?search=gguf). Be cautious about downloading models from untrusted sources, as there may be security vulnerabilities in llama.cpp or ggml that could be maliciously exploited.

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

LocalAI applies a set of defaults when loading models with the llama.cpp backend, one of these is mirostat sampling - while it achieves better results, it slows down the inference. You can disable this by setting `mirostat: 0` in the model config file. See also the advanced section ({{%relref "advanced/advanced-usage" %}}) for more information and [this issue](https://github.com/mudler/LocalAI/issues/2780).

### Everything is slow, how is it possible?

See the performance section of the runtime errors reference: {{% relref "reference/runtime-errors" %}}.

### Can I use it with a Discord bot, or XXX?

Yes! If the client uses OpenAI and supports setting a different base URL to send requests to, you can use the LocalAI endpoint. This allows to use this with every application that was supposed to work with OpenAI, but without changing the application!

### Can this leverage GPUs? 

There is GPU support, see {{%relref "features/GPU-acceleration" %}}.

### Where is the WebUI?

LocalAI ships with a built-in web interface. Once LocalAI is running, open `http://localhost:8080` in your browser to manage models, chat, and configure the server. There is nothing extra to install. Because LocalAI also exposes an OpenAI-compatible API, you can additionally plug it into any existing application that talks to the OpenAI API by pointing that application at the LocalAI endpoint.

### How can I troubleshoot when something is wrong?

Enable the debug mode by setting `DEBUG=true` in the environment variables. This will give you more information on what's going on.
You can also specify `--debug` in the command line. For matching a specific runtime or backend error message to its cause and fix, see the runtime errors reference: {{% relref "reference/runtime-errors" %}}.

### I'm getting 'invalid pitch', 'SIGILL', a CUDA out-of-memory error, or a model that will not load

These runtime and backend failures, and how to read the real error behind an HTTP `500`, are documented in the runtime errors reference: {{% relref "reference/runtime-errors" %}}.


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

Most gguf-based models should work, but newer models may require additions to the API. If a model doesn't work, please feel free to open up issues. However, be cautious about downloading models from the internet and directly onto your machine, as there may be security vulnerabilities in lama.cpp or ggml that could be maliciously exploited. Some models can be found on Hugging Face: https://huggingface.co/models?search=gguf, or models from gpt4all are compatible too: https://github.com/nomic-ai/gpt4all.

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

There is the availability of localai-webui and chatbot-ui in the examples section and can be setup as per the instructions. However as LocalAI is an API you can already plug it into existing projects that provides are UI interfaces to OpenAI's APIs. There are several already on Github, and should be compatible with LocalAI already (as it mimics the OpenAI API)

### Does it work with AutoGPT? 

Yes, see the [examples](https://github.com/go-skynet/LocalAI/tree/master/examples/)!

### How can I troubleshoot when something is wrong?

Enable the debug mode by setting `DEBUG=true` in the environment variables. This will give you more information on what's going on.
You can also specify `--debug` in the command line.

### I'm getting 'invalid pitch' error when running with CUDA, what's wrong?

This typically happens when your prompt exceeds the context size. Try to reduce the prompt size, or increase the context size.

### I'm getting a 'SIGILL' error, what's wrong?

Your CPU probably does not have support for certain instructions that are compiled by default in the pre-built binaries. If you are running in a container, try setting `REBUILD=true` and disable the CPU instructions that are not compatible with your CPU. For instance: `CMAKE_ARGS="-DLLAMA_F16C=OFF -DLLAMA_AVX512=OFF -DLLAMA_AVX2=OFF -DLLAMA_FMA=OFF" make build`
<h1 align="center">
  <br>
  <img height="300" src="https://user-images.githubusercontent.com/2420543/233147843-88697415-6dbf-4368-a862-ab217f9f7342.jpeg"> <br>
    LocalAI
<br>
</h1>

[![tests](https://github.com/go-skynet/LocalAI/actions/workflows/test.yml/badge.svg)](https://github.com/go-skynet/LocalAI/actions/workflows/test.yml) [![build container images](https://github.com/go-skynet/LocalAI/actions/workflows/image.yml/badge.svg)](https://github.com/go-skynet/LocalAI/actions/workflows/image.yml)

[![](https://dcbadge.vercel.app/api/server/uJAeKSAGDy?style=flat-square&theme=default-inverted)](https://discord.gg/uJAeKSAGDy) 

**LocalAI** is a drop-in replacement REST API that’s compatible with OpenAI API specifications for local inferencing. It allows you to run LLMs (and not only) locally or on-prem with consumer grade hardware, supporting multiple model families that are compatible with the ggml format. Does not require GPU.

For a list of the supported model families, please see [the model compatibility table](https://localai.io/model-compatibility/index.html#model-compatibility-table).

In a nutshell:

- Local, OpenAI drop-in alternative REST API. You own your data.
- NO GPU required. NO Internet access is required either. Optional, GPU Acceleration is available in `llama.cpp`-compatible LLMs. [See building instructions](https://localai.io/basics/build/index.html).
- Supports multiple models, Audio transcription, Text generation with GPTs, Image generation with stable diffusion (experimental)
- Once loaded the first time, it keep models loaded in memory for faster inference
- Doesn't shell-out, but uses C++ bindings for a faster inference and better performance. 

LocalAI was created by [Ettore Di Giacinto](https://github.com/mudler/) and is a community-driven project, focused on making the AI accessible to anyone. Any contribution, feedback and PR is welcome!

| [ChatGPT OSS alternative](https://github.com/go-skynet/LocalAI/tree/master/examples/chatbot-ui)                                                                                                                | [Image generation](https://localai.io/api-endpoints/index.html#image-generation)                                                                                                              |
|------------------------------------------------------------------------------------------------------------------------|------------------------------------------------------------------------------------------------------------------------|
|  ![Screenshot from 2023-04-26 23-59-55](https://user-images.githubusercontent.com/2420543/234715439-98d12e03-d3ce-4f94-ab54-2b256808e05e.png)            | ![b6441997879](https://github.com/go-skynet/LocalAI/assets/2420543/d50af51c-51b7-4f39-b6c2-bf04c403894c)                  |


See the [Getting started](https://localai.io/basics/getting_started/index.html) and [examples](https://github.com/go-skynet/LocalAI/tree/master/examples/) sections to learn how to use LocalAI. For a list of curated models check out the [model gallery](https://localai.io/models/).

## News

- 🔥🔥🔥 06-06-2023: **v1.18.0**: Many updates, new features, and much more 🚀, check out the [Changelog](https://localai.io/basics/news/index.html#-06-06-2023-__v1180__-)!
- 29-05-2023: LocalAI now has a website, [https://localai.io](https://localai.io)! check the news in the [dedicated section](https://localai.io/basics/news/index.html)!

For latest news, follow also on Twitter [@LocalAI_API](https://twitter.com/LocalAI_API) and [@mudler_it](https://twitter.com/mudler_it)

## Contribute and help

To help the project you can:

- Upvote the [Reddit post](https://www.reddit.com/r/selfhosted/comments/12w4p2f/localai_openai_compatible_api_to_run_llm_models/) about LocalAI.

- [Hacker news post](https://news.ycombinator.com/item?id=35726934) - help us out by voting if you like this project.

- If you have technological skills and want to contribute to development, have a look at the open issues. If you are new you can have a look at the [good-first-issue](https://github.com/go-skynet/LocalAI/issues?q=is%3Aissue+is%3Aopen+label%3A%22good+first+issue%22) and [help-wanted](https://github.com/go-skynet/LocalAI/issues?q=is%3Aissue+is%3Aopen+label%3A%22help+wanted%22) labels.

- If you don't have technological skills you can still help improving documentation or add examples or share your user-stories with our community, any help and contribution is welcome!

## Usage

Check out the [Getting started](https://localai.io/basics/getting_started/index.html) section. Here below you will find generic, quick instructions to get ready and use LocalAI.

The easiest way to run LocalAI is by using `docker-compose` (to build locally, see [building LocalAI](https://localai.io/basics/build/index.html)):

```bash

git clone https://github.com/go-skynet/LocalAI

cd LocalAI

# (optional) Checkout a specific LocalAI tag
# git checkout -b build <TAG>

# copy your models to models/
cp your-model.bin models/

# (optional) Edit the .env file to set things like context size and threads
# vim .env

# start with docker-compose
docker-compose up -d --pull always
# or you can build the images with:
# docker-compose up -d --build

# Now API is accessible at localhost:8080
curl http://localhost:8080/v1/models
# {"object":"list","data":[{"id":"your-model.bin","object":"model"}]}

curl http://localhost:8080/v1/completions -H "Content-Type: application/json" -d '{
     "model": "your-model.bin",            
     "prompt": "A long time ago in a galaxy far, far away",
     "temperature": 0.7
   }'
```

### Example: Use GPT4ALL-J model

<details>

```bash
# Clone LocalAI
git clone https://github.com/go-skynet/LocalAI

cd LocalAI

# (optional) Checkout a specific LocalAI tag
# git checkout -b build <TAG>

# Download gpt4all-j to models/
wget https://gpt4all.io/models/ggml-gpt4all-j.bin -O models/ggml-gpt4all-j

# Use a template from the examples
cp -rf prompt-templates/ggml-gpt4all-j.tmpl models/

# (optional) Edit the .env file to set things like context size and threads
# vim .env

# start with docker-compose
docker-compose up -d --pull always
# or you can build the images with:
# docker-compose up -d --build
# Now API is accessible at localhost:8080
curl http://localhost:8080/v1/models
# {"object":"list","data":[{"id":"ggml-gpt4all-j","object":"model"}]}

curl http://localhost:8080/v1/chat/completions -H "Content-Type: application/json" -d '{
     "model": "ggml-gpt4all-j",
     "messages": [{"role": "user", "content": "How are you?"}],
     "temperature": 0.9 
   }'

# {"model":"ggml-gpt4all-j","choices":[{"message":{"role":"assistant","content":"I'm doing well, thanks. How about you?"}}]}
```
</details>


### Build locally

<details>

In order to build the `LocalAI` container image locally you can use `docker`:

```
# build the image
docker build -t localai .
docker run localai
```

Or you can build the binary with `make`:

```
make build
```

</details>

See the [build section](https://localai.io/basics/build/index.html) in our documentation for detailed instructions.

### Run LocalAI in Kubernetes

LocalAI can be installed inside Kubernetes with helm. See [installation instructions](https://localai.io/basics/getting_started/index.html#run-localai-in-kubernetes).

## Supported API endpoints

See the [list of the supported API endpoints](https://localai.io/api-endpoints/index.html) and how to configure image generation and audio transcription.

## Frequently asked questions

See [the FAQ](https://localai.io/faq/index.html) section for a list of common questions.

## Projects already using LocalAI to run local models

Feel free to open up a PR to get your project listed!

- [Kairos](https://github.com/kairos-io/kairos)
- [k8sgpt](https://github.com/k8sgpt-ai/k8sgpt#running-local-models)
- [Spark](https://github.com/cedriking/spark)
- [autogpt4all](https://github.com/aorumbayev/autogpt4all)
- [Mods](https://github.com/charmbracelet/mods)
- [Flowise](https://github.com/FlowiseAI/Flowise)

## Short-term roadmap

- [x] Mimic OpenAI API (https://github.com/go-skynet/LocalAI/issues/10)
- [ ] Binary releases (https://github.com/go-skynet/LocalAI/issues/6)
- [ ] Upstream our golang bindings to llama.cpp (https://github.com/ggerganov/llama.cpp/issues/351) and [gpt4all](https://github.com/go-skynet/LocalAI/issues/85)
- [x] Multi-model support
- [x] Have a webUI!
- [x] Allow configuration of defaults for models.
- [x] Support for embeddings
- [x] Support for audio transcription with https://github.com/ggerganov/whisper.cpp
- [ ] GPU/CUDA support ( https://github.com/go-skynet/LocalAI/issues/69 )
- [ ] Enable automatic downloading of models from a curated gallery, with only free-licensed models, directly from the webui.

## Star history

[![LocalAI Star history Chart](https://api.star-history.com/svg?repos=go-skynet/LocalAI&type=Date)](https://star-history.com/#go-skynet/LocalAI&Date)

## License

LocalAI is a community-driven project created by [Ettore Di Giacinto](https://github.com/mudler/).

MIT

## Author

Ettore Di Giacinto and others

## Acknowledgements

LocalAI couldn't have been built without the help of great software already available from the community. Thank you!

- [llama.cpp](https://github.com/ggerganov/llama.cpp)
- https://github.com/tatsu-lab/stanford_alpaca
- https://github.com/cornelk/llama-go for the initial ideas
- https://github.com/antimatter15/alpaca.cpp
- https://github.com/EdVince/Stable-Diffusion-NCNN
- https://github.com/ggerganov/whisper.cpp
- https://github.com/saharNooby/rwkv.cpp

## Contributors

<a href="https://github.com/go-skynet/LocalAI/graphs/contributors">
  <img src="https://contrib.rocks/image?repo=go-skynet/LocalAI" />
</a>

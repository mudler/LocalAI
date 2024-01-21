+++
disableToc = false
title = "News"
weight = 7
url = '/basics/news/'
icon = "newspaper"
+++

Release notes have been now moved completely over Github releases. 

You can see the release notes [here](https://github.com/mudler/LocalAI/releases).

# Older release notes

## 04-12-2023: __v2.0.0__

This release brings a major overhaul in some backends. 

Breaking/important changes:
- Backend rename: `llama-stable` renamed to `llama-ggml` {{< pr "1287" >}}
- Prompt template changes: {{< pr "1254" >}} (extra space in roles)
- Apple metal bugfixes: {{< pr "1365" >}}

New:
- Added support for LLaVa and OpenAI Vision API support ({{< pr "1254" >}})
- Python based backends are now using conda to track env dependencies ( {{< pr "1144" >}} )
- Support for parallel requests ( {{< pr "1290"  >}} )
- Support for transformers-embeddings ( {{< pr "1308"  >}})
- Watchdog for backends ( {{< pr "1341"  >}}). As https://github.com/ggerganov/llama.cpp/issues/3969 is hitting LocalAI's llama-cpp implementation, we have now a watchdog that can be used to make sure backends are not stalling. This is a generic mechanism that can be enabled for all the backends now.
- Whisper.cpp updates ( {{< pr "1302" >}} )
- Petals backend ( {{< pr "1350" >}} )
- Full LLM fine-tuning example to use with LocalAI: https://localai.io/advanced/fine-tuning/

Due to the python dependencies size of images grew in size. 
If you still want to use smaller images without python dependencies, you can use the corresponding images tags ending with `-core`.

Full changelog: https://github.com/mudler/LocalAI/releases/tag/v2.0.0

## 30-10-2023: __v1.40.0__

This release is a preparation before v2 - the efforts now will be to refactor, polish and add new backends. Follow up on: https://github.com/mudler/LocalAI/issues/1126

## Hot topics

This release now brings the `llama-cpp` backend which is a c++ backend tied to llama.cpp. It follows more closely and tracks recent versions of llama.cpp. It is not feature compatible with the current `llama` backend but plans are to sunset the current `llama` backend in favor of this one. This one will be probably be the latest release containing the older `llama` backend written in go and c++. The major improvement with this change is that there are less layers that could be expose to potential bugs - and as well it ease out maintenance as well.

### Support for  ROCm/HIPBLAS 

This release bring support for AMD thanks to @65a .  See more details in {{< pr "1100" >}}

### More CLI commands

Thanks to @jespino now the local-ai binary has more subcommands allowing to manage the gallery or try out directly inferencing, check it out!

[Release notes](https://github.com/mudler/LocalAI/releases/tag/v1.40.0)

## 25-09-2023: __v1.30.0__

This is an exciting LocalAI release! Besides bug-fixes and enhancements this release brings the new backend to a whole new level by extending support to vllm and vall-e-x for audio generation!

Check out the documentation for vllm [here](https://localai.io/model-compatibility/vllm/) and Vall-E-X [here](https://localai.io/model-compatibility/vall-e-x/)

[Release notes](https://github.com/mudler/LocalAI/releases/tag/v1.30.0)

## 26-08-2023: __v1.25.0__

Hey everyone, [Ettore](https://github.com/mudler/) here, I'm so happy to share this release out - while this summer is hot apparently doesn't stop LocalAI development  :)

This release brings a lot of new features, bugfixes and updates! Also a big shout out to the community, this was a great release!

### Attention 🚨

From this release the `llama` backend supports only `gguf` files (see {{< pr "943" >}}). LocalAI  however still supports `ggml` files. We ship a version of llama.cpp before that change in a separate backend, named `llama-stable` to allow still loading `ggml` files. If you were specifying the `llama` backend manually to load `ggml` files from this release you should use `llama-stable` instead, or do not specify a backend at all (LocalAI will automatically handle this).

### Image generation enhancements

The [Diffusers]({{%relref "docs/features/image-generation" %}}) backend got now various enhancements, including support to generate images from images, longer prompts, and support for more kernels schedulers. See the [Diffusers]({{%relref "docs/features/image-generation" %}}) documentation for more information.

### Lora adapters

Now it's possible to load lora adapters for llama.cpp. See {{< pr "955" >}} for more information.

### Device management

It is now possible for single-devices with one GPU to specify `--single-active-backend` to allow only one backend active at the time {{< pr "925" >}}.

### Community spotlight



#### Resources management

Thanks to the continous community efforts (another cool contribution from {{< github "dave-gray101" >}} ) now it's possible to shutdown a backend programmatically via the API.
There is an ongoing effort in the community to better handling of resources. See also the [🔥Roadmap](https://localai.io/#-hot-topics--roadmap).

#### New how-to section

Thanks to the community efforts now we have a new [how-to website](https://io.midori-ai.xyz/howtos/) with various examples on how to use LocalAI. This is a great starting point for new users! We are currently working on improving it, a huge shout out to {{< github "lunamidori5" >}} from the community for the impressive efforts on this!

#### 💡 More examples!

- Open source autopilot? See the new addition by {{< github "gruberdev" >}} in our [examples](https://github.com/go-skynet/LocalAI/tree/master/examples/continue) on how to use Continue with LocalAI!
- Want to try LocalAI with Insomnia? Check out the new [Insomnia example](https://github.com/go-skynet/LocalAI/tree/master/examples/insomnia) by {{< github "dave-gray101" >}}!

#### LocalAGI in discord!

Did you know that we have now few cool bots in our Discord? come check them out! We also have an instance of [LocalAGI](https://github.com/mudler/LocalAGI) ready to help you out!



### Changelog summary

#### Breaking Changes 🛠
* feat: bump llama.cpp, add gguf support by {{< github "mudler" >}} in {{< pr "943" >}}

#### Exciting New Features 🎉

* feat(Makefile): allow to restrict backend builds by {{< github "mudler" >}} in {{< pr "890" >}}
* feat(diffusers): various enhancements by {{< github "mudler" >}} in {{< pr "895" >}}
* feat: make initializer accept gRPC delay times by {{< github "mudler" >}} in {{< pr "900" >}}
* feat(diffusers): add DPMSolverMultistepScheduler++, DPMSolverMultistepSchedulerSDE++, guidance_scale by {{< github "mudler" >}} in {{< pr "903" >}}
* feat(diffusers): overcome prompt limit by {{< github "mudler" >}} in {{< pr "904" >}}
* feat(diffusers): add img2img and clip_skip, support more kernels schedulers by {{< github "mudler" >}} in {{< pr "906" >}}
* Usage Features by {{< github "dave-gray101" >}}  in {{< pr "863" >}}
* feat(diffusers): be consistent with pipelines, support also depthimg2img by {{< github "mudler" >}} in {{< pr "926" >}}
* feat: add --single-active-backend to allow only one backend active at the time by {{< github "mudler" >}} in {{< pr "925" >}}
* feat: add llama-stable backend by {{< github "mudler" >}} in {{< pr "932" >}}
* feat: allow to customize rwkv tokenizer by {{< github "dave-gray101" >}}  in {{< pr "937" >}}
* feat: backend monitor shutdown endpoint, process based by {{< github "dave-gray101" >}}  in {{< pr "938" >}}
* feat: Allow to load lora adapters for llama.cpp by {{< github "mudler" >}} in {{< pr "955" >}}

Join our Discord community! our vibrant community is growing fast, and we are always happy to help!  https://discord.gg/uJAeKSAGDy

The full changelog is available [here](https://github.com/go-skynet/LocalAI/releases/tag/v.1.25.0).

--- 

## 🔥🔥🔥🔥 12-08-2023: __v1.24.0__ 🔥🔥🔥🔥

This is release brings four(!) new additional backends to LocalAI: [🐶 Bark]({{%relref "docs/features/text-to-audio#bark" %}}), 🦙 [AutoGPTQ]({{%relref "docs/features/text-generation#autogptq" %}}), [🧨 Diffusers]({{%relref "docs/features/image-generation" %}}), 🦙 [exllama]({{%relref "docs/features/text-generation#exllama" %}}) and a lot of improvements!

### Major improvements:

* feat: add bark and AutoGPTQ by {{< github "mudler" >}} in {{< pr "871" >}}
* feat: Add Diffusers by {{< github "mudler" >}} in {{< pr "874" >}}
* feat: add API_KEY list support by {{< github "neboman11" >}} and {{< github "bnusunny" >}} in {{< pr "877" >}}
* feat: Add exllama by {{< github "mudler" >}} in {{< pr "881" >}}
* feat: pre-configure LocalAI galleries by {{< github "mudler" >}} in {{< pr "886" >}}

### 🐶 Bark

[Bark]({{%relref "docs/features/text-to-audio#bark" %}}) is a text-prompted generative audio model - it combines GPT techniques to generate Audio from text. It is a great addition to LocalAI, and it's available in the container images by default.

It can also generate music, see the example: [lion.webm](https://user-images.githubusercontent.com/5068315/230684766-97f5ea23-ad99-473c-924b-66b6fab24289.webm)

### 🦙 AutoGPTQ

[AutoGPTQ]({{%relref "docs/features/text-generation#autogptq" %}}) is an easy-to-use LLMs quantization package with user-friendly apis, based on GPTQ algorithm.

It is targeted mainly for GPU usage only. Check out the [ documentation]({{%relref "docs/features/text-generation" %}}) for usage.

### 🦙 Exllama

[Exllama]({{%relref "docs/features/text-generation#exllama" %}}) is a "A more memory-efficient rewrite of the HF transformers implementation of Llama for use with quantized weights". It is a faster alternative to run LLaMA models on GPU.Check out the [Exllama documentation]({{%relref "docs/features/text-generation#exllama" %}}) for usage.

### 🧨 Diffusers

[Diffusers]({{%relref "docs/features/image-generation#diffusers" %}}) is the go-to library for state-of-the-art pretrained diffusion models for generating images, audio, and even 3D structures of molecules. Currently it is experimental, and supports generation only of images so you might encounter some issues on models which weren't tested yet. Check out the [Diffusers documentation]({{%relref "docs/features/image-generation" %}}) for usage.

### 🔑 API Keys

Thanks to the community contributions now it's possible to specify a list of API keys that can be used to gate API requests.

API Keys can be specified with the `API_KEY` environment variable as a comma-separated list of keys. 

### 🖼️ Galleries

Now by default the model-gallery repositories are configured in the container images

### 💡 New project

[LocalAGI](https://github.com/mudler/LocalAGI) is a simple agent that uses LocalAI functions to have a full locally runnable assistant (with no API keys needed). 

See it [here in action](https://github.com/mudler/LocalAGI/assets/2420543/9ba43b82-dec5-432a-bdb9-8318e7db59a4) planning a trip for San Francisco! 

The full changelog is available [here](https://github.com/go-skynet/LocalAI/releases/tag/v.1.24.0).

--- 

## 🔥🔥 29-07-2023: __v1.23.0__ 🚀

This release focuses mostly on bugfixing and updates, with just a couple of new features:

* feat: add rope settings and negative prompt, drop grammar backend by {{< github "mudler" >}} in {{< pr "797" >}}
* Added CPU information to entrypoint.sh by @finger42 in {{< pr "794" >}}
* feat: cancel stream generation if client disappears by @tmm1 in {{< pr "792" >}}
  
Most notably, this release brings important fixes for CUDA (and not only):

* fix: add rope settings during model load, fix CUDA by {{< github "mudler" >}} in {{< pr "821" >}}
* fix: select function calls if 'name' is set in the request by {{< github "mudler" >}} in {{< pr "827" >}}
* fix: symlink libphonemize in the container by {{< github "mudler" >}} in {{< pr "831" >}}
  
{{% alert note %}}

From this release [OpenAI functions]({{%relref "docs/features/openai-functions" %}}) are available in the `llama` backend. The `llama-grammar` has been deprecated. See also [OpenAI functions]({{%relref "docs/features/openai-functions" %}}).

{{% /alert %}}

The full [changelog is available here](https://github.com/go-skynet/LocalAI/releases/tag/v1.23.0)

--- 

## 🔥🔥🔥 23-07-2023: __v1.22.0__ 🚀

* feat: add llama-master backend by {{< github "mudler" >}} in {{< pr "752" >}}
* [build] pass build type to cmake on libtransformers.a build by @TonDar0n in {{< pr "741" >}}
* feat: resolve JSONSchema refs (planners) by {{< github "mudler" >}} in {{< pr "774" >}}
* feat: backends improvements by {{< github "mudler" >}} in {{< pr "778" >}}
* feat(llama2): add template for chat messages by {{< github "dave-gray101" >}}  in {{< pr "782" >}}

{{% alert note %}}

From this release to use the OpenAI functions you need to use the `llama-grammar` backend. It has been added a `llama` backend for tracking `llama.cpp` master and `llama-grammar` for the grammar functionalities that have not been merged yet upstream. See also [OpenAI functions]({{%relref "docs/features/openai-functions" %}}). Until the feature is merged we will have two llama backends.

{{% /alert %}}

## Huggingface embeddings

In this release is now possible to specify to LocalAI external `gRPC` backends that can be used for inferencing {{< pr "778" >}}. It is now possible to write internal backends in any language, and a `huggingface-embeddings` backend is now available in the container image to be used with https://github.com/UKPLab/sentence-transformers. See also [Embeddings]({{%relref "docs/features/embeddings" %}}).

## LLaMa 2 has been released!

Thanks to the community effort now LocalAI supports templating for LLaMa2! more at: {{< pr "782" >}} until we update the model gallery with LLaMa2 models!

## Official langchain integration

Progress has been made to support LocalAI with `langchain`. See: https://github.com/langchain-ai/langchain/pull/8134

--- 

## 🔥🔥🔥 17-07-2023: __v1.21.0__ 🚀

* [whisper] Partial support for verbose_json format in transcribe endpoint by `@ldotlopez` in {{< pr "721" >}}
* LocalAI functions by `@mudler` in {{< pr "726" >}}
* `gRPC`-based backends by `@mudler` in {{< pr "743" >}}
* falcon support (7b and 40b) with `ggllm.cpp` by `@mudler` in {{< pr "743" >}}

### LocalAI functions

This allows to run OpenAI functions as described in the OpenAI blog post and documentation: https://openai.com/blog/function-calling-and-other-api-updates.

This is a video of running the same example, locally with `LocalAI`:
![localai-functions-1](https://github.com/ggerganov/llama.cpp/assets/2420543/5bd15da2-78c1-4625-be90-1e938e6823f1)

And here when it actually picks to reply to the user instead of using functions!
![functions-2](https://github.com/ggerganov/llama.cpp/assets/2420543/e3f89d15-1d2c-45ab-974f-6c9eb8eae41d)

Note: functions are supported only with `llama.cpp`-compatible models.

A full example is available here: https://github.com/go-skynet/LocalAI/tree/master/examples/functions

### gRPC backends

This is an internal refactor which is not user-facing, however, it allows to ease out maintenance and addition of new backends to LocalAI!

### `falcon` support

Now Falcon 7b and 40b models compatible with https://github.com/cmp-nct/ggllm.cpp are supported as well.

The former, ggml-based backend has been renamed to `falcon-ggml`.

### Default pre-compiled binaries

From this release the default behavior of images has changed. Compilation is not triggered on start automatically, to recompile `local-ai` from scratch on start and switch back to the old behavior, you can set `REBUILD=true` in the environment variables. Rebuilding can be necessary if your CPU and/or architecture is old and the pre-compiled binaries are not compatible with your platform. See the [build section]({{%relref "docs/getting-started/build" %}}) for more information.

[Full release changelog](https://github.com/go-skynet/LocalAI/releases/tag/v1.21.0)

--- 

## 🔥🔥🔥 28-06-2023: __v1.20.0__ 🚀

### Exciting New Features 🎉

* Add Text-to-Audio generation with `go-piper` by {{< github "mudler" >}} in {{< pr "649" >}} See [API endpoints]({{%relref "docs/features/text-to-audio" %}}) in our documentation.
* Add gallery repository by {{< github "mudler" >}} in {{< pr "663" >}}. See [models]({{%relref "docs/features/model-gallery" %}}) for documentation.

### Container images
- Standard (GPT + `stablediffusion`): `quay.io/go-skynet/local-ai:v1.20.0`
- FFmpeg: `quay.io/go-skynet/local-ai:v1.20.0-ffmpeg`
- CUDA 11+FFmpeg: `quay.io/go-skynet/local-ai:v1.20.0-cublas-cuda11-ffmpeg`
- CUDA 12+FFmpeg: `quay.io/go-skynet/local-ai:v1.20.0-cublas-cuda12-ffmpeg`

### Updates

Updates to `llama.cpp`, `go-transformers`, `gpt4all.cpp` and `rwkv.cpp`.

The NUMA option was enabled by {{< github "mudler" >}} in {{< pr "684" >}}, along with many new parameters (`mmap`,`mmlock`, ..). See [advanced]({{%relref "docs/advanced" %}}) for the full list of parameters.

### Gallery repositories

In this release there is support for gallery repositories. These are repositories that contain models, and can be used to install models. The default gallery which contains only freely licensed models is in Github: https://github.com/go-skynet/model-gallery, but you can use your own gallery by setting the `GALLERIES` environment variable. An automatic index of huggingface models is available as well.

For example, now you can start `LocalAI` with the following environment variable to use both galleries:

```bash
GALLERIES=[{"name":"model-gallery", "url":"github:go-skynet/model-gallery/index.yaml"}, {"url": "github:ci-robbot/localai-huggingface-zoo/index.yaml","name":"huggingface"}]
```

And in runtime you can install a model from huggingface now with:

```bash
curl http://localhost:8000/models/apply -H "Content-Type: application/json" -d '{ "id": "huggingface@thebloke__open-llama-7b-open-instruct-ggml__open-llama-7b-open-instruct.ggmlv3.q4_0.bin" }'
```

or a `tts` voice with:

```bash
curl http://localhost:8080/models/apply -H "Content-Type: application/json" -d '{ "id": "model-gallery@voice-en-us-kathleen-low" }'
```

See also [models]({{%relref "docs/features/model-gallery" %}}) for a complete documentation.

### Text to Audio

Now `LocalAI` uses [piper](https://github.com/rhasspy/piper) and [go-piper](https://github.com/mudler/go-piper) to generate audio from text. This is an experimental feature, and it requires `GO_TAGS=tts` to be set during build. It is enabled by default in the pre-built container images.

To setup audio models, you can use the new galleries, or setup the models manually as described in [the API section of the documentation]({{%relref "docs/features/text-to-audio" %}}).

You can check the full changelog in [Github](https://github.com/go-skynet/LocalAI/releases/tag/v1.20.0)

--- 

## 🔥🔥🔥 19-06-2023: __v1.19.0__ 🚀

- Full CUDA GPU offload support ( [PR](https://github.com/go-skynet/go-llama.cpp/pull/105) by [mudler](https://github.com/mudler). Thanks to [chnyda](https://github.com/chnyda) for handing over the GPU access, and [lu-zero](https://github.com/lu-zero) to help in debugging  )
- Full GPU Metal Support is now fully functional. Thanks to [Soleblaze](https://github.com/Soleblaze) to iron out the Metal Apple silicon support!

Container images:
- Standard (GPT + `stablediffusion`): `quay.io/go-skynet/local-ai:v1.19.2`
- FFmpeg: `quay.io/go-skynet/local-ai:v1.19.2-ffmpeg`
- CUDA 11+FFmpeg: `quay.io/go-skynet/local-ai:v1.19.2-cublas-cuda11-ffmpeg`
- CUDA 12+FFmpeg: `quay.io/go-skynet/local-ai:v1.19.2-cublas-cuda12-ffmpeg`

--- 

## 🔥🔥🔥 06-06-2023: __v1.18.0__ 🚀

This LocalAI release is plenty of new features, bugfixes and updates! Thanks to the community for the help, this was a great community release!

We now support a vast variety of models, while being backward compatible with prior quantization formats, this new release allows still to load older formats and new [k-quants](https://github.com/ggerganov/llama.cpp/pull/1684)!

### New features

- ✨ Added support for `falcon`-based model families (7b)  ( [mudler](https://github.com/mudler) )
- ✨ Experimental support for Metal Apple Silicon GPU - ( [mudler](https://github.com/mudler) and thanks to [Soleblaze](https://github.com/Soleblaze) for testing! ). See the [build section]({{%relref "docs/getting-started/build#Acceleration" %}}).
- ✨ Support for token stream in the `/v1/completions` endpoint ( [samm81](https://github.com/samm81) )
- ✨ Added huggingface backend ( [Evilfreelancer](https://github.com/EvilFreelancer) )
- 📷 Stablediffusion now can output `2048x2048` images size with `esrgan`! ( [mudler](https://github.com/mudler) )

### Container images
- 🐋 CUDA container images (arm64, x86_64) ( [sebastien-prudhomme](https://github.com/sebastien-prudhomme) )
- 🐋 FFmpeg container images (arm64, x86_64) ( [mudler](https://github.com/mudler) )

### Dependencies updates

- 🆙 Bloomz has been updated to the latest ggml changes, including new quantization format ( [mudler](https://github.com/mudler) )
- 🆙 RWKV has been updated to the new quantization format( [mudler](https://github.com/mudler) )
- 🆙 [k-quants](https://github.com/ggerganov/llama.cpp/pull/1684) format support for the `llama` models ( [mudler](https://github.com/mudler) )
- 🆙 gpt4all has been updated, incorporating upstream changes allowing to load older models, and with different CPU instruction set (AVX only, AVX2) from the same binary! ( [mudler](https://github.com/mudler) )

### Generic

- 🐧 Fully Linux static binary releases ( [mudler](https://github.com/mudler) )
- 📷 Stablediffusion has been enabled on container images by default ( [mudler](https://github.com/mudler) )
  Note: You can disable container image rebuilds with `REBUILD=false`

### Examples

- 💡 [AutoGPT](https://github.com/go-skynet/LocalAI/tree/master/examples/autoGPT) example ( [mudler](https://github.com/mudler) )
- 💡 [PrivateGPT](https://github.com/go-skynet/LocalAI/tree/master/examples/privateGPT) example ( [mudler](https://github.com/mudler) )
- 💡 [Flowise](https://github.com/go-skynet/LocalAI/tree/master/examples/flowise) example ( [mudler](https://github.com/mudler) )

Two new projects offer now direct integration with LocalAI!

- [Flowise](https://github.com/FlowiseAI/Flowise/pull/123)
- [Mods](https://github.com/charmbracelet/mods)

[Full release changelog](https://github.com/go-skynet/LocalAI/releases/tag/v1.18.0)

--- 

## 29-05-2023: __v1.17.0__

Support for OpenCL has been added while building from sources.

You can now build LocalAI from source with `BUILD_TYPE=clblas` to have an OpenCL build. See also the [build section]({{%relref "docs/getting-started/build#Acceleration" %}}).

For instructions on how to install OpenCL/CLBlast see [here](https://github.com/ggerganov/llama.cpp#blas-build).

rwkv.cpp has been updated to the new ggml format [commit](https://github.com/saharNooby/rwkv.cpp/commit/dea929f8cad90b7cf2f820c5a3d6653cfdd58c4e).

--- 

## 27-05-2023: __v1.16.0__ 

Now it's possible to automatically download pre-configured models before starting the API. 

Start local-ai with the `PRELOAD_MODELS` containing a list of models from the gallery, for instance to install `gpt4all-j` as `gpt-3.5-turbo`:

```bash
PRELOAD_MODELS=[{"url": "github:go-skynet/model-gallery/gpt4all-j.yaml", "name": "gpt-3.5-turbo"}]
```

`llama.cpp` models now can also automatically save the prompt cache state as well by specifying in the model YAML configuration file:

```yaml
# Enable prompt caching

# This is a file that will be used to save/load the cache. relative to the models directory.
prompt_cache_path: "alpaca-cache"

# Always enable prompt cache
prompt_cache_all: true
```

See also the [advanced section]({{%relref "docs/advanced" %}}).

## Media, Blogs, Social

- [Create a slackbot for teams and OSS projects that answer to documentation](https://mudler.pm/posts/smart-slackbot-for-teams/)
- [LocalAI meets k8sgpt](https://www.youtube.com/watch?v=PKrDNuJ_dfE) - CNCF Webinar showcasing LocalAI and k8sgpt.
- [Question Answering on Documents locally with LangChain, LocalAI, Chroma, and GPT4All](https://mudler.pm/posts/localai-question-answering/) by Ettore Di Giacinto
- [Tutorial to use k8sgpt with LocalAI](https://medium.com/@tyler_97636/k8sgpt-localai-unlock-kubernetes-superpowers-for-free-584790de9b65) - excellent usecase for localAI, using AI to analyse Kubernetes clusters. by Tyller Gillson

## Previous 

- 23-05-2023: __v1.15.0__ released. `go-gpt2.cpp` backend got renamed to `go-ggml-transformers.cpp` updated including https://github.com/ggerganov/llama.cpp/pull/1508 which breaks compatibility with older models. This impacts RedPajama, GptNeoX, MPT(not `gpt4all-mpt`), Dolly, GPT2 and Starcoder based models. [Binary releases available](https://github.com/go-skynet/LocalAI/releases), various fixes, including {{< pr "341" >}} .
- 21-05-2023: __v1.14.0__ released. Minor updates to the `/models/apply` endpoint, `llama.cpp` backend updated including https://github.com/ggerganov/llama.cpp/pull/1508 which breaks compatibility with older models. `gpt4all` is still compatible with the old format. 
- 19-05-2023: __v1.13.0__ released! 🔥🔥 updates to the `gpt4all` and `llama` backend, consolidated CUDA support ( {{< pr "310" >}} thanks to @bubthegreat and @Thireus ), preliminar support for [installing models via API]({{%relref "docs/advanced#" %}}).
- 17-05-2023:  __v1.12.0__ released! 🔥🔥 Minor fixes, plus CUDA ({{< pr "258" >}}) support for `llama.cpp`-compatible models and image generation ({{< pr "272" >}}).
- 16-05-2023: 🔥🔥🔥 Experimental support for CUDA ({{< pr "258" >}}) in the `llama.cpp` backend and Stable diffusion CPU image generation ({{< pr "272" >}}) in `master`.

Now LocalAI can generate images too:

| mode=0                                                                                                                | mode=1 (winograd/sgemm)                                                                                                                |
|------------------------------------------------------------------------------------------------------------------------|------------------------------------------------------------------------------------------------------------------------|
| ![b6441997879](https://github.com/go-skynet/LocalAI/assets/2420543/d50af51c-51b7-4f39-b6c2-bf04c403894c)              | ![winograd2](https://github.com/go-skynet/LocalAI/assets/2420543/1935a69a-ecce-4afc-a099-1ac28cb649b3)                |

- 14-05-2023: __v1.11.1__ released! `rwkv` backend patch release
- 13-05-2023: __v1.11.0__ released! 🔥 Updated `llama.cpp` bindings: This update includes a breaking change in the model files ( https://github.com/ggerganov/llama.cpp/pull/1405 ) - old models should still work with the `gpt4all-llama` backend.
- 12-05-2023: __v1.10.0__ released! 🔥🔥 Updated `gpt4all` bindings. Added support for GPTNeox (experimental), RedPajama (experimental), Starcoder (experimental), Replit (experimental), MosaicML MPT. Also now `embeddings` endpoint supports tokens arrays. See the [langchain-chroma](https://github.com/go-skynet/LocalAI/tree/master/examples/langchain-chroma) example! Note - this update does NOT include https://github.com/ggerganov/llama.cpp/pull/1405 which makes models incompatible.
- 11-05-2023: __v1.9.0__ released! 🔥 Important whisper updates ( {{< pr "233" >}} {{< pr "229" >}} ) and extended gpt4all model families support ( {{< pr "232" >}} ). Redpajama/dolly experimental ( {{< pr "214" >}} )
- 10-05-2023: __v1.8.0__ released! 🔥 Added support for fast and accurate embeddings with `bert.cpp` ( {{< pr "222" >}} )
- 09-05-2023: Added experimental support for transcriptions endpoint ( {{< pr "211" >}} )
- 08-05-2023: Support for embeddings with models using the `llama.cpp` backend ( {{< pr "207" >}} )
- 02-05-2023: Support for `rwkv.cpp` models ( {{< pr "158" >}} ) and for `/edits` endpoint
- 01-05-2023: Support for SSE stream of tokens in `llama.cpp` backends ( {{< pr "152" >}} )

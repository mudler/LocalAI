
+++
disableToc = false
title = "LocalAGI"
weight = 2
+++

LocalAGI is a small 🤖 virtual assistant that you can run locally, made by the [LocalAI](https://github.com/go-skynet/LocalAI) author and powered by it.

![localagi](https://github.com/mudler/LocalAGI/assets/2420543/b69817ce-2361-4234-a575-8f578e159f33)

[AutoGPT](https://github.com/Significant-Gravitas/Auto-GPT), [babyAGI](https://github.com/yoheinakajima/babyagi), ... and now LocalAGI!

Github Link - https://github.com/mudler/LocalAGI

## Info

The goal is:
- Keep it simple, hackable and easy to understand
- No API keys needed, No cloud services needed, 100% Local. Tailored for Local use, however still compatible with OpenAI.
- Smart-agent/virtual assistant that can do tasks
- Small set of dependencies
- Run with Docker/Podman/Containers
- Rather than trying to do everything, provide a good starting point for other projects

Note: Be warned! It was hacked in a weekend, and it's just an experiment to see what can be done with local LLMs. 

![Screenshot from 2023-08-05 22-40-40](https://github.com/mudler/LocalAGI/assets/2420543/144da83d-3879-44f2-985c-efd690e2b136)

## 🚀 Features

- 🧠 LLM for intent detection
- 🧠 Uses functions for actions
    - 📝 Write to long-term memory
    - 📖 Read from long-term memory 
    - 🌐 Internet access for search
    - :card_file_box: Write files
    - 🔌 Plan steps to achieve a goal
- 🤖 Avatar creation with Stable Diffusion
- 🗨️ Conversational
- 🗣️ Voice synthesis with TTS

## :book: Quick start

No frills, just run docker-compose and start chatting with your virtual assistant:

```bash
# Modify the configuration
# nano .env
docker-compose run -i --rm localagi
```

## How to use it

By default localagi starts in interactive mode

### Examples

Road trip planner by limiting searching to internet to 3 results only:

```bash
docker-compose run -i --rm localagi \
  --skip-avatar \
  --subtask-context \
  --postprocess \
  --search-results 3 \
  --prompt "prepare a plan for my roadtrip to san francisco"
```

Limit results of planning to 3 steps:

```bash
docker-compose run -i --rm localagi \
  --skip-avatar \
  --subtask-context \
  --postprocess \
  --search-results 1 \
  --prompt "do a plan for my roadtrip to san francisco" \
  --plan-message "The assistant replies with a plan of 3 steps to answer the request with a list of subtasks with logical steps. The reasoning includes a self-contained, detailed and descriptive instruction to fullfill the task."
```

### Advanced

localagi has several options in the CLI to tweak the experience:

- `--system-prompt` is the system prompt to use. If not specified, it will use none.
- `--prompt` is the prompt to use for batch mode. If not specified, it will default to interactive mode.
- `--interactive` is the interactive mode. When used with `--prompt` will drop you in an interactive session after the first prompt is evaluated.
- `--skip-avatar` will skip avatar creation. Useful if you want to run it in a headless environment.
- `--re-evaluate` will re-evaluate if another action is needed or we have completed the user request.
- `--postprocess` will postprocess the reasoning for analysis.
- `--subtask-context` will include context in subtasks.
- `--search-results` is the number of search results to use.
- `--plan-message` is the message to use during planning. You can override the message for example to force a plan to have a different message.
- `--tts-api-base` is the TTS API base. Defaults to `http://api:8080`.
- `--localai-api-base` is the LocalAI API base. Defaults to `http://api:8080`.
- `--images-api-base` is the Images API base. Defaults to `http://api:8080`.
- `--embeddings-api-base` is the Embeddings API base. Defaults to `http://api:8080`.
- `--functions-model` is the functions model to use. Defaults to `functions`.
- `--embeddings-model` is the embeddings model to use. Defaults to `all-MiniLM-L6-v2`.
- `--llm-model` is the LLM model to use. Defaults to `gpt-4`.
- `--tts-model` is the TTS model to use. Defaults to `en-us-kathleen-low.onnx`.
- `--stablediffusion-model` is the Stable Diffusion model to use. Defaults to `stablediffusion`.
- `--stablediffusion-prompt` is the Stable Diffusion prompt to use. Defaults to `DEFAULT_PROMPT`.
- `--force-action` will force a specific action.
- `--debug` will enable debug mode.

### Customize

To use a different model, you can see the examples in the `config` folder.
To select a model, modify the `.env` file and change the `PRELOAD_MODELS_CONFIG` variable to use a different configuration file.

### Caveats

The "goodness" of a model has a big impact on how LocalAGI works. Currently `13b` models are powerful enough to actually able to perform multi-step tasks or do more actions. However, it is quite slow when running on CPU (no big surprise here).

The context size is a limitation - you can find in the `config` examples to run with superhot 8k context size, but the quality is not good enough to perform complex tasks.

## What is LocalAGI?

It is a dead simple experiment to show how to tie the various LocalAI functionalities to create a virtual assistant that can do tasks. It is simple on purpose, trying to be minimalistic and easy to understand and customize for everyone.

It is different from babyAGI or AutoGPT as it uses [LocalAI functions](https://localai.io/features/openai-functions/) - it is a from scratch attempt built on purpose to run locally with [LocalAI](https://localai.io) (no API keys needed!) instead of expensive, cloud services. It sets apart from other projects as it strives to be small, and easy to fork on.

### How it works?

`LocalAGI` just does the minimal around LocalAI functions to create a virtual assistant that can do generic tasks. It works by an endless loop of `intent detection`, `function invocation`, `self-evaluation` and `reply generation` (if it decides to reply! :)). The agent is capable of planning complex tasks by invoking multiple functions, and remember things from the conversation.

In a nutshell, it goes like this:

- Decide based on the conversation history if it needs to take an action by using functions. It uses the LLM to detect the intent from the conversation.
- if it need to take an action (e.g. "remember something from the conversation" ) or generate complex tasks ( executing a chain of functions to achieve a goal ) it invokes the functions
- it re-evaluates if it needs to do any other action
- return the result back to the LLM to generate a reply for the user

Under the hood LocalAI converts functions to llama.cpp BNF grammars. While OpenAI fine-tuned a model to reply to functions, LocalAI constrains the LLM to follow grammars. This is a much more efficient way to do it, and it is also more flexible as you can define your own functions and grammars. For learning more about this, check out the [LocalAI documentation](https://localai.io/docs/llm) and my tweet that explains how it works under the hoods: https://twitter.com/mudler_it/status/1675524071457533953.

### Agent functions

The intention of this project is to keep the agent minimal, so can be built on top of it or forked. The agent is capable of doing the following functions:
- remember something from the conversation
- recall something from the conversation
- search something from the internet
- plan a complex task by invoking multiple functions
- write files to disk

## Roadmap

- [x] 100% Local, with Local AI. NO API KEYS NEEDED!
- [x] Create a simple virtual assistant
- [x] Make the virtual assistant do functions like store long-term memory and autonomously search between them when needed
- [x] Create the assistant avatar with Stable Diffusion
- [x] Give it a voice 
- [ ] Use weaviate instead of Chroma
- [ ] Get voice input (push to talk or wakeword)
- [ ] Make a REST API (OpenAI compliant?) so can be plugged by e.g. a third party service
- [x] Take a system prompt so can act with a "character" (e.g. "answer in rick and morty style")

## Development

Run docker-compose with main.py checked-out:

```bash
docker-compose run -v main.py:/app/main.py -i --rm localagi
```

## Notes

- a 13b model is enough for doing contextualized research and search/retrieve memory
- a 30b model is enough to generate a roadmap trip plan ( so cool! )
- With superhot models looses its magic, but maybe suitable for search
- Context size is your enemy. `--postprocess` some times helps, but not always
- It can be silly!
- It is slow on CPU, don't expect `7b` models to perform good, and `13b` models perform better but on CPU are quite slow.

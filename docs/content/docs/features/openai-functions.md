
+++
disableToc = false
title = "🔥 OpenAI functions and tools"
weight = 17
url = "/features/openai-functions/"
+++

LocalAI supports running OpenAI [functions and tools API](https://platform.openai.com/docs/api-reference/chat/create#chat-create-tools) with `llama.cpp` compatible models.

![localai-functions-1](https://github.com/ggerganov/llama.cpp/assets/2420543/5bd15da2-78c1-4625-be90-1e938e6823f1)

To learn more about OpenAI functions, see also the [OpenAI API blog post](https://openai.com/blog/function-calling-and-other-api-updates).

LocalAI is also supporting [JSON mode](https://platform.openai.com/docs/guides/text-generation/json-mode) out of the box with llama.cpp-compatible models.

💡 Check out also [LocalAGI](https://github.com/mudler/LocalAGI) for an example on how to use LocalAI functions.

## Setup

OpenAI functions are available only with `ggml` or `gguf` models compatible with `llama.cpp`.

You don't need to do anything specific - just use `ggml` or `gguf` models.


## Usage example

You can configure a model manually with a YAML config file in the models directory, for example:

```yaml
name: gpt-3.5-turbo
parameters:
  # Model file name
  model: ggml-openllama.bin
  top_p: 80
  top_k: 0.9
  temperature: 0.1
```

To use the functions with the OpenAI client in python:

```python
import openai
# ...
# Send the conversation and available functions to GPT
messages = [{"role": "user", "content": "What's the weather like in Boston?"}]
functions = [
    {
        "name": "get_current_weather",
        "description": "Get the current weather in a given location",
        "parameters": {
            "type": "object",
            "properties": {
                "location": {
                    "type": "string",
                    "description": "The city and state, e.g. San Francisco, CA",
                },
                "unit": {"type": "string", "enum": ["celsius", "fahrenheit"]},
            },
            "required": ["location"],
        },
    }
]
response = openai.ChatCompletion.create(
    model="gpt-3.5-turbo",
    messages=messages,
    functions=functions,
    function_call="auto",
)
# ...
```

{{% alert note %}}
When running the python script, be sure to:

- Set `OPENAI_API_KEY` environment variable to a random string (the OpenAI api key is NOT required!)
- Set `OPENAI_API_BASE` to point to your LocalAI service, for example `OPENAI_API_BASE=http://localhost:8080`

{{% /alert %}}

## Advanced

### Use functions without grammars

The functions calls maps automatically to grammars which are currently supported only by llama.cpp, however, it is possible to turn off the use of grammars, and extract tool arguments from the LLM responses, by specifying in the YAML file `no_grammar` and a regex to map the response from the LLM:

```yaml
name: model_name
parameters:
  # Model file name
  model: model/name

function:
  # set to true to not use grammars
  no_grammar: true
  # set one or more regexes used to extract the function tool arguments from the LLM response
  response_regex:
  - "(?P<function>\w+)\s*\((?P<arguments>.*)\)"
```

The response regex have to be a regex with named parameters to allow to scan the function name and the arguments. For instance, consider:

```
(?P<function>\w+)\s*\((?P<arguments>.*)\)
```

will catch

```
function_name({ "foo": "bar"})
```

### Parallel tools calls

This feature is experimental and has to be configured in the YAML of the model by enabling `function.parallel_calls`:

```yaml
name: gpt-3.5-turbo
parameters:
  # Model file name
  model: ggml-openllama.bin
  top_p: 80
  top_k: 0.9
  temperature: 0.1

function:
  # set to true to allow the model to call multiple functions in parallel
  parallel_calls: true
```

### Use functions with grammar

It is possible to also specify the full function signature (for debugging, or to use with other clients).

The chat endpoint accepts the `grammar_json_functions` additional parameter which takes a JSON schema object.

For example, with curl:

```bash
curl http://localhost:8080/v1/chat/completions -H "Content-Type: application/json" -d '{
     "model": "gpt-4",
     "messages": [{"role": "user", "content": "How are you?"}],
     "temperature": 0.1,
     "grammar_json_functions": {
        "oneOf": [
            {
                "type": "object",
                "properties": {
                    "function": {"const": "create_event"},
                    "arguments": {
                        "type": "object",
                        "properties": {
                            "title": {"type": "string"},
                            "date": {"type": "string"},
                            "time": {"type": "string"}
                        }
                    }
                }
            },
            {
                "type": "object",
                "properties": {
                    "function": {"const": "search"},
                    "arguments": {
                        "type": "object",
                        "properties": {
                            "query": {"type": "string"}
                        }
                    }
                }
            }
        ]
    }
   }'
```

Grammars and function tools can be used as well in conjunction with vision APIs:

```bash
 curl http://localhost:8080/v1/chat/completions -H "Content-Type: application/json" -d '{
     "model": "llava", "grammar": "root ::= (\"yes\" | \"no\")",
     "messages": [{"role": "user", "content": [{"type":"text", "text": "Is there some grass in the image?"}, {"type": "image_url", "image_url": {"url": "https://upload.wikimedia.org/wikipedia/commons/thumb/d/dd/Gfp-wisconsin-madison-the-nature-boardwalk.jpg/2560px-Gfp-wisconsin-madison-the-nature-boardwalk.jpg" }}], "temperature": 0.9}]}'
```


## 💡 Examples

A full e2e example with `docker-compose` is available [here](https://github.com/go-skynet/LocalAI/tree/master/examples/functions).

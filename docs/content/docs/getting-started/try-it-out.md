 
+++
disableToc = false
title = "Try it out"
weight = 4
url = '/basics/try/'
icon = "rocket_launch"
+++

Once LocalAI is installed, you can start it (either by using docker, or the cli, or the systemd service).

By default the LocalAI WebUI should be accessible from http://localhost:8080. You can also use 3rd party projects to interact with LocalAI as you would use OpenAI (see also [Integrations]({{%relref "docs/integrations" %}}) ). 

After installation, install new models by navigating the model gallery, or by using the `local-ai` CLI. 

{{% alert icon="🚀" %}}
To install models with the WebUI, see the [Models section]({{%relref "docs/features/model-gallery" %}}).
With the CLI you can list the models with `local-ai models list` and install them with `local-ai models install <model-name>`.

You can also [run models manually]({{%relref "docs/getting-started/models" %}}) by copying files into the `models` directory.
{{% /alert %}}

You can test out the API endpoints using `curl`, few examples are listed below. The models we are referring here (`gpt-4`, `gpt-4-vision-preview`, `tts-1`, `whisper-1`) are the default models that come with the AIO images - you can also use any other model you have installed.

### Text Generation

Creates a model response for the given chat conversation. [OpenAI documentation](https://platform.openai.com/docs/api-reference/chat/create).

<details>

```bash
curl http://localhost:8080/v1/chat/completions \
      -H "Content-Type: application/json" \
      -d '{ "model": "gpt-4", "messages": [{"role": "user", "content": "How are you doing?", "temperature": 0.1}] }' 
```

</details>

### GPT Vision

Understand images.

<details>

```bash
curl http://localhost:8080/v1/chat/completions \
    -H "Content-Type: application/json" \
    -d '{ 
        "model": "gpt-4-vision-preview", 
        "messages": [
          {
            "role": "user", "content": [
              {"type":"text", "text": "What is in the image?"},
              {
                "type": "image_url", 
                "image_url": {
                  "url": "https://upload.wikimedia.org/wikipedia/commons/thumb/d/dd/Gfp-wisconsin-madison-the-nature-boardwalk.jpg/2560px-Gfp-wisconsin-madison-the-nature-boardwalk.jpg" 
                }
              }
            ], 
          "temperature": 0.9
          }
        ]
      }' 
```

</details>

### Function calling

Call functions

<details>

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4",
    "messages": [
      {
        "role": "user",
        "content": "What is the weather like in Boston?"
      }
    ],
    "tools": [
      {
        "type": "function",
        "function": {
          "name": "get_current_weather",
          "description": "Get the current weather in a given location",
          "parameters": {
            "type": "object",
            "properties": {
              "location": {
                "type": "string",
                "description": "The city and state, e.g. San Francisco, CA"
              },
              "unit": {
                "type": "string",
                "enum": ["celsius", "fahrenheit"]
              }
            },
            "required": ["location"]
          }
        }
      }
    ],
    "tool_choice": "auto"
  }'
```

</details>

### Image Generation

Creates an image given a prompt. [OpenAI documentation](https://platform.openai.com/docs/api-reference/images/create).

<details>

```bash
curl http://localhost:8080/v1/images/generations \
      -H "Content-Type: application/json" -d '{
          "prompt": "A cute baby sea otter",
          "size": "256x256"
        }'
```

</details>

### Text to speech


Generates audio from the input text. [OpenAI documentation](https://platform.openai.com/docs/api-reference/audio/createSpeech).

<details>

```bash
curl http://localhost:8080/v1/audio/speech \
  -H "Content-Type: application/json" \
  -d '{
    "model": "tts-1",
    "input": "The quick brown fox jumped over the lazy dog.",
    "voice": "alloy"
  }' \
  --output speech.mp3
```

</details>


### Audio Transcription

Transcribes audio into the input language. [OpenAI Documentation](https://platform.openai.com/docs/api-reference/audio/createTranscription).

<details>

Download first a sample to transcribe:

```bash
wget --quiet --show-progress -O gb1.ogg https://upload.wikimedia.org/wikipedia/commons/1/1f/George_W_Bush_Columbia_FINAL.ogg 
```

Send the example audio file to the transcriptions endpoint :
```bash
curl http://localhost:8080/v1/audio/transcriptions \
    -H "Content-Type: multipart/form-data" \
    -F file="@$PWD/gb1.ogg" -F model="whisper-1"
```

</details>

### Embeddings Generation

Get a vector representation of a given input that can be easily consumed by machine learning models and algorithms. [OpenAI Embeddings](https://platform.openai.com/docs/api-reference/embeddings).

<details>

```bash
curl http://localhost:8080/embeddings \
    -X POST -H "Content-Type: application/json" \
    -d '{ 
        "input": "Your text string goes here", 
        "model": "text-embedding-ada-002"
      }'
```

</details>

{{% alert icon="💡" %}}

Don't use the model file as `model` in the request unless you want to handle the prompt template for yourself.

Use the model names like you would do with OpenAI like in the examples below. For instance `gpt-4-vision-preview`, or `gpt-4`.

{{% /alert %}}

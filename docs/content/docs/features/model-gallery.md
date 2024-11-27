
+++
disableToc = false
title = "🖼️ Model gallery"

weight = 18
url = '/models'
+++

The model gallery is a curated collection of models configurations for [LocalAI](https://github.com/go-skynet/LocalAI) that enables one-click install of models directly from the LocalAI Web interface.

A list of the models available can also be browsed at [the Public LocalAI Gallery](https://models.localai.io).

LocalAI to ease out installations of models provide a way to preload models on start and downloading and installing them in runtime. You can install models manually by copying them over the `models` directory, or use the API or the Web interface to configure, download and verify the model assets for you. 


{{% alert note %}}
The models in this gallery are not directly maintained by LocalAI. If you find a model that is not working, please open an issue on the model gallery repository.
{{% /alert %}}

{{% alert note %}}
GPT and text generation models might have a license which is not permissive for commercial use or might be questionable or without any license at all. Please check the model license before using it. The official gallery contains only open licensed models.
{{% /alert %}}

![output](https://github.com/mudler/LocalAI/assets/2420543/7b16676e-d5b1-4c97-89bd-9fa5065c21ad)

## Useful Links and resources

- [Open LLM Leaderboard](https://huggingface.co/spaces/HuggingFaceH4/open_llm_leaderboard) - here you can find a list of the most performing models on the Open LLM benchmark. Keep in mind models compatible with LocalAI must be quantized in the `gguf` format.

## How it works

Navigate the WebUI interface in the "Models" section from the navbar at the top. Here you can find a list of models that can be installed, and you can install them by clicking the "Install" button.

## Add other galleries

You can add other galleries by setting the `GALLERIES` environment variable. The `GALLERIES` environment variable is a list of JSON objects, where each object has a `name` and a `url` field. The `name` field is the name of the gallery, and the `url` field is the URL of the gallery's index file, for example:

```json
GALLERIES=[{"name":"<GALLERY_NAME>", "url":"<GALLERY_URL"}]
```

The models in the gallery will be automatically indexed and available for installation.

## API Reference

### Model repositories

You can install a model in runtime, while the API is running and it is started already, or before starting the API by preloading the models.

To install a model in runtime you will need to use the `/models/apply` LocalAI API endpoint.

By default LocalAI is configured with the `localai` repository.

To use additional repositories you need to start `local-ai` with the `GALLERIES` environment variable:

```
GALLERIES=[{"name":"<GALLERY_NAME>", "url":"<GALLERY_URL"}]
```

For example, to enable the default `localai` repository, you can start `local-ai` with:

```
GALLERIES=[{"name":"localai", "url":"github:mudler/localai/gallery/index.yaml"}]
```

where `github:mudler/localai/gallery/index.yaml` will be expanded automatically to `https://raw.githubusercontent.com/mudler/LocalAI/main/index.yaml`.

Note: the url are expanded automatically for `github` and `huggingface`, however `https://` and `http://` prefix works as well.

{{% alert note %}}

If you want to build your own gallery, there is no documentation yet. However you can find the source of the default gallery in the [LocalAI repository](https://github.com/mudler/LocalAI/tree/master/gallery).
{{% /alert %}}


### List Models

To list all the available models, use the `/models/available` endpoint:

```bash
curl http://localhost:8080/models/available
```

To search for a model, you can use `jq`:

```bash
# Get all information about models with a name that contains "replit"
curl http://localhost:8080/models/available | jq '.[] | select(.name | contains("replit"))'

# Get the binary name of all local models (not hosted on Hugging Face)
curl http://localhost:8080/models/available | jq '.[] | .name | select(contains("localmodels"))'

# Get all of the model URLs that contains "orca"
curl http://localhost:8080/models/available | jq '.[] | .urls | select(. != null) | add | select(contains("orca"))'
```

### How to install a model from the repositories

Models can be installed by passing the full URL of the YAML config file, or either an identifier of the model in the gallery. The gallery is a repository of models that can be installed by passing the model name.

To install a model from the gallery repository, you can pass the model name in the `id` field. For instance, to install the `bert-embeddings` model, you can use the following command:

```bash
LOCALAI=http://localhost:8080
curl $LOCALAI/models/apply -H "Content-Type: application/json" -d '{
     "id": "localai@bert-embeddings"
   }'  
```

where:
- `localai` is the repository. It is optional and can be omitted. If the repository is omitted LocalAI will search the model by name in all the repositories. In the case the same model name is present in both galleries the first match wins.
- `bert-embeddings` is the model name in the gallery
  (read its [config here](https://github.com/mudler/LocalAI/tree/master/gallery/blob/main/bert-embeddings.yaml)).

### How to install a model not part of a gallery

If you don't want to set any gallery repository, you can still install models by loading a model configuration file.

In the body of the request you must specify the model configuration file URL (`url`), optionally a name to install the model (`name`), extra files to install (`files`), and configuration overrides (`overrides`). When calling the API endpoint, LocalAI will download the models files and write the configuration to the folder used to store models.

```bash
LOCALAI=http://localhost:8080
curl $LOCALAI/models/apply -H "Content-Type: application/json" -d '{
     "config_url": "<MODEL_CONFIG_FILE_URL>"
   }' 
# or if from a repository
curl $LOCALAI/models/apply -H "Content-Type: application/json" -d '{
     "id": "<GALLERY>@<MODEL_NAME>"
   }' 
# or from a gallery config
curl $LOCALAI/models/apply -H "Content-Type: application/json" -d '{
     "url": "<MODEL_CONFIG_FILE_URL>"
   }' 
```

An example that installs openllama can be:
   
```bash
LOCALAI=http://localhost:8080
curl $LOCALAI/models/apply -H "Content-Type: application/json" -d '{
     "config_url": "https://raw.githubusercontent.com/mudler/LocalAI/master/embedded/models/hermes-2-pro-mistral.yaml"
   }' 
```

The API will return a job `uuid` that you can use to track the job progress:
```
{"uuid":"1059474d-f4f9-11ed-8d99-c4cbe106d571","status":"http://localhost:8080/models/jobs/1059474d-f4f9-11ed-8d99-c4cbe106d571"}
```

For instance, a small example bash script that waits a job to complete can be (requires `jq`):

```bash
response=$(curl -s http://localhost:8080/models/apply -H "Content-Type: application/json" -d '{"url": "$model_url"}')

job_id=$(echo "$response" | jq -r '.uuid')

while [ "$(curl -s http://localhost:8080/models/jobs/"$job_id" | jq -r '.processed')" != "true" ]; do 
  sleep 1
done

echo "Job completed"
```

To preload models on start instead you can use the `PRELOAD_MODELS` environment variable.

<details>

To preload models on start, use the `PRELOAD_MODELS` environment variable by setting it to a JSON array of model uri:

```bash
PRELOAD_MODELS='[{"url": "<MODEL_URL>"}]'
```

Note: `url` or `id` must be specified. `url` is used to a url to a model gallery configuration, while an `id` is used to refer to models inside repositories. If both are specified, the `id` will be used.

For example:

```bash
PRELOAD_MODELS=[{"url": "github:mudler/LocalAI/gallery/stablediffusion.yaml@master"}]
```

or as arg:

```bash
local-ai --preload-models '[{"url": "github:mudler/LocalAI/gallery/stablediffusion.yaml@master"}]'
```

or in a YAML file:

```bash
local-ai --preload-models-config "/path/to/yaml"
```

YAML:
```yaml
- url: github:mudler/LocalAI/gallery/stablediffusion.yaml@master
```

</details>

{{% alert note %}}

You can find already some open licensed models in the [LocalAI gallery](https://github.com/mudler/LocalAI/tree/master/gallery).

If you don't find the model in the gallery you can try to use the "base" model and provide an URL to LocalAI:

<details>

```
curl $LOCALAI/models/apply -H "Content-Type: application/json" -d '{
     "url": "github:mudler/LocalAI/gallery/base.yaml@master",
     "name": "model-name",
     "files": [
        {
            "uri": "<URL>",
            "sha256": "<SHA>",
            "filename": "model"
        }
     ]
   }'
```

</details>

{{% /alert %}}

### Override a model name

To install a model with a different name, specify a `name` parameter in the request body.

```bash
LOCALAI=http://localhost:8080
curl $LOCALAI/models/apply -H "Content-Type: application/json" -d '{
     "url": "<MODEL_CONFIG_FILE>",
     "name": "<MODEL_NAME>"
   }'  
```

For example, to install a model as `gpt-3.5-turbo`:
   
```bash
LOCALAI=http://localhost:8080
curl $LOCALAI/models/apply -H "Content-Type: application/json" -d '{
      "url": "github:mudler/LocalAI/gallery/gpt4all-j.yaml",
      "name": "gpt-3.5-turbo"
   }'  
```
### Additional Files

<details>

To download additional files with the model, use the `files` parameter:

```bash
LOCALAI=http://localhost:8080
curl $LOCALAI/models/apply -H "Content-Type: application/json" -d '{
     "url": "<MODEL_CONFIG_FILE>",
     "name": "<MODEL_NAME>",
     "files": [
        {
            "uri": "<additional_file_url>",
            "sha256": "<additional_file_hash>",
            "filename": "<additional_file_name>"
        }
     ]
   }'  
```

</details>

### Overriding configuration files

<details>

To override portions of the configuration file, such as the backend or the model file, use the `overrides` parameter:

```bash
LOCALAI=http://localhost:8080
curl $LOCALAI/models/apply -H "Content-Type: application/json" -d '{
     "url": "<MODEL_CONFIG_FILE>",
     "name": "<MODEL_NAME>",
     "overrides": {
        "backend": "llama",
        "f16": true,
        ...
     }
   }'  
```

</details>



## Examples

### Embeddings: Bert

<details>

```bash
curl $LOCALAI/models/apply -H "Content-Type: application/json" -d '{
     "id": "bert-embeddings",
     "name": "text-embedding-ada-002"
   }'  
```

To test it:

```bash
LOCALAI=http://localhost:8080
curl $LOCALAI/v1/embeddings -H "Content-Type: application/json" -d '{
    "input": "Test",
    "model": "text-embedding-ada-002"
  }'
```

</details>

### Image generation: Stable diffusion

URL: https://github.com/EdVince/Stable-Diffusion-NCNN

{{< tabs >}}
{{% tab name="Prepare the model in runtime" %}}

While the API is running, you can install the model by using the `/models/apply` endpoint and point it to the `stablediffusion` model in the [models-gallery](https://github.com/mudler/LocalAI/tree/master/gallery#image-generation-stable-diffusion):
```bash
curl $LOCALAI/models/apply -H "Content-Type: application/json" -d '{         
     "url": "github:mudler/LocalAI/gallery/stablediffusion.yaml@master"
   }'
```

{{% /tab %}}
{{% tab name="Automatically prepare the model before start" %}}

You can set the `PRELOAD_MODELS` environment variable:

```bash
PRELOAD_MODELS=[{"url": "github:mudler/LocalAI/gallery/stablediffusion.yaml@master"}]
```

or as arg:

```bash
local-ai --preload-models '[{"url": "github:mudler/LocalAI/gallery/stablediffusion.yaml@master"}]'
```

or in a YAML file:

```bash
local-ai --preload-models-config "/path/to/yaml"
```

YAML:
```yaml
- url: github:mudler/LocalAI/gallery/stablediffusion.yaml@master
```

{{% /tab %}}
{{< /tabs >}}

Test it:

```
curl $LOCALAI/v1/images/generations -H "Content-Type: application/json" -d '{
            "prompt": "floating hair, portrait, ((loli)), ((one girl)), cute face, hidden hands, asymmetrical bangs, beautiful detailed eyes, eye shadow, hair ornament, ribbons, bowties, buttons, pleated skirt, (((masterpiece))), ((best quality)), colorful|((part of the head)), ((((mutated hands and fingers)))), deformed, blurry, bad anatomy, disfigured, poorly drawn face, mutation, mutated, extra limb, ugly, poorly drawn hands, missing limb, blurry, floating limbs, disconnected limbs, malformed hands, blur, out of focus, long neck, long body, Octane renderer, lowres, bad anatomy, bad hands, text",
            "mode": 2,  "seed":9000,
            "size": "256x256", "n":2
}'
```

### Audio transcription: Whisper

URL: https://github.com/ggerganov/whisper.cpp

{{< tabs >}}
{{% tab name="Prepare the model in runtime" %}}

```bash
curl $LOCALAI/models/apply -H "Content-Type: application/json" -d '{         
     "url": "github:mudler/LocalAI/gallery/whisper-base.yaml@master",
     "name": "whisper-1"
   }'
```

{{% /tab %}}
{{% tab name="Automatically prepare the model before start" %}}

You can set the `PRELOAD_MODELS` environment variable:

```bash
PRELOAD_MODELS=[{"url": "github:mudler/LocalAI/gallery/whisper-base.yaml@master", "name": "whisper-1"}]
```

or as arg:

```bash
local-ai --preload-models '[{"url": "github:mudler/LocalAI/gallery/whisper-base.yaml@master", "name": "whisper-1"}]'
```

or in a YAML file:

```bash
local-ai --preload-models-config "/path/to/yaml"
```

YAML:
```yaml
- url: github:mudler/LocalAI/gallery/whisper-base.yaml@master
  name: whisper-1
```

{{% /tab %}}
{{< /tabs >}}

### Note

LocalAI will create a batch process that downloads the required files from a model definition and automatically reload itself to include the new model. 

Input: `url` or `id` (required), `name` (optional), `files` (optional)

```bash
curl http://localhost:8080/models/apply -H "Content-Type: application/json" -d '{
     "url": "<MODEL_DEFINITION_URL>",
     "id": "<GALLERY>@<MODEL_NAME>",
     "name": "<INSTALLED_MODEL_NAME>",
     "files": [
        {
            "uri": "<additional_file>",
            "sha256": "<additional_file_hash>",
            "filename": "<additional_file_name>"
        },
      "overrides": { "backend": "...", "f16": true }
     ]
   }
```

An optional, list of additional files can be specified to be downloaded within `files`. The `name` allows to override the model name. Finally it is possible to override the model config file with `override`.

The `url` is a full URL, or a github url (`github:org/repo/file.yaml`), or a local file (`file:///path/to/file.yaml`).
The `id` is a string in the form `<GALLERY>@<MODEL_NAME>`, where `<GALLERY>` is the name of the gallery, and `<MODEL_NAME>` is the name of the model in the gallery. Galleries can be specified during startup with the `GALLERIES` environment variable.

Returns an `uuid` and an `url` to follow up the state of the process:

```json
{ "uuid":"251475c9-f666-11ed-95e0-9a8a4480ac58", "status":"http://localhost:8080/models/jobs/251475c9-f666-11ed-95e0-9a8a4480ac58"}
```

To see a collection example of curated models definition files, see the [LocalAI repository](https://github.com/mudler/LocalAI/tree/master/gallery).

#### Get model job state `/models/jobs/<uid>`

This endpoint returns the state of the batch job associated to a model installation.

```bash
curl http://localhost:8080/models/jobs/<JOB_ID>
```

Returns a json containing the error, and if the job is being processed:

```json
{"error":null,"processed":true,"message":"completed"}
```

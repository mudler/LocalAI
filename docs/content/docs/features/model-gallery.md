
+++
disableToc = false
title = "🖼️ Model gallery"

weight = 18
url = '/models'
+++

<h1 align="center">
  <br>
  <img height="300" src="https://github.com/go-skynet/model-gallery/assets/2420543/7a6a8183-6d0a-4dc4-8e1d-f2672fab354e"> <br>
<br>
</h1>

The model gallery is a (experimental!) collection of models configurations for [LocalAI](https://github.com/go-skynet/LocalAI).

LocalAI to ease out installations of models provide a way to preload models on start and downloading and installing them in runtime. You can install models manually by copying them over the `models` directory, or use the API to configure, download and verify the model assets for you. As the UI is still a work in progress, you will find here the documentation about the API Endpoints.

{{% alert note %}}
The models in this gallery are not directly maintained by LocalAI. If you find a model that is not working, please open an issue on the model gallery repository.
{{% /alert %}}

{{% alert note %}}
GPT and text generation models might have a license which is not permissive for commercial use or might be questionable or without any license at all. Please check the model license before using it. The official gallery contains only open licensed models.
{{% /alert %}}

## Useful Links and resources

- [Open LLM Leaderboard](https://huggingface.co/spaces/HuggingFaceH4/open_llm_leaderboard) - here you can find a list of the most performing models on the Open LLM benchmark. Keep in mind models compatible with LocalAI must be quantized in the `gguf` format.


## Model repositories

You can install a model in runtime, while the API is running and it is started already, or before starting the API by preloading the models.

To install a model in runtime you will need to use the `/models/apply` LocalAI API endpoint.

To enable the `model-gallery` repository you need to start `local-ai` with the `GALLERIES` environment variable:

```
GALLERIES=[{"name":"<GALLERY_NAME>", "url":"<GALLERY_URL"}]
```

For example, to enable the `model-gallery` repository, start `local-ai` with:

```
GALLERIES=[{"name":"model-gallery", "url":"github:go-skynet/model-gallery/index.yaml"}]
```

where `github:go-skynet/model-gallery/index.yaml` will be expanded automatically to `https://raw.githubusercontent.com/go-skynet/model-gallery/main/index.yaml`.

{{% alert note %}}

As this feature is experimental, you need to run `local-ai` with a list of `GALLERIES`. Currently there are two galleries:

- An official one, containing only definitions and models with a clear LICENSE to avoid any dmca infringment. As I'm not sure what's the best action to do in this case, I'm not going to include any model that is not clearly licensed in this repository which is offically linked to LocalAI.
- A "community" one that contains an index of `huggingface` models that are compatible with the `ggml` format and lives in the `localai-huggingface-zoo` repository.

To enable the two repositories, start `LocalAI` with the `GALLERIES` environment variable:

```bash
GALLERIES=[{"name":"model-gallery", "url":"github:go-skynet/model-gallery/index.yaml"}, {"url": "github:go-skynet/model-gallery/huggingface.yaml","name":"huggingface"}]
```

If running with `docker-compose`, simply edit the `.env` file and uncomment the `GALLERIES` variable, and add the one you want to use.

{{% /alert %}}

{{% alert note %}}
You might not find all the models in this gallery. Automated CI updates the gallery automatically. You can find however most of the models on huggingface (https://huggingface.co/), generally it should be available `~24h` after upload.

By under any circumstances LocalAI and any developer is not responsible for the models in this gallery, as CI is just indexing them and providing a convenient way to install with an automatic configuration with a consistent API. Don't install models from authors you don't trust, and, check the appropriate license for your use case. Models are automatically indexed and hosted on huggingface (https://huggingface.co/). For any issue with the models, please open an issue on the model gallery repository if it's a LocalAI misconfiguration, otherwise refer to the huggingface repository. If you think a model should not be listed, please reach to us and we will remove it from the gallery.
{{% /alert %}}

{{% alert note %}}

There is no documentation yet on how to build a gallery or a repository - but you can find an example in the [model-gallery](https://github.com/go-skynet/model-gallery) repository.

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
     "id": "model-gallery@bert-embeddings"
   }'  
```

where:
- `model-gallery` is the repository. It is optional and can be omitted. If the repository is omitted LocalAI will search the model by name in all the repositories. In the case the same model name is present in both galleries the first match wins.
- `bert-embeddings` is the model name in the gallery
  (read its [config here](https://github.com/go-skynet/model-gallery/blob/main/bert-embeddings.yaml)).

{{% alert note %}}
If the `huggingface` model gallery is enabled (it's enabled by default),
and the model has an entry in the model gallery's associated YAML config
(for `huggingface`, see [`model-gallery/huggingface.yaml`](https://github.com/go-skynet/model-gallery/blob/main/huggingface.yaml)),
you can install models by specifying directly the model's `id`.
For example, to install wizardlm superhot:

```bash
LOCALAI=http://localhost:8080
curl $LOCALAI/models/apply -H "Content-Type: application/json" -d '{
     "id": "huggingface@TheBloke/WizardLM-13B-V1-0-Uncensored-SuperHOT-8K-GGML/wizardlm-13b-v1.0-superhot-8k.ggmlv3.q4_K_M.bin"
   }'  
```

Note that the `id` can be used similarly when pre-loading models at start.
{{% /alert %}}


## How to install a model (without a gallery)

If you don't want to set any gallery repository, you can still install models by loading a model configuration file.

In the body of the request you must specify the model configuration file URL (`url`), optionally a name to install the model (`name`), extra files to install (`files`), and configuration overrides (`overrides`). When calling the API endpoint, LocalAI will download the models files and write the configuration to the folder used to store models.

```bash
LOCALAI=http://localhost:8080
curl $LOCALAI/models/apply -H "Content-Type: application/json" -d '{
     "url": "<MODEL_CONFIG_FILE>"
   }' 
# or if from a repository
curl $LOCALAI/models/apply -H "Content-Type: application/json" -d '{
     "id": "<GALLERY>@<MODEL_NAME>"
   }' 
```

An example that installs openllama can be:
   
```bash
LOCALAI=http://localhost:8080
curl $LOCALAI/models/apply -H "Content-Type: application/json" -d '{
     "url": "https://github.com/go-skynet/model-gallery/blob/main/openllama_3b.yaml"
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
PRELOAD_MODELS=[{"url": "github:go-skynet/model-gallery/stablediffusion.yaml"}]
```

or as arg:

```bash
local-ai --preload-models '[{"url": "github:go-skynet/model-gallery/stablediffusion.yaml"}]'
```

or in a YAML file:

```bash
local-ai --preload-models-config "/path/to/yaml"
```

YAML:
```yaml
- url: github:go-skynet/model-gallery/stablediffusion.yaml
```

</details>

{{% alert note %}}

You can find already some open licensed models in the [model gallery](https://github.com/go-skynet/model-gallery).

If you don't find the model in the gallery you can try to use the "base" model and provide an URL to LocalAI:

<details>

```
curl $LOCALAI/models/apply -H "Content-Type: application/json" -d '{
     "url": "github:go-skynet/model-gallery/base.yaml",
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

## Installing a model with a different name

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
      "url": "github:go-skynet/model-gallery/gpt4all-j.yaml",
      "name": "gpt-3.5-turbo"
   }'  
```
## Additional Files

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

## Overriding configuration files

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
     "url": "github:go-skynet/model-gallery/bert-embeddings.yaml",
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

While the API is running, you can install the model by using the `/models/apply` endpoint and point it to the `stablediffusion` model in the [models-gallery](https://github.com/go-skynet/model-gallery#image-generation-stable-diffusion):
```bash
curl $LOCALAI/models/apply -H "Content-Type: application/json" -d '{         
     "url": "github:go-skynet/model-gallery/stablediffusion.yaml"
   }'
```

{{% /tab %}}
{{% tab name="Automatically prepare the model before start" %}}

You can set the `PRELOAD_MODELS` environment variable:

```bash
PRELOAD_MODELS=[{"url": "github:go-skynet/model-gallery/stablediffusion.yaml"}]
```

or as arg:

```bash
local-ai --preload-models '[{"url": "github:go-skynet/model-gallery/stablediffusion.yaml"}]'
```

or in a YAML file:

```bash
local-ai --preload-models-config "/path/to/yaml"
```

YAML:
```yaml
- url: github:go-skynet/model-gallery/stablediffusion.yaml
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
     "url": "github:go-skynet/model-gallery/whisper-base.yaml",
     "name": "whisper-1"
   }'
```

{{% /tab %}}
{{% tab name="Automatically prepare the model before start" %}}

You can set the `PRELOAD_MODELS` environment variable:

```bash
PRELOAD_MODELS=[{"url": "github:go-skynet/model-gallery/whisper-base.yaml", "name": "whisper-1"}]
```

or as arg:

```bash
local-ai --preload-models '[{"url": "github:go-skynet/model-gallery/whisper-base.yaml", "name": "whisper-1"}]'
```

or in a YAML file:

```bash
local-ai --preload-models-config "/path/to/yaml"
```

YAML:
```yaml
- url: github:go-skynet/model-gallery/whisper-base.yaml
  name: whisper-1
```

{{% /tab %}}
{{< /tabs >}}

### GPTs

<details>

```bash
LOCALAI=http://localhost:8080
curl $LOCALAI/models/apply -H "Content-Type: application/json" -d '{
     "url": "github:go-skynet/model-gallery/gpt4all-j.yaml",
     "name": "gpt4all-j"
   }'  
```

To test it:

```
curl $LOCALAI/v1/chat/completions -H "Content-Type: application/json" -d '{
     "model": "gpt4all-j", 
     "messages": [{"role": "user", "content": "How are you?"}],
     "temperature": 0.1 
   }'
```

</details>

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

To see a collection example of curated models definition files, see the [model-gallery](https://github.com/go-skynet/model-gallery).

#### Get model job state `/models/jobs/<uid>`

This endpoint returns the state of the batch job associated to a model installation.

```bash
curl http://localhost:8080/models/jobs/<JOB_ID>
```

Returns a json containing the error, and if the job is being processed:

```json
{"error":null,"processed":true,"message":"completed"}
```

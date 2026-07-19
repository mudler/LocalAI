
+++
disableToc = false
title = "Model Gallery"
weight = 81
url = '/models'
+++

The model gallery is a curated collection of models configurations for [LocalAI](https://github.com/go-skynet/LocalAI) that enables one-click install of models directly from the LocalAI Web interface.

A list of the models available can also be browsed at [the Public LocalAI Gallery](https://models.localai.io).

LocalAI to ease out installations of models provide a way to preload models on start and downloading and installing them in runtime. You can install models manually by copying them over the `models` directory, or use the API or the Web interface to configure, download and verify the model assets for you. 


{{% notice note %}}
The models in this gallery are not directly maintained by LocalAI. If you find a model that is not working, please open an issue on the [main LocalAI repository](https://github.com/mudler/LocalAI/issues).
 {{% /notice %}}

{{% notice note %}}
GPT and text generation models might have a license which is not permissive for commercial use or might be questionable or without any license at all. Please check the model license before using it. The official gallery contains only open licensed models.
 {{% /notice %}}

![output](https://github.com/mudler/LocalAI/assets/2420543/7b16676e-d5b1-4c97-89bd-9fa5065c21ad)

## Useful Links and resources

- [Open LLM Leaderboard](https://huggingface.co/spaces/HuggingFaceH4/open_llm_leaderboard) - here you can find a list of the most performing models on the Open LLM benchmark. Keep in mind models compatible with LocalAI must be quantized in the `gguf` format.

## How it works

Navigate the WebUI interface in the "Models" section from the navbar at the top. Here you can find a list of models that can be installed, and you can install them by clicking the "Install" button.

## VRAM and download size estimates

When browsing the gallery or importing a model by URI, LocalAI can show **estimated download size** and **estimated VRAM** for models.

- **Where they appear**: In the model gallery table (Size / VRAM column), in the model detail modal, and after starting an import from URI (in the success message).
- **How they are computed**: GGUF models use file size (HTTP HEAD or local stat) and optional GGUF metadata (HTTP Range) for KV cache and overhead; other formats use Hugging Face file sizes and optional config when available. If metadata is unavailable, a size-only heuristic is used.
- **Hardware fit indicator**: When your system reports GPU or RAM capacity, the gallery shows whether the estimated VRAM fits (green) or may not fit (red) using a 95% headroom rule.
- Estimates are best-effort and may be missing if the server does not support HEAD/Range or the request times out.

## Add other galleries

You can add other galleries by:

1. **Using the Web UI**: Navigate to the [Runtime Settings]({{%relref "features/runtime-settings#gallery-settings" %}}) page and configure galleries through the interface.

2. **Using Environment Variables**: Set the `GALLERIES` environment variable. The `GALLERIES` environment variable is a list of JSON objects, where each object has a `name` and a `url` field. The `name` field is the name of the gallery, and the `url` field is the URL of the gallery's index file, for example:

```json
GALLERIES=[{"name":"<GALLERY_NAME>", "url":"<GALLERY_URL"}]
```

3. **Using Configuration Files**: Add galleries to `runtime_settings.json` in the `LOCALAI_CONFIG_DIR` directory.

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

#### Using Local Gallery Files

You can also use local gallery index files by using the `file://` prefix. For security reasons, **local gallery files must be located within your models directory** (the directory specified by `MODELS_PATH` or the default `models/` directory).

**Example:**

```json
GALLERIES=[{"name":"my-local-gallery", "url":"file:///path/to/models/my-gallery-index.yaml"}]
```

**Important notes:**
- The `file://` prefix is required for local paths
- The file path must be absolute (starting with `/` on Unix systems)
- The resolved path must be within your models directory for security
- If you try to access files outside the models directory, LocalAI will block the request

**Valid example** (assuming `MODELS_PATH=/opt/localai/models`):
```json
GALLERIES=[{"name":"local", "url":"file:///opt/localai/models/galleries/my-gallery.yaml"}]
```

**Invalid example** (file outside models directory):
```json
GALLERIES=[{"name":"local", "url":"file:///home/user/my-gallery.yaml"}]
```
This will be rejected with a security error.

{{% notice note %}}

If you want to build your own gallery, there is no documentation yet. However you can find the source of the default gallery in the [LocalAI repository](https://github.com/mudler/LocalAI/tree/master/gallery).
 {{% /notice %}}


### List Models

To list all the available models, use the `/models/available` endpoint:

```bash
curl http://localhost:8080/models/available
```

To search for a model, you can use `jq`:

```bash
curl http://localhost:8080/models/available | jq '.[] | select(.name | contains("replit"))'

curl http://localhost:8080/models/available | jq '.[] | .name | select(contains("localmodels"))'

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

### Model variants

Some gallery entries offer several builds of the same model: different
quantizations, or the same weights served by a different engine. Such an entry
carries a `variants` list, and installing it normally lets LocalAI choose:

- variants whose backend cannot run on this machine are dropped;
- variants that do not fit the available memory are dropped. That budget is
  VRAM on a discrete-GPU host, and system RAM otherwise — including on
  unified-memory machines such as Apple Silicon, where the GPU shares system
  RAM and reports no separate VRAM pool;
- the entry's own build is never dropped. It competes with whatever survived
  rather than waiting for everything else to fail, so an entry that is itself
  the largest build that fits keeps its own payload;
- the largest remaining build wins, because a bigger footprint means a higher
  quality build of the same model;
- a build whose size could not be measured ranks below the entry's own build,
  so an unreadable size never quietly displaces the payload the entry ships;
- if nothing else survives, the entry's own build is installed. The entry is
  always installable, on any machine.

Because the entry's own build competes on size like every other candidate, the
order of the list means nothing and a `variants` list may offer smaller builds,
larger ones, or both.

Sizes are measured from the model's weights rather than downloaded, and cached.

The gallery listing only flags which entries offer variants, with a
`has_variants` field. It deliberately does not describe them: measuring a
variant is a network round trip per referenced build, so describing every
entry inline would make one listing request cost as many round trips as the
whole page has variants.

```bash
curl http://localhost:8080/api/models | jq '.models[] | select(.has_variants) | .name'
```

Ask for the description one entry at a time, as the web UI does when you open
a model's variant menu:

```bash
curl http://localhost:8080/api/models/variants/localai@nanbeige4.1-3b-q4
```

```json
{
  "auto_selected": "nanbeige4.1-3b-q8",
  "variants": [
    { "model": "nanbeige4.1-3b-q8", "backend": "llama-cpp", "memory_bytes": 4187593113, "fits": true, "is_base": false },
    { "model": "nanbeige4.1-3b-q4", "backend": "llama-cpp", "fits": true, "is_base": true }
  ]
}
```

`auto_selected` is what installing without a choice would pick right now. `fits`
is whether auto-selection would consider that variant on this machine, and
`is_base` marks the entry's own build. `memory_bytes` is omitted entirely, as on
the second entry above, when the size could not be measured; read a missing
`memory_bytes` as unknown rather than as a free build.

An entry that declares no variants carries no `has_variants` field and answers
this endpoint with an empty list, so a client never has to ask about it.

To install a specific one, pass its name as `variant`:

```bash
curl $LOCALAI/models/apply -H "Content-Type: application/json" -d '{
     "id": "localai@nanbeige4.1-3b-q4",
     "variant": "nanbeige4.1-3b-q8"
   }'
```

An explicit choice is honored even when the machine looks too small for it, so
you can deliberately install a build LocalAI would not have picked. A `variant`
the entry does not declare fails the install and names what was requested; it
never quietly falls back to auto-selection. The choice is recorded, so a later
reinstall or upgrade of the same model stays on the variant you picked.

The same option exists on the CLI:

```bash
local-ai models install nanbeige4.1-3b-q4 --variant nanbeige4.1-3b-q8
```

Entries without a `variants` list are unaffected by any of this and install
exactly as they always have.

### Artifact-backed models

Gallery models with an `artifacts` declaration are fully materialized during
installation. Their operation progresses through these phases:

```text
resolving -> downloading -> verifying -> committing -> persisting
```

The admin Operations Bar and `GET /api/operations` expose `currentBytes` and
`totalBytes` as raw transport bytes. Cancelling an active download leaves its
partial files in place so a retry can resume. A verification failure never
exposes a completed snapshot, while a retry or another installation reuses an
already verified content-addressed snapshot.

Deleting a model configuration does not delete its content-addressed snapshot
bytes. This allows another configuration or a later reinstall to reuse the
cache; safe cache garbage collection is deferred.

### How to install a model not part of a gallery

If you don't want to set any gallery repository, you can still install models by loading a model configuration file.

In the body of the request you must specify the model configuration file URL (`url`), optionally a name to install the model (`name`), extra files to install (`files`), and configuration overrides (`overrides`). When calling the API endpoint, LocalAI will download the models files and write the configuration to the folder used to store models.

```bash
LOCALAI=http://localhost:8080
curl $LOCALAI/models/apply -H "Content-Type: application/json" -d '{
     "config_url": "<MODEL_CONFIG_FILE_URL>"
   }' 
curl $LOCALAI/models/apply -H "Content-Type: application/json" -d '{
     "id": "<GALLERY>@<MODEL_NAME>"
   }' 
curl $LOCALAI/models/apply -H "Content-Type: application/json" -d '{
     "url": "<MODEL_CONFIG_FILE_URL>"
   }' 
```

An example that installs hermes-2-pro-mistral can be:
   
```bash
LOCALAI=http://localhost:8080
curl $LOCALAI/models/apply -H "Content-Type: application/json" -d '{
     "config_url": "https://raw.githubusercontent.com/mudler/LocalAI/v2.25.0/embedded/models/hermes-2-pro-mistral.yaml"
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

{{% notice note %}}

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

 {{% /notice %}}

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

{{% notice warning %}}
**Local file security restriction:** When using `file://` URLs, the file path must be within your models directory (specified by `MODELS_PATH`). Files outside this directory will be rejected for security reasons.
{{% /notice %}}

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

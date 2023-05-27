# AutoGPT

Example of integration with [AutoGPT](https://github.com/Significant-Gravitas/Auto-GPT).

## Run

```bash
# Clone LocalAI
git clone https://github.com/go-skynet/LocalAI

cd LocalAI/examples/autoGPT

docker-compose run --rm auto-gpt
```

Note: The example automatically downloads the `gpt4all` model as it is under a permissive license. The GPT4All model does not seem to be enough to run AutoGPT. WizardLM-7b-uncensored seems to perform better (with `f16: true`).

See the `.env` configuration file to set a different model with the [model-gallery](https://github.com/go-skynet/model-gallery) by editing `PRELOAD_MODELS`.

## Without docker

Run AutoGPT with `OPENAI_API_BASE` pointing to the LocalAI endpoint. If you run it locally for instance:

```
OPENAI_API_BASE=http://localhost:8080 python ...
```

Note: you need a model named `gpt-3.5-turbo` and `text-embedding-ada-002`. You can preload those in LocalAI at start by setting in the env:

```
PRELOAD_MODELS=[{"url": "github:go-skynet/model-gallery/gpt4all-j.yaml", "name": "gpt-3.5-turbo"}, { "url": "github:go-skynet/model-gallery/bert-embeddings.yaml", "name": "text-embedding-ada-002"}]
```
---
name: models
description: Use when a LocalAI server needs a model - find one in the model gallery, install it, follow the install job, change a model's configuration, or give a model a second name with an alias.
---

# Find, install and configure LocalAI models

A model in LocalAI is a YAML configuration file in the models folder, plus the weights it names. The model gallery lists ready-made configurations. Installing a gallery entry downloads the weights and writes the YAML.

Full docs: https://localai.io/docs/models/ and https://localai.io/docs/models/setting-up-models/

These steps use the admin API. With API keys on, send an administrator key:

```bash
LOCALAI=http://localhost:8080
AUTH="Authorization: Bearer $LOCALAI_API_KEY"
```

## 1. See what is installed

```bash
curl -s $LOCALAI/v1/models -H "$AUTH" | jq -r '.data[].id'
```

If the model is in the list, you are done. Do not install it again.

## 2. Find a model in the gallery

`GET /models/available` returns every gallery entry. It is a long list: filter it with `jq`.

```bash
curl -s $LOCALAI/models/available -H "$AUTH" \
  | jq -r '.[] | select(.name | test("qwen"; "i")) | "\(.gallery.name)@\(.name)  \(.size // "")"'
```

Search by tag (for example `llm`, `embeddings`, `tts`, `stt`, `image`):

```bash
curl -s $LOCALAI/models/available -H "$AUTH" \
  | jq -r '.[] | select((.tags // []) | index("embeddings")) | .name'
```

Pick one name. Prefer a small model when the user did not ask for a size. If several names match, show the user the short list and ask.

The web UI has the same list on the **Models** page. Browse the gallery file: https://github.com/mudler/LocalAI/blob/master/gallery/index.yaml

## 3. Install it

```bash
curl -s $LOCALAI/models/apply -H "$AUTH" \
  -H "Content-Type: application/json" \
  -d '{"id": "localai@gemma-4-e2b-it-qat-q4_0"}'
```

- `id` is `<gallery>@<name>`. The gallery part is optional; without it, the first gallery that has the name wins.
- Add `"name": "my-name"` to install it under another name.
- Some entries have several builds (`variants`). LocalAI picks the best one that this machine can run. To force one, add `"variant": "<variant name>"`.
- Add `"overrides": {"context_size": 8192}` to change fields of the YAML at install time.

The answer has a job ID:

```json
{"uuid": "1059474d-...", "status": "http://localhost:8080/models/jobs/1059474d-..."}
```

## 4. Follow the job

```bash
JOB=1059474d-...
curl -s $LOCALAI/models/jobs/$JOB -H "$AUTH" | jq '{processed, progress, message, error}'
```

- `processed: false`: still running. Check again after a few seconds. `progress` is a percentage.
- `processed: true` and no `error`: installed. The model is now in `/v1/models`.
- `error` set: the install failed. Tell the user the error. Do not retry in a loop.

A large model can take many minutes to download. Wait between checks.

## 5. Use it

Call it by its name, as any OpenAI model:

```bash
curl -s $LOCALAI/v1/chat/completions -H "$AUTH" \
  -H "Content-Type: application/json" \
  -d '{"model": "gemma-4-e2b-it-qat-q4_0", "messages": [{"role": "user", "content": "Hi"}]}'
```

The first call loads the model, so it is slower.

## Change a model's configuration

Read the configuration as JSON:

```bash
curl -s $LOCALAI/api/models/config-json/MODEL -H "$AUTH"
```

Change only the fields you send. The server merges them, checks the result and writes the YAML:

```bash
curl -s -X PATCH $LOCALAI/api/models/config-json/MODEL -H "$AUTH" \
  -H "Content-Type: application/json" \
  -d '{"context_size": 16384}'
```

A change reloads the model, so the next call is slower. Field reference: https://localai.io/docs/models/model-configuration/

A minimal YAML in the models folder:

```yaml
name: my-model
backend: llama-cpp
parameters:
  model: my-model.Q4_K_M.gguf
context_size: 8192
```

## Give a model a second name (alias)

An alias sends every call for one name to another model. Clients that ask for `gpt-4` get `my-model`. Write `gpt-4.yaml` in the models folder:

```yaml
name: gpt-4
alias: my-model
```

The target must be a real, enabled model, not another alias. Details: https://localai.io/docs/models/model-aliases/

## Remove a model

```bash
curl -s -X POST $LOCALAI/models/delete/MODEL -H "$AUTH"
```

Ask the user before you remove a model.

## On the server machine

The CLI does the same, on the machine that has the models folder:

```bash
local-ai models list
local-ai models install gemma-4-e2b-it-qat-q4_0
local-ai run gemma-4-e2b-it-qat-q4_0
```

## Errors

| You see | Do this |
|---|---|
| 401 or 403 on `/models/...` | These routes need an administrator. Ask one. |
| The name is not in `/models/available` | Search again with a shorter word. Do not invent a name. |
| The job `error` says a backend is missing | The backend for this model is not installed and could not be installed. Tell the user the backend name. |
| The job `error` says there is not enough memory | Pick a smaller model or a smaller quantization. |

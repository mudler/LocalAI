---
name: api
description: Use when you must call a LocalAI server over HTTP - chat, completions, embeddings, images, speech, transcription, reranking - or check that it is up, list its models, or send its API key.
---

# Call the LocalAI API

LocalAI serves an OpenAI-compatible API. A client made for the OpenAI API works when you point its base URL at the server and use a model name that the server has.

Full reference: https://localai.io/docs/api/ and the server's own page at `/swagger/index.html`.

## Before you start

1. Set the address. The default port is 8080.

   ```bash
   LOCALAI=http://localhost:8080
   ```

2. If the server has API keys, send one with each request. Both headers work:

   ```bash
   -H "Authorization: Bearer $LOCALAI_API_KEY"
   -H "x-api-key: $LOCALAI_API_KEY"
   ```

   Never print the key or write it in a file.

3. Check that the server is ready. `200` means ready.

   ```bash
   curl -s -o /dev/null -w '%{http_code}\n' $LOCALAI/readyz
   ```

4. List the models. Use a name from this list. Never guess a name.

   ```bash
   curl -s $LOCALAI/v1/models -H "Authorization: Bearer $LOCALAI_API_KEY" | jq -r '.data[].id'
   ```

   No model, or not the right kind: install one first (skill `localai:models`).

## OpenAI-compatible endpoints

| Task | Method and path |
|---|---|
| Chat | `POST /v1/chat/completions` |
| Text completion | `POST /v1/completions` |
| Responses API | `POST /v1/responses` |
| Anthropic Messages API | `POST /v1/messages` |
| Embeddings | `POST /v1/embeddings` |
| Image from text | `POST /v1/images/generations` |
| Text to speech | `POST /v1/audio/speech` |
| Speech to text | `POST /v1/audio/transcriptions` |
| Models | `GET /v1/models` |

### Chat

```bash
curl -s $LOCALAI/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $LOCALAI_API_KEY" \
  -d '{
    "model": "MODEL",
    "messages": [{"role": "user", "content": "Say hello in one word."}]
  }' | jq -r '.choices[0].message.content'
```

Add `"stream": true` to get server-sent events. Tools and function calling use the OpenAI `tools` field: https://localai.io/docs/api/openai-functions/

### Embeddings

```bash
curl -s $LOCALAI/v1/embeddings \
  -H "Content-Type: application/json" \
  -d '{"model": "MODEL", "input": "The cat sat on the mat."}' \
  | jq '.data[0].embedding | length'
```

### Image

```bash
curl -s $LOCALAI/v1/images/generations \
  -H "Content-Type: application/json" \
  -d '{"model": "MODEL", "prompt": "a red bicycle, studio photo", "size": "512x512"}'
```

The answer has a URL or base64 data in `data[0]`.

### Text to speech

```bash
curl -s $LOCALAI/v1/audio/speech \
  -H "Content-Type: application/json" \
  -d '{"model": "MODEL", "input": "Hello from LocalAI."}' \
  -o hello.wav
```

### Speech to text

```bash
curl -s $LOCALAI/v1/audio/transcriptions \
  -F file=@recording.wav -F model=MODEL | jq -r .text
```

## LocalAI endpoints

| Task | Method and path |
|---|---|
| Rerank documents | `POST /v1/rerank` |
| Object detection | `POST /v1/detection` |
| Ready check | `GET /readyz` |
| Server version | `GET /version` |
| What the server can do | `GET /.well-known/localai.json`, `GET /api/instructions` |
| Gallery and model install | `GET /models/available`, `POST /models/apply` (skill `localai:models`) |

### Rerank

```bash
curl -s $LOCALAI/v1/rerank \
  -H "Content-Type: application/json" \
  -d '{
    "model": "MODEL",
    "query": "How do I reset my password?",
    "documents": ["Open Settings, then Account.", "Our office opens at 9."],
    "top_n": 1
  }'
```

### Discover the API

`/api/instructions` lists short guides for each area of the API. Read one by name:

```bash
curl -s $LOCALAI/api/instructions | jq -r '.instructions[].name'
curl -s $LOCALAI/api/instructions/config-management
```

Details: https://localai.io/docs/api/api-discovery/

## Errors

| Status | Meaning | Do this |
|---|---|---|
| 401 | No key, or a wrong key | Send the API key. |
| 403 | This key may not use the feature or the model | Ask an administrator. |
| 404 | The model or the path does not exist | List the models and use a name from the list. |
| 429 | The server is busy or the quota is used up | Wait, then try once more. |
| 500 | The model failed to load or to answer | Read the `error.message`. Tell the user. Do not retry in a loop. |

Error codes: https://localai.io/docs/api/api-errors/

## Rules

- Use one model name from `/v1/models`. Do not change the model without telling the user.
- Calls spend the server's compute. Make one call per request of the user.
- Do not send file bytes as base64 in a chat message when an endpoint takes a file.

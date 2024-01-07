# privateGPT

This example is a re-adaptation of https://github.com/imartinez/privateGPT to work with LocalAI and OpenAI endpoints.

- check OPENAI_API_BASE in example.env - this will be copied, to cointainer's .env when building. 

```bash

docker compose up -d --build

#ingest 
docker exec -it python ingest.py

#query
docker exec -it privateGPT python privateGPT.py



```

This is an example `.env` file for LocalAI:

```
PERSIST_DIRECTORY=/app/db

MODEL_TYPE=OpenAI
EMBEDDINGS_MODEL_NAME=all-MiniLM-L6-v2
MODEL_N_CTX=1000
OPENAI_API_BASE=http://localai_api_1:8080/v1
MODEL_NAME=mistral
```

had nice test results using mistral
PRELOAD_MODELS=[{ "url": "github:go-skynet/model-gallery/mistral.yaml", "name": "mistral"}]

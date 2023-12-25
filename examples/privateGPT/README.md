# privateGPT

This example is a re-adaptation of https://github.com/imartinez/privateGPT to work with LocalAI and OpenAI endpoints.

1. clone repo in this folder
2. copy example.env to ./privateGPT/.env
3. copy/overwrite reqirements.txt in privateGPT
3. run docker compose up -d

then use it like this for example: 

### ingest

python ingest.py

### query

python privateGPT.py -S -M

```bash
git clone git@github.com:go-skynet/privateGPT.git
cp example.env ./privateGTP/.env
cp requirements.txt ./privateGPT/requirements.txt

docker compose up -d

docker exec -it privateGPT /bin/bash

//ingest data from source_documents
python ingest.py

//query data about source_documents
python privateGPT -S -M
```

This is an example `.env` file for LocalAI:

```
PERSIST_DIRECTORY=/app/db
#MODEL_N_CTX=1000
#MODEL_N_BATCH=8
#TARGET_SOURCE_CHUNKS=4

MODEL_TYPE=OpenAI
EMBEDDINGS_MODEL_NAME=all-MiniLM-L6-v2
MODEL_N_CTX=1000
OPENAI_API_BASE=http://localai_api_1:8080/v1
MODEL_NAME=mistral
```

assuming you have the docker-compose from main folder running, add mistral to your PRELOAD_MODELS
PRELOAD_MODELS=[{ "url": "github:go-skynet/model-gallery/mistral.yaml", "name": "mistral"}]

# Data query example

This example makes use of [Llama-Index](https://gpt-index.readthedocs.io/en/stable/getting_started/installation.html) to enable question answering on a set of documents.

It loosely follows [the quickstart](https://gpt-index.readthedocs.io/en/stable/guides/primer/usage_pattern.html).

Summary of the steps:

- prepare the dataset (and store it into `data`)
- prepare a vector index database to run queries on
- run queries

## Requirements

You will need a training data set. Copy that over `data`.

## Setup

Start the API:

```bash
# Clone LocalAI
git clone https://github.com/go-skynet/LocalAI

cd LocalAI/examples/query_data

wget https://huggingface.co/skeskinen/ggml/resolve/main/all-MiniLM-L6-v2/ggml-model-q4_0.bin -O models/bert
wget https://gpt4all.io/models/ggml-gpt4all-j.bin -O models/ggml-gpt4all-j

# start with docker-compose
docker-compose up -d --build
```

### Create a storage

In this step we will create a local vector database from our document set, so later we can ask questions on it with the LLM.

Note: **OPENAI_API_KEY** is not required. However the library might fail if no API_KEY is passed by, so an arbitrary string can be used.

```bash
export OPENAI_API_BASE=http://localhost:8080/v1
export OPENAI_API_KEY=sk-

python store.py
```

After it finishes, a directory "storage" will be created with the vector index database.

## Query

We can now query the dataset. 

```bash
export OPENAI_API_BASE=http://localhost:8080/v1
export OPENAI_API_KEY=sk-

python query.py
```

## Update

To update our vector database, run `update.py`

```bash
export OPENAI_API_BASE=http://localhost:8080/v1
export OPENAI_API_KEY=sk-

python update.py
```
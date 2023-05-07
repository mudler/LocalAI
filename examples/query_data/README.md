# Data query example

This example makes use of [Llama-Index](https://gpt-index.readthedocs.io/en/stable/getting_started/installation.html) to enable question answering on a set of documents.

It loosely follows [the quickstart](https://gpt-index.readthedocs.io/en/stable/guides/primer/usage_pattern.html).

Summary of the steps:

- prepare the dataset (and store it into `data`)
- prepare a vector index database to run queries on
- run queries

## Requirements

For this in order to work, you will need LocalAI and a model compatible with the `llama.cpp` backend. This is will not work with gpt4all, however you can mix models (use a llama.cpp one to build the index database, and gpt4all to query it).

The example uses `WizardLM` for both embeddings and Q&A. Edit the config files in `models/` accordingly to specify the model you use (change `HERE` in the configuration files).

You will also need a training data set. Copy that over `data`.

## Setup

Start the API:

```bash
# Clone LocalAI
git clone https://github.com/go-skynet/LocalAI

cd LocalAI/examples/query_data

# Copy your models, edit config files accordingly

# start with docker-compose
docker-compose up -d --build
```

### Create a storage

In this step we will create a local vector database from our document set, so later we can ask questions on it with the LLM.

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
# Data query example

This example makes use of [Llama-Index](https://gpt-index.readthedocs.io/en/stable/getting_started/installation.html) to enable question answering on a set of documents.

It loosely follows [the quickstart](https://gpt-index.readthedocs.io/en/stable/guides/primer/usage_pattern.html).

## Requirements

For this in order to work, you will need a model compatible with the `llama.cpp` backend. This is will not work with gpt4all.

The example uses `WizardLM`. Edit the config files in `models/` accordingly to specify the model you use (change `HERE`).

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

### Create a storage:

```bash
export OPENAI_API_BASE=http://localhost:8080/v1
export OPENAI_API_KEY=sk-

python store.py
```

After it finishes, a directory "storage" will be created with the vector index database.

## Query

```bash
export OPENAI_API_BASE=http://localhost:8080/v1
export OPENAI_API_KEY=sk-

python query.py
```
## Advanced configuration

This section contains examples on how to install models manually with config files.

### Prerequisites

First clone LocalAI:

```bash
git clone https://github.com/go-skynet/LocalAI

cd LocalAI
```

Setup the model you prefer from the examples below and then start LocalAI:

```bash
docker compose up -d --pull always
```

If LocalAI is already started, you can restart it with 

```bash
docker compose restart
```

See also the getting started: https://localai.io/basics/getting_started/

### Mistral

To setup mistral copy the files inside `mistral` in the `models` folder:

```bash
cp -r examples/configurations/mistral/* models/
```

Now download the model:

```bash
wget https://huggingface.co/TheBloke/Mistral-7B-OpenOrca-GGUF/resolve/main/mistral-7b-openorca.Q6_K.gguf -O models/mistral-7b-openorca.Q6_K.gguf
```


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

You can also start LocalAI just with docker:

```
docker run -p 8080:8080 -v $PWD/models:/models -ti --rm quay.io/go-skynet/local-ai:master --models-path /models --threads 4
```

### Mistral

To setup mistral copy the files inside `mistral` in the `models` folder:

```bash
cp -r examples/configurations/mistral/* models/
```

Now download the model:

```bash
wget https://huggingface.co/TheBloke/Mistral-7B-OpenOrca-GGUF/resolve/main/mistral-7b-openorca.Q6_K.gguf -O models/mistral-7b-openorca.Q6_K.gguf
```

### LLaVA

![llava](https://github.com/mudler/LocalAI/assets/2420543/cb0a0897-3b58-4350-af66-e6f4387b58d3)

#### Setup

```
cp -r examples/configurations/llava/* models/
wget https://huggingface.co/mys/ggml_bakllava-1/resolve/main/ggml-model-q4_k.gguf -O models/ggml-model-q4_k.gguf
wget https://huggingface.co/mys/ggml_bakllava-1/resolve/main/mmproj-model-f16.gguf -O models/mmproj-model-f16.gguf
```

#### Try it out

```
curl http://localhost:8080/v1/chat/completions -H "Content-Type: application/json" -d '{
     "model": "llava",
     "messages": [{"role": "user", "content": [{"type":"text", "text": "What is in the image?"}, {"type": "image_url", "image_url": {"url": "https://upload.wikimedia.org/wikipedia/commons/thumb/d/dd/Gfp-wisconsin-madison-the-nature-boardwalk.jpg/2560px-Gfp-wisconsin-madison-the-nature-boardwalk.jpg" }}], "temperature": 0.9}]}'

```

### Phi-2

```
cp -r examples/configurations/phi-2.yaml models/

curl http://localhost:8080/v1/chat/completions -H "Content-Type: application/json" -d '{
     "model": "phi-2",
     "messages": [{"role": "user", "content": "How are you doing?", "temperature": 0.1}]
}'
```

### Mixtral

```
cp -r examples/configuration/mixtral/* models/
wget https://huggingface.co/TheBloke/Mixtral-8x7B-Instruct-v0.1-GGUF/resolve/main/mixtral-8x7b-instruct-v0.1.Q2_K.gguf -O models/mixtral-8x7b-instruct-v0.1.Q2_K.gguf
```

#### Test it out

```
curl http://localhost:8080/v1/completions -H "Content-Type: application/json" -d '{
     "model": "mixtral",
     "prompt": "How fast is light?",                                                                                    
     "temperature": 0.1 }'
```

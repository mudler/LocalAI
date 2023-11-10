![llava](https://github.com/mudler/LocalAI/assets/2420543/cb0a0897-3b58-4350-af66-e6f4387b58d3)

## Setup

```
mkdir models
wget https://huggingface.co/mys/ggml_bakllava-1/resolve/main/ggml-model-q4_k.gguf -O models/ggml-model-q4_k.gguf
wget https://huggingface.co/mys/ggml_bakllava-1/resolve/main/mmproj-model-f16.gguf -O models/mmproj-model-f16.gguf
docker run -p 8080:8080 -v $PWD/models:/models -ti --rm quay.io/go-skynet/local-ai:master --models-path /models --threads 4
```

## Try it out

```
curl http://localhost:8080/v1/chat/completions -H "Content-Type: application/json" -d '{
     "model": "llava",
     "messages": [{"role": "user", "content": [{"type":"text", "text": "What is in the image?"}, {"type": "image_url", "image_url": {"url": "https://upload.wikimedia.org/wikipedia/commons/thumb/d/dd/Gfp-wisconsin-madison-the-nature-boardwalk.jpg/2560px-Gfp-wisconsin-madison-the-nature-boardwalk.jpg" }}], "temperature": 0.9}]}'
```
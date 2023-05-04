# localai-webui

Example of integration with [dhruvgera/localai-frontend](https://github.com/Dhruvgera/LocalAI-frontend).

![image](https://user-images.githubusercontent.com/42107491/235344183-44b5967d-ba22-4331-804c-8da7004a5d35.png)

## Setup

```bash
# Clone LocalAI
git clone https://github.com/go-skynet/LocalAI

cd LocalAI/examples/localai-webui

# (optional) Checkout a specific LocalAI tag
# git checkout -b build <TAG>

# Download any desired models to models/ in the parent LocalAI project dir
# For example: wget https://gpt4all.io/models/ggml-gpt4all-j.bin

# start with docker-compose
docker-compose up -d --build
```

Open http://localhost:3000 for the Web UI.


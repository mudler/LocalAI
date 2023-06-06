# chatbot-ui

Example of integration with [mckaywrigley/chatbot-ui](https://github.com/mckaywrigley/chatbot-ui).

![Screenshot from 2023-04-26 23-59-55](https://user-images.githubusercontent.com/2420543/234715439-98d12e03-d3ce-4f94-ab54-2b256808e05e.png)

## Setup

```bash
# Clone LocalAI
git clone https://github.com/go-skynet/LocalAI

cd LocalAI/examples/chatbot-ui

# (optional) Checkout a specific LocalAI tag
# git checkout -b build <TAG>

# Download gpt4all-j to models/
wget https://gpt4all.io/models/ggml-gpt4all-j.bin -O models/ggml-gpt4all-j

# start with docker-compose
docker-compose up -d --pull always
# or you can build the images with:
# docker-compose up -d --build
```

## Pointing chatbot-ui to a separately managed LocalAI service

If you want to use the [chatbot-ui example](https://github.com/go-skynet/LocalAI/tree/master/examples/chatbot-ui) with an externally managed LocalAI service, you can alter the `docker-compose` file so that it looks like the below. You will notice the file is smaller, because we have removed the section that would normally start the LocalAI service. Take care to update the IP address (or FQDN) that the chatbot-ui service tries to access (marked `<<LOCALAI_IP>>` below):
```
version: '3.6'

services:
  chatgpt:
    image: ghcr.io/mckaywrigley/chatbot-ui:main
    ports:
      - 3000:3000
    environment:
      - 'OPENAI_API_KEY=sk-XXXXXXXXXXXXXXXXXXXX'
      - 'OPENAI_API_HOST=http://<<LOCALAI_IP>>:8080'
```

Once you've edited the Dockerfile, you can start it with `docker compose up`, then browse to `http://localhost:3000`.

## Accessing chatbot-ui

Open http://localhost:3000 for the Web UI.


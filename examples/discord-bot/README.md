# discord-bot

## Setup

```bash
# Clone LocalAI
git clone https://github.com/go-skynet/LocalAI

cd LocalAI/examples/discord-bot

# (optional) Checkout a specific LocalAI tag
# git checkout -b build <TAG>

# Download gpt4all-j to models/
wget https://gpt4all.io/models/ggml-gpt4all-j.bin -O models/ggml-gpt4all-j

# Set the discord bot options (see: https://github.com/go-skynet/gpt-discord-bot#setup)
cp -rfv .env.example .env
vim .env

# start with docker-compose
docker-compose up -d --build
```

Note: see setup options here: https://github.com/go-skynet/gpt-discord-bot#setup

Open up the URL in the console and give permission to the bot in your server. Start a thread with `/chat ..`

## Kubernetes

- install the local-ai chart first
- change OPENAI_API_BASE to point to the API address and apply the discord-bot manifest:

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: discord-bot
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: localai
  namespace: discord-bot
  labels:
    app: localai
spec:
  selector:
    matchLabels:
      app: localai
  replicas: 1
  template:
    metadata:
      labels:
        app: localai
      name: localai
    spec:
      containers:
        - name: localai-discord
          env:
          - name: OPENAI_API_KEY
            value: "x"
          - name: DISCORD_BOT_TOKEN
            value: ""
          - name: DISCORD_CLIENT_ID
            value: ""
          - name: OPENAI_API_BASE
            value: "http://local-ai.default.svc.cluster.local:8080"
          - name: ALLOWED_SERVER_IDS
            value: "xx"
          - name: SERVER_TO_MODERATION_CHANNEL
            value: "1:1"
          image: quay.io/go-skynet/gpt-discord-bot:main
```
# Slack bot

Slackbot using: https://github.com/seratch/ChatGPT-in-Slack

## Setup

```bash
# Clone LocalAI
git clone https://github.com/go-skynet/LocalAI

cd LocalAI/examples/slack-bot

git clone https://github.com/seratch/ChatGPT-in-Slack

# (optional) Checkout a specific LocalAI tag
# git checkout -b build <TAG>

# Download gpt4all-j to models/
wget https://gpt4all.io/models/ggml-gpt4all-j.bin -O models/ggml-gpt4all-j

# Set the discord bot options (see: https://github.com/seratch/ChatGPT-in-Slack)
cp -rfv .env.example .env
vim .env

# start with docker-compose
docker-compose up -d --build
```
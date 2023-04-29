# discord-bot

## Setup

```bash
# Clone LocalAI
git clone https://github.com/go-skynet/LocalAI

cd LocalAI/examples/discord-bot

git clone https://github.com/go-skynet/gpt-discord-bot.git

# (optional) Checkout a specific LocalAI tag
# git checkout -b build <TAG>

# Download gpt4all-j to models/
wget https://gpt4all.io/models/ggml-gpt4all-j.bin -O models/ggml-gpt4all-j

# Set the discord bot options
cp -rfv .env.example .env
vim .env

# start with docker-compose
docker-compose up -d --build
```

Open up the URL in the console and give permission to the bot in your server. Start a thread with `/chat ..`


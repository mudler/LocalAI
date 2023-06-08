## Telegram bot

![Screenshot from 2023-06-09 00-36-26](https://github.com/go-skynet/LocalAI/assets/2420543/e98b4305-fa2d-41cf-9d2f-1bb2d75ca902)

This example uses a fork of [chatgpt-telegram-bot](https://github.com/karfly/chatgpt_telegram_bot) to deploy a telegram bot with LocalAI instead of OpenAI.

```bash
# Clone LocalAI
git clone https://github.com/go-skynet/LocalAI

cd LocalAI/examples/telegram-bot

git clone https://github.com/mudler/chatgpt_telegram_bot

cp -rf docker-compose.yml chatgpt_telegram_bot

cd chatgpt_telegram_bot

mv config/config.example.yml config/config.yml
mv config/config.example.env config/config.env

# Edit config/config.yml to set the telegram bot token
vim config/config.yml

# run the bot
docker-compose --env-file config/config.env up --build
```

Note: LocalAI is configured to download `gpt4all-j` in place of `gpt-3.5-turbo` and `stablediffusion` for image generation at the first start. Download size is >6GB, if your network connection is slow, adapt the `docker-compose.yml` file healthcheck section accordingly (replace `20m`, for instance with `1h`, etc.). 
To configure models manually, comment the `PRELOAD_MODELS` environment variable in the `docker-compose.yml` file and see for instance the [chatbot-ui-manual example](https://github.com/go-skynet/LocalAI/tree/master/examples/chatbot-ui-manual) `model` directory.
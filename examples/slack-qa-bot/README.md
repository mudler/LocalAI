## Slack QA Bot 

This example uses https://github.com/spectrocloud-labs/Slack-QA-bot to deploy a slack bot that can answer to your documentation!

- Create a new Slack app using the manifest-dev.yml file
- Install the app into your Slack workspace
- Retrieve your slack keys and edit `.env`
- Start the app

```bash
# Clone LocalAI
git clone https://github.com/go-skynet/LocalAI

cd LocalAI/examples/slack-qa-bot

cp -rfv .env.example .env

# Edit .env and add slackbot api keys, or repository settings to scan
vim .env

# run the bot
docker-compose up
```

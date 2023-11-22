
+++
disableToc = false
title = "Flowise"
weight = 2
+++

Build LLM Apps Easily

![Flowise](https://raw.githubusercontent.com/FlowiseAI/Flowise/main/images/flowise.png)

Github Link - https://github.com/FlowiseAI/Flowise

## ⚡Local Install

Download and Install [NodeJS](https://nodejs.org/en/download) >= 18.15.0

1. Install Flowise
    ```bash
    npm install -g flowise
    ```
2. Start Flowise

    ```bash
    npx flowise start
    ```

3. Open [http://localhost:3000](http://localhost:3000)

## 🐳 Docker

### Docker Compose

1. Go to `docker` folder at the root of the project
2. Copy `.env.example` file, paste it into the same location, and rename to `.env`
3. `docker-compose up -d`
4. Open [http://localhost:3000](http://localhost:3000)
5. You can bring the containers down by `docker-compose stop --rmi all`

### Docker Compose (Flowise + LocalAI)

1. In a command line Run ``git clone https://github.com/go-skynet/LocalAI``
2. Then run ``cd LocalAI/examples/flowise``
3. Then run ``docker-compose up -d --pull always``
4. Open [http://localhost:3000](http://localhost:3000)
5. You can bring the containers down by `docker-compose stop --rmi all`

## 🌱 Env Variables

Flowise support different environment variables to configure your instance. You can specify the following variables in the `.env` file inside `packages/server` folder. Read [more](https://github.com/FlowiseAI/Flowise/blob/main/CONTRIBUTING.md#-env-variables)

## 📖 Documentation

[Flowise Docs](https://docs.flowiseai.com/)

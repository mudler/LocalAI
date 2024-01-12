
+++
disableToc = false
title = "AnythingLLM"
description="Integrate your LocalAI LLM and embedding models into AnythingLLM by Mintplex Labs"
weight = 2
+++

AnythingLLM is an open source ChatGPT equivalent tool for chatting with documents and more in a secure environment by [Mintplex Labs Inc](https://github.com/Mintplex-Labs).

![image](https://github.com/Mintplex-Labs/anything-llm/raw/master/images/screenshots/chatting.gif)

⭐ Star on Github - https://github.com/Mintplex-Labs/anything-llm

* Chat with your LocalAI models (or hosted models like OpenAi, Anthropic, and Azure)
* Embed documents (txt, pdf, json, and more) using your LocalAI Sentence Transformers
* Select any vector database you want (Chroma, Pinecone, qDrant, Weaviate ) or use the embedded on-instance vector database (LanceDB)
* Supports single or multi-user tenancy with built-in permissions
* Full developer API
* Locally running SQLite db for minimal setup.

AnythingLLM is a fully transparent tool to deliver a customized, white-label ChatGPT equivalent experience using only the models and services you or your organization are comfortable using.

### Why AnythingLLM?

AnythingLLM aims to enable you to quickly and comfortably get a ChatGPT equivalent experience using your proprietary documents for your organization with zero compromise on security or comfort.

### What does AnythingLLM include?
- Full UI
- Full admin console and panel for managing users, chats, model selection, vector db connection, and embedder selection
- Multi-user support and logins
- Supports both desktop and mobile view ports
- Built in vector database where no data leaves your instance at all
- Docker support

## Install

### Local via docker

Running via docker and integrating with your LocalAI instance is a breeze.

First, pull in the latest AnythingLLM Docker image
`docker pull mintplexlabs/anythingllm:master`

Next, run the image on a container exposing port `3001`.
`docker run -d -p 3001:3001 mintplexlabs/anythingllm:master`

Now open `http://localhost:3001` and you will start on-boarding for setting up your AnythingLLM instance to your comfort level


## Integration with your LocalAI instance.

There are two areas where you can leverage your models loaded into LocalAI - LLM and Embedding. Any LLM models should be ready to run a chat completion.

### LLM model selection

During onboarding and from the sidebar setting you can select `LocalAI` as your LLM. Here you can set both the model and token limit of the specific model. The dropdown will automatically populate once your url is set.

The URL should look like `http://localhost:8000/v1` or wherever your LocalAI instance is being served from. Non-localhost URLs are permitted if hosting LocalAI on cloud services.

![localai-setup](https://github.com/Mintplex-Labs/anything-llm/raw/master/images/LLMproviders/localai-setup.png)


### LLM embedding model selection

During onboarding and from the sidebar setting you can select `LocalAI` as your preferred embedding engine. This model will be the model used when you upload any kind of document via AnythingLLM. Here you can set the model from available models via the LocalAI API. The dropdown will automatically populate once your url is set.

The URL should look like `http://localhost:8000/v1` or wherever your LocalAI instance is being served from. Non-localhost URLs are permitted if hosting LocalAI on cloud services.

![localai-setup](https://github.com/Mintplex-Labs/anything-llm/raw/master/images/LLMproviders/localai-embedding.png)
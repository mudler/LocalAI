# langchain

Example of using langchain, with the standard OpenAI llm module, and LocalAI. Has docker compose profiles for both the Typescript and Python versions.

**Please Note** - This is a tech demo example at this time. ggml-gpt4all-j has pretty terrible results for most langchain applications with the settings used in this example.

## Setup

```bash
# Clone LocalAI
git clone https://github.com/go-skynet/LocalAI

cd LocalAI/examples/langchain

# (optional) - Edit the example code in typescript.
# vi ./langchainjs-localai-example/index.ts

# Download gpt4all-j to models/
wget https://gpt4all.io/models/ggml-gpt4all-j.bin -O models/ggml-gpt4all-j

# start with docker-compose for typescript!
docker-compose --profile ts up --build

# or start with docker-compose for python!
docker-compose --profile py up --build
```

## Copyright

Some of the example code in index.mts and full_demo.py is adapted from the langchainjs project and is Copyright (c) Harrison Chase. Used under the terms of the MIT license, as is the remainder of this code.
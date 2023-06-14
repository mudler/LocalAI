# flowise

Example of integration with [FlowiseAI/Flowise](https://github.com/FlowiseAI/Flowise).

![Screenshot from 2023-05-30 18-01-03](https://github.com/go-skynet/LocalAI/assets/2420543/02458782-0549-4131-971c-95ee56ec1af8)

You can check a demo video in the Flowise PR: https://github.com/FlowiseAI/Flowise/pull/123

## Run

In this example LocalAI will download the gpt4all model and set it up as "gpt-3.5-turbo". See the `docker-compose.yaml`
```bash
# Clone LocalAI
git clone https://github.com/go-skynet/LocalAI

cd LocalAI/examples/flowise

# start with docker-compose
docker-compose up --pull always

```

## Accessing flowise

Open http://localhost:3000.

## Using LocalAI

Search for LocalAI in the integration, and use the `http://api:8080/` as URL.


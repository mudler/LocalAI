
+++
disableToc = false
title = "BionicGPT"
weight = 2
+++

an on-premise replacement for ChatGPT, offering the advantages of Generative AI while maintaining strict data confidentiality, BionicGPT can run on your laptop or scale into the data center. 

![](https://raw.githubusercontent.com/purton-tech/bionicgpt/main/website/static/github-readme.png)

BionicGPT Homepage - https://bionic-gpt.com
Github link - https://github.com/purton-tech/bionicgpt

<!-- Try it out -->
## Try it out
Cut and paste the following into a `docker-compose.yaml` file and run `docker-compose up -d` access the user interface on http://localhost:7800/auth/sign_up
This has been tested on an AMD 2700x with 16GB of ram. The included `ggml-gpt4all-j` model runs on CPU only.
**Warning** - The images in this `docker-compose` are large due to having the model weights pre-loaded for convenience.

```yaml
services:

  # LocalAI with pre-loaded ggml-gpt4all-j
  local-ai:
    image: ghcr.io/purton-tech/bionicgpt-model-api:llama-2-7b-chat

  # Handles parsing of multiple documents types.
  unstructured:
    image: downloads.unstructured.io/unstructured-io/unstructured-api:db264d8
    ports:
      - "8000:8000"

  # Handles routing between the application, barricade and the LLM API
  envoy:
    image: ghcr.io/purton-tech/bionicgpt-envoy:1.1.10
    ports:
      - "7800:7700"

  # Postgres pre-loaded with pgVector
  db:
    image: ankane/pgvector
    environment:
      POSTGRES_PASSWORD: testpassword
      POSTGRES_USER: postgres
      POSTGRES_DB: finetuna
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U postgres"]
      interval: 10s
      timeout: 5s
      retries: 5

  # Sets up our database tables
  migrations:
    image: ghcr.io/purton-tech/bionicgpt-db-migrations:1.1.10
    environment:
      DATABASE_URL: postgresql://postgres:testpassword@db:5432/postgres?sslmode=disable
    depends_on:
      db:
        condition: service_healthy

  # Barricade handles all /auth routes for user sign up and sign in.
  barricade:
    image: purtontech/barricade
    environment:
        # This secret key is used to encrypt cookies.
        SECRET_KEY: 190a5bf4b3cbb6c0991967ab1c48ab30790af876720f1835cbbf3820f4f5d949
        DATABASE_URL: postgresql://postgres:testpassword@db:5432/postgres?sslmode=disable
        FORWARD_URL: app
        FORWARD_PORT: 7703
        REDIRECT_URL: /app/post_registration
    depends_on:
      db:
        condition: service_healthy
      migrations:
        condition: service_completed_successfully
  
  # Our axum server delivering our user interface
  embeddings-job:
    image: ghcr.io/purton-tech/bionicgpt-embeddings-job:1.1.10
    environment:
      APP_DATABASE_URL: postgresql://ft_application:testpassword@db:5432/postgres?sslmode=disable
    depends_on:
      db:
        condition: service_healthy
      migrations:
        condition: service_completed_successfully
  
  # Our axum server delivering our user interface
  app:
    image: ghcr.io/purton-tech/bionicgpt:1.1.10
    environment:
      APP_DATABASE_URL: postgresql://ft_application:testpassword@db:5432/postgres?sslmode=disable
    depends_on:
      db:
        condition: service_healthy
      migrations:
        condition: service_completed_successfully
```

## Kubernetes Ready

BionicGPT is optimized to run on Kubernetes and implements the full pipeline of LLM fine tuning from data acquisition to user interface.

---
title: "⚙️ Backends"
description: "Learn how to use, manage, and develop backends in LocalAI"
weight: 4
url: "/backends/"
---


LocalAI supports a variety of backends that can be used to run different types of AI models. There are core Backends which are included, and there are containerized applications that provide the runtime environment for specific model types, such as LLMs, diffusion models, or text-to-speech models.

## Managing Backends in the UI

The LocalAI web interface provides an intuitive way to manage your backends:

1. Navigate to the "Backends" section in the navigation menu
2. Browse available backends from configured galleries
3. Use the search bar to find specific backends by name, description, or type
4. Filter backends by type using the quick filter buttons (LLM, Diffusion, TTS, Whisper)
5. Install or delete backends with a single click
6. Monitor installation progress in real-time

Each backend card displays:
- Backend name and description
- Type of models it supports
- Installation status
- Action buttons (Install/Delete)
- Additional information via the info button

## Backend Galleries

Backend galleries are repositories that contain backend definitions. They work similarly to model galleries but are specifically for backends.

### Adding a Backend Gallery

You can add backend galleries by specifying the **Environment Variable**  `LOCALAI_BACKEND_GALLERIES`:

```bash
export LOCALAI_BACKEND_GALLERIES='[{"name":"my-gallery","url":"https://raw.githubusercontent.com/username/repo/main/backends"}]'
```
The URL needs to point to a valid yaml file, for example:

```yaml
- name: "test-backend"
  uri: "quay.io/image/tests:localai-backend-test"
  alias: "foo-backend"
```

Where URI is the path to an OCI container image.

### Backend Gallery Structure

A backend gallery is a collection of YAML files, each defining a backend. Here's an example structure:

```yaml
name: "llm-backend"
description: "A backend for running LLM models"
uri: "quay.io/username/llm-backend:latest"
alias: "llm"
tags:
  - "llm"
  - "text-generation"
```

## Pre-installing Backends

You can pre-install backends when starting LocalAI using the `LOCALAI_EXTERNAL_BACKENDS` environment variable:

```bash
export LOCALAI_EXTERNAL_BACKENDS="llm-backend,diffusion-backend"
local-ai run
```

## Creating a Backend

To create a new backend, you need to:

1. Create a container image that implements the LocalAI backend interface
2. Define a backend YAML file
3. Publish your backend to a container registry

### Backend Container Requirements

Your backend container should:

1. Implement the LocalAI backend interface (gRPC or HTTP)
2. Handle model loading and inference
3. Support the required model types
4. Include necessary dependencies
5. Have a top level `run.sh` file that will be used to run the backend
6. Pushed to a registry so can be used in a gallery

### Getting started

For getting started, see the available backends in LocalAI here: https://github.com/mudler/LocalAI/tree/master/backend . 

- For Python based backends there is a template that can be used as starting point: https://github.com/mudler/LocalAI/tree/master/backend/python/common/template . 
- For Golang based backends, you can see the `bark-cpp` backend as an example: https://github.com/mudler/LocalAI/tree/master/backend/go/bark-cpp
- For C++ based backends, you can see the `llama-cpp` backend as an example: https://github.com/mudler/LocalAI/tree/master/backend/cpp/llama-cpp

### Publishing Your Backend

1. Build your container image:
   ```bash
   docker build -t quay.io/username/my-backend:latest .
   ```

2. Push to a container registry:
   ```bash
   docker push quay.io/username/my-backend:latest
   ```

3. Add your backend to a gallery:
   - Create a YAML entry in your gallery repository
   - Include the backend definition
   - Make the gallery accessible via HTTP/HTTPS

## Backend Types

LocalAI supports various types of backends:

- **LLM Backends**: For running language models
- **Diffusion Backends**: For image generation
- **TTS Backends**: For text-to-speech conversion
- **Whisper Backends**: For speech-to-text conversion

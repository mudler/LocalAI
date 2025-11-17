+++
disableToc = false
title = "Integration Examples"
weight = 5
icon = "sync"
description = "Learn how to integrate LocalAI with popular frameworks and tools"
+++

This tutorial shows you how to integrate LocalAI with popular AI frameworks and tools. LocalAI's OpenAI-compatible API makes it easy to use as a drop-in replacement.

## Prerequisites

- LocalAI running and accessible
- Basic knowledge of the framework you want to integrate
- Python, Node.js, or other runtime as needed

## Python Integrations

### LangChain

LangChain has built-in support for LocalAI:

```python
from langchain.llms import OpenAI
from langchain.chat_models import ChatOpenAI

# For chat models
llm = ChatOpenAI(
    openai_api_key="not-needed",
    openai_api_base="http://localhost:8080/v1",
    model_name="gpt-4"
)

response = llm.predict("Hello, how are you?")
print(response)
```

### OpenAI Python SDK

The official OpenAI Python SDK works directly with LocalAI:

```python
import openai

openai.api_base = "http://localhost:8080/v1"
openai.api_key = "not-needed"

response = openai.ChatCompletion.create(
    model="gpt-4",
    messages=[
        {"role": "user", "content": "Hello!"}
    ]
)

print(response.choices[0].message.content)
```

### LangChain with LocalAI Functions

```python
from langchain.agents import initialize_agent, Tool
from langchain.llms import OpenAI

llm = OpenAI(
    openai_api_key="not-needed",
    openai_api_base="http://localhost:8080/v1"
)

tools = [
    Tool(
        name="Calculator",
        func=lambda x: eval(x),
        description="Useful for mathematical calculations"
    )
]

agent = initialize_agent(tools, llm, agent="zero-shot-react-description")
result = agent.run("What is 25 * 4?")
```

## JavaScript/TypeScript Integrations

### OpenAI Node.js SDK

```javascript
import OpenAI from 'openai';

const openai = new OpenAI({
  baseURL: 'http://localhost:8080/v1',
  apiKey: 'not-needed',
});

async function main() {
  const completion = await openai.chat.completions.create({
    model: 'gpt-4',
    messages: [{ role: 'user', content: 'Hello!' }],
  });

  console.log(completion.choices[0].message.content);
}

main();
```

### LangChain.js

```javascript
import { ChatOpenAI } from "langchain/chat_models/openai";

const model = new ChatOpenAI({
  openAIApiKey: "not-needed",
  configuration: {
    baseURL: "http://localhost:8080/v1",
  },
  modelName: "gpt-4",
});

const response = await model.invoke("Hello, how are you?");
console.log(response.content);
```

## Integration with Specific Tools

### AutoGPT

AutoGPT can use LocalAI by setting the API base URL:

```bash
export OPENAI_API_BASE=http://localhost:8080/v1
export OPENAI_API_KEY=not-needed
```

Then run AutoGPT normally.

### Flowise

Flowise supports LocalAI out of the box. In the Flowise UI:

1. Add a ChatOpenAI node
2. Set the base URL to `http://localhost:8080/v1`
3. Set API key to any value (or leave empty)
4. Select your model

### Continue (VS Code Extension)

Configure Continue to use LocalAI:

```json
{
  "models": [
    {
      "title": "LocalAI",
      "provider": "openai",
      "model": "gpt-4",
      "apiBase": "http://localhost:8080/v1",
      "apiKey": "not-needed"
    }
  ]
}
```

### AnythingLLM

AnythingLLM has native LocalAI support:

1. Go to Settings > LLM Preference
2. Select "LocalAI"
3. Enter your LocalAI endpoint: `http://localhost:8080`
4. Select your model

## REST API Examples

### cURL

```bash
# Chat completion
curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4",
    "messages": [{"role": "user", "content": "Hello!"}]
  }'

# List models
curl http://localhost:8080/v1/models

# Embeddings
curl http://localhost:8080/v1/embeddings \
  -H "Content-Type: application/json" \
  -d '{
    "model": "text-embedding-ada-002",
    "input": "Hello world"
  }'
```

### Python Requests

```python
import requests

response = requests.post(
    "http://localhost:8080/v1/chat/completions",
    json={
        "model": "gpt-4",
        "messages": [{"role": "user", "content": "Hello!"}]
    }
)

print(response.json())
```

## Advanced Integrations

### Custom Wrapper

Create a custom wrapper for your application:

```python
class LocalAIClient:
    def __init__(self, base_url="http://localhost:8080/v1"):
        self.base_url = base_url
        self.api_key = "not-needed"
    
    def chat(self, messages, model="gpt-4", **kwargs):
        response = requests.post(
            f"{self.base_url}/chat/completions",
            json={
                "model": model,
                "messages": messages,
                **kwargs
            },
            headers={"Authorization": f"Bearer {self.api_key}"}
        )
        return response.json()
    
    def embeddings(self, text, model="text-embedding-ada-002"):
        response = requests.post(
            f"{self.base_url}/embeddings",
            json={
                "model": model,
                "input": text
            }
        )
        return response.json()
```

### Streaming Responses

```python
import requests
import json

def stream_chat(messages, model="gpt-4"):
    response = requests.post(
        "http://localhost:8080/v1/chat/completions",
        json={
            "model": model,
            "messages": messages,
            "stream": True
        },
        stream=True
    )
    
    for line in response.iter_lines():
        if line:
            data = json.loads(line.decode('utf-8').replace('data: ', ''))
            if 'choices' in data:
                content = data['choices'][0].get('delta', {}).get('content', '')
                if content:
                    yield content
```

## Common Integration Patterns

### Error Handling

```python
import requests
from requests.exceptions import RequestException

def safe_chat_request(messages, model="gpt-4", retries=3):
    for attempt in range(retries):
        try:
            response = requests.post(
                "http://localhost:8080/v1/chat/completions",
                json={"model": model, "messages": messages},
                timeout=30
            )
            response.raise_for_status()
            return response.json()
        except RequestException as e:
            if attempt == retries - 1:
                raise
            time.sleep(2 ** attempt)  # Exponential backoff
```

### Rate Limiting

```python
from functools import wraps
import time

def rate_limit(calls_per_second=2):
    min_interval = 1.0 / calls_per_second
    last_called = [0.0]
    
    def decorator(func):
        @wraps(func)
        def wrapper(*args, **kwargs):
            elapsed = time.time() - last_called[0]
            left_to_wait = min_interval - elapsed
            if left_to_wait > 0:
                time.sleep(left_to_wait)
            ret = func(*args, **kwargs)
            last_called[0] = time.time()
            return ret
        return wrapper
    return decorator

@rate_limit(calls_per_second=2)
def chat_request(messages):
    # Your chat request here
    pass
```

## Testing Integrations

### Unit Tests

```python
import unittest
from unittest.mock import patch, Mock
import requests

class TestLocalAIIntegration(unittest.TestCase):
    @patch('requests.post')
    def test_chat_completion(self, mock_post):
        mock_response = Mock()
        mock_response.json.return_value = {
            "choices": [{
                "message": {"content": "Hello!"}
            }]
        }
        mock_post.return_value = mock_response
        
        # Your integration code here
        # Assertions
```

## What's Next?

- [API Reference]({{% relref "docs/reference/api-reference" %}}) - Complete API documentation
- [Integrations]({{% relref "docs/integrations" %}}) - List of compatible projects
- [Examples Repository](https://github.com/mudler/LocalAI-examples) - More integration examples

## See Also

- [Features Documentation]({{% relref "docs/features" %}}) - All LocalAI capabilities
- [FAQ]({{% relref "docs/faq" %}}) - Common integration questions
- [Troubleshooting]({{% relref "docs/troubleshooting" %}}) - Integration issues


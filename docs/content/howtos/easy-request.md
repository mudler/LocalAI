
+++
disableToc = false
title = "Easy Request - All"
weight = 2
+++

###Curl Request

Curl Chat API - 

```bash
curl http://localhost:8080/v1/chat/completions -H "Content-Type: application/json" -d '{
     "model": "lunademo",
     "messages": [{"role": "user", "content": "How are you?"}],
     "temperature": 0.9 
   }'
```

###Openai V0

This is for Python, ``OpenAI``=``0.28.1``

OpenAI Chat API Python -

```python
import os
import openai
openai.api_base = "http://localhost:8080/v1"
openai.api_key = "sx-xxx"
OPENAI_API_KEY = "sx-xxx"
os.environ['OPENAI_API_KEY'] = OPENAI_API_KEY

completion = openai.ChatCompletion.create(
  model="lunademo",
  messages=[
    {"role": "system", "content": "You are a helpful assistant."},
    {"role": "user", "content": "How are you?"}
  ]
)

print(completion.choices[0].message.content)
```

OpenAI Completion API Python -

```python
import os
import openai
openai.api_base = "http://localhost:8080/v1"
openai.api_key = "sx-xxx"
OPENAI_API_KEY = "sx-xxx"
os.environ['OPENAI_API_KEY'] = OPENAI_API_KEY

completion = openai.Completion.create(
  model="lunademo",
  prompt="function downloadFile(string url, string outputPath) ",
  max_tokens=256,
  temperature=0.5)

print(completion.choices[0].text)
```


+++
disableToc = false
title = "Easy Request - Openai V0"
weight = 2
+++

This is for Python, ``OpenAI``=``0.28.1``, if you are on ``OpenAI``=>``V1`` please use this [How to]({{%relref "howtos/easy-request-openai-v1" %}})

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
See [OpenAI API](https://platform.openai.com/docs/api-reference) for more info!
Have fun using LocalAI!

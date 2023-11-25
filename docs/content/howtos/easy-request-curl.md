
+++
disableToc = false
title = "Easy Request - Curl"
weight = 2
+++

Now we can make a curl request!

Curl Chat API - 

```bash
curl http://localhost:8080/v1/chat/completions -H "Content-Type: application/json" -d '{
     "model": "lunademo",
     "messages": [{"role": "user", "content": "How are you?"}],
     "temperature": 0.9 
   }'
```

Curl Completion API -

```bash
curl --request POST \
  --url http://localhost:8080/v1/completions \
  --header 'Content-Type: application/json' \
  --data '{
    "model": "lunademo",
    "prompt": "function downloadFile(string url, string outputPath) {",
    "max_tokens": 256,
    "temperature": 0.5
}'
```

See [OpenAI API](https://platform.openai.com/docs/api-reference) for more info!
Have fun using LocalAI!

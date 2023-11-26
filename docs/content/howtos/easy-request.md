
+++
disableToc = false
title = "Easy Request - All"
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


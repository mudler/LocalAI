+++
disableToc = false
title = "Easy Setup - Embeddings"
weight = 2
+++

To install an embedding model, run the following command

```bash
curl http://localhost:8080/models/apply -H "Content-Type: application/json" -d '{
     "id": "model-gallery@bert-embeddings"
   }'  
```

Now we need to make a ``bert.yaml`` in the models folder
```yaml
backend: bert-embeddings
embeddings: true
name: text-embedding-ada-002
parameters:
  model: bert
```

**Restart LocalAI after you change a yaml file**

When you would like to request the model from CLI you can do 

```bash
curl http://localhost:8080/v1/embeddings \
  -H "Content-Type: application/json" \
  -d '{
    "input": "The food was delicious and the waiter...",
    "model": "text-embedding-ada-002"
  }'
```

See [OpenAI Embedding](https://platform.openai.com/docs/api-reference/embeddings/object) for more info!

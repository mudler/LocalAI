# privateGPT

This example is a re-adaptation of https://github.com/imartinez/privateGPT to work with LocalAI and OpenAI endpoints. We have a fork with the changes required to work with privateGPT here https://github.com/go-skynet/privateGPT ( PR: https://github.com/imartinez/privateGPT/pull/408 ).

Follow the instructions in https://github.com/go-skynet/privateGPT:

```bash
git clone git@github.com:go-skynet/privateGPT.git
cd privateGPT
pip install -r requirements.txt
```

Rename `example.env` to `.env` and edit the variables appropriately.

This is an example `.env` file for LocalAI:

```
PERSIST_DIRECTORY=db
# Set to OpenAI here
MODEL_TYPE=OpenAI
EMBEDDINGS_MODEL_NAME=all-MiniLM-L6-v2
MODEL_N_CTX=1000
# LocalAI URL
OPENAI_API_BASE=http://localhost:8080/v1
```
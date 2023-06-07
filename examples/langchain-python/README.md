## Langchain-python

Langchain example from [quickstart](https://python.langchain.com/en/latest/getting_started/getting_started.html).

To interact with langchain, you can just set the `OPENAI_API_BASE` URL and provide a token with a random string.

See the example below:

```
# Clone LocalAI
git clone https://github.com/go-skynet/LocalAI

cd LocalAI/examples/langchain-python

# start with docker-compose
docker-compose up --pull always

pip install langchain
pip install openai

export OPENAI_API_BASE=http://localhost:8080
# Note: **OPENAI_API_KEY** is not required. However the library might fail if no API_KEY is passed by, so an arbitrary string can be used.
export OPENAI_API_KEY=sk-

python test.py
# A good company name for a company that makes colorful socks would be "Colorsocks".

python agent.py
```
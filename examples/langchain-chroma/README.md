# Data query example

This example makes use of [langchain and chroma](https://blog.langchain.dev/langchain-chroma/) to enable question answering on a set of documents.

## Setup

Download the models and start the API:

```bash
# Clone LocalAI
git clone https://github.com/go-skynet/LocalAI

cd LocalAI/examples/query_data

wget https://huggingface.co/skeskinen/ggml/resolve/main/all-MiniLM-L6-v2/ggml-model-q4_0.bin -O models/bert
wget https://gpt4all.io/models/ggml-gpt4all-j.bin -O models/ggml-gpt4all-j

# start with docker-compose
docker-compose up -d --build
```

### Python requirements

```
pip install -r requirements.txt
```

### Create a storage

In this step we will create a local vector database from our document set, so later we can ask questions on it with the LLM.

```bash
export OPENAI_API_BASE=http://localhost:8080/v1
export OPENAI_API_KEY=sk-

wget https://raw.githubusercontent.com/hwchase17/chat-your-data/master/state_of_the_union.txt
python store.py
```

After it finishes, a directory "storage" will be created with the vector index database.

## Query

We can now query the dataset. 

```bash
export OPENAI_API_BASE=http://localhost:8080/v1
export OPENAI_API_KEY=sk-

python query.py
# President Trump recently stated during a press conference regarding tax reform legislation that "we're getting rid of all these loopholes." He also mentioned that he wants to simplify the system further through changes such as increasing the standard deduction amount and making other adjustments aimed at reducing taxpayers' overall burden.    
```

Keep in mind now things are hit or miss!
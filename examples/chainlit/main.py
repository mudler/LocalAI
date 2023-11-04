import os

import weaviate
from llama_index.storage.storage_context import StorageContext
from llama_index.vector_stores import WeaviateVectorStore

from llama_index.query_engine.retriever_query_engine import RetrieverQueryEngine
from llama_index.callbacks.base import CallbackManager
from llama_index import (
    LLMPredictor,
    ServiceContext,
    StorageContext,
    VectorStoreIndex,
)
import chainlit as cl

from llama_index.llms import LocalAI
from llama_index.embeddings import HuggingFaceEmbedding
import yaml

# Load the configuration file
with open("config.yaml", "r") as ymlfile:
    cfg = yaml.safe_load(ymlfile)

# Get the values from the configuration file or set the default values
temperature = cfg['localAI'].get('temperature', 0)
model_name = cfg['localAI'].get('modelName', "gpt-3.5-turbo")
api_base = cfg['localAI'].get('apiBase', "http://local-ai.default")
api_key = cfg['localAI'].get('apiKey', "stub")
streaming = cfg['localAI'].get('streaming', True)
weaviate_url = cfg['weviate'].get('url', "http://weviate.default")
index_name = cfg['weviate'].get('index', "AIChroma")
query_mode = cfg['query'].get('mode', "hybrid")
topK = cfg['query'].get('topK', 1)
alpha = cfg['query'].get('alpha', 0.0)
embed_model_name = cfg['embedding'].get('model', "BAAI/bge-small-en-v1.5")
chunk_size = cfg['query'].get('chunkSize', 1024)


embed_model = HuggingFaceEmbedding(model_name=embed_model_name)


llm = LocalAI(temperature=temperature, model_name=model_name, api_base=api_base, api_key=api_key, streaming=streaming)
llm.globally_use_chat_completions = True;
client = weaviate.Client(weaviate_url)
vector_store = WeaviateVectorStore(weaviate_client=client, index_name=index_name)
storage_context = StorageContext.from_defaults(vector_store=vector_store)

@cl.on_chat_start
async def factory():

    llm_predictor = LLMPredictor(
        llm=llm
    )
    
    service_context = ServiceContext.from_defaults(embed_model=embed_model, callback_manager=CallbackManager([cl.LlamaIndexCallbackHandler()]), llm_predictor=llm_predictor, chunk_size=chunk_size)

    index = VectorStoreIndex.from_vector_store(
        vector_store,
        storage_context=storage_context,
        service_context=service_context
    )

    query_engine = index.as_query_engine(vector_store_query_mode=query_mode, similarity_top_k=topK, alpha=alpha, streaming=True)

    cl.user_session.set("query_engine", query_engine)


@cl.on_message
async def main(message: cl.Message):
    query_engine = cl.user_session.get("query_engine")
    response = await cl.make_async(query_engine.query)(message.content)

    response_message = cl.Message(content="")

    for token in response.response_gen:
        await response_message.stream_token(token=token)

    if response.response_txt:
        response_message.content = response.response_txt

    await response_message.send()

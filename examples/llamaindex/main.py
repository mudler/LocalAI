import os

import weaviate

from llama_index import ServiceContext, VectorStoreIndex, StorageContext
from llama_index.llms import LocalAI
from llama_index.vector_stores import WeaviateVectorStore
from llama_index.storage.storage_context import StorageContext

# Weaviate client setup
client = weaviate.Client("http://weviate.default")

# Weaviate vector store setup
vector_store = WeaviateVectorStore(weaviate_client=client, index_name="AIChroma")

# Storage context setup
storage_context = StorageContext.from_defaults(vector_store=vector_store)

# LocalAI setup
llm = LocalAI(temperature=0, model_name="gpt-3.5-turbo", api_base="http://local-ai.default", api_key="stub")
llm.globally_use_chat_completions = True;

# Service context setup
service_context = ServiceContext.from_defaults(llm=llm, embed_model="local")

# Load index from stored vectors
index = VectorStoreIndex.from_vector_store(
    vector_store,
    storage_context=storage_context,
    service_context=service_context
)

# Query engine setup
query_engine = index.as_query_engine(similarity_top_k=1, vector_store_query_mode="hybrid")

# Query example
response = query_engine.query("What is LocalAI?")
print(response)
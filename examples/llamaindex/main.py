import weaviate
from llama_index import ServiceContext, VectorStoreIndex
from llama_index.llms import LOCALAI_DEFAULTS, OpenAILike
from llama_index.vector_stores import WeaviateVectorStore

# Weaviate vector store setup
vector_store = WeaviateVectorStore(
    weaviate_client=weaviate.Client("http://weviate.default"), index_name="AIChroma"
)

# LLM setup, served via LocalAI
llm = OpenAILike(temperature=0, model="gpt-3.5-turbo", **LOCALAI_DEFAULTS)

# Service context setup
service_context = ServiceContext.from_defaults(llm=llm, embed_model="local")

# Load index from stored vectors
index = VectorStoreIndex.from_vector_store(
    vector_store, service_context=service_context
)

# Query engine setup
query_engine = index.as_query_engine(
    similarity_top_k=1, vector_store_query_mode="hybrid"
)

# Query example
response = query_engine.query("What is LocalAI?")
print(response)

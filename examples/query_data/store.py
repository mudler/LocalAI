import os

# Uncomment to specify your OpenAI API key here (local testing only, not in production!), or add corresponding environment variable (recommended)
# os.environ['OPENAI_API_KEY']= ""

from llama_index import GPTVectorStoreIndex, SimpleDirectoryReader, LLMPredictor, PromptHelper, ServiceContext
from langchain.llms.openai import OpenAI
from llama_index import StorageContext, load_index_from_storage

base_path = os.environ.get('OPENAI_API_BASE', 'http://localhost:8080/v1')

# This example uses text-davinci-003 by default; feel free to change if desired
llm_predictor = LLMPredictor(llm=OpenAI(temperature=0, model_name="gpt-3.5-turbo", openai_api_base=base_path))

# Configure prompt parameters and initialise helper
max_input_size = 400
num_output = 400
max_chunk_overlap = 30

prompt_helper = PromptHelper(max_input_size, num_output, max_chunk_overlap)

# Load documents from the 'data' directory
documents = SimpleDirectoryReader('data').load_data()
service_context = ServiceContext.from_defaults(llm_predictor=llm_predictor, prompt_helper=prompt_helper, chunk_size_limit = 400)
index = GPTVectorStoreIndex.from_documents(documents, service_context=service_context)
index.storage_context.persist(persist_dir="./storage")


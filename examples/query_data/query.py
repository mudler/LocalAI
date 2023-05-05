import os

# Uncomment to specify your OpenAI API key here (local testing only, not in production!), or add corresponding environment variable (recommended)
# os.environ['OPENAI_API_KEY']= ""

from llama_index import   LLMPredictor, PromptHelper, ServiceContext
from langchain.llms.openai import OpenAI
from llama_index import StorageContext, load_index_from_storage


# This example uses text-davinci-003 by default; feel free to change if desired
llm_predictor = LLMPredictor(llm=OpenAI(temperature=0, model_name="gpt-3.5-turbo",openai_api_base="http://localhost:8080/v1"))

# Configure prompt parameters and initialise helper
max_input_size = 1024
num_output = 256
max_chunk_overlap = 20

prompt_helper = PromptHelper(max_input_size, num_output, max_chunk_overlap)

# Load documents from the 'data' directory
service_context = ServiceContext.from_defaults(llm_predictor=llm_predictor, prompt_helper=prompt_helper)

# rebuild storage context
storage_context = StorageContext.from_defaults(persist_dir='./storage')

# load index
index = load_index_from_storage(storage_context,     service_context=service_context,    )

query_engine = index.as_query_engine()
response = query_engine.query("XXXXXX your question here XXXXX")
print(response)
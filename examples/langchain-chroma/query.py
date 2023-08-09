
import os
from langchain.vectorstores import Chroma
from langchain.embeddings import OpenAIEmbeddings
from langchain.chat_models import ChatOpenAI
from langchain.chains import RetrievalQA
from langchain.vectorstores.base import VectorStoreRetriever

base_path = os.environ.get('OPENAI_API_BASE', 'http://localhost:8080/v1')

# Load and process the text
embedding = OpenAIEmbeddings(model="text-embedding-ada-002", openai_api_base=base_path)
persist_directory = 'db'

# Now we can load the persisted database from disk, and use it as normal. 
llm = ChatOpenAI(temperature=0, model_name="gpt-3.5-turbo", openai_api_base=base_path)
vectordb = Chroma(persist_directory=persist_directory, embedding_function=embedding)
retriever = VectorStoreRetriever(vectorstore=vectordb)
qa = RetrievalQA.from_llm(llm=llm, retriever=retriever)

query = "What the president said about taxes ?"
print(qa.run(query))

